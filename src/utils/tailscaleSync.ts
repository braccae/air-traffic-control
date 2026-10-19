import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import type { NodeItem, TagInventoryChunk } from '../types/index.ts';

const execFileAsync = promisify(execFile);

export interface TagSyncResult {
  updatedNodes: NodeItem[];
  addedNodes: NodeItem[];
  removedNodes: NodeItem[];
  unchangedNodes: NodeItem[];
}

export function normalizeTag(tag: string): string {
  if (!tag || typeof tag !== 'string') return '';
  return tag.trim().toLowerCase().replace(/^tag:/, '');
}

export function matchesTailscaleTag(peerTags: string[] | undefined, targetTag: string): boolean {
  if (!peerTags || !Array.isArray(peerTags) || !targetTag || typeof targetTag !== 'string') {
    return false;
  }
  const normalizedTarget = normalizeTag(targetTag);
  if (!normalizedTarget) return false;
  return peerTags.some(t => typeof t === 'string' && normalizeTag(t) === normalizedTarget);
}

function findMatchingNode(peer: any, nodes: NodeItem[]): NodeItem | undefined {
  const candidateId = peer.ID || peer.HostName || (peer.DNSName ? peer.DNSName.split('.')[0] : undefined);
  const peerDns = peer.DNSName ? peer.DNSName.replace(/\.$/, '').toLowerCase() : '';
  const peerTsIp = Array.isArray(peer.TailscaleIPs) && peer.TailscaleIPs.length > 0 ? peer.TailscaleIPs[0] : '';
  const peerHost = (peer.HostName || '').toLowerCase();

  return nodes.find(n => {
    if (candidateId && n.id === candidateId) return true;
    if (peer.ID && n.id === peer.ID) return true;
    if (peerDns && n.tailscaleHost?.toLowerCase() === peerDns) return true;
    if (peerTsIp && (n.tailscaleIp === peerTsIp || n.ip === peerTsIp)) return true;
    if (peerHost && (n.shortName?.toLowerCase() === peerHost || n.name?.toLowerCase() === peerHost)) return true;
    return false;
  });
}

function findMatchingPeer(node: NodeItem, peers: any[]): any | undefined {
  const nodeHost = (node.tailscaleHost || '').toLowerCase();
  const nodeShort = (node.shortName || node.name || '').toLowerCase();
  const nodeIp = node.tailscaleIp || node.ip;

  return peers.find(p => {
    const pDns = p.DNSName ? p.DNSName.replace(/\.$/, '').toLowerCase() : '';
    const pTsIp = Array.isArray(p.TailscaleIPs) && p.TailscaleIPs.length > 0 ? p.TailscaleIPs[0] : '';
    const pHost = (p.HostName || '').toLowerCase();

    if (node.id && (p.ID === node.id || p.HostName === node.id)) return true;
    if (nodeHost && pDns === nodeHost) return true;
    if (nodeIp && pTsIp === nodeIp) return true;
    if (nodeShort && pHost === nodeShort) return true;
    return false;
  });
}

export function syncTagInventoryChunks(
  chunks: TagInventoryChunk[],
  peersInput: any[],
  existingNodesInput: NodeItem[],
  options?: { defaultDatacenter?: string }
): TagSyncResult {
  const chunksList = Array.isArray(chunks) ? chunks : [];
  const peers: any[] = Array.isArray(peersInput)
    ? peersInput
    : (peersInput && typeof peersInput === 'object' ? Object.values(peersInput) : []);
  const existingNodes = Array.isArray(existingNodesInput) ? existingNodesInput : [];
  const defaultDc = options?.defaultDatacenter || 'leo-banjo.ts.net';

  const addedNodes: NodeItem[] = [];
  const updatedNodes: NodeItem[] = [];
  const removedNodes: NodeItem[] = [];
  const unchangedNodes: NodeItem[] = [];

  const handledExistingNodeIds = new Set<string>();
  const processedPeerKeys = new Set<string>();

  for (const chunk of chunksList) {
    for (const peer of peers) {
      if (!matchesTailscaleTag(peer.Tags, chunk.tag)) {
        continue;
      }

      const peerKey = peer.ID || peer.HostName || peer.DNSName || (Array.isArray(peer.TailscaleIPs) ? peer.TailscaleIPs[0] : '');
      if (peerKey && processedPeerKeys.has(peerKey)) {
        continue;
      }
      if (peerKey) {
        processedPeerKeys.add(peerKey);
      }

      const existing = findMatchingNode(peer, existingNodes);

      const name = peer.HostName || peer.DNSName?.split('.')[0] || 'unknown';
      const shortName = peer.HostName?.split('.')[0] || name;
      const tsHost = peer.DNSName ? peer.DNSName.replace(/\.$/, '') : `${name.toLowerCase()}.${defaultDc}`;
      const isWindows = Boolean(peer.OS?.toLowerCase().includes('windows'));
      const nodeType = isWindows ? 'windows' : 'linux';
      const osDescription = peer.OS ? `${peer.OS} (Tailscale Dynamic)` : 'Linux (Tailscale Dynamic)';
      const peerTsIp = Array.isArray(peer.TailscaleIPs) && peer.TailscaleIPs.length > 0 ? peer.TailscaleIPs[0] : '';
      const sshUser = chunk.sshUser || (isWindows ? 'Administrator' : 'core');

      // INVARIANT: authMethod: If chunk.authMethod is set and credentials exist (chunk.vaultSecretId), use chunk.authMethod.
      // If credentials / vaultSecretId are blank or authMethod is 'tailscale-ssh', set authMethod = 'tailscale-ssh' (keyless PAM).
      const hasCredentials = Boolean(chunk.vaultSecretId && chunk.vaultSecretId.trim().length > 0);
      let authMethod = 'tailscale-ssh';
      if (chunk.authMethod && chunk.authMethod !== 'tailscale-ssh' && hasCredentials) {
        authMethod = chunk.authMethod;
      } else {
        authMethod = 'tailscale-ssh';
      }

      const targetStatus: 'online' | 'stopped' = peer.Online ? 'online' : 'stopped';
      const targetConnected = Boolean(peer.Online);

      if (existing) {
        handledExistingNodeIds.add(existing.id);

        if (!existing.isDynamicTagMember) {
          // Manual node: preserve all manual attributes, update status if changed
          if (existing.status !== targetStatus || existing.sshConnected !== targetConnected) {
            updatedNodes.push({
              ...existing,
              status: targetStatus,
              sshConnected: targetConnected,
            });
          } else {
            unchangedNodes.push(existing);
          }
        } else {
          // Dynamic node: update properties
          const updated: NodeItem = {
            ...existing,
            name,
            shortName,
            group: chunk.tag,
            tailscaleIp: peerTsIp || existing.tailscaleIp,
            tailscaleHost: tsHost,
            ip: peerTsIp || existing.ip,
            port: 22,
            nodeType,
            badge: nodeType === 'windows' ? 'WINRM' : 'SSH',
            osDescription,
            status: targetStatus,
            isDynamicTagMember: true,
            followedTag: chunk.tag,
            sshUser,
            vaultSecretId: chunk.vaultSecretId || undefined,
            authMethod,
            connectionProtocol: authMethod === 'tailscale-ssh' ? 'tailscale_ssh' : 'ssh',
            tags: Array.isArray(peer.Tags) && peer.Tags.length > 0 ? peer.Tags : [chunk.tag],
            sshConnected: targetConnected,
            tailscaleTag: chunk.tag,
          };

          const isModified = (
            existing.name !== updated.name ||
            existing.shortName !== updated.shortName ||
            existing.group !== updated.group ||
            existing.tailscaleIp !== updated.tailscaleIp ||
            existing.tailscaleHost !== updated.tailscaleHost ||
            existing.ip !== updated.ip ||
            existing.nodeType !== updated.nodeType ||
            existing.osDescription !== updated.osDescription ||
            existing.status !== updated.status ||
            existing.sshUser !== updated.sshUser ||
            existing.vaultSecretId !== updated.vaultSecretId ||
            existing.authMethod !== updated.authMethod ||
            existing.sshConnected !== updated.sshConnected ||
            existing.followedTag !== updated.followedTag
          );

          if (isModified) {
            updatedNodes.push(updated);
          } else {
            unchangedNodes.push(existing);
          }
        }
      } else {
        // Create new dynamic NodeItem
        const candidateId = peer.ID || peer.HostName || peer.DNSName?.split('.')[0] || `node-dynamic-${Date.now()}`;
        const newNode: NodeItem = {
          id: candidateId,
          name,
          shortName,
          group: chunk.tag,
          datacenter: defaultDc,
          nodeType,
          badge: isWindows ? 'WINRM' : 'SSH',
          badgeColor: 'text-[#8cd6cb]',
          ip: peerTsIp,
          port: 22,
          sshUser,
          authMethod,
          connectionProtocol: authMethod === 'tailscale-ssh' ? 'tailscale_ssh' : 'ssh',
          tags: Array.isArray(peer.Tags) && peer.Tags.length > 0 ? peer.Tags : [chunk.tag],
          sshConnected: targetConnected,
          tailscaleIp: peerTsIp,
          tailscaleHost: tsHost,
          tailscaleTag: chunk.tag,
          osDescription,
          status: targetStatus,
          uptime: '--',
          hasGlances: false,
          hasCockpit: false,
          hasPodman: false,
          hasRestic: false,
          specs: {
            cpu: 'Unknown CPU',
            vcpus: 4,
            ramGiB: 8,
            motherboard: 'Standard Platform',
            bios: '1.0',
            ipmi: 'N/A',
            ecc: 'None',
            secureBoot: 'Disabled',
            iommu: 'Disabled',
          },
          isDynamicTagMember: true,
          followedTag: chunk.tag,
          vaultSecretId: chunk.vaultSecretId || undefined,
        };

        addedNodes.push(newNode);
      }
    }
  }

  // Handle remaining existing nodes
  for (const node of existingNodes) {
    if (handledExistingNodeIds.has(node.id)) {
      continue;
    }

    if (node.isDynamicTagMember) {
      // Dynamic member whose tag peer is no longer found in mesh or tag was removed
      removedNodes.push(node);
    } else {
      // Manual node: check if any peer in mesh provides online/offline status
      const peer = findMatchingPeer(node, peers);
      if (peer) {
        const targetStatus: 'online' | 'stopped' = peer.Online ? 'online' : 'stopped';
        const targetConnected = Boolean(peer.Online);
        if (node.status !== targetStatus || node.sshConnected !== targetConnected) {
          updatedNodes.push({
            ...node,
            status: targetStatus,
            sshConnected: targetConnected,
          });
        } else {
          unchangedNodes.push(node);
        }
      } else {
        unchangedNodes.push(node);
      }
    }
  }

  return {
    updatedNodes,
    addedNodes,
    removedNodes,
    unchangedNodes,
  };
}

export async function fetchLiveTailscalePeers(): Promise<any[]> {
  try {
    const { stdout } = await execFileAsync('tailscale', ['status', '--json'], { timeout: 3000 });
    const data = JSON.parse(stdout);
    const peers: any[] = [];
    if (data.Self) {
      peers.push({ ...data.Self, isSelf: true });
    }
    if (data.Peer) {
      for (const peer of Object.values<any>(data.Peer)) {
        peers.push(peer);
      }
    }
    return peers;
  } catch {
    return [];
  }
}
