package dnf_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	"air-traffic-control/backend/plugins/package/dnf"
	_ "air-traffic-control/backend/plugins/all"
)

type fakeRunner struct {
	lastCmd string
	output  string
	err     error
}

func (f *fakeRunner) RunCommand(ctx context.Context, cmd string) (string, error) {
	f.lastCmd = cmd
	return f.output, f.err
}

func TestDnfPlugin_RegistrationAndMeta(t *testing.T) {
	p, ok := plugin.Get("package/dnf")
	if !ok {
		t.Fatal("expected package/dnf to be registered in default registry")
	}

	meta := p.Meta()
	if meta.ID != "package/dnf" || meta.Kind != plugin.KindPackage || meta.Name != "dnf" {
		t.Errorf("unexpected meta: %+v", meta)
	}

	if !p.Compatible(plugin.HostFacts{OSFamily: "linux"}) {
		t.Error("expected compatible with linux host")
	}
	if !p.Compatible(plugin.HostFacts{OSFamily: "linux", ActivePackage: "package/dnf"}) {
		t.Error("expected compatible when ActivePackage is package/dnf")
	}
	if p.Compatible(plugin.HostFacts{OSFamily: "windows"}) {
		t.Error("expected incompatible with windows host")
	}
	if p.Compatible(plugin.HostFacts{OSFamily: "linux", ActivePackage: "package/apt"}) {
		t.Error("expected incompatible when ActivePackage is package/apt")
	}

	reqs := p.Requirements()
	if len(reqs.HostPackages["dnf"]) == 0 || len(reqs.ControllerCollections) == 0 {
		t.Errorf("expected non-empty requirements, got %+v", reqs)
	}
}

func TestDnfPlugin_Probe(t *testing.T) {
	p := dnf.New()
	ctx := context.Background()

	det, err := p.Probe(ctx, nil, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("unexpected error on nil runner: %v", err)
	}
	if det.Detected || det.Status != "OFFLINE" {
		t.Errorf("expected OFFLINE detection on nil runner, got %+v", det)
	}

	r := &fakeRunner{output: "/usr/bin/dnf\n"}
	det, err = p.Probe(ctx, r, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !det.Detected || det.Status != "ONLINE" || det.Details != "/usr/bin/dnf" {
		t.Errorf("unexpected detection: %+v", det)
	}

	rErr := &fakeRunner{err: errors.New("not found")}
	det, err = p.Probe(ctx, rErr, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if det.Detected || det.Status != "UNAVAILABLE" {
		t.Errorf("expected UNAVAILABLE detection, got %+v", det)
	}
}

func TestDnfPlugin_Read(t *testing.T) {
	p := dnf.New()
	ctx := context.Background()

	if _, err := p.Read(ctx, nil, plugin.HostFacts{}); err == nil {
		t.Fatal("expected error when runner is nil")
	}

	rErr := &fakeRunner{err: errors.New("ssh error")}
	if _, err := p.Read(ctx, rErr, plugin.HostFacts{}); err == nil {
		t.Fatal("expected error when command fails")
	}

	sampleOutput := `Last metadata expiration check: 0:12:34 ago on Thu 22 Oct 2026 07:00:00 PM EDT.
curl.x86_64                        8.9.1-2.fc41                     updates
kernel-core.x86_64                 6.11.5-300.fc41                  updates
long-package-name-that-wraps.noarch
                                   1.2.3-1.fc41                     fedora
Obsoleting Packages
old-pkg.x86_64                     1.0-1.fc41                       updates
`
	r := &fakeRunner{output: sampleOutput}
	got, err := p.Read(ctx, r, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if !strings.Contains(r.lastCmd, "dnf check-update -q") {
		t.Errorf("expected dnf check-update command, got %q", r.lastCmd)
	}

	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", got)
	}
	if m["manager"] != "dnf" || m["isImmutable"] != false {
		t.Errorf("unexpected manager metadata: %+v", m)
	}
	if m["updatesCount"] != 3 {
		t.Errorf("expected updatesCount=3, got %v", m["updatesCount"])
	}
	if m["rebootRequired"] != true {
		t.Errorf("expected rebootRequired=true due to kernel-core update, got %v", m["rebootRequired"])
	}

	updates, ok := m["updates"].([]dnf.PackageUpdateEntry)
	if !ok || len(updates) != 3 {
		t.Fatalf("expected 3 PackageUpdateEntry items, got %#v", m["updates"])
	}
	if updates[0].Name != "curl" || updates[0].Arch != "x86_64" || updates[0].NewVersion != "8.9.1-2.fc41" || updates[0].Repository != "updates" {
		t.Errorf("unexpected first update entry: %+v", updates[0])
	}
	if updates[2].Name != "long-package-name-that-wraps" || updates[2].Arch != "noarch" {
		t.Errorf("unexpected wrapped update entry: %+v", updates[2])
	}
}

func TestDnfPlugin_V2InterfacesAndCapabilities(t *testing.T) {
	p, ok := plugin.Get("package/dnf")
	if !ok {
		t.Fatal("package/dnf not registered")
	}

	// 1. DataCollectionPlugin
	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatal("package/dnf does not implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.Interval <= 0 || sched.TTL <= 0 {
		t.Errorf("expected positive collection schedule interval and TTL, got %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "updates" {
		t.Errorf("unexpected collection schema: %+v", schema)
	}

	// 2. CategorizedSettingsProvider
	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatal("package/dnf does not implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if catPage.ID != "package/dnf" || catPage.Category != "package" || catPage.Scope != newapi.SettingsScopeDatacenter {
		t.Errorf("unexpected categorized settings page: %+v", catPage)
	}
	if err := settingsframework.ValidateSchema(catPage.SettingsPage); err != nil {
		t.Fatalf("ValidateSchema failed: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("ValidateCategorizedSettingsPage failed: %v", err)
	}

	// 3. ManagementPlugin
	mp, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatal("package/dnf does not implement newapi.ManagementPlugin")
	}
	mgmtActions := mp.ManagementActions()
	if len(mgmtActions) != 3 {
		t.Fatalf("expected 3 management actions, got %d", len(mgmtActions))
	}
	if len(mp.Actions()) != len(mgmtActions) {
		t.Errorf("Actions() length %d != ManagementActions() length %d", len(mp.Actions()), len(mgmtActions))
	}

	facts := plugin.HostFacts{NodeID: "node-1", OSFamily: "linux"}
	if err := mp.ValidateAction(facts, "install", map[string]any{"name": "htop"}); err != nil {
		t.Errorf("expected valid install action, got: %v", err)
	}
	if err := mp.ValidateAction(facts, "install", map[string]any{}); err == nil {
		t.Error("expected validation error when required name option is missing for install")
	}
	if err := mp.ValidateAction(facts, "update", map[string]any{}); err != nil {
		t.Errorf("expected update action without name to pass validation, got: %v", err)
	}
	if err := mp.ValidateAction(facts, "remove", map[string]any{}); err == nil {
		t.Error("expected validation error when required name option is missing for remove")
	}
	if err := mp.ValidateAction(facts, "unknown", map[string]any{"name": "htop"}); err == nil {
		t.Error("expected error for unsupported action")
	}

	// 4. ContextMenuProvider
	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatal("package/dnf does not implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), facts)
	if len(entries) != 3 {
		t.Fatalf("expected 3 context menu entries for compatible linux host, got %d", len(entries))
	}
	for _, entry := range entries {
		if entry.PluginID != "package/dnf" || entry.Target != newapi.TargetNode {
			t.Errorf("unexpected context menu entry: %+v", entry)
		}
	}
	if !entries[len(entries)-1].Danger || entries[len(entries)-1].Action != "remove" {
		t.Errorf("expected remove context menu entry to be marked danger, got %+v", entries[len(entries)-1])
	}
	if incompat := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "windows"}); len(incompat) != 0 {
		t.Errorf("expected no context menu entries for incompatible host, got %+v", incompat)
	}

	// 5. TaskCompileProvider
	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatal("package/dnf does not implement newapi.TaskCompileProvider")
	}
	spec, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		ID:       "req-1",
		Type:     newapi.TaskTypePluginAction,
		NodeID:   "node-1",
		PluginID: "package/dnf",
		Action:   "install",
		Params:   map[string]any{"name": "nginx"},
	})
	if err != nil {
		t.Fatalf("CompileTask failed: %v", err)
	}
	if len(spec.Steps) != 1 || spec.Steps[0].Module != "ansible.builtin.dnf" {
		t.Fatalf("unexpected compiled steps: %+v", spec.Steps)
	}
	if spec.Steps[0].Params["name"] != "nginx" || spec.Steps[0].Params["state"] != "present" {
		t.Errorf("unexpected step params: %+v", spec.Steps[0].Params)
	}

	// Default update "*" behavior
	updateSpec, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		ID:       "req-2",
		Type:     newapi.TaskTypePluginAction,
		NodeID:   "node-1",
		PluginID: "package/dnf",
		Action:   "update",
		Params:   map[string]any{},
	})
	if err != nil {
		t.Fatalf("CompileTask update failed: %v", err)
	}
	if updateSpec.Steps[0].Params["name"] != "*" || updateSpec.Steps[0].Params["state"] != "latest" {
		t.Errorf("unexpected update step params: %+v", updateSpec.Steps[0].Params)
	}

	if _, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{}); err == nil {
		t.Error("expected error when action is empty in CompileTask")
	}
}
