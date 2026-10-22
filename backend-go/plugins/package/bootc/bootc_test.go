package bootc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
)

// fakeRunner records the command it received and returns canned output.
type fakeRunner struct {
	lastCmd string
	output  string
	err     error
}

func (f *fakeRunner) RunCommand(ctx context.Context, cmd string) (string, error) {
	f.lastCmd = cmd
	return f.output, f.err
}

// realHostStatusJSON is the verbatim `sudo -n bootc status --json` document
// captured from a real bootc host (core@likes-it-cold). It has a booted
// deployment, a rollback deployment and no staged deployment.
const realHostStatusJSON = `{"apiVersion":"org.containers.bootc/v1","kind":"BootcHost","metadata":{"name":"host"},"spec":{"bootOrder":"default","image":{"image":"ghcr.io/braccae/alma:dockerhost","transport":"registry"}},"status":{"booted":{"cachedUpdate":null,"composefs":null,"downloadOnly":false,"image":{"architecture":"amd64","image":{"image":"ghcr.io/braccae/alma:dockerhost","transport":"registry"},"imageDigest":"sha256:3d1444a737945b249cf7829adf6847cc929341445947dd14845dabaaea7974d9","timestamp":"2026-10-15T04:11:47Z","version":"dockerhost"},"incompatible":false,"ostree":{"checksum":"45415542e656ef50b5800571bcf9c1ff510a27d549e192d690e42ae699a102a9","deploySerial":0,"stateroot":"default"},"pinned":false,"softRebootCapable":true,"store":"ostreeContainer"},"rollback":{"cachedUpdate":{"architecture":"amd64","image":{"image":"ghcr.io/braccae/alma:dockerhost","transport":"registry"},"imageDigest":"sha256:3d1444a737945b249cf7829adf6847cc929341445947dd14845dabaaea7974d9","timestamp":"2026-10-15T04:11:47Z","version":"dockerhost"},"composefs":null,"downloadOnly":false,"image":{"architecture":"amd64","image":{"image":"ghcr.io/braccae/alma:dockerhost","transport":"registry"},"imageDigest":"sha256:9cb3a3941ee4fd1cf3b5cb837ddf9cb6009435dfd9c770b9a95e98ed0c4c38d3","timestamp":"2026-10-13T12:19:22Z","version":"dockerhost"},"incompatible":false,"ostree":{"checksum":"c1e5c41cb713a38e397e79a5f3a8ab48cf7160f53a7ceac93ea27217ee67984c","deploySerial":0,"stateroot":"default"},"pinned":false,"softRebootCapable":true,"store":"ostreeContainer"},"rollbackQueued":false,"staged":null,"type":"bootcHost","usrOverlay":null}}`

// stagedStatusJSON is a faithful document where a new deployment is staged for
// the next boot (staged non-null).
const stagedStatusJSON = `{"apiVersion":"org.containers.bootc/v1","kind":"BootcHost","metadata":{"name":"host"},"status":{"booted":{"image":{"image":{"image":"quay.io/centos-bootc/centos-bootc:stream9","transport":"registry"},"imageDigest":"sha256:aaaa","version":"9.20240315.0"}},"staged":{"image":{"image":{"image":"quay.io/centos-bootc/centos-bootc:stream9","transport":"registry"},"imageDigest":"sha256:bbbb","version":"9.20240320.0"}},"rollback":null}}`

// assertField extracts a string field from the Read result map.
func assertField(t *testing.T, got any, key, want string) {
	t.Helper()
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("Read returned %T, want map[string]any", got)
	}
	v, ok := m[key]
	if !ok {
		t.Fatalf("result missing key %q", key)
	}
	s, _ := v.(string)
	if s != want {
		t.Errorf("%s = %q, want %q", key, s, want)
	}
}

func TestRead_RealHost_BootedAndFallback(t *testing.T) {
	r := &fakeRunner{output: realHostStatusJSON}
	got, err := New().Read(context.Background(), r, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	assertField(t, got, "currentImage", "ghcr.io/braccae/alma:dockerhost")
	assertField(t, got, "currentImageVersion", "dockerhost")
	assertField(t, got, "currentImageDigest", "sha256:3d1444a737945b249cf7829adf6847cc929341445947dd14845dabaaea7974d9")
	// No deployment is staged on this host.
	assertField(t, got, "stagedImage", "none")
	// The previous deployment is available as a rollback/fallback.
	assertField(t, got, "fallbackImage", "ghcr.io/braccae/alma:dockerhost")
	assertField(t, got, "fallbackImageDigest", "sha256:9cb3a3941ee4fd1cf3b5cb837ddf9cb6009435dfd9c770b9a95e98ed0c4c38d3")

	// The plugin must query real bootc status, with a passwordless-sudo path.
	if !strings.Contains(r.lastCmd, "bootc status --json") {
		t.Errorf("Read did not run `bootc status --json`; ran %q", r.lastCmd)
	}
}

func TestRead_StagedImageReported(t *testing.T) {
	r := &fakeRunner{output: stagedStatusJSON}
	got, err := New().Read(context.Background(), r, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	assertField(t, got, "currentImage", "quay.io/centos-bootc/centos-bootc:stream9")
	assertField(t, got, "stagedImage", "quay.io/centos-bootc/centos-bootc:stream9")
	assertField(t, got, "stagedImageVersion", "9.20240320.0")
	assertField(t, got, "stagedImageDigest", "sha256:bbbb")
	assertField(t, got, "fallbackImage", "none")
}

func TestRead_NoStagedOrRollback_ReportsNone(t *testing.T) {
	r := &fakeRunner{output: `{"kind":"BootcHost","status":{"booted":{"image":{"image":{"image":"quay.io/org/os:latest"}}},"staged":null,"rollback":null}}`}
	got, err := New().Read(context.Background(), r, plugin.HostFacts{})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	assertField(t, got, "currentImage", "quay.io/org/os:latest")
	assertField(t, got, "stagedImage", "none")
	assertField(t, got, "fallbackImage", "none")
}

func TestRead_RunnerNil_Errors(t *testing.T) {
	_, err := New().Read(context.Background(), nil, plugin.HostFacts{})
	if err == nil {
		t.Fatal("expected error for nil runner, got nil")
	}
}

func TestRead_CommandError_Propagates(t *testing.T) {
	r := &fakeRunner{err: errors.New("ssh command execution failed")}
	_, err := New().Read(context.Background(), r, plugin.HostFacts{})
	if err == nil {
		t.Fatal("expected error when bootc status fails, got nil")
	}
}

func TestRead_MalformedJSON_Errors(t *testing.T) {
	r := &fakeRunner{output: "not json at all"}
	_, err := New().Read(context.Background(), r, plugin.HostFacts{})
	if err == nil {
		t.Fatal("expected error for malformed bootc status JSON, got nil")
	}
}

func TestRead_EmptyOutput_Errors(t *testing.T) {
	r := &fakeRunner{output: "   "}
	_, err := New().Read(context.Background(), r, plugin.HostFacts{})
	if err == nil {
		t.Fatal("expected error for empty bootc status output, got nil")
	}
}

// TestPlugin_ImplementsReader asserts the optional Reader interface is satisfied.
func TestPlugin_ImplementsReader(t *testing.T) {
	var _ plugin.Reader = New()
}

// TestProbe_Unavailable is a regression guard for the reporting path.
func TestProbe_Unavailable(t *testing.T) {
	r := &fakeRunner{err: fmt.Errorf("command not found")}
	d, err := New().Probe(context.Background(), r, plugin.HostFacts{})
	if err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
	if d.Detected || d.Status != "UNAVAILABLE" {
		t.Errorf("expected UNAVAILABLE detection, got %+v", d)
	}
}

func TestProbe_NilRunner(t *testing.T) {
	d, err := New().Probe(context.Background(), nil, plugin.HostFacts{})
	if err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
	if d.Detected || d.Status != "OFFLINE" {
		t.Errorf("expected OFFLINE detection for nil runner, got %+v", d)
	}
}

func TestCompatible_HostFacts(t *testing.T) {
	p := New()
	if !p.Compatible(plugin.HostFacts{OSFamily: "linux"}) {
		t.Error("expected compatible with linux")
	}
	if !p.Compatible(plugin.HostFacts{OSFamily: "linux", ActivePackage: "package/bootc"}) {
		t.Error("expected compatible when ActivePackage is package/bootc")
	}
	if p.Compatible(plugin.HostFacts{OSFamily: "windows"}) {
		t.Error("expected incompatible with windows")
	}
	if p.Compatible(plugin.HostFacts{OSFamily: "linux", ActivePackage: "package/dnf"}) {
		t.Error("expected incompatible when ActivePackage is package/dnf")
	}
}

func TestCompile_UpdateAndSwitch(t *testing.T) {
	p := New()
	facts := plugin.HostFacts{OSFamily: "linux"}

	updateSpec, err := p.Compile(facts, "update", map[string]any{})
	if err != nil {
		t.Fatalf("Compile update failed: %v", err)
	}
	if updateSpec.Name != "bootc update" || len(updateSpec.Steps) != 1 {
		t.Fatalf("unexpected update spec: %+v", updateSpec)
	}
	if updateSpec.Steps[0].Module != "ansible.builtin.command" || updateSpec.Steps[0].Params["cmd"] != "bootc upgrade" {
		t.Errorf("unexpected update step: %+v", updateSpec.Steps[0])
	}

	switchSpec, err := p.Compile(facts, "switch", map[string]any{"image": "quay.io/org/os:latest"})
	if err != nil {
		t.Fatalf("Compile switch failed: %v", err)
	}
	if switchSpec.Name != "bootc switch" || len(switchSpec.Steps) != 1 {
		t.Fatalf("unexpected switch spec: %+v", switchSpec)
	}
	if switchSpec.Steps[0].Module != "ansible.builtin.command" || switchSpec.Steps[0].Params["cmd"] != "bootc switch quay.io/org/os:latest" {
		t.Errorf("unexpected switch step: %+v", switchSpec.Steps[0])
	}

	if _, err := p.Compile(facts, "switch", map[string]any{}); err == nil {
		t.Error("expected error when image is missing for switch")
	}
	if _, err := p.Compile(facts, "unknown", map[string]any{}); err == nil {
		t.Error("expected error for unsupported action")
	}
}

func TestBootcPlugin_NewAPIInterfacesAndSchemas(t *testing.T) {
	p, ok := plugin.Get("package/bootc")
	if !ok {
		t.Fatalf("package/bootc not registered in DefaultRegistry")
	}

	// 1. DataCollectionPlugin
	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatalf("package/bootc does not implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.Interval != 60*time.Second || sched.TTL != 60*time.Second {
		t.Errorf("unexpected collection schedule for package/bootc: %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "currentImage" {
		t.Errorf("unexpected collection schema: %+v", schema)
	}

	// 2. ManagementPlugin & action parity
	mgmt, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatalf("package/bootc does not implement newapi.ManagementPlugin")
	}
	mgmtActions := mgmt.ManagementActions()
	legacyActions := p.(plugin.Actor).Actions()
	if len(mgmtActions) != 2 || len(legacyActions) != len(mgmtActions) {
		t.Fatalf("expected 2 management actions matching legacy Actions(), got mgmt=%d legacy=%d", len(mgmtActions), len(legacyActions))
	}
	for i, ma := range mgmtActions {
		if ma.Name != legacyActions[i].Name {
			t.Errorf("action[%d] name mismatch: %q vs %q", i, ma.Name, legacyActions[i].Name)
		}
		if !ma.Idempotent || len(ma.InvalidatesKinds) == 0 || ma.InvalidatesKinds[0] != plugin.KindPackage {
			t.Errorf("unexpected management metadata for %q: %+v", ma.Name, ma)
		}
		if ma.Name == "switch" && (!ma.Dangerous || ma.ConfirmPrompt == "") {
			t.Errorf("expected switch to be marked dangerous with a confirmation prompt: %+v", ma)
		}
	}

	facts := plugin.HostFacts{NodeID: "node-bootc-1", OSFamily: "linux"}
	if err := mgmt.ValidateAction(facts, "update", map[string]any{}); err != nil {
		t.Errorf("expected valid update options to pass ValidateAction, got %v", err)
	}
	if err := mgmt.ValidateAction(facts, "switch", map[string]any{"image": "quay.io/org/os:latest"}); err != nil {
		t.Errorf("expected valid switch options to pass ValidateAction, got %v", err)
	}
	if err := mgmt.ValidateAction(facts, "switch", map[string]any{}); err == nil {
		t.Errorf("expected missing required image option for switch to fail ValidateAction")
	}
	if err := mgmt.ValidateAction(facts, "unknown_action", nil); err == nil {
		t.Errorf("expected unknown action to fail ValidateAction")
	}

	// 3. CategorizedSettingsProvider & settingsframework validation
	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatalf("package/bootc does not implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if err := settingsframework.ValidateSchema(catPage.SettingsPage); err != nil {
		t.Fatalf("ValidateSchema failed for package/bootc settings page: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("ValidateCategorizedSettingsPage failed for package/bootc: %v", err)
	}
	if catPage.ID != "package/bootc" || catPage.Scope != newapi.SettingsScopeDatacenter || catPage.Category != "package" {
		t.Errorf("unexpected categorized settings page metadata: %+v", catPage)
	}
	if _, ok := plugin.GetCategorizedSettingsPage("package/bootc"); !ok {
		t.Errorf("expected package/bootc categorized settings page to be indexed in DefaultRegistry")
	}

	// 4. ContextMenuProvider
	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatalf("package/bootc does not implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), facts)
	if len(entries) != 2 {
		t.Fatalf("expected 2 context menu entries for compatible linux host, got %d (%+v)", len(entries), entries)
	}
	for _, entry := range entries {
		if entry.PluginID != "package/bootc" || entry.Target != newapi.TargetNode || entry.Action == "" {
			t.Errorf("unexpected context menu entry metadata: %+v", entry)
		}
	}
	if winEntries := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "windows"}); len(winEntries) != 0 {
		t.Errorf("expected nil/empty context menu entries for incompatible windows host, got %+v", winEntries)
	}
	if dnfEntries := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "linux", ActivePackage: "package/dnf"}); len(dnfEntries) != 0 {
		t.Errorf("expected nil/empty context menu entries when ActivePackage is package/dnf, got %+v", dnfEntries)
	}

	// 5. TaskCompileProvider
	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatalf("package/bootc does not implement newapi.TaskCompileProvider")
	}
	compiledUpdate, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "package/bootc",
		Action:   "update",
		NodeID:   "node-bootc-1",
	})
	if err != nil {
		t.Fatalf("CompileTask update failed: %v", err)
	}
	if len(compiledUpdate.Steps) != 1 || compiledUpdate.Name != "bootc update" || compiledUpdate.Steps[0].Params["cmd"] != "bootc upgrade" {
		t.Errorf("unexpected CompileTask update result: %+v", compiledUpdate)
	}

	compiledSwitch, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "package/bootc",
		NodeID:   "node-bootc-1",
		Params: map[string]any{
			"action": "switch",
			"image":  "quay.io/acme/os:latest",
		},
	})
	if err != nil {
		t.Fatalf("CompileTask switch failed: %v", err)
	}
	if len(compiledSwitch.Steps) != 1 || compiledSwitch.Name != "bootc switch" || compiledSwitch.Steps[0].Params["cmd"] != "bootc switch quay.io/acme/os:latest" {
		t.Errorf("unexpected CompileTask switch result: %+v", compiledSwitch)
	}

	if _, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{NodeID: "node-bootc-1"}); err == nil {
		t.Errorf("expected error when CompileTask is called without an action")
	}
}
