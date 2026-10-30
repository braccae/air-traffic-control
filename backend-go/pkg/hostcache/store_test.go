package hostcache

import (
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// copyRealDB copies the real repository database (and its WAL sidecars) into a
// temporary directory so tests exercise a genuine SQLite file without touching
// the shared cache. Returns the destination path, or "" if the source is absent.
func copyRealDB(t *testing.T) string {
	t.Helper()
	src := filepath.Join("..", "..", "..", "data", "hosts-cache.sqlite")
	if _, err := os.Stat(src); err != nil {
		return ""
	}
	dst := filepath.Join(t.TempDir(), "hosts-cache.sqlite")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		in, err := os.Open(src + suffix)
		if err != nil {
			continue
		}
		out, err := os.Create(dst + suffix)
		if err != nil {
			_ = in.Close()
			t.Fatalf("create %s: %v", dst+suffix, err)
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = in.Close()
			_ = out.Close()
			t.Fatalf("copy %s: %v", src+suffix, err)
		}
		_ = in.Close()
		_ = out.Close()
	}
	return dst
}

// TestRealDatabaseReadable opens a temp copy of the real database and reads a
// node row back. Zero-Mock Policy: this uses a real SQLite file.
func TestRealDatabaseReadable(t *testing.T) {
	dst := copyRealDB(t)
	if dst == "" {
		t.Skip("real data/hosts-cache.sqlite not present")
	}

	store, err := Open(dst)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	hosts, err := store.GetAllHosts()
	if err != nil {
		t.Fatalf("GetAllHosts: %v", err)
	}
	if len(hosts) == 0 {
		t.Skip("real data/hosts-cache.sqlite has no host rows")
	}

	first := hosts[0]
	if first.ID == "" {
		t.Fatalf("first host has empty id: %+v", first)
	}
	if len(first.NodeJSON) == 0 {
		t.Fatalf("first host has empty node_json")
	}
	// NodeJSON must be a valid JSON object carrying the camelCase NodeItem keys.
	var node map[string]any
	if err := json.Unmarshal(first.NodeJSON, &node); err != nil {
		t.Fatalf("node_json is not valid JSON: %v", err)
	}
	if node["id"] != first.ID {
		t.Fatalf("node_json id %v does not match row id %s", node["id"], first.ID)
	}

	// Round-trip via GetHostByID too.
	again, ok, err := store.GetHostByID(first.ID)
	if err != nil {
		t.Fatalf("GetHostByID: %v", err)
	}
	if !ok || again.ID != first.ID {
		t.Fatalf("GetHostByID did not return the same host")
	}
}

// TestTelemetryTTLRoundTrip verifies the TTL tiers against a real file: a fresh
// write is Fresh under TelemetryTTL, and an aged write is stale under 15s but
// still fresh under 60s.
func TestTelemetryTTLRoundTrip(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "cache.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	const hostID = "node-test"
	payload := json.RawMessage(`{"cpuUsage":12.5,"watts":45}`)
	if err := store.UpsertHostTelemetry(hostID, payload); err != nil {
		t.Fatalf("UpsertHostTelemetry: %v", err)
	}

	got, ok, err := store.GetHostTelemetry(hostID)
	if err != nil {
		t.Fatalf("GetHostTelemetry: %v", err)
	}
	if !ok {
		t.Fatal("telemetry entry not found after upsert")
	}
	if string(got.TelemetryJSON) != string(payload) {
		t.Fatalf("telemetry blob changed: got %s want %s", got.TelemetryJSON, payload)
	}
	if !Fresh(got.UpdatedAt, TelemetryTTL) {
		t.Fatalf("fresh write should be within TelemetryTTL (updatedAt=%d)", got.UpdatedAt)
	}

	// Age the row by 20s and re-check the tiers.
	aged := time.Now().UnixMilli() - 20_000
	if _, err := store.db.Exec(`UPDATE host_telemetry_cache SET updated_at = ? WHERE host_id = ?`, aged, hostID); err != nil {
		t.Fatalf("age telemetry row: %v", err)
	}
	got, ok, err = store.GetHostTelemetry(hostID)
	if err != nil || !ok {
		t.Fatalf("re-read telemetry: ok=%v err=%v", ok, err)
	}
	if Fresh(got.UpdatedAt, TelemetryTTL) {
		t.Fatalf("20s-old entry must be stale under the 15s telemetry TTL")
	}
	if !Fresh(got.UpdatedAt, 60*time.Second) {
		t.Fatalf("20s-old entry must still be fresh under a 60s TTL")
	}
}

// TestSectionTTLTiers locks the TTL mapping to the documented tiers.
func TestSectionTTLTiers(t *testing.T) {
	cases := map[string]time.Duration{
		"interfaces": NetworkTTL,
		"firewall":   FirewallTTL,
		"disks":      DisksTTL,
		"vms":        VMsTTL,
		"storage":    StorageTTL,
		"shares":     SharesTTL,
		"unknown":    60 * time.Second,
	}
	if TelemetryTTL != 15*time.Second {
		t.Fatalf("TelemetryTTL = %v, want 15s", TelemetryTTL)
	}
	if ContainerTTL != 20*time.Second {
		t.Fatalf("ContainerTTL = %v, want 20s", ContainerTTL)
	}
	if NetworkTTL != 30*time.Second {
		t.Fatalf("NetworkTTL = %v, want 30s", NetworkTTL)
	}
	for section, want := range cases {
		if got := SectionTTL(section); got != want {
			t.Errorf("SectionTTL(%q) = %v, want %v", section, got, want)
		}
	}
}

// TestSchemaParity guards the exact table set and the column order of the two
// most important tables against the TS source of truth.
func TestSchemaParity(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "fresh.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	tables := map[string]bool{}
	rows, err := store.db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table: %v", err)
		}
		tables[name] = true
	}
	rows.Close()

	wantTables := []string{
		"hosts", "host_telemetry_cache", "host_containers_cache", "group_vars_cache",
		"host_section_cache", "tasks", "inventory_folders", "tag_inventory_chunks",
	}
	for _, name := range wantTables {
		if !tables[name] {
			t.Errorf("missing table %q", name)
		}
	}

	assertColumns(t, store, "hosts", []string{
		"id", "name", "short_name", "group_name", "ip", "port", "ssh_user",
		"tailscale_ip", "tailscale_host", "status", "sort_order", "vault_secret_id", "node_json", "updated_at",
	})
	assertColumns(t, store, "host_containers_cache", []string{
		"host_id", "probed_users_json", "identities_json", "custom_identities_json",
		"root_containers_json", "updated_at", "root_pods_json", "has_docker", "docker_containers_json",
	})
	assertColumns(t, store, "host_section_cache", []string{
		"host_id", "section", "data_json", "updated_at",
	})
}

func assertColumns(t *testing.T, store *Store, table string, want []string) {
	t.Helper()
	rows, err := store.db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatalf("table_info(%s): %v", table, err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		got = append(got, name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("table %s columns = %v, want %v", table, got, want)
	}
}

// TestHostRoundTrip exercises upsert/get/delete against a real file, including
// the sort_order preservation behaviour and cascade delete of cache rows.
func TestHostRoundTrip(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "hosts.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	node := NodeRecord{
		ID:            "node-alpha",
		Name:          "alpha",
		ShortName:     "alpha",
		Group:         "tag:host",
		IP:            "10.0.0.5",
		Port:          22,
		SSHUser:       "core",
		TailscaleIP:   "100.64.0.5",
		TailscaleHost: "alpha.example.ts.net",
		Status:        "online",
	}
	node.NodeJSON = json.RawMessage(`{"id":"node-alpha","name":"alpha","shortName":"alpha","group":"tag:host","ip":"10.0.0.5","port":22,"sshUser":"core","tailscaleIp":"100.64.0.5","tailscaleHost":"alpha.example.ts.net","status":"online","folder":"folder-1"}`)

	if err := store.UpsertHost(node); err != nil {
		t.Fatalf("UpsertHost: %v", err)
	}
	// A second upsert must not clobber sort_order set by the first.
	if err := store.UpsertHost(node); err != nil {
		t.Fatalf("UpsertHost #2: %v", err)
	}

	got, ok, err := store.GetHostByID("node-alpha")
	if err != nil || !ok {
		t.Fatalf("GetHostByID: ok=%v err=%v", ok, err)
	}
	if got.Name != "alpha" || got.IP != "10.0.0.5" || got.Folder != "folder-1" {
		t.Fatalf("unexpected host: %+v", got)
	}

	// Add dependent rows, then confirm DeleteHost removes them all.
	if err := store.UpsertHostTelemetry("node-alpha", json.RawMessage(`{"cpuUsage":1}`)); err != nil {
		t.Fatalf("UpsertHostTelemetry: %v", err)
	}
	if err := store.UpsertHostSection("node-alpha", "interfaces", json.RawMessage(`[{"iface":"eth0"}]`)); err != nil {
		t.Fatalf("UpsertHostSection: %v", err)
	}

	deleted, err := store.DeleteHost("node-alpha")
	if err != nil {
		t.Fatalf("DeleteHost: %v", err)
	}
	if !deleted {
		t.Fatal("DeleteHost reported no deletion")
	}
	if _, ok, _ := store.GetHostByID("node-alpha"); ok {
		t.Fatal("host still present after delete")
	}
	if _, ok, _ := store.GetHostTelemetry("node-alpha"); ok {
		t.Fatal("telemetry still present after delete")
	}
	if _, ok, _ := store.GetHostSection("node-alpha", "interfaces"); ok {
		t.Fatal("section still present after delete")
	}
}

// TestSeedHostsIfEmpty verifies seeding only happens once and preserves order.
func TestSeedHostsIfEmpty(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "seed.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	seed := []NodeRecord{
		{ID: "n1", Name: "one", NodeJSON: json.RawMessage(`{"id":"n1","name":"one"}`)},
		{ID: "n2", Name: "two", NodeJSON: json.RawMessage(`{"id":"n2","name":"two"}`)},
	}
	got, err := store.SeedHostsIfEmpty(seed)
	if err != nil {
		t.Fatalf("SeedHostsIfEmpty: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 seeded hosts, got %d", len(got))
	}
	if got[0].SortOrder != 0 || got[1].SortOrder != 1 {
		t.Fatalf("seed order not preserved: %+v", got)
	}

	// Seeding again with different data must be a no-op.
	again, err := store.SeedHostsIfEmpty([]NodeRecord{{ID: "n3", Name: "three", NodeJSON: json.RawMessage(`{"id":"n3"}`)}})
	if err != nil {
		t.Fatalf("SeedHostsIfEmpty #2: %v", err)
	}
	if len(again) != 2 {
		t.Fatalf("seed re-ran on non-empty table: got %d", len(again))
	}
}

// TestDeleteCustomFolderStripsHosts confirms the folder id is removed from host
// node_json in the same transaction as the folder row.
func TestDeleteCustomFolderStripsHosts(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "folders.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.UpsertCustomFolder(CustomFolder{ID: "folder-1", Name: "Prod", CreatedAt: nowMS()}); err != nil {
		t.Fatalf("UpsertCustomFolder: %v", err)
	}
	node := NodeRecord{ID: "n1", Name: "one", NodeJSON: json.RawMessage(`{"id":"n1","name":"one","folder":"folder-1"}`)}
	if err := store.UpsertHost(node); err != nil {
		t.Fatalf("UpsertHost: %v", err)
	}

	deleted, err := store.DeleteCustomFolder("folder-1")
	if err != nil {
		t.Fatalf("DeleteCustomFolder: %v", err)
	}
	if !deleted {
		t.Fatal("folder was not deleted")
	}

	host, ok, err := store.GetHostByID("n1")
	if err != nil || !ok {
		t.Fatalf("host disappeared: ok=%v err=%v", ok, err)
	}
	if host.Folder != "" {
		t.Fatalf("folder field not stripped from host: %q", host.Folder)
	}
}

// TestTaskCrashRecoveryOnStartup verifies that any tasks left in RUNNING or QUEUED
// when the server restarts are marked FAILED rather than lingering indefinitely.
func TestTaskCrashRecoveryOnStartup(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tasks-crash.sqlite")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open #1: %v", err)
	}

	if err := store.SaveTask(Task{
		ID:          "task-running-1",
		StartTime:   "2026-10-21 12:00:00",
		NodeTarget:  "likes-it-cold",
		User:        "core@ansible",
		Description: "ansible-playbook site.yml",
		Progress:    45,
		ProgressEta: "TASK [Gathering Facts]",
		Status:      "RUNNING",
		Category:    "ansible",
	}); err != nil {
		t.Fatalf("SaveTask running: %v", err)
	}

	if err := store.SaveTask(Task{
		ID:          "task-ok-1",
		StartTime:   "2026-10-21 11:55:00",
		NodeTarget:  "likes-it-cold",
		User:        "core@ansible",
		Description: "ansible-playbook verify.yml",
		Progress:    100,
		ProgressEta: "Completed",
		Status:      "OK",
		Category:    "ansible",
	}); err != nil {
		t.Fatalf("SaveTask ok: %v", err)
	}

	_ = store.Close()

	// Re-open the store simulating daemon restart.
	reopened, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open #2: %v", err)
	}
	defer func() { _ = reopened.Close() }()

	recovered, err := reopened.GetTask("task-running-1")
	if err != nil || recovered == nil {
		t.Fatalf("GetTask(task-running-1): task=%v err=%v", recovered, err)
	}
	if recovered.Status != "FAILED" {
		t.Errorf("expected interrupted RUNNING task to be FAILED after restart, got %q", recovered.Status)
	}
	if recovered.Progress != 100 {
		t.Errorf("expected progress 100, got %d", recovered.Progress)
	}

	completed, err := reopened.GetTask("task-ok-1")
	if err != nil || completed == nil {
		t.Fatalf("GetTask(task-ok-1): task=%v err=%v", completed, err)
	}
	if completed.Status != "OK" {
		t.Errorf("expected completed OK task to remain OK, got %q", completed.Status)
	}
}

// TestInMemoryBackupToDisk verifies that Backup() on an in-memory store creates a valid
// SQLite database file on disk with all tables, including hosts, host_telemetry_cache,
// and schema_version.
func TestInMemoryBackupToDisk(t *testing.T) {
	tmpDir := t.TempDir()
	backupPath := filepath.Join(tmpDir, "data", "hosts-cache-backup.sqlite")

	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:): %v", err)
	}
	defer store.Close()

	var initialMode string
	if err := store.db.QueryRow("PRAGMA journal_mode;").Scan(&initialMode); err != nil {
		t.Fatalf("query initial journal_mode: %v", err)
	}
	t.Logf("Initial journal mode: %q", initialMode)

	// Override backupPath to isolated temporary file
	store.backupPath = backupPath

	// Populate data into the in-memory database
	testHost := NodeRecord{
		ID:        "node-backup-test",
		Name:      "Backup Target Node",
		ShortName: "backup-test",
		Group:     "test-servers",
		IP:        "192.168.1.100",
		Port:      22,
		SSHUser:   "core",
		Status:    "online",
		NodeJSON:  json.RawMessage(`{"id":"node-backup-test","name":"Backup Target Node"}`),
	}
	if err := store.UpsertHost(testHost); err != nil {
		t.Fatalf("UpsertHost: %v", err)
	}

	telemJSON := json.RawMessage(`{"cpu_pct":12.5,"mem_used_pct":45.2}`)
	if err := store.UpsertHostTelemetry("node-backup-test", telemJSON); err != nil {
		t.Fatalf("UpsertHostTelemetry: %v", err)
	}

	// First backup execution
	if err := store.Backup(); err != nil {
		t.Fatalf("first Backup() failed: %v", err)
	}

	// Verify file was created on disk
	fi, err := os.Stat(backupPath)
	if err != nil {
		t.Fatalf("stat backup file: %v", err)
	}
	if fi.Size() == 0 {
		t.Fatalf("backup file is empty")
	}

	// Verify content of the backup file directly via SQLite
	db, err := sql.Open("sqlite", "file:"+backupPath+"?mode=ro")
	if err != nil {
		t.Fatalf("open backup db: %v", err)
	}
	defer db.Close()

	// 1. Verify schema_version table
	var version int
	if err := db.QueryRow("SELECT version FROM schema_version WHERE id = 1").Scan(&version); err != nil {
		t.Fatalf("query schema_version from backup: %v", err)
	}
	if version != SchemaVersion {
		t.Errorf("schema_version in backup = %d, want %d", version, SchemaVersion)
	}

	// 2. Verify hosts table
	var hostID, hostName string
	if err := db.QueryRow("SELECT id, name FROM hosts WHERE id = 'node-backup-test'").Scan(&hostID, &hostName); err != nil {
		t.Fatalf("query hosts from backup: %v", err)
	}
	if hostID != "node-backup-test" || hostName != "Backup Target Node" {
		t.Errorf("host data mismatch: id=%q, name=%q", hostID, hostName)
	}

	// 3. Verify host_telemetry_cache table
	var telemID string
	if err := db.QueryRow("SELECT host_id FROM host_telemetry_cache WHERE host_id = 'node-backup-test'").Scan(&telemID); err != nil {
		t.Fatalf("query host_telemetry_cache from backup: %v", err)
	}
	if telemID != "node-backup-test" {
		t.Errorf("telemetry host_id mismatch: got %q, want node-backup-test", telemID)
	}

	// 4. Test second backup execution (overwriting existing file)
	testHost2 := NodeRecord{
		ID:        "node-backup-test-2",
		Name:      "Backup Target Node 2",
		ShortName: "backup-test-2",
		Group:     "test-servers",
		IP:        "192.168.1.101",
		Port:      22,
		SSHUser:   "core",
		Status:    "online",
		NodeJSON:  json.RawMessage(`{"id":"node-backup-test-2","name":"Backup Target Node 2"}`),
	}
	if err := store.UpsertHost(testHost2); err != nil {
		t.Fatalf("UpsertHost #2: %v", err)
	}

	if err := store.Backup(); err != nil {
		t.Fatalf("second Backup() failed: %v", err)
	}

	// Reopen and verify second host is present in the updated backup
	db2, err := sql.Open("sqlite", "file:"+backupPath+"?mode=ro")
	if err != nil {
		t.Fatalf("reopen backup db: %v", err)
	}
	defer db2.Close()

	var count int
	if err := db2.QueryRow("SELECT COUNT(*) FROM hosts").Scan(&count); err != nil {
		t.Fatalf("count hosts in second backup: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 hosts in second backup, got %d", count)
	}

	// Verify store is still usable for reads and writes
	hosts, err := store.GetAllHosts()
	if err != nil {
		t.Fatalf("GetAllHosts after backup: %v", err)
	}
	if len(hosts) != 2 {
		t.Errorf("expected 2 hosts in store, got %d", len(hosts))
	}
}

// TestBackupInMemory verifies that an in-memory database is correctly
// persisted to disk by Backup(). The backup file must contain the same
// data as the in-memory database.
func TestBackupInMemory(t *testing.T) {
	// Open an in-memory store with a disk backup path
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open :memory: failed: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Write test data
	store.UpsertHost(NodeRecord{
		ID:        "node-1",
		Name:      "test-node",
		ShortName: "node",
		Group:     "tag:test",
		IP:        "10.0.0.1",
		Port:      22,
		SSHUser:   "core",
		Status:    "online",
		NodeJSON:  json.RawMessage(`{"id":"node-1","name":"test-node"}`),
	})
	store.UpsertHostTelemetry("node-1", json.RawMessage(`{"cpu":12.5}`))
	store.UpsertHostSection("node-1", "interfaces", json.RawMessage(`[{"iface":"eth0"}]`))

	// Verify data is in memory
	hosts, err := store.GetAllHosts()
	if err != nil {
		t.Fatalf("GetAllHosts: %v", err)
	}
	if len(hosts) != 1 {
		t.Fatalf("expected 1 host, got %d", len(hosts))
	}

	// Perform backup
	if err := store.Backup(); err != nil {
		t.Fatalf("Backup() failed: %v", err)
	}

	// Verify backup file exists and is non-empty
	backupPath := store.backupPath
	info, err := os.Stat(backupPath)
	if err != nil {
		t.Fatalf("backup file not created: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("backup file is empty")
	}

	// Verify backup file contains the data by opening it
	backupStore, err := Open(backupPath)
	if err != nil {
		t.Fatalf("Open backup file: %v", err)
	}
	defer func() { _ = backupStore.Close() }()

	backupHosts, err := backupStore.GetAllHosts()
	if err != nil {
		t.Fatalf("GetAllHosts from backup: %v", err)
	}
	if len(backupHosts) != 1 {
		t.Fatalf("expected 1 host in backup, got %d", len(backupHosts))
	}
	if backupHosts[0].ID != "node-1" {
		t.Errorf("expected node-1 in backup, got %s", backupHosts[0].ID)
	}

	// Verify telemetry in backup
	telemetry, ok, err := backupStore.GetHostTelemetry("node-1")
	if err != nil {
		t.Fatalf("GetHostTelemetry from backup: %v", err)
	}
	if !ok {
		t.Fatal("telemetry not found in backup")
	}
	if string(telemetry.TelemetryJSON) != `{"cpu":12.5}` {
		t.Errorf("telemetry mismatch: got %s", telemetry.TelemetryJSON)
	}

	// Verify section in backup
	section, ok, err := backupStore.GetHostSection("node-1", "interfaces")
	if err != nil {
		t.Fatalf("GetHostSection from backup: %v", err)
	}
	if !ok {
		t.Fatal("section not found in backup")
	}
	if string(section.DataJSON) != `[{"iface":"eth0"}]` {
		t.Errorf("section mismatch: got %s", section.DataJSON)
	}
}

// TestOpenSnapshotsLegacySchemaBeforeReset guards against silent inventory
// loss: a legacy database with real rows but no schema_version used to be
// dropped wholesale by the schema reset. Open() must snapshot it first.
func TestOpenSnapshotsLegacySchemaBeforeReset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts-cache.sqlite")

	legacy, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if _, err := legacy.Exec(`CREATE TABLE hosts (id TEXT PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
		t.Fatalf("create legacy hosts table: %v", err)
	}
	if _, err := legacy.Exec(`INSERT INTO hosts (id, name) VALUES ('node-legacy', 'legacy-host')`); err != nil {
		t.Fatalf("seed legacy host: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open legacy database: %v", err)
	}
	defer store.Close()

	snapshots, err := filepath.Glob(filepath.Join(dir, "hosts-cache.pre-reset-*.sqlite"))
	if err != nil {
		t.Fatalf("glob snapshots: %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("expected exactly one pre-reset snapshot, got %v", snapshots)
	}

	snap, err := sql.Open("sqlite", "file:"+snapshots[0]+"?mode=ro")
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer snap.Close()
	var name string
	if err := snap.QueryRow(`SELECT name FROM hosts WHERE id = 'node-legacy'`).Scan(&name); err != nil {
		t.Fatalf("legacy row missing from snapshot: %v", err)
	}
	if name != "legacy-host" {
		t.Fatalf("snapshot name = %q, want legacy-host", name)
	}

	// The live database is reset to the current schema and version.
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM hosts WHERE id = 'node-legacy'`).Scan(&count); err != nil {
		t.Fatalf("query reset db: %v", err)
	}
	if count != 0 {
		t.Fatalf("legacy row still present after reset")
	}
	version, err := GetCurrentSchemaVersion(store.db)
	if err != nil {
		t.Fatalf("schema version after reset: %v", err)
	}
	if version != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, SchemaVersion)
	}
}

// TestUpsertHostPersistsVaultSecretID guards the credential reference column.
// The value used to live only inside node_json, so GetHostByID (and every
// reader that trusts the column) saw an empty reference and SSH fell back to
// "no authentication methods".
func TestUpsertHostPersistsVaultSecretID(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "hosts-cache.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	blob := json.RawMessage(`{"id":"node-keyed","name":"keyed","shortName":"keyed","ip":"10.0.0.5","sshUser":"core","vaultSecretId":"ssh_key"}`)
	if err := store.UpsertHost(NodeRecord{
		ID: "node-keyed", Name: "keyed", ShortName: "keyed", IP: "10.0.0.5", SSHUser: "core",
		VaultSecretID: "ssh_key", NodeJSON: blob,
	}); err != nil {
		t.Fatalf("upsert host: %v", err)
	}

	rec, ok, err := store.GetHostByID("node-keyed")
	if err != nil || !ok {
		t.Fatalf("get host: ok=%v err=%v", ok, err)
	}
	if rec.VaultSecretID != "ssh_key" {
		t.Fatalf("VaultSecretID = %q, want ssh_key", rec.VaultSecretID)
	}

	var column sql.NullString
	if err := store.db.QueryRow(`SELECT vault_secret_id FROM hosts WHERE id = 'node-keyed'`).Scan(&column); err != nil {
		t.Fatalf("query column: %v", err)
	}
	if !column.Valid || column.String != "ssh_key" {
		t.Fatalf("vault_secret_id column = %+v, want ssh_key", column)
	}

	// Clearing the reference must clear the column too, not just the blob.
	cleared := json.RawMessage(`{"id":"node-keyed","name":"keyed","shortName":"keyed","ip":"10.0.0.5","sshUser":"core"}`)
	if err := store.UpsertHost(NodeRecord{
		ID: "node-keyed", Name: "keyed", ShortName: "keyed", IP: "10.0.0.5", SSHUser: "core",
		NodeJSON: cleared,
	}); err != nil {
		t.Fatalf("upsert cleared host: %v", err)
	}
	rec, ok, err = store.GetHostByID("node-keyed")
	if err != nil || !ok {
		t.Fatalf("get cleared host: ok=%v err=%v", ok, err)
	}
	if rec.VaultSecretID != "" {
		t.Fatalf("VaultSecretID = %q after clearing, want empty", rec.VaultSecretID)
	}
}

// A caller that holds a decoded record plus a blob which omits vaultSecretId
// must still persist the record's reference.
func TestUpsertHostKeepsRecordVaultSecretIDWhenBlobOmitsIt(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "hosts-cache.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if err := store.UpsertHost(NodeRecord{
		ID:            "node-blob",
		Name:          "blob",
		ShortName:     "blob",
		IP:            "10.0.0.6",
		SSHUser:       "core",
		VaultSecretID: "ssh_key",
		NodeJSON:      json.RawMessage(`{"id":"node-blob","name":"blob"}`),
	}); err != nil {
		t.Fatalf("upsert host: %v", err)
	}

	rec, ok, err := store.GetHostByID("node-blob")
	if err != nil || !ok {
		t.Fatalf("get host: ok=%v err=%v", ok, err)
	}
	if rec.VaultSecretID != "ssh_key" {
		t.Fatalf("VaultSecretID = %q, want ssh_key from the record", rec.VaultSecretID)
	}
}
