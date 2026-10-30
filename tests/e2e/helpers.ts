import { test, expect, type Page, type APIRequestContext } from '@playwright/test';

export interface TestHost {
  id: string;
  name: string;
  tailscaleHost?: string;
  ip?: string;
  port?: number;
  sshUser?: string;
  datacenter?: string;
  group?: string;
  status?: string;
  authMethod?: string;
  sshConnected?: boolean;
  hasRestic?: boolean;
  hasPodman?: boolean;
  hasGlances?: boolean;
}

export interface TestSetup {
  page: Page;
  request: APIRequestContext;
  createdHosts: TestHost[];
}

const API_BASE = process.env.ATC_API_URL || 'http://host.containers.internal:18090';

// ── Fixtures ─────────────────────────────────────────────────────────────────
// Browsers come from the Playwright image itself; the default `page` fixture
// launches one per test. No CDP/browserless hop.
export const testWithContainer = test.extend<TestSetup>({
  createdHosts: async ({}, use) => {
    const hosts: TestHost[] = [];
    await use(hosts);
    for (const h of hosts) {
      try {
        await fetch(`${API_BASE}/api/nodes/${encodeURIComponent(h.id)}`, { method: 'DELETE' });
      } catch {
        /* ignore */
      }
    }
  },
});

// ── Safe host data ────────────────────────────────────────────────

export const SAFE_HOSTS: TestHost[] = [
  {
    id: 'test-node-alpha',
    name: 'alpha-test',
    tailscaleHost: 'alpha-test.ts.net',
    ip: '100.64.0.10',
    port: 22,
    sshUser: 'core',
    datacenter: 'ts.net',
    group: 'tag:test',
    status: 'online',
    authMethod: 'ssh-agent',
    sshConnected: true,
    hasRestic: true,
    hasPodman: true,
    hasGlances: true,
  },
  {
    id: 'test-node-beta',
    name: 'beta-test',
    tailscaleHost: 'beta-test.ts.net',
    ip: '100.64.0.11',
    port: 22,
    sshUser: 'core',
    datacenter: 'ts.net',
    group: 'tag:test',
    status: 'online',
    authMethod: 'ssh-agent',
    sshConnected: true,
    hasRestic: false,
    hasPodman: true,
    hasGlances: false,
  },
  {
    id: 'test-node-gamma',
    name: 'gamma-test',
    tailscaleHost: 'gamma-test.ts.net',
    ip: '100.64.0.12',
    port: 2222,
    sshUser: 'apps',
    datacenter: 'ts.net',
    group: 'tag:worker',
    status: 'offline',
    authMethod: 'password',
    sshConnected: false,
    hasRestic: false,
    hasPodman: false,
    hasGlances: false,
  },
];

// ── Register test hosts via the real API ────────────────────────────

export async function registerTestHosts(
  hosts: TestHost[] = SAFE_HOSTS,
): Promise<TestHost[]> {
  const created: TestHost[] = [];
  for (const h of hosts) {
    const res = await fetch(`${API_BASE}/api/nodes`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        id: h.id,
        name: h.name,
        tailscaleHost: h.tailscaleHost,
        ip: h.ip,
        port: h.port ?? 22,
        sshUser: h.sshUser ?? 'core',
        datacenter: h.datacenter ?? 'unknown',
        group: h.group ?? 'tag:test',
        status: h.status ?? 'online',
        authMethod: h.authMethod ?? 'ssh-agent',
        sshConnected: h.sshConnected ?? false,
        hasRestic: h.hasRestic ?? false,
        hasPodman: h.hasPodman ?? false,
        hasGlances: h.hasGlances ?? false,
      }),
    });
    if (!res.ok) {
      throw new Error(`Failed to register test host ${h.id}: ${await res.text()}`);
    }
    created.push(h);
  }
  return created;
}
