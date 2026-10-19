package interfaces_test

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
