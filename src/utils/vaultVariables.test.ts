import { describe, it, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import http from 'node:http';
import {
  decryptVaultString,
  encryptVaultString,
  materializeVaultVarsFile,
  registerVaultRoutes,
} from './vaultManager.ts';
import { createHostCacheStore } from './hostCacheDb.ts';

function createMockApp() {
  const routes: Array<{ method: string; path: string; handler: Function }> = [];
  const app = {
    get: (p: string, h: Function) => routes.push({ method: 'GET', path: p, handler: h }),
    post: (p: string, h: Function) => routes.push({ method: 'POST', path: p, handler: h }),
    put: (p: string, h: Function) => routes.push({ method: 'PUT', path: p, handler: h }),
    delete: (p: string, h: Function) => routes.push({ method: 'DELETE', path: p, handler: h }),
  };

  const server = http.createServer((req, res) => {
    const parsedUrl = new URL(req.url || '/', 'http://127.0.0.1');
    const pathname = parsedUrl.pathname;
    const method = req.method || 'GET';

    let body = '';
    req.on('data', chunk => { body += chunk; });
    req.on('end', async () => {
      let parsedBody = {};
      if (body) {
        try { parsedBody = JSON.parse(body); } catch {}
      }

      for (const r of routes) {
        if (r.method !== method) continue;
        const rParts = r.path.split('/');
        const uParts = pathname.split('/');
        if (rParts.length !== uParts.length) continue;

        const params: Record<string, string> = {};
        let match = true;
        for (let i = 0; i < rParts.length; i++) {
          if (rParts[i].startsWith(':')) {
            params[rParts[i].slice(1)] = decodeURIComponent(uParts[i]);
          } else if (rParts[i] !== uParts[i]) {
            match = false;
            break;
          }
        }

        if (match) {
          const reqMock = { params, body: parsedBody, query: Object.fromEntries(parsedUrl.searchParams) };
          const resMock = {
            status(code: number) {
              res.statusCode = code;
              return resMock;
            },
            json(data: any) {
              res.setHeader('Content-Type', 'application/json');
              res.end(JSON.stringify(data));
              return resMock;
            },
          };
          await r.handler(reqMock, resMock);
          return;
        }
      }

      res.statusCode = 404;
      res.end(JSON.stringify({ error: 'Not found' }));
    });
  });

  return { app, server };
}

function listen(server: http.Server): Promise<{ baseUrl: string }> {
  return new Promise(resolve => {
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address();
      const port = typeof addr === 'object' && addr ? addr.port : 0;
      resolve({ baseUrl: `http://127.0.0.1:${port}` });
    });
  });
}

describe('Vault variable editor API (SQLite-backed vault store)', () => {
  let server: http.Server;
  let baseUrl: string;
  let store: ReturnType<typeof createHostCacheStore>;

  const postJson = (pathname: string, body: any, method = 'POST') =>
    fetch(`${baseUrl}${pathname}`, {
      method,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });

  beforeEach(async () => {
    store = createHostCacheStore(':memory:');
    const mock = createMockApp();
    server = mock.server;
    registerVaultRoutes(mock.app, { variableStore: store });
    const listening = await listen(server);
    baseUrl = listening.baseUrl;
  });

  afterEach(async () => {
    await new Promise<void>(resolve => server.close(() => resolve()));
    store.close();
  });

  it('creates a variable, stores ciphertext only, and never returns the value', async () => {
    const secret = 'super-secret-token-value';
    const res = await postJson('/api/vault/variables', {
      name: 'api_key',
      value: secret,
      password: 'editor-pass',
    });
    assert.equal(res.status, 201);

    const rawBody = await res.text();
    assert.ok(!rawBody.includes(secret), 'response must not echo the plaintext');
    assert.ok(!rawBody.includes('$ANSIBLE_VAULT'), 'response must not expose ciphertext');

    const data = JSON.parse(rawBody);
    assert.equal(data.success, true);
    assert.equal(data.variable.name, 'api_key');
    assert.equal(data.variable.maskedValue, '•••••••• (vault: api_key)');

    const block = store.getVaultVariableBlock('api_key');
    assert.ok(block && block.includes('$ANSIBLE_VAULT;1.1;AES256'));
    assert.equal(await decryptVaultString(block as string, { password: 'editor-pass' }), secret);
  });

  it('lists variables as name + mask only', async () => {
    const secret = 'listed-secret-value';
    await postJson('/api/vault/variables', { name: 'db_password', value: secret, password: 'p' });

    const res = await fetch(`${baseUrl}/api/vault/variables`);
    const rawBody = await res.text();
    assert.ok(!rawBody.includes(secret));
    assert.ok(!rawBody.includes('$ANSIBLE_VAULT'));

    const data = JSON.parse(rawBody);
    assert.equal(data.success, true);
    assert.equal(data.total, 1);
    assert.deepEqual(Object.keys(data.variables[0]).sort(), [
      'createdAt',
      'maskedValue',
      'name',
      'updatedAt',
    ]);
    assert.equal(data.variables[0].maskedValue, '•••••••• (vault: db_password)');

    // Store-level listing carries no secret material either.
    const fromStore = store.listVaultVariables();
    assert.equal(fromStore.length, 1);
    assert.deepEqual(Object.keys(fromStore[0]).sort(), ['createdAt', 'name', 'updatedAt']);
  });

  it('rejects invalid names, empty values and duplicates', async () => {
    const invalidName = await postJson('/api/vault/variables', {
      name: 'bad name; rm -rf /',
      value: 'x',
      password: 'p',
    });
    assert.equal(invalidName.status, 400);

    const emptyValue = await postJson('/api/vault/variables', {
      name: 'ok_name',
      value: '',
      password: 'p',
    });
    assert.equal(emptyValue.status, 400);

    const created = await postJson('/api/vault/variables', {
      name: 'ok_name',
      value: 'v1',
      password: 'p',
    });
    assert.equal(created.status, 201);

    const duplicate = await postJson('/api/vault/variables', {
      name: 'ok_name',
      value: 'v2',
      password: 'p',
    });
    assert.equal(duplicate.status, 409);
  });

  it('updates a value by re-encrypting, and renames without decrypting', async () => {
    await postJson('/api/vault/variables', { name: 'app_secret', value: 'old-value', password: 'p' });

    const updated = await postJson(
      '/api/vault/variables/app_secret',
      { value: 'new-value', password: 'p' },
      'PUT'
    );
    assert.equal(updated.status, 200);
    const updatedBody = await updated.text();
    assert.ok(!updatedBody.includes('new-value'));

    const newBlock = store.getVaultVariableBlock('app_secret');
    assert.equal(await decryptVaultString(newBlock as string, { password: 'p' }), 'new-value');

    const renamed = await postJson(
      '/api/vault/variables/app_secret',
      { newName: 'renamed_secret' },
      'PUT'
    );
    assert.equal(renamed.status, 200);
    assert.equal(store.getVaultVariableBlock('app_secret'), null);
    const renamedBlock = store.getVaultVariableBlock('renamed_secret');
    assert.ok(renamedBlock && renamedBlock.startsWith('renamed_secret: !vault |'));
    assert.equal(await decryptVaultString(renamedBlock as string, { password: 'p' }), 'new-value');

    const missing = await postJson('/api/vault/variables/nope', { value: 'x', password: 'p' }, 'PUT');
    assert.equal(missing.status, 404);
  });

  it('deletes a variable and reports missing ones', async () => {
    await postJson('/api/vault/variables', { name: 'temp_secret', value: 'v', password: 'p' });

    const deleted = await fetch(`${baseUrl}/api/vault/variables/temp_secret`, { method: 'DELETE' });
    assert.equal(deleted.status, 200);
    assert.equal(store.getVaultVariableBlock('temp_secret'), null);

    const again = await fetch(`${baseUrl}/api/vault/variables/temp_secret`, { method: 'DELETE' });
    assert.equal(again.status, 404);
  });

  it('materializes an owner-only ciphertext vars file for playbooks and cleans it up', async () => {
    const secret = 'playbook-secret-value';
    await postJson('/api/vault/variables', {
      name: 'playbook_secret',
      value: secret,
      password: 'p',
    });

    const materialized = materializeVaultVarsFile(store);
    assert.ok(materialized, 'expected a materialized vars file');

    const mode = fs.statSync(materialized!.filePath).mode & 0o777;
    assert.equal(mode, 0o600, 'vault vars file must be owner-only');

    const content = fs.readFileSync(materialized!.filePath, 'utf8');
    assert.ok(content.includes('playbook_secret: !vault |'));
    assert.ok(content.includes('$ANSIBLE_VAULT;1.1;AES256'));
    assert.ok(!content.includes(secret), 'vars file must contain ciphertext only');

    materialized!.cleanup();
    assert.equal(fs.existsSync(materialized!.filePath), false);
  });

  it('returns null when the store has no variables', () => {
    assert.equal(materializeVaultVarsFile(store), null);
    assert.equal(materializeVaultVarsFile(undefined), null);
  });
});

describe('Vault variables persist in the SQLite vault store', () => {
  it('survives a store restart with ciphertext intact', async () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'vault-vars-db-'));
    const dbPath = path.join(dir, 'hosts-cache.sqlite');
    const password = 'restart-pass';
    const secret = 'persisted-secret-value';

    try {
      const first = createHostCacheStore(dbPath);
      const block = await encryptVaultString(secret, 'restart_secret', { password });
      first.upsertVaultVariable('restart_secret', block);
      first.close();

      const second = createHostCacheStore(dbPath);
      const listed = second.listVaultVariables();
      assert.equal(listed.length, 1);
      assert.equal(listed[0].name, 'restart_secret');

      const reloadedBlock = second.getVaultVariableBlock('restart_secret');
      assert.ok(reloadedBlock && reloadedBlock.includes('$ANSIBLE_VAULT;1.1;AES256'));
      assert.equal(await decryptVaultString(reloadedBlock as string, { password }), secret);
      second.close();
    } finally {
      fs.rmSync(dir, { recursive: true, force: true });
    }
  });
});
