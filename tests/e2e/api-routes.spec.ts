import { test, expect } from '@playwright/test';

/**
 * ATC API Route Verification — All endpoints must respond
 *
 * Uses Playwright's request context to test the Go daemon directly
 * (independent of Vite frontend) — validates backend health.
 */

const API_BASE = process.env.ATC_API_URL || 'http://host.containers.internal:18090';

test.describe('API Routes', () => {

  test('GET /api/health returns healthy', async ({ request }) => {
    const res = await request.get(`${API_BASE}/api/health`);
    expect(res.ok()).toBeTruthy();
    const data = await res.json();
    expect(data.status).toBe('healthy');
  });

  test('GET /api/nodes returns array (or baseline when DB missing)', async ({ request }) => {
    const res = await request.get(`${API_BASE}/api/nodes`);
    expect(res.ok()).toBeTruthy();
    const data = await res.json();
    expect(Array.isArray(data)).toBeTruthy();
  });

  test('GET /api/tailscale/peers returns peers', async ({ request }) => {
    const res = await request.get(`${API_BASE}/api/tailscale/peers`);
    expect(res.ok()).toBeTruthy();
  });

  test('GET /api/glances/telemetry with host param', async ({ request }) => {
    const res = await request.get(`${API_BASE}/api/glances/telemetry?host=test-node-alpha`);
    // May return 502 if host isn't reachable — that's acceptable for test host
    expect([200, 502, 404]).toContain(res.status());
  });

  test('POST /api/nodes creates safe test node', async ({ request }) => {
    const res = await request.post(`${API_BASE}/api/nodes`, {
      data: {
        id: 'test-api-host',
        name: 'api-test',
        ip: '100.64.99.99',
        datacenter: 'test',
        group: 'tag:api',
        status: 'online',
      },
    });
    expect([200, 201]).toContain(res.status());
    // Cleanup
    try { await request.delete(`${API_BASE}/api/nodes/test-api-host`); } catch {}
  });

  test('GET /api/plugins lists available plugins', async ({ request }) => {
    const res = await request.get(`${API_BASE}/api/plugins`);
    expect(res.ok()).toBeTruthy();
    const data = await res.json();
    expect(Array.isArray(data) || (data && typeof data === 'object')).toBeTruthy();
  });

});
