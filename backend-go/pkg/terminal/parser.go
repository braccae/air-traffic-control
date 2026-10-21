package terminal

import (
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/nodeapi"
)

// ExecResult represents the result of a terminal execution.
type ExecResult struct {
	Output    string `json:"output"`
	ExitCode  int    `json:"exitCode"`
	Stdout    string `json:"stdout,omitempty"`
	Stderr    string `json:"stderr,omitempty"`
	Error     string `json:"error,omitempty"`
	NodeID    string `json:"nodeId,omitempty"`
	Success   bool   `json:"success"`
	Timestamp int64  `json:"timestamp,omitempty"`
}

// ParseTerminalCommand evaluates fallback/diagnostic commands matching TypeScript orchestrator.ts
func ParseTerminalCommand(cmd string, node *nodeapi.Object) (string, int) {
	trimmed := strings.TrimSpace(cmd)
	if trimmed == "" {
		return "", 0
	}

	parts := strings.Fields(trimmed)
	bin := strings.ToLower(parts[0])

	nodeName := "dev-pc"
	uptime := "1d 22h"
	tailscaleIP := "100.97.159.101"
	tailscaleHost := "dev-pc.leo-banjo.ts.net"
	cpu := "AMD Ryzen 5 5600X 6-Core Processor"

	if node != nil {
		if val, ok := node.GetString("name"); ok && val != "" {
			nodeName = val
		}
		if val, ok := node.GetString("uptime"); ok && val != "" {
			uptime = val
		}
		if val, ok := node.GetString("tailscaleIp"); ok && val != "" {
			tailscaleIP = val
		}
		if val, ok := node.GetString("tailscaleHost"); ok && val != "" {
			tailscaleHost = val
		}
		if rawSpecs, ok := node.Get("specs"); ok && len(rawSpecs) > 0 {
			if specsObj, err := nodeapi.ParseObject(rawSpecs); err == nil {
				if val, ok := specsObj.GetString("cpu"); ok && val != "" {
					cpu = val
				}
			}
		}
	}

	switch bin {
	case "help":
		return strings.Join([]string{
			"Air Traffic Control Integrated Terminal Helper",
			"Supported commands:",
			"  cockpit-bridge   - Query or run Cockpit system bridge session",
			"  podman ps        - List active rootless/system containers",
			"  podman pod ps    - List Podman pods on target",
			"  podman probe     - Probe unprivileged users running rootless Podman containers",
			"  glances          - Display real-time telemetry snapshot",
			"  restic snapshots - List encrypted repository snapshots",
			"  tailscale status - Show Tailscale mesh nodes and IP routing",
			"  uptime           - Show host uptime and load average",
			"  uname -a         - Print kernel and architecture details",
			"  clear            - Clear terminal window",
		}, "\n"), 0

	case "uname":
		var cleanName strings.Builder
		for _, r := range nodeName {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
				cleanName.WriteRune(r)
			}
		}
		return fmt.Sprintf("Linux %s 6.8.4-pve #1 SMP PREEMPT_DYNAMIC x86_64 GNU/Linux", cleanName.String()), 0

	case "uptime":
		return fmt.Sprintf(" 10:28:44 up %s, 3 users, load average: 0.42, 0.58, 0.61", uptime), 0

	case "tailscale":
		if len(parts) > 1 && parts[1] == "status" {
			return strings.Join([]string{
				fmt.Sprintf("%s  %s  linux  -", tailscaleIP, tailscaleHost),
				"100.97.159.102  cold-war.ts.net         linux  active; direct 192.168.1.150:41641; 1.2ms",
				"100.97.159.103  noodle.ts.net           linux  active; direct 192.168.1.160:41641; 0.8ms",
				"100.97.159.120  pong-edge.ts.net        linux  active; relay \"ord\", 14.1ms",
				"Tailscale SSH: active (enforcing strict PAM MFA)",
			}, "\n"), 0
		}
		return "Usage: tailscale status [--json]", 1

	case "podman":
		if len(parts) > 1 && parts[1] == "ps" {
			return strings.Join([]string{
				"CONTAINER ID  USER (UID)       IMAGE                              STATUS        PORTS                   NAMES",
				"d9841af024ea  deployer (1000)  k8s.gcr.io/pause:3.9               Up (healthy)  8096:8096, 443:443      infra-con-media",
				"3f0a9918bc22  deployer (1000)  jellyfin/jellyfin:10.8.13          Up 2 days     8096->8096/tcp          jellyfin-media-server",
				"c810d48a1239  deployer (1000)  caddy:2.7.6-alpine                 Up 2 days     80->80/tcp, 443->443    caddy-gateway",
				"7e4b9019a101  podman (1001)    ollama/ollama:latest               Up (CUDA)     11434->11434/tcp        ollama-vllm-engine",
				"e29d48bc991a  podman (1001)    ghcr.io/open-webui/open-webui:main Up 12 hours   3000->8080/tcp          open-webui",
			}, "\n"), 0
		}
		if len(parts) > 2 && parts[1] == "pod" && parts[2] == "ps" {
			return strings.Join([]string{
				"POD ID        NAME          USER (UID)       STATUS   CREATED       # OF CONTAINERS  INFRA ID",
				"a98c091f09e1  media-stack   deployer (1000)  Running  2 days ago    3                d9841af024ea",
				"bb09817adfe2  ai-inference  podman (1001)    Running  12 hours ago  2                44b910fae120",
			}, "\n"), 0
		}
		if len(parts) > 1 && parts[1] == "probe" {
			return strings.Join([]string{
				"ROOTLESS PODMAN USER PROBE",
				"USER         UID   SUBUID RANGE    LINGER   CONTAINERS  ACTIVE PODS",
				"deployer     1000  100000:65536    yes      3 running   media-stack",
				"podman       1001  165536:65536    yes      2 running   ai-inference",
				"restic-svc   984   231072:65536    no       1 running   standalone (restic-exporter)",
			}, "\n"), 0
		}
		return "Usage: podman [ps|pod ps|probe|logs|restart]", 1

	case "cockpit-bridge":
		return strings.Join([]string{
			"cockpit-bridge: version 312.1",
			"Channel [0] opened: {\"command\":\"init\",\"version\":1}",
			"Authentication: PAM root@pam authorized",
			"Socket /run/cockpit/socket active, D-Bus session bus mapped",
			"Ready for multiplexed WebSocket frames.",
		}, "\n"), 0

	case "restic":
		if len(parts) > 1 && parts[1] == "snapshots" {
			return strings.Join([]string{
				"repository s3://restic-homelab-vault/the-freezer opened (version 2, compression: auto)",
				"ID        Time                 Host                  Tags        Paths",
				"--------------------------------------------------------------------------------",
				"f7a91b2e  2025-05-14 10:04:12  the-freezer.homelab   gold,daily  /etc, /tank0/appdata",
				"09a12c4d  2025-05-13 10:00:00  the-freezer.homelab   gold,daily  /etc, /tank0/appdata",
				"88db91e0  2025-05-12 10:00:00  the-freezer.homelab   gold,daily  /etc, /tank0/appdata",
				"--------------------------------------------------------------------------------",
				"3 snapshots (100% deduplicated, 0 modified chunks)",
			}, "\n"), 0
		}
		return "Usage: restic [snapshots|backup|check]", 1

	case "glances":
		return strings.Join([]string{
			"GLANCES v3.4.0.5 - Telemetry Probe",
			fmt.Sprintf("CPU: %s (16 vCPUs) - 1.4%% usage, 38°C", cpu),
			"MEM: 42.4 / 62.8 GiB (67.5% used, ZFS ARC: 12.3 GiB)",
			"SWAP: 0.0 / 8.0 GiB (0% used)",
			"ZFS POOL tank0: 14.2 / 48.0 TB (29.5% used, RAIDZ2, Scrub healthy)",
			"NETWORK: bond0 Rx: 12.4 MB/s, Tx: 8.9 MB/s (10GbE SFP+)",
			"PROCESSES: 182 total, 1 running, 181 sleeping",
		}, "\n"), 0

	default:
		return fmt.Sprintf("bash: %s: command not found. Type 'help' for available diagnostic commands.", bin), 127
	}
}
