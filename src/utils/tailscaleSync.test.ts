import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  matchesTailscaleTag,
  syncTagInventoryChunks,
  type TagSyncResult,
} from './tailscaleSync.ts';
import type { NodeItem, TagInventoryChunk } from '../types/index.ts';

describe('tailscaleSync: Dynamic Tailscale Tag Synchronization Engine', () => {
  describe('matchesTailscaleTag', () => {
    it('matches exact tags with tag: prefix', () => {
      assert.equal(matchesTailscaleTag(['tag:worker'], 'tag:worker'), true);
    });

    it('matches tag when target lacks tag: prefix', () => {
      assert.equal(matchesTailscaleTag(['tag:worker'], 'worker'), true);
    });

    it('matches tag when peer tag lacks tag: prefix', () => {
      assert.equal(matchesTailscaleTag(['worker'], 'tag:worker'), true);
    });

    it('matches tag when both lack tag: prefix', () => {
      assert.equal(matchesTailscaleTag(['pve'], 'pve'), true);
    });

    it('is case-insensitive for both peer and target tags', () => {
      assert.equal(matchesTailscaleTag(['tag:WORKER'], 'tag:worker'), true);
      assert.equal(matchesTailscaleTag(['tag:pve'], 'PVE'), true);
      assert.equal(matchesTailscaleTag(['Tag:Storage'], 'tag:storage'), true);
    });

    it('matches when target tag is one of multiple peer tags', () => {
      const peerTags = ['tag:k3s', 'tag:worker', 'tag:fast-network'];
      assert.equal(matchesTailscaleTag(peerTags, 'tag:worker'), true);
      assert.equal(matchesTailscaleTag(peerTags, 'k3s'), true);
    });

    it('returns false when tag does not match', () => {
      assert.equal(matchesTailscaleTag(['tag:pve'], 'tag:worker'), false);
    });

    it('returns false for undefined, null, or empty peer tags', () => {
      assert.equal(matchesTailscaleTag(undefined, 'tag:worker'), false);
      assert.equal(matchesTailscaleTag([], 'tag:worker'), false);
    });

    it('returns false for empty or whitespace-only target tag', () => {
      assert.equal(matchesTailscaleTag(['tag:worker'], ''), false);
      assert.equal(matchesTailscaleTag(['tag:worker'], '   '), false);
    });
  });

  describe('syncTagInventoryChunks', () => {
    it('discovers matching peers and creates dynamic NodeItems with isDynamicTagMember: true and followedTag', () => {
      const chunks: TagInventoryChunk[] = [
        {
          id: 'chunk-tag:worker',
          tag: 'tag:worker',
          authMethod: 'tailscale-ssh',
          createdAt: Date.now(),
        },
      ];

      const peers = [
        {
          ID: 'nPeer1234',
          HostName: 'worker-01',
          DNSName: 'worker-01.leo-banjo.ts.net.',
          OS: 'linux',
          TailscaleIPs: ['100.64.0.15', 'fd7a:115c:a1e0::15'],
          Tags: ['tag:worker'],
          Online: true,
        },
      ];

      const existingNodes: NodeItem[] = [];

      const result: TagSyncResult = syncTagInventoryChunks(chunks, peers, existingNodes);

      assert.equal(result.addedNodes.length, 1);
      assert.equal(result.updatedNodes.length, 0);
      assert.equal(result.removedNodes.length, 0);
      assert.equal(result.unchangedNodes.length, 0);

      const node = result.addedNodes[0];
      assert.equal(node.id, 'nPeer1234');
      assert.equal(node.name, 'worker-01');
      assert.equal(node.shortName, 'worker-01');
      assert.equal(node.group, 'tag:worker');
      assert.equal(node.tailscaleIp, '100.64.0.15');
      assert.equal(node.tailscaleHost, 'worker-01.leo-banjo.ts.net');
      assert.equal(node.ip, '100.64.0.15');
      assert.equal(node.port, 22);
      assert.equal(node.nodeType, 'linux');
      assert.equal(node.osDescription, 'linux (Tailscale Dynamic)');
      assert.equal(node.status, 'online');
      assert.equal(node.isDynamicTagMember, true);
      assert.equal(node.followedTag, 'tag:worker');
      assert.equal(node.sshUser, 'core');
      assert.equal(node.authMethod, 'tailscale-ssh');
      assert.equal(node.connectionProtocol, 'tailscale_ssh');
    });

    it('defaults to keyless tailscale-ssh when credentials / vaultSecretId are blank', () => {
      const chunks: TagInventoryChunk[] = [
        {
          id: 'chunk-tag:pve',
          tag: 'tag:pve',
          sshUser: 'root',
          authMethod: 'vault-password', // configured as vault-password but vaultSecretId is omitted
          createdAt: Date.now(),
        },
      ];

      const peers = [
        {
          ID: 'nPve1',
          HostName: 'pve-node-1',
          DNSName: 'pve-node-1.leo-banjo.ts.net.',
          OS: 'linux',
          TailscaleIPs: ['100.64.0.21'],
          Tags: ['tag:pve'],
          Online: true,
        },
      ];

      const result = syncTagInventoryChunks(chunks, peers, []);
      assert.equal(result.addedNodes.length, 1);
      const node = result.addedNodes[0];
      assert.equal(node.sshUser, 'root');
      assert.equal(node.vaultSecretId, undefined);
      // Invariant: blank credentials default to keyless tailscale-ssh
      assert.equal(node.authMethod, 'tailscale-ssh');
      assert.equal(node.connectionProtocol, 'tailscale_ssh');
    });

    it('applies shared vault credentials when vaultSecretId is provided', () => {
      const chunks: TagInventoryChunk[] = [
        {
          id: 'chunk-tag:apps',
          tag: 'tag:apps',
          sshUser: 'apps',
          authMethod: 'vault-sshkey',
          vaultSecretId: 'secret-vault-ssh-apps-key',
          createdAt: Date.now(),
        },
      ];

      const peers = [
        {
          ID: 'nApp1',
          HostName: 'app-service-1',
          DNSName: 'app-service-1.leo-banjo.ts.net.',
          OS: 'linux',
          TailscaleIPs: ['100.64.0.35'],
          Tags: ['tag:apps'],
          Online: false,
        },
      ];

      const result = syncTagInventoryChunks(chunks, peers, []);
      assert.equal(result.addedNodes.length, 1);
      const node = result.addedNodes[0];
      assert.equal(node.sshUser, 'apps');
      assert.equal(node.vaultSecretId, 'secret-vault-ssh-apps-key');
      assert.equal(node.authMethod, 'vault-sshkey');
      assert.equal(node.status, 'stopped');
    });

    it('handles Windows nodes with Administrator default sshUser and windows nodeType', () => {
      const chunks: TagInventoryChunk[] = [
        {
          id: 'chunk-tag:win',
          tag: 'tag:win',
          authMethod: 'tailscale-ssh',
          createdAt: Date.now(),
        },
      ];

      const peers = [
        {
          ID: 'nWinServer',
          HostName: 'win-dc-01',
          DNSName: 'win-dc-01.leo-banjo.ts.net.',
          OS: 'windows',
          TailscaleIPs: ['100.64.0.99'],
          Tags: ['tag:win'],
          Online: true,
        },
      ];

      const result = syncTagInventoryChunks(chunks, peers, []);
      const node = result.addedNodes[0];
      assert.equal(node.nodeType, 'windows');
      assert.equal(node.sshUser, 'Administrator');
      assert.equal(node.badge, 'WINRM');
      assert.equal(node.osDescription, 'windows (Tailscale Dynamic)');
    });

    it('preserves existing manual nodes and updates online/offline status without removing them', () => {
      const manualNode: NodeItem = {
        id: 'node-manual-server',
        name: 'manual-server',
        shortName: 'manual-server',
        group: 'tag:host',
        datacenter: 'leo-banjo.ts.net',
        nodeType: 'linux',
        badge: 'SSH',
        badgeColor: 'text-[#8cd6cb]',
        ip: '192.168.1.100',
        port: 22,
        sshUser: 'core',
        authMethod: 'ssh-ed25519 (ssh-agent)',
        tags: ['tag:host'],
        sshConnected: false,
        tailscaleIp: '100.64.0.200',
        tailscaleHost: 'manual-server.leo-banjo.ts.net',
        osDescription: 'Ubuntu Linux',
        status: 'stopped',
        uptime: '10d',
        hasGlances: true,
        hasCockpit: false,
        hasPodman: true,
        hasRestic: true,
        specs: {
          cpu: 'AMD EPYC',
          vcpus: 16,
          ramGiB: 64,
          motherboard: 'Supermicro',
          bios: '1.0',
          ipmi: 'DHCP',
          ecc: 'Active',
          secureBoot: 'Enabled',
          iommu: 'Enabled',
        },
      };

      const chunks: TagInventoryChunk[] = [
        {
          id: 'chunk-tag:worker',
          tag: 'tag:worker',
          authMethod: 'tailscale-ssh',
          createdAt: Date.now(),
        },
      ];

      // Peer with the same tailscale host/ip is online in mesh
      const peers = [
        {
          ID: 'nManualPeer',
          HostName: 'manual-server',
          DNSName: 'manual-server.leo-banjo.ts.net.',
          OS: 'linux',
          TailscaleIPs: ['100.64.0.200'],
          Online: true,
        },
      ];

      const result = syncTagInventoryChunks(chunks, peers, [manualNode]);

      assert.equal(result.removedNodes.length, 0);
      assert.equal(result.addedNodes.length, 0);
      // Status updated from stopped to online because peer is online
      assert.equal(result.updatedNodes.length, 1);
      assert.equal(result.updatedNodes[0].id, 'node-manual-server');
      assert.equal(result.updatedNodes[0].status, 'online');
      assert.equal(result.updatedNodes[0].sshConnected, true);
      // Manual properties preserved!
      assert.equal(result.updatedNodes[0].isDynamicTagMember, undefined);
      assert.equal(result.updatedNodes[0].uptime, '10d');
      assert.equal(result.updatedNodes[0].hasGlances, true);
    });

    it('updates existing dynamic nodes when peer state changes and moves unreferenced dynamic nodes to removedNodes', () => {
      const activeDynamicNode: NodeItem = {
        id: 'node-worker-01',
        name: 'worker-01',
        shortName: 'worker-01',
        group: 'tag:worker',
        datacenter: 'leo-banjo.ts.net',
        nodeType: 'linux',
        badge: 'SSH',
        badgeColor: 'text-[#8cd6cb]',
        ip: '100.64.0.10',
        port: 22,
        sshUser: 'core',
        authMethod: 'tailscale-ssh',
        connectionProtocol: 'tailscale_ssh',
        tags: ['tag:worker'],
        sshConnected: true,
        tailscaleIp: '100.64.0.10',
        tailscaleHost: 'worker-01.leo-banjo.ts.net',
        osDescription: 'linux (Tailscale Dynamic)',
        status: 'online',
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
        followedTag: 'tag:worker',
      };

      const staleDynamicNode: NodeItem = {
        ...activeDynamicNode,
        id: 'node-worker-02',
        name: 'worker-02',
        shortName: 'worker-02',
        tailscaleIp: '100.64.0.11',
        tailscaleHost: 'worker-02.leo-banjo.ts.net',
      };

      const chunks: TagInventoryChunk[] = [
        {
          id: 'chunk-tag:worker',
          tag: 'tag:worker',
          authMethod: 'tailscale-ssh',
          createdAt: Date.now(),
        },
      ];

      // worker-01 is now stopped; worker-02 has left the mesh
      const peers = [
        {
          ID: 'nWorker1',
          HostName: 'worker-01',
          DNSName: 'worker-01.leo-banjo.ts.net.',
          OS: 'linux',
          TailscaleIPs: ['100.64.0.10'],
          Tags: ['tag:worker'],
          Online: false,
        },
      ];

      const result = syncTagInventoryChunks(chunks, peers, [activeDynamicNode, staleDynamicNode]);

      // worker-01 updated (online -> stopped)
      assert.equal(result.updatedNodes.length, 1);
      assert.equal(result.updatedNodes[0].id, 'node-worker-01');
      assert.equal(result.updatedNodes[0].status, 'stopped');
      assert.equal(result.updatedNodes[0].sshConnected, false);

      // worker-02 removed
      assert.equal(result.removedNodes.length, 1);
      assert.equal(result.removedNodes[0].id, 'node-worker-02');
    });

    it('removes dynamic nodes when their followed tag chunk is deleted', () => {
      const dynamicNode: NodeItem = {
        id: 'node-pve-01',
        name: 'pve-01',
        shortName: 'pve-01',
        group: 'tag:pve',
        datacenter: 'leo-banjo.ts.net',
        nodeType: 'linux',
        badge: 'SSH',
        badgeColor: 'text-[#8cd6cb]',
        ip: '100.64.0.50',
        port: 22,
        sshUser: 'root',
        authMethod: 'tailscale-ssh',
        tags: ['tag:pve'],
        sshConnected: true,
        tailscaleIp: '100.64.0.50',
        tailscaleHost: 'pve-01.leo-banjo.ts.net',
        osDescription: 'linux (Tailscale Dynamic)',
        status: 'online',
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
        followedTag: 'tag:pve',
      };

      // No chunks exist (user deleted the chunk)
      const chunks: TagInventoryChunk[] = [];
      const peers = [
        {
          ID: 'nPve1',
          HostName: 'pve-01',
          DNSName: 'pve-01.leo-banjo.ts.net.',
          OS: 'linux',
          TailscaleIPs: ['100.64.0.50'],
          Tags: ['tag:pve'],
          Online: true,
        },
      ];

      const result = syncTagInventoryChunks(chunks, peers, [dynamicNode]);
      assert.equal(result.removedNodes.length, 1);
      assert.equal(result.removedNodes[0].id, 'node-pve-01');
      assert.equal(result.addedNodes.length, 0);
      assert.equal(result.updatedNodes.length, 0);
    });
  });
});
