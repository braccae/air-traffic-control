package podman_test

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

func TestPodmanPlugin_ProbeAndCompile(t *testing.T) {
	p, ok := plugin.Get("container/podman")
	if !ok {
		t.Fatalf("container/podman not registered")
	}

	runner := &mockRunner{
		responses: map[string]string{
			"command -v podman": "podman",
			"podman ps -q":      "c1\nc2\nc3",
		},
	}

	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if !det.Detected || !strings.Contains(det.Status, "3 container") {
		t.Errorf("unexpected probe result: %+v", det)
	}

	actor := p.(plugin.Actor)
	task, err := actor.Compile(plugin.HostFacts{NodeID: "node1"}, "restart", map[string]any{
		"name": "caddy",
		"user": "apps",
	})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if len(task.Steps) != 1 || task.Steps[0].Module != "containers.podman.podman_container" {
		t.Errorf("unexpected task steps: %+v", task.Steps)
	}
	if task.Steps[0].Params["become_user"] != "apps" {
		t.Errorf("expected become_user apps, got %v", task.Steps[0].Params["become_user"])
	}
}
