import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import { renderToString } from 'react-dom/server';
import { CockpitSubSidebar } from './CockpitSubSidebar.tsx';
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
    motherboard: 'ASRock Rack',
    bios: 'v1.2',
    ipmi: 'none',
    ecc: 'yes',
    secureBoot: 'disabled',
    iommu: 'enabled',
  },
};

test('CockpitSubSidebar always renders Overview and Terminal tabs', () => {
  const html = renderToString(
    React.createElement(CockpitSubSidebar, {
      node: mockNode,
      activeItem: 'overview',
      onSelectItem: () => {},
    })
  );

  assert.ok(html.includes('Overview'), 'Must always include Overview tab');
  assert.ok(html.includes('Terminal'), 'Must always include Terminal tab');
});

test('CockpitSubSidebar always pins Modules tab at the bottom', () => {
  const html = renderToString(
    React.createElement(CockpitSubSidebar, {
      node: mockNode,
      activeItem: 'overview',
      onSelectItem: () => {},
    })
  );

  assert.ok(html.includes('Modules'), 'Must include Modules tab');
  assert.ok(html.includes('extension') || html.includes('view_module'), 'Must include modules icon');
});

test('CockpitSubSidebar does NOT render undetected plugins or default to zfs when plugins are missing', () => {
  const html = renderToString(
    React.createElement(CockpitSubSidebar, {
      node: mockNode,
      activeItem: 'overview',
      plugins: [], // explicitly empty plugins detected
      onSelectItem: () => {},
    })
  );

  assert.strictEqual(html.includes('ZFS Pools') || html.includes('ZFS Storage'), false, 'Undetected ZFS must NOT render');
  assert.strictEqual(html.includes('Btrfs & Storage'), false, 'Undetected Btrfs must NOT render');
  assert.strictEqual(html.includes('NFS & SMB Shares'), false, 'Undetected Shares must NOT render');
  assert.strictEqual(html.includes('Podman & Pods'), false, 'Undetected Podman must NOT render');
  assert.strictEqual(html.includes('Restic Snapshots'), false, 'Undetected Restic must NOT render');
});

test('CockpitSubSidebar renders detected plugins grouped dynamically by section/kind with badges', () => {
  const mockPlugins = [
    {
      id: 'storage/zfs',
      kind: 'storage',
      name: 'zfs',
      label: 'ZFS Storage',
      icon: 'inventory_2',
      section: 'storage',
      order: 10,
      detection: { detected: true, status: '3 pools' },
    },
    {
      id: 'share/nfs',
      kind: 'share',
      name: 'nfs',
      label: 'NFS Exports',
      icon: 'folder_shared',
      section: 'share',
      order: 20,
      detection: { detected: true, status: 'ACTIVE' },
    },
    {
      id: 'container/podman',
      kind: 'container',
      name: 'podman',
      label: 'Podman Containers',
      icon: 'token',
      section: 'container',
      order: 10,
      detection: { detected: true, status: '4 running' },
    },
  ];

  const html = renderToString(
    React.createElement(CockpitSubSidebar, {
      node: mockNode,
      activeItem: 'storage/zfs',
      plugins: mockPlugins,
      onSelectItem: () => {},
    })
  );

  // Group headings / sections
  assert.ok(html.includes('STORAGE') || html.includes('STORAGE & FS'), 'Must display Storage section');
  assert.ok(html.includes('SHARES') || html.includes('SHARE'), 'Must display Shares section');
  assert.ok(html.includes('CONTAINERS') || html.includes('CONTAINER'), 'Must display Container section');

  // Plugin labels and badges
  assert.ok(html.includes('ZFS Storage'), 'Must render ZFS Storage tab');
  assert.ok(html.includes('3 pools'), 'Must render 3 pools status badge');
  assert.ok(html.includes('NFS Exports'), 'Must render NFS Exports tab');
  assert.ok(html.includes('ACTIVE'), 'Must render ACTIVE status badge');
  assert.ok(html.includes('Podman Containers'), 'Must render Podman Containers tab');
  assert.ok(html.includes('4 running'), 'Must render 4 running status badge');
});
