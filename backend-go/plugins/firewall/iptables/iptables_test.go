package iptables_test

import (
	"context"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
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
	if !det.Detected || det.Status != "inactive" {
		t.Fatalf("expected Detected=true and Status=inactive when firewalld is running, got %+v", det)
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
