package ansqueue

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The ansible_queue_tasks table is shared with server.ts during the migration.
// A task the TS coordinator staged carries fields this package does not model
// (e.g. `playbookRun`); a Go read/write cycle must never drop them.
func TestTaskJSONPreservesUnmodelledFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	blob := `{
		"id": "queue-playbook-1-abc",
		"title": "Run playbook visual-check.yml (all)",
		"targetNodeId": "playbook:visual-check.yml",
		"targetNodeName": "all",
		"targetHostRoute": "all",
		"category": "custom",
		"primaryModule": "ansible-playbook",
		"steps": [],
		"playbookRun": {"playbook": "visual-check.yml", "target": "all", "checkMode": false},
		"become": false,
		"status": "completed",
		"createdAt": 1792505948849
	}`
	if _, err := store.DB().Exec(`
		INSERT INTO ansible_queue_tasks (id, title, target_node_id, target_node_name,
			target_host_route, target_ssh_user, target_ssh_port, category, primary_module,
			become, become_method, status, created_at, task_json, error, updated_at)
		VALUES ('queue-playbook-1-abc', 'Run playbook visual-check.yml (all)',
			'playbook:visual-check.yml', 'all', 'all', 'core', 22, 'custom',
			'ansible-playbook', 0, 'run0', 'completed', 1792505948849, ?, NULL, ?)`,
		blob, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	snap := reopened.Snapshot()
	if len(snap.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(snap.Tasks))
	}
	if len(snap.Tasks[0].Extra) == 0 {
		t.Fatalf("unknown field playbookRun was not preserved: %#v", snap.Tasks[0])
	}

	// A status transition rewrites task_json; the unmodelled key must survive.
	if _, _, err := reopened.UpdateStatus("queue-playbook-1-abc", StatusFailed, nil); err != nil {
		t.Fatal(err)
	}
	var written string
	if err := reopened.DB().QueryRow(`SELECT task_json FROM ansible_queue_tasks WHERE id = ?`,
		"queue-playbook-1-abc").Scan(&written); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(written, `"playbookRun"`) {
		t.Fatalf("task_json lost playbookRun after a status update: %s", written)
	}
	if !strings.Contains(written, `"visual-check.yml"`) {
		t.Fatalf("task_json lost the playbookRun payload: %s", written)
	}

	// And the HTTP/API encoding path keeps it too.
	encoded, err := MarshalTaskJSON(snap.Tasks[0])
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["playbookRun"]; !ok {
		t.Fatalf("marshalled task missing playbookRun: %s", encoded)
	}
}
