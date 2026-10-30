import { defineConfig } from '@playwright/test';

/**
 * The Playwright container ships its own Chromium, Firefox and WebKit builds, so
 * tests launch a bundled browser directly. There is no browserless/CDP endpoint
 * to connect to.
 *
 * Vite (:5173) and the Go daemon (:18090) are expected to be running on the
 * host; from inside the container they are reachable via
 * host.containers.internal. `just dev` starts both.
 */
export default defineConfig({
  testDir: './tests/e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: 2,
  workers: 3,
  reporter: 'list',
  use: {
    baseURL: process.env.ATC_WEB_URL || 'http://host.containers.internal:5173',
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },
  webServer: {
    command: 'just dev-split',
    url: process.env.ATC_WEB_URL || 'http://host.containers.internal:5173',
    reuseExistingServer: !process.env.CI,
    timeout: 120000,
  },
});
