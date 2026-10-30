import type { NodeItem } from '../types/index.ts';

/**
 * The aliases this user interface may receive for the machine that is
 * running the frontend itself.
 */
export const LOCAL_TARGETS = ['localhost', '127.0.0.1', '::1'] as const;

export type LocalTarget = (typeof LOCAL_TARGETS)[number];

/**
 * True when `value` is a hostname/address alias for the local machine.
 */
export function isLocalTarget(value: string): value is LocalTarget {
  return (LOCAL_TARGETS as readonly string[]).includes(value);
}

/**
 * The local node is the registered node that represents this machine.
 *
 * It is looked up by address (127.0.0.1 / localhost / ::1) or by the
 * short name the local machine is registered under (`dev-pc`).
 */
export function findLocalNode(nodes: NodeItem[] | null | undefined): NodeItem | null {
  if (!nodes) return null;
  return nodes.find(
    (n) =>
      n.ip === '127.0.0.1' ||
      n.ip === 'localhost' ||
      n.ip === '::1' ||
      n.shortName === 'dev-pc' ||
      n.tailscaleHost?.startsWith('dev-pc'),
  ) ?? null;
}

/**
 * Resolve a node id / target string to the ID of a node that actually
 * exists in the registered inventory.
 *
 * Resolve a node id / target string to the ID of a node that actually
 * exists in the registered inventory.
 *
 * Any value is returned unchanged and is verified by the backend.
 */
export function resolveModuleNodeId(targetId: string, nodes: NodeItem[] | null | undefined): string {
  const trimmed = targetId.trim();
  if (!trimmed) return '';

  // Never propagate a fabricated id such as 'local-host'.
  if (trimmed === 'local-host' || trimmed === 'local_host') {
    return '';
  }

  // An id that already exists in the registered inventory is valid as-is.
  if (nodes && nodes.some((n) => n.id === trimmed)) {
    return trimmed;
  }

  // Hostname/address aliases resolve to the registered local node.
  if (isLocalTarget(trimmed)) {
    const local = findLocalNode(nodes);
    return local ? local.id : trimmed;
  }

  return trimmed;
}
