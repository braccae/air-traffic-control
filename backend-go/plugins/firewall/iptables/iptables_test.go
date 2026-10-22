package iptables_test

import (
	"context"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	_ "air-traffic-control/backend/plugins/all"
	"air-traffic-control/backend/plugins/firewall/iptables"
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

const iptablesSaveFixture = `-P INPUT DROP
-P FORWARD DROP
-P OUTPUT ACCEPT
-A INPUT -i tailscale0 -s 100.64.0.0/10 -p tcp -m tcp --dport 22 -m comment --comment "Allow Tailscale SSH" -j ACCEPT
-A INPUT -p tcp -m tcp --dport 443 -j ACCEPT
-A INPUT -p udp -m udp --dport 51820 -m comment --comment "WireGuard VPN" -j ACCEPT
-A INPUT -p tcp -m tcp --dport 23 -j DROP`

func TestIptablesPlugin_AbsentToolReturnsDetectedFalse(t *testing.T) {
	p, ok := plugin.Get("firewall/iptables")
	if !ok {
		t.Fatalf("firewall/iptables not registered")
	}

	emptyRunner := &mockRunner{responses: map[string]string{}}
	det, err := p.Probe(context.Background(), emptyRunner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if det.Detected {
		t.Fatalf("expected Detected=false when iptables tool is absent, got %+v", det)
	}
}

func TestIptablesPlugin_FirewalldActiveReportsInactive(t *testing.T) {
	p, ok := plugin.Get("firewall/iptables")
	if !ok {
		t.Fatalf("firewall/iptables not registered")
	}

	runner := &mockRunner{
		responses: map[string]string{
			"command -v iptables": "/usr/sbin/iptables\n",
			"===RULES===":         "running\n===RULES===\n" + iptablesSaveFixture + "\n===MODS===\nip_tables 32768 1\n",
		},
	}

	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if det.Detected || det.Status != "inactive" {
		t.Fatalf("expected Detected=false and Status=inactive when firewalld is running, got %+v", det)
	}
}

func TestIptablesPlugin_ProbeReadAndCompile(t *testing.T) {
	p, ok := plugin.Get("firewall/iptables")
	if !ok {
		t.Fatalf("firewall/iptables not registered")
	}

	meta := p.Meta()
	if meta.ID != "firewall/iptables" || meta.Kind != plugin.KindFirewall || meta.Name != "iptables" || meta.Order != 11 {
		t.Fatalf("unexpected meta: %+v", meta)
	}

	runner := &mockRunner{
		responses: map[string]string{
			"command -v iptables": "/usr/sbin/iptables\n",
			"===RULES===":         "NO_FIREWALLD\n===RULES===\n" + iptablesSaveFixture + "\n===MODS===\nip_tables 32768 1\n",
			"===IP6===":           iptablesSaveFixture + "\n===IP6===\n-P INPUT ACCEPT\n",
		},
	}

	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if !det.Detected || det.Status != "active" {
		t.Fatalf("expected Detected=true, Status=active; got %+v", det)
	}

	reader := p.(plugin.Reader)
	data, err := reader.Read(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	m := data.(map[string]any)
	if m["status"] != "active" || m["backend"] != "iptables" {
		t.Fatalf("unexpected read metadata: %+v", m)
	}

	rules, ok := m["rules"].([]iptables.ParsedFirewallRule)
	if !ok {
		t.Fatalf("rules is not []iptables.ParsedFirewallRule: %T", m["rules"])
	}
	if len(rules) != 4 {
		t.Fatalf("expected 4 parsed rules, got %d: %+v", len(rules), rules)
	}
	if rules[0].Port != "22" || rules[0].Proto != "tcp" || rules[0].Interface != "tailscale0" || rules[0].Source != "100.64.0.0/10" || rules[0].Comment != "Allow Tailscale SSH" {
		t.Errorf("unexpected first rule: %+v", rules[0])
	}
	if rules[3].Policy != "DROP" || rules[3].Port != "23" {
		t.Errorf("unexpected fourth rule: %+v", rules[3])
	}

	actor := p.(plugin.Actor)
	task, err := actor.Compile(plugin.HostFacts{NodeID: "node1"}, "allow_port", map[string]any{
		"port":    "8443",
		"proto":   "tcp",
		"chain":   "INPUT",
		"source":  "10.0.0.0/8",
		"comment": "HTTPS alt",
	})
	if err != nil {
		t.Fatalf("compile allow_port failed: %v", err)
	}
	if len(task.Steps) != 1 || task.Steps[0].Module != "ansible.builtin.iptables" {
		t.Fatalf("unexpected task steps: %+v", task.Steps)
	}
	params := task.Steps[0].Params
	if params["destination_port"] != "8443" || params["protocol"] != "tcp" || params["jump"] != "ACCEPT" || params["state"] != "present" {
		t.Errorf("unexpected allow_port params: %+v", params)
	}

	delTask, err := actor.Compile(plugin.HostFacts{NodeID: "node1"}, "delete_port", map[string]any{
		"port":  "8443",
		"proto": "tcp",
	})
	if err != nil {
		t.Fatalf("compile delete_port failed: %v", err)
	}
	if delTask.Steps[0].Params["state"] != "absent" {
		t.Errorf("expected delete_port state=absent, got %+v", delTask.Steps[0].Params)
	}
}

func TestIptablesPlugin_V2NewAPIInterfaces(t *testing.T) {
	p, ok := plugin.Get("firewall/iptables")
	if !ok {
		t.Fatalf("firewall/iptables not registered")
	}

	// 1. DataCollectionPlugin
	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatalf("expected firewall/iptables to implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.Interval <= 0 || sched.TTL <= 0 {
		t.Fatalf("expected positive schedule Interval and TTL, got %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "rules" {
		t.Fatalf("unexpected collection schema: %+v", schema)
	}

	// 2. ManagementPlugin
	mgmt, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatalf("expected firewall/iptables to implement newapi.ManagementPlugin")
	}
	mgmtActions := mgmt.ManagementActions()
	if len(mgmtActions) != 3 {
		t.Fatalf("expected 3 management actions, got %d", len(mgmtActions))
	}
	legacyActions := mgmt.Actions()
	if len(legacyActions) != len(mgmtActions) {
		t.Fatalf("Actions() length %d does not match ManagementActions() length %d", len(legacyActions), len(mgmtActions))
	}

	facts := plugin.HostFacts{NodeID: "node1", OSFamily: "linux"}
	if err := mgmt.ValidateAction(facts, "allow_port", map[string]any{"port": "443"}); err != nil {
		t.Fatalf("expected valid allow_port options, got error: %v", err)
	}
	if err := mgmt.ValidateAction(facts, "allow_port", map[string]any{}); err == nil {
		t.Fatalf("expected error when required port option is missing")
	}
	if err := mgmt.ValidateAction(facts, "deny_port", map[string]any{"port": "23", "jump": "INVALID"}); err == nil {
		t.Fatalf("expected error for invalid enum value on deny_port")
	}
	if err := mgmt.ValidateAction(facts, "unknown_action", map[string]any{}); err == nil {
		t.Fatalf("expected error for unknown action")
	}

	// 3. CategorizedSettingsProvider
	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatalf("expected firewall/iptables to implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if err := settingsframework.ValidateSchema(csp.SettingsPage()); err != nil {
		t.Fatalf("settings schema validation failed: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("categorized settings page validation failed: %v", err)
	}
	if catPage.ID != "firewall/iptables" || catPage.Category != "firewall" || catPage.Scope != newapi.SettingsScopeDatacenter {
		t.Fatalf("unexpected categorized settings page metadata: %+v", catPage)
	}

	// 4. ContextMenuProvider
	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatalf("expected firewall/iptables to implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), facts)
	if len(entries) != 3 {
		t.Fatalf("expected 3 context menu entries for compatible linux host, got %d", len(entries))
	}
	for _, entry := range entries {
		if entry.PluginID != "firewall/iptables" || entry.Target != newapi.TargetFirewall || entry.Action == "" {
			t.Errorf("unexpected context menu entry: %+v", entry)
		}
	}
	if incompat := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "windows"}); len(incompat) != 0 {
		t.Fatalf("expected no context menu entries for incompatible windows host, got %+v", incompat)
	}

	// 5. TaskCompileProvider
	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatalf("expected firewall/iptables to implement newapi.TaskCompileProvider")
	}
	compiledTask, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "firewall/iptables",
		Action:   "deny_port",
		NodeID:   "node1",
		Params: map[string]any{
			"port":  "23",
			"proto": "tcp",
			"jump":  "REJECT",
		},
	})
	if err != nil {
		t.Fatalf("CompileTask failed: %v", err)
	}
	if len(compiledTask.Steps) != 1 || compiledTask.Steps[0].Params["jump"] != "REJECT" || compiledTask.Steps[0].Params["destination_port"] != "23" {
		t.Fatalf("unexpected CompileTask output: %+v", compiledTask)
	}
}
