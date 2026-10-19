package lvm_test

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

func TestLVMPlugin_ProbeReadAndCompile(t *testing.T) {
	p, ok := plugin.Get("storage/lvm")
	if !ok {
		t.Fatalf("storage/lvm not registered")
	}

	runner := &mockRunner{
		responses: map[string]string{
			"command -v lvs": "lvs\nvgs",
			"vgs --noheadings": "vg_data\nvg_system",
			"reportformat json": "{\"report\": [{\"vg\": [{\"vg_name\": \"vg_data\"}]}]}",
		},
	}

	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if !det.Detected || !strings.Contains(det.Status, "2 VG") {
		t.Errorf("unexpected lvm probe: %+v", det)
	}

	reader := p.(plugin.Reader)
	data, err := reader.Read(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	m := data.(map[string]any)
	if m["vgs"] == "" {
		t.Errorf("expected vgs json data")
	}

	actor := p.(plugin.Actor)
	task, err := actor.Compile(plugin.HostFacts{NodeID: "node1"}, "create_lv", map[string]any{
		"vg":   "vg_data",
		"lv":   "lv_app",
		"size": "20G",
	})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if len(task.Steps) != 1 || task.Steps[0].Module != "community.general.lvol" {
		t.Errorf("unexpected steps: %+v", task.Steps)
	}
}
