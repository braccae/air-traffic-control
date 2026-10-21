import test from 'node:test';
import assert from 'node:assert/strict';
import {
  parseSizeToBytes,
  summarizeStorageProbes,
  summarizeRecentActivity,
  countQueueStatuses,
  formatRelativeTime,
  parseConsoleTimestamp,
  type NodeStorageProbe,
} from './datacenterSummary.ts';
import type { GlobalTaskItem, QueuedAnsibleTask } from '../types/index.ts';

const ZPOOL_LIST = [
  'NAME\tSIZE\tALLOC\tFREE\tCKPOINT\tEXPANDSZ\tFRAG\tCAP\tDEDUP\tHEALTH\tALTROOT',
  'tank\t1.81T\t900G\t950G\t-\t-\t2%\t48%\t1.00x\tONLINE\t-',
  'backup\t465G\t100G\t365G\t-\t-\t11%\t21%\t1.00x\tDEGRADED\t-',
].join('\n');

const FINDMNT_JSON = JSON.stringify({
  filesystems: [
    { target: '/', source: '/dev/nvme0n1p2', fstype: 'btrfs', size: '465.8G', used: '210.4G', avail: '255.3G' },
    { target: '/home', source: '/dev/nvme0n1p2', fstype: 'btrfs', size: '465.8G', used: '210.4G', avail: '255.3G' },
    { target: '/mnt/tank', source: 'tank', fstype: 'zfs', size: '1.8T', used: '900G', avail: '950G' },
  ],
});

const TI = 1024 ** 4;
const GI = 1024 ** 3;

test('parseSizeToBytes parses unit suffixes and refuses placeholders', () => {
  assert.equal(parseSizeToBytes('1.81T'), Math.round(1.81 * TI));
  assert.equal(parseSizeToBytes('950G'), 950 * GI);
  assert.equal(parseSizeToBytes('512M'), 512 * 1024 ** 2);
  assert.equal(parseSizeToBytes('12.4GiB'), Math.round(12.4 * GI));
  assert.equal(parseSizeToBytes(2048), 2048);
  assert.equal(parseSizeToBytes('-'), undefined);
  assert.equal(parseSizeToBytes(''), undefined);
  assert.equal(parseSizeToBytes('unknown'), undefined);
  assert.equal(parseSizeToBytes('not-a-size'), undefined);
  assert.equal(parseSizeToBytes(null), undefined);
});

test('summarizeStorageProbes aggregates real pool and mount measurements without double counting', () => {
  const probes: NodeStorageProbe[] = [
    {
      nodeId: 'node-a',
      nodeName: 'dev-pc',
      payload: { success: true, findmnt: FINDMNT_JSON, zpoolList: ZPOOL_LIST, zpoolStatus: '' },
    },
    {
      nodeId: 'node-b',
      nodeName: 'likes-it-cold',
      payload: { success: true, findmnt: '', zpoolList: 'NO_ZPOOL_LIST' },
    },
    {
      nodeId: 'node-c',
      nodeName: 'offline-host',
      fetchError: 'SSH connection to 100.64.0.9:22 refused',
    },
  ];

  const summary = summarizeStorageProbes(probes);

  assert.equal(summary.pools.length, 2);
  assert.equal(summary.probedHosts, 2);
  assert.equal(summary.unavailableHosts.length, 1);
  assert.equal(summary.unavailableHosts[0].nodeName, 'offline-host');
  assert.equal(summary.unavailableHosts[0].error, 'SSH connection to 100.64.0.9:22 refused');

  // /home and / share one source device and must be counted once.
  assert.equal(summary.filesystemCount, 2);
  assert.equal(summary.filesystemsMeasured, 2);
  assert.equal(summary.filesystemSizeBytes, Math.round(465.8 * GI) + Math.round(1.8 * TI));
  assert.equal(summary.filesystemUsedBytes, Math.round(210.4 * GI) + 900 * GI);
  assert.equal(summary.filesystemAvailBytes, Math.round(255.3 * GI) + 950 * GI);

  assert.equal(summary.poolCapacityBytes, Math.round(1.81 * TI) + 465 * GI);
  assert.equal(summary.poolFreeBytes, 950 * GI + 365 * GI);

  assert.deepEqual(
    summary.degradedPools.map(p => `${p.name}@${p.host}:${p.health}`),
    ['backup@dev-pc:DEGRADED']
  );
  assert.equal(summary.unmeasuredPools.length, 0);
  assert.equal(summary.hasData, true);
});

test('summarizeStorageProbes reports unmeasured pool sizes instead of inventing zeros', () => {
  const summary = summarizeStorageProbes([
    {
      nodeId: 'node-a',
      nodeName: 'dev-pc',
      // Status-only output: pool name + health, size columns absent.
      payload: {
        success: true,
        zpoolList: 'tank\t-\t-\t-\t-\t-\t-\t-\t-\tONLINE\t-',
        zpoolStatus: '  pool: tank\n state: ONLINE\nconfig:\n\n\tNAME        STATE\n\ttank        ONLINE\n',
      },
    },
  ]);

  assert.equal(summary.pools.length, 1);
  assert.equal(summary.pools[0].sizeBytes, undefined);
  assert.equal(summary.unmeasuredPools.length, 1);
  assert.equal(summary.poolCapacityBytes, 0);
  assert.equal(summary.hasData, true);
});

test('summarizeStorageProbes counts btrfs subvolume mounts that carry no capacity columns', () => {
  // `findmnt -J` omits SIZE/USED/AVAIL for btrfs subvolumes on a live host.
  const findmnt = JSON.stringify({
    filesystems: [
      { target: '/', source: '/dev/nvme1n1p2[/@]', fstype: 'btrfs', options: 'rw,subvol=/' },
      { target: '/home', source: '/dev/nvme1n1p2[/@home]', fstype: 'btrfs', options: 'rw,subvol=/@home' },
      { target: '/srv', source: '/dev/nvme1n1p2[/@srv]', fstype: 'btrfs', options: 'rw,subvol=/@srv' },
    ],
  });

  const summary = summarizeStorageProbes([
    { nodeId: 'node-dev-pc', nodeName: 'dev-pc', payload: { success: true, findmnt, zpoolList: '' } },
  ]);

  assert.equal(summary.filesystemCount, 3);
  assert.equal(summary.filesystemsMeasured, 0);
  assert.equal(summary.filesystemSizeBytes, 0);
  assert.equal(summary.hasData, true);
});

test('summarizeStorageProbes surfaces a failed payload error and stays empty', () => {
  const summary = summarizeStorageProbes([
    { nodeId: 'n1', nodeName: 'alpha', payload: { success: false, error: 'Storage probe failed' } },
  ]);

  assert.equal(summary.hasData, false);
  assert.equal(summary.pools.length, 0);
  assert.equal(summary.filesystemCount, 0);
  assert.equal(summary.unavailableHosts[0].error, 'Storage probe failed');
  assert.equal(summary.probedHosts, 0);
});

const consoleTasks: GlobalTaskItem[] = [
  {
    id: 't1',
    startTime: '2026-10-20 09:00:00',
    nodeTarget: 'dev-pc.leo-banjo.ts.net',
    user: 'core@ssh-agent',
    description: 'zpool status -v',
    progress: 100,
    progressEta: 'done',
    status: 'OK',
    category: 'active',
  },
  {
    id: 't2',
    startTime: '2026-10-20 09:30:00',
    nodeTarget: 'ping.leo-banjo.ts.net',
    user: 'core@ssh-agent',
    description: 'podman ps --all',
    progress: 100,
    progressEta: 'done',
    status: 'FAILED',
    category: 'active',
  },
];

const queuedTask = {
  id: 'q1',
  title: 'Restart nginx.service',
  targetNodeId: 'node-a',
  targetNodeName: 'dev-pc',
  category: 'system',
  primaryModule: 'ansible.builtin.systemd',
  steps: [{ name: 'restart', module: 'ansible.builtin.systemd', params: {} }],
  become: true,
  status: 'staged',
  createdAt: Date.parse('2026-10-20T09:45:00'),
} as unknown as QueuedAnsibleTask;

test('summarizeRecentActivity merges console and queue feeds newest-first', () => {
  const activity = summarizeRecentActivity(consoleTasks, [queuedTask], 6);

  assert.equal(activity.length, 3);
  assert.deepEqual(
    activity.map(a => a.id),
    ['queue:q1', 'console:t2', 'console:t1']
  );
  assert.equal(activity[0].source, 'queue');
  assert.equal(activity[0].status, 'STAGED');
  assert.equal(activity[0].target, 'dev-pc');
  assert.equal(activity[1].status, 'FAILED');
});

test('summarizeRecentActivity honours the limit and keeps untimestamped entries last', () => {
  const untimed = { ...consoleTasks[0], id: 't3', startTime: '' } as GlobalTaskItem;
  const activity = summarizeRecentActivity([...consoleTasks, untimed], [queuedTask], 2);

  assert.equal(activity.length, 2);
  assert.deepEqual(activity.map(a => a.id), ['queue:q1', 'console:t2']);

  const all = summarizeRecentActivity([...consoleTasks, untimed], [], 10);
  assert.equal(all[all.length - 1].id, 'console:t3');
  assert.equal(all[all.length - 1].timestamp, null);
});

test('countQueueStatuses tallies each mutation state', () => {
  const counts = countQueueStatuses([
    queuedTask,
    { ...queuedTask, id: 'q2', status: 'running' } as QueuedAnsibleTask,
    { ...queuedTask, id: 'q3', status: 'failed' } as QueuedAnsibleTask,
    { ...queuedTask, id: 'q4', status: 'completed' } as QueuedAnsibleTask,
  ]);

  assert.deepEqual(counts, { staged: 1, running: 1, completed: 1, failed: 1, cancelled: 0 });
  assert.deepEqual(countQueueStatuses([]), { staged: 0, running: 0, completed: 0, failed: 0, cancelled: 0 });
});

test('parseConsoleTimestamp and formatRelativeTime handle real and absent timestamps', () => {
  const parsed = parseConsoleTimestamp('2026-10-20 09:30:00');
  assert.equal(typeof parsed, 'number');
  assert.equal(parseConsoleTimestamp(''), null);
  assert.equal(parseConsoleTimestamp(undefined), null);

  const now = parsed as number;
  assert.equal(formatRelativeTime(null), 'time unavailable');
  assert.equal(formatRelativeTime(now, now), 'just now');
  assert.equal(formatRelativeTime(now - 5 * 60_000, now), '5m ago');
  assert.equal(formatRelativeTime(now - 3 * 3600_000, now), '3h ago');
  assert.equal(formatRelativeTime(now - 2 * 86400_000, now), '2d ago');
});
