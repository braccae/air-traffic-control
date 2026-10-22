package zfs_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	_ "air-traffic-control/backend/plugins/all"
	"air-traffic-control/backend/plugins/storage/zfs"
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

func TestZFSPlugin_MetaAndCompatibility(t *testing.T) {
	p, ok := plugin.Get("storage/zfs")
	if !ok {
		t.Fatalf("storage/zfs plugin not registered")
	}

	meta := p.Meta()
	if meta.Kind != plugin.KindStorage || meta.Name != "zfs" {
		t.Errorf("unexpected meta: %+v", meta)
	}

	if !p.Compatible(plugin.HostFacts{OSFamily: "linux"}) {
		t.Errorf("expected compatible with linux")
	}
	if p.Compatible(plugin.HostFacts{OSFamily: "windows"}) {
		t.Errorf("expected incompatible with windows")
	}
	if p.Compatible(plugin.HostFacts{OSFamily: "router_switch"}) {
		t.Errorf("expected incompatible with router_switch")
	}
}

func TestZFSPlugin_NewAPIInterfaces(t *testing.T) {
	p, ok := plugin.Get("storage/zfs")
	if !ok {
		t.Fatal("storage/zfs not registered")
	}

	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatal("expected storage/zfs to implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.TTL != 60*time.Second || sched.Interval != 60*time.Second {
		t.Errorf("expected 60s schedule for storage/zfs, got %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "zfsList" {
		t.Errorf("unexpected collection schema: %+v", schema)
	}

	mgmt, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatal("expected storage/zfs to implement newapi.ManagementPlugin")
	}
	mgmtActions := mgmt.ManagementActions()
	if len(mgmtActions) != 4 {
		t.Fatalf("expected 4 management actions, got %d", len(mgmtActions))
	}
	if len(p.(plugin.Actor).Actions()) != len(mgmtActions) {
		t.Errorf("expected Actions() length %d to match ManagementActions() length %d", len(p.(plugin.Actor).Actions()), len(mgmtActions))
	}

	facts := plugin.HostFacts{NodeID: "node1", OSFamily: "linux"}
	if err := mgmt.ValidateAction(facts, "scrub", map[string]any{"pool": "tank", "state": "scrubbing"}); err != nil {
		t.Errorf("expected valid scrub action, got: %v", err)
	}
	if err := mgmt.ValidateAction(facts, "scrub", map[string]any{"pool": "tank", "state": "invalid"}); err == nil {
		t.Error("expected error for invalid scrub state enum")
	}
	if err := mgmt.ValidateAction(facts, "scrub", map[string]any{}); err == nil {
		t.Error("expected error for missing required pool option")
	}
	if err := mgmt.ValidateAction(facts, "unknown_action", map[string]any{}); err == nil {
		t.Error("expected error for unknown action")
	}
}

func TestZFSPlugin_SettingsFramework(t *testing.T) {
	p, ok := plugin.Get("storage/zfs")
	if !ok {
		t.Fatal("storage/zfs not registered")
	}

	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatal("expected storage/zfs to implement newapi.CategorizedSettingsProvider")
	}

	catPage := csp.CategorizedSettingsPage()
	if catPage.ID != zfs.SettingsPageID {
		t.Errorf("expected settings page ID %q, got %q", zfs.SettingsPageID, catPage.ID)
	}
	if catPage.Scope != newapi.SettingsScopeDatacenter || catPage.Category != "storage" {
		t.Errorf("expected datacenter/storage categorized page, got scope=%q category=%q", catPage.Scope, catPage.Category)
	}
	if err := settingsframework.ValidateSchema(catPage.SettingsPage); err != nil {
		t.Fatalf("ValidateSchema failed: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("ValidateCategorizedSettingsPage failed: %v", err)
	}

	if _, ok := plugin.GetSettingsPage("storage/zfs"); !ok {
		t.Error("expected storage/zfs settings page to be registered in global registry")
	}
	if regCat, ok := plugin.GetCategorizedSettingsPage("storage/zfs"); !ok || regCat.Category != "storage" {
		t.Errorf("expected storage/zfs categorized settings page in global registry, got %+v (ok=%v)", regCat, ok)
	}

	// Verify typed helper defaults
	if got := zfs.DefaultPool(); got != zfs.DefaultPoolName {
		t.Errorf("DefaultPool() = %q, want %q", got, zfs.DefaultPoolName)
	}
	if got := zfs.ScrubIntervalDays(); got != zfs.DefaultScrubIntervalDays {
		t.Errorf("ScrubIntervalDays() = %d, want %d", got, zfs.DefaultScrubIntervalDays)
	}
	if !zfs.AutoSnapshotEnabled() {
		t.Error("expected AutoSnapshotEnabled() default true")
	}
	if _, ok := zfs.EncryptionPassphraseVaultVar(); ok {
		t.Error("expected EncryptionPassphraseVaultVar() to be unset by default")
	}
}

func TestZFSPlugin_ContextMenu(t *testing.T) {
	p, ok := plugin.Get("storage/zfs")
	if !ok {
		t.Fatal("storage/zfs not registered")
	}

	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatal("expected storage/zfs to implement newapi.ContextMenuProvider")
	}

	ctx := context.Background()
	linuxFacts := plugin.HostFacts{NodeID: "node1", OSFamily: "linux"}
	entries := cmp.ContextMenuEntries(ctx, linuxFacts)
	if len(entries) != 4 {
		t.Fatalf("expected 4 context menu entries for linux host, got %d", len(entries))
	}
	if entries[0].ID != "zfs-scrub" || entries[0].Target != newapi.TargetStorage || entries[0].PluginID != "storage/zfs" {
		t.Errorf("unexpected first context menu entry: %+v", entries[0])
	}
	if entries[3].ID != "zfs-destroy-dataset" || !entries[3].Danger || entries[3].ConfirmPrompt == "" {
		t.Errorf("expected danger entry last, got %+v", entries[3])
	}

	// Incompatible host returns nil
	winEntries := cmp.ContextMenuEntries(ctx, plugin.HostFacts{NodeID: "win1", OSFamily: "windows"})
	if len(winEntries) != 0 {
		t.Errorf("expected 0 context menu entries for windows host, got %d", len(winEntries))
	}

	// Verify global registry context menu query includes storage/zfs entries
	regEntries := plugin.ListContextMenuEntries(ctx, newapi.TargetStorage, linuxFacts)
	foundZFS := false
	for _, e := range regEntries {
		if e.PluginID == "storage/zfs" {
			foundZFS = true
			break
		}
	}
	if !foundZFS {
		t.Error("expected global registry ListContextMenuEntries(TargetStorage) to include storage/zfs entries")
	}
}

func TestZFSPlugin_Probe(t *testing.T) {
	p, ok := plugin.Get("storage/zfs")
	if !ok {
		t.Fatal("storage/zfs not registered")
	}

	runner := &mockRunner{
		responses: map[string]string{
			"command -v zfs": "zfs\nzpool",
			"zpool list -H":  "tank\nrpool",
		},
	}

	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("probe error: %v", err)
	}
	if !det.Detected {
		t.Errorf("expected detected=true")
	}
	if !strings.Contains(det.Status, "2 pool") {
		t.Errorf("unexpected status: %s", det.Status)
	}

	// Test no pools found
	emptyRunner := &mockRunner{
		responses: map[string]string{
			"command -v zfs": "zfs\nzpool",
			"zpool list -H":  "no pools available",
		},
	}
	emptyDet, err := p.Probe(context.Background(), emptyRunner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("probe error: %v", err)
	}
	if emptyDet.Detected {
		t.Errorf("expected detected=false when no pools are available")
	}
}

func TestZFSPlugin_Read(t *testing.T) {
	p, ok := plugin.Get("storage/zfs")
	if !ok {
		t.Fatal("storage/zfs not registered")
	}

	reader, ok := p.(plugin.Reader)
	if !ok {
		t.Fatalf("expected plugin to implement plugin.Reader")
	}

	runner := &mockRunner{
		responses: map[string]string{
			"findmnt": "{\"filesystems\": []}\n===ZFS===\ntank/data 10G 5G 5G /tank/data\n===ZPOOL===\npool: tank state: ONLINE\n===ZPOOL_LIST===\ntank 100G 20G 80G ONLINE",
		},
	}

	data, err := reader.Read(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	m, ok := data.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any return, got %T", data)
	}
	if zfsList, _ := m["zfsList"].(string); !strings.Contains(zfsList, "tank/data") {
		t.Errorf("expected zfsList containing tank/data, got %q", zfsList)
	}
}

func TestZFSPlugin_ActionsAndCompile(t *testing.T) {
	p, ok := plugin.Get("storage/zfs")
	if !ok {
		t.Fatal("storage/zfs not registered")
	}

	actor, ok := p.(plugin.Actor)
	if !ok {
		t.Fatalf("expected plugin to implement plugin.Actor")
	}

	facts := plugin.HostFacts{NodeID: "node1"}

	// 1. Scrub action
	task, err := actor.Compile(facts, "scrub", map[string]any{
		"pool": "tank",
	})
	if err != nil {
		t.Fatalf("scrub compile failed: %v", err)
	}
	if len(task.Steps) != 1 || task.Steps[0].Module != "community.general.zpool" {
		t.Errorf("unexpected scrub steps: %+v", task.Steps)
	}

	// 2. Snapshot action
	task, err = actor.Compile(facts, "snapshot", map[string]any{
		"dataset":  "tank/data",
		"snapshot": "2026-10-19",
	})
	if err != nil {
		t.Fatalf("snapshot compile failed: %v", err)
	}
	if task.Steps[0].Params["name"] != "tank/data@2026-10-19" {
		t.Errorf("unexpected snapshot name: %v", task.Steps[0].Params["name"])
	}

	// 3. Create dataset action
	task, err = actor.Compile(facts, "create_dataset", map[string]any{
		"name":       "tank/containers",
		"mountpoint": "/var/lib/containers",
	})
	if err != nil {
		t.Fatalf("create_dataset compile failed: %v", err)
	}
	if task.Steps[0].Params["name"] != "tank/containers" || task.Steps[0].Params["mountpoint"] != "/var/lib/containers" {
		t.Errorf("unexpected create_dataset params: %+v", task.Steps[0].Params)
	}

	// 4. Destroy dataset action
	task, err = actor.Compile(facts, "destroy_dataset", map[string]any{
		"name": "tank/old",
	})
	if err != nil {
		t.Fatalf("destroy_dataset compile failed: %v", err)
	}
	if task.Steps[0].Params["name"] != "tank/old" || task.Steps[0].Params["state"] != "absent" {
		t.Errorf("unexpected destroy_dataset params: %+v", task.Steps[0].Params)
	}

	// 5. CompileTask (TaskCompileProvider)
	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatal("expected plugin to implement newapi.TaskCompileProvider")
	}
	compiledTask, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		ID:       "req-1",
		Type:     newapi.TaskTypeStorageOp,
		NodeID:   "node1",
		PluginID: "storage/zfs",
		Params: map[string]any{
			"target":    "tank",
			"operation": "scrub",
		},
	})
	if err != nil {
		t.Fatalf("CompileTask failed: %v", err)
	}
	if compiledTask.Name != "Scrub ZFS pool tank" {
		t.Errorf("unexpected CompileTask name: %q", compiledTask.Name)
	}
}
