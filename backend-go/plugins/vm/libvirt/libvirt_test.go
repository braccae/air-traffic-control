package libvirt_test

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

func TestLibvirtPlugin_ProbeReadAndCompile(t *testing.T) {
	p, ok := plugin.Get("vm/libvirt")
	if !ok {
		t.Fatalf("vm/libvirt not registered")
	}

	runner := &mockRunner{
		responses: map[string]string{
			"which virsh": "/usr/bin/virsh\n===VIRSH_LIST===\n Id   Name       State\n------------------------\n 1    win11      running\n 2    ubuntu-vm  shut off\n===SERVICES===\nactive\n===KVM===\nkvm_amd",
		},
	}

	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if !det.Detected {
		t.Errorf("expected detected virsh")
	}

	reader := p.(plugin.Reader)
	data, err := reader.Read(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	m := data.(map[string]any)
	vms := m["vms"].([]map[string]any)
	if len(vms) != 2 {
		t.Fatalf("expected 2 vms, got %d", len(vms))
	}
	if vms[0]["status"] != "RUNNING" || vms[1]["status"] != "STOPPED" {
		t.Errorf("unexpected vm statuses: %+v", vms)
	}

	actor := p.(plugin.Actor)
	task, err := actor.Compile(plugin.HostFacts{NodeID: "node1"}, "start", map[string]any{
		"name": "ubuntu-vm",
	})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if len(task.Steps) != 1 || task.Steps[0].Module != "community.general.virt" {
		t.Errorf("unexpected task steps: %+v", task.Steps)
	}
	if task.Steps[0].Params["state"] != "running" {
		t.Errorf("expected state running, got %v", task.Steps[0].Params["state"])
	}
}
