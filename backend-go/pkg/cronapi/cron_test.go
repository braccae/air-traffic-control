package cronapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"air-traffic-control/backend/pkg/ansqueue"
	"air-traffic-control/backend/pkg/hostcache"
)

func setupTestCronAPI(t *testing.T) (*API, *hostcache.Store, *Scheduler) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hosts-cache.sqlite")
	store, err := hostcache.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open hostcache store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	qStore := ansqueue.New(store.SQLDB())

	sched := NewScheduler(SchedulerOptions{
		Store:        store,
		QueueStore:   qStore,
		TickInterval: 50 * time.Millisecond,
	})

	api, err := NewAPI(APIOptions{
		Store:     store,
		Scheduler: sched,
	})
	if err != nil {
		t.Fatalf("failed to create cron api: %v", err)
	}
	return api, store, sched
}

func TestCronExpressionParsingAndNextRun(t *testing.T) {
	// Standard 5-field
	parsed, err := ParseCronExpression("*/5 2 * * 1-5")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(parsed.Minutes) != 12 || len(parsed.Hours) != 1 || parsed.Hours[0] != 2 {
		t.Fatalf("unexpected parsed expression fields: %+v", parsed)
	}

	// Macro
	parsedMacro, err := ParseCronExpression("@daily")
	if err != nil {
		t.Fatalf("macro parse error: %v", err)
	}
	if parsedMacro.Expression != "0 0 * * *" {
		t.Fatalf("unexpected macro expression: %s", parsedMacro.Expression)
	}

	// Next run calculation
	now := time.Now().UnixMilli()
	next := NextCronRun(parsed, now)
	if next == nil || *next <= now {
		t.Fatalf("expected next run in future, got %v", next)
	}
}

func TestCronAPI_CRUDAndPreview(t *testing.T) {
	api, store, sched := setupTestCronAPI(t)
	mux := http.NewServeMux()
	api.Register(mux)

	// GET /api/cron/preview
	req := httptest.NewRequest(http.MethodGet, "/api/cron/preview?expression=0+*+*+*+*&count=3", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from preview, got %d", rec.Code)
	}
	var prevRes struct {
		Success    bool    `json:"success"`
		Expression string  `json:"expression"`
		Runs       []int64 `json:"runs"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &prevRes)
	if len(prevRes.Runs) != 3 {
		t.Fatalf("expected 3 preview runs, got %d", len(prevRes.Runs))
	}

	// POST /api/cron/schedules
	createPayload := `{
		"name": "Daily Cleanup",
		"cronExpression": "0 0 * * *",
		"enabled": true,
		"scope": {"kind": "host", "target": "node-1"},
		"taskKind": "ansible_steps",
		"payload": {"title": "Cleanup", "steps": []}
	}`
	req = httptest.NewRequest(http.MethodPost, "/api/cron/schedules", strings.NewReader(createPayload))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from create, got %d: %s", rec.Code, rec.Body.String())
	}
	var createRes struct {
		Success  bool                   `json:"success"`
		Schedule hostcache.CronSchedule `json:"schedule"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &createRes)
	if createRes.Schedule.Name != "Daily Cleanup" || createRes.Schedule.NextRunAt == nil {
		t.Fatalf("unexpected created schedule: %+v", createRes)
	}

	schedID := createRes.Schedule.ID

	// GET /api/cron/schedules
	req = httptest.NewRequest(http.MethodGet, "/api/cron/schedules", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// GET /api/cron/schedules/:id
	req = httptest.NewRequest(http.MethodGet, "/api/cron/schedules/"+schedID, nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// POST /api/cron/schedules/:id/run
	req = httptest.NewRequest(http.MethodPost, "/api/cron/schedules/"+schedID+"/run", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on run, got %d: %s", rec.Code, rec.Body.String())
	}
	var runRes struct {
		Success bool `json:"success"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &runRes)
	if !runRes.Success {
		t.Fatalf("expected run success: true")
	}

	// Scheduler lifecycle
	sched.Start()
	if !sched.IsRunning() {
		t.Fatalf("expected scheduler to be running")
	}
	sched.Stop()
	if sched.IsRunning() {
		t.Fatalf("expected scheduler to be stopped")
	}

	// DELETE /api/cron/schedules/:id
	req = httptest.NewRequest(http.MethodDelete, "/api/cron/schedules/"+schedID, nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on delete, got %d", rec.Code)
	}

	_, found, _ := store.GetCronSchedule(schedID)
	if found {
		t.Fatalf("expected schedule to be deleted")
	}
}
