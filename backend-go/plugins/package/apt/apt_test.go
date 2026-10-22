package apt_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	_ "air-traffic-control/backend/plugins/all"
	"air-traffic-control/backend/plugins/package/apt"
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

func TestAptPlugin_RegistrationAndMeta(t *testing.T) {
	p, ok := plugin.Get("package/apt")
	if !ok {
		t.Fatal("expected package/apt to be registered in default registry")
	}

	meta := p.Meta()
	if meta.ID != "package/apt" || meta.Kind != plugin.KindPackage || meta.Name != "apt" || meta.Order != 40 {
		t.Errorf("unexpected meta: %+v", meta)
	}

	if !p.Compatible(plugin.HostFacts{OSFamily: "linux"}) {
		t.Error("expected compatible with linux host")
	}
	if !p.Compatible(plugin.HostFacts{OSFamily: "linux", ActivePackage: "package/apt"}) {
		t.Error("expected compatible when ActivePackage is package/apt")
	}
	if p.Compatible(plugin.HostFacts{OSFamily: "windows"}) {
		t.Error("expected incompatible with windows host")
	}
	if p.Compatible(plugin.HostFacts{OSFamily: "linux", ActivePackage: "package/dnf"}) {
		t.Error("expected incompatible when ActivePackage is package/dnf")
	}

	reqs := p.Requirements()
	if len(reqs.HostPackages["apt"]) != 2 || len(reqs.ControllerCollections) == 0 || reqs.ControllerCollections[0] != "ansible.builtin" {
		t.Errorf("unexpected requirements: %+v", reqs)
	}
}

func TestAptPlugin_Probe(t *testing.T) {
	p := apt.New()
	ctx := context.Background()

	det, err := p.Probe(ctx, nil, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("unexpected error on nil runner: %v", err)
	}
	if det.Detected || det.Status != "OFFLINE" {
		t.Errorf("expected OFFLINE detection on nil runner, got %+v", det)
	}

	r := &fakeRunner{output: "/usr/bin/apt-get\n"}
	det, err = p.Probe(ctx, r, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !det.Detected || det.Status != "ONLINE" || det.Details != "/usr/bin/apt-get" {
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

func TestAptPlugin_Read(t *testing.T) {
	p := apt.New()
	ctx := context.Background()

	if _, err := p.Read(ctx, nil, plugin.HostFacts{}); err == nil {
		t.Fatal("expected error when runner is nil")
	}

	rErr := &fakeRunner{err: errors.New("ssh error")}
	if _, err := p.Read(ctx, rErr, plugin.HostFacts{}); err == nil {
		t.Fatal("expected error when command fails")
	}

	sampleOutput := `Reading package lists... Done
Building dependency tree... Done
Calculating upgrade... Done
The following packages will be upgraded:
  curl linux-image-generic openssl
3 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.
Inst curl [7.81.0-1ubuntu1.15] (7.81.0-1ubuntu1.16 Ubuntu:22.04/jammy-updates, Ubuntu:22.04/jammy-security [amd64])
Inst linux-image-generic [5.15.0.91.88] (5.15.0.94.91 Ubuntu:22.04/jammy-updates [amd64])
openssl/jammy-security 3.0.2-0ubuntu1.15 amd64 [upgradable from: 3.0.2-0ubuntu1.14]
`
	r := &fakeRunner{output: sampleOutput}
	got, err := p.Read(ctx, r, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if !strings.Contains(r.lastCmd, "apt-get -s -o Debug::NoLocking=true upgrade") {
		t.Errorf("expected apt-get simulated upgrade command, got %q", r.lastCmd)
	}

	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", got)
	}
	if m["manager"] != "apt" || m["isImmutable"] != false {
		t.Errorf("unexpected manager metadata: %+v", m)
	}
	if m["updatesCount"] != 3 {
		t.Errorf("expected updatesCount=3, got %v", m["updatesCount"])
	}
	if m["rebootRequired"] != true {
		t.Errorf("expected rebootRequired=true due to linux-image-generic update, got %v", m["rebootRequired"])
	}

	updates, ok := m["updates"].([]apt.PackageUpdateEntry)
	if !ok || len(updates) != 3 {
		t.Fatalf("expected 3 PackageUpdateEntry items, got %#v", m["updates"])
	}
	if updates[0].Name != "curl" || updates[0].CurrentVersion != "7.81.0-1ubuntu1.15" || updates[0].NewVersion != "7.81.0-1ubuntu1.16" || updates[0].Arch != "amd64" || !updates[0].Security {
		t.Errorf("unexpected first update entry: %+v", updates[0])
	}
	if updates[1].Name != "linux-image-generic" || updates[1].Security {
		t.Errorf("unexpected second update entry: %+v", updates[1])
	}
	if updates[2].Name != "openssl" || updates[2].CurrentVersion != "3.0.2-0ubuntu1.14" || updates[2].NewVersion != "3.0.2-0ubuntu1.15" || !updates[2].Security {
		t.Errorf("unexpected third update entry: %+v", updates[2])
	}
}

func TestAptPlugin_V2InterfacesAndCapabilities(t *testing.T) {
	p, ok := plugin.Get("package/apt")
	if !ok {
		t.Fatal("package/apt not registered")
	}

	// 1. DataCollectionPlugin
	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatal("package/apt does not implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.Interval != 60*time.Second || sched.TTL != 60*time.Second {
		t.Errorf("expected 60s collection schedule interval and TTL, got %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "updates" {
		t.Errorf("unexpected collection schema: %+v", schema)
	}

	// 2. CategorizedSettingsProvider
	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatal("package/apt does not implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if catPage.ID != "package/apt" || catPage.Category != "package" || catPage.Scope != newapi.SettingsScopeDatacenter {
		t.Errorf("unexpected categorized settings page: %+v", catPage)
	}
	if err := settingsframework.ValidateSchema(catPage.SettingsPage); err != nil {
		t.Fatalf("ValidateSchema failed: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("ValidateCategorizedSettingsPage failed: %v", err)
	}
	if _, ok := plugin.GetCategorizedSettingsPage("package/apt"); !ok {
		t.Error("expected package/apt categorized settings page to be registered in global registry")
	}
	if got := apt.DefaultUpgradeStrategy(); got != "dist" {
		t.Errorf("DefaultUpgradeStrategy() = %q, want %q", got, "dist")
	}
	if !apt.DefaultUpdateCache() {
		t.Error("expected DefaultUpdateCache() default true")
	}

	// 3. ManagementPlugin
	mp, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatal("package/apt does not implement newapi.ManagementPlugin")
	}
	mgmtActions := mp.ManagementActions()
	if len(mgmtActions) != 3 {
		t.Fatalf("expected 3 management actions, got %d", len(mgmtActions))
	}
	if len(mp.Actions()) != len(mgmtActions) {
		t.Errorf("Actions() length %d != ManagementActions() length %d", len(mp.Actions()), len(mgmtActions))
	}

	facts := plugin.HostFacts{NodeID: "node-deb-1", OSFamily: "linux"}
	if err := mp.ValidateAction(facts, "install", map[string]any{"name": "curl"}); err != nil {
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
	if err := mp.ValidateAction(facts, "unknown", map[string]any{"name": "curl"}); err == nil {
		t.Error("expected error for unsupported action")
	}

	// 4. ContextMenuProvider
	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatal("package/apt does not implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), facts)
	if len(entries) != 3 {
		t.Fatalf("expected 3 context menu entries for compatible linux host, got %d", len(entries))
	}
	for _, entry := range entries {
		if entry.PluginID != "package/apt" || entry.Target != newapi.TargetNode {
			t.Errorf("unexpected context menu entry: %+v", entry)
		}
	}
	if !entries[len(entries)-1].Danger || entries[len(entries)-1].Action != "remove" {
		t.Errorf("expected remove context menu entry to be marked danger, got %+v", entries[len(entries)-1])
	}
	if incompat := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "windows"}); len(incompat) != 0 {
		t.Errorf("expected no context menu entries for incompatible windows host, got %+v", incompat)
	}
	if incompatPkg := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "linux", ActivePackage: "package/dnf"}); len(incompatPkg) != 0 {
		t.Errorf("expected no context menu entries when ActivePackage is package/dnf, got %+v", incompatPkg)
	}

	// 5. TaskCompileProvider
	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatal("package/apt does not implement newapi.TaskCompileProvider")
	}
	spec, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		ID:       "req-apt-1",
		Type:     newapi.TaskTypePluginAction,
		NodeID:   "node-deb-1",
		PluginID: "package/apt",
		Action:   "install",
		Params:   map[string]any{"name": "nginx"},
	})
	if err != nil {
		t.Fatalf("CompileTask install failed: %v", err)
	}
	if len(spec.Steps) != 1 || spec.Steps[0].Module != "ansible.builtin.apt" {
		t.Fatalf("unexpected compiled steps: %+v", spec.Steps)
	}
	if spec.Steps[0].Params["name"] != "nginx" || spec.Steps[0].Params["state"] != "present" {
		t.Errorf("unexpected install step params: %+v", spec.Steps[0].Params)
	}

	// Default update "*" behavior with update_cache=true and upgrade=dist
	updateSpec, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		ID:       "req-apt-2",
		Type:     newapi.TaskTypePluginAction,
		NodeID:   "node-deb-1",
		PluginID: "package/apt",
		Params:   map[string]any{"action": "update"},
	})
	if err != nil {
		t.Fatalf("CompileTask update failed: %v", err)
	}
	if updateSpec.Steps[0].Params["state"] != "latest" || updateSpec.Steps[0].Params["update_cache"] != true || updateSpec.Steps[0].Params["upgrade"] != "dist" {
		t.Errorf("unexpected update step params: %+v", updateSpec.Steps[0].Params)
	}

	if _, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{}); err == nil {
		t.Error("expected error when action is empty in CompileTask")
	}
}
