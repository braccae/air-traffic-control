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

test('groupNodesByDynamicCluster groups arbitrary Tailscale tag groups (e.g. tag:host, tag:pve, tag:service)', async () => {
  const { groupNodesByDynamicCluster, buildHostGroupPills, updateNodeSettings } = await import('./navigationViewModel.ts');

  const tailscaleNodes: NodeItem[] = [
    { ...sampleNodes[0], id: 'node-likes-it-cold', name: 'likes-it-cold', group: 'tag:host', sshUser: 'core' },
    { ...sampleNodes[1], id: 'node-ping', name: 'ping', group: 'tag:host', sshUser: 'core' },
    { ...sampleNodes[2], id: 'node-noodle', name: 'noodle', group: 'tag:pve', sshUser: 'root' },
  ];

  const clusters = groupNodesByDynamicCluster(tailscaleNodes);
  assert.equal(clusters.length, 2);
  assert.equal(clusters[0].group, 'tag:host');
  assert.equal(clusters[0].nodes.length, 2);
  assert.equal(clusters[1].group, 'tag:pve');
  assert.equal(clusters[1].nodes.length, 1);

  const pills = buildHostGroupPills(tailscaleNodes);
  assert.equal(pills[0].id, 'all');
  assert.equal(pills[0].name, 'all: 3');
  assert.ok(pills.some(p => p.id === 'tag:host' && p.name === 'tag:host: 2'));
  assert.ok(pills.some(p => p.id === 'tag:pve' && p.name === 'tag:pve: 1'));

  const updated = updateNodeSettings(tailscaleNodes[0], {
    name: 'likes-it-cold (Primary)',
    sshUser: 'ansible',
    port: 2222,
    specs: { ...tailscaleNodes[0].specs, vcpus: 12, ramGiB: 32 },
  });
  assert.equal(updated.name, 'likes-it-cold (Primary)');
  assert.equal(updated.sshUser, 'ansible');
  assert.equal(updated.port, 2222);
  assert.equal(updated.specs.vcpus, 12);
  assert.equal(updated.specs.ramGiB, 32);
});

test('getSubSidebarWrapperClasses generates responsive sticky positioning and independent scrollbar classes for all widths', async () => {
  const { getSubSidebarWrapperClasses, getWorkspaceWrapperClasses } = await import('./navigationViewModel.ts');

  // Verify sticky pinned positioning and independent scroll properties across all widths
  for (const width of ['compact', 'normal', 'wide'] as const) {
    const classes = getSubSidebarWrapperClasses(width);
    assert.ok(classes.includes('self-start'), `Expected self-start for ${width}`);
    assert.ok(classes.includes('lg:sticky lg:top-[76px]'), `Expected lg:sticky lg:top-[76px] for ${width}`);
    assert.ok(classes.includes('lg:max-h-[calc(100vh-100px)]'), `Expected responsive viewport height calculation for ${width}`);
    assert.ok(classes.includes('overflow-y-auto'), `Expected overflow-y-auto for ${width}`);
    assert.ok(classes.includes('shrink-0'), `Expected shrink-0 for ${width}`);
    assert.ok(classes.includes('scrollbar-thin'), `Expected scrollbar-thin for ${width}`);
  }

  // Verify responsive width column allocations
  const compactSidebar = getSubSidebarWrapperClasses('compact');
  assert.ok(compactSidebar.includes('col-span-12 lg:col-span-2 xl:col-span-2'));

  const normalSidebar = getSubSidebarWrapperClasses('normal');
  assert.ok(normalSidebar.includes('col-span-12 lg:col-span-3 xl:col-span-2'));

  const wideSidebar = getSubSidebarWrapperClasses('wide');
  assert.ok(wideSidebar.includes('col-span-12 lg:col-span-4 xl:col-span-3'));

  // Verify complementary workspace column allocations
  const compactWorkspace = getWorkspaceWrapperClasses('compact');
  assert.ok(compactWorkspace.includes('col-span-12 lg:col-span-10 xl:col-span-10'));
  assert.ok(compactWorkspace.includes('flex flex-col gap-2'));
  assert.ok(compactWorkspace.includes('min-w-0'));

  const normalWorkspace = getWorkspaceWrapperClasses('normal');
  assert.ok(normalWorkspace.includes('col-span-12 lg:col-span-9 xl:col-span-10'));
  assert.ok(normalWorkspace.includes('min-w-0'));

  const wideWorkspace = getWorkspaceWrapperClasses('wide');
  assert.ok(wideWorkspace.includes('col-span-12 lg:col-span-8 xl:col-span-9'));
  assert.ok(wideWorkspace.includes('min-w-0'));
});

test('formatNodeTypeBadge replaces generic SSH placeholders with styled node type pills', async () => {
  const { formatNodeTypeBadge } = await import('./navigationViewModel.ts');

  // Generic SSH badge on Linux node -> 'Linux'
  const linuxWithSshBadge = formatNodeTypeBadge({
    ...sampleNodes[0],
    badge: 'SSH',
    nodeType: 'linux',
  });
  assert.equal(linuxWithSshBadge.label, 'Linux');
  assert.ok(linuxWithSshBadge.badgeClasses.includes('text-[#b0c9ff]'));

  // Generic TS-SSH badge on Linux node -> 'Linux'
  const linuxWithTsSsh = formatNodeTypeBadge({
    ...sampleNodes[0],
    badge: 'TS-SSH',
    nodeType: 'linux',
  });
  assert.equal(linuxWithTsSsh.label, 'Linux');

  // Node with LXC type -> 'LXC' with cyan styling
  const lxcNode = formatNodeTypeBadge({
    ...sampleNodes[0],
    badge: 'SSH',
    nodeType: 'lxc',
  });
  assert.equal(lxcNode.label, 'LXC');
  assert.ok(lxcNode.badgeClasses.includes('text-[#8cd6cb]'));

  // Node with PVE group -> 'PVE' with distinct styling
  const pveNode = formatNodeTypeBadge({
    ...sampleNodes[0],
    badge: 'SSH',
    group: 'tag:pve',
    nodeType: 'linux',
  });
  assert.equal(pveNode.label, 'PVE');
  assert.ok(pveNode.badgeClasses.includes('text-[#ffb4ab]'));

  // Node with VM group -> 'VM' with purple styling
  const vmNode = formatNodeTypeBadge({
    ...sampleNodes[0],
    badge: 'SSH',
    group: 'tag:vm',
    nodeType: 'linux',
  });
  assert.equal(vmNode.label, 'VM');
  assert.ok(vmNode.badgeClasses.includes('text-[#d9b9ff]'));

  // Node with explicit non-generic badge preserved (e.g. GPU, LOCAL, SWITCH)
  const gpuNode = formatNodeTypeBadge({
    ...sampleNodes[0],
    badge: 'GPU',
    nodeType: 'linux',
  });
  assert.equal(gpuNode.label, 'GPU');
  assert.ok(gpuNode.badgeClasses.includes('text-[#f2b9ff]'));

  const localNode = formatNodeTypeBadge({
    ...sampleNodes[0],
    badge: 'LOCAL',
    nodeType: 'linux',
  });
  assert.equal(localNode.label, 'LOCAL');
  assert.ok(localNode.badgeClasses.includes('text-[#93c5fd]'));
});

test('shouldShowSshUser suppresses common default users core and root and preserves custom users', async () => {
  const { shouldShowSshUser } = await import('./navigationViewModel.ts');

  // Default / placeholder users should be suppressed
  assert.equal(shouldShowSshUser('core'), false);
  assert.equal(shouldShowSshUser('CORE'), false);
  assert.equal(shouldShowSshUser('root'), false);
  assert.equal(shouldShowSshUser('ROOT'), false);
  assert.equal(shouldShowSshUser(''), false);
  assert.equal(shouldShowSshUser(undefined), false);

  // Custom / non-standard users should be shown
  assert.equal(shouldShowSshUser('pants'), true);
  assert.equal(shouldShowSshUser('ansible'), true);
  assert.equal(shouldShowSshUser('deploy'), true);
  assert.equal(shouldShowSshUser('admin'), true);
});

test('formatNodeIpBadge extracts tailscaleIp or LAN ip correctly', async () => {
  const { formatNodeIpBadge } = await import('./navigationViewModel.ts');

  const withTs = { ...sampleNodes[0], tailscaleIp: '100.105.71.49', ip: '192.168.8.249' };
  assert.equal(formatNodeIpBadge(withTs), '100.105.71.49');

  const withoutTs = { ...sampleNodes[0], tailscaleIp: '', ip: '192.168.1.100' };
  assert.equal(formatNodeIpBadge(withoutTs), '192.168.1.100');
});

test('getDatacenterNavSections returns all 5 navigation sections with accurate metadata and badges', async () => {
  const { getDatacenterNavSections } = await import('./navigationViewModel.ts');

  const sections = getDatacenterNavSections(4, 2);
  assert.equal(sections.length, 5);

  const sectionIds = sections.map(s => s.id);
  assert.deepEqual(sectionIds, ['overview', 'playbooks', 'vars_vault', 'tasks', 'storage']);

  const playbooksSec = sections.find(s => s.id === 'playbooks');
  assert.equal(playbooksSec?.badge, '4 playbooks');

  const varsSec = sections.find(s => s.id === 'vars_vault');
  assert.equal(varsSec?.badge, 'Encrypted');

  const tasksSec = sections.find(s => s.id === 'tasks');
  assert.equal(tasksSec?.badge, '2 active');
});

test('calculateClusterHealthScore accurately computes score, online counts, and status labels', async () => {
  const { calculateClusterHealthScore } = await import('./navigationViewModel.ts');

  // All online
  const healthy = calculateClusterHealthScore(sampleNodes);
  assert.equal(healthy.score, 100);
  assert.equal(healthy.onlineCount, 4);
  assert.equal(healthy.totalCount, 4);
  assert.equal(healthy.statusLabel, 'HEALTHY');

  // Degraded (3 out of 4 online = 75%)
  const degradedNodes = [
    ...sampleNodes.slice(0, 3),
    { ...sampleNodes[3], status: 'stopped' as const },
  ];
  const degraded = calculateClusterHealthScore(degradedNodes);
  assert.equal(degraded.score, 75);
  assert.equal(degraded.statusLabel, 'DEGRADED');

  // Critical (1 out of 4 online = 25%)
  const criticalNodes = [
    sampleNodes[0],
    { ...sampleNodes[1], status: 'stopped' as const },
    { ...sampleNodes[2], status: 'stopped' as const },
    { ...sampleNodes[3], status: 'stopped' as const },
  ];
  const critical = calculateClusterHealthScore(criticalNodes);
  assert.equal(critical.score, 25);
  assert.equal(critical.statusLabel, 'CRITICAL');
});



