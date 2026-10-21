package hostcache

// schemaDDL is the verbatim schema from src/utils/hostCacheDb.ts. It is the
// source of truth for the shared database: do not rename, reorder or drop
// anything without changing the TS side first.
//
// NOTE: modernc.org/sqlite executes each statement in exec separately, so the
// PRAGMAs are applied on open via the DSN (see openStore), not here.
const schemaDDL = `
CREATE TABLE IF NOT EXISTS hosts (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  short_name TEXT NOT NULL,
  group_name TEXT NOT NULL,
  ip TEXT NOT NULL,
  port INTEGER NOT NULL,
  ssh_user TEXT NOT NULL,
  tailscale_ip TEXT NOT NULL,
  tailscale_host TEXT NOT NULL,
  status TEXT NOT NULL,
  sort_order INTEGER NOT NULL DEFAULT 0,
  node_json TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS host_telemetry_cache (
  host_id TEXT PRIMARY KEY,
  telemetry_json TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS host_containers_cache (
  host_id TEXT PRIMARY KEY,
  probed_users_json TEXT NOT NULL DEFAULT '[]',
  identities_json TEXT NOT NULL DEFAULT '[]',
  custom_identities_json TEXT NOT NULL DEFAULT '[]',
  root_containers_json TEXT NOT NULL DEFAULT '[]',
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS group_vars_cache (
  file_path TEXT PRIMARY KEY,
  content TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS host_section_cache (
  host_id TEXT NOT NULL,
  section TEXT NOT NULL,
  data_json TEXT NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (host_id, section)
);

CREATE TABLE IF NOT EXISTS tasks (
  id TEXT PRIMARY KEY,
  start_time TEXT NOT NULL,
  node_target TEXT NOT NULL,
  user TEXT NOT NULL,
  description TEXT NOT NULL,
  progress INTEGER NOT NULL,
  progress_eta TEXT,
  status TEXT NOT NULL,
  category TEXT NOT NULL,
  log_json TEXT,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS inventory_folders (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS tag_inventory_chunks (
  id TEXT PRIMARY KEY,
  tag TEXT NOT NULL,
  ssh_user TEXT,
  vault_secret_id TEXT,
  auth_method TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

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

CREATE INDEX IF NOT EXISTS idx_hosts_sort ON hosts(sort_order ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_hosts_tailscale ON hosts(tailscale_host, tailscale_ip);
CREATE INDEX IF NOT EXISTS idx_sections_host ON host_section_cache(host_id, section);
CREATE INDEX IF NOT EXISTS idx_telemetry_updated ON host_telemetry_cache(updated_at);
CREATE INDEX IF NOT EXISTS idx_tasks_updated ON tasks(updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_folders_name ON inventory_folders(name ASC);
CREATE INDEX IF NOT EXISTS idx_chunks_created ON tag_inventory_chunks(created_at ASC);
CREATE INDEX IF NOT EXISTS idx_ansible_queue_status ON ansible_queue_tasks(status, created_at ASC);
`

// schemaEvolve mirrors the TS ALTER TABLE block: later versions of the cache
// added three columns to host_containers_cache. Each ALTER is best-effort so a
// freshly created table and an already-migrated table end up identical. The
// resulting column order is host_id, probed_users_json, identities_json,
// custom_identities_json, root_containers_json, updated_at, then the three
// appended columns — matching the on-disk database exactly.
var schemaEvolve = []string{
	`ALTER TABLE host_containers_cache ADD COLUMN root_pods_json TEXT DEFAULT '[]';`,
	`ALTER TABLE host_containers_cache ADD COLUMN has_docker INTEGER DEFAULT 0;`,
	`ALTER TABLE host_containers_cache ADD COLUMN docker_containers_json TEXT DEFAULT '[]';`,
}
