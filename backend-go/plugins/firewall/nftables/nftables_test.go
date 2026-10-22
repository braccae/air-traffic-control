package nftables_test

import (
	"context"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
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
			"command -v nft":  "/usr/sbin/nft\n",
			"nft list tables": "table inet filter\n",
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
