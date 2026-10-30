package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"air-traffic-control/backend/pkg/contextmenu"
	"air-traffic-control/backend/pkg/hostcache"
	"air-traffic-control/backend/pkg/nodeapi"
	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	"air-traffic-control/backend/pkg/podman"
	"air-traffic-control/backend/pkg/ssh"
	"air-traffic-control/backend/pkg/taskapi"
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
	mux, _ := newServerMux(nil, mgr, "", nil)

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
	mux, _ := newServerMux(nil, mgr, "", nil)

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

func TestAPINotFoundCatchAll_ReturnsJSON(t *testing.T) {
	mux, _ := newServerMux(nil, nil, "", nil)

	req := httptest.NewRequest(http.MethodGet, "/api/does/not/exist", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("expected json content type, got %q", ct)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode json 404 body: %v", err)
	}
	if body["success"] != false {
		t.Fatalf("expected success=false, got %v", body["success"])
	}
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, "/api/does/not/exist") {
		t.Fatalf("expected error to name the request path, got %q", msg)
	}
}

func TestAPINotFoundCatchAll_DoesNotShadowRealRoutes(t *testing.T) {
	mux, _ := newServerMux(nil, nil, "", nil)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected /api/health to still return 200, got %d", rec.Code)
	}
}

func TestUnknownNonAPIPath_ServesSPAFallback(t *testing.T) {
	mux, _ := newServerMux(nil, nil, "", nil)

	req := httptest.NewRequest(http.MethodGet, "/some/client/route", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// 200 when a real frontend build is embedded, 503 when the binary was built
	// without one. Either way it must not be a JSON API 404.
	if rec.Code != http.StatusOK && rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 200 or 503 for SPA fallback, got %d", rec.Code)
	}
	if strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatal("non-API path must not be answered by the API 404 handler")
	}
}

func TestGETNodesEndpoint_ReturnsPopulatedSpecsForAllNodes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "hosts-cache.sqlite")
	store, err := hostcache.Open(dbPath)
	if err != nil {
		t.Fatalf("open host cache: %v", err)
	}
	mux, _ := newServerMux(nil, nil, dbPath, store)

	req := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var nodes []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&nodes); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(nodes) == 0 {
		t.Fatalf("expected at least 1 node, got 0")
	}

	for _, n := range nodes {
		id, _ := n["id"].(string)
		shortName, _ := n["shortName"].(string)
		badge, _ := n["badge"].(string)

		if shortName == "" || badge == "" {
			t.Errorf("node %s missing shortName/badge: %+v", id, n)
		}

		specs, ok := n["specs"].(map[string]any)
		if !ok || specs == nil {
			t.Fatalf("node %s specs missing", id)
		}

		mb, _ := specs["motherboard"].(string)
		bios, _ := specs["bios"].(string)
		cpu, _ := specs["cpu"].(string)
		vcpus, _ := specs["vcpus"].(float64)
		ram, _ := specs["ramGiB"].(float64)

		if mb == "" || bios == "" || cpu == "" || vcpus <= 0 || ram <= 0 {
			t.Errorf("node %s invalid specs: %+v", id, specs)
		}
	}
}

func TestNewAPIsWiredInServerMux(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "hosts-cache.sqlite")
	store, err := hostcache.Open(dbPath)
	if err != nil {
		t.Fatalf("open host cache: %v", err)
	}
	// Production baseline seeding is disabled (SeedBaseline: false), so seed the
	// node exercised by the task-types execution subtest manually.
	nodeJSON := json.RawMessage(`{"id":"node-likes-it-cold","name":"likes-it-cold","shortName":"likes-it-cold","group":"tag:host","ip":"192.168.8.249","port":22,"sshUser":"core","tailscaleIp":"100.105.71.49","tailscaleHost":"likes-it-cold.leo-banjo.ts.net","status":"online","nodeType":"linux"}`)
	if _, err := store.SeedHostsIfEmpty([]hostcache.NodeRecord{
		{
			ID:            "node-likes-it-cold",
			Name:          "likes-it-cold",
			ShortName:     "likes-it-cold",
			Group:         "tag:host",
			IP:            "192.168.8.249",
			Port:          22,
			SSHUser:       "core",
			TailscaleIP:   "100.105.71.49",
			TailscaleHost: "likes-it-cold.leo-banjo.ts.net",
			Status:        "online",
			NodeJSON:      nodeJSON,
		},
	}); err != nil {
		t.Fatalf("seed host: %v", err)
	}
	mux, _ := newServerMux(nil, nil, dbPath, store)

	// 1. Verify all 29 registered plugins via GET /api/plugins
	t.Run("PluginsEndpoint", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/plugins", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/plugins status=%d body=%s", rec.Code, rec.Body.String())
		}
		var summaries []plugin.PluginSummary
		if err := json.NewDecoder(rec.Body).Decode(&summaries); err != nil {
			t.Fatalf("decode /api/plugins: %v", err)
		}
		if len(summaries) < 29 {
			t.Fatalf("expected at least 29 registered plugins, got %d", len(summaries))
		}
	})

	// 2. Verify global & categorized settings endpoints
	t.Run("SettingsEndpoints", func(t *testing.T) {
		reqPages := httptest.NewRequest(http.MethodGet, "/api/settings/pages", nil)
		recPages := httptest.NewRecorder()
		mux.ServeHTTP(recPages, reqPages)
		if recPages.Code != http.StatusOK {
			t.Fatalf("GET /api/settings/pages status=%d body=%s", recPages.Code, recPages.Body.String())
		}
		var pages []plugin.GlobalSettingsPageView
		if err := json.NewDecoder(recPages.Body).Decode(&pages); err != nil {
			t.Fatalf("decode /api/settings/pages: %v", err)
		}
		if len(pages) == 0 {
			t.Fatal("expected registered settings pages on /api/settings/pages")
		}

		reqCat := httptest.NewRequest(http.MethodGet, "/api/settings/categories?scope=datacenter", nil)
		recCat := httptest.NewRecorder()
		mux.ServeHTTP(recCat, reqCat)
		if recCat.Code != http.StatusOK {
			t.Fatalf("GET /api/settings/categories status=%d body=%s", recCat.Code, recCat.Body.String())
		}
		var groups []settingsframework.CategoryGroup
		if err := json.NewDecoder(recCat.Body).Decode(&groups); err != nil {
			t.Fatalf("decode /api/settings/categories: %v", err)
		}
		if len(groups) == 0 {
			t.Fatal("expected settings category groups")
		}

		reqDC := httptest.NewRequest(http.MethodGet, "/api/settings/datacenter", nil)
		recDC := httptest.NewRecorder()
		mux.ServeHTTP(recDC, reqDC)
		if recDC.Code != http.StatusOK {
			t.Fatalf("GET /api/settings/datacenter status=%d body=%s", recDC.Code, recDC.Body.String())
		}
	})

	// 3. Verify context menu endpoints
	t.Run("ContextMenuEndpoints", func(t *testing.T) {
		reqMenu := httptest.NewRequest(http.MethodGet, "/api/context-menus?target=node&nodeId=node-likes-it-cold", nil)
		recMenu := httptest.NewRecorder()
		mux.ServeHTTP(recMenu, reqMenu)
		if recMenu.Code != http.StatusOK {
			t.Fatalf("GET /api/context-menus status=%d body=%s", recMenu.Code, recMenu.Body.String())
		}
		var menu contextmenu.RenderedMenu
		if err := json.NewDecoder(recMenu.Body).Decode(&menu); err != nil {
			t.Fatalf("decode /api/context-menus: %v", err)
		}
		if menu.Target != contextmenu.TargetNode {
			t.Errorf("expected target=node, got %q", menu.Target)
		}

		reqTargets := httptest.NewRequest(http.MethodGet, "/api/context-menus/targets", nil)
		recTargets := httptest.NewRecorder()
		mux.ServeHTTP(recTargets, reqTargets)
		if recTargets.Code != http.StatusOK {
			t.Fatalf("GET /api/context-menus/targets status=%d body=%s", recTargets.Code, recTargets.Body.String())
		}
	})

	// 4. Verify inventory node types endpoint
	t.Run("NodeTypesEndpoint", func(t *testing.T) {
		reqNT := httptest.NewRequest(http.MethodGet, "/api/node-types", nil)
		recNT := httptest.NewRecorder()
		mux.ServeHTTP(recNT, reqNT)
		if recNT.Code != http.StatusOK {
			t.Fatalf("GET /api/node-types status=%d body=%s", recNT.Code, recNT.Body.String())
		}
		var ntResp nodeapi.NodeTypesResponse
		if err := json.NewDecoder(recNT.Body).Decode(&ntResp); err != nil {
			t.Fatalf("decode /api/node-types: %v", err)
		}
		if ntResp.Count < 7 || len(ntResp.NodeTypes) < 7 {
			t.Fatalf("expected at least 7 built-in node types, got %d", ntResp.Count)
		}
	})

	// 5. Verify task types discovery & execution endpoints
	t.Run("TaskTypesEndpoints", func(t *testing.T) {
		reqTT := httptest.NewRequest(http.MethodGet, "/api/task-types", nil)
		recTT := httptest.NewRecorder()
		mux.ServeHTTP(recTT, reqTT)
		if recTT.Code != http.StatusOK {
			t.Fatalf("GET /api/task-types status=%d body=%s", recTT.Code, recTT.Body.String())
		}
		var specs []taskapi.TaskTypeSpec
		if err := json.NewDecoder(recTT.Body).Decode(&specs); err != nil {
			t.Fatalf("decode /api/task-types: %v", err)
		}
		if len(specs) < 7 {
			t.Fatalf("expected at least 7 built-in task types, got %d", len(specs))
		}

		execBody, _ := json.Marshal(map[string]any{
			"id":     "test-main-task-1",
			"type":   "service_control",
			"nodeId": "node-likes-it-cold",
			"options": map[string]any{
				"service": "sshd.service",
				"state":   "restarted",
			},
		})
		reqExec := httptest.NewRequest(http.MethodPost, "/api/task-types/execute", bytes.NewReader(execBody))
		recExec := httptest.NewRecorder()
		mux.ServeHTTP(recExec, reqExec)
		if recExec.Code != http.StatusOK {
			t.Fatalf("POST /api/task-types/execute status=%d body=%s", recExec.Code, recExec.Body.String())
		}
		var result taskapi.TaskResult
		if err := json.NewDecoder(recExec.Body).Decode(&result); err != nil {
			t.Fatalf("decode TaskResult: %v", err)
		}
		if !result.Success || result.Compiled == nil {
			t.Fatalf("expected compiled TaskResult, got %+v", result)
		}
	})
}

type mockBackupStore struct {
	calls atomic.Int32
	err   error
}

func (m *mockBackupStore) Backup() error {
	m.calls.Add(1)
	return m.err
}

func TestHostDataUpdater_BackupCallsAndErrorLogging(t *testing.T) {
	// Capture log output to verify backup errors are logged
	var logBuf bytes.Buffer
	origOutput := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(origOutput)

	store := &mockBackupStore{err: errors.New("simulated backup failure")}
	updater := NewHostDataUpdater(10 * time.Millisecond)
	updater.SetStore(store)
	updater.Start()

	// Wait for ticker or start to invoke Backup()
	deadline := time.Now().Add(2 * time.Second)
	for store.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	updater.Stop()

	if store.calls.Load() == 0 {
		t.Fatal("expected Backup to be called by HostDataUpdater")
	}

	if !strings.Contains(logBuf.String(), "host updater: backup error: simulated backup failure") {
		t.Errorf("expected backup error to be logged, got log output: %q", logBuf.String())
	}
}
