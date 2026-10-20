package plugin

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"

	_ "modernc.org/sqlite"
)

// SettingsStore persists global settings page values in SQLite. It is the single
// real backing store for every module-contributed settings page, so a settings
// page is never backed by placeholder state — values written through the API are
// read back from disk.
type SettingsStore struct {
	dbPath string
	db     *sql.DB
}

var (
	globalSettingsMu sync.RWMutex
	globalSettings   *SettingsStore
)

// SetGlobalSettingsStore installs the process-wide settings store so resident
// plugins can read their own persisted global settings during Read/Compile.
func SetGlobalSettingsStore(s *SettingsStore) {
	globalSettingsMu.Lock()
	defer globalSettingsMu.Unlock()
	globalSettings = s
}

// GlobalSettingsStore returns the process-wide settings store, or nil when none
// has been installed (for example inside unit tests).
func GlobalSettingsStore() *SettingsStore {
	globalSettingsMu.RLock()
	defer globalSettingsMu.RUnlock()
	return globalSettings
}

// NewSettingsStore opens (creating if necessary) the SQLite database at dbPath
// and ensures the global_settings table exists. WAL journaling and NORMAL
// synchronous mode follow the repository persistence rules.
func NewSettingsStore(dbPath string) (*SettingsStore, error) {
	if dbPath == "" {
		dbPath = ResolveDBPath("")
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open settings db %s: %w", dbPath, err)
	}
	// modernc.org/sqlite serialises access on a single connection pool; cap it
	// so WAL file locking stays predictable across the daemon and coordinator.
	db.SetMaxOpenConns(1)

	s := &SettingsStore{dbPath: dbPath, db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SettingsStore) migrate() error {
	stmts := []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA busy_timeout = 5000;",
		`CREATE TABLE IF NOT EXISTS global_settings (
			page_id    TEXT NOT NULL,
			field      TEXT NOT NULL,
			value      TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY (page_id, field)
		);`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("settings migrate %q: %w", q, err)
		}
	}
	return nil
}

// Close releases the underlying database handle.
func (s *SettingsStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DBPath reports the resolved database path (for diagnostics).
func (s *SettingsStore) DBPath() string {
	if s == nil {
		return ""
	}
	return s.dbPath
}

// Load returns the persisted values for a page. Only fields that have been
// written are present; callers overlay schema defaults using the page fields.
func (s *SettingsStore) Load(pageID string) (map[string]any, error) {
	if s == nil {
		return map[string]any{}, nil
	}
	rows, err := s.db.Query(`SELECT field, value FROM global_settings WHERE page_id = ?`, pageID)
	if err != nil {
		return nil, fmt.Errorf("load settings for %s: %w", pageID, err)
	}
	defer rows.Close()

	out := make(map[string]any)
	for rows.Next() {
		var field, raw string
		if err := rows.Scan(&field, &raw); err != nil {
			return nil, fmt.Errorf("scan settings for %s: %w", pageID, err)
		}
		out[field] = decodeStoredValue(raw)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Save writes the supplied field values for a page in a single transaction,
// upserting each row.
func (s *SettingsStore) Save(pageID string, values map[string]any) error {
	if s == nil {
		return fmt.Errorf("settings store unavailable")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin settings tx for %s: %w", pageID, err)
	}
	for name, val := range values {
		raw, err := json.Marshal(val)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("encode setting %s.%s: %w", pageID, name, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO global_settings (page_id, field, value, updated_at)
			 VALUES (?, ?, ?, datetime('now'))
			 ON CONFLICT(page_id, field)
			 DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			pageID, name, string(raw),
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("persist setting %s.%s: %w", pageID, name, err)
		}
	}
	return tx.Commit()
}

// Value returns a single persisted setting as a string. It reports false when the
// store is unavailable or the field has never been written.
func (s *SettingsStore) Value(pageID, field string) (string, bool) {
	if s == nil {
		return "", false
	}
	var raw string
	if err := s.db.QueryRow(
		`SELECT value FROM global_settings WHERE page_id = ? AND field = ?`,
		pageID, field,
	).Scan(&raw); err != nil {
		return "", false
	}
	if v := decodeStoredValue(raw); v != nil {
		if str, ok := v.(string); ok {
			return str, true
		}
		return fmt.Sprint(v), true
	}
	return raw, true
}

func decodeStoredValue(raw string) any {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		// Tolerate values written by tooling that stored a bare string.
		return raw
	}
	return v
}
