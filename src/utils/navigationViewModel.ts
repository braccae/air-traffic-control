import type { NodeItem, NodeType, TagInventoryChunk, CustomFolder } from '../types/index.ts';

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
    ...(input.connectionProtocol ? { connectionProtocol: input.connectionProtocol } : {}),
    ...(input.vaultSecretId ? { vaultSecretId: input.vaultSecretId } : {}),
    ...(input.winrmPort !== undefined ? { winrmPort: input.winrmPort } : {}),
    ...(input.folder ? { folder: input.folder } : {}),
    ...(input.isDynamicTagMember !== undefined ? { isDynamicTagMember: input.isDynamicTagMember } : {}),
    ...(input.followedTag ? { followedTag: input.followedTag } : {}),
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

  return `${columnClasses} self-start lg:sticky lg:top-[76px] lg:max-h-[calc(100vh-100px)] overflow-y-auto overflow-x-hidden scrollbar-thin shrink-0`;
}

export function getWorkspaceWrapperClasses(width: SubSidebarWidth): string {
  return width === 'wide'
    ? 'col-span-12 lg:col-span-8 xl:col-span-9 flex flex-col gap-2 min-w-0'
    : width === 'compact'
    ? 'col-span-12 lg:col-span-10 xl:col-span-10 flex flex-col gap-2 min-w-0'
    : 'col-span-12 lg:col-span-9 xl:col-span-10 flex flex-col gap-2 min-w-0';
}

export interface FormattedNodeBadge {
  label: string;
  badgeClasses: string;
}

export function formatNodeTypeBadge(node: NodeItem): FormattedNodeBadge | null {
  const rawBadge = (node.badge || '').trim().toUpperCase();
  const lowerName = (node.name || '').toLowerCase();
  const lowerGroup = (node.group || '').toLowerCase();
  const nodeType = (node.nodeType as string || '').toLowerCase();

  let label: string | null = null;

  if (rawBadge === 'GPU') {
    label = 'GPU';
  } else if (
    nodeType === 'pve' ||
    lowerGroup === 'tag:pve' ||
    lowerName.startsWith('pve-') ||
    rawBadge === 'PVE'
  ) {
    label = 'PVE';
  } else if (
    nodeType === 'vm' ||
    lowerGroup === 'tag:vm' ||
    lowerName.startsWith('vm-') ||
    rawBadge === 'VM'
  ) {
    label = 'VM';
  } else if (
    nodeType === 'lxc' ||
    lowerGroup === 'tag:lxc' ||
    lowerName.startsWith('lxc-') ||
    rawBadge === 'LXC'
  ) {
    label = 'LXC';
  } else if (
    nodeType === 'k3s' ||
    lowerGroup === 'tag:k3s' ||
    lowerName.startsWith('k3s-') ||
    rawBadge === 'K3S'
  ) {
    label = 'K3S';
  } else if (nodeType === 'pbs' || rawBadge === 'PBS') {
    label = 'PBS';
  } else if (nodeType === 'windows' || rawBadge === 'WIN') {
    label = 'WIN';
  } else if (
    nodeType === 'router_switch' ||
    rawBadge === 'NET' ||
    rawBadge === 'SWITCH' ||
    rawBadge === 'VYOS'
  ) {
    label = 'NET';
  }

  if (!label) {
    return null;
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
  } else if (upper === 'NET') {
    badgeClasses = 'bg-[#2d281b] text-[#ffd580] border border-[#ffd580]/30';
  } else if (upper === 'WIN') {
    badgeClasses = 'bg-[#182030] text-[#b0c9ff] border border-[#b0c9ff]/30';
  } else if (upper === 'PBS') {
    badgeClasses = 'bg-[#1c2e28] text-[#7ae0c2] border border-[#7ae0c2]/30';
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

export type DatacenterSection = 'overview' | 'playbooks' | 'vars_vault' | 'tasks' | 'schedules' | 'storage';

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
      title: 'Console & Tasks',
      icon: 'terminal',
      description: 'Live job queue & operational logs',
      badge: tasksCount > 0 ? `${tasksCount} active` : undefined,
    },
    {
      id: 'schedules',
      title: 'Scheduled Jobs',
      icon: 'schedule',
      description: 'Cron-scheduled playbooks & tasks',
    },
    {
      id: 'storage',
      title: 'Cluster Storage & Pools',
      icon: 'storage',
      description: 'ZFS, NFS, and Samba storage',
    },
  ];
}

export interface ConsoleHeaderLayoutClasses {
  headerBar: string;
  leftSection: string;
  dockSection: string;
  pillsContainer: string;
  rightSection: string;
}

export function getConsoleHeaderLayoutClasses(): ConsoleHeaderLayoutClasses {
  return {
    headerBar: 'h-7 bg-[#171a2d] px-2 sm:px-3 flex items-center justify-between border-b border-[#26293c] gap-2',
    leftSection: 'shrink-0 flex items-center gap-2 h-full',
    dockSection: 'flex-1 min-w-0 flex items-center gap-1.5 h-full px-2 border-l border-r border-[#26293c]',
    pillsContainer: 'flex items-center gap-1 overflow-x-auto scrollbar-none flex-1 py-0.5',
    rightSection: 'shrink-0 flex items-center gap-2 text-[#c3c6d2]',
  };
}

export function getConsoleWindowPillClasses(isActive: boolean, isMinimized: boolean): {
  pill: string;
  dot: string;
} {
  return {
    pill: `group flex items-center gap-1.5 px-2 py-0.5 rounded text-[11px] font-mono transition-all cursor-pointer border shrink-0 ${
      isActive
        ? 'bg-[#1f6feb]/20 border-[#58a6ff] text-white shadow-xs'
        : isMinimized
        ? 'bg-[#161b22] border-[#30363d] text-gray-400 hover:text-gray-200 hover:border-gray-500'
        : 'bg-[#161b22]/70 border-[#30363d]/60 text-gray-300 hover:border-gray-500'
    }`,
    dot: `w-1.5 h-1.5 rounded-full shrink-0 ${
      isActive
        ? 'bg-[#3fb950] animate-pulse'
        : isMinimized
        ? 'bg-amber-400'
        : 'bg-gray-500'
    }`,
  };
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

export interface TopSubNavTab {
  id: string;
  label: string;
}

export const TOP_SUB_NAV_TABS: TopSubNavTab[] = [
  { id: 'summary', label: 'Cluster Summary' },
  { id: 'playbooks', label: 'Ansible Playbooks' },
  { id: 'group_vars', label: 'Group Vars & Vault' },
  { id: 'mesh', label: 'Tailscale Mesh Status' },
  { id: 'task_queue', label: 'Ansible Task Queue' },
];

export type ConsoleDrawerTab = 'active' | 'task_queue' | 'ansible' | 'syslog' | 'vault';

export function formatTaskQueueTabLabel(stagedCount: number): string {
  return `Task Queue (${stagedCount})`;
}

export interface InventoryTagGroup {
  tag: string;
  chunk?: TagInventoryChunk;
  nodes: NodeItem[];
}

export interface InventoryFolderGroup {
  folder: CustomFolder;
  nodes: NodeItem[];
}

export interface InventoryTree {
  tailscale: {
    shortName: string;
    tags: InventoryTagGroup[];
  };
  nonTailscale: {
    standalone: NodeItem[];
    folders: InventoryFolderGroup[];
  };
}

export function buildInventoryTree(
  nodes: NodeItem[],
  chunks: TagInventoryChunk[] = [],
  folders: CustomFolder[] = [],
  defaultTailnet?: string
): InventoryTree {
  const shortName =
    defaultTailnet ||
    nodes.find(n => Boolean(n.datacenter))?.datacenter ||
    'leo-banjo.ts.net';

  // Tailscale nodes: Boolean(node.tailscaleIp || node.isDynamicTagMember || node.followedTag || (node.group && node.group.startsWith('tag:')) || (Array.isArray(node.tags) && node.tags.length > 0))
  const isTailscaleNode = (node: NodeItem): boolean =>
    Boolean(
      node.tailscaleIp ||
      node.isDynamicTagMember ||
      node.followedTag ||
      (node.group && node.group.startsWith('tag:')) ||
      (Array.isArray(node.tags) && node.tags.length > 0)
    );

  const tailscaleNodes: NodeItem[] = [];
  const nonTailscaleNodes: NodeItem[] = [];

  for (const node of nodes) {
    if (isTailscaleNode(node)) {
      tailscaleNodes.push(node);
    } else {
      nonTailscaleNodes.push(node);
    }
  }

  // Group Tailscale nodes by their tag (prefer node.followedTag || node.group || node.tags?.[0] || 'tag:host')
  const tagMap = new Map<string, { chunk?: TagInventoryChunk; nodes: NodeItem[] }>();

  // Ensure any active TagInventoryChunk is listed even if currently 0 members
  for (const chunk of chunks) {
    if (chunk && chunk.tag) {
      tagMap.set(chunk.tag, { chunk, nodes: [] });
    }
  }

  for (const node of tailscaleNodes) {
    const tag =
      node.followedTag ||
      (node.group && node.group.startsWith('tag:') ? node.group : undefined) ||
      (Array.isArray(node.tags) && node.tags.find(t => t.startsWith('tag:'))) ||
      node.group ||
      (Array.isArray(node.tags) && node.tags[0]) ||
      'tag:host';

    const existing = tagMap.get(tag);
    if (existing) {
      existing.nodes.push(node);
    } else {
      tagMap.set(tag, { chunk: undefined, nodes: [node] });
    }
  }

  const tags: InventoryTagGroup[] = Array.from(tagMap.entries()).map(([tag, data]) => ({
    tag,
    chunk: data.chunk,
    nodes: data.nodes,
  }));

  // Partition non-Tailscale into folders and standalone
  const folderMap = new Map<string, InventoryFolderGroup>();

  // Include all folders from folders input even if empty
  for (const folder of folders) {
    folderMap.set(folder.id, { folder, nodes: [] });
  }

  const standalone: NodeItem[] = [];
  for (const node of nonTailscaleNodes) {
    if (node.folder) {
      const matchedFolder = folders.find(
        f => f.id === node.folder || f.name === node.folder
      );
      if (matchedFolder && folderMap.has(matchedFolder.id)) {
        folderMap.get(matchedFolder.id)!.nodes.push(node);
      } else {
        standalone.push(node);
      }
    } else {
      standalone.push(node);
    }
  }

  const folderGroups: InventoryFolderGroup[] = folders.map(f => folderMap.get(f.id)!);

  return {
    tailscale: {
      shortName,
      tags,
    },
    nonTailscale: {
      standalone,
      folders: folderGroups,
    },
  };
}




