# Homelab Cockpit Refinement & Zero-Mock Stabilization Plan

**Date:** 2026-10-19  
**Branch:** `main`  
**Goal:** Address runtime API JSON parsing errors, static sub-sidebar pinning, deep rootless/rootful Podman inspection via `run0 --user`, UI deduplication, streamlined node editor, Console dock layout fixes, independent terminal windows, configurable Datacenter quick commands, and universal SSH autodetection.

---

## 1. Architectural Principles & Invariants

1. **Zero-Mock Policy (STRICT):**
   - No fake container lists, placeholder images (`rootless/container`), synthetic CPU/RAM (e.g. `0.0%`), or mock log fallbacks.
   - All probes and commands must execute against real host environments via SSH or local APIs.
2. **Universal SSH & Autodetection:**
   - Universal SSH connectivity is the primary transport.
   - Privilege escalation: Prefer `run0` (`run0 --background=no`, `run0 --user <username>`) for systemd DBus session access, fallback to `sudo -n`, fallback to `doas`, fallback to `none`.
   - Tool modularity: Probe for `cockpit-bridge`, `podman`, `docker`, `systemctl`, `journalctl`, `findmnt`, `zpool`, `btrfs`, `nmcli`, and package managers (`dnf`, `apt`, `apk`, `rpm-ostree`, `bootc`).
3. **Defense Against Non-JSON API Responses:**
   - Explicit catch-all route for `/api/*` returning HTTP 404 JSON, preventing Vite SPA fallback from serving `<!doctype html>`.
   - Frontend fetch defensively checks `res.ok` and `content-type: application/json` before `res.json()`.
4. **4-Gate Verification:**
   - `cd backend-go && go test -v ./... && cd ..`
   - `npm test`
   - `npm run lint` (`tsc --noEmit`)
   - `npm run build` (`vite build`)

---

## 2. Actionable Implementation Tasks

### Task 1: API 404 Guard & Error Handling (Eliminate `<!doctype html>` JSON crashes)
- **Files:** `server.ts`, `package.json`, `src/components/PackageUpdatesView.tsx`, `src/components/ZfsManagementView.tsx`, `src/components/PodmanManager.tsx`, `src/utils/apiDefensive.ts`
- **Actions:**
  - In `server.ts`: Add `app.all('/api/*', (req, res) => res.status(404).json({ success: false, error: 'Endpoint not found' }))` right before Vite middlewares.
  - In `package.json`: Update `"dev": "tsx watch server.ts"` so server code changes reload automatically.
  - Create helper `safeFetchJson<T>(url, options)` in `src/utils/apiDefensive.ts` to inspect HTTP response status and content-type before parsing JSON, surfacing structured error messages.
  - Apply `safeFetchJson` across `PackageUpdatesView`, `ZfsManagementView`, and `PodmanManager`.

### Task 2: Static Independent Sub-Sidebar Layout
- **Files:** `src/App.tsx`, `src/components/CockpitSubSidebar.tsx`, `src/utils/navigationViewModel.ts`
- **Actions:**
  - Update `getSubSidebarWrapperClasses`: Ensure `self-start lg:sticky lg:top-[76px] lg:max-h-[calc(100vh-100px)] overflow-y-auto scrollbar-thin shrink-0`.
  - In `App.tsx`: Set grid alignment `items-start` on `<div className="grid grid-cols-12 gap-2 mt-2 w-full items-start">` so the sub-sidebar stays statically pinned while the workspace to its right scrolls independently.

### Task 3: Rootful & Rootless Podman Probing with `run0 --user` & Real Metrics
- **Files:** `src/utils/liveCluster.ts`, `src/utils/orchestrator.ts`, `server.ts`, `src/components/PodmanManager.tsx`, `src/utils/podmanViewModel.ts`
- **Actions:**
  - In `liveCluster.ts`:
    - Add user impersonation support: if host supports `run0`, execute `run0 --background=no --user <user> podman ps --all --format json` and `podman pod ps --format json`.
    - Probe real rootful containers and pods via `run0 podman ...` / `sudo -n podman ...`.
    - Remove placeholder string `rootless/container` in `orchestrator.ts`.
    - Probe live container resource metrics using `podman stats --no-stream --format json` (or `docker stats --no-stream --format json`) so CPU% and Memory report live telemetry instead of 0.0%.
    - Update container logs endpoint (`/api/podman/logs`): execute `run0 --background=no --user <user> podman logs --tail 100 <container>` (or rootful equivalent) without simulated fallback text.

### Task 4: UI Cleanup & Deduplication of Buttons
- **Files:** `src/components/Header.tsx`, `src/components/GlobalTasksExecutionLog.tsx`, `src/components/TopSubNav.tsx`, `src/components/NodeBreadcrumbBar.tsx`
- **Actions:**
  - Remove duplicate terminal buttons:
    - Remove `Terminal / SSH` button from `Header.tsx`.
    - Remove green `+ Term` button from `GlobalTasksExecutionLog.tsx`.
    - Remove `Host Terminal (libghostty)` tab from `TopSubNav.tsx`.
  - Retain the clean single terminal button in `NodeBreadcrumbBar.tsx`.
  - Remove the noisy `PROX-ORCH v8.2-ansible` badge in top-left of `Header.tsx`, keeping clean branding.
  - Preserve the user profile / login pill in top-right of `Header.tsx` for future RBAC/OIDC.

### Task 5: Left Sidebar Host Row Simplification & Badge Clean-Up
- **Files:** `src/components/ServerViewSidebar.tsx`, `src/utils/navigationViewModel.ts`
- **Actions:**
  - Remove non-functioning lock/key SSH badge.
  - Remove meaningless badges like `Linux`, `Local`, `Mgr`, `Host`, `Arm64`.
  - Keep only essential, clean status: online/offline pulse dot, OS icon, node name, monospaced IP, and true platform pills (`PVE`, `VM`, `LXC`, `K3S`).

### Task 6: Streamlined EditNodeModal (Auto-Probed Hardware & Dynamic Type Options)
- **Files:** `src/components/modals/EditNodeModal.tsx`, `src/App.tsx`, `server.ts`
- **Actions:**
  - Remove editable text inputs for CPU model, motherboard, secure boot, IOMMU, IPMI, BIOS, RAM, and vCPUs.
  - Display auto-probed hardware specs as read-only telemetry badges with a "Re-probe Hardware Now" button.
  - Keep persistent editable inputs restricted to: Node Name, Group, Node Type, and connection routes (Primary Tailscale route, fallback LAN IP/hostname, SSH user, port).
  - Dynamically adapt connection options based on Node Type (e.g. Linux SSH vs Proxmox API vs Windows WinRM).
  - Display Tailscale tag & IP as fetched read-only network routes.

### Task 7: Console (Global Tasks) Header Layout & Scrolling Fix
- **Files:** `src/components/GlobalTasksExecutionLog.tsx`, `src/App.tsx`, `src/components/TopSubNav.tsx`
- **Actions:**
  - Rename "Global Tasks and Execution Log" to "Console" (or "Host Console & Tasks").
  - Restructure drawer header bar:
    - Pinned left controls: drawer title "Console" and Category Tabs (`Active Tasks`, `History`).
    - Isolated middle flex container for minimized window pills with `overflow-x-auto scrollbar-none max-w-full`.
    - Pinned right controls: expand, collapse, and clear buttons so controls never scroll off-screen.

### Task 8: Independent Terminal Windows (Remove Internal Session Tabs)
- **Files:** `src/components/modals/TerminalModal.tsx`, `src/components/TerminalTabBar.tsx`
- **Actions:**
  - In `TerminalModal.tsx`:
    - Remove `TerminalTabBar` multi-session tabs inside the window.
    - Provide an independent title bar for each session showing its own node target, quick command selector, minimize `[-]`, maximize `[□]`, and close `[✕]`.
    - Minimized terminal windows dock directly into the Console drawer without duplicate internal tabs.

### Task 9: Configurable Datacenter Quick Commands
- **Files:** `src/components/DatacenterGlobalView.tsx`, `src/components/modals/QuickCommandsModal.tsx`, `src/utils/quickCommandsManager.ts`
- **Actions:**
  - Create `src/utils/quickCommandsManager.ts` with default system commands (`uname -a`, `df -h`, `uptime`, `podman ps`, `systemctl status`) and localStorage persistence.
  - In `DatacenterGlobalView.tsx`: Add a "Quick Commands Manager" card with "+ Add Command", edit, and delete actions.
  - Feed configured quick commands into terminal windows dynamically.

### Task 10: Capability Autodetection & Verification
- **Files:** `server.ts`, `backend-go/pkg/cockpit/bridge.go`, all test suites
- **Actions:**
  - Validate modular capability probing over SSH without mandatory `cockpit-bridge`.
  - Run all 4 verification gates (`go test ./...`, `npm test`, `npm run lint`, `npm run build`).
