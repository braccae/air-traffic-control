package restic_test

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

func TestResticPlugin_ProbeReadAndCompile(t *testing.T) {
	p, ok := plugin.Get("backup/restic")
	if !ok {
		t.Fatalf("backup/restic not registered")
	}

	runner := &mockRunner{
		responses: map[string]string{
			"command -v restic": "restic",
			"snapshots --json":  "[{\"id\":\"abc12345\",\"short_id\":\"abc1234\",\"time\":\"2026-10-19T10:00:00Z\",\"paths\":[\"/data\"],\"tags\":[\"daily\"]}]",
		},
	}

	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if !det.Detected {
		t.Errorf("expected restic detected")
	}

	reader := p.(plugin.Reader)
	data, err := reader.Read(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	m := data.(map[string]any)
	if m["repo"] != "/var/backups/restic" {
		t.Errorf("unexpected repo: %v", m["repo"])
	}

	actor := p.(plugin.Actor)
	task, err := actor.Compile(plugin.HostFacts{NodeID: "node1"}, "backup", map[string]any{
		"path": "/etc",
		"tags": "config,daily",
	})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if len(task.Steps) != 1 || task.Steps[0].Module != "ansible.builtin.command" {
		t.Errorf("unexpected steps: %+v", task.Steps)
	}
}
