package glances_test

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
}

func (m *mockRunner) RunCommand(ctx context.Context, cmd string) (string, error) {
	best, bestLen := "", -1
	for pattern, resp := range m.responses {
		if strings.Contains(cmd, pattern) && len(pattern) > bestLen {
			best, bestLen = resp, len(pattern)
		}
	}
	if bestLen < 0 {
		return "", nil
	}
	return best, nil
}

func TestGlancesPlugin_ProbeAndRead(t *testing.T) {
	p, ok := plugin.Get("telemetry/glances")
	if !ok {
		t.Fatalf("telemetry/glances not registered")
	}

	runner := &mockRunner{
		responses: map[string]string{
			"61208/api/4/status": "OK",
			"61208/api/4/all":    "{\"cpu\":{\"total\":15.5},\"mem\":{\"total\":17179869184,\"used\":4294967296}}",
		},
	}

	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if !det.Detected {
		t.Errorf("expected glances detected")
	}

	reader := p.(plugin.Reader)
	data, err := reader.Read(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	m := data.(map[string]any)
	cpu := m["cpu"].(map[string]any)
	if cpu["total"] != 15.5 {
		t.Errorf("expected cpu total 15.5, got %v", cpu["total"])
	}
}

func TestGlancesPlugin_FallbackTiers(t *testing.T) {
	p, ok := plugin.Get("telemetry/glances")
	if !ok {
		t.Fatalf("telemetry/glances not registered")
	}

	// Nil runner -> OFFLINE
	det, err := p.Probe(context.Background(), nil, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("unexpected error on nil runner: %v", err)
	}
	if det.Detected || det.Status != "OFFLINE" {
		t.Fatalf("expected OFFLINE detection for nil runner, got %+v", det)
	}

	// Tier 4 fallback: /proc/loadavg + /proc/meminfo
	procRunner := &mockRunner{
		responses: map[string]string{
			"test -r /proc/stat": "PROC_OK",
			"/proc/loadavg":      "0.50 0.75 1.25 1/500 12345\nMemTotal:       16000000 kB\nMemAvailable:   12000000 kB\n",
		},
	}
	det, err = p.Probe(context.Background(), procRunner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("unexpected probe error: %v", err)
	}
	if !det.Detected || !strings.Contains(det.Details, "Proc / sysfs fallback") {
		t.Fatalf("expected proc fallback detection, got %+v", det)
	}

	reader := p.(plugin.Reader)
	data, err := reader.Read(context.Background(), procRunner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("fallback read failed: %v", err)
	}
	m := data.(map[string]any)
	if m["fallback"] != true {
		t.Errorf("expected fallback=true, got %v", m["fallback"])
	}
	load := m["load"].(map[string]any)
	if load["min1"] != 0.50 || load["min5"] != 0.75 || load["min15"] != 1.25 {
		t.Errorf("unexpected load metrics: %+v", load)
	}
	mem := m["mem"].(map[string]any)
	if mem["percent"] != 25.0 {
		t.Errorf("expected memory usage 25.0%%, got %v", mem["percent"])
	}
}

func TestGlancesPlugin_V2NewAPIInterfaces(t *testing.T) {
	p, ok := plugin.Get("telemetry/glances")
	if !ok {
		t.Fatalf("telemetry/glances not registered")
	}

	facts := plugin.HostFacts{NodeID: "node-1", OSFamily: "linux"}

	// 1. DataCollectionPlugin
	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatalf("expected telemetry/glances to implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.Interval != 15*time.Second || sched.TTL != 15*time.Second || !sched.Realtime {
		t.Fatalf("expected 15s realtime schedule for telemetry/glances, got %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "cpu" {
		t.Fatalf("unexpected collection schema: %+v", schema)
	}

	// 2. ManagementPlugin
	mgmt, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatalf("expected telemetry/glances to implement newapi.ManagementPlugin")
	}
	mgmtActions := mgmt.ManagementActions()
	if len(mgmtActions) != 3 {
		t.Fatalf("expected 3 management actions, got %d", len(mgmtActions))
	}
	legacyActions := mgmt.Actions()
	if len(legacyActions) != len(mgmtActions) {
		t.Fatalf("Actions() length %d does not match ManagementActions() length %d", len(legacyActions), len(mgmtActions))
	}

	if err := mgmt.ValidateAction(facts, "restart_daemon", map[string]any{}); err != nil {
		t.Fatalf("expected valid restart_daemon options, got error: %v", err)
	}
	if err := mgmt.ValidateAction(facts, "enable_daemon", map[string]any{"service": "glances-web.service"}); err != nil {
		t.Fatalf("expected valid custom service option, got error: %v", err)
	}
	if err := mgmt.ValidateAction(facts, "restart_daemon", map[string]any{"service": "bad; rm -rf /"}); err == nil {
		t.Fatalf("expected error for command-injection service name")
	}
	if err := mgmt.ValidateAction(facts, "unknown_action", map[string]any{}); err == nil {
		t.Fatalf("expected error for unsupported action")
	}

	// 3. CategorizedSettingsProvider
	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatalf("expected telemetry/glances to implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if err := settingsframework.ValidateSchema(csp.SettingsPage()); err != nil {
		t.Fatalf("settings schema validation failed: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("categorized settings page validation failed: %v", err)
	}
	if catPage.ID != "telemetry/glances" || catPage.Category != "telemetry" || catPage.Scope != newapi.SettingsScopeDatacenter {
		t.Fatalf("unexpected categorized settings page metadata: %+v", catPage)
	}
	if _, ok := plugin.GetCategorizedSettingsPage("telemetry/glances"); !ok {
		t.Fatalf("expected telemetry/glances categorized settings page to be registered in default registry")
	}

	// 4. ContextMenuProvider
	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatalf("expected telemetry/glances to implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), facts)
	if len(entries) != 3 {
		t.Fatalf("expected 3 context menu entries for compatible linux host, got %d", len(entries))
	}
	for _, entry := range entries {
		if entry.PluginID != "telemetry/glances" || entry.Target != newapi.TargetNode || entry.Action == "" {
			t.Errorf("unexpected context menu entry: %+v", entry)
		}
	}
	if !entries[len(entries)-1].Danger || entries[len(entries)-1].ConfirmPrompt == "" {
		t.Errorf("expected stop_daemon entry to be marked Danger with a ConfirmPrompt, got %+v", entries[len(entries)-1])
	}
	if incompat := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "windows"}); len(incompat) != 0 {
		t.Fatalf("expected no context menu entries for incompatible windows host, got %+v", incompat)
	}

	// 5. TaskCompileProvider
	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatalf("expected telemetry/glances to implement newapi.TaskCompileProvider")
	}
	compiledTask, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "telemetry/glances",
		Action:   "restart_daemon",
		NodeID:   "node-1",
		Params:   map[string]any{},
	})
	if err != nil {
		t.Fatalf("CompileTask failed: %v", err)
	}
	if len(compiledTask.Steps) != 1 || compiledTask.Steps[0].Module != "ansible.builtin.systemd_service" {
		t.Fatalf("unexpected CompileTask output: %+v", compiledTask)
	}
	if compiledTask.Steps[0].Params["name"] != "glances.service" || compiledTask.Steps[0].Params["state"] != "restarted" {
		t.Errorf("unexpected CompileTask params: %+v", compiledTask.Steps[0].Params)
	}

	// Fallback action from Params["action"]
	compiledEnable, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "telemetry/glances",
		NodeID:   "node-1",
		Params: map[string]any{
			"action":  "enable_daemon",
			"service": "glances.service",
		},
	})
	if err != nil {
		t.Fatalf("CompileTask with params[action] failed: %v", err)
	}
	if len(compiledEnable.Steps) != 1 || compiledEnable.Steps[0].Params["enabled"] != true || compiledEnable.Steps[0].Params["state"] != "started" {
		t.Fatalf("unexpected CompileTask enable_daemon output: %+v", compiledEnable)
	}
}
