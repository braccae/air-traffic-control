package clusterapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"air-traffic-control/backend/pkg/hostcache"
)

func setupTestClusterAPI(t *testing.T) (*API, *hostcache.Store) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hosts-cache.sqlite")
	store, err := hostcache.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open hostcache store: %v", err)
	}

	api, err := NewAPI(Options{
		Store: store,
	})
	if err != nil {
		t.Fatalf("failed to create cluster api: %v", err)
	}
	return api, store
}

func TestClusterLogAndConnections(t *testing.T) {
	api, _ := setupTestClusterAPI(t)
	mux := http.NewServeMux()
	api.Register(mux)

	// Record a cluster log
	api.logStore.Record(LogEntry{
		Kind:    KindApp,
		Level:   "info",
		Source:  "test/coordinator",
		Message: "System startup verified",
	})

	// GET /api/cluster-log
	req := httptest.NewRequest(http.MethodGet, "/api/cluster-log?limit=10", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var logRes struct {
		Success bool       `json:"success"`
		Entries []LogEntry `json:"entries"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &logRes)
	if !logRes.Success || len(logRes.Entries) != 1 || logRes.Entries[0].Message != "System startup verified" {
		t.Fatalf("unexpected cluster log: %+v", logRes)
	}

	// Begin connection
	release := api.connReg.Begin("ssh", "node-1")
	req = httptest.NewRequest(http.MethodGet, "/api/connections", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var connRes struct {
		Success     bool               `json:"success"`
		Connections ConnectionSnapshot `json:"connections"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &connRes)
	if connRes.Connections.Total != 1 || connRes.Connections.ByKind["ssh"] != 1 {
		t.Fatalf("unexpected connections snapshot: %+v", connRes)
	}

	release()
	snapshotAfter := api.connReg.Snapshot()
	if snapshotAfter.Total != 0 || snapshotAfter.ByKind["ssh"] != 0 {
		t.Fatalf("expected 0 connections after release: %+v", snapshotAfter)
	}
}

func TestInventoryChunksAndFolders(t *testing.T) {
	api, store := setupTestClusterAPI(t)
	mux := http.NewServeMux()
	api.Register(mux)

	// POST /api/inventory/chunks
	chunkPayload := `{"tag":"tag:worker","authMethod":"tailscale-ssh","sshUser":"core"}`
	req := httptest.NewRequest(http.MethodPost, "/api/inventory/chunks", strings.NewReader(chunkPayload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on create chunk, got %d: %s", rec.Code, rec.Body.String())
	}

	// GET /api/inventory/chunks
	req = httptest.NewRequest(http.MethodGet, "/api/inventory/chunks", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var chunkRes struct {
		Success bool                  `json:"success"`
		Chunks  []hostcache.TagChunk  `json:"chunks"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &chunkRes)
	if len(chunkRes.Chunks) != 1 || chunkRes.Chunks[0].Tag != "tag:worker" {
		t.Fatalf("unexpected chunks: %+v", chunkRes)
	}

	// DELETE /api/inventory/chunks/:id
	req = httptest.NewRequest(http.MethodDelete, "/api/inventory/chunks/"+chunkRes.Chunks[0].ID, nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on delete chunk, got %d", rec.Code)
	}
	chunksAfter, _ := store.GetAllTagChunks()
	if len(chunksAfter) != 0 {
		t.Fatalf("expected 0 chunks after delete, got %d", len(chunksAfter))
	}

	// POST /api/inventory/folders
	folderPayload := `{"name":"Cold Storage"}`
	req = httptest.NewRequest(http.MethodPost, "/api/inventory/folders", strings.NewReader(folderPayload))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on create folder, got %d", rec.Code)
	}

	// GET /api/inventory/folders
	req = httptest.NewRequest(http.MethodGet, "/api/inventory/folders", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var folderRes struct {
		Success bool                     `json:"success"`
		Folders []hostcache.CustomFolder `json:"folders"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &folderRes)
	if len(folderRes.Folders) != 1 || folderRes.Folders[0].Name != "Cold Storage" {
		t.Fatalf("unexpected folders: %+v", folderRes)
	}
}

func TestTasksAndResticSnapshot(t *testing.T) {
	api, store := setupTestClusterAPI(t)
	mux := http.NewServeMux()
	api.Register(mux)

	// Pre-create task
	_ = store.SaveTask(hostcache.Task{
		ID:          "task-1",
		StartTime:   "2026-10-20 20:00:00",
		NodeTarget:  "node-1",
		User:        "core",
		Description: "system update",
		Progress:    100,
		Status:      "OK",
		Category:    "maintenance",
	})

	// GET /api/tasks
	req := httptest.NewRequest(http.MethodGet, "/api/tasks", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var tasks []hostcache.Task
	_ = json.Unmarshal(rec.Body.Bytes(), &tasks)
	if len(tasks) != 1 || tasks[0].ID != "task-1" {
		t.Fatalf("unexpected tasks: %+v", tasks)
	}

	// POST /api/restic/snapshot
	req = httptest.NewRequest(http.MethodPost, "/api/restic/snapshot", strings.NewReader(`{"host":"likes-it-cold"}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	// Wait briefly for background goroutine in restic snapshot to finish
	time.Sleep(100 * time.Millisecond)
}
