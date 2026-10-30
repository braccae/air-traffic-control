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

func TestIptablesPlugin_MasqueradeAndForwardPortCompile(t *testing.T) {
	p, ok := plugin.Get("firewall/iptables")
	if !ok {
		t.Fatalf("firewall/iptables not registered")
	}
	actor := p.(plugin.Actor)
	facts := plugin.HostFacts{NodeID: "node1", OSFamily: "linux"}

	// Masquerade enable
	enableTask, err := actor.Compile(facts, "masquerade", map[string]any{"enabled": true})
	if err != nil {
		t.Fatalf("compile masquerade enable failed: %v", err)
	}
	if len(enableTask.Steps) != 1 || enableTask.Steps[0].Module != "ansible.builtin.shell" {
		t.Fatalf("unexpected masquerade enable steps: %+v", enableTask.Steps)
	}
	enableCmd, _ := enableTask.Steps[0].Params["cmd"].(string)
	wantEnable := "iptables -t nat -C POSTROUTING -j MASQUERADE 2>/dev/null || iptables -t nat -A POSTROUTING -j MASQUERADE"
	if enableCmd != wantEnable {
		t.Errorf("unexpected masquerade enable command:\n got: %s\nwant: %s", enableCmd, wantEnable)
	}

	// Masquerade disable
	disableTask, err := actor.Compile(facts, "masquerade", map[string]any{"enabled": false})
	if err != nil {
		t.Fatalf("compile masquerade disable failed: %v", err)
	}
	disableCmd, _ := disableTask.Steps[0].Params["cmd"].(string)
	wantDisable := "iptables -t nat -C POSTROUTING -j MASQUERADE 2>/dev/null && iptables -t nat -D POSTROUTING -j MASQUERADE || true"
	if disableCmd != wantDisable {
		t.Errorf("unexpected masquerade disable command:\n got: %s\nwant: %s", disableCmd, wantDisable)
	}

	// Forward port enable
	fwdTask, err := actor.Compile(facts, "forward_port", map[string]any{
		"port":   "8080",
		"proto":  "tcp",
		"toport": "80",
		"toaddr": "192.168.1.10",
	})
	if err != nil {
		t.Fatalf("compile forward_port failed: %v", err)
	}
	if fwdTask.Steps[0].Module != "ansible.builtin.shell" {
		t.Fatalf("unexpected forward_port module: %s", fwdTask.Steps[0].Module)
	}
	fwdCmd, _ := fwdTask.Steps[0].Params["cmd"].(string)
	for _, want := range []string{
		"iptables -t nat -C PREROUTING -p tcp --dport 8080 -j DNAT --to-destination 192.168.1.10:80",
		"iptables -t nat -A PREROUTING -p tcp --dport 8080 -j DNAT --to-destination 192.168.1.10:80",
		"iptables -C FORWARD -p tcp -d 192.168.1.10 --dport 80 -j ACCEPT",
		"iptables -A FORWARD -p tcp -d 192.168.1.10 --dport 80 -j ACCEPT",
	} {
		if !strings.Contains(fwdCmd, want) {
			t.Errorf("forward_port command missing %q: %s", want, fwdCmd)
		}
	}

	// Forward port removal
	rmTask, err := actor.Compile(facts, "forward_port", map[string]any{
		"port":   "8080",
		"proto":  "tcp",
		"toport": "80",
		"toaddr": "192.168.1.10",
		"state":  "disabled",
	})
	if err != nil {
		t.Fatalf("compile forward_port removal failed: %v", err)
	}
	rmCmd, _ := rmTask.Steps[0].Params["cmd"].(string)
	for _, want := range []string{
		"iptables -t nat -D PREROUTING -p tcp --dport 8080 -j DNAT --to-destination 192.168.1.10:80",
		"iptables -D FORWARD -p tcp -d 192.168.1.10 --dport 80 -j ACCEPT",
	} {
		if !strings.Contains(rmCmd, want) {
			t.Errorf("forward_port removal missing %q: %s", want, rmCmd)
		}
	}

	// Validation failures
	if _, err := actor.Compile(facts, "forward_port", map[string]any{"port": "8080"}); err == nil {
		t.Errorf("expected missing toport to fail compile")
	}
	if _, err := actor.Compile(facts, "forward_port", map[string]any{"port": "8000:8080", "toport": "80"}); err == nil {
		t.Errorf("expected port range to fail compile")
	}
	if _, err := actor.Compile(facts, "masquerade", map[string]any{"enabled": "maybe"}); err == nil {
		t.Errorf("expected invalid enabled value to fail compile")
	}
}

func TestParseIptablesNAT(t *testing.T) {
	raw := `-P PREROUTING ACCEPT
-A PREROUTING -p tcp -m tcp --dport 8080 -j DNAT --to-destination 192.168.1.10:80
-A PREROUTING -p udp -m udp --dport 5353 -j DNAT --to-destination :53
-P POSTROUTING ACCEPT
-A POSTROUTING -o eth0 -j MASQUERADE`

	masquerade, forwards := iptables.ParseIptablesNAT(raw)
	if !masquerade {
		t.Errorf("expected masquerade=true")
	}
	if len(forwards) != 2 {
		t.Fatalf("expected 2 forward ports, got %d: %+v", len(forwards), forwards)
	}
	if forwards[0].Port != "8080" || forwards[0].Proto != "tcp" || forwards[0].ToAddr != "192.168.1.10" || forwards[0].ToPort != "80" {
		t.Errorf("unexpected first forward: %+v", forwards[0])
	}
	if forwards[1].Proto != "udp" || forwards[1].Port != "5353" || forwards[1].ToAddr != "" || forwards[1].ToPort != "53" {
		t.Errorf("unexpected second forward: %+v", forwards[1])
	}

	if m, f := iptables.ParseIptablesNAT(""); m || len(f) != 0 {
		t.Errorf("expected empty NAT state for empty input, got masquerade=%v forwards=%+v", m, f)
	}
}

func TestIptablesPlugin_ReadReportsNATState(t *testing.T) {
	p, ok := plugin.Get("firewall/iptables")
	if !ok {
		t.Fatalf("firewall/iptables not registered")
	}
	runner := &mockRunner{
		responses: map[string]string{
			"===NAT===": iptablesSaveFixture + "\n===IP6===\n-P INPUT ACCEPT\n===NAT===\n" +
				"-P PREROUTING ACCEPT\n" +
				"-A PREROUTING -p tcp -m tcp --dport 8080 -j DNAT --to-destination 192.168.1.10:80\n" +
				"-P POSTROUTING ACCEPT\n" +
				"-A POSTROUTING -o eth0 -j MASQUERADE\n",
		},
	}

	data, err := p.(plugin.Reader).Read(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	m := data.(map[string]any)
	if m["masquerade"] != true {
		t.Errorf("expected masquerade=true, got %v", m["masquerade"])
	}
	fps, ok := m["forwardPorts"].([]iptables.NATForwardPort)
	if !ok {
		t.Fatalf("forwardPorts is not []iptables.NATForwardPort: %T", m["forwardPorts"])
	}
	if len(fps) != 1 || fps[0].Port != "8080" || fps[0].Proto != "tcp" || fps[0].ToAddr != "192.168.1.10" || fps[0].ToPort != "80" {
		t.Errorf("unexpected forwardPorts: %+v", fps)
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
	if len(mgmtActions) != 5 {
		t.Fatalf("expected 5 management actions, got %d", len(mgmtActions))
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
