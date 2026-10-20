import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { execFileSync } from 'node:child_process';

import {
  RCLONE_PROVIDERS,
  getProvider,
  sanitizeRemoteName,
  sanitizeMountTarget,
  isValidMountPolicy,
  serializeRemoteConfig,
  serializeRemoteConfigToVault,
  deserializeRemoteConfig,
  deserializeRemoteConfigFromVault,
  maskRemoteConfig,
  remoteView,
  buildRcloneVaultVarName,
  encryptSecret,
  decryptSecret,
  buildRcloneConfigSection,
  buildSystemdServiceUnit,
  buildSystemdServiceDropIn,
  buildSystemdAutomountUnits,
  buildSystemdMountUnitName,
  buildSystemdAutomountUnitName,
  buildSystemdDropInPath,
  buildFstabLine,
  buildMountScript,
  previewMountArtifacts,
  verifyRemoteConfig,
  registerRcloneRoutes,
  type RcloneCompiledTask,
} from './rcloneManager.ts';
import { createHostCacheStore } from './hostCacheDb.ts';
import type { RcloneRemoteRecord, NodeItem } from '../types/index.ts';

const KEY = Buffer.alloc(32, 7);

function rcloneAvailable(): boolean {
  try {
    execFileSync('rclone', ['version'], { stdio: 'ignore' });
    return true;
  } catch {
    return false;
  }
}

test('provider catalog exposes required providers', () => {
  const ids = RCLONE_PROVIDERS.map(p => p.id);
  for (const id of ['s3', 's3-compatible', 'sftp', 'webdav', 'b2', 'local']) {
    assert.ok(ids.includes(id), `expected provider ${id}`);
  }
  const sftp = getProvider('sftp')!;
  assert.equal(sftp.type, 'sftp');
  assert.ok(sftp.fields.find(f => f.name === 'pass' && f.secret && f.obscure));
});

test('sanitizeRemoteName rejects unsafe names', () => {
  assert.equal(sanitizeRemoteName('  hetzner  '), 'hetzner');
  for (const bad of ['', 'bad name', 'a/b', 'a:b', 'x'.repeat(65), 'semi;colon']) {
    assert.throws(() => sanitizeRemoteName(bad), /remote name/i, `expected "${bad}" to be rejected`);
  }
});

test('sanitizeMountTarget requires a safe absolute path', () => {
  assert.equal(sanitizeMountTarget('/mnt/rclone//hetzner'), '/mnt/rclone/hetzner');
  for (const bad of ['', 'relative/path', '/mnt/$(rm -rf /)', '/mnt/a;b', '/mnt/`x`']) {
    assert.throws(() => sanitizeMountTarget(bad), /mount target/i);
  }
});

test('mount policy validation', () => {
  for (const p of ['systemd-mount', 'systemd-automount', 'fstab', 'script']) {
    assert.ok(isValidMountPolicy(p));
  }
  assert.ok(!isValidMountPolicy('nfs'));
});

test('secret values are encrypted in Ansible Vault store and masked as •••••••• (vault: <var_name>)', async () => {
  const prevPass = process.env.ANSIBLE_VAULT_PASSWORD;
  process.env.ANSIBLE_VAULT_PASSWORD = 'rclone-vault-unit-pass';
  const hostCache = createHostCacheStore(':memory:');
  try {
    const stored = await serializeRemoteConfigToVault(
      'hetzner',
      'sftp',
      { host: 'h', user: 'u', pass: 'p@ss' },
      hostCache
    );
    assert.ok(!stored.includes('p@ss'), 'plaintext secret must not be stored');
    assert.ok(stored.includes('"host":"h"'), 'non-secret fields stay readable');
    assert.ok(stored.includes('vault:rclone_hetzner_pass'), 'secret field references vault variable');

    const varName = buildRcloneVaultVarName('hetzner', 'pass');
    assert.equal(varName, 'rclone_hetzner_pass');
    const vaultBlock = hostCache.getVaultVariableBlock(varName);
    assert.ok(vaultBlock && vaultBlock.includes('$ANSIBLE_VAULT;1.1;AES256'));
    assert.ok(!vaultBlock!.includes('p@ss'));

    const masked = maskRemoteConfig(stored, 'hetzner');
    assert.equal(masked.pass, '•••••••• (vault: rclone_hetzner_pass)');
    assert.equal(masked.host, 'h');

    const roundtrip = await deserializeRemoteConfigFromVault(stored, hostCache, {
      auditStore: hostCache,
      actor: 'tester',
      host: 'edge-01',
      action: 'unit-test-decrypt',
    });
    assert.deepEqual(roundtrip, { host: 'h', user: 'u', pass: 'p@ss' });

    const auditEvents = hostCache.getVaultAuditEvents({ variableName: varName });
    assert.equal(auditEvents.length, 1);
    assert.equal(auditEvents[0].variableName, 'rclone_hetzner_pass');
    assert.equal(auditEvents[0].actor, 'tester');
    assert.equal(auditEvents[0].host, 'edge-01');
    assert.equal(auditEvents[0].action, 'unit-test-decrypt');
  } finally {
    hostCache.close();
    if (prevPass === undefined) delete process.env.ANSIBLE_VAULT_PASSWORD;
    else process.env.ANSIBLE_VAULT_PASSWORD = prevPass;
  }
});

test('legacy secret helper compatibility', () => {
  const blob = encryptSecret('super-secret', KEY);
  assert.ok(blob.startsWith('enc:v1:'));
  assert.ok(!blob.includes('super-secret'));
  assert.equal(decryptSecret(blob, KEY), 'super-secret');
  assert.throws(() => decryptSecret(blob, Buffer.alloc(32, 9)));

  const stored = serializeRemoteConfig('sftp', { host: 'h', user: 'u', pass: 'p@ss' }, KEY);
  const masked = maskRemoteConfig(stored, 'hetzner');
  assert.equal(masked.pass, '•••••••• (vault: rclone_hetzner_pass)');
  assert.equal(masked.host, 'h');

  const roundtrip = deserializeRemoteConfig(stored, KEY);
  assert.deepEqual(roundtrip, { host: 'h', user: 'u', pass: 'p@ss' });
});

test('remoteView never leaks decrypted secrets and uses vault mask format', () => {
  const record: RcloneRemoteRecord = {
    id: 'r1',
    name: 'hetzner',
    provider: 'sftp',
    type: 'sftp',
    configJson: JSON.stringify({ host: 'h', user: 'u', pass: 'vault:rclone_hetzner_pass' }),
    createdAt: 1,
    updatedAt: 2,
  };
  const view = remoteView(record);
  assert.equal(view.fields.pass, '•••••••• (vault: rclone_hetzner_pass)');
  assert.ok(!JSON.stringify(view).includes('topsecret'));
});

test('preview: each mount policy installs different systemd units and drop-in artifacts', () => {
  const a = previewMountArtifacts('systemd-mount', 'hetzner', '/mnt/rclone/hetzner');
  const b = previewMountArtifacts('systemd-automount', 'hetzner', '/mnt/rclone/hetzner');
  const c = previewMountArtifacts('fstab', 'hetzner', '/mnt/rclone/hetzner');
  const d = previewMountArtifacts('script', 'hetzner', '/mnt/rclone/hetzner');
  assert.deepEqual(a, [
    '/etc/systemd/system/rclone-hetzner.service',
    '/etc/systemd/system/rclone-hetzner.service.d/atc-rclone.conf',
  ]);
  assert.deepEqual(b, [
    '/etc/systemd/system/mnt-rclone-hetzner.mount',
    '/etc/systemd/system/mnt-rclone-hetzner.mount.d/atc-rclone.conf',
    '/etc/systemd/system/mnt-rclone-hetzner.automount',
  ]);
  assert.equal(buildSystemdMountUnitName('/mnt/rclone/hetzner'), 'mnt-rclone-hetzner.mount');
  assert.equal(buildSystemdAutomountUnitName('/mnt/rclone/hetzner'), 'mnt-rclone-hetzner.automount');
  assert.equal(
    buildSystemdDropInPath('mnt-rclone-hetzner.mount'),
    '/etc/systemd/system/mnt-rclone-hetzner.mount.d/atc-rclone.conf'
  );
  assert.ok(c[0].includes('fuse.rclone'));
  assert.ok(d.some(p => p.endsWith('.sh')));
  assert.notDeepEqual(a, b);
  assert.notDeepEqual(b, d);
});

test('generated units, drop-ins, and scripts never embed credentials', () => {
  const service = buildSystemdServiceUnit('hetzner', '/mnt/rclone/hetzner', '--vfs-cache-mode writes');
  assert.ok(service.includes('ExecStart=/usr/bin/rclone mount hetzner: /mnt/rclone/hetzner'));
  assert.ok(!/pass|secret|key/i.test(service.replace(/vfs-cache-mode|RestartSec|After|Wants|Description|ExecStop|Type|User/g, '')));

  const dropIn = buildSystemdServiceDropIn('hetzner', '/mnt/rclone/hetzner', '--vfs-cache-mode writes');
  assert.ok(dropIn.includes('[Service]'));
  assert.ok(dropIn.includes('RCLONE_CONFIG=/root/.config/rclone/rclone.conf'));

  const { mountUnit, automountUnit, mountDropIn } = buildSystemdAutomountUnits('hetzner', '/mnt/rclone/hetzner', '--vfs-cache-mode writes');
  assert.ok(mountUnit.includes('Type=fuse.rclone'));
  assert.ok(automountUnit.includes('[Automount]'));
  assert.ok(mountDropIn.includes('[Mount]'));

  const fstab = buildFstabLine('hetzner', '/mnt/rclone/hetzner', '--vfs-cache-mode writes');
  assert.ok(fstab.includes('x-systemd.automount'));

  const script = buildMountScript('hetzner', '/mnt/rclone/hetzner', '--vfs-cache-mode writes');
  assert.ok(script.startsWith('#!/bin/sh'));
  assert.ok(script.includes('exec /usr/bin/rclone mount hetzner: /mnt/rclone/hetzner'));
});

test('buildRcloneConfigSection obscures secret fields (real rclone)', { skip: !rcloneAvailable() }, async () => {
  const section = await buildRcloneConfigSection('hetzner', 'sftp', {
    host: 'sftp.example.com',
    user: 'backup',
    port: '22',
    pass: 'plaintext-password',
  });
  assert.ok(section.startsWith('[hetzner]'));
  assert.ok(section.includes('type = sftp'));
  assert.ok(section.includes('host = sftp.example.com'));
  assert.ok(!section.includes('plaintext-password'), 'password must be obscured, not plaintext');
});

test('local provider verifies against the real rclone binary', { skip: !rcloneAvailable() }, async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'atc-rclone-verify-'));
  fs.writeFileSync(path.join(dir, 'marker.txt'), 'hello');
  try {
    const ok = await verifyRemoteConfig('atctest-local', 'local', {});
    assert.equal(ok.ok, true, `expected local verify to succeed: ${ok.error}`);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('verifyRemoteConfig reports honest failure for unreachable provider', { skip: !rcloneAvailable() }, async () => {
  const result = await verifyRemoteConfig('atctest-bad', 'webdav', {
    url: 'http://127.0.0.1:1/definitely-not-here',
    vendor: 'other',
  }, 8000);
  assert.equal(result.ok, false);
  assert.ok((result.error || '').length > 0, 'failure must include an error message');
});

// ---------------------------------------------------------------------------
// Route-level integration: Vault store + Vault Audit log + install/policy re-stage
// ---------------------------------------------------------------------------

type Handler = (req: any, res: any) => any;

function makeRecorderApp() {
  const routes = new Map<string, Handler>();
  const app: any = {
    get: (p: string, h: Handler) => routes.set(`GET ${p}`, h),
    post: (p: string, h: Handler) => routes.set(`POST ${p}`, h),
    put: (p: string, h: Handler) => routes.set(`PUT ${p}`, h),
    delete: (p: string, h: Handler) => routes.set(`DELETE ${p}`, h),
  };
  const call = (method: string, path: string, req: any = {}) => {
    const handler = routes.get(`${method} ${path}`);
    if (!handler) throw new Error(`no route ${method} ${path}`);
    let statusCode = 200;
    let payload: any;
    const res = {
      status(code: number) {
        statusCode = code;
        return res;
      },
      json(body: any) {
        payload = body;
        return res;
      },
    };
    return Promise.resolve(handler({ params: {}, body: {}, ...req }, res)).then(() => ({ statusCode, payload }));
  };
  return { app, call };
}

test('install & verify routes persist secrets in vault_variables, log Vault Audit events, and stage policy-specific tasks', async () => {
  const prevPass = process.env.ANSIBLE_VAULT_PASSWORD;
  process.env.ANSIBLE_VAULT_PASSWORD = 'unit-test-vault-password';
  const hostCache = createHostCacheStore(':memory:');
  try {
    const node = {
      id: 'node-1',
      name: 'edge-01',
      shortName: 'edge-01',
      group: 'lab',
      ip: '192.168.1.10',
      port: 22,
      sshUser: 'core',
      tailscaleIp: '100.64.0.5',
      tailscaleHost: 'edge-01.ts.net',
      status: 'online',
    } as unknown as NodeItem;
    const staged: any[] = [];
    const compiled: Array<{ policy: string; options: any }> = [];

    const { app, call } = makeRecorderApp();
    registerRcloneRoutes(app, {
      hostCache,
      actor: 'cody@atc',
      getNode: id => (id === node.id ? node : null),
      compileInstall: async (_nodeId, options): Promise<RcloneCompiledTask> => {
        compiled.push({ policy: options.policy, options });
        const module =
          options.policy === 'fstab'
            ? 'ansible.posix.mount'
            : options.policy === 'script'
            ? 'ansible.builtin.copy'
            : 'ansible.builtin.systemd_service';
        return { name: `Install ${options.name}`, steps: [{ name: 'x', module, params: { a: 1 } }] };
      },
      stageTask: task => {
        staged.push(task);
        return { id: `q-${staged.length}`, ...task };
      },
    });

    // Create a remote (guided setup stores secret in vault_variables and reference in rclone_remotes).
    const created = await call('POST', '/api/rclone/remotes', {
      body: { name: 'hetzner', provider: 'sftp', fields: { host: 'h', user: 'u', pass: 'p' } },
    });
    assert.equal(created.statusCode, 200);
    assert.equal(created.payload.remote.name, 'hetzner');
    assert.equal(created.payload.remote.fields.pass, '•••••••• (vault: rclone_hetzner_pass)');
    const remoteId = created.payload.remote.id;

    // The stored configJson must reference vault_variables and never contain the plaintext secret.
    const stored = hostCache.getRcloneRemote(remoteId)!;
    assert.ok(!stored.configJson.includes('"p"'), 'plaintext secret must not be persisted');
    assert.ok(stored.configJson.includes('vault:rclone_hetzner_pass'));
    const vaultBlock = hostCache.getVaultVariableBlock('rclone_hetzner_pass');
    assert.ok(vaultBlock && vaultBlock.includes('$ANSIBLE_VAULT;1.1;AES256'));

    // Verify route decrypts the vault variable and records a Vault Audit event.
    await call('POST', '/api/rclone/remotes/:id/verify', {
      params: { id: remoteId },
      body: { actor: 'auditor@atc' },
    });
    const verifyAudit = hostCache.getVaultAuditEvents({ variableName: 'rclone_hetzner_pass' });
    assert.equal(verifyAudit.length, 1);
    assert.equal(verifyAudit[0].actor, 'auditor@atc');
    assert.equal(verifyAudit[0].host, 'coordinator');
    assert.equal(verifyAudit[0].action, 'rclone.verifyRemote:hetzner');

    // Install with systemd-mount -> decrypts secret, logs audit event for target host, stages task.
    const install = await call('POST', '/api/rclone/remotes/:id/install', {
      params: { id: remoteId },
      body: { nodeId: node.id, mountTarget: '/mnt/rclone/hetzner', policy: 'systemd-mount' },
    });
    assert.equal(install.statusCode, 200);
    assert.ok(install.payload.task, 'expected a staged queue task');
    assert.equal(install.payload.mount.policy, 'systemd-mount');
    assert.equal(
      install.payload.secretsMasked.pass,
      '•••••••• (vault: rclone_hetzner_pass)'
    );
    assert.equal(staged.length, 1);
    assert.equal(staged[0].steps[0].module, 'ansible.builtin.systemd_service');
    assert.equal(staged[0].targetNodeId, node.id);
    // Obscured config is passed to the compiler, never the plaintext password.
    assert.ok(compiled[0].options.configContent.includes('type = sftp'));
    assert.ok(!compiled[0].options.configContent.includes('pass = p\n'));

    // Check Vault Audit log recorded the install decryption against edge-01.ts.net.
    const installAudit = hostCache.getVaultAuditEvents({
      variableName: 'rclone_hetzner_pass',
      host: 'edge-01.ts.net',
    });
    assert.equal(installAudit.length, 1);
    assert.equal(installAudit[0].actor, 'cody@atc');
    assert.equal(installAudit[0].action, 'rclone.install:hetzner (systemd-mount)');

    // Mount persisted for the node.
    const mounts = hostCache.getRcloneMounts(node.id);
    assert.equal(mounts.length, 1);
    assert.equal(mounts[0].remoteId, remoteId);

    // Change the mount policy -> re-install stages a *different* task and logs another audit event.
    const repolicy = await call('POST', '/api/rclone/remotes/:id/install', {
      params: { id: remoteId },
      body: { nodeId: node.id, mountTarget: '/mnt/rclone/hetzner', policy: 'fstab' },
    });
    assert.equal(repolicy.statusCode, 200);
    assert.equal(staged.length, 2);
    assert.equal(staged[1].steps[0].module, 'ansible.posix.mount');
    assert.notEqual(staged[0].steps[0].module, staged[1].steps[0].module);
    assert.equal(hostCache.getRcloneMount(node.id, remoteId)!.policy, 'fstab');
    assert.equal(hostCache.getVaultAuditEvents({ variableName: 'rclone_hetzner_pass' }).length, 3);
  } finally {
    hostCache.close();
    if (prevPass === undefined) delete process.env.ANSIBLE_VAULT_PASSWORD;
    else process.env.ANSIBLE_VAULT_PASSWORD = prevPass;
  }
});

test('install route rejects missing node, bad policy, and unknown node', async () => {
  const prevPass = process.env.ANSIBLE_VAULT_PASSWORD;
  process.env.ANSIBLE_VAULT_PASSWORD = 'unit-test-vault-password';
  const hostCache = createHostCacheStore(':memory:');
  try {
    const { app, call } = makeRecorderApp();
    registerRcloneRoutes(app, {
      hostCache,
      getNode: () => null,
      compileInstall: async () => ({ name: 'x', steps: [] }),
      stageTask: () => ({}),
    });

    const created = await call('POST', '/api/rclone/remotes', {
      body: { name: 'r1', provider: 'local', fields: {} },
    });
    const id = created.payload.remote.id;

    const noNode = await call('POST', '/api/rclone/remotes/:id/install', { params: { id }, body: {} });
    assert.equal(noNode.statusCode, 400);

    const badPolicy = await call('POST', '/api/rclone/remotes/:id/install', {
      params: { id },
      body: { nodeId: 'n1', policy: 'nope' },
    });
    assert.equal(badPolicy.statusCode, 400);

    const unknownNode = await call('POST', '/api/rclone/remotes/:id/install', {
      params: { id },
      body: { nodeId: 'missing', policy: 'systemd-mount' },
    });
    assert.equal(unknownNode.statusCode, 404);
  } finally {
    hostCache.close();
    if (prevPass === undefined) delete process.env.ANSIBLE_VAULT_PASSWORD;
    else process.env.ANSIBLE_VAULT_PASSWORD = prevPass;
  }
});
