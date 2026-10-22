package ufw_test

import (
	"context"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
	_ "air-traffic-control/backend/plugins/all"
	"air-traffic-control/backend/plugins/firewall/ufw"
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

const ufwVerboseFixture = `Status: active
Logging: on (low)
Default: deny (incoming), allow (outgoing), disabled (routed)
New profiles: skip

To                         Action      From
--                         ------      ----
22/tcp on tailscale0       ALLOW IN    100.64.0.0/10              # Tailscale SSH
443/tcp                    ALLOW IN    Anywhere                   # HTTPS
23/tcp                     DENY IN     Anywhere`

func TestUfwPlugin_AbsentToolReturnsDetectedFalse(t *testing.T) {
	p, ok := plugin.Get("firewall/ufw")
	if !ok {
		t.Fatalf("firewall/ufw not registered")
	}

	emptyRunner := &mockRunner{responses: map[string]string{}}
	det, err := p.Probe(context.Background(), emptyRunner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if det.Detected {
		t.Fatalf("expected Detected=false when ufw tool is absent, got %+v", det)
	}
}

func TestUfwPlugin_ProbeReadAndCompile(t *testing.T) {
	p, ok := plugin.Get("firewall/ufw")
	if !ok {
		t.Fatalf("firewall/ufw not registered")
	}

	meta := p.Meta()
	if meta.ID != "firewall/ufw" || meta.Kind != plugin.KindFirewall || meta.Name != "ufw" || meta.Label != "UFW" || meta.Order != 13 {
		t.Fatalf("unexpected meta: %+v", meta)
	}

	reqs := p.Requirements()
	if len(reqs.ControllerCollections) == 0 || reqs.ControllerCollections[0] != "community.general" {
		t.Fatalf("expected community.general collection requirement, got %+v", reqs)
	}

	runner := &mockRunner{
		responses: map[string]string{
			"command -v ufw":     "/usr/sbin/ufw\n",
			"ufw status verbose": ufwVerboseFixture,
			"ufw status":         "Status: active\n",
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
	if m["status"] != "active" || m["backend"] != "ufw" {
		t.Fatalf("unexpected read metadata: %+v", m)
	}

	rules, ok := m["rules"].([]ufw.ParsedFirewallRule)
	if !ok {
		t.Fatalf("rules is not []ufw.ParsedFirewallRule: %T", m["rules"])
	}
	if len(rules) != 3 {
		t.Fatalf("expected 3 parsed rules, got %d: %+v", len(rules), rules)
	}
	if rules[0].Port != "22" || rules[0].Proto != "tcp" || rules[0].Interface != "tailscale0" || rules[0].Source != "100.64.0.0/10" || rules[0].Comment != "Tailscale SSH" {
		t.Errorf("unexpected first rule: %+v", rules[0])
	}
	if rules[2].Policy != "DROP" || rules[2].Port != "23" {
		t.Errorf("unexpected third rule: %+v", rules[2])
	}

	actor := p.(plugin.Actor)
	task, err := actor.Compile(plugin.HostFacts{NodeID: "node1"}, "allow_port", map[string]any{
		"port":    "443",
		"proto":   "tcp",
		"from_ip": "10.0.0.0/8",
		"comment": "Allow HTTPS",
	})
	if err != nil {
		t.Fatalf("compile allow_port failed: %v", err)
	}
	if len(task.Steps) != 1 || task.Steps[0].Module != "community.general.ufw" {
		t.Fatalf("unexpected task steps: %+v", task.Steps)
	}
	params := task.Steps[0].Params
	if params["rule"] != "allow" || params["port"] != "443" || params["proto"] != "tcp" || params["from_ip"] != "10.0.0.0/8" {
		t.Errorf("unexpected allow_port params: %+v", params)
	}

	delTask, err := actor.Compile(plugin.HostFacts{NodeID: "node1"}, "delete_port", map[string]any{
		"port":  "443",
		"proto": "tcp",
	})
	if err != nil {
		t.Fatalf("compile delete_port failed: %v", err)
	}
	if delTask.Steps[0].Params["delete"] != true {
		t.Errorf("expected delete_port delete=true, got %+v", delTask.Steps[0].Params)
	}
}
