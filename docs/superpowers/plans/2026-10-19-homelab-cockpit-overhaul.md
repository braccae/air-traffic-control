# PROX-ORCH Homelab Cockpit 100-Task Master Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Transform the PROX-ORCH Cockpit into a production-grade, zero-mock, enterprise-ready homelab orchestration platform with real hardware probing, interactive floating/draggable/minimizable libghostty terminals, deep Tailscale mesh telemetry (47 peers), live Ansible orchestration with streaming logs, and hardened Go daemon and SQLite persistence.

**Architecture:** Dual-stack architecture: React 19 + Tailwind v4 + libghostty frontend communicating over WebSocket and REST with an Express/TypeScript coordinator and a high-performance Go daemon (`backend-go`), backed by SQLite in WAL mode with real SSH/Cockpit/Glances telemetry and Zero Mock fallbacks.

**Tech Stack:** TypeScript, React 19, Tailwind CSS v4, libghostty (WASM), Vite, Express, Go 1.22+, SQLite3, Tailscale CLI, Ansible 2.18+, Restic.

## Global Constraints
- **Zero-Mock Policy:** Never use fake `setTimeout` state toggles, fabricated temperature/CPU numbers, synthetic random jitter loops, or mock container arrays in production routes or views.
- **Documentation Integrity:** Preserve all existing docstrings, tests, and comments. Maintain 100% pass rate on `go test ./...` and `npm test`.
- **Live Cluster Safety:** Use non-destructive commands (`tailscale status --json`, `smartctl`, `lsblk`, `ip -j`, `systemctl is-active`, `ansible-playbook --check`) unless explicitly performing an administrative action.

---

## Category A: Architecture, Guidelines & Developer Tooling (Tasks 1–12)

- [ ] **Task 1: Create `AGENTS.md` - Agentic & Pair Programming Guidelines**
  - Files: `AGENTS.md`
  - Interfaces: Documents dual-stack model, SSH agent forwarding conventions, Zero-Mock policy, and testing standards.
  - Verification: `test -f AGENTS.md && grep -q "Zero-Mock" AGENTS.md`

- [ ] **Task 2: Create `GUIDELINES.md` - Cluster Topology & Operational Standards**
  - Files: `GUIDELINES.md`
  - Interfaces: Documents `leo-banjo.ts.net` topology, node tagging (`tag:host`, `tag:manager`), core user privileges, and port allocations.
  - Verification: `test -f GUIDELINES.md && grep -q "leo-banjo.ts.net" GUIDELINES.md`

- [ ] **Task 3: Overhaul `README.md`**
  - Files: `README.md`
  - Interfaces: Replace placeholder AI Studio text with comprehensive architecture diagram, prerequisites, dev environment setup, and API reference.
  - Verification: `grep -q "PROX-ORCH" README.md`

- [ ] **Task 4: Create Project `Makefile`**
  - Files: `Makefile`
  - Targets: `dev`, `build`, `test`, `go-build`, `go-test`, `lint`, `clean`, `inspect`.
  - Verification: `make test && make lint`

- [ ] **Task 5: Create Dynamic Ansible Inventory Script (`inventory/tailscale_inventory.py`)**
  - Files: `inventory/tailscale_inventory.py`
  - Interfaces: Runs `tailscale status --json` or hits `/api/tailscale/peers`, parses nodes, groups by tags (`tag:host`), outputs Ansible dynamic inventory JSON.
  - Verification: `python3 inventory/tailscale_inventory.py --list | jq ._meta.hostvars`

- [ ] **Task 6: Create Production Ansible Playbooks Directory & Sample Playbooks**
  - Files: `ansible/playbooks/site.yml`, `ansible/playbooks/security-hardening.yml`, `ansible/playbooks/podman-update.yml`, `ansible/playbooks/tailscale-status.yml`
  - Interfaces: Standard idempotent playbooks compatible with `ansible-playbook --check`.
  - Verification: `ansible-playbook -i inventory/tailscale_inventory.py ansible/playbooks/tailscale-status.yml --syntax-check`

- [ ] **Task 7: Create Dynamic Ansible Syntax Checker Script (`scripts/ansible-check.sh`)**
  - Files: `scripts/ansible-check.sh`
  - Interfaces: Validates syntax and linting of all playbooks in `ansible/playbooks/`.
  - Verification: `bash scripts/ansible-check.sh`

- [ ] **Task 8: Create Cluster Node Diagnostic Script (`scripts/cluster-doctor.sh`)**
  - Files: `scripts/cluster-doctor.sh`
  - Interfaces: Checks SSH agent, Tailscale connection, Go daemon health, and SQLite DB integrity.
  - Verification: `bash scripts/cluster-doctor.sh`

- [ ] **Task 9: Create SQLite Cache Pruning Utility (`scripts/prune-cache.ts`)**
  - Files: `scripts/prune-cache.ts`
  - Interfaces: Deletes stale `host_section_cache` and `host_telemetry_cache` records older than TTL.
  - Verification: `npx tsx scripts/prune-cache.ts`

- [ ] **Task 10: Create Systemd Service Template (`backend-go/prox-orch-daemon.service`)**
  - Files: `backend-go/prox-orch-daemon.service`
  - Interfaces: Unit file to run `prox-orch-daemon` as a user or system service with appropriate capabilities.
  - Verification: `test -f backend-go/prox-orch-daemon.service`

- [ ] **Task 11: Create Automated Dynamic Host Discovery Script (`scripts/sync-hosts.ts`)**
  - Files: `scripts/sync-hosts.ts`
  - Interfaces: Queries `tailscale status --json` and syncs live nodes into `hosts-cache.sqlite`.
  - Verification: `npx tsx scripts/sync-hosts.ts`

- [ ] **Task 12: Add Git Pre-Commit Hook / Verification Script (`scripts/pre-commit.sh`)**
  - Files: `scripts/pre-commit.sh`
  - Interfaces: Validates TypeScript compilation and Go test pass before commits.
  - Verification: `bash scripts/pre-commit.sh`

---

## Category B: Ghostty Terminal Overhaul (Draggable, Resizable, Minimizable, Multi-Tab) (Tasks 13–27)

- [ ] **Task 13: Implement Draggable Hook (`src/hooks/useDraggable.ts`)**
  - Interfaces: Tracks `(x, y)` position with pointer capture, handles boundaries and dragging handle.
  - Tests: Unit tests in `src/hooks/useDraggable.test.ts`.

- [ ] **Task 14: Implement Resizable Hook (`src/hooks/useResizable.ts`)**
  - Interfaces: Tracks `(width, height)` with 8-direction handles (`n`, `s`, `e`, `w`, `ne`, `nw`, `se`, `sw`) and min/max constraints.
  - Tests: Unit tests in `src/hooks/useResizable.test.ts`.

- [ ] **Task 15: Create Terminal Session Manager (`src/utils/terminalSessionManager.ts`)**
  - Interfaces: Manages state for multiple sessions: `sessions: TerminalSession[]`, `activeSessionId`, `addSession`, `closeSession`, `minimizeSession`, `maximizeSession`.
  - Tests: Unit tests in `src/utils/terminalSessionManager.test.ts`.

- [ ] **Task 16: Create Minimized Terminal Dock Bar (`src/components/TerminalDockBar.tsx`)**
  - Interfaces: Renders bottom taskbar chips for minimized terminals with status badge, host name, and restore click.

- [ ] **Task 17: Create Multi-Tab Header Bar (`src/components/TerminalTabBar.tsx`)**
  - Interfaces: Tab list with active highlight, title, host indicator, close tab button, and new tab (+) button.

- [ ] **Task 18: Add Quick Command Palette to Terminal Header**
  - Interfaces: Quick shortcut buttons (`htop`, `podman ps`, `journalctl -f -n 50`, `ip a`, `uname -a`) that inject commands into the active PTY.

- [ ] **Task 19: Add Window Control Actions (Minimize, Maximize, Close)**
  - Interfaces: Header buttons `_` (minimize to dock), `□` / `❐` (maximize/restore), `✕` (close session).

- [ ] **Task 20: Add Host Switcher / Picker in New Tab Creation**
  - Interfaces: Dropdown inside terminal allowing opening a new tab connected to any cluster node.

- [ ] **Task 21: Implement WebSocket PTY Endpoint in `server.ts` (`/ws/terminal`)**
  - Interfaces: Streams real bidirectional PTY data via `node-pty` or Go daemon OpenPTY, handling ANSI escape sequences and resize payloads (`{ type: 'resize', cols, rows }`).

- [ ] **Task 22: Update `src/utils/ghosttyTerminal.ts` for Bidirectional PTY Streaming**
  - Interfaces: Wire Ghostty VT canvas to WebSocket stream with resize handler (`window.addEventListener('resize')` / `fitAddon`).

- [ ] **Task 23: Refactor `src/components/modals/TerminalModal.tsx` into Floating Window**
  - Interfaces: Convert static modal into floating, draggable, resizable window component with dock integration.

- [ ] **Task 24: Persist Window Positions & Sizes in `localStorage`**
  - Interfaces: Remembers user's preferred terminal window size and position across page reloads.

- [ ] **Task 25: Add Keyboard Shortcuts for Terminal Management**
  - Interfaces: `Ctrl+Shift+T` toggle terminal, `Escape` minimize, `Ctrl+W` close tab, `Ctrl+Shift+F` maximize.

- [ ] **Task 26: Add Terminal Clipboard & Selection Helpers**
  - Interfaces: Automatic copy on selection, right-click paste, paste button in terminal toolbar.

- [ ] **Task 27: Add Reconnect Indicator & Auto-Retry on Connection Drop**
  - Interfaces: Visual badge displaying connection status (`Connected`, `Reconnecting...`, `Disconnected`).

---

## Category C: Tailscale Peer Explorer & Deep Telemetry (Tasks 28–42)

- [ ] **Task 28: Implement Backend Endpoint `GET /api/tailscale/peers`**
  - Interfaces: Parses `tailscale status --json` returning full details for all 47 peers (addresses, DERP relay, OS, active, Rx/Tx, exit node).

- [ ] **Task 29: Implement Backend Endpoint `GET /api/nodes/:id/tailscale`**
  - Interfaces: Returns node-specific Tailscale telemetry, allowed IPs, endpoints, and key expiration.

- [ ] **Task 30: Implement Backend Endpoint `POST /api/tailscale/ping`**
  - Interfaces: Executes real `tailscale ping -c 1 <peer>` and returns measured RTT and direct vs relay status.

- [ ] **Task 31: Implement Backend Endpoint `GET /api/tailscale/derp-map`**
  - Interfaces: Returns DERP region information, region latency, and active relays.

- [ ] **Task 32: Remove Peer Truncation Cap (3 Peers) in `TailscaleMeshCard.tsx`**
  - Interfaces: Replace `peers.slice(0, 3)` with full list, pagination, and expand/collapse.

- [ ] **Task 33: Remove Mock Fallback Latencies & Static Dirs in `TailscaleMeshCard.tsx`**
  - Interfaces: Delete `likes-it-cold 1.8ms`, `ping 2.1ms`, `fred 3.4ms` static mock divs; display real live peer metrics.

- [ ] **Task 34: Remove Hardcoded SSH Agent Socket Path in `TailscaleMeshCard.tsx`**
  - Interfaces: Read actual `SSH_AUTH_SOCK` from node metadata or probe response.

- [ ] **Task 35: Create Dedicated `TailscaleExplorerView.tsx` Component**
  - Interfaces: Full NOC-style view with search, filter by tag (`tag:host`, `tag:client`, `tag:mobile`), and sort by status.

- [ ] **Task 36: Add WireGuard Direct UDP vs DERP Relay Visual Indicators**
  - Interfaces: Visual badge showing green direct WireGuard vs amber DERP relay with region name (`DERP-1 (NYC)`).

- [ ] **Task 37: Add Real Transfer Stats Display (`RxBytes`, `TxBytes`)**
  - Interfaces: Format bytes into human-readable throughput (`1.2 GiB received`, `450 MiB sent`).

- [ ] **Task 38: Add Subnet Routes Display (`AllowedIPs`)**
  - Interfaces: Show advertised routes (e.g., `192.168.8.0/24`) with copy-to-clipboard button.

- [ ] **Task 39: Add WireGuard Handshake & Last Seen Timestamps**
  - Interfaces: Real-time relative timestamp ("2s ago", "Active now").

- [ ] **Task 40: Add Node Key Expiration Warning Badges**
  - Interfaces: Warn if node key expires in < 14 days with expiry date.

- [ ] **Task 41: Add Exit Node Status & Routing Badges**
  - Interfaces: Identify which peers offer exit node routing.

- [ ] **Task 42: Create Interactive Ping Modal / Inline Tool**
  - Interfaces: Clicking "Ping" on any peer triggers real `POST /api/tailscale/ping` and displays output.

---

## Category D: Deceptive Mock Elimination & Live Control Wiring (Tasks 43–67)

- [ ] **Task 43: Add Backend Route `POST /api/nodes/:id/interfaces/:name/toggle`**
  - Interfaces: Executes `ip link set <name> up/down` via SSH.
- [ ] **Task 44: Wire `NetworkInterfacesView.tsx` to Live Interface Toggle**
  - Interfaces: Replace fake 400ms `setTimeout` with API call.
- [ ] **Task 45: Add MTU, Duplex & Speed Metrics in `NetworkInterfacesView.tsx`**
- [ ] **Task 46: Add Backend Route `POST /api/nodes/:id/vms/:name/action`**
  - Interfaces: Executes `virsh start/shutdown/reboot <name>`.
- [ ] **Task 47: Wire `VirtualMachinesKVM.tsx` to Live VM Action Endpoint**
  - Interfaces: Replace local React boolean toggle with live API call.
- [ ] **Task 48: Wire "Console" Button in `VirtualMachinesKVM.tsx` to PTY Session**
- [ ] **Task 49: Display Real VM vCPUs, RAM & Autostart in `VirtualMachinesKVM.tsx`**
- [ ] **Task 50: Remove Synthetic Firewall Rules Injection in `server.ts`**
  - Interfaces: Delete synthetic `fw-ts` and `fw-drop-default` injections.
- [ ] **Task 51: Add Backend Route `POST /api/nodes/:id/firewall/rules`**
  - Interfaces: Executes `firewall-cmd --add-rule` / `nft add rule`.
- [ ] **Task 52: Add Backend Route `DELETE /api/nodes/:id/firewall/rules/:id`**
  - Interfaces: Executes rule removal on target host.
- [ ] **Task 53: Wire `FirewallView.tsx` Add Rule Form to Backend API**
- [ ] **Task 54: Remove Synthesized Disk Temperatures in `server.ts`**
  - Interfaces: Execute `smartctl -A -j` / `nvme smart-log` for genuine sensor readings.
- [ ] **Task 55: Add Backend Route `POST /api/nodes/:id/disks/:name/smart-test`**
  - Interfaces: Triggers `smartctl -t short /dev/<name>`.
- [ ] **Task 56: Remove Fabricated Default Pool in `ZfsManagementView.tsx`**
- [ ] **Task 57: Display Real ARC Cache Hit Rates & Stats in `ZfsManagementView.tsx`**
  - Interfaces: Read from `/proc/spl/kstat/zfs/arcstats`.
- [ ] **Task 58: Wire Scrub Action Button in `ZfsManagementView.tsx`**
  - Interfaces: Triggers `zpool scrub <pool>`.
- [ ] **Task 59: Add Real Share Reload in `NetworkSharesView.tsx`**
  - Interfaces: Executes `smbcontrol all reload-config` / `exportfs -ra`.
- [ ] **Task 60: Dynamically Compute Health Status in `SystemHealthTelemetry.tsx`**
  - Interfaces: Replace static `"HEALTHY"` with dynamic calculation based on CPU, RAM, Disk, and load thresholds.
- [ ] **Task 61: Remove Static `w-[20%]` CPU Bar in `SystemHealthTelemetry.tsx`**
- [ ] **Task 62: Dynamically Compute `X/Y Up` Cluster Counter in `Header.tsx`**
  - Interfaces: Compute count of online vs unreachable nodes.
- [ ] **Task 63: Remove Deceptive `activeCount || 4` in `GlobalTasksExecutionLog.tsx`**
- [ ] **Task 64: Wire Live Ansible Task Logs to `GlobalTasksExecutionLog.tsx`**
- [ ] **Task 65: Wire Real Repository URL and Snapshots in `ResticBackupPolicy.tsx`**
- [ ] **Task 66: Add Backend Route `POST /api/nodes/:id/reboot`**
  - Interfaces: Executes `systemctl reboot` with task tracking.
- [ ] **Task 67: Wire Reboot Confirmation Modal to Live Reboot Endpoint**

---

## Category E: Production Ansible Orchestration & Vault Security (Tasks 68–80)

- [ ] **Task 68: Implement Real Playbook Runner in `server.ts`**
  - Interfaces: Replace fake `setInterval` with `child_process.spawn('ansible-playbook')`.
- [ ] **Task 69: Stream Real Playbook Output via WebSocket (`task_output`)**
- [ ] **Task 70: Implement Playbook Dry-Run (`--check`) Support**
- [ ] **Task 71: Implement Target Host Filtering (`--limit`) Support**
- [ ] **Task 72: Implement Dynamic Playbook Discovery Endpoint (`GET /api/playbooks`)**
- [ ] **Task 73: Implement Syntax Check Endpoint (`POST /api/playbook/syntax-check`)**
- [ ] **Task 74: Implement Dynamic Inventory View Endpoint (`GET /api/ansible/inventory`)**
- [ ] **Task 75: Add Real Vault Encryption in `GroupVarsModal.tsx`**
  - Interfaces: Uses `ansible-vault encrypt_string` instead of plaintext storage.
- [ ] **Task 76: Add Playbook Cancellation Endpoint (`POST /api/playbook/cancel`)**
- [ ] **Task 77: Add Extra-Vars JSON/YAML Input Support in `RunPlaybookModal.tsx`**
- [ ] **Task 78: Add Tag Selection Support (`--tags`, `--skip-tags`) in `RunPlaybookModal.tsx`**
- [ ] **Task 79: Parse Real Playbook Summary (ok, changed, failed) into Task Status**
- [ ] **Task 80: Store All Playbook Execution Logs in SQLite `tasks` Table**

---

## Category F: Backend & Go Daemon Hardening (Tasks 81–92)

- [ ] **Task 81: Fix Restic Plaintext Password Security Vulnerability in `backend-go`**
  - Interfaces: Inject `RESTIC_PASSWORD` via environment variable instead of bash command string interpolation.
- [ ] **Task 82: Remove Hardcoded Restic Mock Data in `backend-go/pkg/restic/backup.go`**
- [ ] **Task 83: Remove Hardcoded Mock Metrics in `backend-go/pkg/glances/client.go`**
- [ ] **Task 84: Clean Up Dead Scanner Code in `backend-go/pkg/cockpit/bridge.go`**
- [ ] **Task 85: Wire `pkg/tailscale`, `pkg/restic`, `pkg/glances` in `backend-go/main.go`**
- [ ] **Task 86: Remove Fictional Cluster Mock Nodes in `backend-go/main.go`**
- [ ] **Task 87: Eliminate Synthetic Random Noise Generator in `server.ts`**
- [ ] **Task 88: Eliminate Synthetic Random Noise Generator in `backend-go/main.go`**
- [ ] **Task 89: Add SQLite Table `tasks` and Wire All Task Updates**
- [ ] **Task 90: Add SQLite Indices on `hosts`, `host_section_cache`, and `host_telemetry_cache`**
- [ ] **Task 91: Enable `PRAGMA synchronous = NORMAL;` in SQLite Initialization**
- [ ] **Task 92: Implement Centralized Cache Invalidation on Host Update/Delete**

---

## Category G: UX, Layout, Accessibility & Polish (Tasks 93–100)

- [ ] **Task 93: Delete Unused Dead Code (`src/components/OperationalTabs.tsx`)**
- [ ] **Task 94: Improve Visual Contrast in Sidebar & Section Panels**
- [ ] **Task 95: Implement Global Keyboard Shortcut `Cmd/Ctrl+K` for Search Modal**
- [ ] **Task 96: Add Real-Time Live Pulse Indicator on Online Nodes**
- [ ] **Task 97: Implement Real Search Across All Cluster Resources in `SearchModal.tsx`**
- [ ] **Task 98: Add Autoscroll and Filter to Container Logs Modal**
- [ ] **Task 99: Add Toast Notification System for Background Operations**
- [ ] **Task 100: End-to-End System Verification (Go Tests, Vitest, TypeScript, Live Smoke Test)**
