package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os/exec"
	"time"

	"prox-orch/backend/pkg/podman"
	"prox-orch/backend/pkg/ssh"
	"prox-orch/backend/pkg/types"
	"prox-orch/backend/pkg/websocket"
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

func newServerMux(hub *websocket.Hub, podmanMgr *podman.Manager) *http.ServeMux {
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
			"engine":    "golang/1.22",
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
		users := []types.UnprivilegedPodmanUser{
			{
				User:        "deployer",
				UID:         1000,
				GID:         1000,
				HomeDir:     "/home/deployer",
				SubUIDRange: "100000:65536",
				Linger:      true,
				ActiveProcesses: []string{
					"/usr/bin/conmon --api-version 1 -c 3f0a9918bc22 -n jellyfin-media-server",
					"/usr/bin/conmon --api-version 1 -c c810d48a1239 -n caddy-gateway",
				},
				Containers: []types.PodmanContainer{
					{
						ID:       "3f0a9918bc22",
						PodID:    "a98c091f09e1",
						Name:     "jellyfin-media-server",
						Image:    "jellyfin/jellyfin:10.8.13",
						Status:   "Running (VAAPI)",
						CPU:      "2.14%",
						Memory:   "1.68 GiB",
						User:     "deployer",
						UID:      1000,
						Rootless: true,
					},
					{
						ID:       "c810d48a1239",
						PodID:    "a98c091f09e1",
						Name:     "caddy-gateway",
						Image:    "caddy:2.7.6-alpine",
						Status:   "Running",
						CPU:      "0.25%",
						Memory:   "138 MiB",
						User:     "deployer",
						UID:      1000,
						Rootless: true,
					},
				},
				Pods: []types.PodmanPod{
					{
						ID:       "a98c091f09e1",
						Name:     "media-stack",
						Status:   "Running",
						User:     "deployer",
						UID:      1000,
						Rootless: true,
					},
				},
			},
			{
				User:        "podman",
				UID:         1001,
				GID:         1001,
				HomeDir:     "/home/podman",
				SubUIDRange: "165536:65536",
				Linger:      true,
				ActiveProcesses: []string{
					"/usr/bin/conmon --api-version 1 -c 7e4b9019a101 -n ollama-vllm-engine",
					"/usr/bin/conmon --api-version 1 -c e29d48bc991a -n open-webui",
				},
				Containers: []types.PodmanContainer{
					{
						ID:       "7e4b9019a101",
						PodID:    "bb09817adfe2",
						Name:     "ollama-vllm-engine",
						Image:    "ollama/ollama:latest",
						Status:   "Running (CUDA 12.4)",
						CPU:      "3.8%",
						Memory:   "6.4 GiB VRAM",
						User:     "podman",
						UID:      1001,
						Rootless: true,
					},
					{
						ID:       "e29d48bc991a",
						PodID:    "bb09817adfe2",
						Name:     "open-webui",
						Image:    "ghcr.io/open-webui/open-webui:main",
						Status:   "Running",
						CPU:      "0.9%",
						Memory:   "512 MiB System",
						User:     "podman",
						UID:      1001,
						Rootless: true,
					},
				},
				Pods: []types.PodmanPod{
					{
						ID:       "bb09817adfe2",
						Name:     "ai-inference",
						Status:   "Running",
						User:     "podman",
						UID:      1001,
						Rootless: true,
					},
				},
			},
			{
				User:        "restic-svc",
				UID:         984,
				GID:         984,
				HomeDir:     "/var/lib/restic",
				SubUIDRange: "231072:65536",
				Linger:      false,
				ActiveProcesses: []string{
					"/usr/bin/conmon --api-version 1 -c 8811aabbccdd -n restic-exporter",
				},
				Containers: []types.PodmanContainer{
					{
						ID:       "8811aabbccdd",
						Name:     "restic-exporter",
						Image:    "ngosang/restic-exporter:latest",
						Status:   "Running",
						CPU:      "0.1%",
						Memory:   "12 MiB",
						User:     "restic-svc",
						UID:      984,
						Rootless: true,
					},
				},
			},
		}
		json.NewEncoder(w).Encode(users)
	})

	mux.HandleFunc("/api/nodes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		nodes := []types.Node{
			{
				ID:            "node-150-openmediavault",
				Name:          "150 (openmediavault)",
				Group:         "cold-war",
				Datacenter:    "homelab-dc1",
				NodeType:      types.NodeTypeLinuxLinux,
				IP:            "192.168.1.150",
				Port:          22,
				TailscaleIP:   "100.97.159.101",
				TailscaleHost: "the-freezer.ts.net",
				OSDescription: "AlmaLinux 10.2 / Debian 12",
				Status:        "online",
				Uptime:        "16h 44m",
				HasGlances:    true,
				HasCockpit:    true,
				HasPodman:     true,
				HasRestic:     true,
				AuthMethod:    "ssh-ed25519",
			},
			{
				ID:            "node-153-llama-inference-box",
				Name:          "153 (llama-inference)",
				Group:         "cold-war",
				Datacenter:    "homelab-dc1",
				NodeType:      types.NodeTypeLinuxLinux,
				IP:            "192.168.1.153",
				Port:          22,
				TailscaleIP:   "100.97.159.104",
				TailscaleHost: "inference-node.ts.net",
				OSDescription: "Ubuntu 24.04 LTS (NVIDIA Driver 550.54)",
				Status:        "online",
				Uptime:        "4d 12h",
				HasGlances:    true,
				HasCockpit:    true,
				HasPodman:     true,
				HasRestic:     true,
				AuthMethod:    "ssh-ed25519",
			},
			{
				ID:            "node-202-win-render-srv",
				Name:          "202 (win-render-srv)",
				Group:         "cold-war",
				Datacenter:    "homelab-dc1",
				NodeType:      types.NodeTypeWindows,
				IP:            "192.168.1.202",
				Port:          22,
				TailscaleIP:   "100.97.159.112",
				TailscaleHost: "win-render.ts.net",
				OSDescription: "Windows Server 2022 Datacenter",
				Status:        "stopped",
				Uptime:        "0m",
				HasGlances:    false,
				HasCockpit:    false,
				HasPodman:     false,
				HasRestic:     true,
				AuthMethod:    "winrm_ssh",
			},
			{
				ID:            "node-pong-edge",
				Name:          "pong (Edge node)",
				Group:         "noodle",
				Datacenter:    "homelab-dc1",
				NodeType:      types.NodeTypeRouterSwitch,
				IP:            "192.168.1.254",
				Port:          22,
				TailscaleIP:   "100.97.159.120",
				TailscaleHost: "pong-gw.ts.net",
				OSDescription: "VyOS 1.5-rolling / EdgeRouter",
				Status:        "online",
				Uptime:        "42d 18h",
				HasGlances:    false,
				HasCockpit:    false,
				HasPodman:     false,
				HasRestic:     false,
				AuthMethod:    "ssh-ed25519",
			},
		}
		json.NewEncoder(w).Encode(nodes)
	})

	return mux
}

func main() {
	port := flag.Int("port", 8080, "HTTP and WebSocket server port")
	flag.Parse()

	log.Printf("Starting PROX-ORCH Go Core Daemon on port %d...", *port)

	sshManager := ssh.NewClientManager()
	runner := &sshWithLocalFallbackRunner{sshMgr: sshManager}
	podmanMgr := podman.NewManager(runner, ssh.Config{Host: "127.0.0.1", Port: 22, User: "root"})
	hub := websocket.NewHub()
	go hub.Run()

	// Seed ticker for live telemetry broadcasts to all connected clients
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			cpuVal := 1.2 + rand.Float64()*1.8
			watts := 42.0 + rand.Float64()*5.0
			fans := 800 + rand.Intn(40)
			temp := 37.5 + rand.Float64()*2.0

			telem := map[string]interface{}{
				"type": "telemetry_tick",
				"data": map[string]interface{}{
					"cpuUsage":     fmt.Sprintf("%.1f%%", cpuVal),
					"cpuTemp":      fmt.Sprintf("%.1f°C", temp),
					"fanSpeed":     fmt.Sprintf("%d RPM", fans),
					"packageWatts": fmt.Sprintf("%.1f W", watts),
					"timestamp":    time.Now().Format(time.RFC3339),
				},
			}
			hub.BroadcastJSON(telem)
		}
	}()

	mux := newServerMux(hub, podmanMgr)
	log.Printf("PROX-ORCH backend listening on :%d", *port)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", *port), mux); err != nil {
		log.Fatalf("Server stopped: %v", err)
	}
}
