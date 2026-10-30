import type { NodeItem } from '../types/index.ts';

export const LIVE_TAILNET_NODES: NodeItem[] = [];

/**
 * Runs a command on a remote or local node via the coordinator/daemon REST API.
 */
export async function runNodeCommand(
  node: NodeItem,
  command: string,
  timeoutMs = 6000
): Promise<string> {
  const route = node.id || node.tailscaleHost || node.ip;
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const res = await fetch(`/api/nodes/${encodeURIComponent(route)}/command`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ command, timeoutMs }),
      signal: controller.signal,
    });
    if (!res.ok) {
      throw new Error(`Command execution failed: ${res.statusText}`);
    }
    const data = await res.json();
    return typeof data === 'string' ? data : (data.stdout ?? data.output ?? '');
  } finally {
    clearTimeout(timer);
  }
}
