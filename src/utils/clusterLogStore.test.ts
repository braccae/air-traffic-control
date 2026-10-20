import test from 'node:test';
import assert from 'node:assert/strict';
import {
  CLUSTER_LOG_CAPACITY,
  __resetClusterLogForTests,
  installConsoleCapture,
  listClusterLogs,
  onClusterLog,
  recordClusterLog,
} from './clusterLogStore.ts';

test('records entries with monotonic ids and real timestamps', () => {
  __resetClusterLogForTests();
  const before = Date.now();
  const first = recordClusterLog({ kind: 'app', level: 'info', source: 'atc/coordinator', message: 'started' });
  const second = recordClusterLog({ kind: 'poll', level: 'info', source: 'poll:telemetry', host: 'likes-it-cold', message: 'ok' });
  const after = Date.now();

  assert.equal(first.id, 1);
  assert.equal(second.id, 2);
  assert.ok(first.ts >= before && first.ts <= after);
  assert.ok(second.ts >= first.ts);
});

test('rounds measured durations and keeps success flags', () => {
  __resetClusterLogForTests();
  const entry = recordClusterLog({
    kind: 'poll',
    level: 'error',
    source: 'poll:telemetry',
    host: 'ping',
    detail: 'glances-multi-tier',
    durationMs: 41.7,
    ok: false,
    message: 'glances-multi-tier failed in 42ms: timeout',
  });

  assert.equal(entry.durationMs, 42);
  assert.equal(entry.ok, false);
  assert.equal(listClusterLogs()[0].host, 'ping');
});

test('filters by kind, host and sinceId while clamping the limit', () => {
  __resetClusterLogForTests();
  recordClusterLog({ kind: 'app', level: 'info', source: 'atc/coordinator', message: 'a' });
  recordClusterLog({ kind: 'poll', level: 'info', source: 'poll:telemetry', host: 'a-host', message: 'b' });
  recordClusterLog({ kind: 'poll', level: 'info', source: 'poll:telemetry', host: 'b-host', message: 'c' });

  assert.equal(listClusterLogs({ kind: 'poll' }).length, 2);
  assert.equal(listClusterLogs({ host: 'a-host' }).length, 1);
  assert.equal(listClusterLogs({ sinceId: 1 }).length, 2);
  assert.equal(listClusterLogs({ limit: 1 }).length, 1);
  assert.equal(listClusterLogs({ limit: 100000 }).length, 3);
});

test('bounded ring buffer drops the oldest entries', () => {
  __resetClusterLogForTests();
  for (let i = 0; i < CLUSTER_LOG_CAPACITY + 5; i += 1) {
    recordClusterLog({ kind: 'app', level: 'info', source: 'atc/coordinator', message: `line-${i}` });
  }

  const all = listClusterLogs({ limit: CLUSTER_LOG_CAPACITY });
  assert.equal(all.length, CLUSTER_LOG_CAPACITY);
  assert.equal(all[0].id, 6);
  assert.equal(all[all.length - 1].id, CLUSTER_LOG_CAPACITY + 5);
});

test('notifies subscribers and stops after unsubscribe', () => {
  __resetClusterLogForTests();
  const seen: number[] = [];
  const unsubscribe = onClusterLog(entry => seen.push(entry.id));

  recordClusterLog({ kind: 'app', level: 'info', source: 'atc/coordinator', message: 'one' });
  unsubscribe();
  recordClusterLog({ kind: 'app', level: 'info', source: 'atc/coordinator', message: 'two' });

  assert.deepEqual(seen, [1]);
});

test('console capture records real app logs and preserves console output', () => {
  __resetClusterLogForTests();

  const written: unknown[][] = [];
  const originalLog = console.log;
  console.log = (...args: unknown[]) => {
    written.push(args);
  };

  try {
    installConsoleCapture();
    console.log('coordinator listening on %d', 3000);
  } finally {
    console.log = originalLog;
  }

  assert.deepEqual(written, [['coordinator listening on %d', 3000]]);

  const entries = listClusterLogs({ kind: 'app' });
  assert.equal(entries.length, 1);
  assert.equal(entries[0].kind, 'app');
  assert.equal(entries[0].source, 'atc/coordinator');
  assert.equal(entries[0].level, 'info');
  assert.equal(entries[0].message, 'coordinator listening on 3000');
});
