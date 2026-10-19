import test from 'node:test';
import assert from 'node:assert/strict';
import {
  filterPodsByStatus,
  filterStandaloneDaemons,
  countPodmanStatuses,
  correlateIdentitiesWithProbe,
  mergeLiveProbeIntoPods,
  getContainerSecurityBadge,
  groupStandaloneContainersByUser,
  filterUserContainerGroups,
  parseDockerPsOutput,
  separatePodsAndStandaloneContainers,
} from './podmanViewModel.ts';
import { getDefaultHostPodmanProbe } from './orchestrator.ts';
import type { ContainerItem, PodItem, ServiceUser, StandaloneDaemon } from '../types/index.ts';

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
          cpu: '0.2%',
          memory: '32 MiB',
          user: 'pants',
          uid: 1000,
          rootless: true,
        },
        {
          id: '97a2a7c3695f',
          name: 'eloquent_hermann',
          image: 'localhost/dx:c1-verify',
          status: 'Up 44 hours',
          cpu: '0.3%',
          memory: '48 MiB',
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

test('getContainerSecurityBadge accurately classifies rootful vs rootless with UID', () => {
  const rootResult = getContainerSecurityBadge('root', 0, false);
  assert.equal(rootResult.isRootful, true);
  assert.equal(rootResult.label, 'Rootful / System');
  assert.equal(rootResult.rootless, false);

  const implicitRoot = getContainerSecurityBadge(undefined, 0);
  assert.equal(implicitRoot.isRootful, true);
  assert.equal(implicitRoot.label, 'Rootful / System');

  const coreResult = getContainerSecurityBadge('core', 1000, true);
  assert.equal(coreResult.isRootful, false);
  assert.equal(coreResult.label, 'Rootless (UID 1000)');
  assert.equal(coreResult.rootless, true);

  const appsResult = getContainerSecurityBadge('apps', 2000, true);
  assert.equal(appsResult.isRootful, false);
  assert.equal(appsResult.label, 'Rootless (UID 2000)');

  const mediaResult = getContainerSecurityBadge('media', 2700);
  assert.equal(mediaResult.isRootful, false);
  assert.equal(mediaResult.label, 'Rootless (UID 2700)');
});

test('groupStandaloneContainersByUser groups containers by user, placing root first and computing status counts', () => {
  const sampleStandalone: ContainerItem[] = [
    {
      id: 'app-c1',
      name: 'nextcloud-app',
      image: 'docker.io/library/nextcloud:28',
      status: 'Up 4 days',
      cpu: '0.8%',
      memory: '256 MiB',
      user: 'apps',
      uid: 2000,
      rootless: true,
    },
    {
      id: 'app-c2',
      name: 'redis-cache',
      image: 'docker.io/library/redis:7-alpine',
      status: 'Exited (137) 2 hours ago',
      cpu: '0%',
      memory: '0 B',
      user: 'apps',
      uid: 2000,
      rootless: true,
    },
    {
      id: 'media-c1',
      name: 'jellyfin',
      image: 'docker.io/jellyfin/jellyfin:latest',
      status: 'Running',
      cpu: '1.2%',
      memory: '512 MiB',
      user: 'media',
      uid: 2700,
      rootless: true,
    },
    {
      id: 'sys-c1',
      name: 'systemd-journald-proxy',
      image: 'registry.access.redhat.com/ubi9/ubi:latest',
      status: 'Up 12 days',
      cpu: '0.1%',
      memory: '32 MiB',
      user: 'root',
      uid: 0,
      rootless: false,
    },
    {
      id: 'sys-c2',
      name: 'node-exporter-host',
      image: 'quay.io/prometheus/node-exporter:v1.7.0',
      status: 'Exited (0)',
      cpu: '0%',
      memory: '0 B',
      user: 'root',
      uid: 0,
      rootless: false,
    },
  ];

  const groups = groupStandaloneContainersByUser(sampleStandalone);

  assert.equal(groups.length, 3);
  // Root group should always be first
  assert.equal(groups[0].user, 'root');
  assert.equal(groups[0].uid, 0);
  assert.equal(groups[0].rootless, false);
  assert.equal(groups[0].badge, 'Rootful / System');
  assert.equal(groups[0].totalContainers, 2);
  assert.equal(groups[0].runningContainers, 1);
  assert.equal(groups[0].stoppedContainers, 1);

  // Subsequent groups sorted alphabetically
  assert.equal(groups[1].user, 'apps');
  assert.equal(groups[1].uid, 2000);
  assert.equal(groups[1].rootless, true);
  assert.equal(groups[1].badge, 'Rootless (UID 2000)');
  assert.equal(groups[1].totalContainers, 2);
  assert.equal(groups[1].runningContainers, 1);
  assert.equal(groups[1].stoppedContainers, 1);

  assert.equal(groups[2].user, 'media');
  assert.equal(groups[2].uid, 2700);
  assert.equal(groups[2].rootless, true);
  assert.equal(groups[2].badge, 'Rootless (UID 2700)');
  assert.equal(groups[2].totalContainers, 1);
  assert.equal(groups[2].runningContainers, 1);
  assert.equal(groups[2].stoppedContainers, 0);
});

test('filterUserContainerGroups filters container groups and omits empty groups', () => {
  const sampleStandalone: ContainerItem[] = [
    {
      id: 'app-c1',
      name: 'nextcloud-app',
      image: 'nextcloud:28',
      status: 'Up 4 days',
      cpu: '0.8%',
      memory: '256 MiB',
      user: 'apps',
      uid: 2000,
      rootless: true,
    },
    {
      id: 'media-c1',
      name: 'jellyfin',
      image: 'jellyfin:latest',
      status: 'Exited (0)',
      cpu: '0%',
      memory: '0 B',
      user: 'media',
      uid: 2700,
      rootless: true,
    },
  ];

  const groups = groupStandaloneContainersByUser(sampleStandalone);
  assert.equal(groups.length, 2);

  const runningGroups = filterUserContainerGroups(groups, 'running');
  assert.equal(runningGroups.length, 1);
  assert.equal(runningGroups[0].user, 'apps');
  assert.equal(runningGroups[0].containers.length, 1);

  const stoppedGroups = filterUserContainerGroups(groups, 'stopped');
  assert.equal(stoppedGroups.length, 1);
  assert.equal(stoppedGroups[0].user, 'media');
  assert.equal(stoppedGroups[0].containers.length, 1);
});

test('parseDockerPsOutput extracts docker containers and detects docker engine presence', () => {
  // Case 1: Docker not installed
  const noDockerResult = parseDockerPsOutput('NO_DOCKER', 'NO_DOCKER');
  assert.equal(noDockerResult.hasDocker, false);
  assert.equal(noDockerResult.dockerContainers.length, 0);

  // Case 2: Docker installed with 0 containers running
  const emptyDockerResult = parseDockerPsOutput('', '/usr/bin/docker');
  assert.equal(emptyDockerResult.hasDocker, true);
  assert.equal(emptyDockerResult.dockerContainers.length, 0);

  // Case 3: Docker installed with containers
  const sampleDockerPs = `
078f3181ee17:::admiring_gauss:::ghcr.io/github/github-mcp-server:latest:::Up 24 hours
98f238a0bef2:::scanservjs:::sbs20/scanservjs:latest:::Up 3 days
pause-123:::k8s-infra:::registry.k8s.io/pause:3.9:::Up 1 hour
4a40d267c1fc:::old_worker:::local/worker:v1:::Exited (0) 5 hours ago
`;
  const activeDockerResult = parseDockerPsOutput(sampleDockerPs, '/usr/bin/docker');
  assert.equal(activeDockerResult.hasDocker, true);
  // Infra pause container should be filtered out
  assert.equal(activeDockerResult.dockerContainers.length, 3);

  const gauss = activeDockerResult.dockerContainers.find(c => c.name === 'admiring_gauss')!;
  assert.ok(gauss);
  assert.equal(gauss.id, '078f3181ee17');
  assert.equal(gauss.image, 'ghcr.io/github/github-mcp-server:latest');
  assert.equal(gauss.status, 'Up 24 hours');
  assert.equal(gauss.rootless, false);
  assert.equal(gauss.user, 'docker');

  const oldWorker = activeDockerResult.dockerContainers.find(c => c.name === 'old_worker')!;
  assert.ok(oldWorker);
  assert.equal(oldWorker.status, 'Exited (0) 5 hours ago');
});

test('separatePodsAndStandaloneContainers separates podded containers from standalone containers', () => {
  const mixedContainers: ContainerItem[] = [
    {
      id: 'c1',
      name: 'caddy-proxy',
      image: 'caddy:alpine',
      status: 'Running',
      cpu: '0.1%',
      memory: '16 MiB',
      user: 'root',
      uid: 0,
      rootless: false,
      podId: 'ingress-pod',
      podName: 'pod: ingress',
    },
    {
      id: 'c2',
      name: 'dnsmasq',
      image: 'dnsmasq:latest',
      status: 'Running',
      cpu: '0.1%',
      memory: '8 MiB',
      user: 'root',
      uid: 0,
      rootless: false,
      podId: 'ingress-pod',
      podName: 'pod: ingress',
    },
    {
      id: 'c3',
      name: 'standalone-worker',
      image: 'worker:latest',
      status: 'Running',
      cpu: '0.5%',
      memory: '64 MiB',
      user: 'apps',
      uid: 2000,
      rootless: true,
    },
  ];

  const { pods, standaloneContainers } = separatePodsAndStandaloneContainers(mixedContainers);

  assert.equal(pods.length, 1);
  assert.equal(pods[0].id, 'ingress-pod');
  assert.equal(pods[0].name, 'pod: ingress');
  assert.equal(pods[0].containers.length, 2);
  assert.equal(pods[0].user, 'root');
  assert.equal(pods[0].rootless, false);

  assert.equal(standaloneContainers.length, 1);
  assert.equal(standaloneContainers[0].name, 'standalone-worker');
  assert.equal(standaloneContainers[0].user, 'apps');
});


