# Agent & Contributor Guidelines for PROX-ORCH Cockpit

Welcome to **PROX-ORCH Homelab Cockpit** (`air-traffic-control`). This document sets the engineering standards, architecture constraints, and behavioral policies for AI agents and human contributors working on this codebase.

---

## 1. Core Architecture & Philosophy

The system runs a **dual-stack architecture**:
1. **Frontend / Coordination Layer:**
   - React 19, Vite, Tailwind CSS v4, `ghostty-web` (libghostty WASM terminal).
   - Express coordinator (`server.ts`) providing WebSocket hubs, REST endpoints, SQLite persistence, and SSH multiplexing.
2. **High-Performance Host Daemon (`backend-go`):**
   - Compiled Go binary (`prox-orch-daemon`) running locally or on target nodes.
   - Rootless Podman probing, systemd Cockpit bridge framing, Restic execution, and Glances/PCP metrics collector.
3. **Hardware & Network Fabric:**
   - Tailscale mesh (`leo-banjo.ts.net`) connecting 47+ physical and virtual nodes.
   - Non-interactive SSH with agent forwarding (`SSH_AUTH_SOCK` / `/run/user/<uid>/ssh-agent.socket`) using dedicated unprivileged system users (`core`, `apps`, `auth`, `media`, etc.).

---

## 2. The Zero-Mock Policy (STRICT INVARIANT)

> [!IMPORTANT]
> **NO SIMULATED OR MOCK ACTIONS IN PRODUCTION.**
> Never use `setTimeout` or local React state mutations to fake backend actions. Never return fake hardware strings, synthetic temperatures, or fabricated container lists.

- **Actions must reach the host:** A button that starts a VM, restarts a container, toggles a network link, or runs an Ansible playbook MUST invoke an actual endpoint that executes the command on the real target.
- **Fail Loudly & Accurately:** If a host is unreachable or SSH fails, return an informative error status (`{ success: false, error: "SSH connection to 100.x.y.z:22 refused" }`). Do not disguise failure with synthetic success or mock containers.
- **No Random Telemetry Jitter:** Do not use `Math.random()` or `rand.Float64()` to generate fluctuating CPU or watt numbers. Display real metrics or show an offline / unmonitored indicator.

---

## 3. SSH & Host Communication Standards

- **Universal SSH Transport (Agentless Architecture):**
  - All communication with remote nodes operates strictly over non-interactive SSH with agent forwarding (`SSH_AUTH_SOCK` / `/run/user/<uid>/ssh-agent.socket`).
  - Target nodes require **zero custom agent software** or persistent background daemons installed.
  - Failover routing automatically navigates Tailscale mesh (`100.x.y.z`), LAN IP (`192.168.x.y`), and local loopback.
- **Modular Capability Autodetection Matrix:**
  - **Privilege Escalation:** Nodes dynamically probe elevation capabilities in strict order:
    1. `run0` (preferred for systemd 256+ polkit sessions; preserves DBus and session context).
    2. `sudo -n` (passwordless sudo fallback for standard Linux).
    3. `doas -n` (lightweight elevation fallback for Alpine / BSD / minimal hosts).
    4. `none` (direct execution for `root` or unprivileged user tasks).
  - **No Mandatory `cockpit-bridge` Dependency:** `cockpit-bridge` is an optional framing protocol. If present, it may be utilized; if absent, the backend MUST gracefully fall back to native CLI commands (`journalctl`, `systemctl`, etc.). Under no circumstances may an operation crash or refuse to run due to missing `cockpit-bridge`.
  - **Container Runtimes:** Probe both rootful and rootless Podman across active user sessions (`/run/user/<uid>`), falling back to Docker daemon (`docker ps`).
  - **Storage Engines:** Dynamically probe OpenZFS (`zpool`, `zfs list`), Btrfs (`btrfs filesystem show`), and LVM/ext4/XFS (`findmnt`).
  - **Package Managers:** Autodetect image/immutable systems (`bootc`, `rpm-ostree`) before traditional managers (`dnf`, `apt`, `apk`).
- **Command Execution:** Always invoke `runNodeCommand(node, command)` or the Go `ssh.ClientPool`.
- **SSH Isolation:** Pass `-F /dev/null -o StrictHostKeyChecking=accept-new -o BatchMode=yes` to avoid hanging on host prompts or conflicting with user configs.
- **Rootless Podman:** Access containers through unprivileged user directories (`/run/user/<uid>/podman/podman.sock` or `runuser -u <user> -- podman ps --format json`).

---

## 4. SQLite Persistence & Cache Rules

- Location: `data/hosts-cache.sqlite`.
- Modes: Always run with `PRAGMA journal_mode = WAL;` and `PRAGMA synchronous = NORMAL;`.
- TTLs:
  - Telemetry: 15 seconds.
  - Containers: 20 seconds.
  - Interfaces & Network: 30 seconds.
  - Firewall: 45 seconds.
  - Disks, Storage, Shares, VMs: 60 seconds.
- Invalidation: Any write mutation (`PUT /api/nodes/:id`, `POST /api/nodes/:id/interfaces/...`) MUST invalidate the corresponding host cache keys.

---

## 5. Verification Commands

Before concluding any work, contributors and agents must run:
```bash
# 1. Run all Go tests
cd backend-go && go test -v ./... && cd ..

# 2. Run all frontend Vitest unit tests
npm test

# 3. Verify TypeScript compilation
npm run lint

# 4. Verify Vite production build
npm run build
```
All four checks must pass with zero errors.
