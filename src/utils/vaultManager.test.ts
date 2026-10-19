import { describe, it, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import http from 'node:http';
import express from 'express';
import {
  resolveVaultPasswordSource,
  encryptVaultString,
  decryptVaultString,
  registerVaultRoutes,
  type VaultPasswordSource,
} from './vaultManager.ts';

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
  });

  afterEach(() => {
    process.env = { ...originalEnv };
    try {
      fs.rmSync(tmpDir, { recursive: true, force: true });
    } catch {
      // ignore cleanup error
    }
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
    let app: express.Express;
    let server: http.Server;
    let baseUrl: string;

    beforeEach(async () => {
      app = express();
      app.use(express.json());
      registerVaultRoutes(app);

      await new Promise<void>((resolve) => {
        server = app.listen(0, '127.0.0.1', () => {
          const addr = server.address();
          if (typeof addr === 'object' && addr) {
            baseUrl = `http://127.0.0.1:${addr.port}`;
          }
          resolve();
        });
      });
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
