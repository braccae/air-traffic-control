package ansqueue

import (
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// queueSchemaDDL is the on-disk schema of ansible_queue_tasks, copied verbatim
// (column order included) from initQueueStore in ansibleQueueRunner.ts and the
// live data/hosts-cache.sqlite. hostcache/schema.go carries the same text so a
// fresh install creates the table before either side opens it.
const queueSchemaDDL = `
CREATE TABLE IF NOT EXISTS ansible_queue_tasks (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  target_node_id TEXT NOT NULL,
  target_node_name TEXT NOT NULL,
  target_host_route TEXT NOT NULL,
  target_ssh_user TEXT NOT NULL,
  target_ssh_port INTEGER NOT NULL,
  category TEXT NOT NULL,
  primary_module TEXT NOT NULL,
  become INTEGER NOT NULL DEFAULT 1,
  become_method TEXT NOT NULL DEFAULT 'run0',
  status TEXT NOT NULL DEFAULT 'staged',
  created_at INTEGER NOT NULL,
  task_json TEXT NOT NULL,
  error TEXT,
  updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_ansible_queue_status ON ansible_queue_tasks(status, created_at ASC);
`

// Store is the SQLite-backed queue stage plus its in-memory mirror. It is safe
// for concurrent use. The mutex is never held across an ansible-playbook run:
// the DB handle is a single-connection pool shared with the rest of the cache,
// so a long playbook under lock would starve every other reader.
type Store struct {
	db      *sql.DB
	initErr error

	mu        sync.Mutex
	tasks     []Task
	isRunning bool
}

// Open opens (creating if needed) the SQLite database at dbPath, applies the
// queue schema, recovers tasks interrupted by a crash and loads the queue.
func Open(dbPath string) (*Store, error) {
	if strings.TrimSpace(dbPath) == "" {
		return nil, errors.New("ansqueue: empty database path")
	}
	if dbPath != ":memory:" {
		if dir := filepath.Dir(dbPath); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("ansqueue: create directory for %s: %w", dbPath, err)
			}
		}
	}

	dsn := "file:" + dbPath + "?mode=rwc"
	if dbPath == ":memory:" {
		dsn = "file::memory:?mode=memory&cache=shared"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("ansqueue: open %s: %w", dbPath, err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	for _, pragma := range []string{
		"PRAGMA busy_timeout = 5000;",
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = NORMAL;",
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("ansqueue: %s: %w", strings.TrimSuffix(pragma, ";"), err)
		}
	}

	s := &Store{db: db}
	if err := s.initialize(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// New binds a Store to an already-open, already-migrated *sql.DB (typically
// hostcache.Store.SQLDB()). It still applies the queue DDL, recovers running
// rows and loads the mirror; a failure there is recorded and surfaced by
// Err()/every method, because New cannot return an error.
func New(db *sql.DB) *Store {
	s := &Store{db: db}
	if db == nil {
		s.initErr = errors.New("ansqueue: nil database handle")
		return s
	}
	if err := s.initialize(); err != nil {
		s.initErr = err
	}
	return s
}

// Err returns the initialization error recorded by New, if any.
func (s *Store) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.initErr
}

// DB exposes the underlying handle for callers that need it (tests, tooling).
// The handle is shared: do not Close it here.
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) initialize() error {
	if s.db == nil {
		return errors.New("ansqueue: nil database handle")
	}
	if _, err := s.db.Exec(queueSchemaDDL); err != nil {
		return fmt.Errorf("ansqueue: apply schema: %w", err)
	}

	// Crash recovery: a task left `running` can only mean the daemon died mid-run.
	if _, err := s.db.Exec(
		`UPDATE ansible_queue_tasks
		    SET status = 'failed',
		        error = 'Task execution interrupted by server restart',
		        updated_at = ?
		  WHERE status = 'running'`, time.Now().UnixMilli(),
	); err != nil {
		return fmt.Errorf("ansqueue: recover running tasks: %w", err)
	}

	rows, err := s.db.Query(`SELECT task_json, status, error FROM ansible_queue_tasks ORDER BY created_at ASC`)
	if err != nil {
		return fmt.Errorf("ansqueue: load queue: %w", err)
	}
	defer rows.Close()

	loaded := make([]Task, 0)
	for rows.Next() {
		var (
			blob   string
			status string
			errMsg sql.NullString
		)
		if err := rows.Scan(&blob, &status, &errMsg); err != nil {
			return fmt.Errorf("ansqueue: scan queue row: %w", err)
		}
		var task Task
		if err := json.Unmarshal([]byte(blob), &task); err != nil {
			// A corrupt row must not silently vanish from the queue: fail loudly.
			return fmt.Errorf("ansqueue: decode task_json: %w", err)
		}
		task.Status = Status(status)
		if errMsg.Valid && errMsg.String != "" {
			task.Error = errMsg.String
		}
		loaded = append(loaded, task)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("ansqueue: iterate queue rows: %w", err)
	}

	s.mu.Lock()
	s.tasks = loaded
	s.isRunning = false
	s.mu.Unlock()
	return nil
}

// Close releases the database handle. It is a no-op for a Store built with New
// over a shared handle only if the caller never calls it; callers that do not
// own the handle must not call Close.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Snapshot returns the current queue mirror and run flag.
func (s *Store) Snapshot() QueueState {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Task, len(s.tasks))
	copy(out, s.tasks)
	return QueueState{Tasks: out, IsRunning: s.isRunning}
}

// Enqueue normalises and persists a task (UPSERT by id), then mirrors it in
// memory. Default filling matches the TS enqueueTask exactly.
func (s *Store) Enqueue(task Task) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initErr != nil {
		return Task{}, s.initErr
	}

	t := task
	if t.ID == "" {
		t.ID = newTaskID()
	}
	if t.Title == "" {
		t.Title = "Ansible Task"
	}
	if t.TargetNodeID == "" {
		t.TargetNodeID = "localhost"
	}
	if t.TargetNodeName == "" {
		t.TargetNodeName = firstNonEmpty(t.TargetNodeID, "localhost")
	}
	if t.TargetHostRoute == "" {
		t.TargetHostRoute = firstNonEmpty(t.TargetNodeName, t.TargetNodeID, "127.0.0.1")
	}
	if t.TargetSSHUser == "" {
		t.TargetSSHUser = "core"
	}
	if t.TargetSSHPort == 0 {
		t.TargetSSHPort = 22
	}
	if t.Category == "" {
		t.Category = "system"
	}
	if t.PrimaryModule == "" {
		if len(t.Steps) > 0 {
			t.PrimaryModule = t.Steps[0].Module
		}
		if t.PrimaryModule == "" {
			t.PrimaryModule = "ansible.builtin.command"
		}
	}
	if t.Steps == nil {
		t.Steps = []Step{}
	}
	if t.Become == nil {
		t.Become = BoolPtr(true)
	}
	if t.BecomeMethod == "" {
		t.BecomeMethod = "run0"
	}
	if t.Status == "" {
		t.Status = StatusStaged
	}
	if t.CreatedAt == 0 {
		t.CreatedAt = time.Now().UnixMilli()
	}
	t.Error = RedactSecrets(t.Error)

	blob, err := MarshalTaskJSON(t)
	if err != nil {
		return Task{}, fmt.Errorf("ansqueue: encode task: %w", err)
	}

	// Persist first: the mirror must never claim a task the database rejected.
	if _, err := s.db.Exec(`
		INSERT INTO ansible_queue_tasks (
			id, title, target_node_id, target_node_name, target_host_route,
			target_ssh_user, target_ssh_port, category, primary_module,
			become, become_method, status, created_at, task_json, error, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			title = excluded.title,
			target_node_id = excluded.target_node_id,
			target_node_name = excluded.target_node_name,
			target_host_route = excluded.target_host_route,
			target_ssh_user = excluded.target_ssh_user,
			target_ssh_port = excluded.target_ssh_port,
			category = excluded.category,
			primary_module = excluded.primary_module,
			become = excluded.become,
			become_method = excluded.become_method,
			status = excluded.status,
			task_json = excluded.task_json,
			error = excluded.error,
			updated_at = excluded.updated_at`,
		t.ID, t.Title, t.TargetNodeID, t.TargetNodeName, t.TargetHostRoute,
		t.TargetSSHUser, t.TargetSSHPort, t.Category, t.PrimaryModule,
		boolToInt(t.BecomeEnabled()), t.BecomeMethod, string(t.Status), t.CreatedAt,
		string(blob), nullableString(t.Error), time.Now().UnixMilli(),
	); err != nil {
		return Task{}, fmt.Errorf("ansqueue: persist task %s: %w", t.ID, err)
	}

	replaced := false
	for i := range s.tasks {
		if s.tasks[i].ID == t.ID {
			s.tasks[i] = t
			replaced = true
			break
		}
	}
	if !replaced {
		s.tasks = append(s.tasks, t)
	}

	return t, nil
}

// Remove deletes a task by id from memory and SQLite. It reports false when the
// id was never queued (matching the TS removeTask).
func (s *Store) Remove(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initErr != nil {
		return false, s.initErr
	}

	idx := -1
	for i := range s.tasks {
		if s.tasks[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false, nil
	}

	if _, err := s.db.Exec(`DELETE FROM ansible_queue_tasks WHERE id = ?`, id); err != nil {
		return false, fmt.Errorf("ansqueue: delete task %s: %w", id, err)
	}
	s.tasks = append(s.tasks[:idx], s.tasks[idx+1:]...)
	return true, nil
}

// Clear removes every non-running task from memory and SQLite.
func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initErr != nil {
		return s.initErr
	}

	if _, err := s.db.Exec(`DELETE FROM ansible_queue_tasks WHERE status != ?`, string(StatusRunning)); err != nil {
		return fmt.Errorf("ansqueue: clear queue: %w", err)
	}

	kept := make([]Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		if t.Status == StatusRunning {
			kept = append(kept, t)
		}
	}
	s.tasks = kept
	return nil
}

// UpdateStatus sets a task's status (and optionally its error message), stamps
// executedAt, and persists both the columns and task_json. The bool reports
// whether the task existed.
func (s *Store) UpdateStatus(id string, status Status, errMsg *string) (Task, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initErr != nil {
		return Task{}, false, s.initErr
	}

	idx := -1
	for i := range s.tasks {
		if s.tasks[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Task{}, false, nil
	}

	task := s.tasks[idx]
	task.Status = status
	if errMsg != nil {
		task.Error = RedactSecrets(*errMsg)
	}
	task.ExecutedAt = time.Now().UnixMilli()

	blob, err := MarshalTaskJSON(task)
	if err != nil {
		return Task{}, true, fmt.Errorf("ansqueue: encode task: %w", err)
	}
	if _, err := s.db.Exec(`
		UPDATE ansible_queue_tasks
		   SET status = ?, error = ?, task_json = ?, updated_at = ?
		 WHERE id = ?`,
		string(status), nullableString(task.Error), string(blob), time.Now().UnixMilli(), id,
	); err != nil {
		return Task{}, true, fmt.Errorf("ansqueue: update task %s: %w", id, err)
	}
	s.tasks[idx] = task
	return task, true, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func (s *Store) stagedCountLocked() int {
	n := 0
	for _, t := range s.tasks {
		if t.Status == StatusStaged {
			n++
		}
	}
	return n
}

// StagedCount returns the number of staged tasks.
func (s *Store) StagedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stagedCountLocked()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

var randMu sync.Mutex

// nextRandomUint64 draws from crypto/rand. The queue only needs a
// process-unique suffix, but a real CSPRNG costs nothing here and avoids two
// tasks colliding inside the same millisecond.
func nextRandomUint64() uint64 {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		randMu.Lock()
		defer randMu.Unlock()
		return uint64(time.Now().UnixNano())
	}
	return binary.BigEndian.Uint64(buf[:])
}
