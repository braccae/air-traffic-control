package apk_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	_ "air-traffic-control/backend/plugins/all"
	"air-traffic-control/backend/plugins/package/apk"
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

func TestApkPlugin_RegistrationAndMeta(t *testing.T) {
	p, ok := plugin.Get("package/apk")
	if !ok {
		t.Fatal("expected package/apk to be registered in default registry")
	}

	meta := p.Meta()
	if meta.ID != "package/apk" || meta.Kind != plugin.KindPackage || meta.Name != "apk" {
		t.Errorf("unexpected meta: %+v", meta)
	}

	if !p.Compatible(plugin.HostFacts{OSFamily: "linux"}) {
		t.Error("expected compatible with linux host")
	}
	if !p.Compatible(plugin.HostFacts{OSFamily: "linux", ActivePackage: "package/apk"}) {
		t.Error("expected compatible when ActivePackage is package/apk")
	}
	if p.Compatible(plugin.HostFacts{OSFamily: "windows"}) {
		t.Error("expected incompatible with windows host")
	}
	if p.Compatible(plugin.HostFacts{OSFamily: "linux", ActivePackage: "package/dnf"}) {
		t.Error("expected incompatible when ActivePackage is package/dnf")
	}

	reqs := p.Requirements()
	if len(reqs.HostPackages["apk"]) == 0 || len(reqs.ControllerCollections) == 0 {
		t.Errorf("expected non-empty requirements, got %+v", reqs)
	}
}

func TestApkPlugin_Probe(t *testing.T) {
	p := apk.New()
	ctx := context.Background()

	det, err := p.Probe(ctx, nil, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("unexpected error on nil runner: %v", err)
	}
	if det.Detected || det.Status != "OFFLINE" {
		t.Errorf("expected OFFLINE detection on nil runner, got %+v", det)
	}

	r := &fakeRunner{output: "/sbin/apk\n"}
	det, err = p.Probe(ctx, r, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !det.Detected || det.Status != "ONLINE" || det.Details != "/sbin/apk" {
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

func TestApkPlugin_Read(t *testing.T) {
	p := apk.New()
	ctx := context.Background()

	if _, err := p.Read(ctx, nil, plugin.HostFacts{}); err == nil {
		t.Fatal("expected error when runner is nil")
	}

	rErr := &fakeRunner{err: errors.New("ssh error")}
	if _, err := p.Read(ctx, rErr, plugin.HostFacts{}); err == nil {
		t.Fatal("expected error when command fails")
	}

	sampleOutput := `Installed:                                Available:
musl-1.2.4_git20230717-r1                < 1.2.4_git20230717-r2
openssl-3.1.4-r1                         < 3.1.4-r2
busybox-extras-1.36.1-r4                 < 1.36.1-r5
`
	r := &fakeRunner{output: sampleOutput}
	got, err := p.Read(ctx, r, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if !strings.Contains(r.lastCmd, "apk version -v -l '<'") {
		t.Errorf("expected apk version check command, got %q", r.lastCmd)
	}

	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", got)
	}
	if m["manager"] != "apk" || m["isImmutable"] != false {
		t.Errorf("unexpected manager metadata: %+v", m)
	}
	if m["updatesCount"] != 3 {
		t.Errorf("expected updatesCount=3, got %v", m["updatesCount"])
	}
	if m["rebootRequired"] != true {
		t.Errorf("expected rebootRequired=true due to musl update, got %v", m["rebootRequired"])
	}

	updates, ok := m["updates"].([]apk.PackageUpdateEntry)
	if !ok || len(updates) != 3 {
		t.Fatalf("expected 3 PackageUpdateEntry items, got %#v", m["updates"])
	}
	if updates[0].Name != "musl" || updates[0].CurrentVersion != "1.2.4_git20230717-r1" || updates[0].NewVersion != "1.2.4_git20230717-r2" || !updates[0].Security {
		t.Errorf("unexpected first update entry: %+v", updates[0])
	}
	if updates[1].Name != "openssl" || updates[1].CurrentVersion != "3.1.4-r1" || updates[1].NewVersion != "3.1.4-r2" || !updates[1].Security {
		t.Errorf("unexpected second update entry: %+v", updates[1])
	}
	if updates[2].Name != "busybox-extras" || updates[2].CurrentVersion != "1.36.1-r4" || updates[2].NewVersion != "1.36.1-r5" {
		t.Errorf("unexpected third update entry: %+v", updates[2])
	}
}

func TestApkPlugin_V2InterfacesAndCapabilities(t *testing.T) {
	p, ok := plugin.Get("package/apk")
	if !ok {
		t.Fatal("package/apk not registered")
	}

	// 1. DataCollectionPlugin
	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatal("package/apk does not implement newapi.DataCollectionPlugin")
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
		t.Fatal("package/apk does not implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if catPage.ID != "package/apk" || catPage.Category != "package" || catPage.Scope != newapi.SettingsScopeDatacenter {
		t.Errorf("unexpected categorized settings page: %+v", catPage)
	}
	if err := settingsframework.ValidateSchema(catPage.SettingsPage); err != nil {
		t.Fatalf("ValidateSchema failed: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("ValidateCategorizedSettingsPage failed: %v", err)
	}
	if _, ok := plugin.GetSettingsPage("package/apk"); !ok {
		t.Error("expected package/apk settings page in global registry")
	}

	// 3. ManagementPlugin
	mp, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatal("package/apk does not implement newapi.ManagementPlugin")
	}
	mgmtActions := mp.ManagementActions()
	if len(mgmtActions) != 3 {
		t.Fatalf("expected 3 management actions, got %d", len(mgmtActions))
	}
	if len(mp.Actions()) != len(mgmtActions) {
		t.Errorf("Actions() length %d != ManagementActions() length %d", len(mp.Actions()), len(mgmtActions))
	}

	facts := plugin.HostFacts{NodeID: "node-alpine", OSFamily: "linux"}
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
		t.Fatal("package/apk does not implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), facts)
	if len(entries) != 3 {
		t.Fatalf("expected 3 context menu entries for compatible linux host, got %d", len(entries))
	}
	for _, entry := range entries {
		if entry.PluginID != "package/apk" || entry.Target != newapi.TargetNode {
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
		t.Fatal("package/apk does not implement newapi.TaskCompileProvider")
	}
	spec, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		ID:       "req-apk-1",
		Type:     newapi.TaskTypePluginAction,
		NodeID:   "node-alpine",
		PluginID: "package/apk",
		Action:   "install",
		Params:   map[string]any{"name": "htop"},
	})
	if err != nil {
		t.Fatalf("CompileTask failed: %v", err)
	}
	if len(spec.Steps) != 1 || spec.Steps[0].Module != "community.general.apk" {
		t.Fatalf("unexpected compiled steps: %+v", spec.Steps)
	}
	if spec.Steps[0].Params["name"] != "htop" || spec.Steps[0].Params["state"] != "present" {
		t.Errorf("unexpected step params: %+v", spec.Steps[0].Params)
	}

	// Default update "*" behavior
	updateSpec, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		ID:       "req-apk-2",
		Type:     newapi.TaskTypePluginAction,
		NodeID:   "node-alpine",
		PluginID: "package/apk",
		Action:   "update",
		Params:   map[string]any{},
	})
	if err != nil {
		t.Fatalf("CompileTask update failed: %v", err)
	}
	if updateSpec.Steps[0].Params["update_cache"] != true || updateSpec.Steps[0].Params["upgrade"] != true || updateSpec.Steps[0].Params["state"] != "latest" {
		t.Errorf("unexpected update step params: %+v", updateSpec.Steps[0].Params)
	}

	// Fallback action from Params["action"]
	removeSpec, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		ID:       "req-apk-3",
		Type:     newapi.TaskTypePluginAction,
		NodeID:   "node-alpine",
		PluginID: "package/apk",
		Params:   map[string]any{"action": "remove", "name": "curl"},
	})
	if err != nil {
		t.Fatalf("CompileTask remove fallback failed: %v", err)
	}
	if removeSpec.Steps[0].Params["name"] != "curl" || removeSpec.Steps[0].Params["state"] != "absent" {
		t.Errorf("unexpected remove step params: %+v", removeSpec.Steps[0].Params)
	}

	if _, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{}); err == nil {
		t.Error("expected error when action is empty in CompileTask")
	}
}
