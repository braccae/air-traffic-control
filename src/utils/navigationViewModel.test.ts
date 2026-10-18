import test from 'node:test';
import assert from 'node:assert/strict';
import {
  filterNodesBySearchQuery,
  groupNodesByCluster,
  buildProvisionedNode,
  resolveNodeIcon,
} from './navigationViewModel.ts';
import type { NodeItem } from '../types/index.ts';

const sampleNodes: NodeItem[] = [
  {
    id: 'node-150',
    name: '150 (openmediavault)',
    shortName: '150',
    group: 'cold-war',
    datacenter: 'homelab-dc1',
    nodeType: 'linux',
    badge: 'SSH',
    ip: '192.168.1.150',
    port: 22,
    tailscaleIp: '100.97.159.101',
    tailscaleHost: 'the-freezer.ts.net',
    osDescription: 'AlmaLinux 10.2 / Debian 12',
    status: 'online',
    uptime: '16h 44m',
    hasGlances: true,
    hasCockpit: true,
    hasPodman: true,
    hasRestic: true,
    specs: {
      cpu: 'Intel Core i9-13900K',
      vcpus: 16,
      ramGiB: 64,
      motherboard: 'ASUS Pro WS W680-ACE',
      bios: '3401',
      ipmi: '192.168.1.50',
      ecc: 'Active',
      secureBoot: 'Disabled',
      iommu: 'Enabled',
    },
  },
  {
    id: 'node-153',
    name: '153 (llama-inference)',
    shortName: '153',
    group: 'cold-war',
    datacenter: 'homelab-dc1',
    nodeType: 'linux',
    badge: 'GPU',
    ip: '192.168.1.153',
    port: 22,
    tailscaleIp: '100.97.159.104',
    tailscaleHost: 'inference-ai.ts.net',
    osDescription: 'Ubuntu 24.04 LTS (NVIDIA CUDA 12.4)',
    status: 'online',
    uptime: '3d 21h',
    hasGlances: true,
    hasCockpit: true,
    hasPodman: true,
    hasRestic: true,
    specs: {
      cpu: 'AMD Ryzen 9 7950X',
      vcpus: 32,
      ramGiB: 64,
      motherboard: 'ASRock Rack B650D4U',
      bios: '8.02',
      ipmi: '192.168.1.53',
      ecc: 'Active ECC UDIMM',
      secureBoot: 'Disabled',
      iommu: 'Enabled',
    },
  },
  {
    id: 'node-161',
    name: '161 (postgres-ha)',
    shortName: '161',
    group: 'noodle',
    datacenter: 'homelab-dc1',
    nodeType: 'linux',
    ip: '192.168.1.161',
    port: 22,
    tailscaleIp: '100.97.159.107',
    tailscaleHost: 'pg-primary.ts.net',
    osDescription: 'Rocky Linux 9.3 (Blue Onyx)',
    status: 'online',
    uptime: '62d 18h',
    hasGlances: true,
    hasCockpit: true,
    hasPodman: true,
    hasRestic: true,
    specs: {
      cpu: 'AMD EPYC 7282',
      vcpus: 16,
      ramGiB: 64,
      motherboard: 'Supermicro H11SSL-NC',
      bios: '2.5',
      ipmi: '192.168.1.57',
      ecc: 'Active ECC Reg',
      secureBoot: 'Enabled',
      iommu: 'Enabled',
    },
  },
  {
    id: 'node-edge-1',
    name: 'edge-gw-01',
    shortName: 'edge-gw-01',
    group: 'edge',
    datacenter: 'homelab-dc1',
    nodeType: 'router_switch',
    ip: '192.168.1.254',
    port: 22,
    tailscaleIp: '100.97.159.120',
    tailscaleHost: 'edge-gw-01.ts.net',
    osDescription: 'VyOS 1.5-rolling',
    status: 'online',
    uptime: '180d',
    hasGlances: false,
    hasCockpit: false,
    hasPodman: false,
    hasRestic: false,
    specs: {
      cpu: 'Octeon III',
      vcpus: 2,
      ramGiB: 2,
      motherboard: 'ER-X-SFP',
      bios: 'U-Boot',
      ipmi: 'N/A',
      ecc: 'Embedded',
      secureBoot: 'Enabled',
      iommu: 'N/A',
    },
  },
];

test('filterNodesBySearchQuery filters case-insensitively by name, ip, tailscaleIp, group, and osDescription', () => {
  assert.equal(filterNodesBySearchQuery(sampleNodes, '').length, 4);
  assert.equal(filterNodesBySearchQuery(sampleNodes, '   ').length, 4);

  // By name (case-insensitive)
  const byName = filterNodesBySearchQuery(sampleNodes, 'OPENMEDIAVAULT');
  assert.equal(byName.length, 1);
  assert.equal(byName[0].id, 'node-150');

  // By IP
  const byIp = filterNodesBySearchQuery(sampleNodes, '192.168.1.161');
  assert.equal(byIp.length, 1);
  assert.equal(byIp[0].id, 'node-161');

  // By Tailscale IP
  const byTsIp = filterNodesBySearchQuery(sampleNodes, '100.97.159.104');
  assert.equal(byTsIp.length, 1);
  assert.equal(byTsIp[0].id, 'node-153');

  // By group (case-insensitive)
  const byGroup = filterNodesBySearchQuery(sampleNodes, 'NOODLE');
  assert.equal(byGroup.length, 1);
  assert.equal(byGroup[0].id, 'node-161');

  // By osDescription (case-insensitive)
  const byOs = filterNodesBySearchQuery(sampleNodes, 'cuda 12.4');
  assert.equal(byOs.length, 1);
  assert.equal(byOs[0].id, 'node-153');

  // No match
  assert.equal(filterNodesBySearchQuery(sampleNodes, 'nonexistent-host').length, 0);
});

test('groupNodesByCluster groups nodes into coldWar, noodle, and other clusters', () => {
  const grouped = groupNodesByCluster(sampleNodes);
  assert.equal(grouped.coldWar.length, 2);
  assert.deepEqual(
    grouped.coldWar.map(n => n.id),
    ['node-150', 'node-153']
  );

  assert.equal(grouped.noodle.length, 1);
  assert.equal(grouped.noodle[0].id, 'node-161');

  assert.equal(grouped.other.length, 1);
  assert.equal(grouped.other[0].id, 'node-edge-1');
});

test('buildProvisionedNode normalizes tailscaleHost and sets capabilities based on nodeType', () => {
  const linuxNode = buildProvisionedNode(
    {
      name: '155 (Docker Prod)',
      nodeType: 'linux',
      group: 'cold-war',
    },
    'test-155'
  );

  assert.equal(linuxNode.id, 'node-test-155');
  assert.equal(linuxNode.shortName, '155');
  assert.equal(linuxNode.tailscaleHost, '155-docker-prod.ts.net');
  assert.equal(linuxNode.hasGlances, true);
  assert.equal(linuxNode.hasCockpit, true);
  assert.equal(linuxNode.hasPodman, true);

  const k3sNode = buildProvisionedNode({ name: 'k3s-worker-02', nodeType: 'k3s' }, 200);
  assert.equal(k3sNode.id, 'node-200');
  assert.equal(k3sNode.hasGlances, true);
  assert.equal(k3sNode.hasCockpit, true);
  assert.equal(k3sNode.hasPodman, true);

  const lxcNode = buildProvisionedNode({ name: 'lxc-dns', nodeType: 'lxc' }, 201);
  assert.equal(lxcNode.hasGlances, true);
  assert.equal(lxcNode.hasCockpit, true);
  assert.equal(lxcNode.hasPodman, true);

  const routerNode = buildProvisionedNode({ name: 'core-sw-01', nodeType: 'router_switch' }, 202);
  assert.equal(routerNode.hasGlances, false);
  assert.equal(routerNode.hasCockpit, false);
  assert.equal(routerNode.hasPodman, false);

  const winNode = buildProvisionedNode({ name: 'win-dc-01', nodeType: 'windows' }, 203);
  assert.equal(winNode.hasGlances, false);
  assert.equal(winNode.hasCockpit, false);
  assert.equal(winNode.hasPodman, false);
});

test('resolveNodeIcon returns accurate Material Symbol icon names', () => {
  const base = sampleNodes[0];

  assert.equal(resolveNodeIcon({ ...base, nodeType: 'windows' }), 'desktop_windows');
  assert.equal(resolveNodeIcon({ ...base, nodeType: 'router_switch' }), 'settings_ethernet');
  assert.equal(resolveNodeIcon({ ...base, badge: 'GPU' }), 'memory');
  assert.equal(resolveNodeIcon({ ...base, badge: 'LXC', nodeType: 'lxc' }), 'deployed_code');
  assert.equal(resolveNodeIcon({ ...base, badge: 'K3S', nodeType: 'k3s' }), 'lan');
  assert.equal(resolveNodeIcon({ ...base, badge: 'PBS', nodeType: 'pbs' }), 'storage');
  assert.equal(resolveNodeIcon({ ...base, badge: undefined, name: '161 (postgres-ha)', group: 'noodle' }), 'database');
  assert.equal(resolveNodeIcon({ ...base, badge: undefined, name: '160 (ingress-traefik)', group: 'noodle' }), 'router');
  assert.equal(resolveNodeIcon({ ...base, badge: 'SSH', name: '150 (openmediavault)', group: 'cold-war' }), 'dns');
});
