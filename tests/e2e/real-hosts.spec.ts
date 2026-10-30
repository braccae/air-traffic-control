import { test, expect, type APIRequestContext } from '@playwright/test';
import { lookup } from 'node:dns/promises';

/**
 * Real-host SSH verification.
 *
 * Two layers are covered:
 *
 *   1. **Inventory nodes** — exercised through ATC's own transport chain
 *      (`/api/nodes/:id/interfaces` → connection plugin → tsnet/ssh → vault
 *      credential). This is the path the application actually uses and the only
 *      way to catch a regression where `vaultSecretId` is persisted but never
 *      reaches the connection plugin. It is also the only path that can verify
 *      Tailscale SSH (keyless) hosts, because their authentication happens at
 *      the Tailscale control layer rather than in the SSH handshake.
 *
 *   2. **Ad-hoc hosts** — `POST /api/ssh/test` for hosts that are not (yet) in
 *      the inventory. That endpoint performs a plain TCP SSH dial, so it can
 *      only verify credential-based auth: the host must accept the shared
 *      `ssh_key` vault variable.
 *
 * Nothing is faked: a host that is offline, misspelled, or missing the key is
 * reported with the real transport error.
 *
 * Overrides (all optional):
 *   ATC_API_URL ATC_WEB_URL ATC_SSH_KEY_NAME
 *   ATC_HOST_FRED ATC_USER_FRED ATC_HOST_SHORTS ... (see each entry)
 */

const API_BASE = process.env.ATC_API_URL || 'http://host.containers.internal:18090';
const KEY_NAME = process.env.ATC_SSH_KEY_NAME || 'ssh_key';

type AuthMode = 'vault-key' | 'tailscale-ssh';

interface InventoryHost {
  label: string;
  host: string;
  auth: AuthMode;
  nodeId: string;
}

interface AdHocHost {
  label: string;
  host: string;
  user: string;
}

/** Nodes already onboarded; the credential reference must reach the transport. */
const INVENTORY_HOSTS: InventoryHost[] = [
  {
    label: 'fred',
    host: process.env.ATC_HOST_FRED || '100.100.173.57',
    auth: 'tailscale-ssh',
    nodeId: 'node-fred',
  },
  {
    label: '192.168.16.235',
    host: process.env.ATC_HOST_LAN_235 || '192.168.16.235',
    auth: 'vault-key',
    nodeId: 'node-dev-pc',
  },
  {
    label: '192.168.8.249',
    host: process.env.ATC_HOST_LAN_249 || '192.168.8.249',
    auth: 'vault-key',
    nodeId: 'node-likes-it-cold',
  },
  {
    label: '192.168.8.3',
    host: process.env.ATC_HOST_LAN_3 || '192.168.8.3',
    auth: 'vault-key',
    nodeId: 'node-the-freezer',
  },
];

/** Hosts not in the inventory; verified with the shared key before onboarding. */
const ADHOC_HOSTS: AdHocHost[] = [
  {
    label: 'shorts',
    host: process.env.ATC_HOST_SHORTS || '192.168.8.184',
    user: process.env.ATC_USER_SHORTS || 'root',
  },
  {
    label: 'openwrt',
    host: process.env.ATC_HOST_OPENWRT || 'openwrt',
    user: process.env.ATC_USER_OPENWRT || 'root',
  },
];

async function requireSharedKey(request: APIRequestContext) {
  const res = await request.get(`${API_BASE}/api/vault/variables`);
  expect(res.status()).toBe(200);
  const body = await res.json();
  const names: string[] = (body.variables ?? []).map((v: { name: string }) => v.name);
  test.skip(
    !names.includes(KEY_NAME),
    `Vault variable "${KEY_NAME}" does not exist yet. Add it under Settings → Variables → SSH Vault Secrets.`,
  );
}

async function isResolvable(host: string): Promise<boolean> {
  // IP literals are always usable; hostnames must resolve from the test container.
  if (/^\d{1,3}(\.\d{1,3}){3}$/.test(host) || host.includes(':')) return true;
  try {
    await lookup(host);
    return true;
  } catch {
    return false;
  }
}

test.describe('Real host SSH verification', () => {
  test.describe.configure({ timeout: 60_000 });

  // ── Inventory nodes: full transport chain (plugin + tsnet + vault secret) ──
  for (const spec of INVENTORY_HOSTS) {
    test(`${spec.label} resolves through its inventory node (${spec.nodeId})`, async ({ request }) => {
      if (spec.auth === 'vault-key') {
        await requireSharedKey(request);
      }

      const nodesRes = await request.get(`${API_BASE}/api/nodes`);
      expect(nodesRes.status()).toBe(200);
      const nodes: Array<{ id: string; vaultSecretId?: string }> = await nodesRes.json();
      const node = nodes.find(n => n.id === spec.nodeId);
      test.skip(!node, `Inventory node ${spec.nodeId} is not present; add ${spec.host} first.`);

      if (spec.auth === 'vault-key') {
        // The credential reference must survive the round-trip to the column,
        // not just live in node_json (a dropped column made every connection
        // offer zero auth methods).
        expect(
          node!.vaultSecretId,
          `${spec.label}: node ${spec.nodeId} does not reference ${KEY_NAME}`,
        ).toBe(KEY_NAME);
      }

      const res = await request.get(
        `${API_BASE}/api/nodes/${encodeURIComponent(spec.nodeId)}/interfaces`,
      );
      const body = await res.json();
      expect(
        body.error,
        `${spec.label}: ${body.error || `HTTP ${res.status()} returned no interfaces`}`,
      ).toBeUndefined();
      expect(Array.isArray(body.interfaces)).toBe(true);
      expect(body.interfaces.length).toBeGreaterThan(0);
      // Real data from the host, never a synthetic placeholder.
      expect(body.interfaces[0]).toHaveProperty('name');
    });
  }

  // ── Ad-hoc hosts: credential check before onboarding ──────────────────────
  for (const spec of ADHOC_HOSTS) {
    test(`${spec.label} accepts ${KEY_NAME} at ${spec.host}`, async ({ request }) => {
      await requireSharedKey(request);

      const resolvable = await isResolvable(spec.host);
      test.skip(
        !resolvable,
        `${spec.label}: "${spec.host}" does not resolve from the test container. ` +
          `Set ATC_HOST_${spec.label.toUpperCase().replace(/[^A-Z0-9]+/g, '_')} to its reachable address.`,
      );

      const res = await request.post(`${API_BASE}/api/ssh/test`, {
        data: {
          host: spec.host,
          port: 22,
          sshUser: spec.user,
          authMethod: 'vault-sshkey',
          vaultSecretId: KEY_NAME,
          timeoutMs: 15_000,
        },
      });

      const body = await res.json();
      expect(body.success, `${spec.label}: ${body.error || `HTTP ${res.status()}`}`).toBe(true);
      expect(body.output).toContain(`user=${spec.user}`);
      expect(body.output).toContain('host=');
      expect(body.authMethods).toContain(`vault-private-key:${KEY_NAME}`);
    });
  }

  test('tag chunks can reference the same shared key by name', async ({ request }) => {
    await requireSharedKey(request);

    const runId = `${Date.now()}${Math.random().toString(36).slice(2, 6)}`;
    const chunks = [
      { id: `e2e-chunk-pve-${runId}`, tag: `e2e-pve-${runId}`, user: 'root' },
      { id: `e2e-chunk-host-${runId}`, tag: `e2e-host-${runId}`, user: 'core' },
    ];

    try {
      for (const chunk of chunks) {
        const res = await request.post(`${API_BASE}/api/inventory/chunks`, {
          data: {
            id: chunk.id,
            tag: chunk.tag,
            sshUser: chunk.user,
            authMethod: 'vault-sshkey',
            vaultSecretId: KEY_NAME,
          },
        });
        expect(res.status()).toBe(200);
        const body = await res.json();
        expect(body.chunk.tag).toBe(`tag:${chunk.tag}`);
        expect(body.chunk.sshUser).toBe(chunk.user);
        expect(body.chunk.vaultSecretId).toBe(KEY_NAME);
      }

      const listed = await (await request.get(`${API_BASE}/api/inventory/chunks`)).json();
      for (const chunk of chunks) {
        const stored = listed.chunks.find((c: { id: string }) => c.id === chunk.id);
        expect(stored).toBeTruthy();
        expect(stored.vaultSecretId).toBe(KEY_NAME);
        expect(stored.sshUser).toBe(chunk.user);
      }
    } finally {
      for (const chunk of chunks) {
        await request.delete(`${API_BASE}/api/inventory/chunks/${encodeURIComponent(chunk.id)}`);
      }
    }
  });
});
