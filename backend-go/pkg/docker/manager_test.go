package docker_test

import (
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/docker"
	"air-traffic-control/backend/pkg/ssh"
)

type staticRunner struct {
	responses map[string]string
}

func (s *staticRunner) RunCommand(cfg ssh.Config, command string) (string, error) {
	for pattern, resp := range s.responses {
		if strings.Contains(command, pattern) {
			return resp, nil
		}
	}
	return "", nil
}

func TestDockerManager_ListAllContainers(t *testing.T) {
	runner := &staticRunner{
		responses: map[string]string{
			"docker ps -a": `{"ID":"c1","Names":"nextcloud-aio-apache","Image":"ghcr.io/apache:latest","State":"running","Status":"Up 2 hours","Ports":"80/tcp"}
{"ID":"c2","Names":"nextcloud-aio-database","Image":"postgres:15","State":"running","Status":"Up 2 hours","Ports":"5432/tcp"}`,
			"ls -1 /run/user/*/docker.sock": "/run/user/1000/docker.sock\n",
			"cat /etc/passwd":               "core:x:1000:1000::/home/core:/bin/bash\n",
			"ps -eo uid,user:32,args":       "1000 core dockerd-rootless\n",
		},
	}

	mgr := docker.NewManager(runner, ssh.Config{})
	containers, err := mgr.ListAllContainers()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(containers) < 2 {
		t.Fatalf("expected at least 2 containers, got %d", len(containers))
	}

	if containers[0].Name != "nextcloud-aio-apache" {
		t.Errorf("expected first container nextcloud-aio-apache, got %s", containers[0].Name)
	}
	if containers[0].Rootless {
		t.Errorf("expected rootful container, got rootless")
	}
}

func TestDockerManager_ListImagesVolumesNetworks(t *testing.T) {
	runner := &staticRunner{
		responses: map[string]string{
			"docker images": `{"ID":"img1","Repository":"alpine","Tag":"latest","Digest":"","Size":"7.3MB","CreatedSince":"2 weeks ago","Containers":"N/A"}`,
			"docker volume ls": `{"Name":"vol1","Driver":"local","Mountpoint":"/var/lib/docker/volumes/vol1/_data","Scope":"local","Labels":""}`,
			"docker network ls": `{"ID":"net1","Name":"bridge","Driver":"bridge","Scope":"local","Internal":"false","IPv6":"false","Labels":""}`,
		},
	}

	mgr := docker.NewManager(runner, ssh.Config{})
	images, err := mgr.ListImages()
	if err != nil || len(images) != 1 || images[0].Repository != "alpine" {
		t.Fatalf("unexpected images: %+v err: %v", images, err)
	}

	volumes, err := mgr.ListVolumes()
	if err != nil || len(volumes) != 1 || volumes[0].Name != "vol1" {
		t.Fatalf("unexpected volumes: %+v err: %v", volumes, err)
	}

	networks, err := mgr.ListNetworks()
	if err != nil || len(networks) != 1 || networks[0].Name != "bridge" {
		t.Fatalf("unexpected networks: %+v err: %v", networks, err)
	}
}
