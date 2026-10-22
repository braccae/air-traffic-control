package rpmostree_test

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
	"air-traffic-control/backend/plugins/package/rpmostree"
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

const sampleRpmOstreeStatusJSON = `{
  "deployments": [
    {
      "id": "fedora-silverblue-41.20261020.0",
      "osname": "fedora-silverblue",
      "checksum": "aaaa1111bbbb2222cccc3333dddd4444eeee5555ffff66667777888899990000",
      "version": "41.20261020.0",
      "origin": "fedora:fedora/41/x86_64/silverblue",
      "booted": false,
      "staged": true,
      "pinned": false,
      "requested-packages": ["htop", "tmux"]
    },
    {
      "id": "fedora-silverblue-41.20261015.0",
      "osname": "fedora-silverblue",
      "checksum": "1111222233334444555566667777888899990000aaaabbbbccccddddeeeeffff",
      "version": "41.20261015.0",
      "origin": "fedora:fedora/41/x86_64/silverblue",
      "booted": true,
      "staged": false,
      "pinned": false,
      "requested-packages": ["htop", "tmux"]
    },
    {
      "id": "fedora-silverblue-41.20261001.0",
      "osname": "fedora-silverblue",
      "checksum": "9999888877776666555544443333222211110000ffffeeeeddddccccbbbbaaaa",
      "version": "41.20261001.0",
      "origin": "fedora:fedora/41/x86_64/silverblue",
      "booted": false,
      "staged": false,
      "pinned": true,
      "requested-packages": ["htop"]
    }
  ]
}`

func TestRpmOstreePlugin_RegistrationAndMeta(t *testing.T) {
	p, ok := plugin.Get("package/rpmostree")
	if !ok {
		t.Fatalf("expected package/rpmostree to be registered")
	}

	meta := p.Meta()
	if meta.ID != "package/rpmostree" {
		t.Errorf("Meta().ID = %q, want %q", meta.ID, "package/rpmostree")
	}
	if meta.Kind != plugin.KindPackage {
		t.Errorf("Meta().Kind = %q, want %q", meta.Kind, plugin.KindPackage)
	}
	if meta.Name != "rpmostree" {
		t.Errorf("Meta().Name = %q, want %q", meta.Name, "rpmostree")
	}
	if meta.Order != 20 {
		t.Errorf("Meta().Order = %d, want 20", meta.Order)
	}

	reqs := p.Requirements()
	if len(reqs.HostPackages["dnf"]) == 0 || reqs.HostPackages["dnf"][0] != "rpm-ostree" {
		t.Errorf("unexpected HostPackages: %+v", reqs.HostPackages)
	}
	if len(reqs.ControllerCollections) == 0 || reqs.ControllerCollections[0] != "ansible.builtin" {
		t.Errorf("unexpected ControllerCollections: %+v", reqs.ControllerCollections)
	}
}

func TestRpmOstreePlugin_Compatible(t *testing.T) {
	p := rpmostree.New()

	if !p.Compatible(plugin.HostFacts{OSFamily: "linux"}) {
		t.Error("expected compatible with linux host")
	}
	if !p.Compatible(plugin.HostFacts{OSFamily: "linux", ActivePackage: "package/rpmostree"}) {
		t.Error("expected compatible when ActivePackage is package/rpmostree")
	}
	if p.Compatible(plugin.HostFacts{OSFamily: "windows"}) {
		t.Error("expected incompatible with windows host")
	}
	if p.Compatible(plugin.HostFacts{OSFamily: "linux", ActivePackage: "package/dnf"}) {
		t.Error("expected incompatible when ActivePackage is package/dnf")
	}
}

func TestRpmOstreePlugin_Probe(t *testing.T) {
	p := rpmostree.New()
	ctx := context.Background()

	// Nil runner
	det, err := p.Probe(ctx, nil, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("unexpected error for nil runner: %v", err)
	}
	if det.Detected || det.Status != "OFFLINE" {
		t.Errorf("expected OFFLINE detection for nil runner, got %+v", det)
	}

	// Command error / missing binary
	rMissing := &fakeRunner{err: errors.New("not found")}
	det, err = p.Probe(ctx, rMissing, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("unexpected error for missing binary: %v", err)
	}
	if det.Detected || det.Status != "UNAVAILABLE" {
		t.Errorf("expected UNAVAILABLE detection, got %+v", det)
	}

	// Detected
	rOK := &fakeRunner{output: "/usr/bin/rpm-ostree\n"}
	det, err = p.Probe(ctx, rOK, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !det.Detected || det.Status != "ONLINE" || det.Details != "/usr/bin/rpm-ostree" {
		t.Errorf("unexpected ONLINE detection: %+v", det)
	}
}

func TestRpmOstreePlugin_Read(t *testing.T) {
	p := rpmostree.New()
	ctx := context.Background()

	// Nil runner
	if _, err := p.Read(ctx, nil, plugin.HostFacts{}); err == nil {
		t.Fatal("expected error for nil runner")
	}

	// Command error
	if _, err := p.Read(ctx, &fakeRunner{err: errors.New("ssh failed")}, plugin.HostFacts{}); err == nil {
		t.Fatal("expected error when rpm-ostree status fails")
	}

	// Empty output
	if _, err := p.Read(ctx, &fakeRunner{output: "   "}, plugin.HostFacts{}); err == nil {
		t.Fatal("expected error for empty output")
	}

	// Malformed JSON
	if _, err := p.Read(ctx, &fakeRunner{output: "not-json"}, plugin.HostFacts{}); err == nil {
		t.Fatal("expected error for malformed JSON")
	}

	// Valid status JSON
	r := &fakeRunner{output: sampleRpmOstreeStatusJSON}
	got, err := p.Read(ctx, r, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if !strings.Contains(r.lastCmd, "rpm-ostree status --json") {
		t.Errorf("expected Read to run rpm-ostree status --json, got %q", r.lastCmd)
	}

	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", got)
	}
	if m["currentOrigin"] != "fedora:fedora/41/x86_64/silverblue" {
		t.Errorf("currentOrigin = %v", m["currentOrigin"])
	}
	if m["currentVersion"] != "41.20261015.0" {
		t.Errorf("currentVersion = %v", m["currentVersion"])
	}
	if m["stagedVersion"] != "41.20261020.0" {
		t.Errorf("stagedVersion = %v", m["stagedVersion"])
	}
	if m["rollbackVersion"] != "41.20261001.0" {
		t.Errorf("rollbackVersion = %v", m["rollbackVersion"])
	}
	if m["pendingDeployment"] != true {
		t.Errorf("pendingDeployment = %v, want true", m["pendingDeployment"])
	}
	layered, ok := m["layeredPackages"].([]string)
	if !ok || len(layered) != 2 || layered[0] != "htop" || layered[1] != "tmux" {
		t.Errorf("layeredPackages = %#v, want [htop tmux]", m["layeredPackages"])
	}
	deployments, ok := m["deployments"].([]map[string]any)
	if !ok || len(deployments) != 3 {
		t.Errorf("expected 3 deployments, got %#v", m["deployments"])
	}
}

func TestRpmOstreePlugin_CompileAndCompileTask(t *testing.T) {
	p := rpmostree.New()
	facts := plugin.HostFacts{NodeID: "node-atomic-1", OSFamily: "linux"}

	// Install
	installSpec, err := p.Compile(facts, "install", map[string]any{"name": "vim-enhanced"})
	if err != nil {
		t.Fatalf("Compile(install) failed: %v", err)
	}
	if installSpec.Name != "rpm-ostree install" || len(installSpec.Steps) != 1 {
		t.Fatalf("unexpected install spec: %+v", installSpec)
	}
	if installSpec.Steps[0].Module != "ansible.builtin.command" {
		t.Errorf("unexpected module: %s", installSpec.Steps[0].Module)
	}
	if installSpec.Steps[0].Params["cmd"] != "rpm-ostree install --idempotent vim-enhanced" {
		t.Errorf("unexpected cmd: %v", installSpec.Steps[0].Params["cmd"])
	}

	// Install missing name
	if _, err := p.Compile(facts, "install", map[string]any{}); err == nil {
		t.Error("expected error when package name is missing for install")
	}

	// Update
	updateSpec, err := p.Compile(facts, "update", map[string]any{})
	if err != nil {
		t.Fatalf("Compile(update) failed: %v", err)
	}
	if updateSpec.Steps[0].Params["cmd"] != "rpm-ostree upgrade" {
		t.Errorf("unexpected update cmd: %v", updateSpec.Steps[0].Params["cmd"])
	}

	// Remove
	removeSpec, err := p.Compile(facts, "remove", map[string]any{"name": "vim-enhanced"})
	if err != nil {
		t.Fatalf("Compile(remove) failed: %v", err)
	}
	if removeSpec.Steps[0].Params["cmd"] != "rpm-ostree uninstall vim-enhanced" {
		t.Errorf("unexpected remove cmd: %v", removeSpec.Steps[0].Params["cmd"])
	}

	// Remove missing name
	if _, err := p.Compile(facts, "remove", map[string]any{}); err == nil {
		t.Error("expected error when package name is missing for remove")
	}

	// Unsupported action
	if _, err := p.Compile(facts, "rebaseto", map[string]any{}); err == nil {
		t.Error("expected error for unsupported action")
	}

	// CompileTask parity
	taskSpec, err := p.CompileTask(context.Background(), facts, newapi.TaskRequest{
		ID:       "req-1",
		Type:     newapi.TaskTypePluginAction,
		NodeID:   "node-atomic-1",
		PluginID: "package/rpmostree",
		Action:   "install",
		Params:   map[string]any{"name": "htop"},
	})
	if err != nil {
		t.Fatalf("CompileTask(install) failed: %v", err)
	}
	if taskSpec.Steps[0].Params["cmd"] != "rpm-ostree install --idempotent htop" {
		t.Errorf("unexpected CompileTask cmd: %v", taskSpec.Steps[0].Params["cmd"])
	}

	// CompileTask with action inside Params
	taskFromParams, err := p.CompileTask(context.Background(), facts, newapi.TaskRequest{
		ID:       "req-2",
		Type:     newapi.TaskTypePluginAction,
		NodeID:   "node-atomic-1",
		PluginID: "package/rpmostree",
		Params:   map[string]any{"action": "update"},
	})
	if err != nil {
		t.Fatalf("CompileTask(params.action=update) failed: %v", err)
	}
	if taskFromParams.Steps[0].Params["cmd"] != "rpm-ostree upgrade" {
		t.Errorf("unexpected CompileTask update cmd: %v", taskFromParams.Steps[0].Params["cmd"])
	}

	// CompileTask missing action
	if _, err := p.CompileTask(context.Background(), facts, newapi.TaskRequest{
		ID:       "req-3",
		Type:     newapi.TaskTypePluginAction,
		NodeID:   "node-atomic-1",
		PluginID: "package/rpmostree",
	}); err == nil {
		t.Error("expected error when action is missing from TaskRequest")
	}
}

func TestRpmOstreePlugin_V2InterfacesAndCapabilities(t *testing.T) {
	p, ok := plugin.Get("package/rpmostree")
	if !ok {
		t.Fatalf("package/rpmostree not registered")
	}

	// 1. DataCollectionPlugin
	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatal("expected package/rpmostree to implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.Interval != 60*time.Second || sched.TTL != 60*time.Second {
		t.Errorf("expected 60s schedule for package/rpmostree, got %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "deployments" {
		t.Errorf("unexpected collection schema: %+v", schema)
	}

	// 2. ManagementPlugin
	mgmt, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatal("expected package/rpmostree to implement newapi.ManagementPlugin")
	}
	mgmtActions := mgmt.ManagementActions()
	if len(mgmtActions) != 3 {
		t.Fatalf("expected 3 management actions, got %d", len(mgmtActions))
	}
	actorActions := p.(plugin.Actor).Actions()
	if len(actorActions) != len(mgmtActions) {
		t.Errorf("Actions() len = %d, ManagementActions() len = %d", len(actorActions), len(mgmtActions))
	}

	facts := plugin.HostFacts{NodeID: "node-1", OSFamily: "linux"}
	if err := mgmt.ValidateAction(facts, "install", map[string]any{"name": "tmux"}); err != nil {
		t.Errorf("ValidateAction(install) failed: %v", err)
	}
	if err := mgmt.ValidateAction(facts, "install", map[string]any{}); err == nil {
		t.Error("expected ValidateAction(install) without name to fail")
	}
	if err := mgmt.ValidateAction(facts, "install", map[string]any{"name": "   "}); err == nil {
		t.Error("expected ValidateAction(install) with blank name to fail")
	}
	if err := mgmt.ValidateAction(facts, "update", map[string]any{}); err != nil {
		t.Errorf("ValidateAction(update) failed: %v", err)
	}
	if err := mgmt.ValidateAction(facts, "remove", map[string]any{"name": "tmux"}); err != nil {
		t.Errorf("ValidateAction(remove) failed: %v", err)
	}
	if err := mgmt.ValidateAction(facts, "remove", map[string]any{}); err == nil {
		t.Error("expected ValidateAction(remove) without name to fail")
	}
	if err := mgmt.ValidateAction(facts, "unknown", map[string]any{}); err == nil {
		t.Error("expected ValidateAction(unknown) to fail")
	}

	// 3. CategorizedSettingsProvider
	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatal("expected package/rpmostree to implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if catPage.ID != rpmostree.SettingsPageID || catPage.Category != "package" || catPage.Scope != newapi.SettingsScopeDatacenter {
		t.Errorf("unexpected categorized settings page: %+v", catPage)
	}
	if err := settingsframework.ValidateSchema(catPage.SettingsPage); err != nil {
		t.Errorf("ValidateSchema failed: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Errorf("ValidateCategorizedSettingsPage failed: %v", err)
	}
	if _, ok := plugin.GetSettingsPage("package/rpmostree"); !ok {
		t.Error("expected package/rpmostree settings page in global registry")
	}
	if regCat, ok := plugin.GetCategorizedSettingsPage("package/rpmostree"); !ok || regCat.Category != "package" {
		t.Errorf("expected package/rpmostree categorized settings page in global registry, got %+v (ok=%v)", regCat, ok)
	}

	// Verify settings helper defaults
	if got := rpmostree.UpdatePolicy(); got != rpmostree.DefaultUpdatePolicy {
		t.Errorf("UpdatePolicy() = %q, want %q", got, rpmostree.DefaultUpdatePolicy)
	}
	if got := rpmostree.IdempotentInstall(); got != rpmostree.DefaultIdempotentInstall {
		t.Errorf("IdempotentInstall() = %v, want %v", got, rpmostree.DefaultIdempotentInstall)
	}
	if got := rpmostree.AllowLiveApply(); got != rpmostree.DefaultAllowLiveApply {
		t.Errorf("AllowLiveApply() = %v, want %v", got, rpmostree.DefaultAllowLiveApply)
	}
	if got := rpmostree.RebootAfterUpgrade(); got != rpmostree.DefaultRebootAfterUpgrade {
		t.Errorf("RebootAfterUpgrade() = %v, want %v", got, rpmostree.DefaultRebootAfterUpgrade)
	}
	if got := rpmostree.DefaultRefspec(); got != "" {
		t.Errorf("DefaultRefspec() = %q, want empty", got)
	}
	if _, ok := rpmostree.RegistryAuthVaultVar(); ok {
		t.Error("expected RegistryAuthVaultVar() to be unset by default")
	}

	// 4. ContextMenuProvider
	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatal("expected package/rpmostree to implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), facts)
	if len(entries) != 3 {
		t.Fatalf("expected 3 context menu entries for compatible linux host, got %d", len(entries))
	}
	if entries[0].ID != "rpmostree-update" || entries[0].Target != newapi.TargetNode || entries[0].PluginID != "package/rpmostree" {
		t.Errorf("unexpected first context menu entry: %+v", entries[0])
	}
	if entries[2].ID != "rpmostree-remove" || !entries[2].Danger {
		t.Errorf("expected third context menu entry to be danger remove action, got %+v", entries[2])
	}
	if winEntries := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "windows"}); len(winEntries) != 0 {
		t.Errorf("expected no context menu entries for windows host, got %+v", winEntries)
	}
}
