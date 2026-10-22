package btrfs_test

import (
	"context"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	_ "air-traffic-control/backend/plugins/all"
)

type mockRunner struct {
	responses map[string]string
}

func (m *mockRunner) RunCommand(ctx context.Context, cmd string) (string, error) {
	for pattern, resp := range m.responses {
		if strings.Contains(cmd, pattern) {
			return resp, nil
		}
	}
	return "", nil
}

func TestBtrfsPlugin_ProbeReadAndCompile(t *testing.T) {
	p, ok := plugin.Get("storage/btrfs")
	if !ok {
		t.Fatalf("storage/btrfs not registered")
	}

	runner := &mockRunner{
		responses: map[string]string{
			"command -v btrfs":      "btrfs",
			"btrfs filesystem show": "Label: 'pool'  uuid: 1234-5678\n\tTotal devices 1 FS bytes used 1.00GiB",
		},
	}

	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if !det.Detected {
		t.Errorf("expected btrfs detected")
	}

	reader := p.(plugin.Reader)
	data, err := reader.Read(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	m := data.(map[string]any)
	if !strings.Contains(m["btrfs"].(string), "Label: 'pool'") {
		t.Errorf("unexpected read: %+v", m)
	}

	actor := p.(plugin.Actor)
	task, err := actor.Compile(plugin.HostFacts{NodeID: "node1"}, "create_subvolume", map[string]any{
		"path": "/data/subvol1",
	})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if len(task.Steps) != 1 || task.Steps[0].Module != "community.general.btrfs_subvolume" {
		t.Errorf("unexpected steps: %+v", task.Steps)
	}
}

func TestBtrfsPlugin_V2InterfacesAndCapabilities(t *testing.T) {
	p, ok := plugin.Get("storage/btrfs")
	if !ok {
		t.Fatalf("storage/btrfs not registered")
	}

	// 1. Verify DataCollectionPlugin
	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatal("expected storage/btrfs to implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.Interval <= 0 || sched.TTL <= 0 {
		t.Errorf("expected positive collection schedule interval and TTL, got %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "btrfs" {
		t.Errorf("unexpected collection schema: %+v", schema)
	}

	// 2. Verify ManagementPlugin
	mgmt, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatal("expected storage/btrfs to implement newapi.ManagementPlugin")
	}
	actions := mgmt.ManagementActions()
	if len(actions) != 2 {
		t.Fatalf("expected 2 management actions, got %d", len(actions))
	}
	if err := mgmt.ValidateAction(plugin.HostFacts{OSFamily: "linux"}, "create_subvolume", map[string]any{"path": "/data/subvol"}); err != nil {
		t.Errorf("expected valid create_subvolume options to pass validation, got: %v", err)
	}
	if err := mgmt.ValidateAction(plugin.HostFacts{OSFamily: "linux"}, "create_subvolume", map[string]any{}); err == nil {
		t.Error("expected missing path option to fail validation")
	}
	if err := mgmt.ValidateAction(plugin.HostFacts{OSFamily: "linux"}, "unknown_action", map[string]any{}); err == nil {
		t.Error("expected unknown action to fail validation")
	}

	// 3. Verify CategorizedSettingsProvider
	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatal("expected storage/btrfs to implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if catPage.ID != "storage/btrfs" || catPage.Category != "storage" || catPage.Scope != newapi.SettingsScopeDatacenter {
		t.Errorf("unexpected categorized settings page: %+v", catPage)
	}
	if err := settingsframework.ValidateSchema(catPage.SettingsPage); err != nil {
		t.Errorf("ValidateSchema failed: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Errorf("ValidateCategorizedSettingsPage failed: %v", err)
	}
	if _, ok := plugin.GetCategorizedSettingsPage("storage/btrfs"); !ok {
		t.Error("expected storage/btrfs categorized settings page to be registered in global registry")
	}

	// 4. Verify ContextMenuProvider
	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatal("expected storage/btrfs to implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "linux"})
	if len(entries) != 2 {
		t.Fatalf("expected 2 context menu entries for linux host, got %d", len(entries))
	}
	if entries[0].Target != newapi.TargetStorage || entries[0].PluginID != "storage/btrfs" {
		t.Errorf("unexpected first context menu entry: %+v", entries[0])
	}
	if !entries[1].Danger || entries[1].ConfirmPrompt == "" {
		t.Errorf("expected second context menu entry to be a danger action with confirmation prompt, got %+v", entries[1])
	}
	if winEntries := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "windows"}); len(winEntries) != 0 {
		t.Errorf("expected no context menu entries for incompatible windows host, got %+v", winEntries)
	}

	// 5. Verify TaskCompileProvider & TaskTypeProvider
	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatal("expected storage/btrfs to implement newapi.TaskCompileProvider")
	}
	ttp, ok := p.(newapi.TaskTypeProvider)
	if !ok {
		t.Fatal("expected storage/btrfs to implement newapi.TaskTypeProvider")
	}
	taskTypes := ttp.TaskTypes()
	if len(taskTypes) != 1 || taskTypes[0].Type != newapi.TaskTypeStorageOp {
		t.Fatalf("expected 1 storage_op task type, got %+v", taskTypes)
	}

	compiled, err := tcp.CompileTask(context.Background(), plugin.HostFacts{NodeID: "node1", OSFamily: "linux"}, newapi.TaskRequest{
		ID:       "req-1",
		Type:     newapi.TaskTypeStorageOp,
		NodeID:   "node1",
		PluginID: "storage/btrfs",
		Action:   "delete_subvolume",
		Params: map[string]any{
			"path": "/data/old_subvol",
		},
	})
	if err != nil {
		t.Fatalf("CompileTask failed: %v", err)
	}
	if len(compiled.Steps) != 1 || compiled.Steps[0].Params["state"] != "absent" {
		t.Errorf("unexpected compiled task steps: %+v", compiled.Steps)
	}
}
