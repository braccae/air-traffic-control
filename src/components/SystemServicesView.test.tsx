import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import { renderToString } from 'react-dom/server';
import { SystemServicesView } from './SystemServicesView.tsx';
import type { NodeItem } from '../types/index.ts';

const mockNode: NodeItem = {
  id: 'node-test-1',
  name: 'prod-edge-01',
  shortName: 'edge-01',
  group: 'servers',
  datacenter: 'dc1',
  nodeType: 'linux',
  ip: '192.168.1.100',
  port: 22,
  tailscaleIp: '100.64.0.10',
  tailscaleHost: 'prod-edge-01.ts.net',
  osDescription: 'Fedora 40',
  status: 'online',
  uptime: '5 days',
  hasGlances: true,
  hasCockpit: false,
  hasPodman: true,
  hasRestic: true,
  specs: {
    cpu: '8 Cores',
    vcpus: 8,
    ramGiB: 16,
    motherboard: 'Supermicro X11SSH',
    bios: 'v2.4a',
    ipmi: 'Enabled',
    ecc: 'Yes',
    secureBoot: 'Enabled',
    iommu: 'Active',
  },
};

test('SystemServicesView renders header, daemon reload, reboot, and metric cards', () => {
  const html = renderToString(
    React.createElement(SystemServicesView, {
      node: mockNode,
    })
  );

  assert.ok(html.includes('System Services &amp; Lifecycle') || html.includes('System Services'), 'Must render title');
  assert.ok(html.includes('Daemon Reload'), 'Must render Daemon Reload button');
  assert.ok(html.includes('Reboot'), 'Must render Reboot button');
  assert.ok(html.includes('Total Services'), 'Must render Total Services stat');
  assert.ok(html.includes('Running'), 'Must render Running stat');
  assert.ok(html.includes('Failed'), 'Must render Failed stat');
});
