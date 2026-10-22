package restic_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	_ "air-traffic-control/backend/plugins/all"
	"air-traffic-control/backend/plugins/backup/restic"
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

func TestResticPlugin_MetaAndCompatibility(t *testing.T) {
	p, ok := plugin.Get("backup/restic")
	if !ok {
		t.Fatalf("backup/restic not registered")
	}

	meta := p.Meta()
	if meta.ID != "backup/restic" || meta.Kind != plugin.KindBackup || meta.Name != "restic" {
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

	reqs := p.Requirements()
	if len(reqs.HostPackages["dnf"]) == 0 || reqs.HostPackages["dnf"][0] != "restic" {
		t.Errorf("unexpected requirements: %+v", reqs)
	}
}

func TestResticPlugin_ProbeReadAndCompile(t *testing.T) {
	p, ok := plugin.Get("backup/restic")
	if !ok {
		t.Fatalf("backup/restic not registered")
	}

	runner := &mockRunner{
		responses: map[string]string{
			"command -v restic": "restic",
			"snapshots --json":  "[{\"id\":\"abc12345\",\"short_id\":\"abc1234\",\"time\":\"2026-10-19T10:00:00Z\",\"paths\":[\"/data\"],\"tags\":[\"daily\"]}]",
		},
	}

	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if !det.Detected {
		t.Errorf("expected restic detected")
	}

	// Verify unavailable probe when binary is missing
	emptyDet, err := p.Probe(context.Background(), &mockRunner{responses: map[string]string{}}, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if emptyDet.Detected || emptyDet.Status != "UNAVAILABLE" {
		t.Errorf("expected UNAVAILABLE detection when restic missing, got %+v", emptyDet)
	}

	reader := p.(plugin.Reader)
	data, err := reader.Read(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	m := data.(map[string]any)
	if m["repo"] != "/var/backups/restic" {
		t.Errorf("unexpected repo: %v", m["repo"])
	}

	// Verify per-host override in Read
	overrideData, err := reader.Read(context.Background(), runner, plugin.HostFacts{
		OSFamily:  "linux",
		Overrides: map[string]string{"resticRepo": "/srv/restic-custom"},
	})
	if err != nil {
		t.Fatalf("read with override failed: %v", err)
	}
	if overrideData.(map[string]any)["repo"] != "/srv/restic-custom" {
		t.Errorf("expected override repo /srv/restic-custom, got %v", overrideData.(map[string]any)["repo"])
	}

	actor := p.(plugin.Actor)
	task, err := actor.Compile(plugin.HostFacts{NodeID: "node1"}, "backup", map[string]any{
		"path": "/etc",
		"tags": "config,daily",
	})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if len(task.Steps) != 1 || task.Steps[0].Module != "ansible.builtin.command" {
		t.Errorf("unexpected steps: %+v", task.Steps)
	}

	checkTask, err := actor.Compile(plugin.HostFacts{NodeID: "node1"}, "check", map[string]any{
		"repo": "/mnt/backups",
	})
	if err != nil {
		t.Fatalf("check compile failed: %v", err)
	}
	if len(checkTask.Steps) != 1 || !strings.Contains(checkTask.Steps[0].Params["cmd"].(string), "restic -r /mnt/backups check") {
		t.Errorf("unexpected check steps: %+v", checkTask.Steps)
	}
}

func TestResticPlugin_V2InterfacesAndCapabilities(t *testing.T) {
	p, ok := plugin.Get("backup/restic")
	if !ok {
		t.Fatalf("backup/restic not registered")
	}

	// 1. Verify DataCollectionPlugin
	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatal("expected backup/restic to implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.Interval != 60*time.Second || sched.TTL != 60*time.Second {
		t.Errorf("expected 60s schedule for backup/restic, got %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "snapshots" {
		t.Errorf("unexpected collection schema: %+v", schema)
	}

	// 2. Verify ManagementPlugin
	mgmt, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatal("expected backup/restic to implement newapi.ManagementPlugin")
	}
	actions := mgmt.ManagementActions()
	if len(actions) != 2 {
		t.Fatalf("expected 2 management actions, got %d", len(actions))
	}
	if len(p.(plugin.Actor).Actions()) != len(actions) {
		t.Errorf("expected Actions() length %d to match ManagementActions() length %d", len(p.(plugin.Actor).Actions()), len(actions))
	}

	facts := plugin.HostFacts{NodeID: "node1", OSFamily: "linux"}
	if err := mgmt.ValidateAction(facts, "backup", map[string]any{"path": "/etc"}); err != nil {
		t.Errorf("expected valid backup options to pass validation, got: %v", err)
	}
	if err := mgmt.ValidateAction(facts, "backup", map[string]any{}); err == nil {
		t.Error("expected missing path option on backup to fail validation")
	}
	if err := mgmt.ValidateAction(facts, "check", map[string]any{}); err != nil {
		t.Errorf("expected check action with default repo to pass validation, got: %v", err)
	}
	if err := mgmt.ValidateAction(facts, "unknown_action", map[string]any{}); err == nil {
		t.Error("expected unknown action to fail validation")
	}

	// 3. Verify CategorizedSettingsProvider
	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatal("expected backup/restic to implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if catPage.ID != restic.SettingsPageID || catPage.Category != "backup" || catPage.Scope != newapi.SettingsScopeDatacenter {
		t.Errorf("unexpected categorized settings page: %+v", catPage)
	}
	if len(catPage.Fields) != 5 {
		t.Errorf("expected 5 settings fields, got %d", len(catPage.Fields))
	}
	if err := settingsframework.ValidateSchema(catPage.SettingsPage); err != nil {
		t.Errorf("ValidateSchema failed: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Errorf("ValidateCategorizedSettingsPage failed: %v", err)
	}
	if _, ok := plugin.GetSettingsPage("backup/restic"); !ok {
		t.Error("expected backup/restic settings page in global registry")
	}
	if regCat, ok := plugin.GetCategorizedSettingsPage("backup/restic"); !ok || regCat.Category != "backup" {
		t.Errorf("expected backup/restic categorized settings page in global registry, got %+v (ok=%v)", regCat, ok)
	}

	// Verify typed settings helper defaults
	if got := restic.DefaultRepository(); got != restic.DefaultRepoPath {
		t.Errorf("DefaultRepository() = %q, want %q", got, restic.DefaultRepoPath)
	}
	if got := restic.RetentionDaily(); got != restic.DefaultRetentionDaily {
		t.Errorf("RetentionDaily() = %d, want %d", got, restic.DefaultRetentionDaily)
	}
	if got := restic.RetentionWeekly(); got != restic.DefaultRetentionWeekly {
		t.Errorf("RetentionWeekly() = %d, want %d", got, restic.DefaultRetentionWeekly)
	}
	if got := restic.RetentionMonthly(); got != restic.DefaultRetentionMonthly {
		t.Errorf("RetentionMonthly() = %d, want %d", got, restic.DefaultRetentionMonthly)
	}
	if _, ok := restic.PasswordVaultVar(); ok {
		t.Error("expected PasswordVaultVar() to be unset by default")
	}

	// 4. Verify ContextMenuProvider
	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatal("expected backup/restic to implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), facts)
	if len(entries) != 2 {
		t.Fatalf("expected 2 context menu entries for linux host, got %d", len(entries))
	}
	if entries[0].ID != "restic-backup" || entries[0].Target != newapi.TargetNode || entries[0].PluginID != "backup/restic" {
		t.Errorf("unexpected first context menu entry: %+v", entries[0])
	}
	if entries[1].ID != "restic-check" || entries[1].Action != "check" {
		t.Errorf("unexpected second context menu entry: %+v", entries[1])
	}
	if winEntries := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "windows"}); len(winEntries) != 0 {
		t.Errorf("expected no context menu entries for incompatible windows host, got %+v", winEntries)
	}

	// Verify global registry context menu query includes backup/restic entries
	regEntries := plugin.ListContextMenuEntries(context.Background(), newapi.TargetNode, facts)
	foundRestic := false
	for _, e := range regEntries {
		if e.PluginID == "backup/restic" {
			foundRestic = true
			break
		}
	}
	if !foundRestic {
		t.Error("expected global registry ListContextMenuEntries(TargetNode) to include backup/restic entries")
	}

	// 5. Verify TaskTypeProvider & TaskCompileProvider
	ttp, ok := p.(newapi.TaskTypeProvider)
	if !ok {
		t.Fatal("expected backup/restic to implement newapi.TaskTypeProvider")
	}
	taskTypes := ttp.TaskTypes()
	if len(taskTypes) != 1 || taskTypes[0].Type != newapi.TaskTypeBackupSnapshot {
		t.Fatalf("expected 1 backup_snapshot task type, got %+v", taskTypes)
	}

	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatal("expected backup/restic to implement newapi.TaskCompileProvider")
	}
	compiledBackup, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		ID:       "req-backup-1",
		Type:     newapi.TaskTypeBackupSnapshot,
		NodeID:   "node1",
		PluginID: "backup/restic",
		Params: map[string]any{
			"operation":  "snapshot",
			"path":       "/var/lib/data",
			"repository": "/srv/backups/restic",
			"tags":       "nightly",
		},
	})
	if err != nil {
		t.Fatalf("CompileTask(backup) failed: %v", err)
	}
	if compiledBackup.Name != "Run restic backup of /var/lib/data" {
		t.Errorf("unexpected compiled backup task name: %q", compiledBackup.Name)
	}
	if !strings.Contains(compiledBackup.Steps[0].Params["cmd"].(string), "restic -r /srv/backups/restic backup --tag nightly /var/lib/data") {
		t.Errorf("unexpected compiled backup command: %v", compiledBackup.Steps[0].Params["cmd"])
	}

	compiledCheck, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		ID:       "req-check-1",
		Type:     newapi.TaskTypeBackupSnapshot,
		NodeID:   "node1",
		PluginID: "backup/restic",
		Action:   "check",
		Params: map[string]any{
			"repository": "/srv/backups/restic",
		},
	})
	if err != nil {
		t.Fatalf("CompileTask(check) failed: %v", err)
	}
	if compiledCheck.Name != "Check restic repo /srv/backups/restic" {
		t.Errorf("unexpected compiled check task name: %q", compiledCheck.Name)
	}
}
