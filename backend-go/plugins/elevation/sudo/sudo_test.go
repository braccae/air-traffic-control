package sudo_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	_ "air-traffic-control/backend/plugins/all"
	"air-traffic-control/backend/plugins/elevation/sudo"
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

func TestSudoPlugin_RegistrationAndMeta(t *testing.T) {
	p, ok := plugin.Get("elevation/sudo")
	if !ok {
		t.Fatal("expected elevation/sudo to be registered in default registry")
	}

	meta := p.Meta()
	if meta.ID != "elevation/sudo" || meta.Kind != plugin.KindElevation || meta.Name != "sudo" || meta.Order != 20 {
		t.Errorf("unexpected meta: %+v", meta)
	}

	if !p.Compatible(plugin.HostFacts{OSFamily: "linux"}) {
		t.Error("expected compatible with linux host")
	}
	if !p.Compatible(plugin.HostFacts{OSFamily: "freebsd"}) {
		t.Error("expected compatible with freebsd host")
	}
	if !p.Compatible(plugin.HostFacts{OSFamily: "linux", ActiveElevation: "elevation/sudo"}) {
		t.Error("expected compatible when ActiveElevation is elevation/sudo")
	}
	if p.Compatible(plugin.HostFacts{OSFamily: "windows"}) {
		t.Error("expected incompatible with windows host")
	}
	if p.Compatible(plugin.HostFacts{OSFamily: "linux", ActiveElevation: "elevation/run0"}) {
		t.Error("expected incompatible when ActiveElevation is elevation/run0")
	}

	reqs := p.Requirements()
	for _, mgr := range []string{"dnf", "apt", "apk"} {
		if len(reqs.HostPackages[mgr]) == 0 || reqs.HostPackages[mgr][0] != "sudo" {
			t.Errorf("unexpected %s host package requirements: %+v", mgr, reqs)
		}
	}
	if len(reqs.ControllerCollections) == 0 || reqs.ControllerCollections[0] != "ansible.builtin" {
		t.Errorf("unexpected controller collections: %+v", reqs)
	}
}

func TestSudoPlugin_Probe(t *testing.T) {
	p := sudo.New()
	ctx := context.Background()

	det, err := p.Probe(ctx, nil, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("unexpected error on nil runner: %v", err)
	}
	if det.Detected || det.Status != "OFFLINE" {
		t.Errorf("expected OFFLINE detection on nil runner, got %+v", det)
	}

	r := &fakeRunner{output: "0\n"}
	det, err = p.Probe(ctx, r, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(r.lastCmd, "sudo -n id -u") {
		t.Errorf("expected sudo probe command, got %q", r.lastCmd)
	}
	if !det.Detected || det.Status != "ONLINE" {
		t.Errorf("unexpected detection: %+v", det)
	}

	rNonRoot := &fakeRunner{output: "1000\n"}
	det, err = p.Probe(ctx, rNonRoot, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if det.Detected || det.Status != "UNAVAILABLE" {
		t.Errorf("expected UNAVAILABLE detection for non-zero uid, got %+v", det)
	}

	rErr := &fakeRunner{err: errors.New("sudo: a password is required")}
	det, err = p.Probe(ctx, rErr, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if det.Detected || det.Status != "UNAVAILABLE" {
		t.Errorf("expected UNAVAILABLE detection on error, got %+v", det)
	}
}

func TestSudoPlugin_V2InterfacesAndCapabilities(t *testing.T) {
	p, ok := plugin.Get("elevation/sudo")
	if !ok {
		t.Fatal("elevation/sudo not registered")
	}

	// 1. CategorizedSettingsProvider
	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatal("elevation/sudo does not implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if catPage.ID != "elevation/sudo" || catPage.Category != "elevation" || catPage.Scope != newapi.SettingsScopeDatacenter {
		t.Errorf("unexpected categorized settings page: %+v", catPage)
	}
	if err := settingsframework.ValidateSchema(catPage.SettingsPage); err != nil {
		t.Fatalf("ValidateSchema failed: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("ValidateCategorizedSettingsPage failed: %v", err)
	}
	if _, ok := plugin.GetCategorizedSettingsPage("elevation/sudo"); !ok {
		t.Error("expected elevation/sudo categorized settings page to be registered in global registry")
	}
	if got := sudo.DefaultTargetUser(); got != "root" {
		t.Errorf("DefaultTargetUser() = %q, want %q", got, "root")
	}
	if !sudo.DefaultNonInteractive() {
		t.Error("expected DefaultNonInteractive() default true")
	}

	// 2. ManagementPlugin
	mp, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatal("elevation/sudo does not implement newapi.ManagementPlugin")
	}
	mgmtActions := mp.ManagementActions()
	if len(mgmtActions) != 1 || mgmtActions[0].Name != "elevate" {
		t.Fatalf("expected 1 management action 'elevate', got %+v", mgmtActions)
	}
	if mgmtActions[0].Category != newapi.ActionCategorySecurity || !mgmtActions[0].Dangerous {
		t.Errorf("expected elevate action to be security category and dangerous, got %+v", mgmtActions[0])
	}
	if len(mp.Actions()) != len(mgmtActions) {
		t.Errorf("Actions() length %d != ManagementActions() length %d", len(mp.Actions()), len(mgmtActions))
	}

	facts := plugin.HostFacts{NodeID: "node-ubuntu-1", OSFamily: "linux"}
	if err := mp.ValidateAction(facts, "elevate", map[string]any{"command": "systemctl restart sshd"}); err != nil {
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
		t.Fatal("elevation/sudo does not implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), facts)
	if len(entries) != 1 {
		t.Fatalf("expected 1 context menu entry for compatible linux host, got %d", len(entries))
	}
	if entries[0].PluginID != "elevation/sudo" || entries[0].Target != newapi.TargetNode || entries[0].Action != "elevate" || !entries[0].Danger {
		t.Errorf("unexpected context menu entry: %+v", entries[0])
	}
	if incompat := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "windows"}); len(incompat) != 0 {
		t.Errorf("expected no context menu entries for incompatible windows host, got %+v", incompat)
	}
	if incompatElev := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "linux", ActiveElevation: "elevation/run0"}); len(incompatElev) != 0 {
		t.Errorf("expected no context menu entries when ActiveElevation is elevation/run0, got %+v", incompatElev)
	}

	// 4. TaskCompileProvider & Compile parity
	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatal("elevation/sudo does not implement newapi.TaskCompileProvider")
	}
	spec, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		ID:       "req-sudo-1",
		Type:     newapi.TaskTypePluginAction,
		NodeID:   "node-ubuntu-1",
		PluginID: "elevation/sudo",
		Action:   "elevate",
		Params:   map[string]any{"command": "systemctl restart caddy"},
	})
	if err != nil {
		t.Fatalf("CompileTask elevate failed: %v", err)
	}
	if len(spec.Steps) != 1 || spec.Steps[0].Module != "ansible.builtin.command" {
		t.Fatalf("unexpected compiled steps: %+v", spec.Steps)
	}
	if spec.Steps[0].Params["cmd"] != "systemctl restart caddy" || spec.Steps[0].Params["become"] != true || spec.Steps[0].Params["become_method"] != "sudo" {
		t.Errorf("unexpected step params: %+v", spec.Steps[0].Params)
	}

	// Fallback default command "id -u" when command option is empty
	fallbackSpec, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		ID:       "req-sudo-2",
		Type:     newapi.TaskTypePluginAction,
		NodeID:   "node-ubuntu-1",
		PluginID: "elevation/sudo",
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
