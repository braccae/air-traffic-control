# Air Traffic Control Homelab Cluster Guidelines & Topology

This document details the hardware inventory, network architecture, and operational standards for the `leo-banjo.ts.net` homelab mesh cluster.

---

## 1. Network Topology & Tailnet (`leo-banjo.ts.net`)

All communications traverse an encrypted Tailscale WireGuard overlay network.

### Node Tagging Conventions
- `tag:host`: Primary physical and virtual compute hosts capable of container hosting and virtualization.
- `tag:manager`: Orchestration controllers running Air Traffic Control coordinators or Ansible engines.
- `tag:storage`: Dedicated storage and backup repositories (ZFS, NFS, Samba, Restic target).
- `tag:client`: Workstations and operator terminals.
- `tag:exit-node`: Subnet routing and gateway egress nodes.

---

## 2. Cluster Hardware Inventory

| Host Name | Tailscale FQDN | Tailscale IP | Hardware Platform | CPU & Memory | Role & Workloads |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `likes-it-cold` | `likes-it-cold.leo-banjo.ts.net` | `100.105.71.49` | Lenovo ThinkCentre M725s (3141) | AMD Ryzen 5 PRO 2400GE (4c/8t), 16 GiB | AlmaLinux 10.2, Podman unprivileged services (`apps`, `auth`, `db`, `media`, `proxy`) |
| `ping` | `ping.leo-banjo.ts.net` | `100.73.87.87` | Dell OptiPlex 7040 (0Y7WYT) | Intel Core i5-6500 (4c/4t), 8 GiB | Core edge router, rootless `uosserver` pod |
| `dev-pc` | `dev-pc.leo-banjo.ts.net` | `100.126.230.70` | ASUS ROG STRIX B450-F GAMING | AMD Ryzen 7 3700X (8c/16t), 64 GiB | Primary development workstation & orchestration coordinator |
| `the-freezer` | `the-freezer.leo-banjo.ts.net` | `100.119.231.11` | ASUS TUF GAMING B550-PLUS | AMD Ryzen 9 5900X (12c/24t), 64 GiB | High-density ZFS storage tank (`zpool`), Restic vault repository |
| `fred` | `fred.leo-banjo.ts.net` | `100.64.0.12` | Custom Mini-ITX | AMD Ryzen 5 3600 (6c/12t), 32 GiB | Compute node & secondary KVM host |
| `chuck` | `chuck.leo-banjo.ts.net` | `100.64.0.13` | Intel NUC 11 Pro | Intel Core i7-1165G7 (4c/8t), 32 GiB | Microservices & ingress load balancer |
| `larry` | `larry.leo-banjo.ts.net` | `100.64.0.14` | Intel NUC 10 | Intel Core i5-10210U (4c/8t), 16 GiB | Monitoring, Prometheus, Grafana |
| `orangepizero3`| `orangepizero3.leo-banjo.ts.net` | `100.64.0.15` | Orange Pi Zero 3 | Allwinner H618 (Quad-core ARM), 4 GiB | Out-of-band sentinel & environmental monitor |

---

## 3. SSH & Authentication Standards

- Default system user: `core` with passwordless sudo or unprivileged group membership.
- Authentication: Public key only. Keys are forwarded via local agent:
  ```bash
  export SSH_AUTH_SOCK="/run/user/$(id -u)/ssh-agent.socket"
  ```
- Command execution timeout: 8,000ms. Keep interactive shell timeouts configured for PTY streaming.

---

## 4. Port Allocations & Services

- `22`: SSH Daemon (Strict HostKey checking via accept-new).
- `3000`: Air Traffic Control Fullstack Express coordinator & Vite frontend.
- `18090`: High-performance Go daemon (`air-traffic-control-daemon`).
- `9090`: Cockpit Web Console (`cockpit-bridge`).
- `61208`: Glances system telemetry REST API.
- `2049` / `445`: NFS v4 and Samba SMB storage protocols.
