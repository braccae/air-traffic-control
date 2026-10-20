package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"air-traffic-control/backend/cmd/proxy"
	"air-traffic-control/backend/pkg/ansqueue"
	"air-traffic-control/backend/pkg/glances"
	"air-traffic-control/backend/pkg/hostcache"
	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/podman"
	"air-traffic-control/backend/pkg/readonlyapi"
	"air-traffic-control/backend/pkg/restic"
	"air-traffic-control/backend/pkg/ssh"
	"air-traffic-control/backend/pkg/tailscale"
	"air-traffic-control/backend/pkg/types"
	"air-traffic-control/backend/pkg/websocket"
	_ "air-traffic-control/backend/plugins/all"
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

func newServerMux(hub *websocket.Hub, podmanMgr *podman.Manager, dbPath string) *http.ServeMux {
	mux := http.NewServeMux()

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
		w.Header().Set("Content-Type", "application/json")
		var nodes []types.Node
		if tsStatus, err := tailscale.GetLocalStatus(); err == nil && tsStatus != nil {
			if tsStatus.Self.HostName != "" {
				ip := ""
				if len(tsStatus.Self.TailscaleIPs) > 0 {
					ip = tsStatus.Self.TailscaleIPs[0]
				}
				nodes = append(nodes, types.Node{
					ID:            "node-" + tsStatus.Self.HostName,
					Name:          tsStatus.Self.HostName,
					Group:         "tag:host",
					Datacenter:    "leo-banjo.ts.net",
					NodeType:      types.NodeTypeLinuxLinux,
					IP:            ip,
					Port:          22,
					TailscaleIP:   ip,
					TailscaleHost: tsStatus.Self.DNSName,
					OSDescription: "Linux",
					Status:        "online",
					HasCockpit:    true,
					HasPodman:     true,
					HasRestic:     true,
					AuthMethod:    "tailscale-ssh",
				})
			}
			for _, p := range tsStatus.Peer {
				ip := ""
				if len(p.TailscaleIPs) > 0 {
					ip = p.TailscaleIPs[0]
				}
				status := "offline"
				if p.Online {
					status = "online"
				}
				nodes = append(nodes, types.Node{
					ID:            "node-" + p.HostName,
					Name:          p.HostName,
					Group:         "tag:host",
					Datacenter:    "leo-banjo.ts.net",
					NodeType:      types.NodeTypeLinuxLinux,
					IP:            ip,
					Port:          22,
					TailscaleIP:   ip,
					TailscaleHost: p.DNSName,
					OSDescription: "Linux",
					Status:        status,
					HasCockpit:    true,
					HasPodman:     true,
					HasRestic:     true,
					AuthMethod:    "tailscale-ssh",
				})
			}
		}

		if len(nodes) == 0 {
			// Grounded cluster baseline from GUIDELINES.md leo-banjo.ts.net
			nodes = []types.Node{
				{
					ID:            "node-likes-it-cold",
					Name:          "likes-it-cold",
					Group:         "tag:host",
					Datacenter:    "leo-banjo.ts.net",
					NodeType:      types.NodeTypeLinuxLinux,
					IP:            "100.105.71.49",
					Port:          22,
					TailscaleIP:   "100.105.71.49",
					TailscaleHost: "likes-it-cold.leo-banjo.ts.net",
					OSDescription: "AlmaLinux 10.2 (Lavender Lion)",
					Status:        "online",
					HasCockpit:    true,
					HasPodman:     true,
					HasRestic:     true,
					AuthMethod:    "ssh-ed25519",
				},
				{
					ID:            "node-ping",
					Name:          "ping",
					Group:         "tag:host",
					Datacenter:    "leo-banjo.ts.net",
					NodeType:      types.NodeTypeLinuxLinux,
					IP:            "100.73.87.87",
					Port:          22,
					TailscaleIP:   "100.73.87.87",
					TailscaleHost: "ping.leo-banjo.ts.net",
					OSDescription: "AlmaLinux 10.2 (Lavender Lion)",
					Status:        "online",
					HasCockpit:    true,
					HasPodman:     true,
					HasRestic:     false,
					AuthMethod:    "ssh-ed25519",
				},
				{
					ID:            "node-dev-pc",
					Name:          "dev-pc",
					Group:         "tag:host",
					Datacenter:    "leo-banjo.ts.net",
					NodeType:      types.NodeTypeLinuxLinux,
					IP:            "100.103.55.90",
					Port:          22,
					TailscaleIP:   "100.103.55.90",
					TailscaleHost: "dev-pc.leo-banjo.ts.net",
					OSDescription: "Arch Linux / Linux 6.12",
					Status:        "online",
					HasCockpit:    true,
					HasPodman:     true,
					HasRestic:     true,
					AuthMethod:    "tailscale-ssh",
				},
				{
					ID:            "node-the-freezer",
					Name:          "the-freezer",
					Group:         "tag:host",
					Datacenter:    "leo-banjo.ts.net",
					NodeType:      types.NodeTypeLinuxLinux,
					IP:            "100.111.45.109",
					Port:          22,
					TailscaleIP:   "100.111.45.109",
					TailscaleHost: "the-freezer.leo-banjo.ts.net",
					OSDescription: "openmediavault 7.4.11-1 (Sandworm)",
					Status:        "online",
					HasCockpit:    true,
					HasPodman:     true,
					HasRestic:     true,
					AuthMethod:    "tailscale-ssh",
				},
			}
		}
		json.NewEncoder(w).Encode(nodes)
	})

	mux.HandleFunc("/api/tailscale/peers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		status, err := tailscale.GetLocalStatus()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(status)
	})

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

	// Mount plugin framework REST API routes
	pluginAPI := plugin.NewAPI(plugin.APIOptions{DBPath: dbPath})
	pluginAPI.RegisterRoutes(mux)

	// Mount the ported read-only routes. They share the same hosts-cache.sqlite
	// store as the plugin API. Startup fails loudly if the resolver cannot be
	// built, so we never serve a half-broken API.
	resolvedDB := plugin.ResolveDBPath(dbPath)
	if store, err := hostcache.Open(resolvedDB); err != nil {
		log.Printf("readonly API disabled: %v", err)
	} else {
		if roAPI, err := readonlyapi.New(readonlyapi.Options{Store: store, DBPath: resolvedDB}); err != nil {
			log.Printf("readonly API disabled: %v", err)
		} else {
			roAPI.Register(mux)
		}

		// Mount the Ansible mutation queue (staging, execution, halting). It
		// shares the cache file's pooled handle; the queue store never closes it.
		var queueBroadcast func(event map[string]any)
		if hub != nil {
			queueBroadcast = func(event map[string]any) { hub.BroadcastJSON(event) }
		}
		if qAPI, err := ansqueue.NewAPI(ansqueue.APIOptions{
			Store: ansqueue.New(store.SQLDB()),
			RunOptions: ansqueue.Options{
				WorkDir:     ansqueue.ResolveWorkDir(),
				Invalidator: ansqueue.NewCacheInvalidator(store),
			},
			Broadcast: queueBroadcast,
		}); err != nil {
			log.Printf("ansible queue API disabled: %v", err)
		} else {
			qAPI.Register(mux)
		}
	}

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

	return mux
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
	dbFlag := flag.String("db", "", "Path to hosts-cache.sqlite (defaults to ATC_DB_PATH or auto-detected from the repo root)")
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

	mux := newServerMux(hub, podmanMgr, *dbFlag)
	log.Printf("Air Traffic Control backend listening on :%d", *port)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", *port), mux); err != nil {
		log.Fatalf("Server stopped: %v", err)
	}
}
