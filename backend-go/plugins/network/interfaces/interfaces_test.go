package interfaces_test

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

func TestInterfacesPlugin_ProbeReadAndCompile(t *testing.T) {
	p, ok := plugin.Get("network/interfaces")
	if !ok {
		t.Fatalf("network/interfaces not registered")
	}

	runner := &mockRunner{
		responses: map[string]string{
			"command -v ip": "ip",
			"ip -j link":    "[{\"ifname\":\"lo\"},{\"ifname\":\"eth0\"}]",
			"===LINK===":    "[{\"ifname\":\"eth0\",\"addr_info\":[{\"family\":\"inet\",\"local\":\"192.168.1.50\",\"prefixlen\":24}]}]\n===LINK===\n[{\"ifname\":\"eth0\",\"address\":\"00:11:22:33:44:55\",\"operstate\":\"UP\",\"flags\":[\"UP\"],\"mtu\":1500,\"stats64\":{\"rx\":{\"bytes\":1048576},\"tx\":{\"bytes\":524288}}}]",
		},
	}

	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if !det.Detected || !strings.Contains(det.Status, "2 interface") {
		t.Errorf("unexpected probe result: %+v", det)
	}

	reader := p.(plugin.Reader)
	data, err := reader.Read(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	m := data.(map[string]any)
	ifaces := m["interfaces"].([]map[string]any)
	if len(ifaces) != 1 {
		t.Fatalf("expected 1 interface, got %d", len(ifaces))
	}
	if ifaces[0]["name"] != "eth0" || ifaces[0]["ip"] != "192.168.1.50/24" {
		t.Errorf("unexpected iface data: %+v", ifaces[0])
	}

	actor := p.(plugin.Actor)
	task, err := actor.Compile(plugin.HostFacts{NodeID: "node1"}, "connect_interface", map[string]any{
		"name": "wg0",
		"type": "wireguard",
		"cidr": "10.0.0.1/24",
	})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if len(task.Steps) != 1 || task.Steps[0].Module != "ansible.builtin.template" {
		t.Errorf("unexpected steps: %+v", task.Steps)
	}
}

func TestInterfacesPlugin_ToggleAndErrorPaths(t *testing.T) {
	p, ok := plugin.Get("network/interfaces")
	if !ok {
		t.Fatalf("network/interfaces not registered")
	}

	if det, err := p.Probe(context.Background(), nil, plugin.HostFacts{OSFamily: "linux"}); err != nil || det.Detected || det.Status != "OFFLINE" {
		t.Errorf("expected OFFLINE detection for nil runner, got %+v (err=%v)", det, err)
	}

	actor := p.(plugin.Actor)
	facts := plugin.HostFacts{NodeID: "node1", OSFamily: "linux"}

	toggleTask, err := actor.Compile(facts, "toggle_interface", map[string]any{
		"name":  "eth0",
		"state": "down",
	})
	if err != nil {
		t.Fatalf("toggle_interface compile failed: %v", err)
	}
	if len(toggleTask.Steps) != 1 || toggleTask.Steps[0].Module != "ansible.builtin.command" {
		t.Fatalf("unexpected toggle steps: %+v", toggleTask.Steps)
	}
	if toggleTask.Steps[0].Params["cmd"] != "ip link set eth0 down" {
		t.Errorf("unexpected toggle cmd: %v", toggleTask.Steps[0].Params["cmd"])
	}

	if _, err := actor.Compile(facts, "toggle_interface", map[string]any{}); err == nil {
		t.Errorf("expected error when toggle_interface is missing interface name")
	}
	if _, err := actor.Compile(facts, "unknown_action", map[string]any{}); err == nil {
		t.Errorf("expected error for unknown action")
	}
}

func TestInterfacesPlugin_NewAPICompliance(t *testing.T) {
	p, ok := plugin.Get("network/interfaces")
	if !ok {
		t.Fatalf("network/interfaces not registered")
	}

	// 1. DataCollectionPlugin
	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatalf("expected network/interfaces to implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.Interval <= 0 || sched.TTL <= 0 {
		t.Errorf("unexpected CollectionSchedule: %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "interfaces" {
		t.Errorf("unexpected CollectionSchema: %+v", schema)
	}

	// 2. ManagementPlugin
	mgmt, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatalf("expected network/interfaces to implement newapi.ManagementPlugin")
	}
	mgmtActions := mgmt.ManagementActions()
	legacyActions := mgmt.Actions()
	if len(mgmtActions) != 2 || len(legacyActions) != len(mgmtActions) {
		t.Fatalf("expected 2 management actions matching legacy actions, got %d vs %d", len(mgmtActions), len(legacyActions))
	}
	for i, ma := range mgmtActions {
		if ma.Name != legacyActions[i].Name {
			t.Errorf("action %d mismatch: %q vs %q", i, ma.Name, legacyActions[i].Name)
		}
		if !ma.Idempotent || len(ma.InvalidatesKinds) == 0 || ma.InvalidatesKinds[0] != plugin.KindNetwork {
			t.Errorf("unexpected management metadata for %q: %+v", ma.Name, ma)
		}
	}

	facts := plugin.HostFacts{NodeID: "node1", OSFamily: "linux"}
	if err := mgmt.ValidateAction(facts, "connect_interface", map[string]any{"name": "eth0", "type": "wireguard"}); err != nil {
		t.Errorf("expected valid connect_interface options, got error: %v", err)
	}
	if err := mgmt.ValidateAction(facts, "interface", map[string]any{"interface": "wg0"}); err != nil {
		t.Errorf("expected alias 'interface' with 'interface' param to validate, got error: %v", err)
	}
	if err := mgmt.ValidateAction(facts, "connect_interface", map[string]any{}); err == nil {
		t.Errorf("expected error for missing required option 'name'")
	}
	if err := mgmt.ValidateAction(facts, "toggle_interface", map[string]any{"name": "eth0", "state": "down"}); err != nil {
		t.Errorf("expected valid toggle_interface options, got error: %v", err)
	}
	if err := mgmt.ValidateAction(facts, "toggle_interface", map[string]any{"name": "eth0", "state": "invalid"}); err == nil {
		t.Errorf("expected error for invalid enum option 'state'")
	}
	if err := mgmt.ValidateAction(facts, "unknown_action", map[string]any{}); err == nil {
		t.Errorf("expected error for unknown action")
	}

	// 3. CategorizedSettingsProvider
	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatalf("expected network/interfaces to implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Errorf("ValidateCategorizedSettingsPage failed: %v", err)
	}
	if err := settingsframework.ValidateSchema(csp.SettingsPage()); err != nil {
		t.Errorf("settingsframework.ValidateSchema failed: %v", err)
	}
	if catPage.ID != "network/interfaces" || catPage.Category != "network" || catPage.Scope != newapi.SettingsScopeDatacenter {
		t.Errorf("unexpected CategorizedSettingsPage metadata: %+v", catPage)
	}

	// 4. ContextMenuProvider
	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatalf("expected network/interfaces to implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), facts)
	if len(entries) != 2 {
		t.Fatalf("expected 2 context menu entries for Linux host, got %d: %+v", len(entries), entries)
	}
	for _, entry := range entries {
		if entry.PluginID != "network/interfaces" || entry.Target != newapi.TargetNetwork || entry.Action == "" {
			t.Errorf("unexpected context menu entry: %+v", entry)
		}
	}
	if winEntries := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "windows"}); len(winEntries) != 0 {
		t.Errorf("expected no context menu entries for windows host, got %+v", winEntries)
	}
	if routerEntries := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "router_switch"}); len(routerEntries) != 0 {
		t.Errorf("expected no context menu entries for router_switch host, got %+v", routerEntries)
	}

	// 5. TaskCompileProvider
	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatalf("expected network/interfaces to implement newapi.TaskCompileProvider")
	}
	spec1, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "network/interfaces",
		NodeID:   "node1",
		Action:   "connect_interface",
		Params:   map[string]any{"name": "wg0", "type": "wireguard", "cidr": "10.0.0.1/24", "gateway": "10.0.0.254", "dns": "1.1.1.1"},
	})
	if err != nil {
		t.Fatalf("CompileTask failed: %v", err)
	}
	if len(spec1.Steps) != 1 || spec1.Steps[0].Params["interface"] != "wg0" || spec1.Steps[0].Params["dns"] != "1.1.1.1" {
		t.Errorf("unexpected CompileTask output: %+v", spec1)
	}

	spec2, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "network/interfaces",
		NodeID:   "node1",
		Params:   map[string]any{"action": "toggle_interface", "name": "eth0", "state": "up"},
	})
	if err != nil {
		t.Fatalf("CompileTask with action in Params failed: %v", err)
	}
	if len(spec2.Steps) != 1 || spec2.Steps[0].Params["cmd"] != "ip link set eth0 up" {
		t.Errorf("unexpected CompileTask output: %+v", spec2)
	}

	if _, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{NodeID: "node1"}); err == nil {
		t.Errorf("expected error when CompileTask is called without an action")
	}
}
