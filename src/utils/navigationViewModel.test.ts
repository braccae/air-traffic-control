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

test('formatNodeTypeBadge returns null for standard Linux hosts and clutter badges', async () => {
  const { formatNodeTypeBadge } = await import('./navigationViewModel.ts');

  // Standard Linux host without virtualization tier -> null
  const standardHost = formatNodeTypeBadge({
    ...sampleNodes[0],
    badge: undefined,
    nodeType: 'linux',
  });
  assert.equal(standardHost, null);

  // Clutter / generic badges -> null
  const clutterBadges = [
    'Linux',
    'linux',
    'Local',
    'LOCAL',
    'Mgr',
    'mgr',
    'Host',
    'HOST',
    'Arm64',
    'arm64',
    'ts-ssh',
    'TS-SSH',
    'ssh',
    'SSH',
    'core',
    'CORE',
    'x86_64',
    'aarch64',
  ];
  for (const b of clutterBadges) {
    const res = formatNodeTypeBadge({
      ...sampleNodes[0],
      badge: b,
      nodeType: 'linux',
    });
    assert.equal(res, null, `Expected badge "${b}" to return null`);
  }
});

test('formatNodeTypeBadge returns styled pills for distinct virtualization and platform tiers', async () => {
  const { formatNodeTypeBadge } = await import('./navigationViewModel.ts');

  // PVE via nodeType, group, prefix, or badge
  const pve1 = formatNodeTypeBadge({ ...sampleNodes[0], nodeType: 'pve' as any });
  assert.equal(pve1?.label, 'PVE');
  assert.ok(pve1?.badgeClasses.includes('text-[#ffb4ab]'));

  const pve2 = formatNodeTypeBadge({ ...sampleNodes[0], group: 'tag:pve', nodeType: 'linux' });
  assert.equal(pve2?.label, 'PVE');

  const pve3 = formatNodeTypeBadge({ ...sampleNodes[0], name: 'pve-cluster-01', nodeType: 'linux' });
  assert.equal(pve3?.label, 'PVE');

  const pve4 = formatNodeTypeBadge({ ...sampleNodes[0], badge: 'PVE', nodeType: 'linux' });
  assert.equal(pve4?.label, 'PVE');

  // VM via nodeType, group, prefix, or badge
  const vm1 = formatNodeTypeBadge({ ...sampleNodes[0], nodeType: 'vm' as any });
  assert.equal(vm1?.label, 'VM');
  assert.ok(vm1?.badgeClasses.includes('text-[#d9b9ff]'));

  const vm2 = formatNodeTypeBadge({ ...sampleNodes[0], group: 'tag:vm', nodeType: 'linux' });
  assert.equal(vm2?.label, 'VM');

  const vm3 = formatNodeTypeBadge({ ...sampleNodes[0], name: 'vm-postgres-01', nodeType: 'linux' });
  assert.equal(vm3?.label, 'VM');

  const vm4 = formatNodeTypeBadge({ ...sampleNodes[0], badge: 'VM', nodeType: 'linux' });
  assert.equal(vm4?.label, 'VM');

  // LXC via nodeType, prefix, or badge
  const lxc1 = formatNodeTypeBadge({ ...sampleNodes[0], nodeType: 'lxc' });
  assert.equal(lxc1?.label, 'LXC');
  assert.ok(lxc1?.badgeClasses.includes('text-[#8cd6cb]'));

  const lxc2 = formatNodeTypeBadge({ ...sampleNodes[0], name: 'lxc-pihole', nodeType: 'linux' });
  assert.equal(lxc2?.label, 'LXC');

  const lxc3 = formatNodeTypeBadge({ ...sampleNodes[0], badge: 'LXC', nodeType: 'linux' });
  assert.equal(lxc3?.label, 'LXC');

  // K3S via nodeType, prefix, or badge
  const k3s1 = formatNodeTypeBadge({ ...sampleNodes[0], nodeType: 'k3s' });
  assert.equal(k3s1?.label, 'K3S');
  assert.ok(k3s1?.badgeClasses.includes('text-[#8cd6cb]'));

  const k3s2 = formatNodeTypeBadge({ ...sampleNodes[0], name: 'k3s-agent-01', nodeType: 'linux' });
  assert.equal(k3s2?.label, 'K3S');

  const k3s3 = formatNodeTypeBadge({ ...sampleNodes[0], badge: 'K3S', nodeType: 'linux' });
  assert.equal(k3s3?.label, 'K3S');

  // PBS via nodeType or badge
  const pbs1 = formatNodeTypeBadge({ ...sampleNodes[0], nodeType: 'pbs' });
  assert.equal(pbs1?.label, 'PBS');
  assert.ok(pbs1?.badgeClasses.includes('text-[#7ae0c2]'));

  const pbs2 = formatNodeTypeBadge({ ...sampleNodes[0], badge: 'PBS', nodeType: 'linux' });
  assert.equal(pbs2?.label, 'PBS');

  // WIN via nodeType or badge
  const win1 = formatNodeTypeBadge({ ...sampleNodes[0], nodeType: 'windows' });
  assert.equal(win1?.label, 'WIN');
  assert.ok(win1?.badgeClasses.includes('text-[#b0c9ff]'));

  const win2 = formatNodeTypeBadge({ ...sampleNodes[0], badge: 'WIN', nodeType: 'linux' });
  assert.equal(win2?.label, 'WIN');

  // NET via nodeType router_switch or badge NET/SWITCH/VYOS
  const net1 = formatNodeTypeBadge({ ...sampleNodes[0], nodeType: 'router_switch' });
  assert.equal(net1?.label, 'NET');
  assert.ok(net1?.badgeClasses.includes('text-[#ffd580]'));

  const net2 = formatNodeTypeBadge({ ...sampleNodes[0], badge: 'NET', nodeType: 'linux' });
  assert.equal(net2?.label, 'NET');

  const net3 = formatNodeTypeBadge({ ...sampleNodes[0], badge: 'SWITCH', nodeType: 'linux' });
  assert.equal(net3?.label, 'NET');

  // GPU via badge
  const gpu = formatNodeTypeBadge({ ...sampleNodes[0], badge: 'GPU', nodeType: 'linux' });
  assert.equal(gpu?.label, 'GPU');
  assert.ok(gpu?.badgeClasses.includes('text-[#f2b9ff]'));
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

test('getDatacenterNavSections returns all 6 navigation sections with accurate metadata and badges', async () => {
  const { getDatacenterNavSections } = await import('./navigationViewModel.ts');

  const sections = getDatacenterNavSections(4, 2);
  assert.equal(sections.length, 6);

  const sectionIds = sections.map(s => s.id);
  assert.deepEqual(sectionIds, ['overview', 'playbooks', 'vars_vault', 'tasks', 'schedules', 'storage']);

  const playbooksSec = sections.find(s => s.id === 'playbooks');
  assert.equal(playbooksSec?.badge, '4 playbooks');

  const varsSec = sections.find(s => s.id === 'vars_vault');
  assert.equal(varsSec?.badge, 'Encrypted');

  const tasksSec = sections.find(s => s.id === 'tasks');
  assert.equal(tasksSec?.badge, '2 active');
  assert.equal(tasksSec?.title, 'Console & Tasks');
  assert.equal(tasksSec?.icon, 'terminal');
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

test('TOP_SUB_NAV_TABS excludes duplicate xterm_console and contains expected cluster tabs', async () => {
  const { TOP_SUB_NAV_TABS } = await import('./navigationViewModel.ts');
  const tabIds = TOP_SUB_NAV_TABS.map(t => t.id);

  assert.equal(tabIds.includes('xterm_console'), false, 'xterm_console must not be in top tabs');
  assert.deepEqual(tabIds, ['summary', 'playbooks', 'group_vars', 'mesh', 'task_queue']);
});

test('getConsoleHeaderLayoutClasses provides pinned left/right sections and isolated middle dock with horizontal scroll only on pills', async () => {
  const { getConsoleHeaderLayoutClasses } = await import('./navigationViewModel.ts');
  const layout = getConsoleHeaderLayoutClasses();

  // Header bar has proper height, spacing and layout
  assert.ok(layout.headerBar.includes('h-7'), 'headerBar must be h-7');
  assert.ok(layout.headerBar.includes('justify-between'), 'headerBar must use justify-between');
  assert.ok(layout.headerBar.includes('gap-2'), 'headerBar must have gap-2');

  // Pinned Left Section must NEVER scroll horizontally
  assert.ok(layout.leftSection.includes('shrink-0'), 'leftSection must be shrink-0');
  assert.ok(!layout.leftSection.includes('overflow-x-auto'), 'leftSection must not scroll horizontally');

  // Middle Dock Section isolates pills and expands to fill space without pushing neighbors
  assert.ok(layout.dockSection.includes('flex-1'), 'dockSection must be flex-1');
  assert.ok(layout.dockSection.includes('min-w-0'), 'dockSection must have min-w-0 to prevent layout blowout');
  assert.ok(layout.dockSection.includes('border-l'), 'dockSection must have left border divider');
  assert.ok(layout.dockSection.includes('border-r'), 'dockSection must have right border divider');

  // Minimized Pills Container is the ONLY element that scrolls horizontally
  assert.ok(layout.pillsContainer.includes('overflow-x-auto'), 'pillsContainer must have overflow-x-auto');
  assert.ok(layout.pillsContainer.includes('scrollbar-none'), 'pillsContainer must have scrollbar-none');
  assert.ok(layout.pillsContainer.includes('flex-1'), 'pillsContainer must be flex-1');

  // Pinned Right Section must NEVER scroll horizontally or be compressed
  assert.ok(layout.rightSection.includes('shrink-0'), 'rightSection must be shrink-0');
  assert.ok(!layout.rightSection.includes('overflow-x-auto'), 'rightSection must not scroll horizontally');
});

test('getConsoleWindowPillClasses properly styles active, minimized, and background windows', async () => {
  const { getConsoleWindowPillClasses } = await import('./navigationViewModel.ts');

  // 1. Active open window
  const activeStyles = getConsoleWindowPillClasses(true, false);
  assert.ok(activeStyles.pill.includes('shrink-0'), 'active pill must be shrink-0');
  assert.ok(activeStyles.pill.includes('border-[#58a6ff]'), 'active pill must have active border');
  assert.ok(activeStyles.dot.includes('bg-[#3fb950]'), 'active dot must be green');
  assert.ok(activeStyles.dot.includes('animate-pulse'), 'active dot must pulse');

  // 2. Minimized window
  const minStyles = getConsoleWindowPillClasses(false, true);
  assert.ok(minStyles.pill.includes('shrink-0'), 'minimized pill must be shrink-0');
  assert.ok(minStyles.pill.includes('text-gray-400'), 'minimized pill text must be gray-400');
  assert.ok(minStyles.dot.includes('bg-amber-400'), 'minimized dot must be amber-400');

  // 3. Inactive/background open window
  const bgStyles = getConsoleWindowPillClasses(false, false);
  assert.ok(bgStyles.pill.includes('shrink-0'), 'background pill must be shrink-0');
  assert.ok(bgStyles.dot.includes('bg-gray-500'), 'background dot must be gray-500');
});

test('getSubSidebarWrapperClasses includes overflow-x-hidden across all widths', async () => {
  const { getSubSidebarWrapperClasses } = await import('./navigationViewModel.ts');

  for (const width of ['compact', 'normal', 'wide'] as const) {
    const classes = getSubSidebarWrapperClasses(width);
    assert.ok(classes.includes('overflow-x-hidden'), `Expected overflow-x-hidden for width ${width}`);
    assert.ok(classes.includes('overflow-y-auto'), `Expected overflow-y-auto for width ${width}`);
    assert.ok(classes.includes('shrink-0'), `Expected shrink-0 for width ${width}`);
  }
});

test('formatTaskQueueTabLabel formats staged task count correctly', async () => {
  const { formatTaskQueueTabLabel } = await import('./navigationViewModel.ts');

  assert.strictEqual(formatTaskQueueTabLabel(0), 'Task Queue (0)');
  assert.strictEqual(formatTaskQueueTabLabel(5), 'Task Queue (5)');
});

test('buildInventoryTree partitions nodes into Tailscale tag groups and Non-Tailscale standalone & folders matching operator sketch', async () => {
  const { buildInventoryTree } = await import('./navigationViewModel.ts');

  const chunks = [
    { id: 'chunk-worker', tag: 'tag:worker', authMethod: 'tailscale-ssh' as const, createdAt: 100 },
    { id: 'chunk-pve', tag: 'tag:pve', authMethod: 'vault-password' as const, createdAt: 200 },
  ];

  const folders = [
    { id: 'folder-lab', name: 'Lab Hardware', createdAt: 500 },
  ];

  const nodes: NodeItem[] = [
    {
      ...sampleNodes[0],
      id: 'node-ts-1',
      name: 'worker-01',
      group: 'tag:worker',
      tailscaleIp: '100.97.159.101',
      tags: ['tag:worker'],
    },
    {
      ...sampleNodes[0],
      id: 'node-ts-2',
      name: 'worker-02',
      group: '',
      followedTag: 'tag:worker',
      tailscaleIp: '100.97.159.102',
      tags: [],
    },
    {
      ...sampleNodes[0],
      id: 'node-ts-3',
      name: 'pve-host-1',
      group: 'tag:pve',
      tailscaleIp: '100.97.159.103',
      tags: ['tag:pve'],
    },
    {
      ...sampleNodes[0],
      id: 'node-nonts-1',
      name: 'router-direct-1',
      group: 'network',
      tailscaleIp: '',
      ip: '192.168.1.1',
      tags: [],
      folder: undefined,
    },
    {
      ...sampleNodes[0],
      id: 'node-nonts-2',
      name: 'winrm-box-1',
      group: 'windows',
      tailscaleIp: '',
      ip: '192.168.1.50',
      tags: [],
      folder: undefined,
    },
    {
      ...sampleNodes[0],
      id: 'node-nonts-3',
      name: 'lab-pdu-01',
      group: 'iot',
      tailscaleIp: '',
      ip: '192.168.1.99',
      tags: [],
      folder: 'folder-lab',
    },
  ];

  const tree = buildInventoryTree(nodes, chunks, folders, 'leo-banjo.ts.net');

  // Verify Tailscale section
  assert.equal(tree.tailscale.shortName, 'leo-banjo.ts.net');
  assert.equal(tree.tailscale.tags.length, 2);

  const workerTag = tree.tailscale.tags.find(t => t.tag === 'tag:worker');
  assert.ok(workerTag, 'tag:worker should exist');
  assert.equal(workerTag?.chunk?.id, 'chunk-worker');
  assert.deepEqual(workerTag?.nodes.map(n => n.id), ['node-ts-1', 'node-ts-2']);

  const pveTag = tree.tailscale.tags.find(t => t.tag === 'tag:pve');
  assert.ok(pveTag, 'tag:pve should exist');
  assert.equal(pveTag?.chunk?.id, 'chunk-pve');
  assert.deepEqual(pveTag?.nodes.map(n => n.id), ['node-ts-3']);

  // Verify Non-Tailscale section
  assert.equal(tree.nonTailscale.standalone.length, 2);
  assert.deepEqual(tree.nonTailscale.standalone.map(n => n.id), ['node-nonts-1', 'node-nonts-2']);

  assert.equal(tree.nonTailscale.folders.length, 1);
  assert.equal(tree.nonTailscale.folders[0].folder.id, 'folder-lab');
  assert.equal(tree.nonTailscale.folders[0].folder.name, 'Lab Hardware');
  assert.deepEqual(tree.nonTailscale.folders[0].nodes.map(n => n.id), ['node-nonts-3']);
});

test('buildInventoryTree preserves empty chunks and empty folders with nodes: []', async () => {
  const { buildInventoryTree } = await import('./navigationViewModel.ts');

  const chunks = [
    { id: 'chunk-active', tag: 'tag:active', authMethod: 'tailscale-ssh' as const, createdAt: 100 },
    { id: 'chunk-empty', tag: 'tag:empty', authMethod: 'vault-password' as const, createdAt: 200 },
  ];

  const folders = [
    { id: 'folder-filled', name: 'Filled Folder', createdAt: 300 },
    { id: 'folder-empty', name: 'Cold Storage Empty', createdAt: 400 },
  ];

  const nodes: NodeItem[] = [
    {
      ...sampleNodes[0],
      id: 'node-1',
      group: 'tag:active',
      tailscaleIp: '100.97.159.10',
      tags: ['tag:active'],
    },
    {
      ...sampleNodes[0],
      id: 'node-2',
      tailscaleIp: '',
      ip: '192.168.1.20',
      tags: [],
      folder: 'folder-filled',
    },
  ];

  const tree = buildInventoryTree(nodes, chunks, folders);

  // Empty chunk preserved
  const emptyChunkTag = tree.tailscale.tags.find(t => t.tag === 'tag:empty');
  assert.ok(emptyChunkTag, 'empty chunk should be preserved in tags');
  assert.equal(emptyChunkTag?.chunk?.id, 'chunk-empty');
  assert.equal(emptyChunkTag?.nodes.length, 0);

  // Active chunk has node
  const activeChunkTag = tree.tailscale.tags.find(t => t.tag === 'tag:active');
  assert.ok(activeChunkTag);
  assert.equal(activeChunkTag?.nodes.length, 1);

  // Empty folder preserved
  const emptyFolder = tree.nonTailscale.folders.find(f => f.folder.id === 'folder-empty');
  assert.ok(emptyFolder, 'empty folder should be preserved in folders');
  assert.equal(emptyFolder?.nodes.length, 0);

  // Filled folder has node
  const filledFolder = tree.nonTailscale.folders.find(f => f.folder.id === 'folder-filled');
  assert.ok(filledFolder);
  assert.equal(filledFolder?.nodes.length, 1);
});

test('buildInventoryTree matches folder by name when node.folder equals folder.name', async () => {
  const { buildInventoryTree } = await import('./navigationViewModel.ts');

  const folders = [
    { id: 'folder-123', name: 'Lab Hardware', createdAt: 100 },
  ];

  const nodes: NodeItem[] = [
    {
      ...sampleNodes[0],
      id: 'node-named-folder',
      tailscaleIp: '',
      ip: '192.168.1.30',
      tags: [],
      folder: 'Lab Hardware', // Matches name, not id
    },
  ];

  const tree = buildInventoryTree(nodes, [], folders);
  assert.equal(tree.nonTailscale.standalone.length, 0);
  assert.equal(tree.nonTailscale.folders[0].nodes.length, 1);
  assert.equal(tree.nonTailscale.folders[0].nodes[0].id, 'node-named-folder');
});

test('buildInventoryTree with filterNodesBySearchQuery supports tree searching across TS and non-TS hosts', async () => {
  const { buildInventoryTree, filterNodesBySearchQuery } = await import('./navigationViewModel.ts');

  const nodes: NodeItem[] = [
    {
      ...sampleNodes[0],
      id: 'node-ts-ai',
      name: 'ai-worker-01',
      group: 'tag:worker',
      tailscaleIp: '100.97.159.50',
      tags: ['tag:worker'],
    },
    {
      ...sampleNodes[0],
      id: 'node-nonts-ai',
      name: 'ai-storage-san',
      group: 'storage',
      tailscaleIp: '',
      ip: '192.168.1.60',
      tags: [],
    },
    {
      ...sampleNodes[0],
      id: 'node-other',
      name: 'web-frontend',
      group: 'tag:web',
      tailscaleIp: '100.97.159.70',
      tags: ['tag:web'],
    },
  ];

  const filtered = filterNodesBySearchQuery(nodes, 'ai-');
  const tree = buildInventoryTree(filtered, [], []);

  // Only AI nodes should be in the tree
  assert.equal(tree.tailscale.tags.length, 1);
  assert.equal(tree.tailscale.tags[0].nodes.length, 1);
  assert.equal(tree.tailscale.tags[0].nodes[0].id, 'node-ts-ai');

  assert.equal(tree.nonTailscale.standalone.length, 1);
  assert.equal(tree.nonTailscale.standalone[0].id, 'node-nonts-ai');
});


