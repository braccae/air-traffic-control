import test from 'node:test';
import assert from 'node:assert/strict';
import {
  __resetConnectionRegistryForTests,
  beginConnection,
  getConnectionSnapshot,
  onConnectionChange,
} from './connectionRegistry.ts';

test('counts open connections per kind and in total', () => {
  __resetConnectionRegistryForTests();

  const releaseSshA = beginConnection('ssh', 'core@100.64.0.1:22');
  const releaseSshB = beginConnection('ssh', 'core@100.64.0.2:22');
  const releaseWs = beginConnection('websocket', 'ws /ws');

  const snapshot = getConnectionSnapshot();
  assert.equal(snapshot.byKind.ssh, 2);
  assert.equal(snapshot.byKind.websocket, 1);
  assert.equal(snapshot.byKind.winrm, 0);
  assert.equal(snapshot.total, 3);
  assert.deepEqual(snapshot.details.ssh.sort(), ['core@100.64.0.1:22', 'core@100.64.0.2:22']);

  releaseSshA();
  releaseSshB();
  releaseWs();
  assert.equal(getConnectionSnapshot().total, 0);
});

test('release is idempotent and never underflows', () => {
  __resetConnectionRegistryForTests();

  const release = beginConnection('tailscale', 'tailscale status --json');
  release();
  release();
  release();

  const snapshot = getConnectionSnapshot();
  assert.equal(snapshot.byKind.tailscale, 0);
  assert.equal(snapshot.total, 0);
});

test('groups repeated labels and notifies subscribers on change', () => {
  __resetConnectionRegistryForTests();

  const snapshots: number[] = [];
  const unsubscribe = onConnectionChange(snapshot => snapshots.push(snapshot.total));

  const releaseA = beginConnection('local', 'dev-pc');
  const releaseB = beginConnection('local', 'dev-pc');

  assert.equal(snapshots.at(-1), 2);
  assert.deepEqual(getConnectionSnapshot().details.local, ['dev-pc x2']);

  unsubscribe();
  releaseA();
  releaseB();

  assert.equal(snapshots.length, 2);
  assert.equal(getConnectionSnapshot().total, 0);
});
