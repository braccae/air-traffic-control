# Go Plugin Framework & Connection Layer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement a compile-time Go plugin registry, pure-Go SSH + embedded tsnet connection layer with Ansible proxy bridge, dynamic inner sidebar, and host Modules tab covering all 13 existing views.

**Architecture:** The Go daemon owns all plugin definitions, probes, readers, and task compilation templates. TypeScript coordinator delegates plugin discovery, reads, and action compilation to the Go daemon over HTTP, invalidating cache on mutation. The frontend inner sidebar and Modules tab dynamically render based on detected and compatible plugins using a cohesive UI kit.

**Tech Stack:** Go 1.22+, `golang.org/x/crypto/ssh`, `tailscale.com/tsnet`, React 19, TypeScript, Vite, Tailwind CSS v4, SQLite.

**Spec:** [`docs/superpowers/specs/2026-10-19-go-plugin-framework-design.md`](file:///home/pants/Projects/go_projects/air-traffic-control/docs/superpowers/specs/2026-10-19-go-plugin-framework-design.md)

## Global Constraints

- Compile-time plugin registration via `init()` and blank imports; no dynamic `.so` or runtime WASM.
- Zero-mock invariant: strictly no synthetic telemetry or mock successes; failed probes must report errors and prevent active tab display.
- Every host mutation must produce a declarative `QueuedAnsibleTask` executed sequentially with checkpoint halting.
- The daemon runs controller-only; targets remain agentless.
- All four verification checks (`go test ./...`, `npm test`, `npm run lint`, `npm run build`) must pass cleanly.

## Review Focus

- If tsnet `none`-auth negotiation fails against a Tailscale SSH server, the connection must fall back cleanly to tailnet SSH with identity key without panicking.
- An unreachable daemon at `127.0.0.1:18090` must fail loudly in TypeScript API responses without masking failures or serving stale mock data.
- Unknown/unsupported plugin kinds must render graceful fallback or unsupported pill in Modules view rather than throwing React render errors.
- Template variables containing sensitive strings must propagate the `secret: true` flag into `QueuedAnsibleTask.secretsMasked`.
- Legacy `QueuedAnsibleTask` records lacking the new connection block must deserialize and execute without breaking existing SQLite task queues.

---

### Task 1: Tailscale tsnet & Pure-Go SSH Spike Verification

**Files:**
- Create: `backend-go/cmd/spike-tsnet/main.go`
- Test: `backend-go/pkg/ssh/spike_test.go`

**Interfaces:**
- Produces: Verified auth mechanism for tsnet connection plugin (none auth vs key fallback).

- [ ] **Step 1: Write verification test for tsnet loopback & none-auth dialer**
- [ ] **Step 2: Run test and observe failure/missing types**
- [ ] **Step 3: Implement minimal spike harness testing tsnet dial with fallback**
- [ ] **Step 4: Run spike test and record confirmed auth behavior**
- [ ] **Step 5: Clean up scratch binaries and commit test findings**

---

### Task 2: Core Plugin Registry & Interfaces in Go

**Files:**
- Create: `backend-go/pkg/plugin/plugin.go`
- Create: `backend-go/pkg/plugin/registry.go`
- Create: `backend-go/pkg/plugin/types.go`
- Test: `backend-go/pkg/plugin/registry_test.go`

**Interfaces:**
- Produces: `Plugin`, `Reader`, `Actor`, `Runner`, `HostFacts`, `Detection`, `Requirements`, `TaskSpec`, `Register()`, `Get()`, `List()`.

- [ ] **Step 1: Write failing registry unit tests (register, list by kind, filter compatible, duplicate guard)**
- [ ] **Step 2: Run `go test ./pkg/plugin` to verify failure**
- [ ] **Step 3: Implement plugin types, schema definitions, and thread-safe registry in Go**
- [ ] **Step 4: Run `go test ./pkg/plugin` and ensure all pass**
- [ ] **Step 5: Commit registry core**

---

### Task 3: Connection Layer & Ansible Proxy Command

**Files:**
- Create: `backend-go/plugins/connection/ssh/ssh.go`
- Create: `backend-go/plugins/connection/tailscale/tailscale.go`
- Create: `backend-go/plugins/connection/local/local.go`
- Create: `backend-go/cmd/proxy/proxy.go`
- Modify: `backend-go/main.go`
- Test: `backend-go/plugins/connection/ssh/ssh_test.go`

**Interfaces:**
- Consumes: `backend-go/pkg/plugin`
- Produces: Connection runners capable of executing commands and establishing `net.Conn`, plus `air-traffic-control-daemon proxy <host> <port>` CLI subcommand.

- [ ] **Step 1: Write unit tests for SSH, local runner, and proxy stdio piping using mock net.Conn**
- [ ] **Step 2: Run tests to verify failure**
- [ ] **Step 3: Implement `connection/ssh`, `connection/tailscale` (with tsnet), `connection/local`, and proxy command**
- [ ] **Step 4: Run connection unit tests and confirm pass**
- [ ] **Step 5: Commit connection layer**

---

### Task 4: Port Core Host System & Elevation Plugins to Go

**Files:**
- Create: `backend-go/plugins/elevation/run0/run0.go`
- Create: `backend-go/plugins/elevation/sudo/sudo.go`
- Create: `backend-go/plugins/elevation/doas/doas.go`
- Create: `backend-go/plugins/package/dnf/dnf.go`
- Create: `backend-go/plugins/package/apt/apt.go`
- Create: `backend-go/plugins/package/apk/apk.go`
- Create: `backend-go/plugins/system/service/service.go`
- Create: `backend-go/plugins/all/all.go`
- Test: `backend-go/plugins/elevation/elevation_test.go`
- Test: `backend-go/plugins/package/package_test.go`

**Interfaces:**
- Consumes: `backend-go/pkg/plugin`
- Produces: Self-registering elevation, package management, and system service plugins with embedded task templates.

- [ ] **Step 1: Write failing tests for elevation probing order and package manager command compilation**
- [ ] **Step 2: Run test suite to verify failure**
- [ ] **Step 3: Implement elevation (run0, sudo, doas) and package (dnf, apt, apk) plugins with Jinja templates**
- [ ] **Step 4: Run `go test ./plugins/...` to verify all pass**
- [ ] **Step 5: Commit system, elevation, and package plugins**

---

### Task 5: Port Storage, Shares, Containers, Backups & Observability Plugins to Go

**Files:**
- Create: `backend-go/plugins/storage/zfs/zfs.go`
- Create: `backend-go/plugins/storage/btrfs/btrfs.go`
- Create: `backend-go/plugins/storage/lvm/lvm.go`
- Create: `backend-go/plugins/share/nfs/nfs.go`
- Create: `backend-go/plugins/share/smb/smb.go`
- Create: `backend-go/plugins/container/podman/podman.go`
- Create: `backend-go/plugins/backup/restic/restic.go`
- Create: `backend-go/plugins/logs/journald/journald.go`
- Create: `backend-go/plugins/disk/smart/smart.go`
- Create: `backend-go/plugins/vm/libvirt/libvirt.go`
- Create: `backend-go/plugins/telemetry/glances/glances.go`
- Create: `backend-go/plugins/network/interfaces/interfaces.go`
- Create: `backend-go/plugins/firewall/firewalld/firewalld.go`
- Test: `backend-go/plugins/storage/zfs/zfs_test.go`
- Test: `backend-go/plugins/share/share_test.go`

**Interfaces:**
- Consumes: `backend-go/pkg/plugin`, `backend-go/pkg/podman`, `backend-go/pkg/restic`
- Produces: Plugins for all 13 host views with Read data shapes conforming to existing frontend expectations.

- [ ] **Step 1: Write failing tests for ZFS, NFS/SMB, Podman, and SMART probe/read JSON generation**
- [ ] **Step 2: Run tests to verify missing plugin implementations**
- [ ] **Step 3: Implement all remaining host plugins with embedded YAML task templates and Read parsers**
- [ ] **Step 4: Run `go test ./...` in `backend-go` to confirm all pass**
- [ ] **Step 5: Commit ported domain plugins**

---

### Task 6: Expose Plugin Management REST API in Go Daemon

**Files:**
- Modify: `backend-go/main.go`
- Create: `backend-go/pkg/plugin/api.go`
- Test: `backend-go/pkg/plugin/api_test.go`

**Interfaces:**
- Produces: HTTP endpoints:
  - `GET /api/plugins`
  - `GET /api/nodes/:id/plugins`
  - `GET /api/nodes/:id/modules`
  - `GET /api/nodes/:id/plugins/:kind/:name`
  - `POST /api/nodes/:id/plugins/:kind/:name/actions/:action`
  - `POST /api/nodes/:id/plugins/refresh`

- [ ] **Step 1: Write tests for HTTP endpoints verifying node probe resolution and action compilation**
- [ ] **Step 2: Run tests to verify endpoints are not registered**
- [ ] **Step 3: Implement HTTP handlers in `pkg/plugin/api.go` reading node records from `data/hosts-cache.sqlite`**
- [ ] **Step 4: Mount handlers in `main.go` and verify all tests pass**
- [ ] **Step 5: Commit Go plugin API server routes**

---

### Task 7: Update TypeScript Coordinator, Task Schema & Remove Deprecated Drivers

**Files:**
- Modify: `src/types/index.ts`
- Modify: `server.ts`
- Modify: `src/utils/ansibleQueueCompiler.ts`
- Modify: `src/utils/ansibleQueueRunner.ts`
- Modify: `src/utils/drivers/index.ts`
- Delete: `src/utils/drivers/systemDriver.ts`
- Delete: `src/utils/drivers/storageDriver.ts`
- Delete: `src/utils/drivers/networkDriver.ts`
- Test: `src/utils/ansibleQueueRunner.test.ts`
- Test: `src/utils/drivers/systemDriver.test.ts` (replaced with plugin API proxy test)

**Interfaces:**
- Consumes: Daemon `/api/nodes/:id/plugins/...`
- Produces: Updated `QueuedAnsibleTask` supporting generic connection blocks and proxying action compilations to Go daemon.

- [ ] **Step 1: Update `QueuedAnsibleTask` in `src/types/index.ts` with generic connection block; add failing test for legacy row deserialization**
- [ ] **Step 2: Run `npm test` to verify compiler/queue failures**
- [ ] **Step 3: Update `server.ts` and `ansibleQueueRunner.ts` to proxy plugin requests to Go daemon and generate temporary task inventory with ProxyCommand**
- [ ] **Step 4: Remove superseded TS drivers in `src/utils/drivers/` and update driver tests to verify daemon proxying**
- [ ] **Step 5: Run `npm test` and verify all unit tests pass; commit changes**

---

### Task 8: Build Frontend Shared Plugin Kit & Dynamic CockpitSubSidebar

**Files:**
- Create: `src/plugins/kit/PluginResourceTable.tsx`
- Create: `src/plugins/kit/PluginActionForm.tsx`
- Create: `src/plugins/kit/PluginStateView.tsx`
- Create: `src/plugins/kit/index.ts`
- Modify: `src/components/CockpitSubSidebar.tsx`
- Test: `src/components/CockpitSubSidebar.test.tsx`

**Interfaces:**
- Consumes: `GET /api/nodes/:id/plugins`
- Produces: Dynamic sidebar tabs grouped by category, rendering only detected plugins with real badges.

- [ ] **Step 1: Write unit tests for `CockpitSubSidebar` verifying that undetected plugins render no tab and active plugins render grouped tabs**
- [ ] **Step 2: Run `npm test` to confirm test failure**
- [ ] **Step 3: Implement plugin UI kit components and refactor `CockpitSubSidebar` to consume `/api/nodes/:id/plugins`**
- [ ] **Step 4: Run `npm test` and verify sidebar tests pass**
- [ ] **Step 5: Commit plugin UI kit and dynamic sidebar**

---

### Task 9: Implement Host Modules View & Custom Plugin View Routing

**Files:**
- Create: `src/components/HostModulesView.tsx`
- Create: `src/plugins/views/index.ts`
- Modify: `src/App.tsx`
- Modify: `src/components/modals/EditNodeModal.tsx`
- Test: `src/components/HostModulesView.test.tsx`

**Interfaces:**
- Consumes: `GET /api/nodes/:id/modules`, `POST /api/nodes/:id/plugins/:kind/:name/actions/install`
- Produces: Modules tab at sidebar bottom showing Active/Available/Unsupported states with package requirements, plus manual plugin overrides in Edit Node modal.

- [ ] **Step 1: Write unit test for `HostModulesView` rendering active, available, and unsupported plugins with install action staging**
- [ ] **Step 2: Run `npm test` to verify failure**
- [ ] **Step 3: Implement `HostModulesView`, wire up routing in `App.tsx`, and add connection/elevation/package override dropdowns in `EditNodeModal.tsx`**
- [ ] **Step 4: Run `npm test` and ensure all tests pass**
- [ ] **Step 5: Commit Modules view and view registry**

---

### Task 10: End-to-End Verification & AGENTS.md Documentation Update

**Files:**
- Modify: `AGENTS.md`
- Test: Run full verification suite

- [ ] **Step 1: Update `AGENTS.md` §6 to document the Go plugin framework, connection layer, and Modules tab**
- [ ] **Step 2: Run `cd backend-go && go test -v ./... && cd ..`**
- [ ] **Step 3: Run `npm test`**
- [ ] **Step 4: Run `npm run lint`**
- [ ] **Step 5: Run `npm run build`**
- [ ] **Step 6: Commit all documentation updates and test adjustments**
