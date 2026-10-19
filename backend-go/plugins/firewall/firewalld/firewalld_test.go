package firewalld_test

import (
	"context"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
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

func TestFirewalldPlugin_ProbeReadAndCompile(t *testing.T) {
	p, ok := plugin.Get("firewall/firewalld")
	if !ok {
		t.Fatalf("firewall/firewalld not registered")
	}

	runner := &mockRunner{
		responses: map[string]string{
			"firewall-cmd --state": "running\n===ZONES===\npublic\n  interfaces: eth0\ntrusted\n  interfaces: tailscale0\n===LIST_ALL===\npublic (active)\n  services: ssh dhcpv6-client\n  ports: 8080/tcp 9000/udp\n===NFT===\ntable inet firewalld",
		},
	}

	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if !det.Detected || det.Status != "running" {
		t.Errorf("unexpected probe result: %+v", det)
	}

	reader := p.(plugin.Reader)
	data, err := reader.Read(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	m := data.(map[string]any)
	if m["status"] != "running" || m["nftablesActive"] != true {
		t.Errorf("unexpected read result: %+v", m)
	}

	actor := p.(plugin.Actor)
	task, err := actor.Compile(plugin.HostFacts{NodeID: "node1"}, "allow_port", map[string]any{
		"port":  "443",
		"proto": "tcp",
		"zone":  "public",
	})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if len(task.Steps) != 1 || task.Steps[0].Module != "ansible.posix.firewalld" {
		t.Errorf("unexpected task steps: %+v", task.Steps)
	}
	if task.Steps[0].Params["port"] != "443/tcp" || task.Steps[0].Params["zone"] != "public" {
		t.Errorf("unexpected task params: %+v", task.Steps[0].Params)
	}
}
