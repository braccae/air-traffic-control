# Agent & Contributor Guidelines for Air Traffic Control

Welcome to **Air Traffic Control** (`air-traffic-control`), the multi-cockpit homelab and datacenter orchestrator. This document sets the engineering standards, architecture constraints, and behavioral policies for AI agents and human contributors working on this codebase.

---

## 1. Core Architecture & Philosophy

The system runs a **dual-stack architecture**:
1. **Frontend / Coordination Layer:**
   - React 19, Vite, Tailwind CSS v4, `ghostty-web` (libghostty WASM terminal).
   - Express coordinator (`server.ts`) providing WebSocket hubs, REST endpoints, SQLite persistence, and SSH multiplexing.
2. **High-Performance Host Daemon (`backend-go`):**
   - Compiled Go binary (`air-traffic-control-daemon`) running locally or on target nodes.
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
  - **Testing vs Production Invariant:** `ssh-agent forwarding` is strictly for local testing and developer workstations. In production environments, credentials must be secured via keyless PAM (`tailscale-ssh`) or Tiered Ansible Vault encrypted credentials.
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

---

## 5. Ansible Mutation Queue & Execution Invariants

All host state mutations (network interface changes, firewall rules, package upgrades, container lifecycle events, storage volumes, and system settings) are orchestrated through declarative Ansible tasks via the **On-the-Fly Playbook Queue**.

- **Queue Staging & Persistence:**
  - Mutation actions stage declarative steps into SQLite (`ansible_queue_tasks` in `data/hosts-cache.sqlite`).
  - Tasks carry target node ID, target host route, execution priority, and structured Ansible steps.
  - The UI dynamically displays the staged count in the Console `Task Queue (N)` tab and top-level navigation badges.
- **One-by-One Execution with Checkpoint Halting:**
  - Queue execution runs strictly sequentially (`POST /api/queue/execute`).
  - **Checkpoint Halting:** If any task or step fails during playbook execution, the runner immediately halts queue processing, marks the task as `failed`, records the error payload, and stops subsequent queued tasks from running to prevent compounding host misconfigurations.
- **Structured JSON Callback Requirement (`ANSIBLE_STDOUT_CALLBACK=json`):**
  - All automated runner invocations MUST export `ANSIBLE_STDOUT_CALLBACK=json` (or set `stdout_callback = json` in `ansible.cfg`).
  - This guarantees parseable JSON task results for real-time frontend streaming and accordion task drill-down.
- **Native Module Prioritization & Galaxy Collection Guard:**
  - **Native Builtins First:** Always prioritize standard Ansible core modules (`ansible.builtin.service`, `ansible.builtin.systemd`, `ansible.builtin.package`, `ansible.builtin.copy`, `ansible.posix.firewalld`, etc.) for idempotency and rollback safety.
  - **Galaxy Collection Prompt Guard:** When a community or external collection is required (e.g., `community.general.*`, `cisco.ios.*`), probe collection availability. If missing, prompt the operator with an installation guard via `POST /api/ansible/collections/install` before running.
  - **Shell / Command Last Resort:** `ansible.builtin.shell` or `ansible.builtin.command` must strictly remain an explicitly documented last resort when no declarative Ansible module exists.
- **Secret Protection Invariant:**
  - Sensitive values (passwords, auth tokens, private keys) MUST never be shown or logged in plain text.
  - Secret fields must always be masked in UI summaries, logs, and task payloads as `•••••••• (vault: <var_name>)` and referenced via Ansible Vault or secure extra vars.

---

## 6. Pluggable Go Plugin Framework & Extensibility

All host capabilities, storage systems, container engines, package managers, network interfaces, and connection transports are structured as compile-time Go plugins registered in `backend-go/pkg/plugin`. The Go daemon owns all plugin definitions, capability probes, telemetry readers, and Ansible task compilation templates. The TypeScript coordinator delegates discovery and task compilation directly to the Go daemon over HTTP.

### Compile-Time Plugin Registry (`backend-go/pkg/plugin`)
Plugins are registered via Go `init()` functions and imported into the daemon via blank imports in `backend-go/plugins/all/all.go`. No dynamic `.so` or runtime WASM binaries are used.
- **Plugin Interfaces:**
  - `Plugin`: Defines metadata (`Name`, `Kind`, `Description`, `Version`), requirements, and `Detect(ctx, conn, facts)` probe.
  - `Reader`: Reads real-time telemetry or state data over the connection (`Read(ctx, conn, facts)`).
  - `Actor`: Compiles declarative Ansible tasks for mutations (`CompileTask(action, params)`).
  - `Runner`: Optional direct non-Ansible execution for non-destructive operations.
- **Plugin Kinds:**
  - `connection`: SSH, Tailscale (`tsnet`), Local loopback (`connection/ssh`, `connection/tailscale`, `connection/local`).
  - `elevation`: `run0`, `sudo`, `doas`, `none`.
  - `storage`: OpenZFS (`zpool`, `zfs`), Btrfs (`btrfs`), LVM/ext4/XFS (`findmnt`).
  - `container`: Podman (rootful and rootless user sessions), Docker daemon.
  - `package`: Image/immutable managers (`bootc`, `rpm-ostree`), traditional package managers (`dnf`, `apt`, `apk`).
  - `shares`: NFS exports (`nfs`), Samba shares (`samba`).
  - `backup`: Restic (`restic`).
  - `telemetry`: Glances HTTP/SSH (`glances`), `/proc` + `/sys` fallback (`proc`).
  - `system`: Systemd units and services (`systemd`), network interfaces and links (`network`).

### Connection Layer & Ansible Proxy Bridge
All target nodes remain agentless. Communication is handled through pure-Go transports:
- **Transports (`backend-go/plugins/connection/*`):**
  - `connection/ssh`: Pure Go `golang.org/x/crypto/ssh` client pool with agent forwarding (`SSH_AUTH_SOCK`).
  - `connection/tailscale`: Embedded `tailscale.com/tsnet` server providing direct encrypted Tailscale mesh connectivity without relying on host-level tailscaled.
  - `connection/local`: Loopback executor for controller localhost operations.
- **Ansible Proxy Bridge Subcommand:**
  - Running `air-traffic-control-daemon proxy <host> <port>` establishes a stdio-to-target stream through the daemon's connection pool.
  - Ansible uses this via `ssh_common_args = -o ProxyCommand="air-traffic-control-daemon proxy %h %p"` in `ansible.cfg`, eliminating external OpenSSH client dependency quirks and seamlessly bridging Ansible through tsnet or custom tunnels.

### Dynamic Inner Sidebar & Host Modules View
The user interface dynamically adapts to each host's probed capabilities:
- **Dynamic Inner Sidebar (`CockpitSubSidebar`):**
  - Tabs are dynamically registered based on detected plugins returned by `GET /api/nodes/:id/plugins`.
  - If a capability is not detected on a host (e.g. no ZFS or no Podman), its tab is automatically hidden from the navigation sub-sidebar to eliminate clutter.
  - Core views (`Terminal`, `Modules`, and default system views) remain readily accessible.
- **Host Modules View (`HostModulesView`):**
  - Accessible via the **Modules** tab at the bottom of the node sub-sidebar.
  - Groups plugins into **Active** (probed and working), **Available** (installed or installable on the host), and **Unsupported** (missing OS or architecture prerequisites).
  - Provides one-click installation actions that stage declarative Ansible tasks into the mutation queue.
  - Allows manual connection, elevation, and package plugin overrides per host via the Edit Node modal.

### Standard for Adding a New Plugin
Adding a new capability to Air Traffic Control requires three simple steps:
1. **Implement the Go Plugin Package:** Create `backend-go/plugins/<kind>/<name>/<name>.go` implementing `plugin.Plugin` (and optionally `plugin.Reader` or `plugin.Actor`) and register it in `init()` via `plugin.Register(...)`.
2. **Import the Plugin:** Add a single blank import line in `backend-go/plugins/all/all.go` (`_ "air-traffic-control/plugins/<kind>/<name>"`).
3. **Optional Custom View:** If the plugin requires a dedicated UI beyond the standard telemetry card or existing view, add a view component in `src/plugins/views/index.ts`.

---

## 7. Conservative Telemetry Engine (30–60s Cadence)

To maintain minimal footprint on managed nodes and homelab network links, telemetry polling follows a conservative strategy:
- **Conservative Polling Interval:** Polling cadence is set to 30–60 seconds (default 45s), minimizing CPU wakeups and SSH session overhead.
- **Multi-Tier Degradation Hierarchy:**
  1. **Tier 1 (Direct Glances HTTP REST):** Fast REST API query (`http://<host>:61208/api/4/...`) with 2.5s timeout.
  2. **Tier 2 (SSH Localhost Curl):** Queries Glances via SSH loopback when the HTTP port is not exposed on external interfaces.
  3. **Tier 3 (Glances CLI):** Executes `glances --stdout-json cpu,mem,load,sensors` over SSH when the Glances daemon is not running.
  4. **Tier 4 (proc/sysfs Fallback):** Reads `/proc/stat`, `/proc/meminfo`, and `/sys/class/thermal` directly via lightweight SSH command.
- **Strict Zero-Mock Invariant:** If all 4 tiers fail, the node must be flagged as offline or unmonitored; under no circumstances should synthetic metrics or randomized jitter be returned.

---

## 8. Automated uv Bootstrapper & Persistent Venv Manager

Air Traffic Control encapsulates all Ansible and WinRM Python dependencies in an isolated virtualenv to guarantee reproducible, zero-system-pollution execution:
- **Persistent Virtualenv Directory:** `data/ansible-venv` (persisted across restarts; not committed to git).
- **Standalone `uv` Bootstrapper:** Downloads or utilizes standalone binary cached at `data/bin/uv` (falling back to system `uv` or `python3 -m venv`).
- **Core Dependencies:** Pre-installs `ansible-core`, `pywinrm[kerberos]`, `requests-credssp`, and `pydantic`.
- **Auto-Healing Initialization:** On daemon startup, `ensureAnsibleVenv()` verifies the virtualenv and auto-heals any missing packages or corrupted environments before servicing API or playbook requests.

---

## 9. Multi-Pathway Node Onboarding & Tiered Vault Hierarchy

The **Add Node Modal** provides four distinct onboarding pathways tailored for modern hybrid infrastructures:
1. **Tailscale Mesh Manual Selection:** Direct peer selection from live `tailscale status --json`. Leverages keyless PAM `tailscale-ssh` with zero password/key management.
2. **Manual SSH with Ansible Vault Encryption:** Direct SSH connections to physical/virtual Linux hosts using password or private key. Sensitive values are immediately encrypted into `$ANSIBLE_VAULT;1.1;AES256` blocks.
3. **WinRM Connections for Windows Hosts:** Windows server management over WinRM (port 5985 HTTP / 5986 HTTPS, NTLM/Kerberos), with encrypted administrator credentials.
4. **Following a Tailscale Tag (Dynamic Inventory Chunk):** Creates a `TagInventoryChunk` that dynamically synchronizes all nodes matching a Tailscale tag (e.g. `tag:worker`), applying shared credentials (defaulting to keyless `tailscale-ssh` if left blank).

### Tiered Vault Password Resolution Hierarchy
When encrypting or decrypting secrets, Air Traffic Control resolves the master vault password in strict precedence:
1. **`vault_password_file` (File-First):** Path specified in `ansible.cfg` or options.
2. **Environment Variables:** `ANSIBLE_VAULT_PASSWORD` (inline string) or `ANSIBLE_VAULT_PASSWORD_FILE` (file path).
3. **Interactive Prompt Fallback:** Operator `ask_password` input modal when no automated source is configured.

---

## 10. Dynamic Tag Inventory Chunks & Hierarchical Inventory Architecture

The inventory sidebar (`ServerViewSidebar`) displays nodes in a clean, multi-level hierarchy mirroring the operator's mental model:
- **Tailscale Mesh Sub-tree:** Grouped under the tailnet shortname (e.g. `leo-banjo.ts.net`), broken down into tag chunks (`tag:host`, `tag:worker`, etc.).
- **Unassigned / Standalone Mesh Nodes:** Nodes on the tailnet without specific chunk tags.
- **Non-Tailscale Nodes:** Direct IP/LAN hosts displayed at root or inside operator-managed folders.
- **Custom Folders:** Collapsible folders (`CustomFolder`) created and managed dynamically in SQLite via `POST /api/inventory/folders` and `DELETE /api/inventory/folders/:id`.

```
Inventory
  <ts-shortname> (e.g. leo-banjo.ts.net)
    <tag1> (e.g. tag:host)
      <host 1>
      <host 2>
    <tag2> (e.g. tag:worker)
      <host 3>
      <host 4>
  (non ts-host)
  <non-ts-host 2>
  <Folder/> (e.g. Cold Storage)
    <non ts-host 3>
```

---

## 11. Verification Commands

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
