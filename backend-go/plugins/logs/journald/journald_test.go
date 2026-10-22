package journald_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	_ "air-traffic-control/backend/plugins/all"
)

type mockRunner struct {
	responses map[string]string
	lastCmd   string
	err       error
}

func (m *mockRunner) RunCommand(ctx context.Context, cmd string) (string, error) {
	m.lastCmd = cmd
	if m.err != nil {
		return "", m.err
	}
	for pattern, resp := range m.responses {
		if strings.Contains(cmd, pattern) {
			return resp, nil
		}
	}
	return "", nil
}

func TestJournaldPlugin_RegisteredAndBaseContract(t *testing.T) {
	p, ok := plugin.Get("logs/journald")
	if !ok {
		t.Fatal("logs/journald plugin not registered")
	}

	meta := p.Meta()
	if meta.ID != "logs/journald" || meta.Kind != plugin.KindLogs || meta.Name != "journald" {
		t.Errorf("unexpected meta: %+v", meta)
	}
	if !p.Compatible(plugin.HostFacts{OSFamily: "linux"}) {
		t.Error("expected logs/journald to be compatible with linux")
	}
	if p.Compatible(plugin.HostFacts{OSFamily: "windows"}) {
		t.Error("expected logs/journald to be incompatible with windows")
	}
	if p.Compatible(plugin.HostFacts{OSFamily: "router_switch"}) {
		t.Error("expected logs/journald to be incompatible with router_switch")
	}
	reqs := p.Requirements()
	if len(reqs.HostPackages["dnf"]) == 0 || len(reqs.HostPackages["apt"]) == 0 {
		t.Errorf("expected host packages for dnf and apt, got %+v", reqs)
	}
}

func TestJournaldPlugin_ProbeAndRead(t *testing.T) {
	p, ok := plugin.Get("logs/journald")
	if !ok {
		t.Fatalf("logs/journald not registered")
	}

	// 1. Nil runner -> OFFLINE
	det, err := p.Probe(context.Background(), nil, plugin.HostFacts{OSFamily: "linux"})
	if err != nil || det.Detected || det.Status != "OFFLINE" {
		t.Errorf("expected OFFLINE for nil runner, got %+v (err=%v)", det, err)
	}

	// 2. journalctl absent -> UNAVAILABLE
	emptyRunner := &mockRunner{responses: map[string]string{}}
	det, err = p.Probe(context.Background(), emptyRunner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil || det.Detected || det.Status != "UNAVAILABLE" {
		t.Errorf("expected UNAVAILABLE when journalctl missing, got %+v (err=%v)", det, err)
	}

	// 3. journalctl present -> ONLINE
	runner := &mockRunner{
		responses: map[string]string{
			"command -v journalctl": "/usr/bin/journalctl",
			"journalctl":            "Oct 19 12:00:00 host systemd[1]: Started Caddy.\nOct 19 12:00:01 host caddy[123]: [INFO] Server listening on :443",
		},
	}

	det, err = p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if !det.Detected || det.Status != "ONLINE" {
		t.Errorf("expected journalctl detected ONLINE, got %+v", det)
	}

	// 4. Read with unit, priority, lines, and user scope overrides
	reader := p.(plugin.Reader)
	if _, err := reader.Read(context.Background(), nil, plugin.HostFacts{OSFamily: "linux"}); err == nil {
		t.Error("expected error when reading with nil runner")
	}

	data, err := reader.Read(context.Background(), runner, plugin.HostFacts{
		OSFamily: "linux",
		Overrides: map[string]string{
			"unit":     "caddy",
			"priority": "info",
			"lines":    "50",
			"scope":    "user",
		},
	})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !strings.Contains(runner.lastCmd, "journalctl --user -n 50 --no-pager -u 'caddy' -p info") {
		t.Errorf("unexpected journalctl command: %q", runner.lastCmd)
	}
	m := data.(map[string]any)
	lines := m["lines"].([]string)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if m["unit"] != "caddy" || m["count"] != 2 {
		t.Errorf("unexpected read map fields: %+v", m)
	}

	// 5. Reject unsafe filter overrides
	if _, err := reader.Read(context.Background(), runner, plugin.HostFacts{
		OSFamily:  "linux",
		Overrides: map[string]string{"unit": "bad;rm -rf /"},
	}); err == nil {
		t.Error("expected invalid unit filter to return an error")
	}
	if _, err := reader.Read(context.Background(), runner, plugin.HostFacts{
		OSFamily:  "linux",
		Overrides: map[string]string{"priority": "notapriority"},
	}); err == nil {
		t.Error("expected invalid priority filter to return an error")
	}
	if _, err := reader.Read(context.Background(), runner, plugin.HostFacts{
		OSFamily:  "linux",
		Overrides: map[string]string{"lines": "100;id"},
	}); err == nil {
		t.Error("expected invalid line count filter to return an error")
	}
}

func TestJournaldPlugin_CompileAndManagementActions(t *testing.T) {
	p, ok := plugin.Get("logs/journald")
	if !ok {
		t.Fatalf("logs/journald not registered")
	}

	actor, ok := p.(plugin.Actor)
	if !ok {
		t.Fatalf("logs/journald does not implement plugin.Actor")
	}

	facts := plugin.HostFacts{NodeID: "node1", OSFamily: "linux"}

	// 1. Rotate
	rotateTask, err := actor.Compile(facts, "rotate", nil)
	if err != nil {
		t.Fatalf("Compile rotate failed: %v", err)
	}
	if len(rotateTask.Steps) != 1 || rotateTask.Steps[0].Params["cmd"] != "journalctl --rotate" {
		t.Errorf("unexpected rotate steps: %+v", rotateTask.Steps)
	}

	// 2. Flush
	flushTask, err := actor.Compile(facts, "flush", nil)
	if err != nil {
		t.Fatalf("Compile flush failed: %v", err)
	}
	if len(flushTask.Steps) != 1 || flushTask.Steps[0].Params["cmd"] != "journalctl --flush" {
		t.Errorf("unexpected flush steps: %+v", flushTask.Steps)
	}

	// 3. Vacuum by size (explicit and default)
	vacuumSizeTask, err := actor.Compile(facts, "vacuum_size", map[string]any{"size": "1G"})
	if err != nil {
		t.Fatalf("Compile vacuum_size failed: %v", err)
	}
	if len(vacuumSizeTask.Steps) != 1 || vacuumSizeTask.Steps[0].Params["cmd"] != "journalctl --vacuum-size=1G" {
		t.Errorf("unexpected vacuum_size steps: %+v", vacuumSizeTask.Steps)
	}

	defaultVacuumSizeTask, err := actor.Compile(facts, "vacuum-size", map[string]any{})
	if err != nil {
		t.Fatalf("Compile default vacuum-size failed: %v", err)
	}
	if len(defaultVacuumSizeTask.Steps) != 1 || defaultVacuumSizeTask.Steps[0].Params["cmd"] != "journalctl --vacuum-size=500M" {
		t.Errorf("unexpected default vacuum_size steps: %+v", defaultVacuumSizeTask.Steps)
	}

	// 4. Vacuum by time (explicit and default)
	vacuumTimeTask, err := actor.Compile(facts, "vacuum_time", map[string]any{"time": "14d"})
	if err != nil {
		t.Fatalf("Compile vacuum_time failed: %v", err)
	}
	if len(vacuumTimeTask.Steps) != 1 || vacuumTimeTask.Steps[0].Params["cmd"] != "journalctl --vacuum-time=14d" {
		t.Errorf("unexpected vacuum_time steps: %+v", vacuumTimeTask.Steps)
	}

	defaultVacuumTimeTask, err := actor.Compile(facts, "vacuum-time", map[string]any{})
	if err != nil {
		t.Fatalf("Compile default vacuum-time failed: %v", err)
	}
	if len(defaultVacuumTimeTask.Steps) != 1 || defaultVacuumTimeTask.Steps[0].Params["cmd"] != "journalctl --vacuum-time=30d" {
		t.Errorf("unexpected default vacuum_time steps: %+v", defaultVacuumTimeTask.Steps)
	}

	// 5. Invalid actions and options
	if _, err := actor.Compile(facts, "vacuum_size", map[string]any{"size": "500M; rm -rf /"}); err == nil {
		t.Error("expected invalid vacuum size to fail Compile")
	}
	if _, err := actor.Compile(facts, "vacuum_time", map[string]any{"time": "invalid_duration"}); err == nil {
		t.Error("expected invalid vacuum time to fail Compile")
	}
	if _, err := actor.Compile(facts, "unknown_action", nil); err == nil {
		t.Error("expected unknown action to fail Compile")
	}
}

func TestJournaldPlugin_NewAPICompliance(t *testing.T) {
	p, ok := plugin.Get("logs/journald")
	if !ok {
		t.Fatalf("logs/journald not registered in DefaultRegistry")
	}

	// 1. DataCollectionPlugin
	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatalf("logs/journald does not implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.Interval != 60*time.Second || sched.TTL != 60*time.Second || !sched.Realtime {
		t.Errorf("unexpected collection schedule for logs/journald: %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "lines" {
		t.Errorf("unexpected collection schema: %+v", schema)
	}

	// 2. ManagementPlugin & action parity
	mgmt, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatalf("logs/journald does not implement newapi.ManagementPlugin")
	}
	mgmtActions := mgmt.ManagementActions()
	legacyActions := p.(plugin.Actor).Actions()
	if len(mgmtActions) != 4 || len(legacyActions) != len(mgmtActions) {
		t.Fatalf("expected 4 management actions matching legacy Actions(), got mgmt=%d legacy=%d", len(mgmtActions), len(legacyActions))
	}
	for i := range mgmtActions {
		if mgmtActions[i].Name != legacyActions[i].Name {
			t.Errorf("action mismatch at %d: mgmt=%s legacy=%s", i, mgmtActions[i].Name, legacyActions[i].Name)
		}
		if !mgmtActions[i].Idempotent || len(mgmtActions[i].InvalidatesKinds) == 0 || mgmtActions[i].InvalidatesKinds[0] != plugin.KindLogs {
			t.Errorf("unexpected management metadata for %s: %+v", mgmtActions[i].Name, mgmtActions[i])
		}
	}

	facts := plugin.HostFacts{NodeID: "node1", OSFamily: "linux"}
	if err := mgmt.ValidateAction(facts, "rotate", nil); err != nil {
		t.Errorf("expected rotate to pass ValidateAction, got %v", err)
	}
	if err := mgmt.ValidateAction(facts, "vacuum_size", map[string]any{"size": "250M"}); err != nil {
		t.Errorf("expected valid vacuum_size to pass ValidateAction, got %v", err)
	}
	if err := mgmt.ValidateAction(facts, "vacuum_size", map[string]any{"size": "bad value"}); err == nil {
		t.Errorf("expected invalid vacuum_size to fail ValidateAction")
	}
	if err := mgmt.ValidateAction(facts, "vacuum_time", map[string]any{"time": "7d"}); err != nil {
		t.Errorf("expected valid vacuum_time to pass ValidateAction, got %v", err)
	}
	if err := mgmt.ValidateAction(facts, "vacuum_time", map[string]any{"time": "bad_time"}); err == nil {
		t.Errorf("expected invalid vacuum_time to fail ValidateAction")
	}
	if err := mgmt.ValidateAction(facts, "unknown", nil); err == nil {
		t.Errorf("expected unknown action to fail ValidateAction")
	}

	// 3. CategorizedSettingsProvider & settingsframework validation
	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatalf("logs/journald does not implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if err := settingsframework.ValidateSchema(catPage.SettingsPage); err != nil {
		t.Fatalf("ValidateSchema failed for logs/journald settings page: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("ValidateCategorizedSettingsPage failed for logs/journald: %v", err)
	}
	if catPage.ID != "logs/journald" || catPage.Scope != newapi.SettingsScopeDatacenter || catPage.Category != "logs" {
		t.Errorf("unexpected categorized settings page metadata: %+v", catPage)
	}
	if _, ok := plugin.DefaultRegistry().GetSettingsPage("logs/journald"); !ok {
		t.Errorf("expected logs/journald settings page to be indexed in DefaultRegistry")
	}

	// 4. ContextMenuProvider
	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatalf("logs/journald does not implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), facts)
	if len(entries) != 4 {
		t.Fatalf("expected 4 context menu entries for compatible linux host, got %d (%+v)", len(entries), entries)
	}
	if entries[0].PluginID != "logs/journald" || entries[0].Target != newapi.TargetNode || entries[0].Action != "rotate" {
		t.Errorf("unexpected context menu entry metadata: %+v", entries[0])
	}
	if winEntries := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "windows"}); len(winEntries) != 0 {
		t.Errorf("expected nil/empty context menu entries for incompatible windows host, got %+v", winEntries)
	}
	if routerEntries := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "router_switch"}); len(routerEntries) != 0 {
		t.Errorf("expected nil/empty context menu entries for incompatible router_switch host, got %+v", routerEntries)
	}

	// 5. TaskCompileProvider
	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatalf("logs/journald does not implement newapi.TaskCompileProvider")
	}
	compiled, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "logs/journald",
		Action:   "vacuum_size",
		NodeID:   "node1",
		Params: map[string]any{
			"size": "2G",
		},
	})
	if err != nil {
		t.Fatalf("CompileTask failed: %v", err)
	}
	if len(compiled.Steps) != 1 || compiled.Steps[0].Params["cmd"] != "journalctl --vacuum-size=2G" {
		t.Errorf("unexpected CompileTask steps: %+v", compiled.Steps)
	}

	compiledViaParam, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "logs/journald",
		NodeID:   "node1",
		Params: map[string]any{
			"action": "rotate",
		},
	})
	if err != nil {
		t.Fatalf("CompileTask with action in Params failed: %v", err)
	}
	if len(compiledViaParam.Steps) != 1 || compiledViaParam.Steps[0].Params["cmd"] != "journalctl --rotate" {
		t.Errorf("unexpected CompileTask steps: %+v", compiledViaParam.Steps)
	}

	if _, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "logs/journald",
		NodeID:   "node1",
	}); err == nil {
		t.Errorf("expected CompileTask with empty action to return an error")
	}
}
