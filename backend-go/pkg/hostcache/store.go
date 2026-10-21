package hostcache

import (
	"database/sql"
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

// Store is the Go cache/store for data/hosts-cache.sqlite. It is safe for
// concurrent use by multiple goroutines: the underlying *sql.DB is guarded and
// every multi-statement mutation runs in a transaction.
//
// Open() opens read-write-create (this side is the write owner and creates the
// WAL sidecars); OpenReadOnly() opens the same file for readers only. The daemon
// is multi-goroutine, so the handle is pooled and busy-timeout is set so a
// cross-process writer (the TS coordinator) never gets an immediate SQLITE_BUSY.
type Store struct {
	path     string
	db       *sql.DB
	readOnly bool
	mu       sync.Mutex // guards composite read-modify-write sequences
}

// Open opens (creating if needed) the cache database read-write. It applies the
// schema, recovers any running background tasks interrupted by a server restart,
// and returns a Store. This is the write owner: it must run before any
// read-only consumer so the WAL sidecar files exist.
func Open(path string) (*Store, error) { return openStore(path, false) }

// OpenReadOnly opens an existing cache database for reads only. It does not
// create the file or the WAL sidecars, so Open() (or the TS coordinator) must
// have created them first.
func OpenReadOnly(path string) (*Store, error) { return openStore(path, true) }

func openStore(path string, readOnly bool) (*Store, error) {
	if path == "" {
		return nil, errors.New("hostcache: empty database path")
	}
	if !readOnly && path != ":memory:" {
		if dir := filepath.Dir(path); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("hostcache: create directory for %s: %w", path, err)
			}
		}
	}

	var dsn string
	if path == ":memory:" {
		dsn = "file::memory:?mode=memory&cache=shared"
	} else {
		dsn = "file:" + path
		if readOnly {
			dsn += "?mode=ro"
		} else {
			dsn += "?mode=rwc"
		}
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("hostcache: open %s: %w", path, err)
	}
	// A single connection keeps the per-connection PRAGMAs below in effect and
	// removes intra-process lock contention; WAL keeps cross-process readers happy.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	pragmas := []string{"PRAGMA busy_timeout = 5000;", "PRAGMA foreign_keys = ON;"}
	if !readOnly {
		pragmas = append([]string{"PRAGMA journal_mode = WAL;", "PRAGMA synchronous = NORMAL;"}, pragmas...)
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("hostcache: %s: %w", strings.TrimSuffix(p, ";"), err)
		}
	}

	if !readOnly {
		if _, err := db.Exec(schemaDDL); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("hostcache: apply schema: %w", err)
		}
		for _, stmt := range schemaEvolve {
			if _, err := db.Exec(stmt); err != nil && !isDuplicateColumn(err) {
				_ = db.Close()
				return nil, fmt.Errorf("hostcache: evolve schema: %w", err)
			}
		}
		// Crash recovery: any background task left in RUNNING or QUEUED state
		// when the daemon starts up was interrupted by a process exit/restart.
		if _, err := db.Exec(`
			UPDATE tasks
			   SET status = 'FAILED',
			       progress = 100,
			       progress_eta = 'Task execution interrupted by server restart',
			       updated_at = ?
			 WHERE status IN ('RUNNING', 'QUEUED')`, nowMS()); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("hostcache: recover running tasks: %w", err)
		}
	}

	return &Store{path: path, db: db, readOnly: readOnly}, nil
}

func isDuplicateColumn(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "duplicate column")
}

// Path returns the database path this store was opened with.
func (s *Store) Path() string { return s.path }

// Close releases the underlying database handle.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

// ---------------------------------------------------------------------------
// hosts
// ---------------------------------------------------------------------------

// SeedHostsIfEmpty inserts initialHosts (assigning sort_order by position) only
// when the hosts table is empty, then returns all hosts. Mirrors the TS seed.
func (s *Store) SeedHostsIfEmpty(initialHosts []NodeRecord) ([]NodeRecord, error) {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM hosts`).Scan(&count); err != nil {
		return nil, err
	}
	if count == 0 {
		for i, host := range initialHosts {
			if err := s.upsertHost(host, i, true); err != nil {
				return nil, err
			}
		}
	}
	return s.GetAllHosts()
}

// GetAllHosts returns every host ordered by sort_order then id.
func (s *Store) GetAllHosts() ([]NodeRecord, error) {
	rows, err := s.db.Query(`SELECT node_json, sort_order, updated_at FROM hosts ORDER BY sort_order ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]NodeRecord, 0)
	for rows.Next() {
		var blob string
		var order int
		var updated int64
		if err := rows.Scan(&blob, &order, &updated); err != nil {
			return nil, err
		}
		var rec NodeRecord
		if err := json.Unmarshal([]byte(blob), &rec); err != nil {
			return nil, fmt.Errorf("hostcache: decode node_json: %w", err)
		}
		rec.NodeJSON = json.RawMessage(blob)
		rec.SortOrder = order
		rec.UpdatedAt = updated
		out = append(out, rec)
	}
	return out, rows.Err()
}

// GetHostByID returns the host with the given id and whether it was found.
func (s *Store) GetHostByID(id string) (NodeRecord, bool, error) {
	var blob string
	var order int
	var updated int64
	err := s.db.QueryRow(`SELECT node_json, sort_order, updated_at FROM hosts WHERE id = ?`, id).
		Scan(&blob, &order, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return NodeRecord{}, false, nil
	}
	if err != nil {
		return NodeRecord{}, false, err
	}
	var rec NodeRecord
	if err := json.Unmarshal([]byte(blob), &rec); err != nil {
		return NodeRecord{}, false, fmt.Errorf("hostcache: decode node_json: %w", err)
	}
	rec.NodeJSON = json.RawMessage(blob)
	rec.SortOrder = order
	rec.UpdatedAt = updated
	return rec, true, nil
}

// UpsertHost inserts or updates a single host. Like the TS implementation it
// preserves the existing sort_order (defaulting to 999 for a new host).
func (s *Store) UpsertHost(rec NodeRecord) error { return s.upsertHost(rec, 0, false) }

// UpsertHosts inserts or updates many hosts, assigning sort_order by position.
func (s *Store) UpsertHosts(nodes []NodeRecord) error {
	for i, n := range nodes {
		if err := s.upsertHost(n, i, true); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) upsertHost(rec NodeRecord, order int, setOrder bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := upsertHostTx(tx, rec, order, setOrder); err != nil {
		return err
	}
	return tx.Commit()
}

func upsertHostTx(tx *sql.Tx, rec NodeRecord, order int, setOrder bool) error {
	nodeJSON := rec.NodeJSON
	col := rec
	if len(nodeJSON) > 0 {
		var fromBlob NodeRecord
		if err := json.Unmarshal(nodeJSON, &fromBlob); err == nil {
			col = fromBlob
		}
	} else {
		b, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		nodeJSON = b
	}

	port := col.Port
	if port == 0 {
		port = 22
	}
	sshUser := col.SSHUser
	if sshUser == "" {
		sshUser = "core"
	}

	finalOrder := order
	if !setOrder {
		var existing sql.NullInt64
		if err := tx.QueryRow(`SELECT sort_order FROM hosts WHERE id = ?`, col.ID).Scan(&existing); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if existing.Valid {
			finalOrder = int(existing.Int64)
		} else {
			finalOrder = 999
		}
	}

	_, err := tx.Exec(`
		INSERT INTO hosts (
			id, name, short_name, group_name, ip, port, ssh_user,
			tailscale_ip, tailscale_host, status, sort_order, node_json, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			short_name = excluded.short_name,
			group_name = excluded.group_name,
			ip = excluded.ip,
			port = excluded.port,
			ssh_user = excluded.ssh_user,
			tailscale_ip = excluded.tailscale_ip,
			tailscale_host = excluded.tailscale_host,
			status = excluded.status,
			node_json = excluded.node_json,
			updated_at = excluded.updated_at`,
		col.ID, col.Name, col.ShortName, col.Group, col.IP, port, sshUser,
		col.TailscaleIP, col.TailscaleHost, col.Status, finalOrder, string(nodeJSON), nowMS(),
	)
	return err
}

// DeleteHost removes a host and its telemetry/container/section rows. It returns
// whether a host row was actually deleted.
func (s *Store) DeleteHost(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range []string{
		`DELETE FROM host_telemetry_cache WHERE host_id = ?`,
		`DELETE FROM host_containers_cache WHERE host_id = ?`,
		`DELETE FROM host_section_cache WHERE host_id = ?`,
	} {
		if _, err := tx.Exec(stmt, id); err != nil {
			return false, err
		}
	}
	res, err := tx.Exec(`DELETE FROM hosts WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ---------------------------------------------------------------------------
// telemetry / containers / sections
// ---------------------------------------------------------------------------

// UpsertHostTelemetry stores the opaque telemetry blob for a host. The Go side
// never parses it; the TS coordinator owns the parsing.
func (s *Store) UpsertHostTelemetry(hostID string, telemetryJSON json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		INSERT INTO host_telemetry_cache (host_id, telemetry_json, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(host_id) DO UPDATE SET
			telemetry_json = excluded.telemetry_json,
			updated_at = excluded.updated_at`,
		hostID, string(orEmpty(telemetryJSON)), nowMS(),
	)
	return err
}

// GetHostTelemetry returns the stored telemetry blob and its updated_at.
func (s *Store) GetHostTelemetry(hostID string) (TelemetryEntry, bool, error) {
	var blob string
	var updated int64
	err := s.db.QueryRow(`SELECT telemetry_json, updated_at FROM host_telemetry_cache WHERE host_id = ?`, hostID).
		Scan(&blob, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return TelemetryEntry{}, false, nil
	}
	if err != nil {
		return TelemetryEntry{}, false, err
	}
	return TelemetryEntry{TelemetryJSON: json.RawMessage(blob), UpdatedAt: updated}, true, nil
}

// UpsertHostContainers merges and stores the container/pod state for a host,
// preserving custom identities and — when the update leaves them nil — the
// previously stored rootPods, hasDocker and dockerContainers, exactly as TS does.
func (s *Store) UpsertHostContainers(hostID string, u ContainersUpdate) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	existing, found, err := getContainersTx(tx, hostID)
	if err != nil {
		return err
	}

	custom := json.RawMessage(nil)
	rootPods := u.RootPodsJSON
	docker := u.DockerContainersJSON
	hasDocker := false
	if found {
		custom = existing.CustomIdentitiesJSON
		if rootPods == nil {
			rootPods = existing.RootPodsJSON
		}
		if docker == nil {
			docker = existing.DockerContainersJSON
		}
		hasDocker = existing.HasDocker
	}
	if u.HasDocker != nil {
		hasDocker = *u.HasDocker
	}

	_, err = tx.Exec(`
		INSERT INTO host_containers_cache (
			host_id, probed_users_json, identities_json, custom_identities_json, root_containers_json,
			root_pods_json, has_docker, docker_containers_json, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(host_id) DO UPDATE SET
			probed_users_json = excluded.probed_users_json,
			identities_json = excluded.identities_json,
			custom_identities_json = excluded.custom_identities_json,
			root_containers_json = excluded.root_containers_json,
			root_pods_json = excluded.root_pods_json,
			has_docker = excluded.has_docker,
			docker_containers_json = excluded.docker_containers_json,
			updated_at = excluded.updated_at`,
		hostID, string(orEmpty(u.ProbedUsersJSON)), string(orEmpty(u.IdentitiesJSON)), string(orEmpty(custom)),
		string(orEmpty(u.RootContainersJSON)), string(orEmpty(rootPods)), boolToInt(hasDocker),
		string(orEmpty(docker)), nowMS(),
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// AddCustomIdentity prepends a custom identity to the host's custom identities.
func (s *Store) AddCustomIdentity(hostID string, identityJSON json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	existing, found, err := getContainersTx(tx, hostID)
	if err != nil {
		return err
	}
	var e ContainersEntry
	if found {
		e = existing
	}
	custom, err := prependJSON(e.CustomIdentitiesJSON, identityJSON)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		INSERT INTO host_containers_cache (
			host_id, probed_users_json, identities_json, custom_identities_json, root_containers_json,
			root_pods_json, has_docker, docker_containers_json, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(host_id) DO UPDATE SET
			probed_users_json = excluded.probed_users_json,
			identities_json = excluded.identities_json,
			custom_identities_json = excluded.custom_identities_json,
			root_containers_json = excluded.root_containers_json,
			root_pods_json = excluded.root_pods_json,
			has_docker = excluded.has_docker,
			docker_containers_json = excluded.docker_containers_json,
			updated_at = excluded.updated_at`,
		hostID, string(orEmpty(e.ProbedUsersJSON)), string(orEmpty(e.IdentitiesJSON)), string(custom),
		string(orEmpty(e.RootContainersJSON)), string(orEmpty(e.RootPodsJSON)), boolToInt(e.HasDocker),
		string(orEmpty(e.DockerContainersJSON)), nowMS(),
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// GetHostContainers returns the stored container/pod state for a host.
func (s *Store) GetHostContainers(hostID string) (ContainersEntry, bool, error) {
	e, found, err := getContainersTx(s.db, hostID)
	return e, found, err
}

// rowQuerier is satisfied by both *sql.DB and *sql.Tx.
type rowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

func getContainersTx(q rowQuerier, hostID string) (ContainersEntry, bool, error) {
	var (
		probed, identities, custom, rootContainers sql.NullString
		rootPods, dockerContainers                 sql.NullString
		hasDocker                                  sql.NullInt64
		updated                                    int64
	)
	err := q.QueryRow(`
		SELECT probed_users_json, identities_json, custom_identities_json, root_containers_json,
		       root_pods_json, has_docker, docker_containers_json, updated_at
		FROM host_containers_cache WHERE host_id = ?`, hostID).
		Scan(&probed, &identities, &custom, &rootContainers, &rootPods, &hasDocker, &dockerContainers, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return ContainersEntry{}, false, nil
	}
	if err != nil {
		return ContainersEntry{}, false, err
	}
	return ContainersEntry{
		ProbedUsersJSON:      rawOrNil(probed),
		IdentitiesJSON:       rawOrNil(identities),
		CustomIdentitiesJSON: rawOrNil(custom),
		RootContainersJSON:   rawOrNil(rootContainers),
		RootPodsJSON:         rawOrNil(rootPods),
		DockerContainersJSON: rawOrNil(dockerContainers),
		HasDocker:            hasDocker.Valid && hasDocker.Int64 != 0,
		UpdatedAt:            updated,
	}, true, nil
}

// UpsertHostSection stores an opaque section blob (interfaces, firewall, disks, ...).
func (s *Store) UpsertHostSection(hostID, section string, dataJSON json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		INSERT INTO host_section_cache (host_id, section, data_json, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(host_id, section) DO UPDATE SET
			data_json = excluded.data_json,
			updated_at = excluded.updated_at`,
		hostID, section, string(orEmpty(dataJSON)), nowMS(),
	)
	return err
}

// GetHostSection returns a stored section blob and its updated_at.
func (s *Store) GetHostSection(hostID, section string) (SectionEntry, bool, error) {
	var blob string
	var updated int64
	err := s.db.QueryRow(`SELECT data_json, updated_at FROM host_section_cache WHERE host_id = ? AND section = ?`, hostID, section).
		Scan(&blob, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return SectionEntry{}, false, nil
	}
	if err != nil {
		return SectionEntry{}, false, err
	}
	return SectionEntry{DataJSON: json.RawMessage(blob), UpdatedAt: updated}, true, nil
}

// InvalidateHostSections deletes cached sections for a host, or for every host
// when hostID is empty.
func (s *Store) InvalidateHostSections(hostID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if hostID == "" {
		_, err := s.db.Exec(`DELETE FROM host_section_cache`)
		return err
	}
	_, err := s.db.Exec(`DELETE FROM host_section_cache WHERE host_id = ?`, hostID)
	return err
}

// ---------------------------------------------------------------------------
// tasks
// ---------------------------------------------------------------------------

// SaveTask inserts or updates a task. Like TS, only the mutable columns are
// updated on conflict (start_time/node_target/user/description/category stay).
func (s *Store) SaveTask(t Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		INSERT INTO tasks (id, start_time, node_target, user, description, progress, progress_eta, status, category, log_json, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			progress = excluded.progress,
			progress_eta = excluded.progress_eta,
			status = excluded.status,
			log_json = excluded.log_json,
			updated_at = excluded.updated_at`,
		t.ID, t.StartTime, t.NodeTarget, t.User, t.Description, t.Progress, t.ProgressEta, t.Status, t.Category,
		string(orEmpty(t.Logs)), nowMS(),
	)
	return err
}

// GetTask returns a single task by ID.
func (s *Store) GetTask(id string) (*Task, error) {
	var (
		t           Task
		progressEta sql.NullString
		logJSON     sql.NullString
	)
	err := s.db.QueryRow(`
		SELECT id, start_time, node_target, user, description, progress, progress_eta, status, category, log_json, updated_at
		FROM tasks WHERE id = ?`, id).
		Scan(&t.ID, &t.StartTime, &t.NodeTarget, &t.User, &t.Description, &t.Progress, &progressEta, &t.Status, &t.Category, &logJSON, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if progressEta.Valid {
		t.ProgressEta = progressEta.String
	}
	if logJSON.Valid {
		t.Logs = json.RawMessage(logJSON.String)
	}
	return &t, nil
}

// GetAllTasks returns up to limit tasks ordered by updated_at descending.
// A non-positive limit falls back to 50, matching TS.
func (s *Store) GetAllTasks(limit int) ([]Task, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT id, start_time, node_target, user, description, progress, progress_eta, status, category, log_json, updated_at
		FROM tasks ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Task, 0)
	for rows.Next() {
		var (
			t           Task
			progressEta sql.NullString
			logJSON     sql.NullString
		)
		if err := rows.Scan(&t.ID, &t.StartTime, &t.NodeTarget, &t.User, &t.Description, &t.Progress, &progressEta, &t.Status, &t.Category, &logJSON, &t.UpdatedAt); err != nil {
			return nil, err
		}
		if progressEta.Valid {
			t.ProgressEta = progressEta.String
		}
		if logJSON.Valid {
			t.Logs = json.RawMessage(logJSON.String)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateTask merges the provided fields into an existing task. Nil fields are
// left unchanged. A missing task is a no-op (matching TS).
func (s *Store) UpdateTask(id string, u TaskUpdate) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var (
		progress    int
		progressEta sql.NullString
		status      string
		logJSON     sql.NullString
	)
	err = tx.QueryRow(`SELECT progress, progress_eta, status, log_json FROM tasks WHERE id = ?`, id).
		Scan(&progress, &progressEta, &status, &logJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	if u.Progress != nil {
		progress = *u.Progress
	}
	if u.ProgressEta != nil {
		progressEta = sql.NullString{String: *u.ProgressEta, Valid: true}
	}
	if u.Status != nil {
		status = *u.Status
	}
	logs := logJSON
	if u.Logs != nil {
		logs = sql.NullString{String: string(u.Logs), Valid: true}
	}

	_, err = tx.Exec(`
		UPDATE tasks SET progress = ?, progress_eta = ?, status = ?, log_json = ?, updated_at = ? WHERE id = ?`,
		progress, progressEta, status, logs, nowMS(), id,
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// ---------------------------------------------------------------------------
// group vars
// ---------------------------------------------------------------------------

// SeedGroupVarsIfEmpty inserts defaults only when group_vars_cache is empty,
// then returns all group vars. Mirrors the TS seed.
func (s *Store) SeedGroupVarsIfEmpty(defaults map[string]string) (map[string]string, error) {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM group_vars_cache`).Scan(&count); err != nil {
		return nil, err
	}
	if count == 0 {
		now := nowMS()
		for file, content := range defaults {
			if _, err := s.db.Exec(`
				INSERT INTO group_vars_cache (file_path, content, updated_at)
				VALUES (?, ?, ?)
				ON CONFLICT(file_path) DO UPDATE SET
					content = excluded.content,
					updated_at = excluded.updated_at`, file, content, now); err != nil {
				return nil, err
			}
		}
	}
	return s.GetAllGroupVars()
}

// UpsertGroupVar inserts or updates a single group var file.
func (s *Store) UpsertGroupVar(file, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		INSERT INTO group_vars_cache (file_path, content, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(file_path) DO UPDATE SET
			content = excluded.content,
			updated_at = excluded.updated_at`, file, content, nowMS())
	return err
}

// GetAllGroupVars returns all group vars as a file_path → content map.
func (s *Store) GetAllGroupVars() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT file_path, content FROM group_vars_cache`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var file, content string
		if err := rows.Scan(&file, &content); err != nil {
			return nil, err
		}
		out[file] = content
	}
	return out, rows.Err()
}

// DeleteGroupVar deletes a single group var file from cache and reports whether a row was removed.
func (s *Store) DeleteGroupVar(file string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec(`DELETE FROM group_vars_cache WHERE file_path = ?`, file)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ---------------------------------------------------------------------------
// custom folders
// ---------------------------------------------------------------------------

// UpsertCustomFolder inserts or updates a folder (only its name changes on conflict).
func (s *Store) UpsertCustomFolder(f CustomFolder) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		INSERT INTO inventory_folders (id, name, created_at)
		VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name = excluded.name`, f.ID, f.Name, f.CreatedAt)
	return err
}

// GetAllCustomFolders returns all folders ordered by name.
func (s *Store) GetAllCustomFolders() ([]CustomFolder, error) {
	rows, err := s.db.Query(`SELECT id, name, created_at FROM inventory_folders ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]CustomFolder, 0)
	for rows.Next() {
		var f CustomFolder
		if err := rows.Scan(&f.ID, &f.Name, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// DeleteCustomFolder deletes a folder and strips its id from any host that
// references it, all in one transaction. Mirrors the TS BEGIN IMMEDIATE block.
func (s *Store) DeleteCustomFolder(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.Query(`SELECT node_json FROM hosts`)
	if err != nil {
		return false, err
	}
	var blobs []string
	for rows.Next() {
		var blob string
		if err := rows.Scan(&blob); err != nil {
			rows.Close()
			return false, err
		}
		var probe struct {
			Folder string `json:"folder"`
		}
		if err := json.Unmarshal([]byte(blob), &probe); err == nil && probe.Folder == id {
			blobs = append(blobs, blob)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, err
	}
	rows.Close()

	for _, blob := range blobs {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(blob), &m); err != nil {
			return false, err
		}
		delete(m, "folder")
		nb, err := json.Marshal(m)
		if err != nil {
			return false, err
		}
		var rec NodeRecord
		if err := json.Unmarshal(nb, &rec); err != nil {
			return false, err
		}
		rec.NodeJSON = nb
		if err := upsertHostTx(tx, rec, 0, false); err != nil {
			return false, err
		}
	}

	res, err := tx.Exec(`DELETE FROM inventory_folders WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ---------------------------------------------------------------------------
// tag inventory chunks
// ---------------------------------------------------------------------------

// UpsertTagChunk inserts or updates a tag inventory chunk.
func (s *Store) UpsertTagChunk(c TagChunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		INSERT INTO tag_inventory_chunks (id, tag, ssh_user, vault_secret_id, auth_method, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			tag = excluded.tag,
			ssh_user = excluded.ssh_user,
			vault_secret_id = excluded.vault_secret_id,
			auth_method = excluded.auth_method`,
		c.ID, c.Tag, nullable(c.SSHUser), nullable(c.VaultSecretID), c.AuthMethod, c.CreatedAt)
	return err
}

// GetAllTagChunks returns all chunks ordered by created_at ascending.
func (s *Store) GetAllTagChunks() ([]TagChunk, error) {
	rows, err := s.db.Query(`
		SELECT id, tag, ssh_user, vault_secret_id, auth_method, created_at
		FROM tag_inventory_chunks ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]TagChunk, 0)
	for rows.Next() {
		var (
			c        TagChunk
			sshUser  sql.NullString
			vaultSec sql.NullString
		)
		if err := rows.Scan(&c.ID, &c.Tag, &sshUser, &vaultSec, &c.AuthMethod, &c.CreatedAt); err != nil {
			return nil, err
		}
		if sshUser.Valid {
			c.SSHUser = sshUser.String
		}
		if vaultSec.Valid {
			c.VaultSecretID = vaultSec.String
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteTagChunk deletes a chunk and reports whether a row was removed.
func (s *Store) DeleteTagChunk(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec(`DELETE FROM tag_inventory_chunks WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// -----------------------------------------------------------------------------
// Vault Variables & Audit Store
// -----------------------------------------------------------------------------

// UpsertVaultVariable stores a named ansible-vault ciphertext block.
func (s *Store) UpsertVaultVariable(name, vaultBlock string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UnixMilli()
	const q = `
		INSERT INTO vault_variables (name, vault_block, created_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			vault_block = excluded.vault_block,
			updated_at = excluded.updated_at
	`
	_, err := s.db.Exec(q, name, vaultBlock, now, now)
	return err
}

// GetVaultVariable retrieves a vault variable block by name.
func (s *Store) GetVaultVariable(name string) (VaultVariable, bool, error) {
	const q = `SELECT name, vault_block, created_at, updated_at FROM vault_variables WHERE name = ?`
	row := s.db.QueryRow(q, name)
	var v VaultVariable
	if err := row.Scan(&v.Name, &v.VaultBlock, &v.CreatedAt, &v.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return VaultVariable{}, false, nil
		}
		return VaultVariable{}, false, err
	}
	return v, true, nil
}

// ListVaultVariables returns all vault variables.
func (s *Store) ListVaultVariables() ([]VaultVariable, error) {
	const q = `SELECT name, vault_block, created_at, updated_at FROM vault_variables ORDER BY name ASC`
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]VaultVariable, 0)
	for rows.Next() {
		var v VaultVariable
		if err := rows.Scan(&v.Name, &v.VaultBlock, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// DeleteVaultVariable deletes a vault variable by name.
func (s *Store) DeleteVaultVariable(name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec(`DELETE FROM vault_variables WHERE name = ?`, name)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// RecordVaultAuditEvent persists a vault audit entry.
func (s *Store) RecordVaultAuditEvent(ev VaultAuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	const q = `
		INSERT INTO vault_audit_log (id, timestamp, variable_name, actor, host, action, password_source)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.Exec(q, ev.ID, ev.Timestamp, ev.VariableName, ev.Actor, ev.Host, ev.Action, ev.PasswordSource)
	return err
}

// GetVaultAuditEvents returns vault audit events with optional filters.
func (s *Store) GetVaultAuditEvents(varName, host string, limit int) ([]VaultAuditEvent, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}

	var (
		q    string
		args []any
	)

	if varName != "" && host != "" {
		q = `SELECT id, timestamp, variable_name, actor, host, action, password_source FROM vault_audit_log WHERE variable_name = ? AND host = ? ORDER BY timestamp DESC LIMIT ?`
		args = []any{varName, host, limit}
	} else if varName != "" {
		q = `SELECT id, timestamp, variable_name, actor, host, action, password_source FROM vault_audit_log WHERE variable_name = ? ORDER BY timestamp DESC LIMIT ?`
		args = []any{varName, limit}
	} else if host != "" {
		q = `SELECT id, timestamp, variable_name, actor, host, action, password_source FROM vault_audit_log WHERE host = ? ORDER BY timestamp DESC LIMIT ?`
		args = []any{host, limit}
	} else {
		q = `SELECT id, timestamp, variable_name, actor, host, action, password_source FROM vault_audit_log ORDER BY timestamp DESC LIMIT ?`
		args = []any{limit}
	}

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]VaultAuditEvent, 0)
	for rows.Next() {
		var e VaultAuditEvent
		if err := rows.Scan(&e.ID, &e.Timestamp, &e.VariableName, &e.Actor, &e.Host, &e.Action, &e.PasswordSource); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// -----------------------------------------------------------------------------
// Rclone Remotes & Mounts
// -----------------------------------------------------------------------------

// UpsertRcloneRemote inserts or updates an rclone remote record.
func (s *Store) UpsertRcloneRemote(r RcloneRemote) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	const q = `
		INSERT INTO rclone_remotes (id, name, provider, type, config_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			provider = excluded.provider,
			type = excluded.type,
			config_json = excluded.config_json,
			updated_at = excluded.updated_at
	`
	_, err := s.db.Exec(q, r.ID, r.Name, r.Provider, r.Type, r.ConfigJSON, r.CreatedAt, r.UpdatedAt)
	return err
}

// GetAllRcloneRemotes returns all rclone remotes ordered by name ASC.
func (s *Store) GetAllRcloneRemotes() ([]RcloneRemote, error) {
	const q = `SELECT id, name, provider, type, config_json, created_at, updated_at FROM rclone_remotes ORDER BY name ASC`
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]RcloneRemote, 0)
	for rows.Next() {
		var r RcloneRemote
		if err := rows.Scan(&r.ID, &r.Name, &r.Provider, &r.Type, &r.ConfigJSON, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRcloneRemote returns an rclone remote by ID.
func (s *Store) GetRcloneRemote(id string) (RcloneRemote, bool, error) {
	const q = `SELECT id, name, provider, type, config_json, created_at, updated_at FROM rclone_remotes WHERE id = ?`
	row := s.db.QueryRow(q, id)
	var r RcloneRemote
	if err := row.Scan(&r.ID, &r.Name, &r.Provider, &r.Type, &r.ConfigJSON, &r.CreatedAt, &r.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RcloneRemote{}, false, nil
		}
		return RcloneRemote{}, false, err
	}
	return r, true, nil
}

// GetRcloneRemoteByName returns an rclone remote by sanitized name.
func (s *Store) GetRcloneRemoteByName(name string) (RcloneRemote, bool, error) {
	const q = `SELECT id, name, provider, type, config_json, created_at, updated_at FROM rclone_remotes WHERE name = ?`
	row := s.db.QueryRow(q, name)
	var r RcloneRemote
	if err := row.Scan(&r.ID, &r.Name, &r.Provider, &r.Type, &r.ConfigJSON, &r.CreatedAt, &r.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RcloneRemote{}, false, nil
		}
		return RcloneRemote{}, false, err
	}
	return r, true, nil
}

// DeleteRcloneRemote deletes a remote and cleans up associated mounts.
func (s *Store) DeleteRcloneRemote(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM rclone_mounts WHERE remote_id = ?`, id); err != nil {
		return false, err
	}
	res, err := tx.Exec(`DELETE FROM rclone_remotes WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// UpsertRcloneMount stores an rclone mount record.
func (s *Store) UpsertRcloneMount(m RcloneMount) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	const q = `
		INSERT INTO rclone_mounts (node_id, remote_id, remote_name, mount_target, policy, status, error, installed_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(node_id, remote_id) DO UPDATE SET
			remote_name = excluded.remote_name,
			mount_target = excluded.mount_target,
			policy = excluded.policy,
			status = excluded.status,
			error = excluded.error,
			updated_at = excluded.updated_at
	`
	_, err := s.db.Exec(q, m.NodeID, m.RemoteID, m.RemoteName, m.MountTarget, m.Policy, m.Status, m.Error, m.InstalledAt, m.UpdatedAt)
	return err
}

// GetRcloneMounts returns rclone mounts, optionally filtered by nodeId.
func (s *Store) GetRcloneMounts(nodeID string) ([]RcloneMount, error) {
	var (
		q    string
		args []any
	)
	if nodeID != "" {
		q = `SELECT node_id, remote_id, remote_name, mount_target, policy, status, error, installed_at, updated_at FROM rclone_mounts WHERE node_id = ? ORDER BY remote_name ASC`
		args = []any{nodeID}
	} else {
		q = `SELECT node_id, remote_id, remote_name, mount_target, policy, status, error, installed_at, updated_at FROM rclone_mounts ORDER BY remote_name ASC`
	}

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]RcloneMount, 0)
	for rows.Next() {
		var (
			m        RcloneMount
			errField sql.NullString
		)
		if err := rows.Scan(&m.NodeID, &m.RemoteID, &m.RemoteName, &m.MountTarget, &m.Policy, &m.Status, &errField, &m.InstalledAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		if errField.Valid {
			m.Error = &errField.String
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// DeleteRcloneMount deletes a mount by (node_id, remote_id).
func (s *Store) DeleteRcloneMount(nodeID, remoteID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec(`DELETE FROM rclone_mounts WHERE node_id = ? AND remote_id = ?`, nodeID, remoteID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// -----------------------------------------------------------------------------
// Cron Schedules Store
// -----------------------------------------------------------------------------

// UpsertCronSchedule inserts or updates a cron schedule.
func (s *Store) UpsertCronSchedule(cs CronSchedule) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	enabledInt := 0
	if cs.Enabled {
		enabledInt = 1
	}

	const q = `
		INSERT INTO cron_schedules (
			id, name, cron_expression, enabled, scope_kind, scope_target, task_kind,
			payload_json, created_at, updated_at, next_run_at, last_run_at, last_run_status, last_run_task_id, last_run_error
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			cron_expression = excluded.cron_expression,
			enabled = excluded.enabled,
			scope_kind = excluded.scope_kind,
			scope_target = excluded.scope_target,
			task_kind = excluded.task_kind,
			payload_json = excluded.payload_json,
			updated_at = excluded.updated_at,
			next_run_at = excluded.next_run_at,
			last_run_at = excluded.last_run_at,
			last_run_status = excluded.last_run_status,
			last_run_task_id = excluded.last_run_task_id,
			last_run_error = excluded.last_run_error
	`
	_, err := s.db.Exec(q,
		cs.ID, cs.Name, cs.CronExpression, enabledInt, cs.ScopeKind, cs.ScopeTarget, cs.TaskKind,
		string(cs.PayloadJSON), cs.CreatedAt, cs.UpdatedAt, cs.NextRunAt, cs.LastRunAt, cs.LastRunStatus, cs.LastRunTaskID, cs.LastRunError,
	)
	return err
}

// GetAllCronSchedules returns all schedules ordered by created_at ASC.
func (s *Store) GetAllCronSchedules() ([]CronSchedule, error) {
	const q = `
		SELECT id, name, cron_expression, enabled, scope_kind, scope_target, task_kind,
		       payload_json, created_at, updated_at, next_run_at, last_run_at, last_run_status, last_run_task_id, last_run_error
		FROM cron_schedules ORDER BY created_at ASC
	`
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]CronSchedule, 0)
	for rows.Next() {
		var (
			cs         CronSchedule
			enabled    int
			payloadStr string
			nextRun    sql.NullInt64
			lastRun    sql.NullInt64
			status     sql.NullString
			taskID     sql.NullString
			lastErr    sql.NullString
		)
		if err := rows.Scan(
			&cs.ID, &cs.Name, &cs.CronExpression, &enabled, &cs.ScopeKind, &cs.ScopeTarget, &cs.TaskKind,
			&payloadStr, &cs.CreatedAt, &cs.UpdatedAt, &nextRun, &lastRun, &status, &taskID, &lastErr,
		); err != nil {
			return nil, err
		}
		cs.Enabled = enabled == 1
		cs.PayloadJSON = json.RawMessage(payloadStr)
		if nextRun.Valid {
			cs.NextRunAt = &nextRun.Int64
		}
		if lastRun.Valid {
			cs.LastRunAt = &lastRun.Int64
		}
		if status.Valid {
			cs.LastRunStatus = &status.String
		}
		if taskID.Valid {
			cs.LastRunTaskID = &taskID.String
		}
		if lastErr.Valid {
			cs.LastRunError = &lastErr.String
		}
		out = append(out, cs)
	}
	return out, rows.Err()
}

// GetCronSchedule returns a cron schedule by ID.
func (s *Store) GetCronSchedule(id string) (CronSchedule, bool, error) {
	const q = `
		SELECT id, name, cron_expression, enabled, scope_kind, scope_target, task_kind,
		       payload_json, created_at, updated_at, next_run_at, last_run_at, last_run_status, last_run_task_id, last_run_error
		FROM cron_schedules WHERE id = ?
	`
	row := s.db.QueryRow(q, id)
	var (
		cs         CronSchedule
		enabled    int
		payloadStr string
		nextRun    sql.NullInt64
		lastRun    sql.NullInt64
		status     sql.NullString
		taskID     sql.NullString
		lastErr    sql.NullString
	)
	if err := row.Scan(
		&cs.ID, &cs.Name, &cs.CronExpression, &enabled, &cs.ScopeKind, &cs.ScopeTarget, &cs.TaskKind,
		&payloadStr, &cs.CreatedAt, &cs.UpdatedAt, &nextRun, &lastRun, &status, &taskID, &lastErr,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CronSchedule{}, false, nil
		}
		return CronSchedule{}, false, err
	}
	cs.Enabled = enabled == 1
	cs.PayloadJSON = json.RawMessage(payloadStr)
	if nextRun.Valid {
		cs.NextRunAt = &nextRun.Int64
	}
	if lastRun.Valid {
		cs.LastRunAt = &lastRun.Int64
	}
	if status.Valid {
		cs.LastRunStatus = &status.String
	}
	if taskID.Valid {
		cs.LastRunTaskID = &taskID.String
	}
	if lastErr.Valid {
		cs.LastRunError = &lastErr.String
	}
	return cs, true, nil
}

// DeleteCronSchedule deletes a cron schedule by ID.
func (s *Store) DeleteCronSchedule(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec(`DELETE FROM cron_schedules WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ListDueCronSchedules returns enabled schedules where next_run_at <= nowMs.
func (s *Store) ListDueCronSchedules(nowMs int64) ([]CronSchedule, error) {
	const q = `
		SELECT id, name, cron_expression, enabled, scope_kind, scope_target, task_kind,
		       payload_json, created_at, updated_at, next_run_at, last_run_at, last_run_status, last_run_task_id, last_run_error
		FROM cron_schedules
		WHERE enabled = 1 AND next_run_at IS NOT NULL AND next_run_at <= ?
		ORDER BY next_run_at ASC
	`
	rows, err := s.db.Query(q, nowMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]CronSchedule, 0)
	for rows.Next() {
		var (
			cs         CronSchedule
			enabled    int
			payloadStr string
			nextRun    sql.NullInt64
			lastRun    sql.NullInt64
			status     sql.NullString
			taskID     sql.NullString
			lastErr    sql.NullString
		)
		if err := rows.Scan(
			&cs.ID, &cs.Name, &cs.CronExpression, &enabled, &cs.ScopeKind, &cs.ScopeTarget, &cs.TaskKind,
			&payloadStr, &cs.CreatedAt, &cs.UpdatedAt, &nextRun, &lastRun, &status, &taskID, &lastErr,
		); err != nil {
			return nil, err
		}
		cs.Enabled = enabled == 1
		cs.PayloadJSON = json.RawMessage(payloadStr)
		if nextRun.Valid {
			cs.NextRunAt = &nextRun.Int64
		}
		if lastRun.Valid {
			cs.LastRunAt = &lastRun.Int64
		}
		if status.Valid {
			cs.LastRunStatus = &status.String
		}
		if taskID.Valid {
			cs.LastRunTaskID = &taskID.String
		}
		if lastErr.Valid {
			cs.LastRunError = &lastErr.String
		}
		out = append(out, cs)
	}
	return out, rows.Err()
}

// SetCronScheduleNextRun updates only the next_run_at timestamp.
func (s *Store) SetCronScheduleNextRun(id string, nextRunAt *int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	const q = `UPDATE cron_schedules SET next_run_at = ?, updated_at = ? WHERE id = ?`
	_, err := s.db.Exec(q, nextRunAt, time.Now().UnixMilli(), id)
	return err
}

// RecordCronScheduleRun updates run outcome info.
func (s *Store) RecordCronScheduleRun(id string, status string, at int64, taskID, lastErr *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	const q = `
		UPDATE cron_schedules
		SET last_run_at = ?, last_run_status = ?, last_run_task_id = ?, last_run_error = ?, updated_at = ?
		WHERE id = ?
	`
	_, err := s.db.Exec(q, at, status, taskID, lastErr, time.Now().UnixMilli(), id)
	return err
}

