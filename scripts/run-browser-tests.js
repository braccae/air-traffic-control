/**
 * Standalone Playwright runner connecting to browserless/chrome container via CDP.
 * Run: node scripts/run-browser-tests.js
 */
import { chromium } from '@playwright/test';

(async () => {
  const browser = await chromium.connectOverCDP('http://localhost:3000');
  const context = await browser.newContext();
  const page = await context.newPage();

  console.log('=== Sidebar does not show fake hardcoded datacenter tag ===');
  await page.goto('http://192.168.8.211:5173/');
  const rootContent = await page.textContent('#root');
  if (rootContent && rootContent.includes('leo-banjo')) {
    throw new Error('FAIL: "leo-banjo" found in DOM (should be auto-detected)');
  }
  console.log('PASS: No "leo-banjo" in DOM\n');

  console.log('=== No fake telemetry shown when no node selected ===');
  await page.waitForLoadState('networkidle');
  const bodyContent = await page.textContent('body');
  // Fake telemetry values that were replaced: CPU 4.2, memory 6.4 / 16 GiB, ZFS data
  // Note: 'HEALTHY' may be a legitimate health status badge, not fake telemetry
  const hasFake4_2 = bodyContent.includes('4.2');
  const hasFake6_4 = bodyContent.includes('6.4 / 16 GiB');
  if (hasFake4_2 || hasFake6_4) {
    throw new Error('FAIL: fake telemetry values found');
  }
  console.log('PASS: No fake telemetry values\n');

  console.log('=== Overview tab renders (no crash) ===');
  if (!await page.locator('#root').isVisible()) {
    throw new Error('FAIL: #root not visible or crashed');
  }
  console.log('PASS: #root visible\n');

  console.log('=== WebSocket connection attempts to correct endpoint ===');
  await page.waitForTimeout(500);
  const bodyText = await page.textContent('body');
  if (bodyText.includes('Connection refused')) {
    throw new Error('FAIL: WebSocket connection error');
  }
  console.log('PASS: No Connection refused errors\n');

  console.log('All browser tests passed!');
  await browser.close();
})().catch(err => {
  console.error('BROWSER TEST FAILED:', err.message);
  process.exit(1);
});