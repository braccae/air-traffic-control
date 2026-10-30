import test from 'node:test';
import assert from 'node:assert/strict';
import { findLocalNode, resolveModuleNodeId, isLocalTarget } from './nodeTarget.ts';
import type { NodeItem } from '../types/index.ts';

function makeNode(overrides: Partial<NodeItem> = {}): NodeItem {
  const base = {
    id: 'node-dev-pc',
    name: 'dev-pc (local)',
    shortName: 'dev-pc',
    group: 'tag:host',
    datacenter: 'leo-banjo.ts.net',
    nodeType: 'linux',
    badge: 'LOCAL',
    badgeColor: '#b0c9ff',
    ip: '127.0.0.1',
    port: 22,
    sshUser: 'pants',
    authMethod: 'local-socket / ssh-agent',
    tags: ['tag:host'],
    sshConnected: true,
    rttMs: '0.1ms',
    tailscaleIp: '100.97.159.101',
    tailscaleHost: 'dev-pc.leo-banjo.ts.net',
    osDescription: 'CachyOS Linux (6.19.11-1-cachyos)',
    status: 'online',
    uptime: '1d 22h',
    hasGlances: true,
    hasCockpit: true,
    hasPodman: true,
    hasRestic: true,
    specs: {
      cpu: 'AMD Ryzen 5 5600X',
      vcpus: 12,
      ramGiB: 32,
      motherboard: 'ASUS ROG STRIX B450-F',
      bios: 'ASUS 4801',
      ipmi: 'Local Bus',
      ecc: 'Non-ECC',
      secureBoot: 'Disabled',
      iommu: 'Enabled',
    },
  };
  return { ...base, ...overrides } as NodeItem;
}

test('isLocalTarget recognizes localhost aliases', () => {
  assert.equal(isLocalTarget('localhost'), true);
  assert.equal(isLocalTarget('127.0.0.1'), true);
  assert.equal(isLocalTarget('::1'), true);
  assert.equal(isLocalTarget('dev-pc'), false);
  assert.equal(isLocalTarget('laptop.leo-banjo.ts.net'), false);
});

test('findLocalNode returns the registered local node by address', () => {
  const nodes = [makeNode({ id: 'node-worker-1', shortName: 'worker-1', ip: '192.168.8.10', tailscaleHost: 'worker-1.leo-banjo.ts.net' }), makeNode()];
  assert.ok(findLocalNode(nodes));
  assert.equal(findLocalNode(nodes)!.id, 'node-dev-pc');
});

test('findLocalNode returns the registered local node by dev-pc short name', () => {
  const nodes = [makeNode({ id: 'node-worker-1', shortName: 'worker-1', ip: '10.0.0.5', tailscaleHost: 'worker-1.leo-banjo.ts.net' }), makeNode({ shortName: 'dev-pc' })];
  assert.equal(findLocalNode(nodes)?.id, 'node-dev-pc');
});

test('findLocalNode returns null when no local node is registered', () => {
  const nodes = [makeNode({ id: 'node-worker-1', shortName: 'worker-1', ip: '192.168.8.10', tailscaleHost: 'worker-1.leo-banjo.ts.net' })];
  assert.equal(findLocalNode(nodes), null);
});

test('resolveModuleNodeId leaves non-localhost targets unchanged', () => {
  const nodes = [makeNode({ id: 'node-abc123', shortName: 'dev-pc' })];
  assert.equal(resolveModuleNodeId('dev-pc', nodes), 'dev-pc');
});

test('resolveModuleNodeId never returns a fabricated local-host id', () => {
  const nodes = [makeNode({ id: 'node-abc123', shortName: 'dev-pc' })];
  assert.equal(resolveModuleNodeId('local-host', nodes), '');
  assert.equal(resolveModuleNodeId('local_host', nodes), '');
});

test('resolveModuleNodeId resolves a localhost alias to the real local node id', () => {
  const nodes = [makeNode({ id: 'node-abc123', shortName: 'dev-pc' })];
  assert.equal(resolveModuleNodeId('localhost', nodes), 'node-abc123');
  assert.equal(resolveModuleNodeId('127.0.0.1', nodes), 'node-abc123');
});

test('resolveModuleNodeId returns an existing id unchanged', () => {
  const nodes = [makeNode({ id: 'node-abc123' })];
  assert.equal(resolveModuleNodeId('node-abc123', nodes), 'node-abc123');
});
