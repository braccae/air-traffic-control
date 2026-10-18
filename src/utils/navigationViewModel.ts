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
    group: input.group || 'cold-war',
    datacenter: input.datacenter || 'homelab-dc1',
    nodeType,
    badge: input.badge,
    badgeColor: input.badgeColor,
    ip: input.ip || '192.168.1.180',
    port: input.port || 22,
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
  if (node.group === 'noodle') return 'router';
  return 'dns';
}
