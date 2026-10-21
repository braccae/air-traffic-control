/**
 * Live accounting of connections the ATC coordinator currently holds open.
 *
 * Every count is a refcount of a genuinely open transport:
 *  - `ssh`       : an in-flight `ssh` child process (command or PTY session)
 *  - `winrm`     : an in-flight Ansible WinRM playbook execution
 *  - `tailscale` : an in-flight `tailscale status` invocation against tailscaled
 *  - `websocket` : a live `/ws` browser connection to the coordinator
 *  - `local`     : an in-flight local loopback command execution
 *
 * Zero-Mock: nothing is incremented speculatively; `beginConnection` is only called
 * around code paths that really open the transport.
 */

export type ConnectionKind = 'ssh' | 'winrm' | 'tailscale' | 'websocket' | 'local';

export const CONNECTION_KINDS: ConnectionKind[] = ['websocket', 'ssh', 'winrm', 'tailscale', 'local'];

export interface ConnectionSnapshot {
  total: number;
  byKind: Record<ConnectionKind, number>;
  details: Record<ConnectionKind, string[]>;
  updatedAt: number;
}

const MAX_LABELS_PER_KIND = 20;

const counts: Record<ConnectionKind, number> = {
  ssh: 0,
  winrm: 0,
  tailscale: 0,
  websocket: 0,
  local: 0,
};

const labels: Record<ConnectionKind, Map<string, number>> = {
  ssh: new Map(),
  winrm: new Map(),
  tailscale: new Map(),
  websocket: new Map(),
  local: new Map(),
};

const listeners = new Set<(snapshot: ConnectionSnapshot) => void>();
let updatedAt = Date.now();

function buildSnapshot(): ConnectionSnapshot {
  const byKind = { ...counts };
  const details = {} as Record<ConnectionKind, string[]>;
  for (const kind of CONNECTION_KINDS) {
    details[kind] = Array.from(labels[kind].entries())
      .filter(([, n]) => n > 0)
      .slice(0, MAX_LABELS_PER_KIND)
      .map(([label, n]) => (n > 1 ? `${label} x${n}` : label));
  }
  return {
    total: CONNECTION_KINDS.reduce((sum, kind) => sum + byKind[kind], 0),
    byKind,
    details,
    updatedAt,
  };
}

function notify(): void {
  updatedAt = Date.now();
  const snapshot = buildSnapshot();
  for (const listener of listeners) {
    try {
      listener(snapshot);
    } catch {
      // A broken subscriber must never break connection accounting.
    }
  }
}

/** Registers an open connection. The returned release function is idempotent. */
export function beginConnection(kind: ConnectionKind, label: string): () => void {
  const safeLabel = label && label.trim() ? label.trim() : kind;
  counts[kind] += 1;
  labels[kind].set(safeLabel, (labels[kind].get(safeLabel) || 0) + 1);
  notify();

  let released = false;
  return () => {
    if (released) return;
    released = true;
    counts[kind] = Math.max(0, counts[kind] - 1);
    const remaining = (labels[kind].get(safeLabel) || 0) - 1;
    if (remaining > 0) {
      labels[kind].set(safeLabel, remaining);
    } else {
      labels[kind].delete(safeLabel);
    }
    notify();
  };
}

export function getConnectionSnapshot(): ConnectionSnapshot {
  return buildSnapshot();
}

export function onConnectionChange(listener: (snapshot: ConnectionSnapshot) => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function __resetConnectionRegistryForTests(): void {
  for (const kind of CONNECTION_KINDS) {
    counts[kind] = 0;
    labels[kind].clear();
  }
  listeners.clear();
  updatedAt = Date.now();
}
