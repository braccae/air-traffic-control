import test from 'node:test';
import assert from 'node:assert/strict';
import {
  shortenPluginLabel,
  expandDatabaseEntries,
  extractDetectedEngines,
  type PluginLike,
} from './pluginLabels.ts';

function plugin(overrides: Partial<PluginLike> = {}): PluginLike {
  return {
    id: 'database/sql',
    kind: 'database',
    name: 'sql',
    label: 'SQL Databases',
    icon: 'database',
    section: 'database',
    order: 9,
    detection: { detected: true, status: 'ONLINE', details: '' },
    ...overrides,
  };
}

test('shortenPluginLabel drops redundant group words from user-facing labels', () => {
  assert.strictEqual(
    shortenPluginLabel('System Services & Power', 'service', 'system'),
    'Services'
  );
  assert.strictEqual(
    shortenPluginLabel('Podman & Rootless Containers', 'podman', 'container'),
    'Podman'
  );
  assert.strictEqual(shortenPluginLabel('Zfs Storage', 'zfs', 'storage'), 'ZFS');
  assert.strictEqual(shortenPluginLabel('Btrfs Storage', 'btrfs', 'storage'), 'Btrfs');
  assert.strictEqual(shortenPluginLabel('LVM Storage', 'lvm', 'storage'), 'LVM');
  assert.strictEqual(shortenPluginLabel('Docker & Containers', 'docker', 'container'), 'Docker');
  assert.strictEqual(shortenPluginLabel('LXC Containers', 'lxc', 'container'), 'LXC');
  assert.strictEqual(shortenPluginLabel('Firewalld Rules', 'firewalld', 'firewall'), 'Firewall');
});

test('shortenPluginLabel falls back to stripping the group suffix for unknown names', () => {
  assert.strictEqual(shortenPluginLabel('Mystery Storage', 'mystery', 'storage'), 'Mystery');
  assert.strictEqual(shortenPluginLabel('Mystery Containers', 'mystery', 'container'), 'Mystery');
  assert.strictEqual(shortenPluginLabel('Mystery Databases', 'mystery', 'database'), 'Mystery');
  assert.strictEqual(shortenPluginLabel('Untouched Label', 'other', 'network'), 'Untouched Label');
});

test('extractDetectedEngines prefers the details payload and falls back to status', () => {
  assert.deepStrictEqual(
    extractDetectedEngines({ detected: true, status: 'ONLINE (x)', details: 'Engines: mariadb, postgres' }),
    ['mariadb', 'postgres']
  );
  assert.deepStrictEqual(
    extractDetectedEngines({ detected: true, status: 'ONLINE (mongodb, redis)' }),
    ['mongodb', 'redis']
  );
  assert.deepStrictEqual(
    extractDetectedEngines({ detected: true, status: 'ONLINE' }),
    []
  );
});

test('expandDatabaseEntries expands detected SQL engines into friendly per-engine entries', () => {
  const entries = expandDatabaseEntries(
    plugin({
      detection: {
        detected: true,
        status: 'ONLINE (mariadb, postgres)',
        details: 'Engines: mariadb, postgres',
      },
    })
  );

  assert.strictEqual(entries.length, 2);
  assert.deepStrictEqual(entries.map(e => e.label), ['MariaDB', 'Postgres']);
  // Entries keep the original id/name so clicking opens the same view.
  assert.ok(entries.every(e => e.id === 'database/sql' && e.name === 'sql'));
  assert.deepStrictEqual(entries.map(e => e.engine), ['mariadb', 'postgres']);
});

test('expandDatabaseEntries deduplicates engines and maps MySQL/MSSQL/NoSQL names', () => {
  const sqlEntries = expandDatabaseEntries(
    plugin({
      detection: {
        detected: true,
        status: 'ONLINE',
        details: 'Engines: mysql, mysql, mssql',
      },
    })
  );
  assert.deepStrictEqual(sqlEntries.map(e => e.label), ['MySQL', 'MSSQL']);

  const nosqlEntries = expandDatabaseEntries(
    plugin({
      id: 'database/nosql',
      name: 'nosql',
      label: 'NoSQL Databases',
      detection: { detected: true, status: 'ONLINE (mongodb, redis, couchdb, rocksdb)' },
    })
  );
  assert.deepStrictEqual(nosqlEntries.map(e => e.label), ['MongoDB', 'Redis', 'CouchDB', 'RocksDB']);
});

test('expandDatabaseEntries never fabricates engines when the payload has none', () => {
  const entries = expandDatabaseEntries(
    plugin({ detection: { detected: true, status: 'ONLINE', details: '' } })
  );
  assert.strictEqual(entries.length, 1);
  assert.strictEqual(entries[0].label, 'SQL');
  assert.strictEqual(entries[0].engine, undefined);
});

test('expandDatabaseEntries leaves undetected and non-database plugins untouched', () => {
  const undetected = plugin({
    detection: { detected: false, status: 'UNAVAILABLE', details: 'No supported SQL database engines found' },
  });
  assert.deepStrictEqual(expandDatabaseEntries(undetected), [undetected]);

  const storage = plugin({
    id: 'storage/zfs',
    kind: 'storage',
    name: 'zfs',
    label: 'ZFS Storage',
    section: 'storage',
  });
  assert.deepStrictEqual(expandDatabaseEntries(storage), [storage]);
});
