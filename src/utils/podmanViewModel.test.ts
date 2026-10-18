import test from 'node:test';
import assert from 'node:assert/strict';
import {
  filterPodsByStatus,
  filterStandaloneDaemons,
  countPodmanStatuses,
  correlateIdentitiesWithProbe,
  mergeLiveProbeIntoPods,
} from './podmanViewModel.ts';
import { getDefaultHostPodmanProbe } from './orchestrator.ts';
import type { PodItem, ServiceUser, StandaloneDaemon } from '../types/index.ts';

const samplePods: PodItem[] = [
  {
    id: 'media-stack',
    name: 'pod: media-stack',
    containersRunningCount: 2,
    ports: 'Ports: 8096:8096, 443:443',
    totalCpu: '2.4%',
    totalRam: '1.82 GiB',
    user: 'deployer',
    uid: 1000,
    rootless: true,
    containers: [
      {
        id: 'c1',
        name: 'infra-con-media',
        image: 'k8s.gcr.io/pause:3.9',
        status: 'Running',
        cpu: '0.01%',
        memory: '4.2 MiB',
        user: 'deployer',
        uid: 1000,
        rootless: true,
      },
      {
        id: 'c2',
        name: 'jellyfin-media-server',
        image: 'jellyfin/jellyfin:10.8.13',
        status: 'Running (VAAPI)',
        cpu: '2.14%',
        memory: '1.68 GiB',
        user: 'deployer',
        uid: 1000,
        rootless: true,
      },
      {
        id: 'c3',
        name: 'caddy-old',
        image: 'caddy:2.7.6-alpine',
        status: 'Exited (code 0)',
        cpu: '0%',
        memory: '0 B',
        user: 'deployer',
        uid: 1000,
        rootless: true,
      },
    ],
  },
  {
    id: 'ai-inference',
    name: 'pod: ai-inference',
    containersRunningCount: 2,
    ports: 'Ports: 11434:11434',
    totalCpu: '4.8%',
    totalRam: '6.9 GiB',
    user: 'podman',
    uid: 1001,
    rootless: true,
    containers: [
      {
        id: 'c4',
        name: 'ollama-vllm-engine',
        image: 'ollama/ollama:latest',
        status: 'Running (CUDA 12.4)',
        cpu: '3.8%',
        memory: '6.4 GiB VRAM',
        user: 'podman',
        uid: 1001,
        rootless: true,
      },
    ],
  },
];

const sampleDaemons: StandaloneDaemon[] = [
  {
    name: 'restic-exporter',
    subtext: 'prom-metrics:9150 (restic-svc:984)',
    status: 'running',
    memory: '12 MiB',
    user: 'restic-svc',
    uid: 984,
    rootless: true,
  },
  {
    name: 'node-exporter-old',
    subtext: 'Exited (code 0)',
    status: 'exited',
    memory: '0 B',
    user: 'podman',
    uid: 1001,
    rootless: true,
  },
];

test('filterPodsByStatus filters pod containers and omits empty pods for running/stopped', () => {
  const allPods = filterPodsByStatus(samplePods, 'all');
  assert.equal(allPods.length, 2);
  assert.equal(allPods[0].containers.length, 3);

  const runningPods = filterPodsByStatus(samplePods, 'running');
  assert.equal(runningPods.length, 2);
  assert.equal(runningPods[0].containers.length, 2);
  assert.equal(runningPods[1].containers.length, 1);

  const stoppedPods = filterPodsByStatus(samplePods, 'stopped');
  assert.equal(stoppedPods.length, 1);
  assert.equal(stoppedPods[0].id, 'media-stack');
  assert.equal(stoppedPods[0].containers.length, 1);
  assert.equal(stoppedPods[0].containers[0].name, 'caddy-old');
});

test('filterStandaloneDaemons filters standalone daemons by status', () => {
  assert.equal(filterStandaloneDaemons(sampleDaemons, 'all').length, 2);

  const running = filterStandaloneDaemons(sampleDaemons, 'running');
  assert.equal(running.length, 1);
  assert.equal(running[0].name, 'restic-exporter');

  const stopped = filterStandaloneDaemons(sampleDaemons, 'stopped');
  assert.equal(stopped.length, 1);
  assert.equal(stopped[0].name, 'node-exporter-old');
});

test('countPodmanStatuses computes accurate counts across pod containers and standalone daemons', () => {
  const counts = countPodmanStatuses(samplePods, sampleDaemons);
  assert.deepEqual(counts, {
    all: 6,
    running: 4,
    stopped: 2,
  });
});

test('correlateIdentitiesWithProbe identifies unprivileged users running podman containers and verifies UIDs and container counts', () => {
  const identities: ServiceUser[] = [
    {
      user: 'deployer',
      uidGid: '1000:1000',
      rolesGroups: 'wheel, sudo, docker',
      authMechanism: 'ssh-ed25519 (Ansible Vault managed)',
      lingerSubUid: 'Linger: Yes',
      icon: 'badge',
    },
    {
      user: 'restic-svc',
      uidGid: '984:984',
      rolesGroups: 'backup-operators',
      authMechanism: '/sbin/nologin (systemd-timer)',
      lingerSubUid: 'Linger: No',
      icon: 'smart_toy',
    },
    {
      user: 'podman',
      uidGid: '1001:1001',
      rolesGroups: 'podman, systemd-journal',
      authMechanism: 'Rootless SubUID 100000-165535',
      lingerSubUid: 'Linger: Enabled',
      icon: 'deployed_code',
    },
    {
      user: 'cockpit-wsinstance',
      uidGid: '977:977',
      rolesGroups: 'cockpit-ws, ssl-cert',
      authMechanism: 'Unix Socket Web PKI',
      lingerSubUid: 'PAM GSSAPI',
      icon: 'terminal',
    },
  ];

  const probedUsers = getDefaultHostPodmanProbe();
  const correlated = correlateIdentitiesWithProbe(identities, probedUsers);

  assert.equal(correlated.length, 4);

  const deployer = correlated.find(c => c.user === 'deployer')!;
  assert.equal(deployer.isRunningPodman, true);
  assert.equal(deployer.uid, 1000);
  assert.ok(deployer.uid > 0);
  assert.equal(deployer.containerCount, 3);

  const podman = correlated.find(c => c.user === 'podman')!;
  assert.equal(podman.isRunningPodman, true);
  assert.equal(podman.uid, 1001);
  assert.ok(podman.uid > 0);
  assert.equal(podman.containerCount, 2);

  const restic = correlated.find(c => c.user === 'restic-svc')!;
  assert.equal(restic.isRunningPodman, true);
  assert.equal(restic.uid, 984);
  assert.ok(restic.uid > 0);
  assert.equal(restic.containerCount, 1);

  const cockpit = correlated.find(c => c.user === 'cockpit-wsinstance')!;
  assert.equal(cockpit.isRunningPodman, false);
  assert.equal(cockpit.uid, 977);
  assert.ok(cockpit.uid > 0);
  assert.equal(cockpit.containerCount, 0);
});

test('buildPodmanStateFromProbe converts live probed unprivileged users into PodItems, StandaloneDaemons, and dynamic ServiceUsers', () => {
  const liveProbe = [
    {
      user: 'pants',
      uid: 1000,
      gid: 1000,
      homeDir: '/home/pants',
      subUidRange: '100000:65536',
      linger: true,
      activeProcesses: [
        '/usr/bin/conmon -c 628e92ae175f -n hungry_swirles',
        '/usr/bin/conmon -c 97a2a7c3695f -n eloquent_hermann',
      ],
      containers: [
        {
          id: '628e92ae175f',
          name: 'hungry_swirles',
          image: 'localhost/alma:bug5-test',
          status: 'Up 44 hours',
          user: 'pants',
          uid: 1000,
          rootless: true,
        },
        {
          id: '97a2a7c3695f',
          name: 'eloquent_hermann',
          image: 'localhost/dx:c1-verify',
          status: 'Up 44 hours',
          user: 'pants',
          uid: 1000,
          rootless: true,
        },
      ],
    },
  ];

  const baseIdentities: ServiceUser[] = [
    {
      user: 'deployer',
      uidGid: '1000:1000',
      rolesGroups: 'wheel, sudo, docker',
      authMechanism: 'ssh-ed25519 (Ansible Vault managed)',
      lingerSubUid: 'Linger: Yes',
      icon: 'badge',
    },
  ];

  const correlated = correlateIdentitiesWithProbe(baseIdentities, liveProbe);
  // Should include newly discovered live user 'pants' even if not in baseIdentities!
  const pantsIdentity = correlated.find(c => c.user === 'pants');
  assert.ok(pantsIdentity, 'expected newly probed unprivileged user pants to be added to correlated identities');
  assert.equal(pantsIdentity.uid, 1000);
  assert.equal(pantsIdentity.isRunningPodman, true);
  assert.equal(pantsIdentity.containerCount, 2);

  const mergedPods = mergeLiveProbeIntoPods(samplePods, liveProbe);
  assert.equal(mergedPods.length, 3);
  assert.equal(mergedPods[0].id, 'rootless-pants-1000');
  assert.equal(mergedPods[0].user, 'pants');
  assert.equal(mergedPods[0].uid, 1000);
  assert.equal(mergedPods[0].containers.length, 2);
});

