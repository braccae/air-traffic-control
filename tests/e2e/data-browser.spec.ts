import { test, expect, type APIRequestContext } from '@playwright/test';

/**
 * Data browser native drivers must not depend on vendor CLI tools being
 * installed on the managed host (psql / mysql / redis-cli / mongosh). The
 * unified Go driver is tried first and connects through the node's SSH
 * tunnel, so containerised databases on non-default host ports work too.
 *
 * dev-pc is the reference node: valkey, mongodb, mariadb and postgres run as
 * containers with published ports (6379, 27017, 13306, 15432).
 */

const API_BASE = process.env.ATC_API_URL || 'http://host.containers.internal:18090';
const NODE_ID = 'node-dev-pc';

interface QueryResult {
  engine?: string;
  driver?: string;
  fallback?: string;
  error?: string;
  rows?: unknown[][];
  raw?: string;
}

async function nativeQuery(
  request: APIRequestContext,
  body: Record<string, unknown>,
  nodeId = NODE_ID,
): Promise<QueryResult> {
  const res = await request.post(
    `${API_BASE}/api/nodes/${encodeURIComponent(nodeId)}/plugins/database/unified/query`,
    { data: body },
  );
  expect(res.status()).toBe(200);
  return (await res.json()) as QueryResult;
}

test.describe('Database native drivers (dev-pc)', () => {
  test.setTimeout(60_000);

  test('redis/valkey answers PING without redis-cli', async ({ request }) => {
    const result = await nativeQuery(request, { engine: 'redis', query: 'PING' });
    expect(result.fallback, `fallback=${result.fallback}`).toBeUndefined();
    expect(result.error, result.error).toBeUndefined();
    expect(String(result.raw || '')).toContain('PONG');
  });

  test('mongodb lists collections without mongosh', async ({ request }) => {
    const result = await nativeQuery(request, { engine: 'mongodb', query: 'db.getCollectionNames()' });
    expect(result.fallback, `fallback=${result.fallback}`).toBeUndefined();
    expect(String(result.error || '')).not.toContain('deadline not supported');
    expect(String(result.error || '')).not.toContain('auth error');
    // The driver must have answered with a JSON collection array.
    expect(String(result.raw || '')).toContain('[');
  });

  test('mariadb reaches a containerised instance on a non-default host port', async ({ request }) => {
    const result = await nativeQuery(request, {
      engine: 'mariadb',
      user: process.env.ATC_MARIADB_USER || 'root',
      password: process.env.ATC_MARIADB_PASSWORD || 'secret',
      query: 'SHOW DATABASES',
    });
    expect(result.fallback, `fallback=${result.fallback}`).toBeUndefined();
    expect(String(result.error || '')).not.toContain('Connection refused');
    const flat = (result.rows || []).flat().map(String);
    expect(flat).toContain('testdb');
  });

  test('redis reaches a container-only instance (unpublished port) via its bridge IP', async ({ request }) => {
    // likes-it-cold runs nextcloud-aio-redis with no published host port; the
    // driver must fall back to the container's bridge IP and pick up the
    // container's own REDIS_HOST_PASSWORD.
    const result = await nativeQuery(
      request,
      { engine: 'redis', query: 'DBSIZE' },
      'node-likes-it-cold',
    );
    expect(result.fallback, `fallback=${result.fallback}`).toBeUndefined();
    expect(result.error, result.error).toBeUndefined();
    expect(Number(String(result.raw || '').trim())).toBeGreaterThanOrEqual(0);
  });

  test('mariadb returns rows (data view, not just the list)', async ({ request }) => {
    const result = await nativeQuery(request, {
      engine: 'mariadb',
      user: process.env.ATC_MARIADB_USER || 'root',
      password: process.env.ATC_MARIADB_PASSWORD || 'secret',
      query: 'SELECT 1 AS ok, VERSION() AS version',
    });
    expect(result.error, result.error).toBeUndefined();
    expect((result.rows || []).length).toBeGreaterThan(0);
    expect(String((result.rows || [])[0]?.[0])).toBe('1');
  });

  test('mongodb returns documents (data view, not just collections)', async ({ request }) => {
    const result = await nativeQuery(request, {
      engine: 'mongodb',
      database: 'admin',
      query: JSON.stringify({ find: 'system.version', limit: 5, filter: {} }),
    });
    expect(result.error, result.error).toBeUndefined();
    const raw = String(result.raw || '');
    expect(raw.startsWith('[')).toBe(true);
    expect(raw).toContain('featureCompatibilityVersion');
  });
});

test.describe('Database view tabs', () => {
  test.describe.configure({ timeout: 120_000 });
  test('SQL view renders top-level tabs instead of stacked panels', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');
    await page.waitForSelector('#root');
    await page.getByTestId('node-row-node-dev-pc').click();
    await page.waitForTimeout(2000);
    await page.locator('aside button', { hasText: 'Postgres' }).first().click();
    await page.waitForTimeout(4000);

    const tabs = page.getByTestId('sql-view-tabs');
    await expect(tabs).toBeVisible({ timeout: 15000 });
    await expect(tabs).toContainText('Browse Data');
    await expect(tabs).toContainText('SQL Query');
    await expect(tabs).toContainText('Databases');
    await expect(tabs).toContainText('Users');
  });

  test('NoSQL view renders top-level tabs', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');
    await page.waitForSelector('#root');
    await page.getByTestId('node-row-node-dev-pc').click();
    await page.waitForTimeout(2000);
    await page.locator('aside button', { hasText: 'Redis' }).first().click();
    await page.waitForTimeout(5000);

    const tabs = page.getByTestId('nosql-view-tabs');
    await expect(tabs).toBeVisible({ timeout: 20000 });
    await expect(tabs).toContainText('Browse Data');
    await expect(tabs).toContainText('Raw Query');
    await expect(tabs).toContainText('Databases');
    await expect(tabs).toContainText('Users');
  });
});

test.describe('SQL data browser UI', () => {
  test.describe.configure({ timeout: 120_000 });
  test('Load Tables does not fall back to a missing psql binary', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');
    await page.waitForSelector('#root');

    // Select the-freezer, which has a Postgres data browser tab.
    await page.getByTestId('node-row-node-the-freezer').click();
    await page.waitForTimeout(2000);

    const tab = page.getByText('Postgres').first();
    await expect(tab).toBeVisible({ timeout: 10000 });
    await tab.click();
    await page.waitForTimeout(3000);

    const loadBtn = page.getByRole('button', { name: /Load Tables/i });
    await expect(loadBtn).toBeVisible({ timeout: 10000 });
    // Wait for the native-driver request itself instead of a fixed timeout, so
    // the assertion is deterministic under parallel suite load.
    const [driverResponse] = await Promise.all([
      page.waitForResponse(
        (r) =>
          r.url().includes('/plugins/database/unified/query') &&
          r.request().method() === 'POST',
        { timeout: 60_000 },
      ),
      loadBtn.click(),
    ]);
    expect(driverResponse.status()).toBe(200);
    // Let the view render the result or the real driver error.
    await expect
      .poll(
        async () => {
          const text = await page.locator('body').innerText();
          return text.includes('TABLES (') || text.includes('Browser Error');
        },
        { timeout: 30_000 },
      )
      .toBe(true);

    const body = await page.locator('body').innerText();
    // The native driver must be attempted; the old CLI fallback message must
    // never appear, even when the driver cannot connect.
    expect(body).not.toContain('psql: command not found');
    expect(body).not.toContain('psql not found');
    expect(body).not.toContain('command not found');
  });
});
