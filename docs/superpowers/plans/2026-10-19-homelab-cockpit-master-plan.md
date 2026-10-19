# Homelab Cockpit Master Plan: Unified Workspace, Systems Management & UX Architecture

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Transform the cockpit into a unified, consistent homelab command center: integrate minimized terminals and all modals into the bottom drawer with persistent PTY sessions; streamline node adding and VM creation with auto-probing; structure container hierarchy by Pod and User with Docker detection; provide visual editors for SMB/NFS shares, users, and passwords; expand firewall port ranges and network interface setup; prefer systemd `run0` for elevation; add a global Datacenter sidebar view; implement Catppuccin Macchiato and Latte themes with system auto-detection; add multi-distro package update management; and cleanly route ZFS vs Btrfs storage architectures.

**Architecture:**
- **Frontend Layer:** React 19 + TypeScript + Tailwind CSS v4. Centralized `windowManager` managing minimized window pills inside `GlobalTasksExecutionLog`.
- **Coordination & Host Backend:** Express coordinator (`server.ts`) with persistent WebSocket PTY mux, SQLite WAL cache (`data/hosts-cache.sqlite`), and Go daemon (`backend-go`).
- **Systems & Privilege Model:** Systemd `run0` privileged execution with graceful fallback to `sudo -n`.
- **Themes:** Catppuccin Macchiato (dark) and Catppuccin Latte (light) with `prefers-color-scheme` system default.

**Tech Stack:** React 19, TypeScript, Tailwind CSS v4, Express, SQLite, Go, OpenZFS, Btrfs, Podman, Docker, systemd/run0, Samba, NFS.

---

## Global Constraints
- **Zero-Mock Policy:** All actions, shares, users, containers, package updates, and storage pools must probe and execute against real hosts or real local files. No random noise or synthetic entries.
- **Clickable Markdown Links:** All file paths and symbol references must use clickable markdown links (`file:///...`).
- **Preserve Documentation:** Maintain all existing comments and docstrings.
- **Verification Gates:** Before completing any task, all 4 gates must pass with 0 errors:
  1. `cd backend-go && go test -v ./... && cd ..`
  2. `npm test`
  3. `npm run lint` (`tsc --noEmit`)
  4. `npm run build`

---

## Tasks

### Task 1: Unified Window Manager & Integrated Bottom Panel
**Files:**
- Create: `src/utils/windowManager.ts`
- Create: `src/utils/windowManager.test.ts`
- Modify: `src/components/GlobalTasksExecutionLog.tsx`
- Modify: `src/components/modals/TerminalModal.tsx`
- Modify: `src/App.tsx`
- Delete: `src/components/TerminalDockBar.tsx`

**Interfaces:**
- `WindowManager`: Tracks windows/modals (`id`, `title`, `icon`, `type: 'terminal' | 'modal'`, `isMinimized`, `isOpen`, `meta`).
- `GlobalTasksExecutionLog`: Integrated bottom drawer with minimized window pills bar, active task tabs, and live streaming output.
- WebSocket PTY connections in `TerminalModal` stay connected while minimized.

- [ ] **Step 1: Write unit tests in `src/utils/windowManager.test.ts`**
  Verify registering, minimizing, restoring, and closing windows with persistent metadata.
- [ ] **Step 2: Implement `src/utils/windowManager.ts`**
  State manager supporting arbitrary modals and terminal sessions.
- [ ] **Step 3: Update `GlobalTasksExecutionLog.tsx`**
  Embed the minimized windows dock directly into the top bar of the bottom panel.
- [ ] **Step 4: Update `TerminalModal.tsx` and `App.tsx`**
  Keep terminal WebSocket connection alive in background when minimized; remove standalone `TerminalDockBar.tsx`.
- [ ] **Step 5: Run tests and verify**
  `npm test`, `npm run lint`, `npm run build`.

---

### Task 2: Streamlined AddNodeModal with Auto-Probe & Dedicated CreateVmModal
**Files:**
- Modify: `src/components/modals/AddNodeModal.tsx`
- Create: `src/components/modals/CreateVmModal.tsx`
- Modify: `src/components/VirtualMachinesKVM.tsx`
- Modify: `src/App.tsx`
- Modify: `server.ts`

**Interfaces:**
- `AddNodeModal`: Only asks for Host Identifier/Tailscale Peer, Group, SSH User, Port, and Auth Method. Minimizable to bottom panel.
- Backend `/api/nodes/probe-host`: Automatically probes CPU model, vCPUs, RAM, OS description, motherboard, and BIOS via SSH.
- `CreateVmModal`: Dedicated modal for VM Name, Target Node, OS/ISO, vCPUs, RAM, Disk Size, and Network Bridge. Minimizable to bottom panel.

- [ ] **Step 1: Write unit tests for hardware probe extraction**
- [ ] **Step 2: Streamline `AddNodeModal.tsx`**
  Remove manual hardware entry; trigger auto-probe on submission; add minimize button.
- [ ] **Step 3: Build `CreateVmModal.tsx`**
  Add dedicated VM creation form with validation, cancel/minimize/create controls.
- [ ] **Step 4: Wire in `VirtualMachinesKVM.tsx` and `App.tsx`**
  Replace `onNewVM={() => setAddNodeOpen(true)}` with `setCreateVmOpen(true)`.
- [ ] **Step 5: Run tests and verify**

---

### Task 3: Podman & Docker Hierarchy (Pod -> User Grouping & Docker Detection)
**Files:**
- Modify: `src/utils/liveCluster.ts`
- Modify: `src/utils/podmanViewModel.ts`
- Modify: `src/utils/podmanViewModel.test.ts`
- Modify: `src/components/PodmanManager.tsx`
- Modify: `server.ts`

**Interfaces:**
- Podman hierarchy: Pods displayed first with their member containers; standalone containers grouped by User (`root` for rootful, `core`, `apps`, etc. for rootless).
- Distinct "Rootful / System Pod" and "Rootless" badges.
- Docker detection: If `docker` CLI or socket exists on the host, probe `docker ps` and render Docker containers with a distinct "Docker Engine" section.

- [ ] **Step 1: Write unit tests in `src/utils/podmanViewModel.test.ts`**
  Test grouping containers by Pod and User, and Docker engine separation.
- [ ] **Step 2: Update `src/utils/liveCluster.ts` and `server.ts`**
  Parse rootful pods/containers and Docker containers from host probe.
- [ ] **Step 3: Update `PodmanManager.tsx`**
  Render hierarchical tree (Pods -> Containers, then User -> Standalone, and Docker Containers).
- [ ] **Step 4: Run tests and verify**

---

### Task 4: Visual UI for SMB & NFS Shares, System Users & Passwords
**Files:**
- Modify: `server.ts`
- Modify: `src/components/NetworkSharesView.tsx`
- Create: `src/components/modals/ShareEditorModal.tsx`
- Create: `src/components/modals/UserManagerModal.tsx`
- Create: `src/utils/sharesManager.test.ts`

**Interfaces:**
- Backend endpoints:
  - `POST /api/nodes/:id/shares/nfs` (create/edit NFS export)
  - `POST /api/nodes/:id/shares/smb` (create/edit SMB share)
  - `GET /api/nodes/:id/users` (list host users)
  - `POST /api/nodes/:id/users/create` (create user)
  - `POST /api/nodes/:id/users/:user/password` (set user passwd)
  - `POST /api/nodes/:id/users/:user/smbpassword` (set smbpasswd)
- Frontend: Visual share modal (path, clients, options, permissions) and user/password modal.

- [ ] **Step 1: Write unit tests in `src/utils/sharesManager.test.ts`**
- [ ] **Step 2: Implement backend share and user management routes in `server.ts`**
- [ ] **Step 3: Create `ShareEditorModal.tsx` and `UserManagerModal.tsx`**
- [ ] **Step 4: Integrate into `NetworkSharesView.tsx`**
  Add "+ Add Share" and "Manage Users & Passwords" buttons.
- [ ] **Step 5: Run tests and verify**

---

### Task 5: Advanced Firewall Controls & Network Interface Connection
**Files:**
- Modify: `server.ts`
- Modify: `src/components/FirewallView.tsx`
- Modify: `src/components/NetworkInterfacesView.tsx`
- Create: `src/components/modals/ConnectNetworkModal.tsx`
- Create: `src/utils/firewallRules.test.ts`

**Interfaces:**
- `FirewallView`: Add rule modal with port ranges (`8000-8080`), protocols (`tcp`, `udp`, `sctp`, `icmp`), interface binding (`tailscale0`, `eth0`), and zone selector (`public`, `internal`, `trusted`).
- `NetworkInterfacesView`: "+ Connect Network" button with VLAN, Bridge, WireGuard, and static IP configuration modal.

- [ ] **Step 1: Write unit tests in `src/utils/firewallRules.test.ts`**
- [ ] **Step 2: Update firewall backend routes and `FirewallView.tsx`**
- [ ] **Step 3: Create `ConnectNetworkModal.tsx` and integrate into `NetworkInterfacesView.tsx`**
- [ ] **Step 4: Run tests and verify**

---

### Task 6: Privilege Escalation (`run0` Preferred Over `sudo`)
**Files:**
- Modify: `src/utils/liveCluster.ts`
- Modify: `server.ts`
- Modify: `backend-go/pkg/cockpit/bridge.go`
- Modify: `src/components/NodeBreadcrumbBar.tsx`
- Create: `src/utils/privilegeEscalation.test.ts`

**Interfaces:**
- Elevation helper: Probes if `run0` is available on the node; prefers `run0 --background=no <cmd>` over `sudo -n <cmd>`.
- UI: Breadcrumb privilege pill displaying `run0 (Active)` or `sudo (Active)` with interactive elevation request button.

- [ ] **Step 1: Write unit tests for command elevation resolution in `src/utils/privilegeEscalation.test.ts`**
- [ ] **Step 2: Update `liveCluster.ts`, `server.ts`, and `backend-go` to check and prefer `run0`**
- [ ] **Step 3: Add elevation indicator and request trigger to `NodeBreadcrumbBar.tsx`**
- [ ] **Step 4: Run tests and verify**

---

### Task 7: Sidebar "Datacenter" Global Options Tab & Badge Clean-Up
**Files:**
- Modify: `src/components/ServerViewSidebar.tsx`
- Modify: `src/components/DatacenterGlobalView.tsx` (Create)
- Modify: `src/App.tsx`
- Modify: `src/utils/navigationViewModel.ts`
- Modify: `src/utils/navigationViewModel.test.ts`

**Interfaces:**
- Left sidebar top switcher: `[Nodes (48)]  [Datacenter]`.
- Datacenter view exposes: Global Ansible Playbooks Library, Cluster Variables & Secrets, Global Tasks, and Tailnet Mesh Overview.
- Sidebar node row cleanup: Replace unstyled `core SSH` text with polished status pills (IP, OS icon, online pulse dot).

- [ ] **Step 1: Update node row badge rendering in `ServerViewSidebar.tsx`**
- [ ] **Step 2: Add `[Nodes] [Datacenter]` switcher to `ServerViewSidebar.tsx`**
- [ ] **Step 3: Create `DatacenterGlobalView.tsx` in workspace**
- [ ] **Step 4: Update unit tests in `navigationViewModel.test.ts`**
- [ ] **Step 5: Run tests and verify**

---

### Task 8: Catppuccin Macchiato & Latte Dual Theme with System Preference
**Files:**
- Modify: `src/utils/themeManager.ts`
- Modify: `src/utils/themeManager.test.ts`
- Modify: `src/index.css`
- Modify: `src/components/Header.tsx`
- Modify: `index.html`

**Interfaces:**
- Theme options: `'system' | 'macchiato' | 'latte'`.
- Default: `'system'` (automatically follows `prefers-color-scheme`).
- Macchiato dark palette: `#24273a` Base, `#1e2030` Mantle, `#363a4f` Surface, `#cad3f5` Text, `#8aadf4` Blue, `#c6a0f6` Mauve.
- Latte light palette: `#eff1f5` Base, `#4c4f69` Text, `#1e66f5` Blue.
- Header toggle: 3-way toggle or dropdown with icons (`monitor`, `dark_mode`, `light_mode`).

- [ ] **Step 1: Write unit tests in `src/utils/themeManager.test.ts`**
  Test system preference resolution and theme switching.
- [ ] **Step 2: Add Catppuccin Macchiato palette to `src/index.css`**
- [ ] **Step 3: Update `themeManager.ts`, `Header.tsx`, and `index.html` pre-hydration script**
- [ ] **Step 4: Run tests and verify**

---

### Task 9: Package & Update Management Tools (`dnf`, `apt`, `apk`, `rpm-ostree`, `bootc`)
**Files:**
- Modify: `server.ts`
- Create: `src/components/PackageUpdatesView.tsx`
- Create: `src/utils/packageManager.test.ts`
- Modify: `src/components/CockpitSubSidebar.tsx`
- Modify: `src/App.tsx`

**Interfaces:**
- Backend `GET /api/nodes/:id/packages/check`:
  Probes package manager (`rpm-ostree`, `dnf`, `apt`, `apk`, `bootc`) and returns pending updates.
- Backend `POST /api/nodes/:id/packages/apply`:
  Executes update command or dispatches Ansible update task.
- `PackageUpdatesView`: UI listing available updates with versions, changelog/repository, and "Apply Updates" button. Added to Cockpit sub-sidebar under SYSTEM.

- [ ] **Step 1: Write unit tests for package manager detection in `src/utils/packageManager.test.ts`**
- [ ] **Step 2: Implement package endpoints in `server.ts`**
- [ ] **Step 3: Create `PackageUpdatesView.tsx`**
- [ ] **Step 4: Add submenu item in `CockpitSubSidebar.tsx` and route in `App.tsx`**
- [ ] **Step 5: Run tests and verify**

---

### Task 10: Dynamic Storage Architecture (ZFS vs Btrfs/LVM Dedicated Switcher)
**Files:**
- Modify: `src/components/ZfsManagementView.tsx`
- Modify: `src/components/CockpitSubSidebar.tsx`
- Modify: `src/utils/zfsParser.ts`
- Modify: `src/utils/zfsParser.test.ts`

**Interfaces:**
- If host has real ZFS pools (`zpool list -H` returns pools):
  - Sub-sidebar displays `ZFS Pools` (`ONLINE`).
  - `ZfsManagementView` displays active pools, vdev hierarchy, scrub triggers, and datasets.
- If host has NO ZFS pools:
  - Sub-sidebar displays `Btrfs & Storage` (or `LVM / Storage`).
  - View displays authentic Btrfs subvolumes, mounts, and LVM volumes with subvolume snapshots/mounts rather than empty ZFS cards.

- [ ] **Step 1: Update `ZfsManagementView.tsx` storage engine routing**
- [ ] **Step 2: Update `CockpitSubSidebar.tsx` storage label**
- [ ] **Step 3: Add unit test in `zfsParser.test.ts`**
- [ ] **Step 4: Run tests and verify**

---

### Task 11: End-to-End System Verification & Regression Suite
- [ ] **Step 1: Run Go tests:** `cd backend-go && go test -v ./... && cd ..`
- [ ] **Step 2: Run all Vitest unit tests:** `npm test`
- [ ] **Step 3: Run TypeScript linter:** `npm run lint`
- [ ] **Step 4: Run Vite production build:** `npm run build`
- [ ] **Step 5: Smoke test in browser and verify live WebSocket PTY streaming**
