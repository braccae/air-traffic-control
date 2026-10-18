import test from 'node:test';
import assert from 'node:assert/strict';
import { calculateSystemHealth, filterContainersByStatus, formatBytes, parseTerminalCommand } from './orchestrator.ts';
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
  assert.match(helpRes.output, /PROX-ORCH Integrated Terminal Helper/);

  const psRes = parseTerminalCommand('podman ps', dummyNode);
  assert.equal(psRes.exitCode, 0);
  assert.match(psRes.output, /CONTAINER ID/);
});

test('parseTerminalCommand responds to unknown commands with error 127', () => {
  const unknownRes = parseTerminalCommand('foobar123', dummyNode);
  assert.equal(unknownRes.exitCode, 127);
  assert.match(unknownRes.output, /command not found/);
});
