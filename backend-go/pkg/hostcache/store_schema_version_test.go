package hostcache

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestSchemaVersionIntegration tests the complete schema version reset lifecycle:
// 1. Fresh DB auto-initializes to SchemaVersion
// 2. Data persists when re-opening with same SchemaVersion
// 3. Data is reset when schema version changes (simulated by manual version update)
func TestSchemaVersionIntegration(t *testing.T) {
	// Use a temp directory that will be cleaned up automatically
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "schema_integration.sqlite")

	// ========================================================================
	// Phase 1: Fresh DB - should auto-initialize to SchemaVersion
	// ========================================================================
	store1, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open #1 (fresh DB): %v", err)
	}

	// Verify schema version is initialized
	version, err := GetCurrentSchemaVersion(store1.db)
	if err != nil {
		_ = store1.Close()
		t.Fatalf("GetCurrentSchemaVersion #1: %v", err)
	}
	if version != SchemaVersion {
		_ = store1.Close()
		t.Errorf("Phase 1: fresh DB version = %d, want %d", version, SchemaVersion)
	}

	// Insert a sample host record
	testHost := NodeRecord{
		ID:        "integration-host-1",
		Name:      "integration-host",
		ShortName: "int-host",
		Group:     "integration-group",
		IP:        "10.0.0.10",
		Port:      22,
		SSHUser:   "core",
		Status:    "online",
		NodeJSON:  json.RawMessage(`{"id":"integration-host-1","name":"integration-host","ip":"10.0.0.10"}`),
	}
	if err := store1.UpsertHost(testHost); err != nil {
		_ = store1.Close()
		t.Fatalf("UpsertHost: %v", err)
	}

	// Verify host was inserted
	hosts, err := store1.GetAllHosts()
	if err != nil {
		_ = store1.Close()
		t.Fatalf("GetAllHosts #1: %v", err)
	}
	if len(hosts) != 1 || hosts[0].ID != "integration-host-1" {
		_ = store1.Close()
		t.Fatalf("Phase 1: expected 1 host, got %v", hosts)
	}

	_ = store1.Close()

	// ========================================================================
	// Phase 2: Re-open with SAME SchemaVersion - data should PERSIST
	// ========================================================================
	store2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open #2 (same version): %v", err)
	}
	defer func() { _ = store2.Close() }()

	// Verify schema version still matches
	version, err = GetCurrentSchemaVersion(store2.db)
	if err != nil {
		t.Fatalf("GetCurrentSchemaVersion #2: %v", err)
	}
	if version != SchemaVersion {
		t.Errorf("Phase 2: version = %d, want %d", version, SchemaVersion)
	}

	// Verify host data persisted
	hosts, err = store2.GetAllHosts()
	if err != nil {
		t.Fatalf("GetAllHosts #2: %v", err)
	}
	if len(hosts) != 1 || hosts[0].ID != "integration-host-1" {
		t.Fatalf("Phase 2: data lost on re-open with same version: expected 1 host, got %v", hosts)
	}
	if hosts[0].Name != "integration-host" || hosts[0].IP != "10.0.0.10" {
		t.Errorf("Phase 2: host data corrupted: %+v", hosts[0])
	}

	// Add another host to verify we can continue writing
	secondHost := NodeRecord{
		ID:        "integration-host-2",
		Name:      "integration-host-2",
		ShortName: "int-host-2",
		Group:     "integration-group",
		IP:        "10.0.0.11",
		Port:      22,
		SSHUser:   "core",
		Status:    "online",
		NodeJSON:  json.RawMessage(`{"id":"integration-host-2","name":"integration-host-2","ip":"10.0.0.11"}`),
	}
	if err := store2.UpsertHost(secondHost); err != nil {
		t.Fatalf("UpsertHost #2: %v", err)
	}

	// Verify both hosts exist
	hosts, err = store2.GetAllHosts()
	if err != nil {
		t.Fatalf("GetAllHosts #2b: %v", err)
	}
	if len(hosts) != 2 {
		t.Fatalf("Phase 2b: expected 2 hosts, got %d", len(hosts))
	}

	_ = store2.Close()

	// ========================================================================
	// Phase 3: Simulate schema version change - manually update version,
	// then re-open - data should be RESET (wiped)
	// ========================================================================
	// Manually update schema_version to a different value (simulating a version bump)
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=rwc")
	if err != nil {
		t.Fatalf("sql.Open for manual version update: %v", err)
	}
	newVersion := SchemaVersion + 100 // Use a clearly different version
	if err := SetSchemaVersion(db, newVersion); err != nil {
		_ = db.Close()
		t.Fatalf("SetSchemaVersion to %d: %v", newVersion, err)
	}
	_ = db.Close()

	// Verify the version was set
	db, err = sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		t.Fatalf("sql.Open for version verify: %v", err)
	}
	version, err = GetCurrentSchemaVersion(db)
	if err != nil {
		_ = db.Close()
		t.Fatalf("GetCurrentSchemaVersion after manual update: %v", err)
	}
	if version != newVersion {
		_ = db.Close()
		t.Fatalf("Manual version update failed: got %d, want %d", version, newVersion)
	}
	_ = db.Close()

	// Now re-open via Store - should detect version mismatch and reset
	store3, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open #3 (version mismatch): %v", err)
	}
	defer func() { _ = store3.Close() }()

	// Verify schema version was reset to current SchemaVersion
	version, err = GetCurrentSchemaVersion(store3.db)
	if err != nil {
		t.Fatalf("GetCurrentSchemaVersion #3: %v", err)
	}
	if version != SchemaVersion {
		t.Errorf("Phase 3: version after reset = %d, want %d", version, SchemaVersion)
	}

	// Verify ALL previous data was wiped
	hosts, err = store3.GetAllHosts()
	if err != nil {
		t.Fatalf("GetAllHosts #3: %v", err)
	}
	if len(hosts) != 0 {
		t.Fatalf("Phase 3: data NOT wiped on version change: expected 0 hosts, got %d: %v", len(hosts), hosts)
	}

	// Verify tables are usable after reset by inserting new data
	newHost := NodeRecord{
		ID:        "post-reset-host",
		Name:      "post-reset-host",
		ShortName: "post-reset",
		Group:     "post-reset-group",
		IP:        "10.0.0.20",
		Port:      22,
		SSHUser:   "core",
		Status:    "online",
		NodeJSON:  json.RawMessage(`{"id":"post-reset-host","name":"post-reset-host","ip":"10.0.0.20"}`),
	}
	if err := store3.UpsertHost(newHost); err != nil {
		t.Fatalf("UpsertHost after reset: %v", err)
	}

	hosts, err = store3.GetAllHosts()
	if err != nil {
		t.Fatalf("GetAllHosts after reset: %v", err)
	}
	if len(hosts) != 1 || hosts[0].ID != "post-reset-host" {
		t.Fatalf("Phase 3: tables not usable after reset: %v", hosts)
	}

	// ========================================================================
	// Phase 4: Cleanup verification - file should still exist and be valid
	// ========================================================================
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Fatalf("Phase 4: database file was deleted: %v", err)
	}

	// Verify we can open read-only and see the post-reset data
	storeRO, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("OpenReadOnly after reset: %v", err)
	}
	defer func() { _ = storeRO.Close() }()

	hosts, err = storeRO.GetAllHosts()
	if err != nil {
		t.Fatalf("GetAllHosts read-only: %v", err)
	}
	if len(hosts) != 1 || hosts[0].ID != "post-reset-host" {
		t.Fatalf("Phase 4: read-only view mismatch: %v", hosts)
	}
}
