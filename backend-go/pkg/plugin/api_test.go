package plugin_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
	_ "air-traffic-control/backend/plugins/all"
)

type dummyRunner struct {
	responses map[string]string
}

func (d *dummyRunner) RunCommand(ctx context.Context, cmd string) (string, error) {
	for k, v := range d.responses {
		if strings.Contains(cmd, k) {
			return v, nil
		}
	}
	return "", nil
}

type memoryNodeResolver struct {
	mu    sync.RWMutex
	nodes map[string]plugin.HostFacts
}

func newMemoryNodeResolver() *memoryNodeResolver {
	return &memoryNodeResolver{
		nodes: map[string]plugin.HostFacts{
			"node-test-1": {
				NodeID:           "node-test-1",
				OSFamily:         "linux",
				HostRoute:        "127.0.0.1",
				ActiveConnection: "connection/local",
				ActiveElevation:  "elevation/none",
				ActivePackage:    "package/dnf",
			},
		},
	}
}

func (m *memoryNodeResolver) ResolveNode(nodeID string) (plugin.HostFacts, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	facts, ok := m.nodes[nodeID]
	return facts, ok, nil
}

type staticRunnerResolver struct {
	runner plugin.Runner
}

func (s *staticRunnerResolver) ResolveRunner(ctx context.Context, facts plugin.HostFacts) (plugin.Runner, error) {
	if s.runner != nil {
		return s.runner, nil
	}
	return &dummyRunner{}, nil
}

func TestAPI_ListPlugins(t *testing.T) {
	api := plugin.NewAPI(plugin.APIOptions{
		Registry:       nil, // uses defaultRegistry
		NodeResolver:   newMemoryNodeResolver(),
		RunnerResolver: &staticRunnerResolver{},
	})

	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/plugins", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var plugins []plugin.PluginSummary
	if err := json.NewDecoder(rec.Body).Decode(&plugins); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(plugins) == 0 {
		t.Fatalf("expected registered plugins, got none")
	}

	// Verify we have plugins from connection, elevation, storage, etc.
	foundZFS := false
	for _, p := range plugins {
		if p.ID == "storage/zfs" {
			foundZFS = true
			if len(p.Actions) == 0 {
				t.Errorf("expected zfs to have actions in summary")
			}
			break
		}
	}
	if !foundZFS {
		t.Errorf("expected storage/zfs in plugin list")
	}
}

func TestAPI_NodePluginsAndModules(t *testing.T) {
	nodeResolver := newMemoryNodeResolver()
	runner := &dummyRunner{
		responses: map[string]string{
			"command -v zpool":      "/usr/sbin/zpool",
			"command -v zfs":        "/usr/sbin/zfs",
			"zpool list -H -o name": "tank\n",
		},
	}

	api := plugin.NewAPI(plugin.APIOptions{
		NodeResolver:   nodeResolver,
		RunnerResolver: &staticRunnerResolver{runner: runner},
	})

	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	// 1. GET /api/nodes/:id/plugins
	t.Run("NodePlugins", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/nodes/node-test-1/plugins", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var detected []plugin.NodePluginItem
		if err := json.NewDecoder(rec.Body).Decode(&detected); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		foundZfs := false
		for _, item := range detected {
			if item.ID == "storage/zfs" {
				foundZfs = true
				if !item.Detection.Detected {
					t.Errorf("expected zfs to be detected")
				}
				if !strings.Contains(item.Detection.Status, "pool") {
					t.Errorf("expected zfs status to contain pool, got %s", item.Detection.Status)
				}
			}
		}
		if !foundZfs {
			t.Errorf("expected storage/zfs in detected node plugins list")
		}
	})

	// 2. GET /api/nodes/:id/modules
	t.Run("NodeModules", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/nodes/node-test-1/modules", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var modules []plugin.NodeModuleItem
		if err := json.NewDecoder(rec.Body).Decode(&modules); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		foundActive := false
		for _, m := range modules {
			if m.ID == "storage/zfs" {
				if m.Status != "active" {
					t.Errorf("expected zfs status active, got %s", m.Status)
				}
				foundActive = true
			}
		}
		if !foundActive {
			t.Errorf("expected zfs module in active status")
		}
	})
}

func TestAPI_ReadAndActionEndpoints(t *testing.T) {
	nodeResolver := newMemoryNodeResolver()
	runner := &dummyRunner{
		responses: map[string]string{
			"findmnt": "{\"filesystems\": []}\n===ZFS===\ntank/data 10G 5G 5G /tank/data\n===ZPOOL===\npool: tank state: ONLINE\n===ZPOOL_LIST===\ntank 100G 20G 80G ONLINE",
		},
	}

	api := plugin.NewAPI(plugin.APIOptions{
		NodeResolver:   nodeResolver,
		RunnerResolver: &staticRunnerResolver{runner: runner},
	})

	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	// 1. GET /api/nodes/:id/plugins/:kind/:name (Read)
	t.Run("PluginRead", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/nodes/node-test-1/plugins/storage/zfs", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var res map[string]any
		if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
			t.Fatalf("failed to decode read response: %v", err)
		}

		if res["zpoolList"] == nil {
			t.Errorf("expected zpoolList in read response, got: %v", res)
		}
	})

	// 2. POST /api/nodes/:id/plugins/:kind/:name/actions/:action (Action Compile)
	t.Run("PluginActionCompile", func(t *testing.T) {
		payload := map[string]any{
			"options": map[string]any{
				"pool": "tank",
			},
		}
		bodyBytes, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-test-1/plugins/storage/zfs/actions/scrub", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var task plugin.TaskSpec
		if err := json.NewDecoder(rec.Body).Decode(&task); err != nil {
			t.Fatalf("failed to decode task response: %v", err)
		}

		if len(task.Steps) == 0 {
			t.Fatalf("expected compiled task steps, got 0")
		}
		if task.Steps[0].Module != "community.general.zpool" {
			t.Errorf("expected module community.general.zpool, got %s", task.Steps[0].Module)
		}
	})
}

func TestAPI_CachingAndRefresh(t *testing.T) {
	nodeResolver := newMemoryNodeResolver()
	runner := &dummyRunner{
		responses: map[string]string{
			"command -v zpool":      "/usr/sbin/zpool",
			"command -v zfs":        "/usr/sbin/zfs",
			"zpool list -H -o name": "tank\n",
		},
	}

	api := plugin.NewAPI(plugin.APIOptions{
		NodeResolver:   nodeResolver,
		RunnerResolver: &staticRunnerResolver{runner: runner},
	})

	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	// First call - populates cache
	req1 := httptest.NewRequest(http.MethodGet, "/api/nodes/node-test-1/plugins", nil)
	rec1 := httptest.NewRecorder()
	mux.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("call 1 failed: %d", rec1.Code)
	}

	// Second call - should hit cache
	req2 := httptest.NewRequest(http.MethodGet, "/api/nodes/node-test-1/plugins", nil)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("call 2 failed: %d", rec2.Code)
	}

	// POST /api/nodes/:id/plugins/refresh
	refreshReq := httptest.NewRequest(http.MethodPost, "/api/nodes/node-test-1/plugins/refresh", nil)
	refreshRec := httptest.NewRecorder()
	mux.ServeHTTP(refreshRec, refreshReq)
	if refreshRec.Code != http.StatusOK {
		t.Fatalf("refresh call failed: %d", refreshRec.Code)
	}
}

func TestAPI_SQLiteNodeResolver(t *testing.T) {
	// Test resolving against real data/hosts-cache.sqlite if present
	candidates := []string{
		filepath.Join("..", "..", "..", "data", "hosts-cache.sqlite"),
		filepath.Join("..", "..", "data", "hosts-cache.sqlite"),
		filepath.Join("data", "hosts-cache.sqlite"),
	}
	var dbPath string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			dbPath = c
			break
		}
	}
	if dbPath == "" {
		t.Skip("data/hosts-cache.sqlite not found, skipping SQLite resolver test")
	}

	// Check if the hosts table exists
	d, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		t.Skip("cannot open sqlite database for table check")
	}
	defer d.Close()
	var tableExists int
	err = d.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='hosts'").Scan(&tableExists)
	if err != nil || tableExists == 0 {
		t.Skip("hosts table not found in SQLite database")
	}

	resolver, err := plugin.NewSQLiteNodeResolver(dbPath)
	if err != nil {
		t.Fatalf("failed to create sqlite node resolver: %v", err)
	}
	defer resolver.Close()

	facts, found, err := resolver.ResolveNode("node-likes-it-cold")
	if err != nil {
		t.Fatalf("failed to resolve node: %v", err)
	}
	if !found {
		t.Fatalf("node-likes-it-cold not found in SQLite db")
	}

	if facts.NodeID != "node-likes-it-cold" {
		t.Errorf("expected node ID node-likes-it-cold, got %s", facts.NodeID)
	}
	if facts.OSFamily != "linux" {
		t.Errorf("expected OSFamily linux, got %s", facts.OSFamily)
	}
	if facts.HostRoute == "" {
		t.Errorf("expected non-empty HostRoute")
	}
}

func TestResolveDBPath(t *testing.T) {
	// Explicit path wins verbatim.
	if got := plugin.ResolveDBPath("/tmp/explicit-hosts-cache.sqlite"); got != "/tmp/explicit-hosts-cache.sqlite" {
		t.Fatalf("explicit path not honored: got %q", got)
	}

	// ATC_DB_PATH env var is consulted when no explicit path is given.
	t.Setenv("ATC_DB_PATH", "/tmp/env-hosts-cache.sqlite")
	if got := plugin.ResolveDBPath(""); got != "/tmp/env-hosts-cache.sqlite" {
		t.Fatalf("ATC_DB_PATH not honored: got %q", got)
	}

	// With neither set, resolution walks upward and must never be empty.
	t.Setenv("ATC_DB_PATH", "")
	if got := plugin.ResolveDBPath(""); got == "" {
		t.Fatalf("ResolveDBPath returned empty string")
	}
}
