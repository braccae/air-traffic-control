package nftables_test

import (
	"context"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	_ "air-traffic-control/backend/plugins/all"
	"air-traffic-control/backend/plugins/firewall/nftables"
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

const nftRulesetFixture = `table inet filter {
	chain input {
		type filter hook input priority filter; policy drop;
		iifname "tailscale0" ip saddr 100.64.0.0/10 tcp dport 22 accept comment "Tailscale SSH" # handle 4
		tcp dport 443 accept comment "HTTPS" # handle 5
		tcp dport 23 drop comment "Block Telnet" # handle 6
	}
}`

func TestNftablesPlugin_AbsentToolReturnsDetectedFalse(t *testing.T) {
	p, ok := plugin.Get("firewall/nftables")
	if !ok {
		t.Fatalf("firewall/nftables not registered")
	}

	emptyRunner := &mockRunner{responses: map[string]string{}}
	det, err := p.Probe(context.Background(), emptyRunner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if det.Detected {
		t.Fatalf("expected Detected=false when nft tool is absent, got %+v", det)
	}
}

func TestNftablesPlugin_ProbeReadAndCompile(t *testing.T) {
	p, ok := plugin.Get("firewall/nftables")
	if !ok {
		t.Fatalf("firewall/nftables not registered")
	}

	meta := p.Meta()
	if meta.ID != "firewall/nftables" || meta.Kind != plugin.KindFirewall || meta.Name != "nftables" || meta.Order != 12 {
		t.Fatalf("unexpected meta: %+v", meta)
	}

	runner := &mockRunner{
		responses: map[string]string{
			"command -v nft":   "/usr/sbin/nft\n",
			"nft list tables":  "table inet filter\n",
			"nft list ruleset": nftRulesetFixture,
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
	if m["status"] != "active" || m["backend"] != "nftables" {
		t.Fatalf("unexpected read metadata: %+v", m)
	}

	rules, ok := m["rules"].([]nftables.ParsedFirewallRule)
	if !ok {
		t.Fatalf("rules is not []nftables.ParsedFirewallRule: %T", m["rules"])
	}
	if len(rules) != 3 {
		t.Fatalf("expected 3 parsed rules, got %d: %+v", len(rules), rules)
	}
	if rules[0].ID != "nft-handle-4" || rules[0].Port != "22" || rules[0].Proto != "tcp" || rules[0].Interface != "tailscale0" || rules[0].Source != "100.64.0.0/10" || rules[0].Comment != "Tailscale SSH" {
		t.Errorf("unexpected first rule: %+v", rules[0])
	}
	if rules[2].Policy != "DROP" || rules[2].Port != "23" {
		t.Errorf("unexpected third rule: %+v", rules[2])
	}

	actor := p.(plugin.Actor)
	task, err := actor.Compile(plugin.HostFacts{NodeID: "node1"}, "allow_port", map[string]any{
		"port":  "443",
		"proto": "tcp",
	})
	if err != nil {
		t.Fatalf("compile allow_port failed: %v", err)
	}
	if len(task.Steps) != 1 || task.Steps[0].Module != "ansible.builtin.command" {
		t.Fatalf("unexpected task steps: %+v", task.Steps)
	}
	cmd, _ := task.Steps[0].Params["cmd"].(string)
	if !strings.Contains(cmd, "nft add rule inet filter input tcp dport 443 accept") {
		t.Errorf("unexpected command param: %q", cmd)
	}
}

func TestNftablesPlugin_V2InterfacesAndCapabilities(t *testing.T) {
	p, ok := plugin.Get("firewall/nftables")
	if !ok {
		t.Fatalf("firewall/nftables not registered")
	}

	// 1. DataCollectionPlugin
	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatalf("expected firewall/nftables to implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.Interval <= 0 || sched.TTL <= 0 || sched.Realtime {
		t.Errorf("unexpected CollectionSchedule: %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "rules" {
		t.Errorf("unexpected CollectionSchema: %+v", schema)
	}

	// 2. ManagementPlugin
	mgmt, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatalf("expected firewall/nftables to implement newapi.ManagementPlugin")
	}
	actions := mgmt.ManagementActions()
	if len(actions) != 2 {
		t.Fatalf("expected 2 ManagementActions, got %d", len(actions))
	}
	if actions[0].Name != "allow_port" || actions[0].Category != newapi.ActionCategorySecurity {
		t.Errorf("unexpected allow_port ManagementActionSpec: %+v", actions[0])
	}
	if actions[1].Name != "deny_port" || !actions[1].Dangerous || actions[1].ConfirmPrompt == "" {
		t.Errorf("unexpected deny_port ManagementActionSpec: %+v", actions[1])
	}

	facts := plugin.HostFacts{NodeID: "node1", OSFamily: "linux"}
	if err := mgmt.ValidateAction(facts, "allow_port", map[string]any{"port": "8080", "proto": "tcp"}); err != nil {
		t.Errorf("expected valid allow_port to pass ValidateAction, got: %v", err)
	}
	if err := mgmt.ValidateAction(facts, "allow_port", map[string]any{}); err == nil {
		t.Errorf("expected missing port to fail ValidateAction")
	}
	if err := mgmt.ValidateAction(facts, "deny_port", map[string]any{"port": "23", "policy": "invalid_policy"}); err == nil {
		t.Errorf("expected invalid enum policy to fail ValidateAction")
	}
	if err := mgmt.ValidateAction(facts, "unknown_action", map[string]any{"port": "80"}); err == nil {
		t.Errorf("expected unknown action to fail ValidateAction")
	}

	// 3. CategorizedSettingsProvider & SettingsPageProvider
	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatalf("expected firewall/nftables to implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("ValidateCategorizedSettingsPage failed: %v", err)
	}
	if err := settingsframework.ValidateSchema(catPage.SettingsPage); err != nil {
		t.Fatalf("settingsframework.ValidateSchema failed: %v", err)
	}
	if catPage.ID != nftables.SettingsPageID || catPage.Category != "firewall" || catPage.Scope != newapi.SettingsScopeDatacenter {
		t.Errorf("unexpected CategorizedSettingsPage metadata: %+v", catPage)
	}
	if _, ok := plugin.GetCategorizedSettingsPage(nftables.SettingsPageID); !ok {
		t.Errorf("expected firewall/nftables categorized settings page to be indexed in DefaultRegistry")
	}

	// 4. ContextMenuProvider
	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatalf("expected firewall/nftables to implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), facts)
	if len(entries) != 2 {
		t.Fatalf("expected 2 context menu entries for compatible linux host, got %d", len(entries))
	}
	if entries[0].Target != newapi.TargetFirewall || entries[0].PluginID != "firewall/nftables" || entries[0].Action != "allow_port" {
		t.Errorf("unexpected first context menu entry: %+v", entries[0])
	}
	if !entries[1].Danger || entries[1].Action != "deny_port" {
		t.Errorf("unexpected second context menu entry: %+v", entries[1])
	}
	if winEntries := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "windows"}); len(winEntries) != 0 {
		t.Errorf("expected nil/empty context menu entries for windows host, got %+v", winEntries)
	}

	// 5. TaskCompileProvider
	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatalf("expected firewall/nftables to implement newapi.TaskCompileProvider")
	}
	compiledTask, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		ID:       "req-1",
		Type:     newapi.TaskTypePluginAction,
		NodeID:   "node1",
		PluginID: "firewall/nftables",
		Action:   "deny_port",
		Params: map[string]any{
			"port":   "23",
			"proto":  "tcp",
			"policy": "reject",
		},
	})
	if err != nil {
		t.Fatalf("CompileTask failed: %v", err)
	}
	if len(compiledTask.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(compiledTask.Steps))
	}
	cmd, _ := compiledTask.Steps[0].Params["cmd"].(string)
	if !strings.Contains(cmd, "nft add rule inet filter input tcp dport 23 reject") {
		t.Errorf("unexpected CompileTask cmd: %q", cmd)
	}

	if _, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		ID:       "req-2",
		Type:     newapi.TaskTypePluginAction,
		NodeID:   "node1",
		PluginID: "firewall/nftables",
	}); err == nil {
		t.Errorf("expected CompileTask with missing action to return error")
	}
}
