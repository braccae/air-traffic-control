# Air Traffic Control (ATC): Multi-Cockpit Homelab & Datacenter Orchestrator

[![TypeScript](https://img.shields.io/badge/TypeScript-5.8-blue.svg)](https://www.typescriptlang.org/)
[![React](https://img.shields.io/badge/React-19.0-61dafb.svg)](https://react.dev/)
[![TailwindCSS](https://img.shields.io/badge/Tailwind-4.0-38b2ac.svg)](https://tailwindcss.com/)
[![Go](https://img.shields.io/badge/Go-1.22+-00add8.svg)](https://golang.org/)
[![Tailscale](https://img.shields.io/badge/Tailscale-Mesh-blueviolet.svg)](https://tailscale.com/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

**Air Traffic Control** (`air-traffic-control`) is a modern, high-density homelab orchestration platform and Network Operations Center (NOC) interface. Born from an aviation theme as a unified dashboard to oversee, monitor, and coordinate multiple Linux Cockpit instances across distributed infrastructure—*Air Traffic Control managing a fleet of Cockpits*—it unifies rootless Podman management, Cockpit bridges, ZFS storage arrays, KVM virtual machines, SMART disk telemetry, and interactive terminals into a single pane of glass.

---

## Key Features

- **Libghostty Terminal Integration:** Hardware-accelerated WASM terminal powered by `ghostty-web` with floating, draggable, resizable, minimizable windows and multi-tab session management.
- **Deep Tailscale Telemetry:** Real-time visibility into all 47+ mesh peers with DERP relay vs Direct WireGuard indicators, transfer throughput (`RxBytes`, `TxBytes`), and subnet routes.
- **Rootless Podman Orchestration:** Automatic probing across unprivileged service users (`apps`, `auth`, `core`, `db`, `media`, `proxy`) via `/run/user/<uid>/podman/podman.sock`.
- **Zero-Mock Policy:** All hardware metrics, firewall rules, network interfaces, and storage shares are queried directly from live hosts via SSH, Glances, Cockpit, and systemd.
- **High-Performance Go Daemon:** Optional `air-traffic-control-daemon` compiled in Go 1.22+ for sub-millisecond host telemetry and rootless container inspection.
- **Production Ansible Integration:** Live playbook execution, dynamic inventory generation from Tailscale tags, dry-run (`--check`) verification, and encrypted group variables.

---

## Architecture Overview

```
 ┌────────────────────────────────────────────────────────┐
 │            Web Browser / NOC Console                   │
 │   React 19 + Tailwind CSS v4 + Libghostty (WASM)       │
 └─────────────┬───────────────────────────▲──────────────┘
               │ HTTP REST                 │ WebSocket (PTY & Telemetry)
               ▼                           │
 ┌─────────────────────────────────────────┴──────────────┐
 │    Express Coordinator (server.ts / Vite Dev Server)   │
 │   - SQLite3 Cache (WAL mode, TTL invalidation)         │
 │   - Dynamic Ansible Runner & Dynamic Inventory         │
 └─────────────┬───────────────────────────┬──────────────┘
               │ Non-Interactive SSH       │ Unix / HTTP
               ▼                           ▼
 ┌───────────────────────────┐  ┌─────────────────────────┐
 │   Target Cluster Hosts    │  │  High-Perf Go Daemon    │
 │   (`likes-it-cold`, etc.) │  │  (`air-traffic-control- │
 │  - Rootless Podman        │  │   daemon`)              │
 │  - ZFS & SMART lsblk      │  │  - Cockpit Bridge D-Bus │
 │  - Firewalld & virsh KVM  │  │  - Glances / PCP client │
 └───────────────────────────┘  └─────────────────────────┘
```

---

## Quick Start

### Prerequisites
- Node.js 20+ and npm
- Go 1.22+ (for compiling the Go daemon)
- Tailscale installed and connected
- SSH agent running (`ssh-add -l`)

### Installation & Development
```bash
# Clone the repository
git clone https://github.com/your-org/air-traffic-control.git
cd air-traffic-control

# Install dependencies
npm install

# Build the Go host daemon
cd backend-go && go build -o /tmp/air-traffic-control-daemon main.go && cd ..

# Launch development environment (Frontend + Express coordinator)
npm run dev
```

Visit `http://localhost:3000` in your browser.

---

## Verification & Testing

```bash
# Run all Go unit tests
cd backend-go && go test -v ./... && cd ..

# Run all frontend Vitest tests
npm test

# Type check
npm run lint

# Production build
npm run build
```

---

## Documentation

- [AGENTS.md](AGENTS.md): Coding rules, Zero-Mock policy, and pair-programming guidelines.
- [ARCHITECTURE.md](ARCHITECTURE.md): Architectural design, Ansible mutation queue, and telemetry hierarchy.
- [GUIDELINES.md](GUIDELINES.md): Tailscale cluster topology, host inventory, and port map.
