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
