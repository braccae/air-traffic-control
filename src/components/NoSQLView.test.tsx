import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import { renderToString } from 'react-dom/server';
import { NoSQLView } from './NoSQLView.tsx';
import type { NodeItem } from '../types/index.ts';

const mockNode: NodeItem = {
  id: 'node-test-1',
  name: 'Test Node',
  shortName: 'test-1',
  group: 'servers',
  datacenter: 'dc1',
  nodeType: 'linux',
  ip: '192.168.1.100',
  port: 22,
  tailscaleIp: '100.64.0.1',
  tailscaleHost: 'test-1.leo-banjo.ts.net',
  osDescription: 'Fedora 40',
  status: 'online',
  uptime: '2 days',
  hasGlances: true,
  hasCockpit: false,
  hasPodman: true,
  hasRestic: true,
  specs: {
    cpu: 'AMD Ryzen',
    vcpus: 8,
    ramGiB: 32,
    motherboard: 'ASRock',
    bios: 'v1.0',
    ipmi: 'none',
    ecc: 'yes',
    secureBoot: 'disabled',
    iommu: 'enabled',
  },
};

test('NoSQLView renders header and engine selector tabs', () => {
  const html = renderToString(
    React.createElement(NoSQLView, {
      node: mockNode,
      pluginId: 'database/nosql',
    })
  );

  assert.ok(html.includes('NoSQL Database Management'), 'Must render NoSQL Database Management title');
  assert.ok(html.includes('Redis / Valkey'), 'Must render Redis / Valkey engine tab');
  assert.ok(html.includes('mongodb'), 'Must render mongodb engine tab');
  assert.ok(html.includes('couchdb'), 'Must render couchdb engine tab');
  assert.ok(html.includes('rocksdb'), 'Must render rocksdb engine tab');
});

test('NoSQLView displays Raw Query Workbench and action buttons', () => {
  const html = renderToString(
    React.createElement(NoSQLView, {
      node: mockNode,
      pluginId: 'database/nosql',
      initialData: {
        engines: [
          {
            engine: 'redis',
            status: 'active',
            version: '7.2.4',
            databases: ['db0', 'db1'],
            users: [{ username: 'default', roles: ['on', '~*', '+@all'] }],
            metrics: { used_memory_human: '14M' },
            dataFormat: 'keyvalue',
          },
        ],
        count: 1,
      },
    })
  );

  assert.ok(html.includes('Raw Query Workbench'), 'Must render Raw Query Workbench');
  assert.ok(html.includes('Run Query'), 'Must render Run Query button');
  assert.ok(html.includes('Databases / Keyspaces'), 'Must render Databases / Keyspaces section');
  assert.ok(html.includes('Users &amp; Access Controls') || html.includes('Users & Access Controls'), 'Must render Users section');
});
