import { expect, type Page } from '@playwright/test';
import { testWithContainer } from './helpers';

// Use the container-backed test (re-exported as `test`)
const test = testWithContainer;

/**
 * Interactive terminal e2e.
 *
 * Every assertion targets the real PTY pipeline:
 *   browser -> /ws -> Go websocket hub -> local or SSH PTY -> real shell.
 *
 * Nothing is faked: the test fails if the ghostty-web engine cannot load, if
 * the WebSocket never opens, if no `terminal_start` frame reaches the backend,
 * or if the echoed marker never comes back from the actual shell.
 */

const ENGINE_ERROR_PATTERNS = [
  'ghostty-web not initialized',
  'Failed to initialize libghostty WASM terminal',
];

interface TerminalHarness {
  wsFramesSent: string[];
  engineErrors: string[];
}

function attachHarness(page: Page): TerminalHarness {
  const harness: TerminalHarness = { wsFramesSent: [], engineErrors: [] };
  page.on('console', (msg) => {
    const text = msg.text();
    for (const pattern of ENGINE_ERROR_PATTERNS) {
      if (text.includes(pattern)) harness.engineErrors.push(text);
    }
  });
  page.on('pageerror', (err) => {
    const text = String(err);
    for (const pattern of ENGINE_ERROR_PATTERNS) {
      if (text.includes(pattern)) harness.engineErrors.push(text);
    }
  });
  page.on('websocket', (ws) => {
    ws.on('framesent', (frame) => {
      const payload = frame.payload;
      harness.wsFramesSent.push(typeof payload === 'string' ? payload : String(payload));
    });
  });
  return harness;
}

async function openTerminalForNode(page: Page, nodeId: string) {
  await page.goto('/');
  await page.waitForLoadState('networkidle');
  await page.waitForSelector('#root');

  const row = page.getByTestId(`node-row-${nodeId}`);
  await expect(row).toBeVisible({ timeout: 15000 });
  await row.click();

  const openBtn = page.getByTestId('open-terminal');
  await expect(openBtn).toBeVisible({ timeout: 10000 });
  await openBtn.click();

  // The ghostty engine must mount and produce a real canvas.
  const container = page.getByTestId('ghostty-terminal-container');
  await expect(container).toBeVisible({ timeout: 10000 });
  await expect(container.locator('canvas')).toHaveCount(1, { timeout: 15000 });
  await expect(page.getByTestId('ghostty-engine-error')).toHaveCount(0);

  // The WS status badge must reach a real "connected" state.
  const statusDot = page.getByTestId('terminal-conn-status').first();
  await expect(statusDot).toHaveAttribute('data-status', 'connected', { timeout: 20000 });
}

async function verifyPtyEcho(page: Page, marker: string) {
  const lastOutput = page.getByTestId('ghostty-last-output');
  // The a11y mirror only renders once the first PTY frame arrives; a remote
  // SSH shell takes longer to produce it than localhost. Give it a bounded
  // window, then ensure the marker is not already present (false-positive
  // guard) before typing.
  await lastOutput.waitFor({ state: 'attached', timeout: 30000 }).catch(() => {});
  if ((await lastOutput.count()) > 0) {
    await expect(lastOutput).not.toContainText(marker);
  }

  const container = page.getByTestId('ghostty-terminal-container');
  await container.click();
  await page.keyboard.type(`echo ${marker}`);
  await page.keyboard.press('Enter');

  // The marker only exists after the real shell echoed it back over the PTY.
  await expect(lastOutput).toContainText(marker, { timeout: 30000 });
}

test.describe('Interactive terminal PTY', () => {
  test('localhost terminal spawns a real shell and echoes output', async ({ page }) => {
    const harness = attachHarness(page);
    await openTerminalForNode(page, 'node-localhost');

    // A real terminal_start frame must have reached the backend.
    const startFrame = harness.wsFramesSent.find(
      (f) => f.includes('"terminal_start"') && f.includes('node-localhost'),
    );
    expect(startFrame, 'expected a terminal_start WS frame for node-localhost').toBeTruthy();

    await verifyPtyEcho(page, `atc-e2e-local-${Date.now()}`);

    expect(harness.engineErrors, 'ghostty engine errors must not occur').toEqual([]);
  });

  test('remote SSH terminal (the-freezer) spawns a real shell over tsnet', async ({ page }) => {
    const harness = attachHarness(page);
    await openTerminalForNode(page, 'node-the-freezer');

    const startFrame = harness.wsFramesSent.find(
      (f) => f.includes('"terminal_start"') && f.includes('node-the-freezer'),
    );
    expect(startFrame, 'expected a terminal_start WS frame for node-the-freezer').toBeTruthy();

    await verifyPtyEcho(page, `atc-e2e-freezer-${Date.now()}`);

    expect(harness.engineErrors, 'ghostty engine errors must not occur').toEqual([]);
  });
});
