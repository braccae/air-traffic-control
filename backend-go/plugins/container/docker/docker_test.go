package docker_test

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

func TestDockerPlugin_Registered(t *testing.T) {
	p, ok := plugin.Get("container/docker")
	if !ok {
		t.Fatalf("container/docker plugin not registered")
	}
	if p.Meta().Kind != plugin.KindContainer {
		t.Errorf("unexpected kind: %v", p.Meta().Kind)
	}
	if p.Meta().Name != "docker" {
		t.Errorf("unexpected name: %v", p.Meta().Name)
	}
}

func TestDockerPlugin_ProbeAndCompile(t *testing.T) {
	p, ok := plugin.Get("container/docker")
	if !ok {
		t.Fatalf("container/docker not registered")
	}

	runner := &mockRunner{
		responses: map[string]string{
			"command -v docker": "/usr/bin/docker",
			"docker ps -a -q":   "5\n",
		},
	}

	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if !det.Detected || !strings.Contains(det.Status, "5 container") {
		t.Errorf("unexpected probe result: %+v", det)
	}

	actor, ok := p.(plugin.Actor)
	if !ok {
		t.Fatalf("docker plugin does not implement Actor")
	}

	// 1. Restart container (declarative community.docker.docker_container)
	task, err := actor.Compile(plugin.HostFacts{NodeID: "likes-it-cold"}, "restart", map[string]any{
		"name": "nextcloud-aio-apache",
		"user": "root",
	})
	if err != nil {
		t.Fatalf("compile restart failed: %v", err)
	}
	if len(task.Steps) != 1 || task.Steps[0].Module != "community.docker.docker_container" {
		t.Errorf("unexpected task steps: %+v", task.Steps)
	}
	if task.Steps[0].Params["name"] != "nextcloud-aio-apache" || task.Steps[0].Params["restart"] != true {
		t.Errorf("unexpected params: %+v", task.Steps[0].Params)
	}

	// 2. Start container with rootless user become
	startTask, err := actor.Compile(plugin.HostFacts{NodeID: "likes-it-cold"}, "start", map[string]any{
		"name": "rootless-app",
		"user": "apps",
	})
	if err != nil {
		t.Fatalf("compile start failed: %v", err)
	}
	if len(startTask.Steps) != 1 || startTask.Steps[0].Module != "community.docker.docker_container" {
		t.Errorf("unexpected start steps: %+v", startTask.Steps)
	}
	if startTask.Steps[0].Params["become_user"] != "apps" || startTask.Steps[0].Params["become"] != true {
		t.Errorf("expected become apps: %+v", startTask.Steps[0].Params)
	}

	// 3. Pull image (declarative community.docker.docker_image)
	pullTask, err := actor.Compile(plugin.HostFacts{NodeID: "likes-it-cold"}, "pull", map[string]any{
		"image": "alpine:latest",
	})
	if err != nil {
		t.Fatalf("compile pull failed: %v", err)
	}
	if len(pullTask.Steps) != 1 || pullTask.Steps[0].Module != "community.docker.docker_image" {
		t.Errorf("unexpected pull steps: %+v", pullTask.Steps)
	}
	if pullTask.Steps[0].Params["name"] != "alpine:latest" {
		t.Errorf("unexpected pull params: %+v", pullTask.Steps[0].Params)
	}

	// 4. Remove container
	rmTask, err := actor.Compile(plugin.HostFacts{NodeID: "likes-it-cold"}, "remove", map[string]any{
		"name": "old-container",
	})
	if err != nil {
		t.Fatalf("compile remove failed: %v", err)
	}
	if len(rmTask.Steps) != 1 || rmTask.Steps[0].Params["state"] != "absent" {
		t.Errorf("unexpected remove params: %+v", rmTask.Steps[0].Params)
	}
}
