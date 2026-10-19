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
