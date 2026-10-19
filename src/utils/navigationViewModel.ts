import type { NodeItem, NodeType } from '../types/index.ts';

export function filterNodesBySearchQuery(nodes: NodeItem[], query: string): NodeItem[] {
  const normalized = query.trim().toLowerCase();
  if (!normalized) return nodes;

  return nodes.filter(
    n =>
      n.name.toLowerCase().includes(normalized) ||
      n.ip.toLowerCase().includes(normalized) ||
      n.tailscaleIp.toLowerCase().includes(normalized) ||
      n.group.toLowerCase().includes(normalized) ||
      (n.osDescription && n.osDescription.toLowerCase().includes(normalized))
  );
}

export function groupNodesByCluster(nodes: NodeItem[]): {
  coldWar: NodeItem[];
  noodle: NodeItem[];
  other: NodeItem[];
} {
  const coldWar = nodes.filter(n => n.group === 'cold-war');
  const noodle = nodes.filter(n => n.group === 'noodle');
  const other = nodes.filter(n => n.group !== 'cold-war' && n.group !== 'noodle');

  return { coldWar, noodle, other };
}

export interface ClusterNodeGroup {
  group: string;
  nodes: NodeItem[];
}

export function groupNodesByDynamicCluster(nodes: NodeItem[]): ClusterNodeGroup[] {
  const byGroup = new Map<string, NodeItem[]>();
  for (const node of nodes) {
    const grp = node.group || 'tag:host';
    const list = byGroup.get(grp) || [];
    list.push(node);
    byGroup.set(grp, list);
  }
  return Array.from(byGroup.entries()).map(([group, groupNodes]) => ({
    group,
    nodes: groupNodes,
  }));
}

export function buildHostGroupPills(nodes: NodeItem[]): Array<{ id: string; name: string }> {
  const counts = new Map<string, number>();
  for (const node of nodes) {
    const grp = node.group || 'tag:host';
    counts.set(grp, (counts.get(grp) || 0) + 1);
  }

  const pills: Array<{ id: string; name: string }> = [
    { id: 'all', name: `all: ${nodes.length}` },
  ];

  for (const [grp, count] of counts.entries()) {
    pills.push({ id: grp, name: `${grp}: ${count}` });
  }
  return pills;
}

export function updateNodeSettings(existing: NodeItem, updates: Partial<NodeItem>): NodeItem {
  const nextName = updates.name !== undefined ? updates.name.trim() || existing.name : existing.name;
  const nextShortName =
    updates.shortName !== undefined
      ? updates.shortName.trim() || nextName.split(' ')[0]
      : nextName.split(' ')[0];

  return {
    ...existing,
    ...updates,
    name: nextName,
    shortName: nextShortName,
    specs: {
      ...existing.specs,
      ...(updates.specs || {}),
    },
  };
}

export function buildProvisionedNode(
  input: Partial<NodeItem>,
  idSuffix?: string | number
): NodeItem {
  const name = input.name?.trim() || 'New Node';
  const shortName = input.shortName || name.split(' ')[0];
  const nodeType: NodeType = input.nodeType || 'linux';
  const supportsLinuxStack =
    nodeType === 'linux' || nodeType === 'lxc' || nodeType === 'k3s';

  const normalizedSlug = name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '');

  const tailscaleHost =
    input.tailscaleHost && input.tailscaleHost.trim()
      ? input.tailscaleHost.trim()
      : `${normalizedSlug || 'node'}.ts.net`;

  const suffix = idSuffix !== undefined ? String(idSuffix) : String(Date.now());
  const id = input.id || (suffix.startsWith('node-') ? suffix : `node-${suffix}`);

  return {
    id,
    name,
    shortName,
    group: input.group || 'tag:host',
    datacenter: input.datacenter || 'leo-banjo.ts.net',
    nodeType,
    badge: input.badge,
    badgeColor: input.badgeColor,
    ip: input.ip || '192.168.1.180',
    port: input.port || 22,
    sshUser: input.sshUser || 'core',
    authMethod: input.authMethod || 'ssh-ed25519',
    tags: input.tags || [input.group || 'tag:host'],
    sshConnected: input.sshConnected,
    rttMs: input.rttMs,
    tailscaleIp: input.tailscaleIp || '100.97.159.180',
    tailscaleHost,
    osDescription: input.osDescription || 'Linux (SSH)',
    status: input.status || 'online',
    uptime: input.uptime || '1m',
    hasGlances: input.hasGlances ?? supportsLinuxStack,
    hasCockpit: input.hasCockpit ?? supportsLinuxStack,
    hasPodman: input.hasPodman ?? supportsLinuxStack,
    hasRestic: input.hasRestic ?? (nodeType !== 'router_switch'),
    specs: input.specs || {
      cpu: 'Intel Core / AMD EPYC',
      vcpus: 8,
      ramGiB: 16,
      motherboard: 'Enterprise Platform',
      bios: '2.8',
      ipmi: 'DHCP',
      ecc: 'Active',
      secureBoot: 'Enabled',
      iommu: 'Enabled',
    },
  };
}

export function resolveNodeIcon(node: NodeItem): string {
  if (node.nodeType === 'windows') return 'desktop_windows';
  if (node.nodeType === 'router_switch') return 'settings_ethernet';
  if (node.badge === 'GPU') return 'memory';
  if (node.badge === 'LXC' || node.nodeType === 'lxc') return 'deployed_code';
  if (node.badge === 'K3S' || node.nodeType === 'k3s') return 'lan';
  if (node.badge === 'PBS' || node.nodeType === 'pbs') return 'storage';
  if (node.name.toLowerCase().includes('postgres')) return 'database';
  if (node.group === 'noodle' || node.group === 'tag:pve') return 'router';
  return 'dns';
}

export type SubSidebarWidth = 'compact' | 'normal' | 'wide';

export function getSubSidebarWrapperClasses(width: SubSidebarWidth): string {
  const columnClasses =
    width === 'wide'
      ? 'col-span-12 lg:col-span-4 xl:col-span-3'
      : width === 'compact'
      ? 'col-span-12 lg:col-span-2 xl:col-span-2'
      : 'col-span-12 lg:col-span-3 xl:col-span-2';

  return `${columnClasses} lg:sticky lg:top-[76px] lg:h-[calc(100vh-140px)] overflow-y-auto shrink-0 pr-1 scrollbar-thin`;
}

export function getWorkspaceWrapperClasses(width: SubSidebarWidth): string {
  return width === 'wide'
    ? 'col-span-12 lg:col-span-8 xl:col-span-9 flex flex-col gap-2'
    : width === 'compact'
    ? 'col-span-12 lg:col-span-10 xl:col-span-10 flex flex-col gap-2'
    : 'col-span-12 lg:col-span-9 xl:col-span-10 flex flex-col gap-2';
}

export interface FormattedNodeBadge {
  label: string;
  badgeClasses: string;
}

export function formatNodeTypeBadge(node: NodeItem): FormattedNodeBadge {
  const rawBadge = (node.badge || '').trim();
  const isGenericSsh = rawBadge.toUpperCase() === 'SSH' || rawBadge.toUpperCase() === 'TS-SSH';

  let label = '';
  if (rawBadge && !isGenericSsh) {
    label = rawBadge;
  } else if (node.nodeType === 'lxc' || node.name.toLowerCase().includes('lxc-')) {
    label = 'LXC';
  } else if (node.nodeType === 'k3s' || node.name.toLowerCase().includes('k3s-')) {
    label = 'K3S';
  } else if (node.nodeType === 'pbs') {
    label = 'PBS';
  } else if (node.nodeType === 'windows') {
    label = 'WIN';
  } else if (node.nodeType === 'router_switch') {
    label = 'NET';
  } else if (node.group === 'tag:pve' || node.name.toLowerCase().includes('pve-')) {
    label = 'PVE';
  } else if (node.group === 'tag:vm' || node.name.toLowerCase().includes('vm-')) {
    label = 'VM';
  } else {
    label = 'Linux';
  }

  const upper = label.toUpperCase();
  let badgeClasses = 'bg-[#182030] text-[#b0c9ff] border border-[#b0c9ff]/20';
  if (upper === 'PVE') {
    badgeClasses = 'bg-[#311c1c] text-[#ffb4ab] border border-[#ffb4ab]/30';
  } else if (upper === 'LXC' || upper === 'K3S') {
    badgeClasses = 'bg-[#1b2a38] text-[#8cd6cb] border border-[#8cd6cb]/30';
  } else if (upper === 'VM') {
    badgeClasses = 'bg-[#291e3b] text-[#d9b9ff] border border-[#d9b9ff]/30';
  } else if (upper === 'GPU') {
    badgeClasses = 'bg-[#2d1b38] text-[#f2b9ff] border border-[#f2b9ff]/30';
  } else if (upper === 'NET' || upper === 'SWITCH' || upper === 'VYOS') {
    badgeClasses = 'bg-[#2d281b] text-[#ffd580] border border-[#ffd580]/30';
  } else if (upper === 'LOCAL') {
    badgeClasses = 'bg-[#1e293b] text-[#93c5fd] border border-[#93c5fd]/30';
  }

  return { label, badgeClasses };
}

export function shouldShowSshUser(sshUser?: string): boolean {
  if (!sshUser) return false;
  const normalized = sshUser.trim().toLowerCase();
  return normalized !== 'core' && normalized !== 'root' && normalized !== '';
}

export function formatNodeIpBadge(node: NodeItem): string {
  if (node.tailscaleIp) return node.tailscaleIp;
  if (node.ip) return node.ip;
  return '';
}

export type SidebarViewMode = 'nodes' | 'datacenter';

export type DatacenterSection = 'overview' | 'playbooks' | 'vars_vault' | 'tasks' | 'storage';

export interface DatacenterNavSection {
  id: DatacenterSection;
  title: string;
  icon: string;
  description: string;
  badge?: string;
}

export function getDatacenterNavSections(playbooksCount = 4, tasksCount = 0): DatacenterNavSection[] {
  return [
    {
      id: 'overview',
      title: 'Overview & Mesh',
      icon: 'cloud_queue',
      description: 'Datacenter topology & mesh summary',
    },
    {
      id: 'playbooks',
      title: 'Ansible Playbooks',
      icon: 'play_arrow',
      description: 'Cluster automation & orchestration',
      badge: `${playbooksCount} playbooks`,
    },
    {
      id: 'vars_vault',
      title: 'Vars & Vault Secrets',
      icon: 'key',
      description: 'Group variables and encrypted vault',
      badge: 'Encrypted',
    },
    {
      id: 'tasks',
      title: 'Global Tasks & Executions',
      icon: 'task_alt',
      description: 'Live job queue & operational logs',
      badge: tasksCount > 0 ? `${tasksCount} active` : undefined,
    },
    {
      id: 'storage',
      title: 'Cluster Storage & Pools',
      icon: 'storage',
      description: 'ZFS, NFS, and Samba storage',
    },
  ];
}

export function calculateClusterHealthScore(nodes: NodeItem[]): {
  score: number;
  onlineCount: number;
  totalCount: number;
  statusLabel: string;
  statusColor: string;
} {
  const total = nodes.length;
  if (total === 0) {
    return { score: 100, onlineCount: 0, totalCount: 0, statusLabel: 'HEALTHY', statusColor: 'text-[#8cd6cb]' };
  }
  const online = nodes.filter(n => n.status === 'online').length;
  const score = Math.round((online / total) * 100);
  let statusLabel = 'HEALTHY';
  let statusColor = 'text-[#8cd6cb]';
  if (score < 75) {
    statusLabel = 'CRITICAL';
    statusColor = 'text-[#ffb4ab]';
  } else if (score < 95) {
    statusLabel = 'DEGRADED';
    statusColor = 'text-[#ffd580]';
  }
  return { score, onlineCount: online, totalCount: total, statusLabel, statusColor };
}


