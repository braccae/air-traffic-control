package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"prox-orch/backend/pkg/podman"
	"prox-orch/backend/pkg/ssh"
	"prox-orch/backend/pkg/types"
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
