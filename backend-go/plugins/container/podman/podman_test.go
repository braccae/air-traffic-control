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

func TestPodmanPlugin_ReadBothRootfulAndRootlessContainers(t *testing.T) {
	p, ok := plugin.Get("container/podman")
	if !ok {
		t.Fatalf("container/podman not registered")
	}

	reader, ok := p.(plugin.Reader)
	if !ok {
		t.Fatalf("container/podman does not implement plugin.Reader")
	}

	runner := &mockRunner{
		responses: map[string]string{
			"--unix-socket=/run/podman/podman.sock \"http://d/v4.0.0/libpod/containers/json?all=true\"": `[
				{"Id": "e377a55482d2", "Names": ["systemd-caddy"], "Image": "ghcr.io/braccae/caddy:latest", "State": "running", "Status": "healthy", "IsInfra": false}
			]`,
			"--unix-socket=/run/podman/podman.sock \"http://d/v4.0.0/libpod/pods/json\"": `[
				{"Id": "f4e8950bc5d4", "Name": "systemd-proxy", "Status": "Running"}
			]`,
			"/etc/passwd": "root:x:0:0:root:/root:/bin/bash\napps:x:2200:2200:Apps:/home/apps:/bin/bash\n",
			"ls -1d /run/user/*/podman/podman.sock": "/run/user/2200/podman/podman.sock\n",
			"--unix-socket=/run/user/2200/podman/podman.sock \"http://d/v4.0.0/libpod/containers/json?all=true\"": `[
				{"Id": "869408baab38", "Names": ["FeedCord"], "Image": "docker.io/qolors/feedcord:latest", "State": "running", "Status": "Up 3 days", "IsInfra": false}
			]`,
		},
	}

	raw, err := reader.Read(context.Background(), runner, plugin.HostFacts{NodeID: "node-1", OSFamily: "linux"})
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	data, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any from Read, got %T", raw)
	}

	if _, hasRoot := data["rootContainers"]; !hasRoot {
		t.Errorf("expected rootContainers key in Read output")
	}
	if _, hasProbed := data["probedUsers"]; !hasProbed {
		t.Errorf("expected probedUsers key in Read output")
	}
}
