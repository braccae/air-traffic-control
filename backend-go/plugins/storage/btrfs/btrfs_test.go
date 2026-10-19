package btrfs_test

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

func TestBtrfsPlugin_ProbeReadAndCompile(t *testing.T) {
	p, ok := plugin.Get("storage/btrfs")
	if !ok {
		t.Fatalf("storage/btrfs not registered")
	}

	runner := &mockRunner{
		responses: map[string]string{
			"command -v btrfs":       "btrfs",
			"btrfs filesystem show": "Label: 'pool'  uuid: 1234-5678\n\tTotal devices 1 FS bytes used 1.00GiB",
		},
	}

	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if !det.Detected {
		t.Errorf("expected btrfs detected")
	}

	reader := p.(plugin.Reader)
	data, err := reader.Read(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	m := data.(map[string]any)
	if !strings.Contains(m["btrfs"].(string), "Label: 'pool'") {
		t.Errorf("unexpected read: %+v", m)
	}

	actor := p.(plugin.Actor)
	task, err := actor.Compile(plugin.HostFacts{NodeID: "node1"}, "create_subvolume", map[string]any{
		"path": "/data/subvol1",
	})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if len(task.Steps) != 1 || task.Steps[0].Module != "community.general.btrfs_subvolume" {
		t.Errorf("unexpected steps: %+v", task.Steps)
	}
}
