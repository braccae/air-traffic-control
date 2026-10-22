# Air Traffic Control — Plugin Migration Guide (v1 to v2)

This guide explains how to migrate existing Air Traffic Control Go plugins in `backend-go/plugins/<kind>/<name>/` from the legacy v1 interface set (`plugin.Plugin`, `plugin.Reader`, `plugin.Actor`, raw `plugin.SettingsPageProvider`) to the modular v2 plugin system (`pkg/plugin/newapi`, `pkg/plugin/settingsframework`, `pkg/contextmenu`, `pkg/nodeapi`, and `pkg/taskapi`).

For the full v2 architecture and API reference, see [`docs/plugin-system.md`](./plugin-system.md).

---

## 1. Backward Compatibility Guarantee

The v2 refactor is **100% backward-compatible** at the registry and HTTP layers:

- `plugin.Register(New())` continues to accept any `plugin.Plugin` in `func init()`.
- Existing `plugin.Reader`, `plugin.Actor`, and `plugin.SettingsPageProvider` methods continue to work unchanged and are required for legacy routes (`/api/plugins`, `/api/nodes/:id/plugins/:kind/:name`, `/api/settings/pages`).
- When a plugin implements v2 extension interfaces (`newapi.ManagementPlugin`, `newapi.DataCollectionPlugin`, `newapi.CategorizedSettingsProvider`, `newapi.ContextMenuProvider`, `newapi.NodeTypeProvider`, `newapi.TaskTypeProvider`, `newapi.TaskCompileProvider`), `plugin.Registry` automatically indexes those capabilities at registration time.

**Rule when porting:** Always **keep** existing `Meta`, `Compatible`, `Requirements`, `Probe`, `Read`, `Actions`, `Compile`, and `SettingsPage` methods intact while adding or upgrading them to the v2 interfaces and builders so no existing caller or test breaks.

---

## 2. Capability Mapping: v1 vs v2

| Capability | v1 (Legacy) | v2 (New System) |
|---|---|---|
| **Base Identity & Probe** | `plugin.Plugin` (`Meta`, `Compatible`, `Requirements`, `Probe`) | `plugin.Plugin` (unchanged; required by all v2 interfaces) |
| **Live Data / Telemetry** | `plugin.Reader` (`Read(ctx, r, h)`) | `newapi.DataCollectionPlugin` (`Read` + `CollectionSchedule()` + `CollectionSchema()`) |
| **Host Mutations** | `plugin.Actor` (`Actions()`, `Compile(h, action, opts)`) | `newapi.ManagementPlugin` (`Actions`, `Compile` + `ManagementActions()`, `ValidateAction(h, action, opts)`) |
| **Settings Pages** | Manual `plugin.SettingsPage` struct literal + direct `plugin.GlobalSettingsStore().Value(...)` | `newapi.CategorizedSettingsProvider` built via `settingsframework.NewPageBuilder(...)` + `settingsframework.NewStoreAdapter(nil)` |
| **Context Menus** | Hardcoded in React frontend components | `newapi.ContextMenuProvider` (`ContextMenuEntries(ctx, h)`) built via `newapi.NewContextMenuBuilder(PluginID)` |
| **Inventory Node Types** | Hardcoded string checks (`h.OSFamily`) | `newapi.NodeTypeProvider` (`NodeTypes() []newapi.NodeTypeSpec`) + `nodeapi.NodeType` constants |
| **Task Execution** | Ad-hoc action strings | `newapi.TaskTypeProvider` (`TaskTypes()`) + `newapi.TaskCompileProvider` (`CompileTask(ctx, h, req)`) |

---

## 3. Step-by-Step Migration Recipes

### 3.1 Migrating Reader Plugins to `newapi.DataCollectionPlugin`

Any plugin that implements `Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error)` should also implement `newapi.DataCollectionPlugin` by adding `CollectionSchedule()` and `CollectionSchema()`.

**Before (v1):**

```go
func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	// ...
}
```

**After (v2):**

```go
var _ newapi.DataCollectionPlugin = (*Plugin)(nil)

func (p *Plugin) CollectionSchedule() newapi.CollectionSchedule {
	return newapi.DefaultScheduleForKind(p.Meta().Kind)
}

func (p *Plugin) CollectionSchema() newapi.CollectionSchema {
	return newapi.CollectionSchema{
		Version:     "v2",
		RootKey:     "items",
		Description: p.Meta().Description,
	}
}

func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	// Keep existing Read logic and return shape intact for API parity
}
```

---

### 3.2 Migrating Actor Plugins to `newapi.ManagementPlugin`

Any plugin that implements `Actions() []plugin.ActionSpec` and `Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error)` should upgrade to `newapi.ManagementPlugin`:

1. Define rich action specifications in `ManagementActions() []newapi.ManagementActionSpec` with `Category` (`ActionCategoryLifecycle`, `ActionCategoryConfiguration`, `ActionCategoryMaintenance`, `ActionCategorySecurity`, `ActionCategoryDiagnostic`), `Idempotent`, `Dangerous`, `ConfirmPrompt`, and `InvalidatesKinds`.
2. Derive `Actions() []plugin.ActionSpec` directly from `ManagementActions()` so there is a single source of truth.
3. Implement `ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error` using `newapi.ValidateManagementOptions`.

**Before (v1):**

```go
func (p *Plugin) Actions() []plugin.ActionSpec {
	return []plugin.ActionSpec{
		{
			Name:  "scrub",
			Label: "Scrub Pool",
			Options: []plugin.OptionSchema{
				{Name: "pool", Type: "string", Required: true},
				{Name: "state", Type: "select", Enum: []string{"scrubbing", "stopped"}, Default: "scrubbing"},
			},
		},
	}
}
```

**After (v2):**

```go
var _ newapi.ManagementPlugin = (*Plugin)(nil)

func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "scrub",
				Label:       "Scrub Pool",
				Description: "Initiate or stop a storage pool data integrity scrub",
				Options: []plugin.OptionSchema{
					{Name: "pool", Type: "string", Required: true, Description: "ZFS pool name"},
					{Name: "state", Type: "select", Enum: []string{"scrubbing", "stopped"}, Default: "scrubbing", Description: "Scrub status"},
				},
			},
			Category:         newapi.ActionCategoryMaintenance,
			Idempotent:       true,
			InvalidatesKinds: []plugin.Kind{plugin.KindStorage},
		},
	}
}

func (p *Plugin) Actions() []plugin.ActionSpec {
	mgmt := p.ManagementActions()
	specs := make([]plugin.ActionSpec, len(mgmt))
	for i, m := range mgmt {
		specs[i] = m.ActionSpec
	}
	return specs
}

func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, action) {
			return newapi.ValidateManagementOptions(spec, opts)
		}
	}
	return fmt.Errorf("unsupported %s action: %s", p.Meta().Name, action)
}
```

---

### 3.3 Migrating Settings Pages to `settingsframework` & `CategorizedSettingsProvider`

Replace verbose `plugin.SettingsPage` struct literals and manual `plugin.GlobalSettingsStore()` lookups with `settingsframework.NewPageBuilder` and `settingsframework.NewStoreAdapter(nil)`.

**Before (v1):**

```go
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return plugin.SettingsPage{
		ID:    "backup/restic",
		Title: "Restic Backup Defaults",
		Icon:  "cloud_sync",
		Order: 20,
		Fields: []plugin.SettingsField{
			{
				Name:     "repoPath",
				Label:    "Default Repository",
				Type:     plugin.SettingsFieldString,
				Default:  "/var/backups/restic",
				Required: true,
			},
			{
				Name:   "passwordVaultVar",
				Label:  "Repository Password Vault Variable",
				Type:   plugin.SettingsFieldSecret,
				Secret: true,
			},
		},
	}
}

func globalDefaultRepo() string {
	store := plugin.GlobalSettingsStore()
	if store == nil {
		return ""
	}
	v, _ := store.Value("backup/restic", "repoPath")
	return strings.TrimSpace(v)
}
```

**After (v2):**

```go
var _ newapi.CategorizedSettingsProvider = (*Plugin)(nil)

func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	return settingsframework.NewPageBuilder("backup/restic", "Restic Backup Defaults").
		WithIcon("cloud_sync").
		WithCategory("backup").
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(20).
		WithDescription("Datacenter-wide repository and retention defaults applied to every node unless a per-host override is configured.").
		StringField("repoPath", "Default Repository", DefaultRepoPath,
			settingsframework.WithRequired(true),
			settingsframework.WithPlaceholder("/var/backups/restic or s3:https://..."),
			settingsframework.WithDescription("Restic repository path or URL used when a node has no per-host override.")).
		IntField("retentionDaily", "Daily Snapshots Kept", 7,
			settingsframework.WithMinMax(0, 365),
			settingsframework.WithDescription("Snapshots retained by the daily forget policy.")).
		SecretField("passwordVaultVar", "Repository Password Vault Variable",
			settingsframework.WithPlaceholder("vault_restic_password"),
			settingsframework.WithDescription("Ansible Vault variable name holding the repository password. The password itself is never stored here.")).
		BuildCategorized()
}

func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

func globalDefaultRepo() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault("backup/restic", "repoPath", "")
}
```

> **Note:** Plugins that did not previously expose a settings page can add a `CategorizedSettingsPage()` + `SettingsPage()` when they have meaningful datacenter-wide defaults (e.g., scrub schedules, default ports, retention policies, package update policies). Ensure the page `ID` matches `Meta().ID` (`"<kind>/<name>"`) so there are no ID collisions.

---

### 3.4 Adding Context Menu Support (`newapi.ContextMenuProvider`)

Ported plugins that expose actions or inspection operations should implement `newapi.ContextMenuProvider` so the UI and `/api/context-menus` endpoint can surface right-click actions for nodes and resources:

```go
var _ newapi.ContextMenuProvider = (*Plugin)(nil)

func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	return newapi.NewContextMenuBuilder(p.Meta().ID).
		WithTarget(newapi.TargetStorage). // or TargetNode, TargetContainer, TargetVM, TargetFirewall, TargetService, TargetDatabase, TargetShare, TargetNetwork
		WithGroup(string(p.Meta().Kind)).
		AddAction("scrub", "Scrub Pool", "cleaning_services", "scrub", 10).
		AddAction("snapshot", "Create Snapshot", "photo_camera", "snapshot", 20).
		AddDangerAction("destroy_dataset", "Destroy Dataset", "delete_forever", "destroy_dataset",
			"Permanently delete this dataset and its contents?", 90).
		Build()
}
```

#### Selecting the Right `ContextMenuTarget`

| Plugin Kind | Recommended `ContextMenuTarget` |
|---|---|
| `connection`, `elevation`, `package`, `telemetry`, `logs`, `disk`, `backup`, `mount` | `newapi.TargetNode` |
| `container` (`podman`, `docker`) | `newapi.TargetContainer` (and/or `newapi.TargetNode`) |
| `vm` (`proxmox`, `libvirt`, `lxc`) | `newapi.TargetVM` |
| `storage` (`zfs`, `btrfs`, `lvm`) | `newapi.TargetStorage` |
| `share` (`nfs`, `smb`) | `newapi.TargetShare` |
| `network` (`interfaces`, `tailscale`) | `newapi.TargetNetwork` |
| `firewall` (`firewalld`, `ufw`, `nftables`, `iptables`) | `newapi.TargetFirewall` |
| `system` (`service`) | `newapi.TargetService` |
| `database` (`sql`, `nosql`) | `newapi.TargetDatabase` |

---

### 3.5 Adding Task Type & Task Compilation Support (`newapi.TaskTypeProvider` & `newapi.TaskCompileProvider`)

Plugins that compile mutations (`Actor`) can also implement `newapi.TaskCompileProvider` (and optionally `newapi.TaskTypeProvider`) so `POST /api/task-types/execute` can dispatch typed task requests directly to the plugin:

```go
var _ newapi.TaskCompileProvider = (*Plugin)(nil)

func (p *Plugin) CompileTask(ctx context.Context, h plugin.HostFacts, req newapi.TaskRequest) (plugin.TaskSpec, error) {
	action := strings.TrimSpace(req.Action)
	if action == "" {
		if a, ok := req.Params["action"].(string); ok {
			action = strings.TrimSpace(a)
		}
	}
	if action == "" {
		return plugin.TaskSpec{}, fmt.Errorf("action is required for plugin %s", p.Meta().ID)
	}
	return p.Compile(h, action, req.Params)
}
```

If the plugin contributes a specialized `TaskTypeSpec` (for example, `TaskTypeStorageOp` on `storage/*`, `TaskTypeBackupSnapshot` on `backup/restic`, or `TaskTypeServiceControl` on `system/service`), also implement `newapi.TaskTypeProvider`:

```go
var _ newapi.TaskTypeProvider = (*Plugin)(nil)

func (p *Plugin) TaskTypes() []newapi.TaskTypeSpec {
	return []newapi.TaskTypeSpec{
		{
			Type:           newapi.TaskTypeStorageOp,
			PluginID:       p.Meta().ID,
			Label:          "ZFS Storage Operation",
			Description:    "Run ZFS pool scrub, dataset snapshot, or dataset lifecycle operations",
			Icon:           p.Meta().Icon,
			SupportsDryRun: true,
			SupportsCancel: true,
		},
	}
}
```

---

### 3.6 Contributing Inventory Node Types (`newapi.NodeTypeProvider`)

Plugins that define or specialize a host archetype (for example, `vm/proxmox` -> `NodeTypeProxmox`, `container/podman` -> `NodeTypeContainerHost`, `storage/zfs` -> `NodeTypeStorageAppliance`) can optionally implement `newapi.NodeTypeProvider`. When doing so, ensure `Type`, `Label`, and `DefaultOSFamily` are non-empty so `newapi.ValidateNodeTypeSpec` passes during `plugin.Register`.

---

## 4. Porting Inventory (All 36 Plugins)

Every compile-time plugin in `backend-go/plugins/` is registered in `backend-go/plugins/all/all.go`:

| Category (`Kind`) | Plugins to Port | Primary v2 Interfaces |
|---|---|---|
| `storage` | `storage/zfs`, `storage/btrfs`, `storage/lvm` | `DataCollectionPlugin`, `ManagementPlugin`, `CategorizedSettingsProvider`, `ContextMenuProvider`, `TaskCompileProvider` |
| `container` | `container/podman`, `container/docker` | `DataCollectionPlugin`, `ManagementPlugin`, `CategorizedSettingsProvider`, `ContextMenuProvider`, `TaskCompileProvider` |
| `firewall` | `firewall/firewalld`, `firewall/ufw`, `firewall/nftables`, `firewall/iptables` | `DataCollectionPlugin`, `ManagementPlugin`, `CategorizedSettingsProvider`, `ContextMenuProvider`, `TaskCompileProvider` |
| `database` | `database/sql`, `database/nosql` | `DataCollectionPlugin`, `ManagementPlugin`, `CategorizedSettingsProvider`, `ContextMenuProvider`, `TaskCompileProvider` |
| `network` | `network/tailscale`, `network/interfaces` | `DataCollectionPlugin`, `ManagementPlugin`, `CategorizedSettingsProvider`, `ContextMenuProvider`, `TaskCompileProvider` |
| `mount` | `mount/rclone` | `DataCollectionPlugin`, `ManagementPlugin`, `CategorizedSettingsProvider`, `ContextMenuProvider`, `TaskCompileProvider` |
| `backup` | `backup/restic` | `DataCollectionPlugin`, `ManagementPlugin`, `CategorizedSettingsProvider`, `ContextMenuProvider`, `TaskCompileProvider` |
| `vm` | `vm/proxmox`, `vm/libvirt`, `vm/lxc` | `DataCollectionPlugin`, `ManagementPlugin`, `CategorizedSettingsProvider`, `ContextMenuProvider`, `TaskCompileProvider` |
| `share` | `share/nfs`, `share/smb` | `DataCollectionPlugin`, `ManagementPlugin`, `CategorizedSettingsProvider`, `ContextMenuProvider`, `TaskCompileProvider` |
| `package` | `package/bootc`, `package/rpmostree`, `package/dnf`, `package/apt`, `package/apk` | `DataCollectionPlugin`, `ManagementPlugin`, `CategorizedSettingsProvider`, `ContextMenuProvider`, `TaskCompileProvider` |
| `telemetry` | `telemetry/glances` | `DataCollectionPlugin`, `CategorizedSettingsProvider`, `ContextMenuProvider` |
| `logs` | `logs/journald` | `DataCollectionPlugin`, `CategorizedSettingsProvider`, `ContextMenuProvider` |
| `system` | `system/service` | `DataCollectionPlugin`, `ManagementPlugin`, `CategorizedSettingsProvider`, `ContextMenuProvider`, `TaskCompileProvider` |
| `disk` | `disk/smart` | `DataCollectionPlugin`, `CategorizedSettingsProvider`, `ContextMenuProvider` |
| `connection` | `connection/ssh`, `connection/tailscale`, `connection/local` | `Plugin`, `Dialer` / `NewRunner`, `CategorizedSettingsProvider`, `ContextMenuProvider` |
| `elevation` | `elevation/run0`, `elevation/sudo`, `elevation/doas`, `elevation/none` | `Plugin`, `ManagementPlugin`, `CategorizedSettingsProvider`, `ContextMenuProvider`, `TaskCompileProvider` |

---

## 5. Migration Checklist & Parity Verification

Before marking any plugin port complete, verify every item below:

1. **Compile-Time Interface Assertions:** Add `var _ newapi.<Interface> = (*Plugin)(nil)` assertions in `<name>.go` so missing methods fail at compile time.
2. **Preserve Existing Signatures & JSON Keys:** Do not rename existing `Meta().ID`, action names, option names, settings field names, or `Read()` JSON keys — existing unit tests and frontend views depend on them.
3. **Settings Page Validation:** If the plugin implements `SettingsPageProvider` / `CategorizedSettingsProvider`, ensure `ID` is unique (`"<kind>/<name>"`), `Title` is non-empty, `Scope` is `"datacenter"` or `"node"`, and `Category` is non-empty. Remember that `plugin.Register` validates settings pages, node types, and task types atomically and rejects the entire plugin if any schema is invalid.
4. **Unit Test Coverage:** Update `<name>_test.go` to test:
   - Registration in `plugin.DefaultRegistry()` (`plugin.Get("<kind>/<name>")`)
   - `newapi` interface implementation (`ManagementPlugin`, `DataCollectionPlugin`, `CategorizedSettingsProvider`, `ContextMenuProvider`, `TaskCompileProvider`)
   - `CategorizedSettingsPage()` validation via `settingsframework.ValidateSchema` and `newapi.ValidateCategorizedSettingsPage`
   - `ContextMenuEntries(ctx, facts)` non-empty output for compatible hosts
   - `Probe`, `Read`, and `Compile` / `CompileTask` parity with v1 behavior
5. **Run the Four Verification Gates:**
   ```bash
   cd backend-go && TMPDIR=/tmp go test -v ./... && cd ..
   npm test
   npm run lint
   npm run build
   ```
