package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"time"

	"prox-orch/backend/pkg/ssh"
	"prox-orch/backend/pkg/types"
	"prox-orch/backend/pkg/websocket"
)

func main() {
	port := flag.Int("port", 8080, "HTTP and WebSocket server port")
	flag.Parse()

	log.Printf("Starting PROX-ORCH Go Core Daemon on port %d...", *port)

	sshManager := ssh.NewClientManager()
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

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		hub.ServeWS(w, r)
	})

	http.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "healthy",
			"version":   "v8.2-ansible",
			"engine":    "golang/1.22",
			"connected": true,
		})
	})

	http.HandleFunc("/api/nodes", func(w http.ResponseWriter, r *http.Request) {
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

	_ = sshManager
	log.Printf("PROX-ORCH backend listening on :%d", *port)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", *port), nil); err != nil {
		log.Fatalf("Server stopped: %v", err)
	}
}
