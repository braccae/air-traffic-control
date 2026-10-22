package plugin_test

import (
	"context"
	"errors"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
)

// fakeRunner implements plugin.Runner for testing
type fakeRunner struct {
	cmdHandler func(cmd string) (string, error)
}

func (f *fakeRunner) RunCommand(ctx context.Context, cmd string) (string, error) {
	if f.cmdHandler != nil {
		return f.cmdHandler(cmd)
	}
	return "", nil
}

// fakePlugin implements plugin.Plugin
type fakePlugin struct {
	meta         plugin.Meta
	compatibleFn func(h plugin.HostFacts) bool
	reqs         plugin.Requirements
	probeFn      func(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error)
}

func (p *fakePlugin) Meta() plugin.Meta {
	return p.meta
}

func (p *fakePlugin) Compatible(h plugin.HostFacts) bool {
	if p.compatibleFn != nil {
		return p.compatibleFn(h)
	}
	return true
}

func (p *fakePlugin) Requirements() plugin.Requirements {
	return p.reqs
}

func (p *fakePlugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if p.probeFn != nil {
		return p.probeFn(ctx, r, h)
	}
	return plugin.Detection{Detected: true, Status: "ONLINE"}, nil
}

// fakeReader implements plugin.Reader
type fakeReaderPlugin struct {
	fakePlugin
	readFn func(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error)
}

func (p *fakeReaderPlugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if p.readFn != nil {
		return p.readFn(ctx, r, h)
	}
	return map[string]string{"status": "ok"}, nil
}

// fakeActor implements plugin.Actor
type fakeActorPlugin struct {
	fakePlugin
	actions   []plugin.ActionSpec
	compileFn func(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error)
}

func (p *fakeActorPlugin) Actions() []plugin.ActionSpec {
	return p.actions
}

func (p *fakeActorPlugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	if p.compileFn != nil {
		return p.compileFn(h, action, opts)
	}
	return plugin.TaskSpec{
		Name: action,
		Steps: []plugin.TaskStep{
			{Name: "step 1", Module: "ansible.builtin.debug", Params: opts},
		},
	}, nil
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := plugin.NewRegistry()

	p := &fakePlugin{
		meta: plugin.Meta{
			ID:    "storage/zfs",
			Kind:  plugin.KindStorage,
			Name:  "zfs",
			Label: "ZFS Storage",
		},
	}

	err := reg.Register(p)
	if err != nil {
		t.Fatalf("unexpected error registering plugin: %v", err)
	}

	got, ok := reg.Get("storage/zfs")
	if !ok {
		t.Fatalf("expected plugin storage/zfs to be found")
	}
	if got.Meta().ID != "storage/zfs" {
		t.Errorf("got ID %q, want %q", got.Meta().ID, "storage/zfs")
	}

	// Duplicate registration guard
	err = reg.Register(p)
	if err == nil {
		t.Fatalf("expected error on duplicate registration, got nil")
	}
}

func TestRegistry_RegisterValidation(t *testing.T) {
	reg := plugin.NewRegistry()

	// Missing ID or invalid format
	p1 := &fakePlugin{
		meta: plugin.Meta{
			ID: "invalid-id-without-slash",
		},
	}
	if err := reg.Register(p1); err == nil {
		t.Errorf("expected error for malformed plugin ID without slash, got nil")
	}

	// Mismatched kind in ID vs Kind field
	p2 := &fakePlugin{
		meta: plugin.Meta{
			ID:   "storage/zfs",
			Kind: plugin.KindShare,
			Name: "zfs",
		},
	}
	if err := reg.Register(p2); err == nil {
		t.Errorf("expected error for mismatched kind in ID, got nil")
	}
}

func TestRegistry_ListAndListByKind(t *testing.T) {
	reg := plugin.NewRegistry()

	pZfs := &fakePlugin{
		meta: plugin.Meta{ID: "storage/zfs", Kind: plugin.KindStorage, Name: "zfs", Order: 1},
	}
	pBtrfs := &fakePlugin{
		meta: plugin.Meta{ID: "storage/btrfs", Kind: plugin.KindStorage, Name: "btrfs", Order: 2},
	}
	pNfs := &fakePlugin{
		meta: plugin.Meta{ID: "share/nfs", Kind: plugin.KindShare, Name: "nfs", Order: 1},
	}

	_ = reg.Register(pZfs)
	_ = reg.Register(pBtrfs)
	_ = reg.Register(pNfs)

	all := reg.List()
	if len(all) != 3 {
		t.Fatalf("expected 3 plugins, got %d", len(all))
	}

	storageList := reg.ListByKind(plugin.KindStorage)
	if len(storageList) != 2 {
		t.Fatalf("expected 2 storage plugins, got %d", len(storageList))
	}
	// Verify ordering by Order
	if storageList[0].Meta().ID != "storage/zfs" || storageList[1].Meta().ID != "storage/btrfs" {
		t.Errorf("unexpected ordering for storage plugins: %v, %v", storageList[0].Meta().ID, storageList[1].Meta().ID)
	}

	shareList := reg.ListByKind(plugin.KindShare)
	if len(shareList) != 1 || shareList[0].Meta().ID != "share/nfs" {
		t.Fatalf("expected 1 share plugin (share/nfs), got %v", shareList)
	}

	networkList := reg.ListByKind(plugin.KindNetwork)
	if len(networkList) != 0 {
		t.Fatalf("expected 0 network plugins, got %d", len(networkList))
	}
}

func TestRegistry_FilterCompatible(t *testing.T) {
	reg := plugin.NewRegistry()

	linuxZfs := &fakePlugin{
		meta: plugin.Meta{ID: "storage/zfs", Kind: plugin.KindStorage, Name: "zfs"},
		compatibleFn: func(h plugin.HostFacts) bool {
			return h.OSFamily == "linux"
		},
	}
	windowsStorage := &fakePlugin{
		meta: plugin.Meta{ID: "storage/storage-spaces", Kind: plugin.KindStorage, Name: "storage-spaces"},
		compatibleFn: func(h plugin.HostFacts) bool {
			return h.OSFamily == "windows"
		},
	}

	_ = reg.Register(linuxZfs)
	_ = reg.Register(windowsStorage)

	linuxFacts := plugin.HostFacts{
		NodeID:   "node-1",
		OSFamily: "linux",
	}
	linuxCompatible := reg.FilterCompatible(linuxFacts)
	if len(linuxCompatible) != 1 || linuxCompatible[0].Meta().ID != "storage/zfs" {
		t.Fatalf("expected only storage/zfs for linux, got %v", linuxCompatible)
	}

	windowsFacts := plugin.HostFacts{
		NodeID:   "node-2",
		OSFamily: "windows",
	}
	winCompatible := reg.FilterCompatible(windowsFacts)
	if len(winCompatible) != 1 || winCompatible[0].Meta().ID != "storage/storage-spaces" {
		t.Fatalf("expected only storage/storage-spaces for windows, got %v", winCompatible)
	}
}

func TestGlobalRegistry(t *testing.T) {
	p := &fakePlugin{
		meta: plugin.Meta{ID: "connection/test-global-conn", Kind: plugin.KindConnection, Name: "test-global-conn"},
	}

	err := plugin.Register(p)
	if err != nil {
		t.Fatalf("failed to register to global registry: %v", err)
	}

	got, ok := plugin.Get("connection/test-global-conn")
	if !ok || got.Meta().ID != "connection/test-global-conn" {
		t.Fatalf("failed to get from global registry: %v", got)
	}

	list := plugin.List()
	if len(list) == 0 {
		t.Fatalf("expected non-empty global plugin list")
	}

	byKind := plugin.ListByKind(plugin.KindConnection)
	if len(byKind) == 0 {
		t.Fatalf("expected connection plugin in ListByKind")
	}

	compat := plugin.FilterCompatible(plugin.HostFacts{OSFamily: "linux"})
	if len(compat) == 0 {
		t.Fatalf("expected compatible plugins in global FilterCompatible")
	}
}

func TestInterfaces_ReaderAndActor(t *testing.T) {
	ctx := context.Background()
	r := &fakeRunner{}
	facts := plugin.HostFacts{NodeID: "test-node", OSFamily: "linux"}

	reader := &fakeReaderPlugin{
		fakePlugin: fakePlugin{
			meta: plugin.Meta{ID: "storage/zfs", Kind: plugin.KindStorage, Name: "zfs"},
		},
		readFn: func(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
			return map[string]any{"pools": []string{"tank"}}, nil
		},
	}

	var p plugin.Plugin = reader
	readerAsserted, ok := p.(plugin.Reader)
	if !ok {
		t.Fatalf("expected plugin to implement plugin.Reader")
	}
	data, err := readerAsserted.Read(ctx, r, facts)
	if err != nil {
		t.Fatalf("unexpected read error: %v", err)
	}
	m, ok := data.(map[string]any)
	if !ok || len(m["pools"].([]string)) != 1 {
		t.Fatalf("unexpected read data: %v", data)
	}

	actor := &fakeActorPlugin{
		fakePlugin: fakePlugin{
			meta: plugin.Meta{ID: "storage/zfs", Kind: plugin.KindStorage, Name: "zfs"},
		},
		actions: []plugin.ActionSpec{
			{
				Name:        "scrub",
				Label:       "Scrub Pool",
				Description: "Start or stop a pool scrub",
				Options: []plugin.OptionSchema{
					{
						Name:        "pool",
						Type:        "string",
						Required:    true,
						Description: "Pool name to scrub",
					},
					{
						Name:        "password",
						Type:        "string",
						Secret:      true,
						Description: "Encryption key or password",
					},
				},
			},
		},
		compileFn: func(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
			if action != "scrub" {
				return plugin.TaskSpec{}, errors.New("unknown action")
			}
			return plugin.TaskSpec{
				Name: "ZFS Scrub",
				Steps: []plugin.TaskStep{
					{
						Name:   "Trigger scrub",
						Module: "ansible.builtin.command",
						Params: map[string]any{"cmd": "zpool scrub " + opts["pool"].(string)},
					},
				},
				SecretsMasked: map[string]string{
					"password": "•••••••• (vault: password)",
				},
			}, nil
		},
	}

	var pActor plugin.Plugin = actor
	actorAsserted, ok := pActor.(plugin.Actor)
	if !ok {
		t.Fatalf("expected plugin to implement plugin.Actor")
	}
	if len(actorAsserted.Actions()) != 1 {
		t.Fatalf("expected 1 action, got %d", len(actorAsserted.Actions()))
	}
	spec, err := actorAsserted.Compile(facts, "scrub", map[string]any{"pool": "tank", "password": "supersecret"})
	if err != nil {
		t.Fatalf("unexpected compile error: %v", err)
	}
	if spec.SecretsMasked["password"] != "•••••••• (vault: password)" {
		t.Fatalf("expected masked secret in task spec, got %v", spec.SecretsMasked)
	}
}

type fakeFullExtPlugin struct {
	fakePlugin
	nodeTypes       []plugin.NodeTypeSpec
	catSettingsPage plugin.CategorizedSettingsPage
	menuEntries     []plugin.ContextMenuEntry
	taskTypes       []plugin.TaskTypeSpec
}

func (p *fakeFullExtPlugin) NodeTypes() []plugin.NodeTypeSpec {
	return p.nodeTypes
}

func (p *fakeFullExtPlugin) SettingsPage() plugin.SettingsPage {
	return p.catSettingsPage.SettingsPage
}

func (p *fakeFullExtPlugin) CategorizedSettingsPage() plugin.CategorizedSettingsPage {
	return p.catSettingsPage
}

func (p *fakeFullExtPlugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []plugin.ContextMenuEntry {
	return p.menuEntries
}

func (p *fakeFullExtPlugin) TaskTypes() []plugin.TaskTypeSpec {
	return p.taskTypes
}

func TestRegistry_ExtendedCapabilities(t *testing.T) {
	reg := plugin.NewRegistry()

	extPlugin := &fakeFullExtPlugin{
		fakePlugin: fakePlugin{
			meta: plugin.Meta{
				ID:    "storage/zfs",
				Kind:  plugin.KindStorage,
				Name:  "zfs",
				Label: "ZFS",
				Order: 10,
			},
			compatibleFn: func(h plugin.HostFacts) bool {
				return h.OSFamily == "linux"
			},
		},
		nodeTypes: []plugin.NodeTypeSpec{
			{
				Type:              plugin.NodeTypeStorageAppliance,
				Label:             "Storage Appliance",
				Icon:              "hard_drive",
				DefaultOSFamily:   "linux",
				DefaultConnection: "connection/ssh",
				Order:             20,
			},
			{
				Type:              plugin.NodeTypeLinux,
				Label:             "Linux Host",
				Icon:              "dns",
				DefaultOSFamily:   "linux",
				DefaultConnection: "connection/ssh",
				Order:             10,
			},
		},
		catSettingsPage: plugin.CategorizedSettingsPage{
			SettingsPage: plugin.SettingsPage{
				ID:          "storage/zfs",
				Title:       "ZFS Settings",
				Icon:        "database",
				Description: "Datacenter ZFS defaults",
				Order:       5,
				Fields: []plugin.SettingsField{
					{Name: "scrub_interval", Label: "Scrub Interval", Type: plugin.SettingsFieldInt, Default: 30},
				},
			},
			Scope:    plugin.SettingsScopeDatacenter,
			Category: "storage",
		},
		menuEntries: []plugin.ContextMenuEntry{
			{
				ID:     "zfs-scrub",
				Target: plugin.TargetStorage,
				Label:  "Scrub Pool",
				Action: "scrub_pool",
				Order:  20,
			},
			{
				ID:     "zfs-status",
				Target: plugin.TargetNode,
				Label:  "ZFS Pool Health",
				Action: "pool_health",
				Order:  10,
			},
		},
		taskTypes: []plugin.TaskTypeSpec{
			{
				Type:           plugin.TaskTypeStorageOp,
				Label:          "ZFS Storage Operation",
				SupportsDryRun: true,
				SupportsCancel: true,
			},
		},
	}

	nodeScopedPlugin := &fakeFullExtPlugin{
		fakePlugin: fakePlugin{
			meta: plugin.Meta{
				ID:    "network/interfaces",
				Kind:  plugin.KindNetwork,
				Name:  "interfaces",
				Label: "Network Interfaces",
				Order: 20,
			},
		},
		catSettingsPage: plugin.CategorizedSettingsPage{
			SettingsPage: plugin.SettingsPage{
				ID:          "network/interfaces",
				Title:       "Node Interface Overrides",
				Icon:        "lan",
				Description: "Per-node network interface settings",
				Order:       15,
			},
			Scope:    plugin.SettingsScopeNode,
			Category: "network",
		},
	}

	if err := reg.Register(extPlugin); err != nil {
		t.Fatalf("failed to register extPlugin: %v", err)
	}
	if err := reg.Register(nodeScopedPlugin); err != nil {
		t.Fatalf("failed to register nodeScopedPlugin: %v", err)
	}

	// 1. Verify NodeType registration and sorting
	nodeTypes := reg.ListNodeTypes()
	if len(nodeTypes) != 2 {
		t.Fatalf("expected 2 node types, got %d", len(nodeTypes))
	}
	if nodeTypes[0].Type != plugin.NodeTypeLinux || nodeTypes[1].Type != plugin.NodeTypeStorageAppliance {
		t.Errorf("expected node types sorted by Order (linux then storage_appliance), got %+v", nodeTypes)
	}
	if gotNT, ok := reg.GetNodeType(plugin.NodeTypeStorageAppliance); !ok || gotNT.Label != "Storage Appliance" {
		t.Errorf("expected to retrieve storage_appliance node type, got %+v (ok=%v)", gotNT, ok)
	}

	// Standalone RegisterNodeType and duplicate rejection
	if err := reg.RegisterNodeType(plugin.NodeTypeSpec{
		Type:            plugin.NodeTypeProxmox,
		Label:           "Proxmox VE",
		DefaultOSFamily: "linux",
		Order:           15,
	}); err != nil {
		t.Fatalf("unexpected error registering standalone node type: %v", err)
	}
	if err := reg.RegisterNodeType(plugin.NodeTypeSpec{
		Type:            plugin.NodeTypeProxmox,
		Label:           "Duplicate Proxmox",
		DefaultOSFamily: "linux",
	}); err == nil {
		t.Fatal("expected error registering duplicate node type")
	}

	// 2. Verify Settings Page categorization (datacenter vs node scope and category filtering)
	dcPages := reg.ListSettingsPagesByScope(plugin.SettingsScopeDatacenter)
	if len(dcPages) != 1 || dcPages[0].ID != "storage/zfs" {
		t.Fatalf("expected 1 datacenter settings page (storage/zfs), got %+v", dcPages)
	}
	nodePages := reg.ListSettingsPagesByScope(plugin.SettingsScopeNode)
	if len(nodePages) != 1 || nodePages[0].ID != "network/interfaces" {
		t.Fatalf("expected 1 node settings page (network/interfaces), got %+v", nodePages)
	}

	byScopeAlias := reg.ListSettingsPagesByCategory("datacenter")
	if len(byScopeAlias) != 1 || byScopeAlias[0].ID != "storage/zfs" {
		t.Fatalf("expected ListSettingsPagesByCategory('datacenter') to return storage/zfs, got %+v", byScopeAlias)
	}
	byCat := reg.ListSettingsPagesByCategory("network")
	if len(byCat) != 1 || byCat[0].ID != "network/interfaces" {
		t.Fatalf("expected ListSettingsPagesByCategory('network') to return network/interfaces, got %+v", byCat)
	}

	if catPage, ok := reg.GetCategorizedSettingsPage("storage/zfs"); !ok || catPage.Category != "storage" {
		t.Errorf("expected GetCategorizedSettingsPage('storage/zfs') category 'storage', got %+v (ok=%v)", catPage, ok)
	}

	// 3. Verify ContextMenuProvider registration and filtering
	ctx := context.Background()
	linuxFacts := plugin.HostFacts{NodeID: "node-1", OSFamily: "linux"}
	winFacts := plugin.HostFacts{NodeID: "node-2", OSFamily: "windows"}

	storageMenus := reg.ListContextMenuEntries(ctx, plugin.TargetStorage, linuxFacts)
	if len(storageMenus) != 1 || storageMenus[0].ID != "zfs-scrub" || storageMenus[0].PluginID != "storage/zfs" {
		t.Fatalf("expected 1 storage menu entry from storage/zfs, got %+v", storageMenus)
	}

	allLinuxMenus := reg.ListContextMenuEntries(ctx, "", linuxFacts)
	if len(allLinuxMenus) != 2 || allLinuxMenus[0].ID != "zfs-status" {
		t.Fatalf("expected 2 sorted menu entries for linux host, got %+v", allLinuxMenus)
	}

	// Incompatible host facts filter out plugin context menu entries
	winMenus := reg.ListContextMenuEntries(ctx, "", winFacts)
	if len(winMenus) != 0 {
		t.Fatalf("expected 0 menu entries for incompatible windows host, got %+v", winMenus)
	}

	// 4. Verify TaskType registration and lookup
	taskTypes := reg.ListTaskTypes()
	if len(taskTypes) != 1 || taskTypes[0].Type != plugin.TaskTypeStorageOp || taskTypes[0].PluginID != "storage/zfs" {
		t.Fatalf("expected 1 registered task type (storage_op), got %+v", taskTypes)
	}
	if gotTT, ok := reg.GetTaskType(plugin.TaskTypeStorageOp); !ok || !gotTT.SupportsDryRun {
		t.Errorf("expected GetTaskType(storage_op) with SupportsDryRun=true, got %+v (ok=%v)", gotTT, ok)
	}

	if err := reg.RegisterTaskType(plugin.TaskTypeSpec{
		Type:  plugin.TaskTypeAnsiblePlaybook,
		Label: "Ansible Playbook",
	}); err != nil {
		t.Fatalf("unexpected error registering standalone task type: %v", err)
	}
	if len(reg.ListTaskTypes()) != 2 {
		t.Fatalf("expected 2 task types after standalone registration, got %d", len(reg.ListTaskTypes()))
	}
}
