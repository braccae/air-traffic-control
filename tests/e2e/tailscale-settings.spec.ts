import { test, expect, type APIRequestContext, type Page } from '@playwright/test';

/**
 * ATC's own Tailscale (tsnet) node: settings input + live status surface.
 *
 * Two regressions are guarded here:
 *   1. Secret fields in GlobalSettingsPanel were pinned to `value=""`, so the
 *      auth key could never be typed. Every keystroke must render.
 *   2. The page had no way to show whether ATC's tsnet node actually came up;
 *      the status card now reports the real registration state or the exact
 *      error, and never a fabricated "connected".
 *
 * Nothing here saves settings: saving a new auth key resets tsnet state and
 * re-registers the node, which must not happen as a side effect of a test run.
 */

const API_BASE = process.env.ATC_API_URL || 'http://host.containers.internal:18090';
const TAILSCALE_PAGE = '/#/datacenter/settings/connection/tailscale';

async function openTailscalePage(page: Page) {
  await page.goto(TAILSCALE_PAGE);
  await expect(page.getByTestId('tsnet-self-status')).toBeVisible({ timeout: 30_000 });
  // Wait for the first status read to settle (it starts tsnet on demand).
  await expect
    .poll(async () => page.getByTestId('tsnet-self-badge').innerText(), { timeout: 60_000 })
    .not.toContain('Checking');
}

async function getSelf(request: APIRequestContext) {
  const res = await request.get(`${API_BASE}/api/tailscale/self`);
  expect(res.status()).toBe(200);
  const body = await res.json();
  expect(body.success).toBe(true);
  return body.self as {
    configured: boolean;
    connected: boolean;
    online: boolean;
    mode: string;
    hostname: string;
    dnsName?: string;
    tailscaleIps: string[];
    backendState?: string;
    stateDir: string;
    ephemeral: boolean;
    error?: string;
  };
}

test.describe('Tailscale settings page', () => {
  test('auth key field accepts typed input instead of discarding it', async ({ page }) => {
    await openTailscalePage(page);

    const secret = page.getByLabel('Tailscale Auth Key');
    await expect(secret).toBeVisible();

    // At rest the field is empty: the API only ever returns a mask.
    await expect(secret).toHaveValue('');

    await secret.click();
    await secret.pressSequentially('tskey-auth-E2E-PROBE', { delay: 25 });
    await expect(secret).toHaveValue('tskey-auth-E2E-PROBE');

    // Still a password input, so the key is never displayed in the clear.
    await expect(secret).toHaveAttribute('type', 'password');

    // No save: a save would replace the stored key and re-register tsnet.
  });

  test('non-secret settings are pre-filled from the daemon', async ({ page }) => {
    await openTailscalePage(page);

    const hostname = page.getByLabel('Node Hostname');
    await expect(hostname).not.toHaveValue('');
    await expect(hostname).not.toHaveAttribute('value', '');
  });

  test('stored secret is shown as a mask hint, never as the value', async ({ page }) => {
    await openTailscalePage(page);

    const secret = page.getByLabel('Tailscale Auth Key');
    const value = await secret.inputValue();
    expect(value).toBe('');

    // When a key is stored the daemon returns a mask; the panel surfaces it as
    // a hint without loading it into the field.
    const body = await page.request
      .get(`${API_BASE}/api/settings/datacenter/connection/tailscale`)
      .then(r => r.json());
    const stored = body?.values?.authKey;
    if (typeof stored === 'string' && stored.includes('••••')) {
      await expect(page.getByText(/Stored: /)).toBeVisible();
      expect(await secret.inputValue()).not.toContain('••••');
    }
  });

  test('status card reports the real tsnet identity or the real error', async ({ page, request }) => {
    const self = await getSelf(request);
    await openTailscalePage(page);

    const card = page.getByTestId('tsnet-self-status');
    await expect(card).toContainText('Transport:');

    if (self.configured && self.connected && self.online) {
      await expect(page.getByTestId('tsnet-self-badge')).toHaveText('Online');
      await expect(card).toContainText(self.hostname);
      if (self.dnsName) {
        await expect(card).toContainText(self.dnsName);
      }
      for (const ip of self.tailscaleIps) {
        await expect(card).toContainText(ip);
      }
      if (self.backendState) {
        await expect(card).toContainText(self.backendState);
      }
    } else {
      // Zero-mock: an unconfigured node must say so and show the real reason.
      await expect(page.getByTestId('tsnet-self-badge')).not.toHaveText('Connected');
      expect(self.error, 'unconfigured node must explain why').toBeTruthy();
      await expect(card).toContainText(self.error!);
    }
  });

  test('/api/tailscale/self and /api/tailscale/status agree', async ({ request }) => {
    const self = await getSelf(request);

    const alias = await request.get(`${API_BASE}/api/tailscale/status`);
    expect(alias.status()).toBe(200);
    const aliasBody = await alias.json();
    expect(aliasBody.success).toBe(true);
    expect(aliasBody.self).toEqual(self);
  });

  test('recheck re-reads status without a reload', async ({ page }) => {
    await openTailscalePage(page);

    const button = page.getByRole('button', { name: /Recheck/ });
    await expect(button).toBeEnabled();
    await button.click();
    await expect
      .poll(async () => page.getByTestId('tsnet-self-badge').innerText(), { timeout: 60_000 })
      .not.toContain('Checking');
  });
});
