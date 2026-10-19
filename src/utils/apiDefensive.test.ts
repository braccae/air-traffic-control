import { describe, it, before, after } from 'node:test';
import assert from 'node:assert';
import http from 'node:http';
import { safeFetchJson, type ApiResponse } from './apiDefensive.ts';

describe('apiDefensive - safeFetchJson', () => {
  let server: http.Server;
  let baseUrl: string;

  before(async () => {
    server = http.createServer((req, res) => {
      const url = new URL(req.url || '/', `http://localhost`);
      const pathname = url.pathname;

      if (pathname === '/api/success-json') {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ success: true, message: 'All systems operational', count: 42 }));
      } else if (pathname === '/api/node-error-json') {
        res.writeHead(400, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ success: false, error: 'Node SSH timeout after 8000ms' }));
      } else if (pathname === '/api/message-only-error') {
        res.writeHead(500, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ message: 'Internal daemon failure' }));
      } else if (pathname === '/api/html-404') {
        res.writeHead(404, { 'Content-Type': 'text/html; charset=utf-8' });
        res.end('<!DOCTYPE html><html lang="en"><head><title>404 Not Found</title></head><body>Cannot GET /api/html-404</body></html>');
      } else if (pathname === '/api/html-200-spa') {
        res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
        res.end('<!doctype html><html lang="en"><head><script type="module" src="/@vite/client"></script></head><body><div id="root"></div></body></html>');
      } else if (pathname === '/api/html-without-content-type') {
        res.writeHead(502);
        res.end('<html><body>502 Bad Gateway</body></html>');
      } else if (pathname === '/api/invalid-json') {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end('{"broken": json');
      } else if (pathname === '/api/echo-post') {
        let body = '';
        req.on('data', chunk => { body += chunk; });
        req.on('end', () => {
          res.writeHead(200, { 'Content-Type': 'application/json' });
          res.end(JSON.stringify({ success: true, received: JSON.parse(body || '{}') }));
        });
      } else if (pathname === '/api/empty-204') {
        res.writeHead(204);
        res.end();
      } else {
        res.writeHead(404, { 'Content-Type': 'text/plain' });
        res.end('Not Found');
      }
    });

    await new Promise<void>((resolve) => {
      server.listen(0, '127.0.0.1', () => {
        const addr = server.address();
        if (typeof addr === 'object' && addr) {
          baseUrl = `http://127.0.0.1:${addr.port}`;
        }
        resolve();
      });
    });
  });

  after(async () => {
    await new Promise<void>((resolve, reject) => {
      server.close((err) => (err ? reject(err) : resolve()));
    });
  });

  it('safely parses valid 200 JSON responses', async () => {
    const res = await safeFetchJson<{ count: number; message: string }>(`${baseUrl}/api/success-json`);
    assert.strictEqual(res.status, 200);
    assert.strictEqual(res.success, true);
    assert.strictEqual(res.error, undefined);
    assert.strictEqual(res.data?.count, 42);
    assert.strictEqual(res.data?.message, 'All systems operational');
  });

  it('surfaces .error from 400 JSON responses without throwing', async () => {
    const res = await safeFetchJson(`${baseUrl}/api/node-error-json`);
    assert.strictEqual(res.status, 400);
    assert.strictEqual(res.success, false);
    assert.strictEqual(res.error, 'Node SSH timeout after 8000ms');
    assert.strictEqual(res.data?.success, false);
  });

  it('surfaces .message when .error is missing in 500 JSON', async () => {
    const res = await safeFetchJson(`${baseUrl}/api/message-only-error`);
    assert.strictEqual(res.status, 500);
    assert.strictEqual(res.success, false);
    assert.strictEqual(res.error, 'Internal daemon failure');
  });

  it('detects 404 HTML response and returns descriptive error instead of JSON SyntaxError', async () => {
    const res = await safeFetchJson(`${baseUrl}/api/html-404`);
    assert.strictEqual(res.status, 404);
    assert.strictEqual(res.success, false);
    assert.ok(res.error?.includes('Server returned HTML'));
    assert.ok(res.error?.includes('404'));
  });

  it('detects Vite SPA index.html returned with 200 OK and returns error instead of JSON crash', async () => {
    const res = await safeFetchJson(`${baseUrl}/api/html-200-spa`);
    assert.strictEqual(res.status, 200);
    assert.strictEqual(res.success, false);
    assert.ok(res.error?.includes('Server returned HTML'));
  });

  it('detects HTML response even without Content-Type header', async () => {
    const res = await safeFetchJson(`${baseUrl}/api/html-without-content-type`);
    assert.strictEqual(res.status, 502);
    assert.strictEqual(res.success, false);
    assert.ok(res.error?.includes('Server returned HTML'));
  });

  it('handles invalid JSON payload gracefully with an informative error', async () => {
    const res = await safeFetchJson(`${baseUrl}/api/invalid-json`);
    assert.strictEqual(res.status, 200);
    assert.strictEqual(res.success, false);
    assert.ok(res.error?.includes('Invalid JSON response'));
  });

  it('forwards request options like POST headers and body', async () => {
    const res = await safeFetchJson<{ received: { action: string } }>(`${baseUrl}/api/echo-post`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action: 'restart' }),
    });
    assert.strictEqual(res.status, 200);
    assert.strictEqual(res.success, true);
    assert.strictEqual(res.data?.received?.action, 'restart');
  });

  it('handles 204 No Content gracefully', async () => {
    const res = await safeFetchJson(`${baseUrl}/api/empty-204`);
    assert.strictEqual(res.status, 204);
    assert.strictEqual(res.success, true);
    assert.strictEqual(res.error, undefined);
  });

  it('handles network failure cleanly without throwing unhandled rejection', async () => {
    // Port 1 is reserved and will immediately refuse connection
    const res = await safeFetchJson('http://127.0.0.1:1/nonexistent');
    assert.strictEqual(res.status, 0);
    assert.strictEqual(res.success, false);
    assert.ok(typeof res.error === 'string' && res.error.length > 0);
  });
});

describe('Express API 404 Guard Simulation', () => {
  let appServer: http.Server;
  let appUrl: string;

  before(async () => {
    const { default: express } = await import('express');
    const app = express();
    app.use(express.json());

    // Sample valid route
    app.get('/api/test-route', (_req, res) => {
      res.json({ success: true, item: 'valid' });
    });

    // Catch-all API 404 guard
    app.all(['/api', '/api/*'], (req, res) => {
      res.status(404).json({
        success: false,
        error: `API route not found: ${req.method} ${req.path}`,
      });
    });

    // Fallthrough SPA handler simulating Vite / index.html
    app.get('*', (_req, res) => {
      res.status(200).send('<!doctype html><html><head><title>SPA</title></head><body><div id="root"></div></body></html>');
    });

    await new Promise<void>((resolve) => {
      appServer = app.listen(0, '127.0.0.1', () => {
        const addr = appServer.address();
        if (typeof addr === 'object' && addr) {
          appUrl = `http://127.0.0.1:${addr.port}`;
        }
        resolve();
      });
    });
  });

  after(async () => {
    await new Promise<void>((resolve, reject) => {
      appServer.close((err) => (err ? reject(err) : resolve()));
    });
  });

  it('valid API routes respond normally with JSON', async () => {
    const res = await safeFetchJson<{ item: string }>(`${appUrl}/api/test-route`);
    assert.strictEqual(res.status, 200);
    assert.strictEqual(res.success, true);
    assert.strictEqual(res.data?.item, 'valid');
  });

  it('unhandled /api/* route returns 404 JSON error instead of index.html', async () => {
    const rawRes = await fetch(`${appUrl}/api/nodes/unknown-node/packages/check`);
    assert.strictEqual(rawRes.status, 404);
    const contentType = rawRes.headers.get('content-type') || '';
    assert.ok(contentType.includes('application/json'), `Expected JSON content-type but got ${contentType}`);
    const data = await rawRes.json();
    assert.strictEqual(data.success, false);
    assert.strictEqual(data.error, 'API route not found: GET /api/nodes/unknown-node/packages/check');

    // Also verify safeFetchJson parses it cleanly
    const safeRes = await safeFetchJson(`${appUrl}/api/nodes/unknown-node/packages/check`);
    assert.strictEqual(safeRes.status, 404);
    assert.strictEqual(safeRes.success, false);
    assert.strictEqual(safeRes.error, 'API route not found: GET /api/nodes/unknown-node/packages/check');
  });

  it('non-API route falls through to SPA index.html, handled safely by safeFetchJson', async () => {
    const safeRes = await safeFetchJson(`${appUrl}/dashboard/nodes`);
    assert.strictEqual(safeRes.status, 200);
    assert.strictEqual(safeRes.success, false);
    assert.ok(safeRes.error?.includes('Server returned HTML'));
  });
});

