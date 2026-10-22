package none_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	_ "air-traffic-control/backend/plugins/all"
	"air-traffic-control/backend/plugins/elevation/none"
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

func TestNonePlugin_RegistrationAndMeta(t *testing.T) {
	p, ok := plugin.Get("elevation/none")
	if !ok {
		t.Fatal("expected elevation/none to be registered in default registry")
	}

	meta := p.Meta()
	if meta.ID != "elevation/none" || meta.Kind != plugin.KindElevation || meta.Name != "none" || meta.Order != 40 {
		t.Errorf("unexpected meta: %+v", meta)
	}

	if !p.Compatible(plugin.HostFacts{OSFamily: "linux"}) {
		t.Error("expected compatible with linux host")
	}
	if !p.Compatible(plugin.HostFacts{OSFamily: "windows"}) {
		t.Error("expected compatible with windows host when ActiveElevation is unset")
	}
	if !p.Compatible(plugin.HostFacts{OSFamily: "linux", ActiveElevation: "elevation/none"}) {
		t.Error("expected compatible when ActiveElevation is elevation/none")
	}
	if p.Compatible(plugin.HostFacts{OSFamily: "linux", ActiveElevation: "elevation/sudo"}) {
		t.Error("expected incompatible when ActiveElevation is elevation/sudo")
	}

	reqs := p.Requirements()
	if len(reqs.ControllerCollections) == 0 || reqs.ControllerCollections[0] != "ansible.builtin" {
		t.Errorf("unexpected controller collections: %+v", reqs)
	}
}

func TestNonePlugin_Probe(t *testing.T) {
	p := none.New()
	ctx := context.Background()

	det, err := p.Probe(ctx, nil, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("unexpected error on nil runner: %v", err)
	}
	if det.Detected || det.Status != "OFFLINE" {
		t.Errorf("expected OFFLINE detection on nil runner, got %+v", det)
	}

	rRoot := &fakeRunner{output: "0\n"}
	det, err = p.Probe(ctx, rRoot, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rRoot.lastCmd != "id -u" {
		t.Errorf("expected probe command 'id -u', got %q", rRoot.lastCmd)
	}
	if !det.Detected || det.Status != "ONLINE" || !strings.Contains(det.Details, "uid=0") {
		t.Errorf("unexpected root detection: %+v", det)
	}

	rNonRoot := &fakeRunner{output: "1000\n"}
	det, err = p.Probe(ctx, rNonRoot, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !det.Detected || det.Status != "UNPRIVILEGED" || !strings.Contains(det.Details, "uid=1000") {
		t.Errorf("expected UNPRIVILEGED detection for non-zero uid, got %+v", det)
	}

	rErr := &fakeRunner{err: errors.New("connection closed")}
	det, err = p.Probe(ctx, rErr, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if det.Detected || det.Status != "UNAVAILABLE" {
		t.Errorf("expected UNAVAILABLE detection on error, got %+v", det)
	}
}

func TestNonePlugin_V2InterfacesAndCapabilities(t *testing.T) {
	p, ok := plugin.Get("elevation/none")
	if !ok {
		t.Fatal("elevation/none not registered")
	}

	// 1. CategorizedSettingsProvider
	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatal("elevation/none does not implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if catPage.ID != "elevation/none" || catPage.Category != "elevation" || catPage.Scope != newapi.SettingsScopeDatacenter {
		t.Errorf("unexpected categorized settings page: %+v", catPage)
	}
	if err := settingsframework.ValidateSchema(catPage.SettingsPage); err != nil {
		t.Fatalf("ValidateSchema failed: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("ValidateCategorizedSettingsPage failed: %v", err)
	}
	if _, ok := plugin.GetCategorizedSettingsPage("elevation/none"); !ok {
		t.Error("expected elevation/none categorized settings page to be registered in global registry")
	}
	if got := none.DefaultCommand(); got != "id -u" {
		t.Errorf("DefaultCommand() = %q, want %q", got, "id -u")
	}
	if got := none.DefaultWorkingDir(); got != "/tmp" {
		t.Errorf("DefaultWorkingDir() = %q, want %q", got, "/tmp")
	}

	// 2. ManagementPlugin
	mp, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatal("elevation/none does not implement newapi.ManagementPlugin")
	}
	mgmtActions := mp.ManagementActions()
	if len(mgmtActions) != 1 || mgmtActions[0].Name != "elevate" {
		t.Fatalf("expected 1 management action 'elevate', got %+v", mgmtActions)
	}
	if mgmtActions[0].Category != newapi.ActionCategorySecurity {
		t.Errorf("expected elevate action to be security category, got %+v", mgmtActions[0])
	}
	if len(mp.Actions()) != len(mgmtActions) {
		t.Errorf("Actions() length %d != ManagementActions() length %d", len(mp.Actions()), len(mgmtActions))
	}

	facts := plugin.HostFacts{NodeID: "node-alpine-1", OSFamily: "linux"}
	if err := mp.ValidateAction(facts, "elevate", map[string]any{"command": "id -u"}); err != nil {
		t.Errorf("expected valid elevate action, got: %v", err)
	}
	if err := mp.ValidateAction(facts, "elevate", map[string]any{}); err == nil {
		t.Error("expected validation error when required command option is missing for elevate")
	}
	if err := mp.ValidateAction(facts, "unknown", map[string]any{"command": "id -u"}); err == nil {
		t.Error("expected error for unsupported action")
	}

	// 3. ContextMenuProvider
	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatal("elevation/none does not implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), facts)
	if len(entries) != 1 {
		t.Fatalf("expected 1 context menu entry for compatible host, got %d", len(entries))
	}
	if entries[0].PluginID != "elevation/none" || entries[0].Target != newapi.TargetNode || entries[0].Action != "elevate" {
		t.Errorf("unexpected context menu entry: %+v", entries[0])
	}
	if incompatElev := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "linux", ActiveElevation: "elevation/sudo"}); len(incompatElev) != 0 {
		t.Errorf("expected no context menu entries when ActiveElevation is elevation/sudo, got %+v", incompatElev)
	}

	// 4. TaskCompileProvider & Compile parity
	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatal("elevation/none does not implement newapi.TaskCompileProvider")
	}
	spec, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		ID:       "req-none-1",
		Type:     newapi.TaskTypePluginAction,
		NodeID:   "node-alpine-1",
		PluginID: "elevation/none",
		Action:   "elevate",
		Params:   map[string]any{"command": "uptime"},
	})
	if err != nil {
		t.Fatalf("CompileTask elevate failed: %v", err)
	}
	if len(spec.Steps) != 1 || spec.Steps[0].Module != "ansible.builtin.command" {
		t.Fatalf("unexpected compiled steps: %+v", spec.Steps)
	}
	if spec.Steps[0].Params["cmd"] != "uptime" || spec.Steps[0].Params["become"] != false || spec.Steps[0].Params["become_method"] != "none" {
		t.Errorf("unexpected step params: %+v", spec.Steps[0].Params)
	}

	// Fallback default command "id -u" when command option is empty
	fallbackSpec, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		ID:       "req-none-2",
		Type:     newapi.TaskTypePluginAction,
		NodeID:   "node-alpine-1",
		PluginID: "elevation/none",
		Params:   map[string]any{"action": "elevate"},
	})
	if err != nil {
		t.Fatalf("CompileTask fallback elevate failed: %v", err)
	}
	if fallbackSpec.Steps[0].Params["cmd"] != "id -u" {
		t.Errorf("expected fallback cmd 'id -u', got %v", fallbackSpec.Steps[0].Params["cmd"])
	}

	if _, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{}); err == nil {
		t.Error("expected error when action is empty in CompileTask")
	}
	if _, err := mp.Compile(facts, "invalid_action", map[string]any{"command": "id -u"}); err == nil {
		t.Error("expected error when action is invalid in Compile")
	}
}
