package docker_test

import (
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/docker"
	"air-traffic-control/backend/pkg/ssh"
)

type staticRunner struct {
	responses map[string]string
	calls     []string
}

func (s *staticRunner) RunCommand(cfg ssh.Config, command string) (string, error) {
	s.calls = append(s.calls, command)
	for pattern, resp := range s.responses {
		if strings.Contains(command, pattern) {
			return resp, nil
		}
	}
	return "", nil
}

func TestDockerManager_SocketEscalationAndListAllContainers(t *testing.T) {
	cmd := docker.RootSocketOrCLICommand("/containers/json?all=1", "ps -a --format '{{json .}}'")
	idxRun0 := strings.Index(cmd, "run0 --background=no curl -s --unix-socket /var/run/docker.sock http://localhost/containers/json?all=1")
	idxSudo := strings.Index(cmd, "sudo -n curl -s --unix-socket /var/run/docker.sock http://localhost/containers/json?all=1")
	idxDoas := strings.Index(cmd, "doas -n curl -s --unix-socket /var/run/docker.sock http://localhost/containers/json?all=1")
	idxDirect := strings.Index(cmd, "|| curl -s --unix-socket /var/run/docker.sock http://localhost/containers/json?all=1")
	idxCLI := strings.Index(cmd, "docker ps -a --format '{{json .}}'")
	if idxRun0 < 0 || idxSudo <= idxRun0 || idxDoas <= idxSudo || idxDirect <= idxDoas || idxCLI <= idxDirect {
		t.Fatalf("invalid escalation order in RootSocketOrCLICommand: %s", cmd)
	}

	runner := &staticRunner{
		responses: map[string]string{
			"--unix-socket /var/run/docker.sock http://localhost/containers/json?all=1": `[
				{
					"Id": "a1b2c3d4e5f60123456789",
					"Names": ["/nextcloud-aio-apache"],
					"Image": "ghcr.io/apache:latest",
					"State": "running",
					"Status": "Up 2 hours",
					"Ports": [{"IP": "0.0.0.0", "PrivatePort": 80, "PublicPort": 8080, "Type": "tcp"}],
					"Labels": {
						"com.docker.compose.project": "nextcloud",
						"com.docker.compose.service": "apache",
						"com.docker.compose.project.config_files": "/opt/nextcloud/docker-compose.yml"
					}
				},
				{
					"Id": "f6e5d4c3b2a19876543210",
					"Names": ["/nextcloud-aio-database"],
					"Image": "postgres:15",
					"State": "running",
					"Status": "Up 2 hours",
					"Ports": [{"PrivatePort": 5432, "Type": "tcp"}],
					"Labels": {
						"com.docker.compose.project": "nextcloud",
						"com.docker.compose.service": "db"
					}
				}
			]`,
			"ls -1 /run/user/*/docker.sock": "/run/user/1000/docker.sock\n",
			"cat /etc/passwd":               "core:x:1000:1000::/home/core:/bin/bash\n",
			"ps -eo uid,user:32,args":       "1000 core dockerd-rootless\n",
			"--unix-socket /run/user/1000/docker.sock http://localhost/containers/json?all=1": `[
				{
					"Id": "99887766554433221100ff",
					"Names": ["/user-redis"],
					"Image": "redis:7-alpine",
					"State": "running",
					"Status": "Up 30 minutes"
				}
			]`,
		},
	}

	mgr := docker.NewManager(runner, ssh.Config{})
	containers, err := mgr.ListAllContainers()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(containers) != 3 {
		t.Fatalf("expected 3 containers (2 rootful + 1 rootless), got %d: %+v", len(containers), containers)
	}

	if containers[0].ID != "a1b2c3d4e5f6" {
		t.Errorf("expected 12-char ID a1b2c3d4e5f6, got %q", containers[0].ID)
	}
	if containers[0].Name != "nextcloud-aio-apache" {
		t.Errorf("expected leading slash trimmed -> nextcloud-aio-apache, got %q", containers[0].Name)
	}
	if containers[0].Ports != "0.0.0.0:8080->80/tcp" {
		t.Errorf("expected formatted port 0.0.0.0:8080->80/tcp, got %q", containers[0].Ports)
	}
	if containers[0].ComposeProject != "nextcloud" || containers[0].ComposeService != "apache" || containers[0].ComposeConfigFiles != "/opt/nextcloud/docker-compose.yml" {
		t.Errorf("expected compose labels extracted, got %+v", containers[0])
	}
	if containers[0].Rootless {
		t.Errorf("expected rootful container, got rootless")
	}

	if containers[2].Name != "user-redis" || !containers[2].Rootless || containers[2].User != "core" || containers[2].UID != 1000 {
		t.Errorf("expected rootless user-redis container for core (1000), got %+v", containers[2])
	}
}

func TestDockerManager_ProbeRejectsPodmanEmulation(t *testing.T) {
	podmanRunner := &staticRunner{
		responses: map[string]string{
			"/version": `{"Platform":{"Name":"Podman Engine"},"Version":"5.2.0","Components":[{"Name":"Podman Engine","Version":"5.2.0"}]}`,
		},
	}
	mgr := docker.NewManager(podmanRunner, ssh.Config{})
	if ok, _ := mgr.Probe(); ok {
		t.Fatalf("expected Probe() to reject Podman Engine socket emulation")
	}

	dockerRunner := &staticRunner{
		responses: map[string]string{
			"/version": `{"Platform":{"Name":"Docker Engine - Community"},"Version":"27.3.1","ApiVersion":"1.47"}`,
		},
	}
	mgr2 := docker.NewManager(dockerRunner, ssh.Config{})
	ok, ver := mgr2.Probe()
	if !ok || ver != "27.3.1" {
		t.Fatalf("expected Probe() to succeed with version 27.3.1, got ok=%v ver=%q", ok, ver)
	}
}

func TestDockerManager_ListImagesVolumesNetworksSocketAndCLIFallback(t *testing.T) {
	runner := &staticRunner{
		responses: map[string]string{
			"/images/json": `[{"Id":"sha256:abcdef0123456789","RepoTags":["alpine:latest"],"RepoDigests":["alpine@sha256:1111"],"Size":7300000,"Containers":2}]`,
			"/volumes":     `{"Volumes":[{"Name":"vol1","Driver":"local","Mountpoint":"/var/lib/docker/volumes/vol1/_data","Scope":"local","Labels":{"env":"prod"}}]}`,
			"/networks":    `[{"Id":"net1234567890abcdef","Name":"bridge","Driver":"bridge","Scope":"local","Internal":false,"EnableIPv6":false,"Labels":{}}]`,
		},
	}

	mgr := docker.NewManager(runner, ssh.Config{})
	images, err := mgr.ListImages()
	if err != nil || len(images) != 1 || images[0].Repository != "alpine" || images[0].Tag != "latest" {
		t.Fatalf("unexpected images from socket JSON: %+v err: %v", images, err)
	}

	volumes, err := mgr.ListVolumes()
	if err != nil || len(volumes) != 1 || volumes[0].Name != "vol1" || volumes[0].Labels != "env=prod" {
		t.Fatalf("unexpected volumes from socket JSON: %+v err: %v", volumes, err)
	}

	networks, err := mgr.ListNetworks()
	if err != nil || len(networks) != 1 || networks[0].Name != "bridge" || networks[0].ID != "net123456789" {
		t.Fatalf("unexpected networks from socket JSON: %+v err: %v", networks, err)
	}
}
