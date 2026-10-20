package ansqueue

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
