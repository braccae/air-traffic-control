import { describe, it, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import {
  REQUIRED_VENV_PACKAGES,
  getAnsibleVenvPath,
  findOrBootstrapUv,
  ensureAnsibleVenv,
  getAnsibleExecutionEnv,
} from './ansibleVenvManager.ts';

describe('ansibleVenvManager', () => {
  let tempDir: string;
  const originalEnv = { ...process.env };

  beforeEach(() => {
    tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'ansible-venv-test-'));
    process.env = { ...originalEnv };
  });

  afterEach(() => {
    process.env = { ...originalEnv };
    try {
      fs.rmSync(tempDir, { recursive: true, force: true });
    } catch {
      // ignore cleanup error
    }
  });

  describe('REQUIRED_VENV_PACKAGES', () => {
    it('contains the expected core and WinRM packages', () => {
      assert.deepEqual(REQUIRED_VENV_PACKAGES, [
        'ansible-core',
        'pywinrm',
        'requests',
        'jmespath',
        'cryptography',
        'netaddr',
      ]);
    });
  });

  describe('getAnsibleVenvPath', () => {
    it('returns process.env.ANSIBLE_VENV_PATH if set', () => {
      process.env.ANSIBLE_VENV_PATH = '/custom/path/ansible-venv';
      assert.equal(getAnsibleVenvPath(), '/custom/path/ansible-venv');
    });

    it('defaults to <cwd>/data/ansible-venv when ANSIBLE_VENV_PATH is not set', () => {
      delete process.env.ANSIBLE_VENV_PATH;
      const expected = path.resolve(process.cwd(), 'data', 'ansible-venv');
      assert.equal(getAnsibleVenvPath(), expected);
    });
  });

  describe('findOrBootstrapUv', () => {
    it('returns uv from customDataDir if it already exists there', async () => {
      const binDir = path.join(tempDir, 'bin');
      fs.mkdirSync(binDir, { recursive: true });
      const fakeUvPath = path.join(binDir, 'uv');
      fs.writeFileSync(fakeUvPath, '#!/bin/sh\necho "uv 0.5.0"', { mode: 0o755 });

      const uv = await findOrBootstrapUv({ customDataDir: tempDir, downloadIfMissing: false });
      assert.equal(uv, fakeUvPath);
    });

    it('returns system uv if present and no local binary exists', async () => {
      const uv = await findOrBootstrapUv({ customDataDir: tempDir, downloadIfMissing: false });
      assert.ok(uv);
      assert.equal(fs.existsSync(uv), true);
    });

    it('throws error if missing and downloadIfMissing is false', async () => {
      process.env.PATH = path.join(tempDir, 'empty-bin');
      process.env.HOME = path.join(tempDir, 'fake-home');
      fs.mkdirSync(process.env.PATH, { recursive: true });

      await assert.rejects(
        async () => {
          await findOrBootstrapUv({ customDataDir: tempDir, downloadIfMissing: false });
        },
        /uv binary not found/i
      );
    });

    it('downloads standalone uv to customDataDir/bin/uv when missing and download enabled', async () => {
      process.env.PATH = path.join(tempDir, 'empty-bin');
      process.env.HOME = path.join(tempDir, 'fake-home');
      fs.mkdirSync(process.env.PATH, { recursive: true });

      const downloadedUv = await findOrBootstrapUv({
        customDataDir: tempDir,
        downloadIfMissing: true,
      });

      assert.equal(downloadedUv, path.join(tempDir, 'bin', 'uv'));
      assert.equal(fs.existsSync(downloadedUv), true);
      const stat = fs.statSync(downloadedUv);
      assert.ok((stat.mode & 0o111) > 0, 'Downloaded uv binary must be executable');
    });
  });

  describe('getAnsibleExecutionEnv', () => {
    it('prepends venv bin directory to PATH and sets VIRTUAL_ENV', () => {
      const venvPath = path.resolve(process.cwd(), 'data', 'ansible-venv');
      const venvBin = path.join(venvPath, 'bin');

      const baseEnv = { PATH: '/usr/bin:/bin', FOO: 'bar' };
      const env = getAnsibleExecutionEnv(baseEnv);

      assert.equal(env.VIRTUAL_ENV, venvPath);
      assert.equal(env.PATH, `${venvBin}:/usr/bin:/bin`);
      assert.equal(env.FOO, 'bar');
    });

    it('handles empty or undefined PATH in baseEnv', () => {
      const venvPath = path.resolve(process.cwd(), 'data', 'ansible-venv');
      const venvBin = path.join(venvPath, 'bin');

      const env = getAnsibleExecutionEnv({});
      assert.equal(env.VIRTUAL_ENV, venvPath);
      assert.equal(env.PATH, venvBin);
    });
  });

  describe('ensureAnsibleVenv', () => {
    it('creates venv and installs packages if ansible is missing', async () => {
      const fakeVenvDir = path.join(tempDir, 'fake-venv');
      const binDir = path.join(fakeVenvDir, 'bin');
      fs.mkdirSync(binDir, { recursive: true });

      const fakeUv = path.join(tempDir, 'mock-uv');
      fs.writeFileSync(
        fakeUv,
        `#!/bin/sh
cmd="$1"
if [ "$cmd" = "venv" ]; then
  mkdir -p "$2/bin"
  touch "$2/bin/python"
  touch "$2/bin/ansible"
  touch "$2/bin/ansible-playbook"
  touch "$2/bin/ansible-vault"
  chmod +x "$2/bin/"*
elif [ "$cmd" = "pip" ]; then
  exit 0
fi
`,
        { mode: 0o755 }
      );

      const info = await ensureAnsibleVenv({
        uvPath: fakeUv,
        venvPath: fakeVenvDir,
      });

      assert.equal(info.venvPath, fakeVenvDir);
      assert.equal(info.binDir, binDir);
      assert.equal(info.ansibleBinary, path.join(binDir, 'ansible'));
      assert.equal(info.ansiblePlaybookBinary, path.join(binDir, 'ansible-playbook'));
      assert.equal(info.ansibleVaultBinary, path.join(binDir, 'ansible-vault'));
      assert.equal(info.pythonBinary, path.join(binDir, 'python'));
      assert.deepEqual(info.installedPackages, REQUIRED_VENV_PACKAGES);
    });

    it('skips installation if ansible binary already exists in venv', async () => {
      const fakeVenvDir = path.join(tempDir, 'existing-venv');
      const binDir = path.join(fakeVenvDir, 'bin');
      fs.mkdirSync(binDir, { recursive: true });
      fs.writeFileSync(path.join(binDir, 'ansible'), '#!/bin/sh\n', { mode: 0o755 });
      fs.writeFileSync(path.join(binDir, 'ansible-playbook'), '#!/bin/sh\n', { mode: 0o755 });
      fs.writeFileSync(path.join(binDir, 'ansible-vault'), '#!/bin/sh\n', { mode: 0o755 });
      fs.writeFileSync(path.join(binDir, 'python'), '#!/bin/sh\n', { mode: 0o755 });

      const info = await ensureAnsibleVenv({
        uvPath: '/non/existent/uv',
        venvPath: fakeVenvDir,
      });

      assert.equal(info.venvPath, fakeVenvDir);
      assert.equal(info.ansibleBinary, path.join(binDir, 'ansible'));
    });
  });
});
