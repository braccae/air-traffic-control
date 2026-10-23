# Air Traffic Control — v1 vs. v2 Plugin System Parity Audit & Comparison

**Audit Date:** 2026-10-22  
**Baseline Commit (v1):** `c645b6f` (`test(plugin): guard SQLiteNodeResolver test when hosts table is absent`)  
**Target Commit (v2):** `927d68b` (`Merge branch 'wt/t_72e60df3'`)  
**Scope:** Core plugin framework (`backend-go/pkg/plugin`, `newapi`, `settingsframework`, `contextmenu`, `nodeapi`, `taskapi`), all 36 compile-time Go plugins across 16 kinds (`backend-go/plugins/<kind>/<name>`), daemon HTTP API routes (`backend-go/main.go`), frontend TypeScript/React suite (`src/`), and Zero-Mock compliance.

---

## 1. Executive Summary

The migration from the legacy **v1 plugin system** to the modular **v2 plugin architecture** across all 36 registered Go plugins is complete and verified:

- **All 4 Verification Gates Pass:** Every Go package (`go test -v ./...`), frontend unit test (`npm test` — 626/626 passing across 110 suites), TypeScript static check (`npm run lint`), and Vite production bundle (`npm run build`) pass with zero errors.
- **100% Backward-Compatible API Parity:** Every legacy v1 plugin contract (`plugin.Plugin`, `plugin.Reader`, `plugin.Actor`, `plugin.SettingsPageProvider`, `plugin.Dialer`), plugin ID (`<kind>/<name>`), action name, option schema, `Read()` JSON payload shape, and HTTP endpoint (`GET /api/plugins`, `GET /api/nodes/:id/plugins`, `POST /api/nodes/:id/plugins/:kind/:name/actions/:action`, `GET|PUT /api/settings/pages/:id`) remains intact and verified.
- **100% v2 Capability Coverage:** All 36 plugins now implement `newapi.ManagementPlugin`, `newapi.CategorizedSettingsProvider` (via `settingsframework.NewPageBuilder`), `newapi.ContextMenuProvider` (via `newapi.NewContextMenuBuilder`), and `newapi.TaskCompileProvider`. Furthermore, 33 of 36 plugins implement `newapi.DataCollectionPlugin` (up from 25 in v1), and 4 plugins (`backup/restic`, `storage/btrfs`, `storage/lvm`, `system/service`) register specialized `newapi.TaskTypeProvider` entries.
- **Major Test Coverage Expansion:** Average statement coverage across all 36 plugin packages increased from **49.3% (v1)** to **83.8% (v2)** (**+34.5 percentage points**). All 10 previously untested plugin packages (`0.0%` coverage in v1 across `elevation/*`, `package/{apk,apt,dnf,rpmostree}`, and `share/{nfs,smb}`) now have dedicated unit test suites with **83.4%–98.1%** statement coverage.
- **Zero-Mock Policy Verified:** Static and runtime audits confirm zero synthetic telemetry jitter (`rand.Float*` / `Math.random()`), zero simulated UI mutations, declarative Ansible module compilation with `ANSIBLE_STDOUT_CALLBACK=json`, and write-only secret masking (`•••••••• (vault: <name>)`).

---

## 2. Verification Gates Report

All four repository verification gates were executed against the merged v2 codebase:

| Gate # | Verification Gate | Exact Command | Status | Summary Result |
|---:|---|---|---|---|
| **1** | **Go Backend Tests** | `cd backend-go && TMPDIR=/tmp go test -v ./...` | **PASS** | 65/65 testable packages pass, 0 failures (`448` top-level `Test*` functions + subtests) |
| **2** | **Frontend Unit Tests** | `npm test -- --run` | **PASS** | `626` tests pass across `110` suites (`0` failed, `0` skipped with `data/ansible-venv` linked) |
| **3** | **TypeScript Lint** | `npm run lint` (`tsc --noEmit`) | **PASS** | Zero type or compiler errors |
| **4** | **Production Build** | `npm run build` (`vite build`) | **PASS** | Built cleanly in `1.59s` (`1756` modules transformed into `dist/`) |

---

## 3. Architectural & API Parity Comparison (v1 vs. v2)

### 3.1 Core Interface & Framework Parity

The v2 plugin system preserves every v1 interface while introducing a modular, acyclic extension layer in `pkg/plugin/newapi`, `pkg/plugin/settingsframework`, `pkg/contextmenu`, `pkg/nodeapi`, and `pkg/taskapi`.

| Capability Domain | v1 System (Baseline `c645b6f`) | v2 System (Current `927d68b`) | Parity & Compatibility Status |
|---|---|---|---|
| **Base Identity & Detection** | `plugin.Plugin` (`Meta`, `Compatible`, `Requirements`, `Probe`) | `plugin.Plugin` (embedded by all `newapi` interfaces) | **100% Parity** — identical IDs, kinds, package requirements, and `Probe()` behavior. |
| **Data Collection & Telemetry** | `plugin.Reader` (`Read(ctx, r, h)`) implemented by 25 plugins | `newapi.DataCollectionPlugin` (`Read` + `CollectionSchedule()` + `CollectionSchema()`) implemented by 33 plugins | **Superset Parity** — all 25 v1 Readers preserve their exact `Read()` payload keys; 8 formerly write-only plugins (`connection/*`, `package/{apk,apt,dnf,rpmostree}`, `elevation/doas`) gained live state inspection. |
| **Host Mutations & Actions** | `plugin.Actor` (`Actions()`, `Compile(h, action, opts)`) implemented by 31 plugins | `newapi.ManagementPlugin` (`Actions`, `Compile` + `ManagementActions()`, `ValidateAction(h, action, opts)`) implemented by all 36 plugins | **Superset Parity** — `Actions()` is projected directly from `ManagementActions()` as a single source of truth; 5 formerly read-only/probe-only plugins (`connection/*`, `logs/journald`, `telemetry/glances`) gained structured diagnostic/lifecycle actions. |
| **Settings Pages & Store** | `plugin.SettingsPageProvider` (`SettingsPage()`) implemented by 3 plugins (`connection/tailscale`, `backup/restic`, `mount/rclone`) via raw struct literals | `newapi.CategorizedSettingsProvider` (`CategorizedSettingsPage()` + `SettingsPage()`) implemented by all 36 plugins via `settingsframework.NewPageBuilder` & `StoreAdapter` | **Superset Parity** — `SettingsPage()` delegates to `CategorizedSettingsPage().SettingsPage`. All 3 v1 settings pages retain their exact field keys, defaults, and secret masks; 33 additional plugins now expose datacenter settings pages. |
| **Contextual UI Menus** | Hardcoded in React components (`src/components/ContextMenu.tsx`) | `newapi.ContextMenuProvider` (`ContextMenuEntries(ctx, h)`) + `pkg/contextmenu` builder and renderer | **New Capability (Backward-Compatible)** — all 36 plugins provide host-aware context menu actions indexed by target (`TargetNode`, `TargetContainer`, `TargetVM`, `TargetStorage`, `TargetShare`, `TargetNetwork`, `TargetFirewall`, `TargetService`, `TargetDatabase`, `TargetUser`, `TargetImage`). |
| **Inventory Node Types** | Ad-hoc string checks on `h.OSFamily` | `pkg/nodeapi` (`NodeType` enum, `NodeMetadata`, `GroupNodes`) + `newapi.NodeTypeProvider` | **Superset Parity** — built-in node types (`linux`, `windows`, `router_switch`, `proxmox`, `container_host`, `storage_appliance`, `kubernetes`) registered in `plugin.Registry` and exposed via HTTP. |
| **Task Compilation & Execution** | Direct `Actor.Compile(h, action, opts)` invocation | `newapi.TaskCompileProvider` (`CompileTask(ctx, h, req)`) + `newapi.TaskTypeProvider` + `pkg/taskapi` (`Executor`, `PluginActionExecutor`) | **100% Parity** — `CompileTask` delegates to `Compile(h, action, req.Params)`, producing identical `plugin.TaskSpec` payloads and secret masking lists. |

### 3.2 Daemon HTTP Endpoint Parity

All legacy daemon endpoints continue to operate with identical request/response schemas alongside the new v2 endpoints wired in `backend-go/main.go`:

| Endpoint Group | HTTP Route | Present in v1 | Present in v2 | Verification Test Location |
|---|---|:---:|:---:|---|
| **Plugins (v1 + v2)** | `GET /api/plugins` | Yes | Yes | `pkg/plugin/api_test.go`, `main_test.go` |
| **Node Plugins** | `GET /api/nodes/:id/plugins` | Yes | Yes | `pkg/plugin/api_test.go` |
| **Node Modules** | `GET /api/nodes/:id/modules` | Yes | Yes | `pkg/plugin/api_test.go` |
| **Plugin Refresh** | `POST /api/nodes/:id/plugins/refresh` | Yes | Yes | `pkg/plugin/api_test.go` |
| **Plugin Read** | `GET /api/nodes/:id/plugins/:kind/:name` | Yes | Yes | `pkg/plugin/api_test.go` |
| **Plugin Action** | `POST /api/nodes/:id/plugins/:kind/:name/actions/:action` | Yes | Yes | `pkg/plugin/api_test.go` |
| **Global Settings (v1)** | `GET /api/settings/pages` | Yes | Yes | `pkg/plugin/settings_test.go` |
| **Global Settings Page (v1)** | `GET \| PUT /api/settings/pages/:id` | Yes | Yes | `pkg/plugin/settings_test.go` |
| **Categorized Settings (v2)** | `GET /api/settings/categories?scope=datacenter\|node` | No | **Yes** | `pkg/plugin/settingsframework/settingsframework_test.go`, `main_test.go` |
| **Datacenter Settings List (v2)** | `GET /api/settings/datacenter` | No | **Yes** | `pkg/plugin/settingsframework/settingsframework_test.go`, `main_test.go` |
| **Datacenter Settings CRUD (v2)** | `GET \| PUT /api/settings/datacenter/:id` | No | **Yes** | `pkg/plugin/settingsframework/settingsframework_test.go`, `main_test.go` |
| **Context Menus (v2)** | `GET /api/context-menus`, `/targets`, `/:target` | No | **Yes** | `pkg/contextmenu/contextmenu_test.go`, `main_test.go` |
| **Node Types (v2)** | `GET /api/node-types` | No | **Yes** | `pkg/nodeapi/types_test.go`, `main_test.go` |
| **Task Types & Exec (v2)** | `GET /api/task-types`, `/:type`, `POST /api/task-types/execute` | No | **Yes** | `pkg/taskapi/taskapi_test.go`, `main_test.go` |

---

## 4. Per-Plugin Capability & Test Coverage Matrix (All 36 Plugins)

The table below compares every compile-time plugin in `backend-go/plugins/<kind>/<name>` between the v1 baseline (`c645b6f`) and the v2 target (`927d68b`).

**Legend for v2 Interfaces:**
- `DC`: `newapi.DataCollectionPlugin`
- `MG`: `newapi.ManagementPlugin`
- `CS`: `newapi.CategorizedSettingsProvider`
- `CM`: `newapi.ContextMenuProvider`
- `TC`: `newapi.TaskCompileProvider`
- `TT`: `newapi.TaskTypeProvider`

| # | Plugin ID (`<kind>/<name>`) | v1 Interfaces | v2 Interfaces (`newapi`) | Context Menu Target(s) | Actions (v2) | v1 Tests (`Test*`) | v2 Tests (`Test*`) | v1 Stmt Cov | v2 Stmt Cov | Cov Delta |
|---:|---|---|---|---|---:|---:|---:|---:|---:|---:|
| 1 | `backup/restic` | `Plugin, Reader, Actor, Settings` | `DC, MG, CS, CM, TC, TT` | `TargetNode` | 2 | 1 | 3 | 68.3% | 85.9% | **+17.6%** |
| 2 | `connection/local` | `Plugin` | `DC, MG, CS, CM, TC` | `TargetNode` | 2 | 2 | 4 | 77.3% | 84.1% | **+6.8%** |
| 3 | `connection/ssh` | `Plugin, Dialer` | `DC, MG, CS, CM, TC` (+`Dialer`) | `TargetNode` | 3 | 3 | 5 | 71.8% | 74.7% | **+2.9%** |
| 4 | `connection/tailscale` | `Plugin, Settings, Dialer` | `DC, MG, CS, CM, TC` (+`Dialer`) | `TargetNode` | 3 | 10 | 11 | 63.7% | 66.9% | **+3.2%** |
| 5 | `container/docker` | `Plugin, Reader, Actor` | `DC, MG, CS, CM, TC` | `TargetContainer` | 5 | 2 | 3 | 59.4% | 69.4% | **+10.0%** |
| 6 | `container/podman` | `Plugin, Reader, Actor` | `DC, MG, CS, CM, TC` | `TargetContainer` | 5 | 2 | 3 | 62.8% | 76.0% | **+13.2%** |
| 7 | `database/nosql` | `Plugin, Reader, Actor` | `DC, MG, CS, CM, TC` | `TargetDatabase` | 5 | 4 | 5 | 62.5% | 65.1% | **+2.6%** |
| 8 | `database/sql` | `Plugin, Reader, Actor` | `DC, MG, CS, CM, TC` | `TargetDatabase` | 5 | 4 | 5 | 62.0% | 64.8% | **+2.8%** |
| 9 | `disk/smart` | `Plugin, Reader, Actor` | `DC, MG, CS, CM, TC` | `TargetNode` | 1 | 1 | 3 | 69.9% | 80.1% | **+10.2%** |
| 10 | `elevation/doas` | `Plugin, Actor` | `DC, MG, CS, CM, TC` | `TargetNode, TargetUser` | 1 | 0 | 3 | 0.0% | 97.4% | **+97.4%** |
| 11 | `elevation/none` | `Plugin, Actor` | `MG, CS, CM, TC` | `TargetNode` | 1 | 0 | 3 | 0.0% | 98.1% | **+98.1%** |
| 12 | `elevation/run0` | `Plugin, Actor` | `MG, CS, CM, TC` | `TargetNode, TargetUser` | 1 | 0 | 3 | 0.0% | 98.1% | **+98.1%** |
| 13 | `elevation/sudo` | `Plugin, Actor` | `MG, CS, CM, TC` | `TargetNode, TargetUser` | 1 | 0 | 3 | 0.0% | 98.1% | **+98.1%** |
| 14 | `firewall/firewalld` | `Plugin, Reader, Actor` | `DC, MG, CS, CM, TC` | `TargetFirewall` | 3 | 6 | 7 | 80.6% | 85.2% | **+4.6%** |
| 15 | `firewall/iptables` | `Plugin, Reader, Actor` | `DC, MG, CS, CM, TC` | `TargetFirewall` | 3 | 3 | 4 | 84.1% | 85.7% | **+1.6%** |
| 16 | `firewall/nftables` | `Plugin, Reader, Actor` | `DC, MG, CS, CM, TC` | `TargetFirewall` | 2 | 2 | 3 | 55.2% | 58.8% | **+3.6%** |
| 17 | `firewall/ufw` | `Plugin, Reader, Actor` | `DC, MG, CS, CM, TC` | `TargetFirewall` | 3 | 2 | 3 | 77.6% | 79.5% | **+1.9%** |
| 18 | `logs/journald` | `Plugin, Reader` | `DC, MG, CS, CM, TC` | `TargetNode` | 4 | 1 | 4 | 67.3% | 94.5% | **+27.2%** |
| 19 | `mount/rclone` | `Plugin, Reader, Actor, Settings` | `DC, MG, CS, CM, TC` | `TargetNode` | 2 | 5 | 6 | 77.2% | 87.5% | **+10.3%** |
| 20 | `network/interfaces` | `Plugin, Reader, Actor` | `DC, MG, CS, CM, TC` | `TargetNetwork` | 2 | 1 | 3 | 72.3% | 84.3% | **+12.0%** |
| 21 | `network/tailscale` | `Plugin, Reader, Actor` | `DC, MG, CS, CM, TC` | `TargetNetwork` | 7 | 9 | 11 | 79.0% | 90.2% | **+11.2%** |
| 22 | `package/apk` | `Plugin, Actor` | `DC, MG, CS, CM, TC` | `TargetNode` | 3 | 0 | 4 | 0.0% | 92.2% | **+92.2%** |
| 23 | `package/apt` | `Plugin, Actor` | `DC, MG, CS, CM, TC` | `TargetNode` | 3 | 0 | 4 | 0.0% | 95.5% | **+95.5%** |
| 24 | `package/bootc` | `Plugin, Reader, Actor` | `DC, MG, CS, CM, TC` | `TargetNode, TargetImage` | 2 | 9 | 13 | 58.9% | 94.3% | **+35.4%** |
| 25 | `package/dnf` | `Plugin, Actor` | `DC, MG, CS, CM, TC` | `TargetNode` | 3 | 0 | 4 | 0.0% | 91.8% | **+91.8%** |
| 26 | `package/rpmostree` | `Plugin, Actor` | `DC, MG, CS, CM, TC` | `TargetNode` | 3 | 0 | 6 | 0.0% | 89.3% | **+89.3%** |
| 27 | `share/nfs` | `Plugin, Reader, Actor` | `DC, MG, CS, CM, TC` | `TargetShare` | 1 | 0 | 3 | 0.0% | 97.6% | **+97.6%** |
| 28 | `share/smb` | `Plugin, Reader, Actor` | `DC, MG, CS, CM, TC` | `TargetShare` | 3 | 0 | 6 | 0.0% | 83.4% | **+83.4%** |
| 29 | `storage/btrfs` | `Plugin, Reader, Actor` | `DC, MG, CS, CM, TC, TT` | `TargetStorage` | 2 | 1 | 2 | 60.5% | 70.1% | **+9.6%** |
| 30 | `storage/lvm` | `Plugin, Reader, Actor` | `DC, MG, CS, CM, TC, TT` | `TargetStorage` | 3 | 6 | 7 | 83.0% | 85.9% | **+2.9%** |
| 31 | `storage/zfs` | `Plugin, Reader, Actor` | `DC, MG, CS, CM, TC` | `TargetStorage` | 4 | 4 | 7 | 67.6% | 83.6% | **+16.0%** |
| 32 | `system/service` | `Plugin, Reader, Actor` | `DC, MG, CS, CM, TC, TT` | `TargetService` | 9 | 4 | 5 | 60.4% | 67.8% | **+7.4%** |
| 33 | `telemetry/glances` | `Plugin, Reader` | `DC, MG, CS, CM, TC` | `TargetNode` | 3 | 1 | 3 | 24.6% | 87.9% | **+63.3%** |
| 34 | `vm/libvirt` | `Plugin, Reader, Actor` | `DC, MG, CS, CM, TC` | `TargetVM` | 8 | 1 | 2 | 65.4% | 81.8% | **+16.4%** |
| 35 | `vm/lxc` | `Plugin, Reader, Actor` | `DC, MG, CS, CM, TC` | `TargetVM` | 4 | 6 | 7 | 84.0% | 89.6% | **+5.6%** |
| 36 | `vm/proxmox` | `Plugin, Reader, Actor` | `DC, MG, CS, CM, TC` | `TargetVM` | 6 | 15 | 19 | 79.3% | 82.2% | **+2.9%** |
| **Total / Mean** | **36 Plugins** | **36 v1 Plugins** | **36 v2 Plugins** | **11 Target Types** | **115** | **105** | **187** | **49.3%** | **83.8%** | **+34.5%** |

---

## 5. Framework & Core Package Test Coverage Comparison

Beyond the 36 plugin packages, the v2 refactor added 4 new framework packages (`pkg/plugin/newapi`, `pkg/plugin/settingsframework`, `pkg/contextmenu`, `pkg/taskapi`) and expanded coverage in existing daemon packages:

| Go Package | v1 `Test*` Functions | v2 `Test*` Functions | v1 Statement Cov | v2 Statement Cov | Delta |
|---|---:|---:|---:|---:|---:|
| `air-traffic-control/backend` (`main`) | 6 | 7 | 35.6% | 43.9% | **+8.3%** |
| `backend/pkg/plugin` | 26 | 27 | 62.6% | 65.0% | **+2.4%** |
| `backend/pkg/plugin/newapi` | N/A | 6 | N/A | 81.9% | **NEW (81.9%)** |
| `backend/pkg/plugin/settingsframework` | N/A | 4 | N/A | 74.9% | **NEW (74.9%)** |
| `backend/pkg/contextmenu` | N/A | 3 | N/A | 78.3% | **NEW (78.3%)** |
| `backend/pkg/nodeapi` | 39 | 44 | 76.8% | 77.4% | **+0.6%** |
| `backend/pkg/taskapi` | N/A | 4 | N/A | 68.7% | **NEW (68.7%)** |
| `backend/pkg/websocket` | 4 | 4 | 66.5% | 72.2% | **+5.7%** |
| **Plugin Packages Aggregate (36 pkgs)** | **105** | **187** | **49.3% (mean)** | **83.8% (mean)** | **+34.5%** |
| **Plugin Implementation LOC** | 13,516 lines | 21,310 lines | — | — | **+7,794 lines** |
| **Plugin Unit Test LOC** | 4,508 lines | 11,615 lines | — | — | **+7,107 lines (+157.7%)** |

---

## 6. Documented Gaps, Nuances & Follow-Up Recommendations

While all 36 plugins pass verification gates and maintain 100% backward compatibility with v1 callers, this audit identified the following architectural nuances and minor gaps for future iteration:

### Gap 1: Asymmetric `DataCollectionPlugin` Implementation in `elevation/*`
- **Observation:** `elevation/doas` implements `newapi.DataCollectionPlugin` (and `plugin.Reader`) to read live `doas` configuration and binary state, whereas its sibling elevation plugins (`elevation/run0`, `elevation/sudo`, `elevation/none`) implement `ManagementPlugin`, `CategorizedSettingsProvider`, `ContextMenuProvider`, and `TaskCompileProvider` without `DataCollectionPlugin` (relying on `Probe()` and the shared `plugins/elevation` probe helper).
- **Impact:** Low / Non-breaking. Elevation status continues to be reported via `Probe()` (`Detection.Details`) and `/api/nodes/:id/privilege`.
- **Recommendation:** Optionally add `DataCollectionPlugin` (`Read`, `CollectionSchedule`, `CollectionSchema`) to `elevation/run0`, `elevation/sudo`, and `elevation/none` if uniform `GET /api/nodes/:id/plugins/elevation/:name` live state inspection is desired across all four elevation providers.

### Gap 2: `newapi.NodeTypeProvider` and `newapi.TaskTypeProvider` Optional Adoption
- **Observation:**
  - `newapi.NodeTypeProvider` is fully supported by `plugin.Registry` (`Register` indexes `NodeTypes()`) and tested in `pkg/plugin/registry_test.go`, but all 7 standard inventory node types (`linux`, `windows`, `router_switch`, `proxmox`, `container_host`, `storage_appliance`, `kubernetes`) are currently registered as built-in `nodeapi` specs rather than emitted by individual plugins via `NodeTypeProvider`.
  - `newapi.TaskTypeProvider` (`TaskTypes()`) is implemented by 4 plugins (`backup/restic`, `storage/btrfs`, `storage/lvm`, `system/service`), while all 36 plugins implement `newapi.TaskCompileProvider` (`CompileTask`).
- **Impact:** Low / By Design. Both `NodeTypeProvider` and `TaskTypeProvider` are optional extension contracts in `pkg/plugin/newapi` because `nodeapi` and `taskapi` pre-populate standard built-in types (`ansible_playbook`, `plugin_action`, `host_probe`, `backup_snapshot`, `service_control`, `storage_op`, `custom`).
- **Recommendation:** Specialized hypervisor or appliance plugins (e.g., `vm/proxmox`, `package/bootc`) can optionally implement `NodeTypeProvider` if plugin-contributed custom node types are introduced in future phases.

### Gap 3: Per-Node Settings Scope (`SettingsScopeNode`) Utilization
- **Observation:** `settingsframework` and `newapi.CategorizedSettingsProvider` support both `SettingsScopeDatacenter` and `SettingsScopeNode` (`GET /api/settings/categories?scope=node` and `StoreAdapter.WithNodeScope(nodeID)`). Currently, all 36 plugins register their `CategorizedSettingsPage()` under `SettingsScopeDatacenter` (`36` datacenter pages, `0` node-scoped pages), using action options or host facts for per-node overrides.
- **Impact:** Low. Datacenter defaults work as designed and fall back cleanly when per-host parameters are omitted.
- **Recommendation:** Expose per-node settings pages (`SettingsScopeNode`) or dual-scope bindings in the Node Edit / Host Modules UI for plugins where persistent per-host defaults (such as custom `backup/restic` repository paths or `telemetry/glances` ports) are preferable to per-action overrides.

### Gap 4: Frontend Consumption of New v2 Daemon Endpoints
- **Observation:** The Go daemon now serves `/api/settings/categories`, `/api/settings/datacenter`, `/api/context-menus`, `/api/node-types`, and `/api/task-types`. Meanwhile, several React components (`src/components/DatacenterSettingsView.tsx`, `src/components/ContextMenu.tsx`) still primarily consume `/api/settings/pages` or local view-model helpers (`clampMenuPosition`, row builders) alongside plugin metadata.
- **Impact:** None on existing functionality (100% backward compatibility is preserved via `/api/settings/pages` and `/api/plugins`).
- **Recommendation:** Incrementally wire the React frontend to query `/api/settings/categories` for dynamic category grouping in Datacenter Settings and `/api/context-menus/:target` to merge plugin-contributed context menu entries into row right-click menus.

### Gap 5: Worktree Symlink Convenience for `data/ansible-venv`
- **Observation:** When running `npm test` inside a fresh git worktree where `data/ansible-venv` is not present at `<worktree>/data/ansible-venv`, 6 of 626 Vitest tests in `ansibleModuleSpecs.test.ts` and `PlaybookVisualEditor.test.tsx` gracefully skip (`# ansible-doc not found`). Linking `data/` from the main repository checkout causes all `626/626` tests to execute and pass (`0` skipped).
- **Impact:** Developer ergonomics only.
- **Recommendation:** Document `ln -s <main-repo>/data data` alongside `ln -s <main-repo>/node_modules node_modules` in worktree setup instructions.
