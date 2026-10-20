package hostcache

import "database/sql"

// SQLDB returns the underlying pooled *sql.DB handle for the cache file.
//
// The handle is shared with this Store: it is a single-connection pool (WAL,
// busy_timeout, schema already applied) and callers MUST NOT close it, change
// its pool size, or hold it across long-running work — a playbook run that
// holds the connection starves every other cache reader/writer in the process.
func (s *Store) SQLDB() *sql.DB { return s.db }

// DeleteHostContainerCache removes the cached container/pod row for a host so
// the next probe re-reads real state from the node.
func (s *Store) DeleteHostContainerCache(hostID string) error {
	if hostID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`DELETE FROM host_containers_cache WHERE host_id = ?`, hostID)
	return err
}

// DeleteHostTelemetry removes the cached telemetry row for a host. The 15s
// telemetry tier cannot survive a mutation that may change any live metric.
func (s *Store) DeleteHostTelemetry(hostID string) error {
	if hostID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`DELETE FROM host_telemetry_cache WHERE host_id = ?`, hostID)
	return err
}

// DeleteHostSections removes the named host_section_cache rows for a host in a
// single transaction. An empty section list is a no-op (an empty hostID would
// target every host, which is never what a targeted invalidation means).
func (s *Store) DeleteHostSections(hostID string, sections []string) error {
	if hostID == "" || len(sections) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, section := range sections {
		if _, err := tx.Exec(`DELETE FROM host_section_cache WHERE host_id = ? AND section = ?`, hostID, section); err != nil {
			return err
		}
	}
	return tx.Commit()
}
