import type { GlobalTaskItem, QueuedAnsibleTask } from '../types/index.ts';
import { parseStorageMounts, parseZfsPools } from './zfsParser.ts';

/**
 * Pure aggregation helpers for the Datacenter Summary page.
 *
 * Every value returned here is derived from a real probe payload or console feed
 * entry. Nothing is synthesised: unmeasured values stay `undefined` and the UI is
 * expected to render them as "unavailable" rather than inventing numbers.
 */

const SIZE_UNITS: Record<string, number> = {
  '': 1,
  B: 1,
  K: 1024,
  KB: 1024,
  KIB: 1024,
  M: 1024 ** 2,
  MB: 1024 ** 2,
  MIB: 1024 ** 2,
  G: 1024 ** 3,
  GB: 1024 ** 3,
  GIB: 1024 ** 3,
  T: 1024 ** 4,
  TB: 1024 ** 4,
  TIB: 1024 ** 4,
  P: 1024 ** 5,
  PB: 1024 ** 5,
  PIB: 1024 ** 5,
};

/**
 * Parses a human-readable size ("1.81T" as emitted by `zpool list -H`,
 * "512M" / "12.4GiB" as emitted by `findmnt`) into bytes.
 * Returns `undefined` for placeholders ("-", "", "none") and unparseable input,
 * so callers can honestly report an unmeasured value instead of a zero.
 */
export function parseSizeToBytes(input?: string | number | null): number | undefined {
  if (input === undefined || input === null) return undefined;
  if (typeof input === 'number') {
    return Number.isFinite(input) && input >= 0 ? input : undefined;
  }

  const raw = String(input).trim();
  if (!raw || raw === '-' || raw.toLowerCase() === 'none' || raw.toLowerCase() === 'unknown') {
    return undefined;
  }

  const match = /^([0-9]*\.?[0-9]+)\s*([a-zA-Z]*)$/.exec(raw);
  if (!match) return undefined;

  const value = Number(match[1]);
  if (!Number.isFinite(value)) return undefined;

  const unit = match[2].toUpperCase();
  const multiplier = SIZE_UNITS[unit];
  if (multiplier === undefined) return undefined;

  return Math.round(value * multiplier);
}

export interface NodeStorageProbe {
  nodeId: string;
  nodeName: string;
  /** Raw JSON payload from `GET /api/nodes/:id/storage`. */
  payload?: {
    success?: boolean;
    error?: string;
    findmnt?: string;
    zfsList?: string;
    zpoolStatus?: string;
    zpoolList?: string;
    btrfs?: string;
  };
  /** Transport/parse error observed by the caller (network failure, non-JSON body, ...). */
  fetchError?: string;
}

export interface StoragePoolSummary {
  nodeId: string;
  host: string;
  name: string;
  sizeBytes?: number;
  freeBytes?: number;
  health: string;
}

export interface UnavailableHost {
  nodeId: string;
  nodeName: string;
  error: string;
}

export interface StorageSummary {
  pools: StoragePoolSummary[];
  degradedPools: StoragePoolSummary[];
  unmeasuredPools: StoragePoolSummary[];
  poolCapacityBytes: number;
  poolFreeBytes: number;
  /** Distinct mounted sources reported by the hosts (real, size-independent). */
  filesystemCount: number;
  /** Subset of `filesystemCount` whose capacity columns were actually present. */
  filesystemsMeasured: number;
  filesystemSizeBytes: number;
  filesystemUsedBytes: number;
  filesystemAvailBytes: number;
  probedHosts: number;
  unavailableHosts: UnavailableHost[];
  /** True when at least one real pool or filesystem was measured. */
  hasData: boolean;
}

export function summarizeStorageProbes(probes: NodeStorageProbe[]): StorageSummary {
  const pools: StoragePoolSummary[] = [];
  const seenPools = new Set<string>();
  const seenMounts = new Set<string>();
  const unavailableHosts: UnavailableHost[] = [];

  let filesystemCount = 0;
  let filesystemsMeasured = 0;
  let filesystemSizeBytes = 0;
  let filesystemUsedBytes = 0;
  let filesystemAvailBytes = 0;

  for (const probe of probes) {
    const payload = probe.payload;

    if (!payload || payload.success !== true) {
      unavailableHosts.push({
        nodeId: probe.nodeId,
        nodeName: probe.nodeName,
        error: probe.fetchError || payload?.error || 'Storage probe returned no data',
      });
      continue;
    }

    const parsedPools = parseZfsPools(payload.zpoolList, payload.zpoolStatus);
    for (const pool of parsedPools) {
      const key = `${probe.nodeId}:${pool.name}`;
      if (seenPools.has(key)) continue;
      seenPools.add(key);
      pools.push({
        nodeId: probe.nodeId,
        host: probe.nodeName,
        name: pool.name,
        sizeBytes: parseSizeToBytes(pool.size),
        freeBytes: parseSizeToBytes(pool.free),
        health: pool.health || 'UNKNOWN',
      });
    }

    const mounts = parseStorageMounts(payload.findmnt);
    for (const mount of mounts) {
      // Multiple mounts can share one backing source; count each device once.
      const key = `${probe.nodeId}:${(mount.source || mount.target).toLowerCase()}`;
      if (seenMounts.has(key)) continue;
      seenMounts.add(key);

      // The mount is real even when findmnt omitted its capacity columns
      // (btrfs subvolumes are commonly reported without SIZE/USED/AVAIL).
      filesystemCount += 1;

      const size = parseSizeToBytes(mount.size);
      if (size === undefined) continue;

      filesystemsMeasured += 1;
      filesystemSizeBytes += size;
      filesystemUsedBytes += parseSizeToBytes(mount.used) ?? 0;
      filesystemAvailBytes += parseSizeToBytes(mount.avail) ?? 0;
    }
  }

  const poolCapacityBytes = pools.reduce((sum, p) => sum + (p.sizeBytes ?? 0), 0);
  const poolFreeBytes = pools.reduce((sum, p) => sum + (p.freeBytes ?? 0), 0);

  const degradedPools = pools.filter(p => p.health.toUpperCase() !== 'ONLINE');
  const unmeasuredPools = pools.filter(p => p.sizeBytes === undefined);

  return {
    pools,
    degradedPools,
    unmeasuredPools,
    poolCapacityBytes,
    poolFreeBytes,
    filesystemCount,
    filesystemsMeasured,
    filesystemSizeBytes,
    filesystemUsedBytes,
    filesystemAvailBytes,
    probedHosts: probes.length - unavailableHosts.length,
    unavailableHosts,
    hasData: pools.length > 0 || filesystemCount > 0,
  };
}

export type ConsoleActivitySource = 'console' | 'queue';

export interface RecentActivityItem {
  id: string;
  source: ConsoleActivitySource;
  title: string;
  target: string;
  status: string;
  /** Epoch milliseconds, or `null` when the feed carried no parseable timestamp. */
  timestamp: number | null;
  category?: string;
}

/**
 * Parses the console feed timestamp format used by the coordinator
 * (`YYYY-MM-DD HH:MM:SS`, local time) into epoch milliseconds.
 */
export function parseConsoleTimestamp(startTime?: string): number | null {
  if (!startTime) return null;
  const normalised = startTime.trim().replace(' ', 'T');
  const parsed = Date.parse(normalised);
  return Number.isNaN(parsed) ? null : parsed;
}

/**
 * Merges the console task feed with the Ansible mutation queue into a single
 * most-recent-first activity list. Entries without a timestamp sort last.
 */
export function summarizeRecentActivity(
  tasks: GlobalTaskItem[],
  queue: QueuedAnsibleTask[],
  limit = 6
): RecentActivityItem[] {
  const fromTasks: RecentActivityItem[] = (tasks || []).map(task => ({
    id: `console:${task.id}`,
    source: 'console' as const,
    title: task.description,
    target: task.nodeTarget,
    status: task.status,
    timestamp: parseConsoleTimestamp(task.startTime),
    category: task.category,
  }));

  const fromQueue: RecentActivityItem[] = (queue || []).map(task => ({
    id: `queue:${task.id}`,
    source: 'queue' as const,
    title: task.title,
    target: task.targetNodeName || task.targetNodeId,
    status: task.status.toUpperCase(),
    timestamp: Number.isFinite(task.createdAt) ? task.createdAt : null,
    category: task.primaryModule || task.category,
  }));

  const merged = [...fromTasks, ...fromQueue].sort((a, b) => {
    if (a.timestamp === null && b.timestamp === null) return 0;
    if (a.timestamp === null) return 1;
    if (b.timestamp === null) return -1;
    return b.timestamp - a.timestamp;
  });

  return limit >= 0 ? merged.slice(0, limit) : merged;
}

export interface QueueStatusCounts {
  staged: number;
  running: number;
  completed: number;
  failed: number;
  cancelled: number;
}

export function countQueueStatuses(queue: QueuedAnsibleTask[]): QueueStatusCounts {
  const counts: QueueStatusCounts = { staged: 0, running: 0, completed: 0, failed: 0, cancelled: 0 };
  for (const task of queue || []) {
    if (task.status in counts) {
      counts[task.status] += 1;
    }
  }
  return counts;
}

/**
 * Compact relative time label ("just now", "4m ago", "3h ago", "2d ago").
 * Returns "time unavailable" when the feed carried no timestamp.
 */
export function formatRelativeTime(timestamp: number | null, now = Date.now()): string {
  if (timestamp === null) return 'time unavailable';

  const deltaMs = now - timestamp;
  if (!Number.isFinite(deltaMs)) return 'time unavailable';
  if (deltaMs < 0) return 'just now';

  const seconds = Math.floor(deltaMs / 1000);
  if (seconds < 60) return 'just now';

  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;

  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;

  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}
