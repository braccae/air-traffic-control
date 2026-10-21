import { describe, it, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import http from 'node:http';
import {
  resolveVaultPasswordSource,
  encryptVaultString,
  decryptVaultString,
  registerVaultRoutes,
  getVaultExecutionEnv,
  type VaultPasswordSource,
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

function listenServer(server: http.Server): Promise<{ baseUrl: string }> {
  return new Promise(resolve => {
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address();
      const port = typeof addr === 'object' && addr ? addr.port : 0;
      resolve({ baseUrl: `http://127.0.0.1:${port}` });
    });
  });
}

describe('Tiered Ansible Vault Credential Engine', () => {
  let tmpDir: string;
  let tmpPassFile: string;
  let originalEnv: NodeJS.ProcessEnv;

  beforeEach(() => {
    tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'vault-manager-test-'));
    tmpPassFile = path.join(tmpDir, 'test-vault-pass.txt');
    originalEnv = { ...process.env };
    delete process.env.ANSIBLE_VAULT_PASSWORD;
    delete process.env.ANSIBLE_VAULT_PASSWORD_FILE;
    delete process.env.ANSIBLE_VAULT_IDENTITY_LIST;
  });

  afterEach(() => {
    process.env = { ...originalEnv };
    try {
      fs.rmSync(tmpDir, { recursive: true, force: true });
    } catch {
      // ignore cleanup error
    }
  });

  describe('getVaultExecutionEnv', () => {
    it('strips ANSIBLE_VAULT_PASSWORD_FILE, ANSIBLE_VAULT_IDENTITY_LIST, DEFAULT_VAULT_PASSWORD_FILE, and ANSIBLE_CONFIG', () => {
      const mockEnv: NodeJS.ProcessEnv = {
        PATH: '/usr/bin',
        FOO: 'bar',
        ANSIBLE_VAULT_PASSWORD_FILE: '/tmp/passfile',
        ANSIBLE_VAULT_IDENTITY_LIST: 'id@/tmp/idfile',
        DEFAULT_VAULT_PASSWORD_FILE: '/tmp/defaultpass',
        ANSIBLE_CONFIG: '/path/to/ansible.cfg',
      };
      const cleaned = getVaultExecutionEnv(mockEnv);
      assert.equal(cleaned.FOO, 'bar');
      assert.equal(cleaned.ANSIBLE_VAULT_PASSWORD_FILE, undefined);
      assert.equal(cleaned.ANSIBLE_VAULT_IDENTITY_LIST, undefined);
      assert.equal(cleaned.DEFAULT_VAULT_PASSWORD_FILE, undefined);
      assert.equal(cleaned.ANSIBLE_CONFIG, undefined);
      assert.ok('PATH' in cleaned);
      assert.ok('VIRTUAL_ENV' in cleaned);
    });
  });

  describe('resolveVaultPasswordSource', () => {
    it('Tier 1: resolves password from vault_password_file option when file exists', () => {
      fs.writeFileSync(tmpPassFile, 'file-secret-pass\n', 'utf8');
      const source = resolveVaultPasswordSource({ vaultPasswordFile: tmpPassFile });
      assert.equal(source.source, 'file');
      assert.equal(source.hasPassword, true);
      assert.equal(source.password, 'file-secret-pass');
      assert.equal(source.filePath, tmpPassFile);
    });

    it('Tier 1: resolves password from ansible.cfg vault_password_file when file exists', () => {
      const customCfg = path.join(tmpDir, 'ansible.cfg');
      const passInCfg = path.join(tmpDir, 'cfg-pass.txt');
      fs.writeFileSync(passInCfg, 'from-cfg-vault-pass\n', 'utf8');
      fs.writeFileSync(customCfg, `[defaults]\nvault_password_file = ${passInCfg}\n`, 'utf8');

      const source = resolveVaultPasswordSource({
        ansibleCfgPath: customCfg,
      } as any);

      assert.equal(source.source, 'file');
      assert.equal(source.hasPassword, true);
      assert.equal(source.password, 'from-cfg-vault-pass');
      assert.equal(source.filePath, passInCfg);
    });

    it('Tier 1: resolves relative vault_password_file against ansible/ directory when relative to cfg', () => {
      const ansibleDir = path.resolve(process.cwd(), 'ansible');
      const testRelFile = path.join(ansibleDir, '.tmp-rel-vault-pass.txt');
      fs.writeFileSync(testRelFile, 'rel-ansible-pass\n', 'utf8');

      try {
        const source = resolveVaultPasswordSource({
          vaultPasswordFile: '.tmp-rel-vault-pass.txt',
        });
        assert.equal(source.source, 'file');
        assert.equal(source.hasPassword, true);
        assert.equal(source.password, 'rel-ansible-pass');
      } finally {
        if (fs.existsSync(testRelFile)) fs.unlinkSync(testRelFile);
      }
    });

    it('Tier 1 fallback: falls through to Tier 2 when vaultPasswordFile does not exist on disk', () => {
      const nonExistent = path.join(tmpDir, 'missing-file.txt');
      process.env.ANSIBLE_VAULT_PASSWORD = 'env-fallback-pass';

      const source = resolveVaultPasswordSource({ vaultPasswordFile: nonExistent });
      assert.equal(source.source, 'env');
      assert.equal(source.hasPassword, true);
      assert.equal(source.password, 'env-fallback-pass');
    });

    it('Tier 2: resolves password from ANSIBLE_VAULT_PASSWORD env var when file not set', () => {
      process.env.ANSIBLE_VAULT_PASSWORD = 'env-secret-pass';
      const source = resolveVaultPasswordSource({});
      assert.equal(source.source, 'env');
      assert.equal(source.hasPassword, true);
      assert.equal(source.password, 'env-secret-pass');
    });

    it('Tier 2: resolves password from options.env ANSIBLE_VAULT_PASSWORD', () => {
      const source = resolveVaultPasswordSource({
        env: { ANSIBLE_VAULT_PASSWORD: 'custom-env-pass' },
      });
      assert.equal(source.source, 'env');
      assert.equal(source.hasPassword, true);
      assert.equal(source.password, 'custom-env-pass');
    });

    it('Tier 2: resolves password from ANSIBLE_VAULT_PASSWORD_FILE when file exists', () => {
      fs.writeFileSync(tmpPassFile, 'env-file-pass\n', 'utf8');
      process.env.ANSIBLE_VAULT_PASSWORD_FILE = tmpPassFile;

      const source = resolveVaultPasswordSource({});
      assert.equal(source.source, 'file');
      assert.equal(source.hasPassword, true);
      assert.equal(source.password, 'env-file-pass');
      assert.equal(source.filePath, tmpPassFile);
    });

    it('Tier 3: falls back to ask_password when neither file nor env var exists', () => {
      const source = resolveVaultPasswordSource({});
      assert.equal(source.source, 'ask_password');
      assert.equal(source.hasPassword, false);
      assert.equal(source.password, undefined);
    });
  });

  describe('encryptVaultString & decryptVaultString', () => {
    it('throws PASSWORD_REQUIRED when encrypting without a password source', async () => {
      await assert.rejects(
        async () => {
          await encryptVaultString('my-secret', 'ssh_pass');
        },
        {
          name: 'Error',
          message: 'PASSWORD_REQUIRED: Vault password source required for encryption',
        }
      );
    });

    it('throws PASSWORD_REQUIRED when decrypting without a password source', async () => {
      await assert.rejects(
        async () => {
          await decryptVaultString('$ANSIBLE_VAULT;1.1;AES256\n1234');
        },
        {
          name: 'Error',
          message: 'PASSWORD_REQUIRED: Vault password source required for decryption',
        }
      );
    });

    it('encrypts plaintext into valid $ANSIBLE_VAULT;1.1;AES256 block and decrypts it with explicit password', async () => {
      const secret = 'super-secret-ssh-key-or-pass';
      const encrypted = await encryptVaultString(secret, 'ssh_password', { password: 'master-key-123' });

      assert.ok(encrypted.includes('ssh_password: !vault |'), 'Contains variable name and !vault |');
      assert.ok(encrypted.includes('$ANSIBLE_VAULT;1.1;AES256'), 'Contains vault header');

      const decrypted = await decryptVaultString(encrypted, { password: 'master-key-123' });
      assert.equal(decrypted, secret);
    });

    it('defaults variable name to secret when varName is omitted', async () => {
      const encrypted = await encryptVaultString('my-token', undefined, { password: 'token-pass' });
      assert.ok(encrypted.includes('secret: !vault |'));
      const decrypted = await decryptVaultString(encrypted, { password: 'token-pass' });
      assert.equal(decrypted, 'my-token');
    });

    it('encrypts and decrypts multiline strings with quotes and special characters', async () => {
      const multiline = `-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0...==\n"quoted" and 'single' and $DOLLARS\n-----END RSA PRIVATE KEY-----`;
      const encrypted = await encryptVaultString(multiline, 'rsa_key', { password: 'rsa-password-999' });
      const decrypted = await decryptVaultString(encrypted, { password: 'rsa-password-999' });
      assert.equal(decrypted, multiline);
    });

    it('decrypts raw $ANSIBLE_VAULT payload without YAML variable prefix', async () => {
      const encrypted = await encryptVaultString('hello-raw', 'test_key', { password: 'p1' });
      const vaultIdx = encrypted.indexOf('$ANSIBLE_VAULT;');
      const rawPayload = encrypted.slice(vaultIdx).split('\n').map(l => l.trim()).join('\n');

      const decrypted = await decryptVaultString(rawPayload, { password: 'p1' });
      assert.equal(decrypted, 'hello-raw');
    });

    it('uses resolved password from ANSIBLE_VAULT_PASSWORD env var when options.password is omitted', async () => {
      process.env.ANSIBLE_VAULT_PASSWORD = 'auto-env-pass';
      const encrypted = await encryptVaultString('auto-secret');
      assert.ok(encrypted.includes('$ANSIBLE_VAULT;1.1;AES256'));

      const decrypted = await decryptVaultString(encrypted);
      assert.equal(decrypted, 'auto-secret');
    });

    it('encrypts and decrypts when ANSIBLE_VAULT_PASSWORD_FILE is set to a real file without options.password', async () => {
      fs.writeFileSync(tmpPassFile, 'tier2-file-secret\n', 'utf8');
      process.env.ANSIBLE_VAULT_PASSWORD_FILE = tmpPassFile;

      const plaintext = 'secret-from-vault-password-file';
      const encrypted = await encryptVaultString(plaintext, 'file_var');
      assert.ok(encrypted.includes('file_var: !vault |'));
      assert.ok(encrypted.includes('$ANSIBLE_VAULT;1.1;AES256'));

      const decrypted = await decryptVaultString(encrypted);
      assert.equal(decrypted, plaintext);
    });

    it('encrypts and decrypts with explicit options.password when ANSIBLE_VAULT_PASSWORD_FILE and ANSIBLE_VAULT_IDENTITY_LIST are set', async () => {
      fs.writeFileSync(tmpPassFile, 'other-file-secret\n', 'utf8');
      process.env.ANSIBLE_VAULT_PASSWORD_FILE = tmpPassFile;
      process.env.ANSIBLE_VAULT_IDENTITY_LIST = 'other@/tmp/dummy-identity';

      const plaintext = 'explicit-secret-value';
      const explicitPassword = 'custom-explicit-password';
      const encrypted = await encryptVaultString(plaintext, 'explicit_var', { password: explicitPassword });
      assert.ok(encrypted.includes('explicit_var: !vault |'));
      assert.ok(encrypted.includes('$ANSIBLE_VAULT;1.1;AES256'));

      const decrypted = await decryptVaultString(encrypted, { password: explicitPassword });
      assert.equal(decrypted, plaintext);
    });

    it('fails when decrypting with incorrect password', async () => {
      const encrypted = await encryptVaultString('my-secret', 'k', { password: 'correct-password' });
      await assert.rejects(
        async () => {
          await decryptVaultString(encrypted, { password: 'wrong-password' });
        },
        /ansible-vault decrypt failed|Decryption failed/i
      );
    });
  });

  describe('REST API Endpoints', () => {
    let server: http.Server;
    let baseUrl: string;

    beforeEach(async () => {
      const mock = createMockApp();
      server = mock.server;
      registerVaultRoutes(mock.app);
      const listening = await listenServer(server);
      baseUrl = listening.baseUrl;
    });

    afterEach(async () => {
      if (server) {
        await new Promise<void>((resolve) => server.close(() => resolve()));
      }
    });

    it('GET /api/vault/status returns password source status', async () => {
      const res1 = await fetch(`${baseUrl}/api/vault/status`);
      const body1 = await res1.json();
      assert.equal(res1.status, 200);
      assert.equal(body1.success, true);
      assert.equal(body1.hasPasswordSource, false);
      assert.equal(body1.source, 'ask_password');

      process.env.ANSIBLE_VAULT_PASSWORD = 'test-status-pass';
      const res2 = await fetch(`${baseUrl}/api/vault/status`);
      const body2 = await res2.json();
      assert.equal(body2.success, true);
      assert.equal(body2.hasPasswordSource, true);
      assert.equal(body2.source, 'env');
    });

    it('POST /api/vault/encrypt validates plaintext and returns encrypted block', async () => {
      const badRes = await fetch(`${baseUrl}/api/vault/encrypt`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({}),
      });
      assert.equal(badRes.status, 400);

      const res = await fetch(`${baseUrl}/api/vault/encrypt`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          plaintext: 'api-secret-value',
          varName: 'api_key',
          password: 'api-password',
        }),
      });
      assert.equal(res.status, 200);
      const data = await res.json();
      assert.equal(data.success, true);
      assert.ok(data.vaultBlock.includes('api_key: !vault |'));
      assert.ok(data.vaultBlock.includes('$ANSIBLE_VAULT;1.1;AES256'));

      // Test decrypting back via POST /api/vault/decrypt
      const decRes = await fetch(`${baseUrl}/api/vault/decrypt`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          vaultBlock: data.vaultBlock,
          password: 'api-password',
        }),
      });
      assert.equal(decRes.status, 200);
      const decData = await decRes.json();
      assert.equal(decData.success, true);
      assert.equal(decData.plaintext, 'api-secret-value');
    });

    it('POST /api/vault/decrypt rejects invalid payload or missing password', async () => {
      const badRes = await fetch(`${baseUrl}/api/vault/decrypt`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({}),
      });
      assert.equal(badRes.status, 400);

      const noPassRes = await fetch(`${baseUrl}/api/vault/decrypt`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ vaultBlock: '$ANSIBLE_VAULT;1.1;AES256\n1234' }),
      });
      assert.equal(noPassRes.status, 400);
      const noPassData = await noPassRes.json();
      assert.equal(noPassData.success, false);
      assert.ok(noPassData.error.includes('PASSWORD_REQUIRED'));
    });
  });
});

describe('Vault decryption audit trail', () => {
  let tmpDir: string;

  beforeEach(() => {
    tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'vault-audit-test-'));
  });

  afterEach(() => {
    try {
      fs.rmSync(tmpDir, { recursive: true, force: true });
    } catch {
      // ignore cleanup error
    }
  });

  const listen = async (server: http.Server): Promise<{ baseUrl: string }> => {
    return listenServer(server);
  };

  it('writes exactly one plaintext-free row per decryption and survives a store restart', async () => {
    const dbPath = path.join(tmpDir, 'hosts-cache.sqlite');
    const store = createHostCacheStore(dbPath);

    const mock = createMockApp();
    registerVaultRoutes(mock.app, { auditStore: store, actor: 'atc-coordinator', action: 'default-action' });
    const { baseUrl } = await listen(mock.server);
    const server = mock.server;

    try {
      const plaintextValue = 'super-secret-db-password-do-not-log';

      // Encrypt a real entry with the real ansible-vault binary.
      const encRes = await fetch(`${baseUrl}/api/vault/encrypt`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ plaintext: plaintextValue, varName: 'db_password', password: 'audit-pass' }),
      });
      const encData = await encRes.json();
      assert.equal(encRes.status, 200);
      assert.ok(encData.vaultBlock.includes('db_password: !vault |'));

      // Decrypt it, naming the actor / host / action that triggered it.
      const decRes = await fetch(`${baseUrl}/api/vault/decrypt`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          vaultBlock: encData.vaultBlock,
          password: 'audit-pass',
          varName: 'db_password',
          host: 'db-01.leo-banjo.ts.net',
          actor: 'mike@atc',
          action: 'playbook: postgres-backup',
        }),
      });
      const decData = await decRes.json();
      assert.equal(decRes.status, 200);
      assert.equal(decData.plaintext, plaintextValue);

      const auditRes = await fetch(`${baseUrl}/api/vault/audit`);
      const auditData = await auditRes.json();
      assert.equal(auditRes.status, 200);
      assert.equal(auditData.success, true);
      assert.equal(auditData.total, 1);
      const row = auditData.events[0];
      assert.equal(row.variableName, 'db_password');
      assert.equal(row.host, 'db-01.leo-banjo.ts.net');
      assert.equal(row.actor, 'mike@atc');
      assert.equal(row.action, 'playbook: postgres-backup');
      assert.equal(row.passwordSource, 'inline');
      assert.ok(row.timestamp > 0);

      // Secret Protection Invariant: no plaintext anywhere in the audit payload.
      const serialized = JSON.stringify(auditData);
      assert.ok(!serialized.includes(plaintextValue));
      assert.ok(!serialized.includes('super-secret'));

      // Filtering by variable and by host.
      const byVar = await (await fetch(`${baseUrl}/api/vault/audit?variable=db_password`)).json();
      assert.equal(byVar.total, 1);
      const byOtherVar = await (await fetch(`${baseUrl}/api/vault/audit?variable=other_var`)).json();
      assert.equal(byOtherVar.total, 0);
      const byHost = await (await fetch(`${baseUrl}/api/vault/audit?host=db-01.leo-banjo.ts.net`)).json();
      assert.equal(byHost.total, 1);
      const byOtherHost = await (await fetch(`${baseUrl}/api/vault/audit?host=nope`)).json();
      assert.equal(byOtherHost.total, 0);
    } finally {
      await new Promise<void>(resolve => server.close(() => resolve()));
    }

    store.close();

    // Persistence: a fresh store over the same file still sees the row.
    const reopened = createHostCacheStore(dbPath);
    try {
      assert.equal(reopened.countVaultAuditEvents(), 1);
      const persisted = reopened.getVaultAuditEvents();
      assert.equal(persisted.length, 1);
      assert.equal(persisted[0].variableName, 'db_password');
      assert.equal(persisted[0].host, 'db-01.leo-banjo.ts.net');
    } finally {
      reopened.close();
    }
  });

  it('returns newest events first and defaults metadata when the request is bare', async () => {
    const store = createHostCacheStore(path.join(tmpDir, 'audit.sqlite'));
    const mock = createMockApp();
    registerVaultRoutes(mock.app, { auditStore: store });
    const { baseUrl } = await listen(mock.server);
    const server = mock.server;

    try {
      const encRes = await fetch(`${baseUrl}/api/vault/encrypt`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ plaintext: 'one', varName: 'first_secret', password: 'p' }),
      });
      const encData = await encRes.json();

      // First decryption: no metadata in the body -> name derived from the block.
      await fetch(`${baseUrl}/api/vault/decrypt`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ vaultBlock: encData.vaultBlock, password: 'p' }),
      });
      await new Promise(r => setTimeout(r, 5));
      await fetch(`${baseUrl}/api/vault/decrypt`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ vaultBlock: encData.vaultBlock, password: 'p', varName: 'second_secret', host: 'h2' }),
      });

      const data = await (await fetch(`${baseUrl}/api/vault/audit`)).json();
      assert.equal(data.total, 2);
      assert.equal(data.events[0].variableName, 'second_secret');
      assert.equal(data.events[1].variableName, 'first_secret');
      assert.equal(data.events[1].host, 'n/a');
      assert.ok(String(data.events[1].actor).length > 0);
      assert.equal(data.events[1].action, 'POST /api/vault/decrypt');
    } finally {
      await new Promise<void>(resolve => server.close(() => resolve()));
      store.close();
    }
  });
});
