# Air Traffic Control — Plugin System Development Guide & API Reference

Air Traffic Control (ATC) discovers, probes, reads, and mutates host capabilities through a **compile-time Go plugin system** in `backend-go/`. There is no dynamic `.so` or WASM runtime loading: every plugin is a Go package under `backend-go/plugins/<kind>/<name>` that registers itself in `init()` and is linked into the daemon via a blank import in `backend-go/plugins/all/all.go`.

This document is the authoritative development guide and API reference for the v2 plugin system (`pkg/plugin`, `pkg/plugin/newapi`, `pkg/plugin/settingsframework`, `pkg/contextmenu`, `pkg/nodeapi`, and `pkg/taskapi`). For migrating existing v1 plugins to the v2 contracts, see [`docs/plugin-migration.md`](./plugin-migration.md).

---

## 1. Architecture & Package Layout

The plugin system is organized as a strict, acyclic dependency graph so plugin packages can adopt v2 features incrementally without circular imports:

```
backend-go/
├── main.go                                 # Daemon entrypoint & HTTP route wiring
├── pkg/
│   ├── plugin/                             # Core contracts, Registry, SettingsStore, HTTP API
│   │   ├── plugin.go                       # Base Plugin, Reader, Actor interfaces
│   │   ├── types.go                        # Kind, Meta, HostFacts, Detection, ActionSpec, TaskSpec, Runner
│   │   ├── settings.go                     # SettingsPage, SettingsField, SettingsPageProvider, Secret masking
│   │   ├── settingsstore.go                # SQLite SettingsStore (global_settings table)
│   │   ├── registry.go                     # Thread-safe Registry (plugins, settings, node types, menus, tasks)
│   │   ├── api.go                          # Core plugin & global settings HTTP handlers
│   │   ├── newapi/                         # v2 modular extension contracts (imports pkg/plugin)
│   │   │   ├── nodetypes.go                # InventoryNodeType, NodeTypeSpec, NodeTypeProvider
│   │   │   ├── settings.go                 # SettingsScope, CategorizedSettingsPage, CategorizedSettingsProvider
│   │   │   ├── contextmenu.go              # ContextMenuTarget, ContextMenuEntry, ContextMenuBuilder, ContextMenuProvider
│   │   │   ├── management.go               # ManagementActionCategory, ManagementActionSpec, ManagementPlugin
│   │   │   ├── datacollection.go           # CollectionSchedule, CollectionSchema, CollectionEnvelope, DataCollectionPlugin
│   │   │   └── tasktypes.go                # TaskType, TaskTypeSpec, TaskRequest, TaskTypeProvider, TaskCompileProvider
│   │   └── settingsframework/              # Fluent page/field builders, scoped StoreAdapter, HTTP Handler
│   ├── contextmenu/                        # Context menu Builder, DefaultRenderer, HTTP API (/api/context-menus)
│   ├── nodeapi/                            # Inventory NodeType system, NodeMetadata, NodeGroup, HTTP API (/api/node-types)
│   └── taskapi/                            # TaskType Registry, Executor, PluginActionExecutor, HTTP API (/api/task-types)
└── plugins/
    ├── all/all.go                          # Blank-import aggregator registering all compile-time plugins
    └── <kind>/<name>/<name>.go             # Individual plugin packages
```

### Core Invariants

1. **Zero-Mock Policy:** `Probe` and `Read` must inspect real host state via the supplied `plugin.Runner`. Never fabricate metrics, fake container/VM lists, or return synthetic success when a command or connection fails. Fail loudly and return an explicit `Detection{Detected: false, Status: "OFFLINE" | "UNAVAILABLE", Error: ...}` or a real Go `error`.
2. **Declarative Ansible Mutations:** Host mutations (`Actor.Compile` / `TaskCompileProvider.CompileTask`) compile into declarative `plugin.TaskSpec` payloads using native Ansible modules (`ansible.builtin.*`, `community.general.*`, `containers.podman.*`, `ansible.posix.*`). Raw `shell`/`command` steps are a last resort only when no native module exists.
3. **Secret Protection Invariant:** Sensitive options and settings fields (`Secret: true` or `SettingsFieldSecret`) are write-only. Plaintext secrets never leave the daemon in HTTP responses or logs; configured secrets are always masked as `•••••••• (vault: <name>)` via `plugin.MaskedSecretValue(name)`.
4. **Compile-Time Registration:** Every plugin calls `_ = plugin.Register(New())` inside `func init()` and is blank-imported in `backend-go/plugins/all/all.go`.

---

## 2. Plugin Kinds & Cache TTL Tiers

Every plugin declares a `plugin.Kind` in `Meta().Kind` that **must** match the `<kind>` prefix of `Meta().ID` (`"<kind>/<name>"`).

| Constant | Value | Default Cache / Poll TTL | Typical Plugins |
|---|---|---|---|
| `KindConnection` | `"connection"` | 60s | `local`, `ssh`, `tailscale` |
| `KindElevation` | `"elevation"` | 60s | `run0`, `sudo`, `doas`, `none` |
| `KindPackage` | `"package"` | 60s | `bootc`, `rpmostree`, `dnf`, `apt`, `apk` |
| `KindContainer` | `"container"` | **20s** | `podman`, `docker` |
| `KindStorage` | `"storage"` | **60s** | `zfs`, `btrfs`, `lvm` |
| `KindShare` | `"share"` | **60s** | `nfs`, `smb` |
| `KindMount` | `"mount"` | **60s** | `rclone` |
| `KindBackup` | `"backup"` | **60s** | `restic` |
| `KindLogs` | `"logs"` | 60s (Realtime) | `journald` |
| `KindNetwork` | `"network"` | **30s** | `interfaces`, `tailscale` |
| `KindFirewall` | `"firewall"` | **45s** | `firewalld`, `ufw`, `nftables`, `iptables` |
| `KindTelemetry` | `"telemetry"` | **15s** (Realtime) | `glances` |
| `KindDisk` | `"disk"` | **60s** | `smart` |
| `KindVM` | `"vm"` | **60s** | `proxmox`, `libvirt`, `lxc` |
| `KindSystem` | `"system"` | 60s | `service` |
| `KindDatabase` | `"database"` | **60s** | `sql`, `nosql` |

---

## 3. Step-by-Step Plugin Development Workflow

### Step 1: Create the Plugin Package

Create `backend-go/plugins/<kind>/<name>/<name>.go` with `package <name>`. Implement `plugin.Plugin` and any applicable v2 extension interfaces from `pkg/plugin/newapi`, `pkg/plugin/settingsframework`, and `pkg/contextmenu`.

```go
package zfs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
)

const (
	PluginID       = "storage/zfs"
	SettingsPageID = "storage/zfs"
)

func init() {
	_ = plugin.Register(New())
}

type Plugin struct{}

func New() *Plugin {
	return &Plugin{}
}

// 1. Base Plugin Contract (Required)

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          PluginID,
		Kind:        plugin.KindStorage,
		Name:        "zfs",
		Label:       "ZFS Storage",
		Icon:        "inventory_2",
		Description: "OpenZFS pools, datasets, snapshots, and background scrub management",
		Order:       10,
	}
}

func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	return h.OSFamily != "windows" && h.OSFamily != "router_switch"
}

func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"zfs"},
			"apt": {"zfsutils-linux"},
			"apk": {"zfs"},
		},
		ControllerCollections: []string{"community.general"},
	}
}

func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{Detected: false, Status: "OFFLINE", Error: "runner is nil"}, nil
	}
	out, err := r.RunCommand(ctx, "command -v zfs && command -v zpool")
	if err != nil || strings.TrimSpace(out) == "" {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Error:    "zfs or zpool command not found",
		}, nil
	}
	return plugin.Detection{Detected: true, Status: "ONLINE"}, nil
}
```

### Step 2: Add Data Collection (`newapi.DataCollectionPlugin`)

If the plugin reads live state from the host, implement `Read` plus `CollectionSchedule()` and `CollectionSchema()` to satisfy `newapi.DataCollectionPlugin`:

```go
func (p *Plugin) CollectionSchedule() newapi.CollectionSchedule {
	return newapi.DefaultScheduleForKind(plugin.KindStorage) // 60s Interval & TTL, 10s Timeout
}

func (p *Plugin) CollectionSchema() newapi.CollectionSchema {
	return newapi.CollectionSchema{
		Version:     "v2",
		RootKey:     "pools",
		Description: "OpenZFS pools, datasets, and mountpoints",
	}
}

func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}
	out, err := r.RunCommand(ctx, "zpool list -H -o name,size,alloc,free,health 2>/dev/null")
	if err != nil {
		return nil, err
	}
	return map[string]any{"zpoolList": strings.TrimSpace(out)}, nil
}
```

### Step 3: Add Management Actions (`newapi.ManagementPlugin`)

If the plugin stages host mutations via Ansible, implement `Actions()`, `ManagementActions()`, `ValidateAction()`, and `Compile()`:

```go
func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "scrub",
				Label:       "Scrub Pool",
				Description: "Initiate or stop a ZFS pool scrub",
				Options: []plugin.OptionSchema{
					{Name: "pool", Type: "string", Required: true, Description: "ZFS pool name"},
					{Name: "state", Type: "select", Enum: []string{"scrubbing", "stopped"}, Default: "scrubbing"},
				},
			},
			Category:         newapi.ActionCategoryMaintenance,
			Idempotent:       true,
			InvalidatesKinds: []plugin.Kind{plugin.KindStorage},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "destroy_dataset",
				Label:       "Destroy Dataset",
				Description: "Permanently delete a ZFS dataset",
				Options: []plugin.OptionSchema{
					{Name: "name", Type: "string", Required: true, Description: "Dataset path"},
				},
			},
			Category:         newapi.ActionCategoryLifecycle,
			Dangerous:        true,
			ConfirmPrompt:    "Permanently destroy this ZFS dataset and all snapshots?",
			InvalidatesKinds: []plugin.Kind{plugin.KindStorage},
		},
	}
}

func (p *Plugin) Actions() []plugin.ActionSpec {
	mgmt := p.ManagementActions()
	out := make([]plugin.ActionSpec, len(mgmt))
	for i, m := range mgmt {
		out[i] = m.ActionSpec
	}
	return out
}

func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, action) {
			return newapi.ValidateManagementOptions(spec, opts)
		}
	}
	return fmt.Errorf("unsupported action %q", action)
}

func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	if err := p.ValidateAction(h, action, opts); err != nil {
		return plugin.TaskSpec{}, err
	}
	// Construct declarative plugin.TaskSpec...
}
```

### Step 4: Add Datacenter / Scoped Settings (`settingsframework.PageBuilder`)

Use `settingsframework.NewPageBuilder` to declare a settings page and implement `SettingsPage()` and `CategorizedSettingsPage()` (`newapi.CategorizedSettingsProvider`):

```go
func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	return settingsframework.NewPageBuilder(SettingsPageID, "ZFS Storage Defaults").
		WithIcon("inventory_2").
		WithCategory("storage").
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(10).
		WithDescription("Datacenter-wide OpenZFS scrub cadence and snapshot retention defaults.").
		StringField("defaultPool", "Default Pool Name", "tank",
			settingsframework.WithDescription("Fallback pool when no per-host pool is specified.")).
		IntField("scrubIntervalDays", "Scrub Interval (Days)", 30,
			settingsframework.WithMinMax(1, 365)).
		BoolField("autoSnapshot", "Enable Automatic Snapshots", true).
		SecretField("encryptionPassphraseVaultVar", "Encryption Passphrase Vault Variable",
			settingsframework.WithPlaceholder("vault_zfs_passphrase")).
		BuildCategorized()
}

func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}
```

To read persisted settings inside `Read()` or `Compile()`, wrap the global store with `settingsframework.NewStoreAdapter(nil)`:

```go
adapter := settingsframework.NewStoreAdapter(nil)
defaultPool := adapter.StringOrDefault(SettingsPageID, "defaultPool", "tank")
scrubDays := adapter.IntOrDefault(SettingsPageID, "scrubIntervalDays", 30)
```

### Step 5: Add Context Menu Entries (`newapi.ContextMenuProvider`)

Implement `ContextMenuEntries(ctx, h)` using `newapi.NewContextMenuBuilder` (or `contextmenu.NewBuilder`):

```go
func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	return newapi.NewContextMenuBuilder(PluginID).
		WithTarget(newapi.TargetStorage).
		WithGroup("maintenance").
		AddAction("zfs-scrub", "Scrub ZFS Pool", "cleaning_services", "scrub", 10).
		AddAction("zfs-snapshot", "Create Snapshot", "photo_camera", "snapshot", 20).
		AddDangerAction("zfs-destroy", "Destroy Dataset", "delete_forever", "destroy_dataset",
			"Permanently destroy this ZFS dataset?", 90).
		Build()
}
```

### Step 6: Register in `plugins/all/all.go` and Write Unit Tests

Add the blank import in `backend-go/plugins/all/all.go`:

```go
import (
	// Storage plugins
	_ "air-traffic-control/backend/plugins/storage/zfs"
)
```

Create `backend-go/plugins/<kind>/<name>/<name>_test.go` to verify registration, compatibility, probing, settings schema validation, context menu generation, and action compilation.

---

## 4. Go Package API Reference

### 4.1 `air-traffic-control/backend/pkg/plugin`

#### Core Interfaces

| Interface | Methods | Purpose |
|---|---|---|
| `Plugin` | `Meta() Meta`<br>`Compatible(h HostFacts) bool`<br>`Requirements() Requirements`<br>`Probe(ctx context.Context, r Runner, h HostFacts) (Detection, error)` | Required base contract for all compile-time plugins. |
| `Reader` | `Read(ctx context.Context, r Runner, h HostFacts) (any, error)` | Optional live state/telemetry reader for `GET /api/nodes/:id/plugins/:kind/:name`. |
| `Actor` | `Actions() []ActionSpec`<br>`Compile(h HostFacts, action string, opts map[string]any) (TaskSpec, error)` | Optional mutation compiler producing declarative Ansible `TaskSpec`s. |
| `Runner` | `RunCommand(ctx context.Context, cmd string) (string, error)` | Transport abstraction passed to `Probe` and `Read`. |
| `Dialer` | `Dial(ctx context.Context, network, address string) (net.Conn, error)` | Optional network connection dialer implemented by connection plugins. |
| `SettingsPageProvider` | `SettingsPage() SettingsPage` | Contributes a settings page schema to the registry. |
| `CategorizedSettingsProvider` | `SettingsPage() SettingsPage`<br>`CategorizedSettingsPage() CategorizedSettingsPage` | Contributes a scoped (`datacenter` or `node`) and categorized settings page. |
| `NodeTypeProvider` | `NodeTypes() []NodeTypeSpec` | Contributes or extends inventory `NodeTypeSpec` definitions. |
| `ContextMenuProvider` | `ContextMenuEntries(ctx context.Context, h HostFacts) []ContextMenuEntry` | Contributes context menu items for compatible hosts/targets. |
| `TaskTypeProvider` | `TaskTypes() []TaskTypeSpec` | Contributes custom or plugin-specific `TaskTypeSpec` definitions. |
| `TaskCompileProvider` | `CompileTask(ctx context.Context, h HostFacts, req TaskRequest) (TaskSpec, error)` | Compiles a typed `TaskRequest` into an Ansible `TaskSpec`. |

#### Registry Functions & Methods (`pkg/plugin/registry.go`)

All functions exist both as methods on `*plugin.Registry` (`plugin.NewRegistry()`) and as package-level functions delegating to `plugin.DefaultRegistry()`:

- **Plugins:**
  - `Register(p Plugin) error` — atomically validates and registers `p` along with any optional `SettingsPageProvider`, `CategorizedSettingsProvider`, `NodeTypeProvider`, `ContextMenuProvider`, and `TaskTypeProvider` interfaces it implements.
  - `Get(id string) (Plugin, bool)`
  - `List() []Plugin`
  - `ListByKind(kind Kind) []Plugin`
  - `FilterCompatible(facts HostFacts) []Plugin`
- **Settings Pages:**
  - `ListSettingsPages() []SettingsPage`
  - `ListCategorizedSettingsPages() []CategorizedSettingsPage`
  - `ListSettingsPagesByScope(scope SettingsScope) []CategorizedSettingsPage`
  - `ListSettingsPagesByCategory(scopeOrCategory string) []CategorizedSettingsPage`
  - `GetSettingsPage(id string) (SettingsPageProvider, bool)`
  - `GetCategorizedSettingsPage(id string) (CategorizedSettingsPage, bool)`
- **Inventory Node Types:**
  - `RegisterNodeType(spec NodeTypeSpec) error`
  - `GetNodeType(t InventoryNodeType) (NodeTypeSpec, bool)`
  - `ListNodeTypes() []NodeTypeSpec`
- **Context Menus:**
  - `RegisterContextMenuProvider(id string, provider ContextMenuProvider) error`
  - `ListContextMenuProviders() []ContextMenuProvider`
  - `ListContextMenuEntries(ctx context.Context, target ContextMenuTarget, h HostFacts) []ContextMenuEntry`
- **Task Types:**
  - `RegisterTaskType(spec TaskTypeSpec) error`
  - `GetTaskType(t TaskType) (TaskTypeSpec, bool)`
  - `ListTaskTypes() []TaskTypeSpec`

---

### 4.2 `air-traffic-control/backend/pkg/plugin/newapi`

`package newapi` provides the modular v2 contracts and validation helpers:

- **Inventory Node Types (`nodetypes.go`):**
  - `InventoryNodeType` constants:
    - `NodeTypeLinux` (`"linux"`)
    - `NodeTypeWindows` (`"windows"`)
    - `NodeTypeRouterSwitch` (`"router_switch"`)
    - `NodeTypeProxmox` (`"proxmox"`)
    - `NodeTypeContainerHost` (`"container_host"`)
    - `NodeTypeStorageAppliance` (`"storage_appliance"`)
    - `NodeTypeKubernetes` (`"kubernetes"`)
  - `DefaultNodeTypes() []NodeTypeSpec`
  - `ValidateNodeTypeSpec(spec NodeTypeSpec) error`
- **Scoped & Categorized Settings (`settings.go`):**
  - `SettingsScope` constants: `SettingsScopeDatacenter` (`"datacenter"`), `SettingsScopeNode` (`"node"`).
  - `CategorizedSettingsPage`, `CategorizedSettingsProvider`
  - `ResolveCategorizedSettingsPage(sp plugin.SettingsPageProvider) CategorizedSettingsPage`
  - `ValidateCategorizedSettingsPage(page CategorizedSettingsPage) error`
- **Context Menus (`contextmenu.go`):**
  - `ContextMenuTarget` constants: `TargetNode` (`"node"`), `TargetContainer` (`"container"`), `TargetVM` (`"vm"`), `TargetStorage` (`"storage"`), `TargetShare` (`"share"`), `TargetNetwork` (`"network"`), `TargetFirewall` (`"firewall"`), `TargetService` (`"service"`), `TargetDatabase` (`"database"`).
  - `NewContextMenuBuilder(pluginID string) ContextMenuBuilder`:
    - `.WithTarget(target ContextMenuTarget)`
    - `.WithGroup(group string)`
    - `.AddEntry(entry ContextMenuEntry)`
    - `.AddAction(id, label, icon, action string, order int)`
    - `.AddDangerAction(id, label, icon, action, confirmPrompt string, order int)`
    - `.AddSubmenu(id, label, icon string, order int, children ...ContextMenuEntry)`
    - `.Build() []ContextMenuEntry`
- **Management Plugins (`management.go`):**
  - `ManagementActionCategory` constants: `ActionCategoryLifecycle` (`"lifecycle"`), `ActionCategoryConfiguration` (`"configuration"`), `ActionCategoryMaintenance` (`"maintenance"`), `ActionCategorySecurity` (`"security"`), `ActionCategoryDiagnostic` (`"diagnostic"`).
  - `ManagementActionSpec` (embeds `plugin.ActionSpec` + `Category`, `Idempotent`, `Dangerous`, `ConfirmPrompt`, `InvalidatesKinds`).
  - `ManagementPlugin` interface (`plugin.Plugin` + `plugin.Actor` + `ManagementActions() []ManagementActionSpec` + `ValidateAction(h, action, opts) error`).
  - `ValidateManagementOptions(spec ManagementActionSpec, opts map[string]any) error`.
- **Data Collection Plugins (`datacollection.go`):**
  - `CollectionSchedule` (`Interval`, `TTL`, `Timeout`, `Realtime`), `CollectionSchema` (`Version`, `RootKey`, `Description`), `CollectionEnvelope`.
  - `DataCollectionPlugin` interface (`plugin.Plugin` + `plugin.Reader` + `CollectionSchedule()` + `CollectionSchema()`).
  - `DefaultScheduleForKind(kind plugin.Kind) CollectionSchedule`.
- **Task Types (`tasktypes.go`):**
  - `TaskType` constants: `TaskTypeAnsiblePlaybook` (`"ansible_playbook"`), `TaskTypePluginAction` (`"plugin_action"`), `TaskTypeHostProbe` (`"host_probe"`), `TaskTypeBackupSnapshot` (`"backup_snapshot"`), `TaskTypeServiceControl` (`"service_control"`), `TaskTypeStorageOp` (`"storage_op"`), `TaskTypeCustom` (`"custom"`).
  - `TaskTypeSpec`, `TaskRequest`, `TaskTypeProvider`, `TaskCompileProvider`.
  - `ValidateTaskRequest(spec TaskTypeSpec, req TaskRequest) error`.

---

### 4.3 `air-traffic-control/backend/pkg/plugin/settingsframework`

- **Fluent Builders (`builder.go`):**
  - `NewPageBuilder(id, title string) *PageBuilder`
  - Page configuration methods: `.WithIcon(icon)`, `.WithDescription(desc)`, `.WithOrder(order)`, `.WithCategory(cat)`, `.WithScope(scope)`
  - Field builder methods on `*PageBuilder` (and standalone constructors):
    - `StringField(name, label, defaultVal string, opts ...FieldOption)`
    - `TextField(name, label, defaultVal string, opts ...FieldOption)`
    - `IntField(name, label string, defaultVal int, opts ...FieldOption)`
    - `BoolField(name, label string, defaultVal bool, opts ...FieldOption)`
    - `SelectField(name, label, defaultVal string, options []plugin.SettingsFieldOption, opts ...FieldOption)`
    - `SecretField(name, label string, opts ...FieldOption)`
  - Functional `FieldOption`s: `WithDescription(s)`, `WithPlaceholder(s)`, `WithDefault(v)`, `WithRequired(b)`, `WithMin(n)`, `WithMax(n)`, `WithMinMax(min, max)`.
  - `.Build() plugin.SettingsPage` and `.BuildCategorized() newapi.CategorizedSettingsPage`.
- **Persistence & Typed Accessors (`store.go`):**
  - `Store` interface (`Load`, `Save`, `Value`, `Close`), `MemoryStore` (`NewMemoryStore()`), `ScopedKey(scope, targetID, pageID)`.
  - `StoreAdapter` (`NewStoreAdapter(store)` / `NewSQLiteStoreAdapter(dbPath)`):
    - `.StringOrDefault(pageID, field, defaultVal string) string`
    - `.IntOrDefault(pageID, field string, defaultVal int) int`
    - `.BoolOrDefault(pageID, field string, defaultVal bool) bool`
    - `.SecretValue(pageID, field string) (string, bool)` (returns unmasked secret for internal backend use, ignoring masks)
    - `.EffectiveValues(page plugin.SettingsPage) (map[string]any, error)`
    - `.LoadScoped(scope, targetID, pageID)` / `.SaveScoped(scope, targetID, pageID, values)`
    - `.ValidateAndSave(page, input)` / `.ValidateAndSaveScoped(scope, targetID, page, input)`
- **Validation, Rendering & HTTP (`render.go`):**
  - `ValidateSchema(page plugin.SettingsPage) error`
  - `CoerceValues(page plugin.SettingsPage, input map[string]any) (map[string]any, error)`
  - `MergeDefaults(page plugin.SettingsPage, stored map[string]any, maskSecrets bool) map[string]any`
  - `RenderPageView`, `RenderCategorizedView`, `RenderCategoryGroups`
  - `NewHandler(reg *plugin.Registry, store Store) *Handler` (`RegisterRoutes(mux)`).

---

### 4.4 `air-traffic-control/backend/pkg/contextmenu`

- **Types (`types.go`):**
  - `MenuTarget` (`TargetNode`, `TargetContainer`, `TargetVM`, `TargetStorage`, `TargetShare`, `TargetNetwork`, `TargetFirewall`, `TargetService`, `TargetDatabase`), `AllTargets()`, `ValidateTarget(t)`, `ParseTarget(raw)`.
  - `MenuItemKind` (`ItemKindAction`, `ItemKindSubmenu`, `ItemKindSeparator`).
  - `MenuItem`, `MenuSection`, `RenderedMenu`, `RenderRequest`.
  - Conversion helpers: `FromNewAPIEntry(e newapi.ContextMenuEntry) MenuItem`, `ToNewAPIEntry(item MenuItem) newapi.ContextMenuEntry`.
- **Builder (`builder.go`):**
  - `NewBuilder(pluginID string, target ...MenuTarget) *Builder`
  - `.WithTarget(t)`, `.WithGroup(g)`, `.AddItem(item)`, `.AddAction(id, label, icon, action, order)`, `.AddDangerAction(id, label, icon, action, confirmPrompt, order)`, `.AddDisabledAction(id, label, icon, action, reason, order)`, `.AddSubmenu(id, label, icon, order, children...)`, `.AddSeparator(order)`, `.AddEntries(entries...)`
  - `.Build() []MenuItem`, `.BuildEntries() []newapi.ContextMenuEntry`, `.BuildSections() []MenuSection`.
- **Renderer & HTTP API (`renderer.go`):**
  - `NewRenderer(RendererOptions{Registry, NodeResolver}) *DefaultRenderer`
  - `.RegisterProvider(id, provider)`, `.RegisterStaticItems(items...)`, `.Render(ctx, req) (RenderedMenu, error)`
  - `NewAPI(renderer).Register(mux)` mounts `GET /api/context-menus` and `GET /api/context-menus/:target`.

---

### 4.5 `air-traffic-control/backend/pkg/nodeapi`

- **Inventory Node Types (`types.go`):**
  - `NodeType` (`linux`, `windows`, `router_switch`, `proxmox`, `container_host`, `storage_appliance`, `kubernetes`).
  - `NodeMetadata` (extends legacy `types.Node` with `NodeType`, `OSFamily`, `ConnectionPlugin`, `ElevationPlugin`, `Tags`, `Labels`, `Capabilities`).
  - `NodeGroup` (`GroupByGroup`, `GroupByDatacenter`, `GroupByNodeType`, `GroupByStatus`, `GroupByTag`, `GroupByOSFamily`).
  - Functions:
    - `AllNodeTypes() []NodeType`
    - `BuiltinNodeTypeSpecs() []newapi.NodeTypeSpec`
    - `MergeNodeTypeSpecs(extra ...[]newapi.NodeTypeSpec) []newapi.NodeTypeSpec`
    - `LookupNodeTypeSpec(t NodeType, extra ...[]newapi.NodeTypeSpec) (newapi.NodeTypeSpec, bool)`
    - `ValidateNodeType(t NodeType, extra ...[]newapi.NodeTypeSpec) error`
    - `NormalizeNodeType(raw string, extra ...[]newapi.NodeTypeSpec) (NodeType, error)`
    - `InferNodeType(n types.Node) NodeType`
    - `FromTypesNode(n types.Node, extra ...[]newapi.NodeTypeSpec) NodeMetadata`
    - `(m NodeMetadata) ToTypesNode() types.Node`
    - `FilterNodesByType(nodes []NodeMetadata, t NodeType) []NodeMetadata`
    - `GroupNodes(nodes []NodeMetadata, by string) []NodeGroup`
    - `GroupTypesNodes(nodes []types.Node, by string, extra ...[]newapi.NodeTypeSpec) []NodeGroup`
    - `NewHandler(extraProvider func() []newapi.NodeTypeSpec) http.HandlerFunc`

---

### 4.6 `air-traffic-control/backend/pkg/taskapi`

- **Types & Validation (`types.go`):**
  - `TaskType` (`ansible_playbook`, `plugin_action`, `host_probe`, `backup_snapshot`, `service_control`, `storage_op`, `custom`), `AllTaskTypes()`, `DefaultTaskTypes()`.
  - `TaskParams` (`ID`, `Type`, `NodeID`, `PluginID`, `Action`, `Options`, `DryRun`, `Become`, `BecomeMethod`, `SecretsMasked`):
    - `.StringOption(name)`, `.IntOption(name)`, `.BoolOption(name)`
    - `.ApplyDefaults(spec TaskTypeSpec)` (populates schema defaults and populates `SecretsMasked`)
    - `.ToRequest() newapi.TaskRequest`, `FromRequest(req newapi.TaskRequest) TaskParams`
  - `TaskResult` (`Success`, `TaskID`, `Type`, `NodeID`, `PluginID`, `Action`, `DryRun`, `Changed`, `Compiled`, `Output`, `Error`, `DurationMs`).
- **Executors & Registry (`executor.go`, `registry.go`):**
  - `Executor` interface (`Type() TaskType`, `Compile(ctx, facts, params) (plugin.TaskSpec, error)`, `Execute(ctx, facts, params) (TaskResult, error)`).
  - `NewFuncExecutor(taskType, compileFn, executeFn)`
  - `NewPluginActionExecutor(pluginRegistry, taskType)` (delegates to `plugin.Actor.Compile` or `newapi.TaskCompileProvider.CompileTask`).
  - `NewRegistry(pluginRegistry ...*plugin.Registry) *Registry`:
    - `.RegisterType(spec)`, `.RegisterExecutor(exec)`, `.SyncFromPlugins()`
    - `.GetType(t, pluginID...)`, `.ListTypes()`, `.GetExecutor(t)`
    - `.Compile(ctx, facts, params)`, `.Execute(ctx, facts, params)`
  - `NewAPI(APIOptions{Registry, PluginRegistry, NodeResolver}).Register(mux)`.

---

## 5. Daemon HTTP API Reference

All plugin system routes are mounted on the Go daemon (`backend-go/main.go`):

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/plugins` | List all registered compile-time plugins (`PluginSummary`: metadata, requirements, actions, `canRead`, `canAct`). |
| `GET` | `/api/nodes/:id/plugins` | Probe and return active/detected plugins on `:id` (cached per `Kind` TTL). |
| `GET` | `/api/nodes/:id/modules` | Return all plugins classified as `active`, `available`, or `unsupported` for `:id`. |
| `POST` | `/api/nodes/:id/plugins/refresh` | Invalidate cached plugin probe detections for `:id`. |
| `GET` | `/api/nodes/:id/plugins/:kind/:name` | Invoke `Reader.Read` on plugin `<kind>/<name>` for `:id`. |
| `POST` | `/api/nodes/:id/plugins/:kind/:name/actions/:action` | Validate options and invoke `Actor.Compile` on `<kind>/<name>`, returning a declarative `TaskSpec` and invalidating cache. |
| `POST` | `/api/nodes/:id/exec` | Execute a command on `:id` via its resolved connection runner. |
| `GET` | `/api/settings/pages` | List all registered global settings pages with current (secret-masked) values. |
| `GET` | `/api/settings/pages/:id` | Get a single settings page schema and values (`:id` may contain `/`, e.g. `backup/restic`). |
| `PUT` | `/api/settings/pages/:id` | Validate, coerce, and persist `{ "values": { ... } }` for `:id`. |
| `GET` | `/api/settings/categories` | List categorized settings groups (`?scope=datacenter\|node&nodeId=...`). |
| `GET` | `/api/settings/datacenter` | List all datacenter-scoped `CategorizedPageView` items. |
| `GET` | `/api/settings/datacenter/:id` | Get a datacenter-scoped `CategorizedPageView` by ID. |
| `PUT` | `/api/settings/datacenter/:id` | Validate and persist settings for datacenter page `:id`. |
| `GET` | `/api/context-menus` | Render context menu sections and items (`?target=node&nodeId=...&resourceId=...&pluginId=...`). |
| `GET` | `/api/context-menus/targets` | Return all valid `MenuTarget` identifiers. |
| `GET` | `/api/context-menus/:target` | Render context menu for `:target` (`?nodeId=...&resourceId=...&pluginId=...`). |
| `GET` | `/api/node-types` | List merged built-in and plugin-contributed `NodeTypeSpec` items (`?osFamily=...`). |
| `GET` | `/api/task-types` | List all registered `TaskTypeSpec` items (`?type=...&pluginId=...`). |
| `GET` | `/api/task-types/:type` | Get a specific `TaskTypeSpec` (`?pluginId=...`). |
| `POST` | `/api/task-types/execute` | Validate and compile/execute a typed `TaskParams` / `TaskRequest`, returning `TaskResult`. |

---

## 6. Verification Gates

Every plugin addition or modification must pass all four verification gates with zero errors:

```bash
cd backend-go && TMPDIR=/tmp go test -v ./... && cd ..
npm test
npm run lint
npm run build
```
