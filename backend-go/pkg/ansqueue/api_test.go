package ansqueue

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/hostcache"
)

func newTestAPI(t *testing.T, exec ExecFunc) (*API, *Store, *httptest.Server) {
	t.Helper()
	store := newTestStore(t)
	api, err := NewAPI(APIOptions{
		Store:      store,
		RunOptions: Options{WorkDir: t.TempDir(), Exec: exec},
	})
	if err != nil {
		t.Fatalf("NewAPI: %v", err)
	}
	mux := http.NewServeMux()
	api.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return api, store, srv
}

func doRequest(t *testing.T, srv *httptest.Server, method, path, body string) (*http.Response, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequest(method, srv.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var decoded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	return resp, decoded
}

const validTaskBody = `{
  "title": "Restart nginx",
  "targetNodeId": "node-a",
  "targetNodeName": "a",
  "targetHostRoute": "10.0.0.5",
  "category": "system",
  "primaryModule": "ansible.builtin.systemd_service",
  "steps": [{"name":"Restart nginx","module":"ansible.builtin.systemd_service","parameters":{"name":"nginx","state":"restarted"}}],
  "become": true,
  "status": "staged"
}`

func TestAPIQueueListAndEnqueue(t *testing.T) {
	_, _, srv := newTestAPI(t, successExec())

	resp, body := doRequest(t, srv, http.MethodGet, "/api/queue", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if body["success"] != true {
		t.Fatalf("body = %v", body)
	}
	if tasks, ok := body["tasks"].([]any); !ok || len(tasks) != 0 {
		t.Fatalf("tasks = %#v", body["tasks"])
	}
	if body["isRunning"] != false {
		t.Fatalf("isRunning = %v", body["isRunning"])
	}

	resp, body = doRequest(t, srv, http.MethodPost, "/api/queue/task", validTaskBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["success"] != true {
		t.Fatalf("body = %v", body)
	}
	task, ok := body["task"].(map[string]any)
	if !ok {
		t.Fatalf("task missing: %v", body)
	}
	if task["title"] != "Restart nginx" || task["targetNodeId"] != "node-a" {
		t.Fatalf("task = %v", task)
	}
	if id, _ := task["id"].(string); !taskIDPattern.MatchString(id) {
		t.Fatalf("id = %q", task["id"])
	}
	if task["status"] != "staged" {
		t.Fatalf("status = %v", task["status"])
	}
	if task["become"] != true {
		t.Fatalf("become = %v", task["become"])
	}

	_, body = doRequest(t, srv, http.MethodGet, "/api/queue", "")
	if tasks, _ := body["tasks"].([]any); len(tasks) != 1 {
		t.Fatalf("queue length = %d", len(tasks))
	}
}

func TestAPIEnqueueValidation(t *testing.T) {
	_, _, srv := newTestAPI(t, successExec())

	cases := map[string]string{
		"missing title":   `{"targetNodeId":"n","targetHostRoute":"h","steps":[{"name":"s","module":"m","parameters":{}}]}`,
		"missing node id": `{"title":"t","targetHostRoute":"h","steps":[{"name":"s","module":"m","parameters":{}}]}`,
		"missing route":   `{"title":"t","targetNodeId":"n","steps":[{"name":"s","module":"m","parameters":{}}]}`,
		"empty steps":     `{"title":"t","targetNodeId":"n","targetHostRoute":"h","steps":[]}`,
		"steps absent":    `{"title":"t","targetNodeId":"n","targetHostRoute":"h"}`,
		"not an object":   `[]`,
		"malformed json":  `{`,
		"empty object":    `{}`,
	}

	const want = "Invalid task payload: title, targetNodeId, targetHostRoute, and non-empty steps required"
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			resp, body := doRequest(t, srv, http.MethodPost, "/api/queue/task", payload)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
			if body["success"] != false || body["error"] != want {
				t.Fatalf("body = %v", body)
			}
		})
	}
}

func TestAPIDeleteTaskAndQueue(t *testing.T) {
	_, store, srv := newTestAPI(t, successExec())

	task := minimalTask()
	task.ID = "task-delete"
	if _, err := store.Enqueue(task); err != nil {
		t.Fatal(err)
	}

	resp, body := doRequest(t, srv, http.MethodDelete, "/api/queue/task/missing", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if body["success"] != false || body["error"] != "Task missing not found in queue" {
		t.Fatalf("body = %v", body)
	}

	resp, body = doRequest(t, srv, http.MethodDelete, "/api/queue/task/task-delete", "")
	if resp.StatusCode != http.StatusOK || body["success"] != true {
		t.Fatalf("status = %d body = %v", resp.StatusCode, body)
	}
	if len(store.Snapshot().Tasks) != 0 {
		t.Fatal("task not removed")
	}

	// Clearing keeps running tasks; with none running the queue empties.
	other := minimalTask()
	other.ID = "task-clear"
	if _, err := store.Enqueue(other); err != nil {
		t.Fatal(err)
	}
	resp, body = doRequest(t, srv, http.MethodDelete, "/api/queue", "")
	if resp.StatusCode != http.StatusOK || body["success"] != true {
		t.Fatalf("status = %d body = %v", resp.StatusCode, body)
	}
	if len(store.Snapshot().Tasks) != 0 {
		t.Fatal("queue not cleared")
	}
}

func TestAPIExecuteEnvelope(t *testing.T) {
	_, store, srv := newTestAPI(t, successExec())

	task := minimalTask()
	task.ID = "task-run"
	if _, err := store.Enqueue(task); err != nil {
		t.Fatal(err)
	}

	resp, body := doRequest(t, srv, http.MethodPost, "/api/queue/execute", `{"taskId":"task-run"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["success"] != true {
		t.Fatalf("success = %v", body["success"])
	}
	result, ok := body["result"].(map[string]any)
	if !ok {
		t.Fatalf("result missing: %v", body)
	}
	if result["success"] != true || result["taskId"] != "task-run" || result["failed"] != false {
		t.Fatalf("result = %v", result)
	}
	if _, ok := result["jsonOutput"].(map[string]any); !ok {
		t.Fatalf("jsonOutput not an object: %v", result["jsonOutput"])
	}
	if body["remainingCount"] != float64(0) {
		t.Fatalf("remainingCount = %v", body["remainingCount"])
	}
	if body["haltedOnError"] != false {
		t.Fatalf("haltedOnError = %v", body["haltedOnError"])
	}

	if store.Snapshot().Tasks[0].Status != StatusCompleted {
		t.Fatal("task not marked completed")
	}
}

func TestAPIExecuteHaltEnvelope(t *testing.T) {
	_, store, srv := newTestAPI(t, failureExec())

	first := minimalTask()
	first.ID = "task-fail"
	second := minimalTask()
	second.ID = "task-after"
	if _, err := store.Enqueue(first); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Enqueue(second); err != nil {
		t.Fatal(err)
	}

	resp, body := doRequest(t, srv, http.MethodPost, "/api/queue/execute", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if body["success"] != false {
		t.Fatalf("success = %v", body["success"])
	}
	if body["haltedOnError"] != true {
		t.Fatalf("haltedOnError = %v", body["haltedOnError"])
	}
	if body["remainingCount"] != float64(1) {
		t.Fatalf("remainingCount = %v", body["remainingCount"])
	}
	result := body["result"].(map[string]any)
	if result["failed"] != true || result["error"] != "Assertion failed" {
		t.Fatalf("result = %v", result)
	}
	if store.Snapshot().Tasks[1].Status != StatusStaged {
		t.Fatal("the next task must stay staged after a halt")
	}
}

func TestAPIExecutionBroadcasts(t *testing.T) {
	store := newTestStore(t)
	var events []map[string]any
	api, err := NewAPI(APIOptions{
		Store:      store,
		RunOptions: Options{WorkDir: t.TempDir(), Exec: successExec()},
		Broadcast:  func(e map[string]any) { events = append(events, e) },
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	api.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	task := minimalTask()
	task.ID = "task-broadcast"
	if _, err := store.Enqueue(task); err != nil {
		t.Fatal(err)
	}

	if _, body := doRequest(t, srv, http.MethodPost, "/api/queue/execute", `{"taskId":"task-broadcast"}`); body["success"] != true {
		t.Fatalf("execute failed: %v", body)
	}

	var types []string
	for _, e := range events {
		eventType, _ := e["type"].(string)
		types = append(types, eventType)
		if _, ok := e["queue"].(QueueState); !ok {
			t.Fatalf("event %s carries no queue state: %#v", eventType, e)
		}
	}
	want := []string{"queue_execution_started", "queue_execution_finished"}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", types, want)
	}
}

func TestAPINewAPIRequiresStore(t *testing.T) {
	if _, err := NewAPI(APIOptions{}); err == nil {
		t.Fatal("expected an error for a nil store")
	}
}

func TestAPIWrappedPluginTaskPayloadAndTasksAlias(t *testing.T) {
	_, store, srv := newTestAPI(t, successExec())

	wrappedPayload := map[string]any{
		"nodeId": "likes-it-cold",
		"task": map[string]any{
			"name":     "Create SQL User app_user",
			"category": "database",
			"steps": []map[string]any{
				{
					"name":   "Ensure database user",
					"module": "community.postgresql.postgresql_user",
					"params": map[string]any{"name": "app_user"},
				},
			},
		},
	}
	rawBody, _ := json.Marshal(wrappedPayload)
	resp, body := doRequest(t, srv, http.MethodPost, "/api/queue/tasks", string(rawBody))
	if resp.StatusCode != http.StatusOK || body["success"] != true {
		t.Fatalf("POST /api/queue/tasks failed: status=%d body=%v", resp.StatusCode, body)
	}

	snap := store.Snapshot()
	if len(snap.Tasks) != 1 {
		t.Fatalf("expected 1 queued task, got %d", len(snap.Tasks))
	}
	if snap.Tasks[0].Title != "Create SQL User app_user" {
		t.Errorf("expected title 'Create SQL User app_user', got %q", snap.Tasks[0].Title)
	}
	if snap.Tasks[0].TargetNodeID != "likes-it-cold" {
		t.Errorf("expected targetNodeId 'likes-it-cold', got %q", snap.Tasks[0].TargetNodeID)
	}
}

func TestExecutePlaybookRunTaskAndRecordBackgroundTask(t *testing.T) {
	hostStore, err := hostcache.Open(filepath.Join(t.TempDir(), "hosts.sqlite"))
	if err != nil {
		t.Fatalf("hostcache.Open: %v", err)
	}
	defer func() { _ = hostStore.Close() }()

	qStore := New(hostStore.SQLDB())
	work := t.TempDir()
	pbDir := filepath.Join(work, "ansible", "playbooks")
	if err := os.MkdirAll(pbDir, 0o755); err != nil {
		t.Fatalf("mkdir playbooks: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pbDir, "site.yml"), []byte("---\n- hosts: all\n  tasks: []\n"), 0o644); err != nil {
		t.Fatalf("write site.yml: %v", err)
	}

	var seenArgs []string
	_, err = qStore.Enqueue(Task{
		ID:              "queue-pb-1",
		Title:           "Run playbook site.yml (tag_host)",
		TargetNodeID:    "playbook:site.yml",
		TargetNodeName:  "tag_host",
		TargetHostRoute: "tag_host",
		Category:        "custom",
		PrimaryModule:   "ansible-playbook",
		PlaybookRun: &PlaybookRun{
			Playbook:  "site.yml",
			Target:    "tag_host",
			CheckMode: true,
			Tags:      "setup",
		},
	})
	if err != nil {
		t.Fatalf("Enqueue PlaybookRun: %v", err)
	}

	outcome, err := qStore.ExecuteNextTask(context.Background(), Options{
		WorkDir:   work,
		TaskStore: hostStore,
		Exec: func(ctx context.Context, name string, args []string, opts ExecOptions) (ExecResult, error) {
			seenArgs = append([]string(nil), args...)
			return successExec()(ctx, name, args, opts)
		},
	})
	if err != nil {
		t.Fatalf("ExecuteNextTask: %v", err)
	}
	if outcome.Result == nil || !outcome.Result.Success {
		t.Fatalf("expected successful PlaybookRun execution, got %+v", outcome)
	}
	joinedArgs := strings.Join(seenArgs, " ")
	if !strings.Contains(joinedArgs, "--check") || !strings.Contains(joinedArgs, "--tags setup") || !strings.Contains(joinedArgs, "site.yml") {
		t.Errorf("expected ansible-playbook args to include --check, --tags setup, and site.yml; got %v", seenArgs)
	}

	bgTask, err := hostStore.GetTask("queue-pb-1")
	if err != nil || bgTask == nil {
		t.Fatalf("expected background task recorded in hostcache.tasks: task=%v err=%v", bgTask, err)
	}
	if bgTask.Status != "OK" {
		t.Errorf("expected background task status OK, got %q", bgTask.Status)
	}
}
