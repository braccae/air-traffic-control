import test from 'node:test';
import assert from 'node:assert/strict';
import {
  calculateSystemHealth,
  filterContainersByStatus,
  formatBytes,
  getDefaultHostPodmanProbe,
  parseTerminalCommand,
  probeUnprivilegedPodmanUsers,
} from './orchestrator.ts';
import type { ContainerItem, NodeItem } from '../types/index.ts';

const dummyNode: NodeItem = {
  id: 'node-150-openmediavault',
  name: '150 (openmediavault)',
  shortName: '150',
  group: 'cold-war',
  datacenter: 'homelab-dc1',
  nodeType: 'linux',
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
};

test('calculateSystemHealth returns HEALTHY on nominal metrics', () => {
  const health = calculateSystemHealth({ cpuUsage: 10, ioDelayPercent: 0.1, cpuTempC: 40 });
  assert.equal(health, 'HEALTHY');
});

test('calculateSystemHealth returns WARNING on moderate load or temp', () => {
  const health = calculateSystemHealth({ cpuUsage: 75, ioDelayPercent: 1.0, cpuTempC: 50 });
  assert.equal(health, 'WARNING');
});

test('calculateSystemHealth returns CRITICAL on high load or temperature', () => {
  const health = calculateSystemHealth({ cpuUsage: 95, ioDelayPercent: 20, cpuTempC: 90 });
  assert.equal(health, 'CRITICAL');
});

test('filterContainersByStatus filters running and stopped containers accurately', () => {
  const containers: ContainerItem[] = [
    { id: '1', name: 'app1', image: 'nginx', status: 'Running', cpu: '1%', memory: '10MiB' },
    { id: '2', name: 'app2', image: 'redis', status: 'Running (VAAPI)', cpu: '2%', memory: '20MiB' },
    { id: '3', name: 'app3', image: 'test', status: 'Exited (code 0)', cpu: '0%', memory: '0MiB' },
  ];

  const running = filterContainersByStatus(containers, 'running');
  assert.equal(running.length, 2);
  assert.equal(running[0].name, 'app1');

  const stopped = filterContainersByStatus(containers, 'stopped');
  assert.equal(stopped.length, 1);
  assert.equal(stopped[0].name, 'app3');

  const all = filterContainersByStatus(containers, 'all');
  assert.equal(all.length, 3);
});

test('formatBytes properly scales units', () => {
  assert.equal(formatBytes(0), '0 B');
  assert.equal(formatBytes(1024), '1 KiB');
  assert.equal(formatBytes(1048576), '1 MiB');
  assert.equal(formatBytes(1073741824 * 5.5), '5.5 GiB');
});

test('parseTerminalCommand responds to help and podman ps', () => {
  const helpRes = parseTerminalCommand('help', dummyNode);
  assert.equal(helpRes.exitCode, 0);
  assert.match(helpRes.output, /Air Traffic Control Integrated Terminal Helper/);

  const psRes = parseTerminalCommand('podman ps', dummyNode);
  assert.equal(psRes.exitCode, 0);
  assert.match(psRes.output, /CONTAINER ID/);
});

test('parseTerminalCommand responds to unknown commands with error 127', () => {
  const unknownRes = parseTerminalCommand('foobar123', dummyNode);
  assert.equal(unknownRes.exitCode, 127);
  assert.match(unknownRes.output, /command not found/);
});

test('probeUnprivilegedPodmanUsers discovers unprivileged users running podman containers and excludes root/idle users', () => {
  const result = probeUnprivilegedPodmanUsers({
    psOutput: [
      '0 root /usr/lib/systemd/systemd --system',
      '0 root /usr/bin/conmon -c root-container -n system-proxy',
      '33 www-data nginx: worker process',
      '1000 deployer /usr/bin/conmon --api-version 1 -c 3f0a9918bc22 -n jellyfin-media-server',
      '1000 deployer /usr/libexec/podman/catatonit -P',
      '1001 podman /usr/bin/pasta --config-net',
      '1001 podman /usr/bin/conmon --api-version 1 -c 7e4b9019a101 -n ollama-vllm-engine',
      '1002 idleuser -bash',
    ].join('\n'),
    passwdOutput: [
      'root:x:0:0:root:/root:/bin/bash',
      'www-data:x:33:33:www-data:/var/www:/usr/sbin/nologin',
      'deployer:x:1000:1000:Deployer:/home/deployer:/bin/bash',
      'podman:x:1001:1001:Podman:/home/podman:/bin/bash',
      'idleuser:x:1002:1002:Idle:/home/idleuser:/bin/bash',
      'svc-backup:x:984:984:Backup:/var/lib/restic:/sbin/nologin',
    ].join('\n'),
    subuidOutput: [
      'deployer:100000:65536',
      'podman:165536:65536',
      'idleuser:231072:65536',
      'svc-backup:296608:65536',
    ].join('\n'),
    lingerUsers: ['deployer', 'podman', 'svc-backup'],
    userContainers: {
      deployer: [
        {
          id: '3f0a9918bc22',
          name: 'jellyfin-media-server',
          image: 'jellyfin/jellyfin:10.8.13',
          status: 'Running (VAAPI)',
          cpu: '2.14%',
          memory: '1.68 GiB',
        },
      ],
      podman: [
        {
          id: '7e4b9019a101',
          name: 'ollama-vllm-engine',
          image: 'ollama/ollama:latest',
          status: 'Running (CUDA 12.4)',
          cpu: '3.8%',
          memory: '6.4 GiB VRAM',
        },
      ],
      'svc-backup': [
        {
          id: '8811aabbccdd',
          name: 'restic-exporter',
          image: 'ngosang/restic-exporter:latest',
          status: 'Running',
          cpu: '0.1%',
          memory: '12 MiB',
        },
      ],
      idleuser: [],
    },
  });

  assert.equal(result.length, 3);
  const users = result.map(u => u.user);
  assert.deepEqual(users, ['deployer', 'podman', 'svc-backup']);

  const deployer = result.find(u => u.user === 'deployer')!;
  assert.equal(deployer.uid, 1000);
  assert.equal(deployer.subUidRange, '100000:65536');
  assert.equal(deployer.linger, true);
  assert.equal(deployer.containers.length, 1);
  assert.equal(deployer.containers[0].rootless, true);
  assert.equal(deployer.containers[0].user, 'deployer');
  assert.equal(deployer.containers[0].uid, 1000);
});

test('probeUnprivilegedPodmanUsers extracts containers from conmon args when userContainers map is unavailable', () => {
  const result = probeUnprivilegedPodmanUsers({
    psOutput: '1050 media+ /usr/bin/conmon --api-version 1 --cid=998877665544 -nimmich-server\n1050 media+ /usr/bin/conmon -c aabbccddeeff -n immich-ml\n',
    passwdOutput: 'media-runner:x:1050:1050::/home/media-runner:/sbin/nologin\n',
    subuidOutput: 'media-runner:362144:65536\n',
  });

  assert.equal(result.length, 1);
  assert.equal(result[0].user, 'media-runner');
  assert.equal(result[0].uid, 1050);
  assert.equal(result[0].containers.length, 2);
  assert.equal(result[0].containers[0].id, '998877665544');
  assert.equal(result[0].containers[0].name, 'immich-server');
  assert.equal(result[0].containers[0].image, 'immich-server:latest');
  assert.notEqual(result[0].containers[0].image, 'rootless/container');
  assert.equal(result[0].containers[0].cpu, '');
  assert.equal(result[0].containers[0].memory, '');
  assert.equal(result[0].containers[0].rootless, true);

  assert.equal(result[0].containers[1].id, 'aabbccddeeff');
  assert.equal(result[0].containers[1].name, 'immich-ml');
  assert.equal(result[0].containers[1].image, 'immich-ml:latest');
  assert.notEqual(result[0].containers[1].image, 'rootless/container');
  assert.equal(result[0].containers[1].cpu, '');
  assert.equal(result[0].containers[1].memory, '');
  assert.equal(result[0].containers[1].rootless, true);
});

test('probeUnprivilegedPodmanUsers associates userPods with discovered rootless users', () => {
  const result = probeUnprivilegedPodmanUsers({
    psOutput: '1000 deployer /usr/bin/conmon -c c1 -n caddy\n',
    passwdOutput: 'deployer:x:1000:1000::/home/deployer:/bin/bash\n',
    subuidOutput: 'deployer:100000:65536\n',
    userPods: {
      deployer: [
        {
          id: 'web-pod',
          name: 'pod: web-pod',
          containersRunningCount: 1,
          ports: '80:80',
          totalCpu: '0.5%',
          totalRam: '100 MiB',
          containers: [],
        },
      ],
    },
  });

  assert.equal(result.length, 1);
  assert.equal(result[0].user, 'deployer');
  assert.ok(result[0].pods && result[0].pods.length === 1);
  assert.equal(result[0].pods[0].id, 'web-pod');
  assert.equal(result[0].pods[0].user, 'deployer');
  assert.equal(result[0].pods[0].uid, 1000);
  assert.equal(result[0].pods[0].rootless, true);
});

test('parseTerminalCommand supports podman probe to list unprivileged users running podman containers', () => {
  const probeRes = parseTerminalCommand('podman probe', dummyNode);
  assert.equal(probeRes.exitCode, 0);
  assert.match(probeRes.output, /deployer/);
  assert.match(probeRes.output, /podman/);
  assert.match(probeRes.output, /1000/);
  assert.match(probeRes.output, /1001/);
});

test('getDefaultHostPodmanProbe returns unprivileged users running podman containers', () => {
  const users = getDefaultHostPodmanProbe();
  assert.ok(users.length >= 2);
  for (const u of users) {
    assert.notEqual(u.uid, 0);
    assert.notEqual(u.user, 'root');
    assert.ok(u.containers.length > 0);
    for (const c of u.containers) {
      assert.equal(c.rootless, true);
      assert.equal(c.user, u.user);
      assert.equal(c.uid, u.uid);
    }
  }
});


