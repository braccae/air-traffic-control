package rcloneapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"air-traffic-control/backend/pkg/ansqueue"
	"air-traffic-control/backend/pkg/hostcache"
	_ "air-traffic-control/backend/plugins/mount/rclone"
)

func setupTestRcloneAPI(t *testing.T) (*API, *hostcache.Store, *ansqueue.Store, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hosts-cache.sqlite")
	store, err := hostcache.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open hostcache store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	qStore := ansqueue.New(store.SQLDB())

	api, err := NewAPI(Options{
		Store:        store,
		QueueStore:   qStore,
		RepoRoot:     dir,
		DefaultActor: "test-actor",
	})
	if err != nil {
		t.Fatalf("failed to create rclone api: %v", err)
	}
	return api, store, qStore, dir
}

func TestProvidersAndSanitization(t *testing.T) {
	api, _, _, _ := setupTestRcloneAPI(t)
	mux := http.NewServeMux()
	api.Register(mux)

	// GET /api/rclone/providers
	req := httptest.NewRequest(http.MethodGet, "/api/rclone/providers", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var res struct {
		Success   bool       `json:"success"`
		Providers []Provider `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if !res.Success || len(res.Providers) < 5 {
		t.Fatalf("unexpected providers response: %+v", res)
	}

	// Sanitization checks
	clean, err := SanitizeRemoteName("  my-drive_01  ")
	if err != nil || clean != "my-drive_01" {
		t.Fatalf("unexpected clean name: %s, %v", clean, err)
	}
	if _, err := SanitizeRemoteName("bad/name"); err == nil {
		t.Fatalf("expected error for bad name")
	}
	if _, err := SanitizeMountTarget("relative/path"); err == nil {
		t.Fatalf("expected error for relative mount target")
	}
	cleanTarget, err := SanitizeMountTarget("/mnt/data//sub")
	if err != nil || cleanTarget != "/mnt/data/sub" {
		t.Fatalf("unexpected clean target: %s, %v", cleanTarget, err)
	}
}

func TestRcloneRemotesCRUDAndMounts(t *testing.T) {
	api, store, _, _ := setupTestRcloneAPI(t)
	mux := http.NewServeMux()
	api.Register(mux)

	// Seed node
	_ = store.UpsertHost(hostcache.NodeRecord{
		ID:        "node-1",
		Name:      "test-node",
		ShortName: "test-node",
		IP:        "192.168.1.100",
		Status:    "ONLINE",
		NodeJSON:  []byte(`{"id":"node-1","name":"test-node","ip":"192.168.1.100"}`),
	})

	// Pre-create a remote record
	configJSON := `{"endpoint":"https://s3.example.com","access_key_id":"vault:rclone_minio_key","secret_access_key":"vault:rclone_minio_secret"}`
	err := store.UpsertRcloneRemote(hostcache.RcloneRemote{
		ID:         "rclone-minio",
		Name:       "minio",
		Provider:   "s3-compatible",
		Type:       "s3",
		ConfigJSON: configJSON,
		CreatedAt:  1000,
		UpdatedAt:  1000,
	})
	if err != nil {
		t.Fatalf("failed to upsert remote: %v", err)
	}

	// GET /api/rclone/remotes
	req := httptest.NewRequest(http.MethodGet, "/api/rclone/remotes", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var remotesRes struct {
		Success bool         `json:"success"`
		Remotes []RemoteView `json:"remotes"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &remotesRes)
	if len(remotesRes.Remotes) != 1 || remotesRes.Remotes[0].Fields["access_key_id"] != "•••••••• (vault: rclone_minio_key)" {
		t.Fatalf("unexpected masked fields: %+v", remotesRes)
	}

	// GET /api/rclone/remotes/:id
	req = httptest.NewRequest(http.MethodGet, "/api/rclone/remotes/rclone-minio", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Mount record test
	mountErr := store.UpsertRcloneMount(hostcache.RcloneMount{
		NodeID:      "node-1",
		RemoteID:    "rclone-minio",
		RemoteName:  "minio",
		MountTarget: "/mnt/minio",
		Policy:      "systemd-mount",
		Status:      "active",
		InstalledAt: 2000,
		UpdatedAt:   2000,
	})
	if mountErr != nil {
		t.Fatalf("failed to insert mount: %v", mountErr)
	}

	// GET /api/rclone/nodes/:nodeId/mounts
	req = httptest.NewRequest(http.MethodGet, "/api/rclone/nodes/node-1/mounts", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var mountsRes struct {
		Success bool                    `json:"success"`
		Mounts  []hostcache.RcloneMount `json:"mounts"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &mountsRes)
	if len(mountsRes.Mounts) != 1 || mountsRes.Mounts[0].MountTarget != "/mnt/minio" {
		t.Fatalf("unexpected mounts: %+v", mountsRes)
	}

	// DELETE /api/rclone/nodes/:nodeId/mounts/:remoteId
	req = httptest.NewRequest(http.MethodDelete, "/api/rclone/nodes/node-1/mounts/rclone-minio", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	mounts, _ := store.GetRcloneMounts("node-1")
	if len(mounts) != 0 {
		t.Fatalf("expected 0 mounts after delete, got %d", len(mounts))
	}

	// DELETE /api/rclone/remotes/:id
	req = httptest.NewRequest(http.MethodDelete, "/api/rclone/remotes/rclone-minio", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	_, found, _ := store.GetRcloneRemote("rclone-minio")
	if found {
		t.Fatalf("remote should be deleted")
	}
}
