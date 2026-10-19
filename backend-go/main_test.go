package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/podman"
	"air-traffic-control/backend/pkg/ssh"
	"air-traffic-control/backend/pkg/types"
)

type testSSHRunner struct{}

func (r *testSSHRunner) RunCommand(cfg ssh.Config, cmd string) (string, error) {
	switch {
	case strings.Contains(cmd, "ps -eo"):
		return "1000 deployer /usr/bin/conmon -c 3f0a9918bc22 -n jellyfin-media-server\n1001 podman /usr/bin/conmon -c 7e4b9019a101 -n ollama-vllm-engine\n", nil
	case strings.Contains(cmd, "/etc/passwd"):
		return "root:x:0:0:root:/root:/bin/bash\ndeployer:x:1000:1000::/home/deployer:/bin/bash\npodman:x:1001:1001::/home/podman:/bin/bash\n", nil
	case strings.Contains(cmd, "/etc/subuid"):
		return "deployer:100000:65536\npodman:165536:65536\n", nil
	case strings.Contains(cmd, "/var/lib/systemd/linger"):
		return "deployer\npodman\n", nil
	case strings.Contains(cmd, "-u deployer") && strings.Contains(cmd, "podman ps"):
		return `[{"Id": "3f0a9918bc22", "Names": ["jellyfin-media-server"], "Image": "jellyfin/jellyfin:10.8.13", "State": "running", "Status": "Up 2 days"}]`, nil
	case strings.Contains(cmd, "-u podman") && strings.Contains(cmd, "podman ps"):
		return `[{"Id": "7e4b9019a101", "Names": ["ollama-vllm-engine"], "Image": "ollama/ollama:latest", "State": "running", "Status": "Up 12 hours"}]`, nil
	default:
		return `[]`, nil
	}
}

func TestPodmanProbeEndpoint_ReturnsUnprivilegedUsers(t *testing.T) {
	mgr := podman.NewManager(&testSSHRunner{}, ssh.Config{Host: "192.168.1.150", Port: 22, User: "root"})
	mux := newServerMux(nil, mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/podman/probe", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var users []types.UnprivilegedPodmanUser
	if err := json.NewDecoder(rec.Body).Decode(&users); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(users) != 2 {
		t.Fatalf("expected 2 unprivileged users running podman containers, got %d", len(users))
	}

	for _, u := range users {
		if u.UID == 0 || u.User == "root" {
			t.Errorf("probe endpoint must only return unprivileged users, got root: %+v", u)
		}
		if len(u.Containers) == 0 {
			t.Errorf("expected unprivileged user %s to have running podman containers", u.User)
		}
		for _, c := range u.Containers {
			if !c.Rootless || c.User != u.User || c.UID != u.UID {
				t.Errorf("expected container %s to have Rootless=true, User=%s, UID=%d, got %+v", c.Name, u.User, u.UID, c)
			}
		}
	}
}

type failingSSHWithLocalFallback struct{}

func (r *failingSSHWithLocalFallback) RunCommand(cfg ssh.Config, cmd string) (string, error) {
	// Simulate local host execution when target is 127.0.0.1 and running as unprivileged user (UID 1000) without passwordless sudo
	switch {
	case strings.Contains(cmd, "ps -eo"):
		return "1000 pants /usr/bin/conmon --api-version 1 -c 628e92ae175f -n hungry_swirles\n1000 pants /usr/bin/conmon --api-version 1 -c 97a2a7c3695f -n eloquent_hermann\n", nil
	case strings.Contains(cmd, "/etc/passwd"):
		return "root:x:0:0:root:/root:/bin/bash\npants:x:1000:1000:pants:/home/pants:/bin/bash\n", nil
	case strings.Contains(cmd, "/etc/subuid"):
		return "pants:100000:65536\n", nil
	case strings.Contains(cmd, "sudo ") && strings.Contains(cmd, "pants"):
		return "", http.ErrNotSupported
	case strings.HasPrefix(cmd, "su - pants"):
		return "", http.ErrNotSupported
	case cmd == "XDG_RUNTIME_DIR=/run/user/1000 podman ps --all --format json" || cmd == "XDG_RUNTIME_DIR=/run/user/1000 podman ps --format json":
		return `[
			{"Id": "628e92ae175f", "Names": ["hungry_swirles"], "Image": "localhost/alma:bug5-test", "State": "running", "Status": "Up 44 hours"},
			{"Id": "97a2a7c3695f", "Names": ["eloquent_hermann"], "Image": "localhost/dx:c1-verify", "State": "running", "Status": "Up 44 hours"},
			{"Id": "54a98e3053c1", "Names": ["romantic_wright"], "Image": "localhost/alma:bug5-test", "State": "exited", "Status": "Exited (0) 44 hours ago"}
		]`, nil
	default:
		return `[]`, nil
	}
}

func TestPodmanProbeEndpoint_DirectUserPodmanFallback(t *testing.T) {
	mgr := podman.NewManager(&failingSSHWithLocalFallback{}, ssh.Config{Host: "127.0.0.1", Port: 22, User: "pants"})
	mux := newServerMux(nil, mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/podman/probe", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var users []types.UnprivilegedPodmanUser
	if err := json.NewDecoder(rec.Body).Decode(&users); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(users) != 1 || users[0].User != "pants" || users[0].UID != 1000 {
		t.Fatalf("expected unprivileged user pants (UID 1000), got %+v", users)
	}
	// Must include full image & status from direct podman ps --all --format json (3 containers: 2 running + 1 exited)
	if len(users[0].Containers) != 3 {
		t.Fatalf("expected 3 containers from direct podman ps --all --format json fallback, got %d: %+v", len(users[0].Containers), users[0].Containers)
	}
	if users[0].Containers[0].Image != "localhost/alma:bug5-test" {
		t.Errorf("expected full container image from podman ps json, got %q", users[0].Containers[0].Image)
	}
}

