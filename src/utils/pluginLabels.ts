// Pure label helpers for the per-node plugin sidebar.
//
// These helpers deliberately carry no React dependency so they can be unit
// tested directly and reused by other plugin views.
//
// Zero-Mock invariant: per-engine database labels are derived strictly from
// the daemon's real detection payload (`detection.details` / `detection.status`).
// No engine names are invented when the payload does not carry them.

export interface PluginDetection {
  detected: boolean;
  status: string;
  details?: string;
  error?: string;
}

export interface PluginLike {
  id: string;
  kind: string;
  name: string;
  label: string;
  icon: string;
  section: string;
  order: number;
  detection: PluginDetection;
  /** Set only on expanded per-engine database entries. */
  engine?: string;
}

// Friendly engine names keyed by the short engine identifiers emitted by the
// Go database plugins (backend-go/plugins/database/{sql,nosql}).
const ENGINE_LABELS: Record<string, string> = {
  postgres: 'Postgres',
  postgresql: 'Postgres',
  mariadb: 'MariaDB',
  mysql: 'MySQL',
  mssql: 'MSSQL',
  sqlserver: 'MSSQL',
  mongodb: 'MongoDB',
  mongo: 'MongoDB',
  redis: 'Redis',
  valkey: 'Valkey',
  couchdb: 'CouchDB',
  rocksdb: 'RocksDB',
};

// Short labels keyed by plugin name/id. Labels sit inside already-labelled
// groups, so the group word ("Storage", "Containers", "Databases") is redundant.
const SHORT_NAMES: Record<string, string> = {
  service: 'Services',
  zfs: 'ZFS',
  btrfs: 'Btrfs',
  lvm: 'LVM',
  podman: 'Podman',
  docker: 'Docker',
  lxc: 'LXC',
  firewalld: 'Firewall',
  iptables: 'iptables',
  nftables: 'nftables',
  ufw: 'UFW',
  sql: 'SQL',
  nosql: 'NoSQL',
  journald: 'Logs',
  glances: 'Glances',
  interfaces: 'Interfaces',
  tailscale: 'Tailscale',
};

function capitalize(value: string): string {
  if (!value) return value;
  return value.charAt(0).toUpperCase() + value.slice(1);
}

function splitEngineList(value: string): string[] {
  const seen = new Set<string>();
  const engines: string[] = [];
  for (const raw of value.split(',')) {
    const engine = raw.trim().toLowerCase();
    if (!engine || seen.has(engine)) continue;
    seen.add(engine);
    engines.push(engine);
  }
  return engines;
}

/**
 * Extract the real detected engine identifiers from a plugin detection
 * payload. Prefers the structured `Engines: ...` details string, then falls
 * back to the `ONLINE (...)` status string produced by the Go probes.
 */
export function extractDetectedEngines(detection: PluginDetection): string[] {
  const details = (detection.details || '').trim();
  if (details) {
    const match = details.match(/engines?:\s*(.+)$/i);
    if (match) {
      const engines = splitEngineList(match[1]);
      if (engines.length > 0) return engines;
    }
  }

  const status = (detection.status || '').trim();
  if (status) {
    const match = status.match(/\(([^)]+)\)/);
    if (match) {
      const engines = splitEngineList(match[1]);
      if (engines.length > 0) return engines;
    }
  }

  return [];
}

/**
 * Shorten a plugin label by dropping the redundant group word. Matching is
 * done on the plugin name first so it stays robust to label text changes.
 */
export function shortenPluginLabel(label: string, name: string, section: string): string {
  const nameKey = (name || '').toLowerCase();
  const short = SHORT_NAMES[nameKey];
  if (short) return short;

  let shortened = (label || '').trim();
  if (shortened === 'System Services & Power') return 'Services';

  const sectionKey = (section || '').toLowerCase();
  if (sectionKey === 'storage') {
    shortened = shortened.replace(/\s+Storage$/i, '');
  } else if (sectionKey === 'container') {
    shortened = shortened
      .replace(/\s*&\s*Rootless Containers$/i, '')
      .replace(/\s+Containers$/i, '');
  } else if (sectionKey === 'database') {
    shortened = shortened.replace(/\s+Databases?$/i, '');
  }

  return shortened || label;
}

/**
 * Expand a detected database plugin into one sidebar entry per real detected
 * engine. Each entry keeps the original plugin id/name so clicking it opens the
 * same SQL/NoSQL view. Undetected plugins, non-database plugins, and payloads
 * without a readable engine list yield a single (shortened) entry.
 */
export function expandDatabaseEntries(plugin: PluginLike): PluginLike[] {
  const sectionKey = (plugin.section || plugin.kind || '').toLowerCase();
  if (sectionKey !== 'database' || !plugin.detection?.detected) {
    return [plugin];
  }

  const engines = extractDetectedEngines(plugin.detection);
  if (engines.length === 0) {
    return [
      {
        ...plugin,
        label: shortenPluginLabel(plugin.label, plugin.name, plugin.section),
      },
    ];
  }

  return engines.map(engine => ({
    ...plugin,
    engine,
    label: ENGINE_LABELS[engine] || capitalize(engine),
  }));
}
