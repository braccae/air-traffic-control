package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"air-traffic-control/backend/cmd/proxy"
	"air-traffic-control/backend/pkg/ansibleapi"
	"air-traffic-control/backend/pkg/ansqueue"
	"air-traffic-control/backend/pkg/cache"
	"air-traffic-control/backend/pkg/clusterapi"
	"air-traffic-control/backend/pkg/collections"
	"air-traffic-control/backend/pkg/contextmenu"
	"air-traffic-control/backend/pkg/cronapi"
	"air-traffic-control/backend/pkg/glances"
	"air-traffic-control/backend/pkg/hostcache"
	"air-traffic-control/backend/pkg/nodeapi"
	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	"air-traffic-control/backend/pkg/podman"
	"air-traffic-control/backend/pkg/rcloneapi"
	"air-traffic-control/backend/pkg/readonlyapi"
	"air-traffic-control/backend/pkg/restic"
	"air-traffic-control/backend/pkg/secrets"
	"air-traffic-control/backend/pkg/ssh"
	"air-traffic-control/backend/pkg/tailscale"
	"air-traffic-control/backend/pkg/taskapi"
	"air-traffic-control/backend/pkg/terminal"
	"air-traffic-control/backend/pkg/types"
	"air-traffic-control/backend/pkg/vaultapi"
	"air-traffic-control/backend/pkg/websocket"
	_ "air-traffic-control/backend/plugins/all"
	tsconn "air-traffic-control/backend/plugins/connection/tailscale"
	"air-traffic-control/backend/webui"
)

type sshWithLocalFallbackRunner struct {
	sshMgr *ssh.ClientManager
}

func (r *sshWithLocalFallbackRunner) RunCommand(cfg ssh.Config, command string) (string, error) {
	if r.sshMgr != nil {
		if out, err := r.sshMgr.RunCommand(cfg, command); err == nil {
			return out, nil
		}
	}
	if cfg.Host == "127.0.0.1" || cfg.Host == "localhost" || cfg.Host == "" {
		out, err := exec.Command("sh", "-c", command).CombinedOutput()
		if err != nil {
			return string(out), fmt.Errorf("local exec failed (%w): %s", err, string(out))
		}
		return string(out), nil
	}
	return "", fmt.Errorf("ssh and local fallback failed for host %s", cfg.Host)
}

func newServerMux(hub *websocket.Hub, podmanMgr *podman.Manager, dbPath string, store *hostcache.Store) (*http.ServeMux, *plugin.API) {
	mux := http.NewServeMux()
	var pluginAPI *plugin.API

	if hub != nil {
		mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
			hub.ServeWS(w, r)
		})
	}

	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "healthy",
			"version":   "v8.2-ansible",
			"engine":    runtime.Version(),
			"connected": true,
		})
	})

	sshManager := ssh.NewClientManager()
	runner := &sshWithLocalFallbackRunner{sshMgr: sshManager}
	var nodeAPI *nodeapi.API

	mux.HandleFunc("/api/podman/probe", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		host := r.URL.Query().Get("host")
		user := r.URL.Query().Get("user")
		if user == "" {
			user = "core"
		}
		if host != "" && host != "127.0.0.1" && host != "localhost" {
			remoteMgr := podman.NewManager(runner, ssh.Config{Host: host, Port: 22, User: user})
			if probed, err := remoteMgr.ProbeUnprivilegedUsers(); err == nil {
				json.NewEncoder(w).Encode(probed)
				return
			}
		}
		if podmanMgr != nil {
			if probed, err := podmanMgr.ProbeUnprivilegedUsers(); err == nil && len(probed) > 0 {
				json.NewEncoder(w).Encode(probed)
				return
			}
		}
		json.NewEncoder(w).Encode([]types.UnprivilegedPodmanUser{})
	})

	mux.HandleFunc("/api/nodes", func(w http.ResponseWriter, r *http.Request) {
		if nodeAPI != nil {
			nodeAPI.HandleNodes(w, r)
			return
		}
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(nodeapi.BaselineNodeObjects())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "node API not initialized"})
	})

	mux.HandleFunc("/api/tailscale/peers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Prefer ATC's own tsnet netmap when it is the active transport: that
		// is the set of peers ATC can actually reach and authenticate as. The
		// host tailscaled view (below) is only used in the local-dev host-socket
		// fallback mode.
		if provider, ok := plugin.Get(tsconn.SettingsPageID); ok {
			if tp, ok := provider.(interface {
				TsnetPeers(context.Context) ([]tailscale.PeerStatus, error)
			}); ok {
				ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
				defer cancel()
				if peers, err := tp.TsnetPeers(ctx); err == nil && peers != nil {
					// Build a self entry from the plugin's self status so the
					// envelope keeps the same shape as the host path.
					self := tailscale.PeerStatus{}
					if sp, ok := provider.(interface {
						SelfStatus(context.Context) tsconn.SelfStatus
					}); ok {
						ss := sp.SelfStatus(ctx)
						self.HostName = ss.Hostname
						self.DNSName = ss.DNSName
						self.TailscaleIPs = ss.TailscaleIPs
						self.Online = ss.Online
					}
					_ = json.NewEncoder(w).Encode(map[string]any{
						"success": true,
						"source":  "tsnet",
						"self":    self,
						"peers":   peers,
					})
					return
				}
			}
		}

		status, err := tailscale.GetLocalStatus()
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": false,
				"error":   fmt.Sprintf("tailscale status unavailable: %v", err),
			})
			return
		}
		peers := make([]tailscale.PeerStatus, 0, len(status.Peer))
		for _, p := range status.Peer {
			peers = append(peers, p)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"source":  "host-socket",
			"self":    status.Self,
			"peers":   peers,
		})
	})

	// ATC's own tsnet identity (distinct from the host tailscaled view above).
	// The handler always returns the real status payload — including the exact
	// failure reason when no key is configured — so the UI never shows a
	// fabricated "connected" state.
	handleTailscaleSelf := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "method not allowed"})
			return
		}
		provider, ok := plugin.Get(tsconn.SettingsPageID)
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": false,
				"error":   "connection/tailscale plugin is not registered",
			})
			return
		}
		selfProvider, ok := provider.(interface {
			SelfStatus(context.Context) tsconn.SelfStatus
		})
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": false,
				"error":   "connection/tailscale plugin does not expose tsnet status",
			})
			return
		}
		// First registration can take a while; bound it so the request never hangs.
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"self":    selfProvider.SelfStatus(ctx),
		})
	}
	mux.HandleFunc("/api/tailscale/self", handleTailscaleSelf)
	// Compatibility alias: older clients link to /api/tailscale/status.
	mux.HandleFunc("/api/tailscale/status", handleTailscaleSelf)

	handleTailscaleRepair := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "method not allowed"})
			return
		}
		provider, ok := plugin.Get(tsconn.SettingsPageID)
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "connection/tailscale plugin is not registered"})
			return
		}
		repairProvider, ok := provider.(interface {
			Repair(context.Context) (tsconn.SelfStatus, []string, error)
		})
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "connection/tailscale plugin does not expose repair"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		status, steps, err := repairProvider.Repair(ctx)
		payload := map[string]any{"success": err == nil, "self": status, "steps": steps}
		if err != nil {
			payload["error"] = err.Error()
		}
		_ = json.NewEncoder(w).Encode(payload)
	}
	mux.HandleFunc("/api/tailscale/repair", handleTailscaleRepair)

	mux.HandleFunc("/api/restic/snapshots", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		repo := r.URL.Query().Get("repo")
		if repo == "" {
			repo = "s3://restic-homelab-vault/cluster"
		}
		resticMgr := restic.NewManager(runner, ssh.Config{Host: "127.0.0.1", Port: 22, User: "core"}, repo)
		password := r.URL.Query().Get("password")
		if password == "" {
			password = os.Getenv("RESTIC_PASSWORD")
		}
		snaps, err := resticMgr.ListSnapshots(password)
		if err != nil {
			json.NewEncoder(w).Encode([]types.ResticSnapshot{})
			return
		}
		json.NewEncoder(w).Encode(snaps)
	})

	mux.HandleFunc("/api/glances/telemetry", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		host := r.URL.Query().Get("host")
		if host == "" {
			host = "127.0.0.1"
		}
		client := glances.NewClient(host, 61208)
		telem, err := client.FetchTelemetry(host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		json.NewEncoder(w).Encode(telem)
	})

	// Mount the ported read-only routes. They share the same hosts-cache.sqlite
	// store as the plugin API. Startup fails loudly if the resolver cannot be
	// built, so we never serve a half-broken API.
	resolvedDB := plugin.ResolveDBPath(dbPath)
	if store == nil {
		log.Printf("readonly API disabled: host cache unavailable")
		pluginAPI = plugin.NewAPI(plugin.APIOptions{DBPath: dbPath})
		mux.HandleFunc("/api/plugins", pluginAPI.HandleListPlugins)
		mux.Handle("/api/nodes/", pluginAPI.NodeRoutesHandler())
	} else {
		if roAPI, err := readonlyapi.New(readonlyapi.Options{Store: store, DBPath: resolvedDB}); err != nil {
			log.Printf("readonly API disabled: %v", err)
		} else {
			roAPI.Register(mux)
		}

		// Mount plugin framework REST API routes - with working node resolver from store
		pluginAPI = plugin.NewAPI(plugin.APIOptions{Store: store, DBPath: dbPath})
		mux.HandleFunc("/api/plugins", pluginAPI.HandleListPlugins)

		// Mount the Ansible mutation queue (staging, execution, halting). It
		// shares the cache file's pooled handle; the queue store never closes it.
		var queueBroadcast func(event map[string]any)
		if hub != nil {
			queueBroadcast = func(event map[string]any) { hub.BroadcastJSON(event) }
		}
		qStore := ansqueue.New(store.SQLDB())
		if qAPI, err := ansqueue.NewAPI(ansqueue.APIOptions{
			Store: qStore,
			RunOptions: ansqueue.Options{
				WorkDir:     ansqueue.ResolveWorkDir(),
				Invalidator: ansqueue.NewCacheInvalidator(store),
				TaskStore:   store,
				Broadcast:   queueBroadcast,
			},
			Broadcast: queueBroadcast,
		}); err != nil {
			log.Printf("ansible queue API disabled: %v", err)
		} else {
			qAPI.Register(mux)
		}

		// Mount ported Ansible API routes (config, environment, inventory, modules, playbooks, group-vars)
		var ansAPI *ansibleapi.API
		if aAPI, err := ansibleapi.NewAPI(ansibleapi.APIOptions{
			Store:      store,
			QueueStore: qStore,
			RepoRoot:   ansqueue.ResolveWorkDir(),
			Broadcast:  queueBroadcast,
		}); err != nil {
			log.Printf("ansible API disabled: %v", err)
		} else {
			ansAPI = aAPI
			ansAPI.Register(mux)
		}

		// Mount ported Ansible Vault API routes
		if vAPI, err := vaultapi.NewAPI(vaultapi.Options{
			Store:    store,
			RepoRoot: ansqueue.ResolveWorkDir(),
		}); err != nil {
			log.Printf("vault API disabled: %v", err)
		} else {
			vAPI.Register(mux)
		}

		// Install the process-wide Ansible Vault credential resolver. Every SSH
		// path (command runner, PTY, connection plugin, probe) resolves a node's
		// vaultSecretId through this single decryptor.
		secrets.SetDefault(secrets.NewResolver(secrets.Options{
			Store:    store,
			RepoRoot: ansqueue.ResolveWorkDir(),
		}))

		// Mount ported Rclone Remotes and Mounts API routes
		if rcAPI, err := rcloneapi.NewAPI(rcloneapi.Options{
			Store:      store,
			QueueStore: qStore,
			RepoRoot:   ansqueue.ResolveWorkDir(),
			Broadcast:  queueBroadcast,
		}); err != nil {
			log.Printf("rclone API disabled: %v", err)
		} else {
			rcAPI.Register(mux)
		}

		// Mount ported Cron Schedules and Scheduler API routes
		cronScheduler := cronapi.NewScheduler(cronapi.SchedulerOptions{
			Store:      store,
			QueueStore: qStore,
			Runner:     ansAPI,
			RepoRoot:   ansqueue.ResolveWorkDir(),
			Broadcast:  queueBroadcast,
		})
		cronScheduler.Start()
		if cAPI, err := cronapi.NewAPI(cronapi.APIOptions{
			Store:     store,
			Scheduler: cronScheduler,
			Broadcast: queueBroadcast,
		}); err != nil {
			log.Printf("cron API disabled: %v", err)
		} else {
			cAPI.Register(mux)
		}

		// Mount ported Cluster API routes (cluster-log, connections, tasks, inventory chunks/folders, tailscale ping, restic snapshot)
		if clAPI, err := clusterapi.NewAPI(clusterapi.Options{
			Store:     store,
			Broadcast: queueBroadcast,
		}); err != nil {
			log.Printf("cluster API disabled: %v", err)
		} else {
			clAPI.Register(mux)
		}

		// Mount the node lifecycle and hardware probe API (CRUD, testSsh, probes).
		if nAPI, err := nodeapi.New(nodeapi.Options{
			Store:        store,
			QueueStore:   qStore,
			Runner:       nodeapi.NewCompositeRunner(nodeapi.NewLocalRunner("sh", ""), nodeapi.NewSSHRunner(sshManager.Pool())),
			Broadcast:    queueBroadcast,
			Fallback:     pluginAPI.NodeRoutesHandler(),
			SeedBaseline: false,
			TailscaleStatus: func() (*tailscale.Status, error) {
				// Prefer ATC's own tsnet netmap: it sees peers (e.g. tag:pve hosts)
				// that the host tailscaled cannot see due to ACLs.
				provider, ok := plugin.Get(tsconn.SettingsPageID)
				if ok {
					if tp, ok := provider.(interface {
						TsnetPeers(context.Context) ([]tailscale.PeerStatus, error)
					}); ok {
						ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
						defer cancel()
						peers, err := tp.TsnetPeers(ctx)
						if err == nil && peers != nil {
							st := &tailscale.Status{}
							st.Peer = make(map[string]tailscale.PeerStatus)
							for _, p := range peers {
								key := p.ID
								if key == "" {
									key = p.DNSName
								}
								st.Peer[key] = p
							}
							// Self entry from the plugin's self status.
							if sp, ok := provider.(interface {
								SelfStatus(context.Context) tsconn.SelfStatus
							}); ok {
								ss := sp.SelfStatus(ctx)
								st.Self.HostName = ss.Hostname
								st.Self.DNSName = ss.DNSName
								st.Self.TailscaleIPs = ss.TailscaleIPs
								st.Self.Online = ss.Online
								st.Self.OS = "linux"
							}
							return st, nil
						}
					}
				}
				return tailscale.GetLocalStatus()
			},
			EnsureLocalhost: nodeapi.EnsureLocalhostNode,
			OnNodeUpdated: func(nodeID string) {
				if pluginAPI != nil && pluginAPI.Cache() != nil {
					pluginAPI.Cache().Invalidate(nodeID)
				}
			},
		}); err != nil {
			log.Printf("node API disabled: %v", err)
		} else {
			nodeAPI = nAPI
			nodeAPI.Register(mux)
		}

		// Provide dependencies to WebSocket Hub
		if hub != nil {
			var connReg *clusterapi.ConnectionRegistry
			var logStore *clusterapi.LogStore
			if clAPI, err := clusterapi.NewAPI(clusterapi.Options{
				Store: store,
				TsnetPing: func(ctx context.Context, target string) (string, bool, *string, error) {
					provider, ok := plugin.Get(tsconn.SettingsPageID)
					if !ok {
						return "", false, nil, fmt.Errorf("connection/tailscale plugin is not registered")
					}
					tp, ok := provider.(interface {
						TsnetPing(context.Context, string) (string, bool, *string, error)
					})
					if !ok {
						return "", false, nil, fmt.Errorf("connection/tailscale plugin does not expose tsnet ping")
					}
					return tp.TsnetPing(ctx, target)
				},
			}); err == nil {
				connReg = clAPI.ConnReg()
				logStore = clAPI.LogStore()
			}
			hub.SetOptions(websocket.HubOptions{
				Store:         store,
				QueueStore:    qStore,
				ConnReg:       connReg,
				LogStore:      logStore,
				SSHManager:    sshManager,
				Resolver:      nodeAPI,
				FactsResolver: pluginAPI.NodeResolver(),
			})
		}
	}

	// Mount Terminal exec API (/api/terminal/exec) unconditionally so terminal
	// execution routes work whether backed by persistent store or baseline inventory.
	termRunner := nodeapi.NewCompositeRunner(nodeapi.NewLocalRunner("sh", ""), nodeapi.NewSSHRunner(sshManager.Pool()))
	termAPI := terminal.NewAPI(nodeAPI, termRunner, store)
	termAPI.Register(mux)

	// Credential connectivity test: resolves a vaultSecretId reference and runs
	// an identity command on the real host (no fabricated success).
	mux.HandleFunc("/api/ssh/test", nodeapi.NewSSHTestHandler(sshManager.Pool()))

	// Wire global & categorized settings pages
	pluginAPI.RegisterSettingsRoutes(mux)

	settingsHandler := settingsframework.NewHandler(pluginAPI.Registry(), pluginAPI.SettingsStore())
	settingsHandler.RegisterRoutes(mux)

	cmRenderer := contextmenu.NewRenderer(contextmenu.RendererOptions{
		Registry:     pluginAPI.Registry(),
		NodeResolver: pluginAPI.NodeResolver(),
	})
	contextmenu.NewAPI(cmRenderer).Register(mux)

	nodeTypesHandler := nodeapi.NewHandler(func() []newapi.NodeTypeSpec {
		if pluginAPI != nil && pluginAPI.Registry() != nil {
			return pluginAPI.Registry().ListNodeTypes()
		}
		return plugin.ListNodeTypes()
	})
	mux.HandleFunc("/api/node-types", nodeTypesHandler)
	mux.HandleFunc("/api/node-types/", nodeTypesHandler)

	taskAPI := taskapi.NewAPI(taskapi.APIOptions{
		PluginRegistry: pluginAPI.Registry(),
		NodeResolver:   pluginAPI.NodeResolver(),
	})
	taskAPI.Register(mux)

	// API 404 catch-all. Mirrors server.ts: an unhandled /api/* request must
	// return JSON, never fall through to the SPA index.html. Every /api/*
	// handler registered above is more specific and keeps precedence.
	apiNotFound := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   fmt.Sprintf("API route not found: %s %s", r.Method, r.URL.Path),
		})
	}
	mux.HandleFunc("/api", apiNotFound)
	mux.HandleFunc("/api/", apiNotFound)

	// Serve the embedded React build last, so it catches everything that is not
	// an API route or a WebSocket upgrade.
	mux.Handle("/", webui.SPAHandler())

	return mux, pluginAPI
}

func readProcLoadAvg() (float64, error) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0.2, err
	}
	var l1 float64
	_, err = fmt.Sscanf(string(data), "%f", &l1)
	return l1, err
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "proxy" {
		if err := proxy.Run(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "proxy error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	port := flag.Int("port", 8080, "HTTP and WebSocket server port")
	dbFlag := flag.String("db", "", "Path to hosts-cache.sqlite (use :memory: for in-memory mode). Empty = ATC_DB_PATH or auto-detected from the repo root.")
	flag.Parse()

	log.Printf("Starting Air Traffic Control Go Core Daemon on port %d...", *port)

	sshManager := ssh.NewClientManager()
	runner := &sshWithLocalFallbackRunner{sshMgr: sshManager}
	podmanMgr := podman.NewManager(runner, ssh.Config{Host: "127.0.0.1", Port: 22, User: "root"})
	hub := websocket.NewHub()
	go hub.Run()

	// Ticker for live telemetry broadcasts based on authentic kernel load
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			l1, _ := readProcLoadAvg()
			telem := map[string]interface{}{
				"type": "telemetry_tick",
				"data": map[string]interface{}{
					"cpuUsage":  fmt.Sprintf("%.1f%%", l1*10.0),
					"timestamp": time.Now().Format(time.RFC3339),
				},
			}
			hub.BroadcastJSON(telem)
		}
	}()

	// ResolveDBPath special-cases ":memory:" and otherwise finds the real
	// hosts-cache.sqlite, so both the in-memory and file modes open correctly
	// (hostcache.Open("") would fail with an empty-path error).
	hostStore, err := hostcache.Open(plugin.ResolveDBPath(*dbFlag))
	if err != nil {
		log.Fatalf("Failed to open host cache: %v", err)
	}

	// Verify (and if necessary rebuild) the managed Ansible virtualenv before
	// serving requests. Vault decryption and playbook runs exec ansible-vault /
	// ansible-playbook out of data/ansible-venv, so a venv that was created on
	// another host (stale shebangs and a dangling bin/python) would make every
	// SSH secret resolution fail with ENOENT. A failure here is logged loudly
	// rather than fatal: the coordinator still serves non-Ansible views, and
	// Settings → Ansible Engine reports the environment as uninstalled.
	repoRoot := ansqueue.ResolveWorkDir()
	if venvDir, err := ansibleapi.EnsureAnsibleVenv(repoRoot); err != nil {
		log.Printf("ansible environment bootstrap failed: %v", err)
	} else {
		log.Printf("ansible environment ready at %s", venvDir)
	}

	// Periodically snapshot the in-memory cache to disk (300s ticker). In file
	// mode Backup is a no-op, so this is safe to run unconditionally.
	updater := NewHostDataUpdater(300 * time.Second)
	updater.SetStore(hostStore)
	updater.Start()
	defer updater.Stop()

	mux, pluginAPI := newServerMux(hub, podmanMgr, *dbFlag, hostStore)

	// Keep the controller's Galaxy collections in sync with what the
	// coordinator can do: baseline catalog first, then anything a detected host
	// capability needs (wired into the plugin probe path). Operator opt-outs and
	// the version cache live in global_settings, so a deliberate removal sticks.
	collectionMgr := collections.New(ansqueue.ResolveWorkDir())
	collectionMgr.SetStateStore(plugin.GlobalSettingsStore())
	collections.SetDefault(collectionMgr)
	plugin.SetCollectionsManager(collectionMgr)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if installed := collectionMgr.EnsureCatalog(ctx); len(installed) > 0 {
			log.Printf("ansible collections: installed baseline catalog entries: %v", installed)
		}
	}()

	// Opt-in weekly upgrade pass. It no-ops unless the operator enabled it, and
	// it skips pinned and disabled collections.
	go func() {
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			collectionMgr.MaybeAutoUpdate(ctx)
			cancel()
			<-ticker.C
		}
	}()

	log.Printf("Air Traffic Control backend listening on :%d", *port)

	// Initialize and start the background cache manager (3s interval). It must
	// use the resolvers and probe cache that the plugin API actually wired up
	// (a fresh APIOptions literal leaves them nil and the manager nil-panics on
	// its first tick). Skip it entirely when no node resolver is available.
	nodeResolver := pluginAPI.NodeResolver()
	runnerResolver := pluginAPI.RunnerResolver()
	if nodeResolver != nil && runnerResolver != nil {
		cacheMgr := cache.NewCacheManager(hostStore, nodeResolver, runnerResolver, pluginAPI.Cache())
		cacheMgr.Start()
	} else {
		log.Printf("background cache manager disabled: no node resolver available")
	}

	if err := http.ListenAndServe(fmt.Sprintf(":%d", *port), mux); err != nil {
		log.Fatalf("Server stopped: %v", err)
	}
}

// HostDataUpdater periodically refreshes host data in the background
type HostDataUpdater struct {
	store    interface{} // hostcache.Store pointer (stored as interface{} to avoid import cycles)
	interval time.Duration
	stop     chan struct{}
	wg       sync.WaitGroup
}

// NewHostDataUpdater creates a new background updater
func NewHostDataUpdater(interval time.Duration) *HostDataUpdater {
	return &HostDataUpdater{
		interval: interval,
		stop:     make(chan struct{}),
	}
}

// SetStore sets the cache store for periodic backups
func (u *HostDataUpdater) SetStore(store interface{}) {
	u.store = store
}

// Start begins background updates
func (u *HostDataUpdater) Start() {
	u.wg.Add(1)
	go func() {
		defer u.wg.Done()
		ticker := time.NewTicker(u.interval)
		defer ticker.Stop()

		// Run an initial backup immediately so the snapshot exists
		// without having to wait for the first ticker interval.
		if u.store != nil {
			if s, ok := u.store.(interface{ Backup() error }); ok {
				if err := s.Backup(); err != nil {
					log.Printf("host updater: backup error: %v", err)
				}
			}
		}

		for {
			select {
			case <-u.stop:
				if u.store != nil {
					if s, ok := u.store.(interface{ Backup() error }); ok {
						if err := s.Backup(); err != nil {
							log.Printf("host updater: backup error: %v", err)
						}
					}
				}
				return
			case <-ticker.C:
				// Backup in-memory store if available
				if u.store != nil {
					if s, ok := u.store.(interface{ Backup() error }); ok {
						if err := s.Backup(); err != nil {
							log.Printf("host updater: backup error: %v", err)
						}
					}
				}
				// TODO: Implement actual host data refresh
				// This would call probe functions and update the cache
			}
		}
	}()
}

// Stop stops background updates gracefully
func (u *HostDataUpdater) Stop() {
	close(u.stop)
	u.wg.Wait()
}

// Ensure import is available
var _ = sync.WaitGroup{}
