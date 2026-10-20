package ansqueue

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "queue.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return store
}

func minimalTask() Task {
	return Task{
		Title:           "Ping node",
		TargetNodeID:    "node-a",
		TargetNodeName:  "a",
		TargetHostRoute: "10.0.0.5",
		Steps:           []Step{{Name: "Ping", Module: "ansible.builtin.ping", Parameters: map[string]any{}}},
	}
}

var taskIDPattern = regexp.MustCompile(`^task-\d+-[0-9a-z]{6}$`)

func TestEnqueueAppliesDefaults(t *testing.T) {
	store := newTestStore(t)

	// Deliberately empty: every field must be default-filled exactly like enqueueTask.
	task, err := store.Enqueue(Task{})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if !taskIDPattern.MatchString(task.ID) {
		t.Fatalf("id %q does not match task-<ms>-<6 base36>", task.ID)
	}
	if task.Title != "Ansible Task" {
		t.Fatalf("title = %q", task.Title)
	}
	if task.TargetNodeID != "localhost" || task.TargetNodeName != "localhost" {
		t.Fatalf("node defaults wrong: %+v", task)
	}
	if task.TargetHostRoute != "localhost" {
		t.Fatalf("route = %q", task.TargetHostRoute)
	}
	if task.TargetSSHUser != "core" || task.TargetSSHPort != 22 {
		t.Fatalf("ssh defaults wrong: %q/%d", task.TargetSSHUser, task.TargetSSHPort)
	}
	if task.Category != "system" || task.PrimaryModule != "ansible.builtin.command" {
		t.Fatalf("category/module defaults wrong: %q/%q", task.Category, task.PrimaryModule)
	}
	if !task.BecomeEnabled() {
		t.Fatal("become must default to true")
	}
	if task.Status != StatusStaged || task.CreatedAt == 0 {
		t.Fatalf("status/createdAt wrong: %+v", task)
	}

	snap := store.Snapshot()
	if len(snap.Tasks) != 1 || snap.IsRunning {
		t.Fatalf("snapshot wrong: %+v", snap)
	}
}

func TestEnqueuePreservesExplicitFields(t *testing.T) {
	store := newTestStore(t)

	task := minimalTask()
	task.ID = "task-explicit"
	task.Become = BoolPtr(false)
	task.Category = "container"
	task.PrimaryModule = "ansible.builtin.ping"
	task.CreatedAt = 111

	got, err := store.Enqueue(task)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "task-explicit" || got.BecomeEnabled() || got.Category != "container" || got.BecomeMethod != "run0" {
		t.Fatalf("explicit fields not preserved: %+v", got)
	}
	if got.PrimaryModule != "ansible.builtin.ping" {
		t.Fatalf("primaryModule = %q", got.PrimaryModule)
	}
}

func TestSnapshotOrderingAndUpsert(t *testing.T) {
	store := newTestStore(t)

	first := minimalTask()
	first.ID = "task-b"
	first.CreatedAt = 200
	second := minimalTask()
	second.ID = "task-a"
	second.CreatedAt = 100

	if _, err := store.Enqueue(first); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Enqueue(second); err != nil {
		t.Fatal(err)
	}

	snap := store.Snapshot()
	if len(snap.Tasks) != 2 || snap.Tasks[0].ID != "task-b" || snap.Tasks[1].ID != "task-a" {
		t.Fatalf("snapshot ordering is insertion order, got %+v", snap.Tasks)
	}

	// Upsert by id must replace, not duplicate.
	update := first
	update.Title = "Renamed"
	if _, err := store.Enqueue(update); err != nil {
		t.Fatal(err)
	}
	snap = store.Snapshot()
	if len(snap.Tasks) != 2 {
		t.Fatalf("upsert duplicated the task: %d rows", len(snap.Tasks))
	}
	if snap.Tasks[0].Title != "Renamed" {
		t.Fatalf("upsert did not update title: %+v", snap.Tasks[0])
	}

}

func TestReloadOrdersByCreatedAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	late := minimalTask()
	late.ID = "task-late"
	late.CreatedAt = 200
	early := minimalTask()
	early.ID = "task-early"
	early.CreatedAt = 100

	// Insert out of order: the DB load must sort by created_at ASC.
	if _, err := store.Enqueue(late); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Enqueue(early); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	snap := reopened.Snapshot()
	if len(snap.Tasks) != 2 || snap.Tasks[0].ID != "task-early" || snap.Tasks[1].ID != "task-late" {
		t.Fatalf("reload order = %+v, want created_at ASC", snap.Tasks)
	}
}

func TestRemove(t *testing.T) {
	store := newTestStore(t)

	task := minimalTask()
	task.ID = "task-remove"
	if _, err := store.Enqueue(task); err != nil {
		t.Fatal(err)
	}

	removed, err := store.Remove("task-remove")
	if err != nil || !removed {
		t.Fatalf("Remove = %v, %v", removed, err)
	}
	removed, err = store.Remove("task-remove")
	if err != nil || removed {
		t.Fatalf("second Remove = %v, %v (want false, nil)", removed, err)
	}
	if len(store.Snapshot().Tasks) != 0 {
		t.Fatal("removed task still in memory")
	}
}

func TestClearKeepsRunning(t *testing.T) {
	store := newTestStore(t)

	staged := minimalTask()
	staged.ID = "task-staged"
	running := minimalTask()
	running.ID = "task-running"

	if _, err := store.Enqueue(staged); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Enqueue(running); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.UpdateStatus("task-running", StatusRunning, nil); err != nil || !ok {
		t.Fatalf("UpdateStatus: ok=%v err=%v", ok, err)
	}

	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	snap := store.Snapshot()
	if len(snap.Tasks) != 1 || snap.Tasks[0].ID != "task-running" {
		t.Fatalf("Clear did not keep the running task: %+v", snap.Tasks)
	}
}

func TestCrashRecoveryMarksRunningFailed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	task := minimalTask()
	task.ID = "task-interrupted"
	if _, err := store.Enqueue(task); err != nil {
		t.Fatal(err)
	}
	// Simulate a hard kill mid-run: only the DB row says running.
	if _, err := store.DB().Exec(`UPDATE ansible_queue_tasks SET status = 'running' WHERE id = ?`, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	snap := reopened.Snapshot()
	if len(snap.Tasks) != 1 {
		t.Fatalf("expected 1 recovered task, got %d", len(snap.Tasks))
	}
	got := snap.Tasks[0]
	if got.Status != StatusFailed {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	if got.Error != "Task execution interrupted by server restart" {
		t.Fatalf("error = %q", got.Error)
	}
	if snap.IsRunning {
		t.Fatal("isRunning must be false after recovery")
	}
}

func TestTaskJSONRoundTripsCamelCase(t *testing.T) {
	store := newTestStore(t)

	task := minimalTask()
	task.ID = "task-json"
	task.Connection = &Connection{Plugin: "connection/ssh", Host: "10.0.0.5", Port: 2222, User: "deploy"}
	task.SecretsMasked = map[string]string{"smb_password": MaskedSecretValue("pw")}
	if _, err := store.Enqueue(task); err != nil {
		t.Fatal(err)
	}

	var blob string
	if err := store.DB().QueryRow(`SELECT task_json FROM ansible_queue_tasks WHERE id = ?`, task.ID).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		`"id"`, `"title"`, `"targetNodeId"`, `"targetNodeName"`, `"targetHostRoute"`,
		`"targetSshUser"`, `"targetSshPort"`, `"connection"`, `"category"`,
		`"primaryModule"`, `"steps"`, `"secretsMasked"`, `"become"`, `"becomeMethod"`,
		`"status"`, `"createdAt"`,
	} {
		if !strings.Contains(blob, key) {
			t.Fatalf("task_json missing key %s: %s", key, blob)
		}
	}
	for _, key := range []string{"TargetNodeID", "PrimaryModule", "CreatedAt"} {
		if strings.Contains(blob, key) {
			t.Fatalf("task_json leaked Go field name %s: %s", key, blob)
		}
	}

	var decoded Task
	if err := json.Unmarshal([]byte(blob), &decoded); err != nil {
		t.Fatalf("task_json is not decodable: %v", err)
	}
	if decoded.TargetNodeID != "node-a" || decoded.Connection == nil || decoded.Connection.Port != 2222 {
		t.Fatalf("round-trip lost data: %+v", decoded)
	}
}

func TestEnqueueRedactsSecretsInError(t *testing.T) {
	store := newTestStore(t)

	task := minimalTask()
	task.ID = "task-secret"
	task.Error = `connect failed: password="hunter2"`
	if _, err := store.Enqueue(task); err != nil {
		t.Fatal(err)
	}

	var stored sql.NullString
	if err := store.DB().QueryRow(`SELECT error FROM ansible_queue_tasks WHERE id = ?`, task.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored.String, "hunter2") {
		t.Fatalf("plaintext secret persisted: %q", stored.String)
	}
	var blob string
	if err := store.DB().QueryRow(`SELECT task_json FROM ansible_queue_tasks WHERE id = ?`, task.ID).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(blob, "hunter2") {
		t.Fatalf("plaintext secret leaked into task_json: %s", blob)
	}
}

func TestUpdateStatusStampsExecutedAt(t *testing.T) {
	store := newTestStore(t)

	task := minimalTask()
	task.ID = "task-status"
	if _, err := store.Enqueue(task); err != nil {
		t.Fatal(err)
	}

	msg := "boom"
	updated, ok, err := store.UpdateStatus("task-status", StatusFailed, &msg)
	if err != nil || !ok {
		t.Fatalf("UpdateStatus: ok=%v err=%v", ok, err)
	}
	if updated.Status != StatusFailed || updated.Error != "boom" || updated.ExecutedAt == 0 {
		t.Fatalf("status update wrong: %+v", updated)
	}

	var blob string
	if err := store.DB().QueryRow(`SELECT task_json FROM ansible_queue_tasks WHERE id = ?`, task.ID).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	var persisted Task
	if err := json.Unmarshal([]byte(blob), &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.ExecutedAt != updated.ExecutedAt || persisted.Status != StatusFailed {
		t.Fatalf("executedAt/status not persisted: %+v", persisted)
	}

	if _, ok, err := store.UpdateStatus("missing", StatusFailed, nil); err != nil || ok {
		t.Fatalf("UpdateStatus on unknown id: ok=%v err=%v", ok, err)
	}
}
