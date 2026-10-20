/**
 * Cluster syslog buffer for the ATC coordinator.
 *
 * Holds real runtime entries only (Zero-Mock Policy):
 *  - `app`  : the ATC coordinator's own console output (captured, not synthesised).
 *  - `poll` : one entry per real backend poll of a node/service, with measured duration.
 *  - `node` : a syslog line that the coordinator actually retrieved from a node.
 *
 * The buffer is bounded so a long-running coordinator cannot grow without limit.
 */
import { format } from 'node:util';

export type ClusterLogKind = 'app' | 'poll' | 'node';
export type ClusterLogLevel = 'debug' | 'info' | 'warn' | 'error';

export interface ClusterLogEntry {
  id: number;
  ts: number;
  kind: ClusterLogKind;
  level: ClusterLogLevel;
  source: string;
  host?: string;
  detail?: string;
  durationMs?: number;
  ok?: boolean;
  message: string;
}

export const CLUSTER_LOG_CAPACITY = 2000;

const buffer: ClusterLogEntry[] = [];
const listeners = new Set<(entry: ClusterLogEntry) => void>();
let sequence = 0;

function coerceMessage(input: ClusterLogEntry | Omit<ClusterLogEntry, 'id'>): string {
  if (input.message !== undefined && input.message !== null) return String(input.message);
  return '';
}

export function recordClusterLog(
  input: Omit<ClusterLogEntry, 'id' | 'ts'> & { ts?: number }
): ClusterLogEntry {
  const entry: ClusterLogEntry = {
    id: ++sequence,
    ts: typeof input.ts === 'number' && Number.isFinite(input.ts) ? input.ts : Date.now(),
    kind: input.kind,
    level: input.level,
    source: input.source || 'atc/coordinator',
    message: coerceMessage(input as ClusterLogEntry),
  };
  if (input.host !== undefined) entry.host = input.host;
  if (input.detail !== undefined) entry.detail = input.detail;
  if (typeof input.durationMs === 'number' && Number.isFinite(input.durationMs)) {
    entry.durationMs = Math.max(0, Math.round(input.durationMs));
  }
  if (typeof input.ok === 'boolean') entry.ok = input.ok;

  buffer.push(entry);
  if (buffer.length > CLUSTER_LOG_CAPACITY) {
    buffer.splice(0, buffer.length - CLUSTER_LOG_CAPACITY);
  }

  for (const listener of listeners) {
    try {
      listener(entry);
    } catch {
      // A broken subscriber must never break logging.
    }
  }

  return entry;
}

export function listClusterLogs(opts?: {
  limit?: number;
  kind?: ClusterLogKind;
  host?: string;
  sinceId?: number;
}): ClusterLogEntry[] {
  const limit = Math.min(Math.max(Math.trunc(opts?.limit ?? 300) || 300, 1), CLUSTER_LOG_CAPACITY);
  const kind = opts?.kind;
  const host = opts?.host;
  const sinceId = typeof opts?.sinceId === 'number' && Number.isFinite(opts.sinceId) ? opts.sinceId : undefined;

  return buffer
    .filter(entry => {
      if (kind && entry.kind !== kind) return false;
      if (host && entry.host !== host) return false;
      if (sinceId !== undefined && entry.id <= sinceId) return false;
      return true;
    })
    .slice(-limit);
}

export function onClusterLog(listener: (entry: ClusterLogEntry) => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

type ConsoleMethod = 'log' | 'info' | 'warn' | 'error' | 'debug';

const CONSOLE_LEVELS: Record<ConsoleMethod, ClusterLogLevel> = {
  log: 'info',
  info: 'info',
  warn: 'warn',
  error: 'error',
  debug: 'debug',
};

let consoleCaptureInstalled = false;

/**
 * Mirrors the coordinator's real console output into the cluster log buffer.
 * The original console methods stay in place and keep writing to stdout/stderr.
 */
export function installConsoleCapture(): void {
  if (consoleCaptureInstalled) return;
  consoleCaptureInstalled = true;

  (Object.keys(CONSOLE_LEVELS) as ConsoleMethod[]).forEach(method => {
    const original = console[method]?.bind(console);
    if (!original) return;

    console[method] = (...args: unknown[]) => {
      original(...args);
      let message: string;
      try {
        message = format(...args);
      } catch {
        message = args.map(arg => String(arg)).join(' ');
      }
      recordClusterLog({
        kind: 'app',
        level: CONSOLE_LEVELS[method],
        source: 'atc/coordinator',
        message,
      });
    };
  });
}

export function __resetClusterLogForTests(): void {
  buffer.length = 0;
  listeners.clear();
  sequence = 0;
}
