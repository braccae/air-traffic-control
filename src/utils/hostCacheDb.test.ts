import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { createHostCacheStore } from './hostCacheDb.ts';
import type { NodeItem, TelemetryData, UnprivilegedPodmanUser, ServiceUser } from '../types/index.ts';

const sampleNode: NodeItem = {
  id: 'node-likes-it-cold',
  name: 'likes-it-cold',
  shortName: 'likes-it-cold',
  group: 'tag:host',
  datacenter: 'leo-banjo.ts.net',
  nodeType: 'linux',
  badge: 'SSH',
  badgeColor: 'text-[#8cd6cb]',
  ip: '192.168.8.249',
  port: 22,
  sshUser: 'core',
  authMethod: 'ssh-ed25519 (/run/user/1000/ssh-agent.socket)',
  tags: ['tag:host', 'tag:server'],
  sshConnected: true,
  rttMs: '0.7ms',
  tailscaleIp: '100.105.71.49',
  tailscaleHost: 'likes-it-cold.leo-banjo.ts.net',
  osDescription: 'AlmaLinux 10.2 (Lavender Lion)',
  status: 'online',
  uptime: '19d 14h 12m',
  hasGlances: true,
  hasCockpit: true,
  hasPodman: true,
  hasRestic: true,
  specs: {
    cpu: 'AMD EPYC / x86_64 Host',
    vcpus: 8,
    ramGiB: 16,
    motherboard: 'QEMU Standard PC (Q35 + ICH9)',
    bios: 'EDK II / OVMF',
    ipmi: '100.105.71.49',
    ecc: 'Active',
    secureBoot: 'Enabled',
    iommu: 'Enabled',
  },
};

describe('SQLite Host Cache Store (hostCacheDb)', () => {
  it('seeds initial hosts when database is empty and preserves edits across calls', () => {
    const store = createHostCacheStore(':memory:');
    const seeded = store.seedHostsIfEmpty([sampleNode]);
    assert.equal(seeded.length, 1);
    assert.equal(seeded[0].id, 'node-likes-it-cold');

    // Update host motherboard and verify persistence
    const updatedNode: NodeItem = {
      ...sampleNode,
      specs: { ...sampleNode.specs!, motherboard: 'Supermicro X11 (Cached)' },
    };
    store.upsertHost(updatedNode);

    // Calling seedHostsIfEmpty again should NOT overwrite edited host
    const afterReseed = store.seedHostsIfEmpty([sampleNode]);
    assert.equal(afterReseed.length, 1);
    assert.equal(afterReseed[0].specs?.motherboard, 'Supermicro X11 (Cached)');

    const byId = store.getHostById('node-likes-it-cold');
    assert.ok(byId);
    assert.equal(byId.specs?.motherboard, 'Supermicro X11 (Cached)');
  });

  it('supports deleting a host and cascading its cached telemetry/containers', () => {
    const store = createHostCacheStore(':memory:');
    store.upsertHost(sampleNode);
    assert.equal(store.getAllHosts().length, 1);

    const deleted = store.deleteHost('node-likes-it-cold');
    assert.equal(deleted, true);
    assert.equal(store.getAllHosts().length, 0);
  });

  it('caches and retrieves host telemetry snapshots with timestamp metadata', () => {
    const store = createHostCacheStore(':memory:');
    store.upsertHost(sampleNode);

    const sampleTelemetry: Partial<TelemetryData> = {
      cpuUsage: 12.4,
      cpuModel: 'Intel(R) Core(TM) i5-6500 CPU @ 3.20GHz',
      vCpus: 4,
      memoryUsedGiB: 4.2,
      memoryTotalGiB: 15.5,
      loadAvg1m: 0.45,
    };

    store.upsertHostTelemetry('node-likes-it-cold', sampleTelemetry);
    const cached = store.getHostTelemetry('node-likes-it-cold');
    assert.ok(cached);
    assert.equal(cached.telemetry.cpuUsage, 12.4);
    assert.equal(cached.telemetry.cpuModel, 'Intel(R) Core(TM) i5-6500 CPU @ 3.20GHz');
    assert.ok(cached.updatedAt > 0);
  });

  it('caches and retrieves probed Podman containers, rootless users, and custom identities per host', () => {
    const store = createHostCacheStore(':memory:');
    store.upsertHost(sampleNode);

    const probedUsers: UnprivilegedPodmanUser[] = [
      {
        user: 'uosserver',
        uid: 1001,
        gid: 1001,
        homeDir: '/home/uosserver',
        linger: true,
        subUidRange: '589824:65536',
        activeProcesses: ['conmon'],
        containers: [
          {
            id: 'c9603dec9010',
            name: 'uosserver',
            image: 'docker.io/library/uosserver:latest',
            status: 'running',
            cpu: '1.2%',
            memory: '512M',
            ports: '25565/tcp',
          },
        ],
      },
    ];

    const identities: ServiceUser[] = [
      {
        user: 'uosserver',
        uidGid: '1001:1001',
        rolesGroups: 'podman, rootless-containers',
        authMechanism: 'Rootless SubUID 589824:65536',
        lingerSubUid: 'Linger: Enabled (1 container)',
        icon: 'deployed_code',
      },
    ];

    store.upsertHostContainers('node-likes-it-cold', {
      probedUsers,
      identities,
      rootContainers: [],
    });

    const customId: ServiceUser = {
      user: 'svc-cache',
      uidGid: '2950:2950',
      rolesGroups: 'podman',
      authMechanism: 'Rootless SubUID',
      lingerSubUid: 'Linger: Enabled',
      icon: 'deployed_code',
    };
    store.addCustomIdentity('node-likes-it-cold', customId);

    const cached = store.getHostContainers('node-likes-it-cold');
    assert.ok(cached);
    assert.equal(cached.probedUsers.length, 1);
    assert.equal(cached.probedUsers[0].user, 'uosserver');
    assert.equal(cached.customIdentities.length, 1);
    assert.equal(cached.customIdentities[0].user, 'svc-cache');
    assert.ok(cached.updatedAt > 0);
  });

  it('caches and retrieves Ansible group_vars files', () => {
    const store = createHostCacheStore(':memory:');
    const seededVars = store.seedGroupVarsIfEmpty({
      'group_vars/all.yml': 'datacenter_name: leo-banjo.ts.net\n',
    });
    assert.equal(seededVars['group_vars/all.yml'], 'datacenter_name: leo-banjo.ts.net\n');

    store.upsertGroupVar('group_vars/all.yml', 'datacenter_name: updated.ts.net\n');
    const allVars = store.getAllGroupVars();
    assert.equal(allVars['group_vars/all.yml'], 'datacenter_name: updated.ts.net\n');
  });
});
