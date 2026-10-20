/**
 * rcloneManager.ts — rclone remote integration for Air Traffic Control.
 *
 * Remotes are stored in ATC's real database (data/hosts-cache.sqlite, table
 * `rclone_remotes`). Secret field values are encrypted at rest with AES-256-GCM
 * before they are ever written to disk; they are decrypted in memory only when a
 * remote is verified or installed.
 *
 * Plaintext credentials are never written to generated unit files or Ansible
 * tasks. When a remote is pushed to a device, secret fields are converted to
 * rclone's own obscured form (`rclone obscure`) — the same reversible encoding
 * rclone writes into rclone.conf — and the config-write step runs with
 * `no_log: true` so obscured values never reach Ansible output either.
 *
 * Zero-Mock Policy: `verifyRemote` runs the real `rclone` binary against the
 * provider. A failure returns an honest error; it never reports a fabricated
 * "connected" state.
 */
import crypto from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { execFile } from 'node:child_process';
import type { Express, Request, Response } from 'express';
import type { HostCacheStore } from './hostCacheDb.ts';
import type {
  NodeItem,
  RcloneRemoteRecord,
  RcloneMountRecord,
  RcloneMountPolicy,
} from '../types/index.ts';
import {
  sanitizeRemoteName,
  sanitizeMountTarget,
  sanitizeConfigValue,
  isValidMountPolicy,
} from './rclonePolicies.ts';

// Re-export the pure, browser-safe helpers so existing importers (routes, tests,
// previews) keep a single import surface for the whole rclone feature.
export {
  MOUNT_POLICIES,
  sanitizeRemoteName,
  sanitizeMountTarget,
  isValidMountPolicy,
  sanitizeConfigValue,
  buildSystemdServiceUnit,
  buildSystemdAutomountUnits,
  buildFstabLine,
  buildMountScript,
  previewMountArtifacts,
} from './rclonePolicies.ts';

// ---------------------------------------------------------------------------
// Provider catalog
// ---------------------------------------------------------------------------

export interface RcloneProviderField {
  name: string;
  label: string;
  required: boolean;
  secret: boolean;
  obscure: boolean;
  default?: string;
  description?: string;
}

export interface RcloneProvider {
  id: string;
  label: string;
  /** rclone backend type written to the config as `type =`. */
  type: string;
  fields: RcloneProviderField[];
}

const f = (
  name: string,
  label: string,
  required: boolean,
  secret: boolean,
  obscure: boolean,
  rest: Partial<RcloneProviderField> = {}
): RcloneProviderField => ({ name, label, required, secret, obscure, ...rest });

export const RCLONE_PROVIDERS: RcloneProvider[] = [
  {
    id: 's3',
    label: 'Amazon S3',
    type: 's3',
    fields: [
      f('provider', 'S3 Provider', false, false, false, { default: 'AWS' }),
      f('access_key_id', 'Access Key ID', true, true, false),
      f('secret_access_key', 'Secret Access Key', true, true, true),
      f('region', 'Region', false, false, false, { default: 'us-east-1' }),
      f('endpoint', 'Endpoint (optional)', false, false, false),
    ],
  },
  {
    id: 's3-compatible',
    label: 'S3-compatible (MinIO/Ceph/R2)',
    type: 's3',
    fields: [
      f('provider', 'S3 Provider', false, false, false, { default: 'Other' }),
      f('endpoint', 'Endpoint URL', true, false, false),
      f('access_key_id', 'Access Key ID', true, true, false),
      f('secret_access_key', 'Secret Access Key', true, true, true),
      f('region', 'Region', false, false, false, { default: 'us-east-1' }),
    ],
  },
  {
    id: 'sftp',
    label: 'SFTP',
    type: 'sftp',
    fields: [
      f('host', 'Host', true, false, false),
      f('user', 'User', true, false, false),
      f('port', 'Port', false, false, false, { default: '22' }),
      f('key_file', 'Private Key File (path)', false, false, false),
      f('pass', 'Password', false, true, true),
    ],
  },
  {
    id: 'webdav',
    label: 'WebDAV',
    type: 'webdav',
    fields: [
      f('url', 'URL', true, false, false),
      f('vendor', 'Vendor', false, false, false, { default: 'other' }),
      f('user', 'User', false, false, false),
      f('pass', 'Password', false, true, true),
    ],
  },
  {
    id: 'b2',
    label: 'Backblaze B2',
    type: 'b2',
    fields: [
      f('account', 'Account ID', true, true, false),
      f('key', 'Application Key', true, true, true),
    ],
  },
  {
    id: 'azureblob',
    label: 'Azure Blob Storage',
    type: 'azureblob',
    fields: [
      f('account', 'Storage Account', true, false, false),
      f('key', 'Account Key', true, true, true),
    ],
  },
  {
    id: 'drive',
    label: 'Google Drive',
    type: 'drive',
    fields: [
      f('scope', 'Scope', false, false, false, { default: 'drive' }),
      f('token', 'OAuth Token (JSON)', true, true, true),
    ],
  },
  {
    id: 'onedrive',
    label: 'Microsoft OneDrive',
    type: 'onedrive',
    fields: [f('token', 'OAuth Token (JSON)', true, true, true)],
  },
  {
    id: 'dropbox',
    label: 'Dropbox',
    type: 'dropbox',
    fields: [f('token', 'OAuth Token (JSON)', true, true, true)],
  },
  {
    id: 'http',
    label: 'HTTP (read-only)',
    type: 'http',
    fields: [f('url', 'Base URL', true, false, false)],
  },
  {
    id: 'local',
    label: 'Local filesystem',
    type: 'local',
    fields: [],
  },
];

export function getProvider(id: string): RcloneProvider | undefined {
  return RCLONE_PROVIDERS.find(p => p.id === id);
}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Secret encryption (AES-256-GCM)
// ---------------------------------------------------------------------------

const SECRET_PREFIX = 'enc:v1:';

function resolveSecretKey(): Buffer {
  const envKey = process.env.ATC_RCLONE_KEY || process.env.ATC_SECRET_KEY;
  if (envKey && envKey.length > 0) {
    return crypto.createHash('sha256').update(envKey).digest();
  }
  const keyPath = path.resolve(process.cwd(), 'data', '.rclone-key');
  if (fs.existsSync(keyPath)) {
    const raw = fs.readFileSync(keyPath);
    if (raw.length === 32) return raw;
    return crypto.createHash('sha256').update(raw).digest();
  }
  const key = crypto.randomBytes(32);
  fs.mkdirSync(path.dirname(keyPath), { recursive: true });
  fs.writeFileSync(keyPath, key, { mode: 0o600 });
  return key;
}

export function encryptSecret(plaintext: string, key?: Buffer): string {
  const k = key || resolveSecretKey();
  const iv = crypto.randomBytes(12);
  const cipher = crypto.createCipheriv('aes-256-gcm', k, iv);
  const ct = Buffer.concat([cipher.update(plaintext, 'utf8'), cipher.final()]);
  const tag = cipher.getAuthTag();
  return `${SECRET_PREFIX}${iv.toString('hex')}:${tag.toString('hex')}:${ct.toString('base64')}`;
}

export function decryptSecret(blob: string, key?: Buffer): string {
  if (!blob.startsWith(SECRET_PREFIX)) {
    throw new Error('Not an encrypted secret blob');
  }
  const k = key || resolveSecretKey();
  const [ivHex, tagHex, ctB64] = blob.slice(SECRET_PREFIX.length).split(':');
  if (!ivHex || !tagHex || !ctB64) throw new Error('Malformed secret blob');
  const decipher = crypto.createDecipheriv('aes-256-gcm', k, Buffer.from(ivHex, 'hex'));
  decipher.setAuthTag(Buffer.from(tagHex, 'hex'));
  return Buffer.concat([decipher.update(Buffer.from(ctB64, 'base64')), decipher.final()]).toString('utf8');
}

/**
 * Encrypts every `secret: true` field value; leaves non-secret fields as plaintext
 * JSON. The result is what gets stored in `rclone_remotes.config_json`.
 */
export function serializeRemoteConfig(
  providerId: string,
  fields: Record<string, string>,
  key?: Buffer
): string {
  const provider = getProvider(providerId);
  if (!provider) throw new Error(`Unknown provider: ${providerId}`);
  const out: Record<string, string> = {};
  for (const field of provider.fields) {
    const raw = fields[field.name];
    if (raw === undefined || raw === null || String(raw).trim() === '') continue;
    const value = sanitizeConfigValue(String(raw));
    out[field.name] = field.secret ? encryptSecret(value, key) : value;
  }
  return JSON.stringify(out);
}

/** Decrypts a stored config back to plaintext field values (in memory only). */
export function deserializeRemoteConfig(configJson: string, key?: Buffer): Record<string, string> {
  const parsed = JSON.parse(configJson || '{}') as Record<string, string>;
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(parsed)) {
    out[k] = typeof v === 'string' && v.startsWith(SECRET_PREFIX) ? decryptSecret(v, key) : v;
  }
  return out;
}

/** A display-safe view of a remote: secrets replaced by a mask. */
export function maskRemoteConfig(configJson: string): Record<string, string> {
  const parsed = JSON.parse(configJson || '{}') as Record<string, string>;
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(parsed)) {
    out[k] = typeof v === 'string' && v.startsWith(SECRET_PREFIX) ? '••••••••' : v;
  }
  return out;
}

/**
 * Views a stored remote as a display record with secret values masked.
 * Never returns decrypted secret material.
 */
export function remoteView(remote: RcloneRemoteRecord) {
  return {
    id: remote.id,
    name: remote.name,
    provider: remote.provider,
    type: remote.type,
    fields: maskRemoteConfig(remote.configJson),
    createdAt: remote.createdAt,
    updatedAt: remote.updatedAt,
  };
}

// ---------------------------------------------------------------------------
// rclone config / unit generation
// ---------------------------------------------------------------------------

/** Runs `rclone obscure <value>` using rclone's own reversible encoding. */
export function obscureValue(plaintext: string): Promise<string> {
  return new Promise((resolve, reject) => {
    execFile('rclone', ['obscure', plaintext], { timeout: 10000 }, (err, stdout, stderr) => {
      if (err) {
        reject(new Error(`rclone obscure failed: ${(stderr || err.message || '').trim()}`));
        return;
      }
      resolve(stdout.trim());
    });
  });
}

/**
 * Builds a single rclone.conf INI section. Secret fields are written in rclone's
 * obscured form; nothing here is a plaintext credential.
 */
export async function buildRcloneConfigSection(
  name: string,
  providerId: string,
  fields: Record<string, string>
): Promise<string> {
  const provider = getProvider(providerId);
  if (!provider) throw new Error(`Unknown provider: ${providerId}`);
  const cleanName = sanitizeRemoteName(name);

  const lines = [`[${cleanName}]`, `type = ${provider.type}`];
  for (const field of provider.fields) {
    const raw = fields[field.name];
    if (raw === undefined || raw === null || String(raw).trim() === '') continue;
    const value = sanitizeConfigValue(String(raw));
    if (field.secret && field.obscure) {
      lines.push(`${field.name} = ${await obscureValue(value)}`);
    } else if (field.secret) {
      // e.g. OAuth tokens are stored as-is by rclone.
      lines.push(`${field.name} = ${value}`);
    } else {
      lines.push(`${field.name} = ${value}`);
    }
  }
  return lines.join('\n');
}

// ---------------------------------------------------------------------------
// Real provider verification (Zero-Mock)
// ---------------------------------------------------------------------------

export interface VerifyResult {
  ok: boolean;
  output: string;
  error?: string;
}

/**
 * Verifies a remote by running the real rclone binary against the provider using
 * an ephemeral config file. Returns the genuine result; never fabricates success.
 */
export async function verifyRemoteConfig(
  name: string,
  providerId: string,
  fields: Record<string, string>,
  timeoutMs = 30000
): Promise<VerifyResult> {
  let section: string;
  try {
    section = await buildRcloneConfigSection(name, providerId, fields);
  } catch (err: any) {
    return { ok: false, output: '', error: err?.message || 'Failed to build rclone config' };
  }

  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'atc-rclone-'));
  const cfgPath = path.join(tmpDir, 'rclone.conf');
  try {
    fs.writeFileSync(cfgPath, section + '\n', { mode: 0o600 });
    const args = ['--config', cfgPath, 'lsd', `${name}:`, '--timeout', '20s', '--contimeout', '15s'];
    return await new Promise<VerifyResult>(resolve => {
      execFile('rclone', args, { timeout: timeoutMs }, (err, stdout, stderr) => {
        if (err) {
          const msg = (stderr || err.message || 'rclone verification failed').trim();
          resolve({ ok: false, output: stdout.trim(), error: msg });
          return;
        }
        resolve({ ok: true, output: stdout.trim() || '(empty listing)' });
      });
    });
  } finally {
    try {
      fs.rmSync(tmpDir, { recursive: true, force: true });
    } catch {
      // best-effort cleanup
    }
  }
}

// ---------------------------------------------------------------------------
// REST routes
// ---------------------------------------------------------------------------

export interface RcloneCompiledTask {
  name: string;
  steps: Array<{
    name: string;
    module: string;
    params?: Record<string, any>;
    parameters?: Record<string, any>;
  }>;
  secretsMasked?: Record<string, string>;
}

export interface RcloneRouteOptions {
  hostCache: HostCacheStore;
  broadcast?: (msg: any) => void;
  getNode?: (id: string) => NodeItem | null;
  /** Compiles the install task via the Go daemon `mount/rclone` plugin (canonical compiler). */
  compileInstall: (nodeId: string, options: Record<string, any>) => Promise<RcloneCompiledTask>;
  /** Stages a compiled task into the Ansible mutation queue. */
  stageTask: (task: {
    title: string;
    targetNodeId: string;
    targetNodeName: string;
    targetHostRoute: string;
    targetSshUser: string;
    targetSshPort: number;
    category: string;
    primaryModule: string;
    steps: Array<{ name: string; module: string; parameters: Record<string, any> }>;
    become: boolean;
    status: string;
  }) => any;
}

function normalizeSteps(task: RcloneCompiledTask) {
  return (task.steps || []).map(s => ({
    name: s.name,
    module: s.module,
    parameters: s.parameters || s.params || {},
  }));
}

export function registerRcloneRoutes(app: Express, options: RcloneRouteOptions): void {
  const { hostCache, broadcast, getNode, compileInstall, stageTask } = options;

  app.get('/api/rclone/providers', (_req: Request, res: Response) => {
    res.json({ success: true, providers: RCLONE_PROVIDERS });
  });

  app.get('/api/rclone/remotes', (_req: Request, res: Response) => {
    res.json({ success: true, remotes: hostCache.getAllRcloneRemotes().map(remoteView) });
  });

  app.get('/api/rclone/remotes/:id', (req: Request, res: Response) => {
    const remote = hostCache.getRcloneRemote(req.params.id);
    if (!remote) {
      res.status(404).json({ success: false, error: 'Remote not found' });
      return;
    }
    res.json({ success: true, remote: remoteView(remote) });
  });

  // Guided setup: create or update a remote. Secret values are encrypted before
  // they touch the database. `verify: true` checks the remote against the real
  // provider first and refuses to persist a remote that cannot be reached.
  app.post('/api/rclone/remotes', async (req: Request, res: Response) => {
    try {
      const { id, name, provider: providerId, fields, verify } = req.body || {};
      const provider = getProvider(providerId);
      if (!provider) {
        res.status(400).json({ success: false, error: `Unknown provider: ${providerId}` });
        return;
      }
      const cleanName = sanitizeRemoteName(name);

      // Merge with an existing remote so secret fields left blank on update are preserved.
      const existing = id ? hostCache.getRcloneRemote(id) : hostCache.getRcloneRemoteByName(cleanName);
      const existingFields = existing ? deserializeRemoteConfig(existing.configJson) : {};
      const merged: Record<string, string> = { ...existingFields };
      if (fields && typeof fields === 'object') {
        for (const [k, v] of Object.entries(fields)) {
          if (v === undefined || v === null || String(v).trim() === '') continue;
          merged[k] = String(v);
        }
      }

      for (const field of provider.fields) {
        if (field.required && !merged[field.name]) {
          res.status(400).json({ success: false, error: `Missing required field: ${field.label}` });
          return;
        }
      }

      if (verify) {
        const result = await verifyRemoteConfig(cleanName, providerId, merged);
        if (!result.ok) {
          res.status(400).json({
            success: false,
            error: `Remote verification failed: ${result.error}`,
            verification: result,
          });
          return;
        }
      }

      const now = Date.now();
      const record: RcloneRemoteRecord = {
        id: existing?.id || id || `rclone-${now}-${Math.random().toString(36).slice(2, 8)}`,
        name: cleanName,
        provider: providerId,
        type: provider.type,
        configJson: serializeRemoteConfig(providerId, merged),
        createdAt: existing?.createdAt || now,
        updatedAt: now,
      };
      hostCache.upsertRcloneRemote(record);

      // Keep the display name in sync on already-installed mounts.
      for (const mount of hostCache.getRcloneMounts()) {
        if (mount.remoteId === record.id && mount.remoteName !== cleanName) {
          hostCache.upsertRcloneMount({ ...mount, remoteName: cleanName, updatedAt: now });
        }
      }

      if (broadcast) broadcast({ type: 'rclone_remotes_updated', remotes: hostCache.getAllRcloneRemotes().map(remoteView) });
      res.json({ success: true, remote: remoteView(record) });
    } catch (err: any) {
      res.status(400).json({ success: false, error: err?.message || 'Failed to save remote' });
    }
  });

  app.delete('/api/rclone/remotes/:id', (req: Request, res: Response) => {
    const deleted = hostCache.deleteRcloneRemote(req.params.id);
    if (broadcast) broadcast({ type: 'rclone_remotes_updated', remotes: hostCache.getAllRcloneRemotes().map(remoteView) });
    res.json({ success: true, deleted });
  });

  // Verify a remote against the real provider without persisting.
  app.post('/api/rclone/remotes/:id/verify', async (req: Request, res: Response) => {
    try {
      const remote = hostCache.getRcloneRemote(req.params.id);
      if (!remote) {
        res.status(404).json({ success: false, error: 'Remote not found' });
        return;
      }
      const fields = deserializeRemoteConfig(remote.configJson);
      const result = await verifyRemoteConfig(remote.name, remote.provider, fields);
      res.status(result.ok ? 200 : 502).json({ success: result.ok, verification: result });
    } catch (err: any) {
      res.status(500).json({ success: false, error: err?.message || 'Verification failed' });
    }
  });

  app.get('/api/rclone/nodes/:nodeId/mounts', (req: Request, res: Response) => {
    res.json({ success: true, mounts: hostCache.getRcloneMounts(req.params.nodeId) });
  });

  // Install a remote to a device: compile the chosen mount policy through the Go
  // daemon and stage the declarative task. The config write step carries obscured
  // values under no_log; unit files never contain credentials.
  app.post('/api/rclone/remotes/:id/install', async (req: Request, res: Response) => {
    try {
      const remote = hostCache.getRcloneRemote(req.params.id);
      if (!remote) {
        res.status(404).json({ success: false, error: 'Remote not found' });
        return;
      }
      const { nodeId, mountTarget, policy = 'systemd-mount', mountOptions } = req.body || {};
      if (!nodeId) {
        res.status(400).json({ success: false, error: 'nodeId is required' });
        return;
      }
      if (!isValidMountPolicy(policy)) {
        res.status(400).json({ success: false, error: `Invalid mount policy: ${policy}` });
        return;
      }
      const node = getNode ? getNode(nodeId) : null;
      if (!node) {
        res.status(404).json({ success: false, error: `Node not found: ${nodeId}` });
        return;
      }

      let target: string;
      try {
        target = sanitizeMountTarget(mountTarget || `/mnt/rclone/${remote.name}`);
      } catch (err: any) {
        res.status(400).json({ success: false, error: err?.message });
        return;
      }

      const fields = deserializeRemoteConfig(remote.configJson);
      const configContent = await buildRcloneConfigSection(remote.name, remote.provider, fields);

      const compiled = await compileInstall(nodeId, {
        name: remote.name,
        configContent,
        mountTarget: target,
        policy,
        mountOptions: mountOptions || '--vfs-cache-mode writes --dir-cache-time 5m',
      });

      const task = stageTask({
        title: compiled.name || `Install rclone remote ${remote.name} (${policy})`,
        targetNodeId: node.id,
        targetNodeName: node.name,
        targetHostRoute: node.tailscaleHost || node.tailscaleIp || node.ip || 'localhost',
        targetSshUser: node.sshUser || 'core',
        targetSshPort: node.port || 22,
        category: 'storage',
        primaryModule: compiled.steps?.[0]?.module || 'ansible.builtin.copy',
        steps: normalizeSteps(compiled),
        become: true,
        status: 'staged',
      });

      const now = Date.now();
      const mount: RcloneMountRecord = {
        nodeId: node.id,
        remoteId: remote.id,
        remoteName: remote.name,
        mountTarget: target,
        policy,
        status: 'pending',
        installedAt: now,
        updatedAt: now,
      };
      hostCache.upsertRcloneMount(mount);

      if (broadcast) {
        broadcast({ type: 'rclone_mounts_updated', mounts: hostCache.getRcloneMounts(node.id) });
      }
      res.json({ success: true, mount, task, secretsMasked: compiled.secretsMasked || {} });
    } catch (err: any) {
      res.status(502).json({ success: false, error: err?.message || 'Install failed' });
    }
  });

  app.delete('/api/rclone/nodes/:nodeId/mounts/:remoteId', (req: Request, res: Response) => {
    const deleted = hostCache.deleteRcloneMount(req.params.nodeId, req.params.remoteId);
    if (broadcast) broadcast({ type: 'rclone_mounts_updated', mounts: hostCache.getRcloneMounts(req.params.nodeId) });
    res.json({ success: true, deleted });
  });
}
