# PROX-ORCH High-Performance Go Core Backend

This directory contains the production-grade Golang backend daemon for **PROX-ORCH**.

## Architecture Highlights
- **SSH Connection Pool (`pkg/ssh`)**:
  - Secure SSH client management supporting ED25519, RSA, and Ansible Vault encrypted keys.
  - Native support for **Linux** (bash/cockpit-bridge/systemd), **Windows** (OpenSSH & PowerShell commands), and **Network Switches/Routers** (Cisco IOS, VyOS, MikroTik CLI modes).
  - Web-ready interactive pseudo-terminals (PTY) bridged directly to WebSockets.
- **Cockpit-Bridge Integration (`pkg/cockpit`)**:
  - Connects to `cockpit-bridge` via SSH stdio/JSON-RPC framing to read `journald` and `dmesg` logs securely without exposing raw ports.
  - Controls systemd services, mounts, and network interfaces.
- **Glances Telemetry (`pkg/glances`)**:
  - Auto-discovers and queries Glances REST API (port 61208 or SSH forwarded) for live CPU, memory, ZFS ARC, temperatures, and fan speeds.
- **Restic Backup Backend (`pkg/restic`)**:
  - Manages AES-256 encrypted immutable backups to S3, MinIO, or local ZFS targets with deduplication statistics and integrity verification.
- **Podman Remote Daemon (`pkg/podman`)**:
  - Directly controls rootless and system Podman pods and containers.
- **Tailscale Mesh (`pkg/tailscale`)**:
  - Reads node routing, exit node advertisement, and peer-to-peer latencies via Tailscale local socket.
- **Real-Time WebSocket Hub (`pkg/websocket`)**:
  - Low-latency sub-millisecond broadcast hub pushing metrics and task execution streams.

## Compiling and Running
```bash
cd backend-go
go mod download
go build -o prox-orch-daemon main.go
./prox-orch-daemon --port 8080
```
