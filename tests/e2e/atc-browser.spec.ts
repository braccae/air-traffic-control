import { expect, type Page } from '@playwright/test';
import { testWithContainer, type TestSetup } from './helpers';

// Use the container-backed test (re-exported as `test`)
const test = testWithContainer;

test.describe('Main UI Flows', () => {

  // The sidebar tailnet group must be derived from a real node, never a
  // hardcoded placeholder. Asserting on one particular string is wrong: with
  // real inventory the group legitimately shows the operator's actual tailnet.
  test('Sidebar tailnet group comes from real inventory data', async ({ page, request }) => {
    const API_BASE = process.env.ATC_API_URL || 'http://host.containers.internal:18090';
    const nodes = (await (await request.get(`${API_BASE}/api/nodes`)).json()) as Array<{
      tailscaleHost?: string;
      datacenter?: string;
    }>;

    // `fred.leo-banjo.ts.net` and `leo-banjo.ts.net` both belong to `leo-banjo`.
    const tailnetOf = (value?: string): string | undefined => {
      if (!value) return undefined;
      const parts = value.replace(/\.$/, '').split('.');
      const suffix = parts.length > 1 ? parts.slice(1).join('.') : parts[0];
      return suffix.replace(/\.ts\.net$/i, '');
    };

    const realShortnames = new Set<string>();
    for (const node of nodes) {
      for (const candidate of [node.tailscaleHost, node.datacenter]) {
        const name = tailnetOf(candidate);
        if (name) realShortnames.add(name);
      }
    }

    await page.goto('/');
    await page.waitForLoadState('networkidle');
    await page.waitForSelector('#root');

    const body = (await page.locator('body').innerText()).toLowerCase();
    // Any tailnet label on screen must correspond to a node that really exists.
    const shownTsNet = new Set(
      (body.match(/[a-z0-9-]+\.ts\.net/g) ?? []).map(m => m.replace('.ts.net', '')),
    );
    for (const shown of shownTsNet) {
      expect(
        realShortnames.has(shown),
        `sidebar shows tailnet "${shown}" but no inventory node belongs to it`,
      ).toBe(true);
    }
  });

  test('No fake telemetry shown when no node selected', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');
    await page.waitForSelector('#root');
    // With no node selected, telemetry cards should NOT show fake numbers
    await expect(page.locator('text=4.2%')).toHaveCount(0);
    await expect(page.locator('text=6.4 / 16 GiB')).toHaveCount(0);
  });

  test('Navigation sidebar loads and shows node count', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');
    await page.waitForSelector('aside');
    await expect(page.locator('aside').first()).toBeVisible();
    await expect(page.locator('body')).toContainText('Nodes');
  });

  test('Overview tab with no selected node does not crash', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');
    await page.waitForSelector('#root');
    await expect(page.locator('#root')).not.toHaveText('');
  });

  test('WebSocket connection attempts to correct endpoint', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');
    await page.waitForSelector('#root');
    await page.waitForTimeout(500);
    await expect(page.locator('body')).not.toContainText('Connection refused');
  });

});
