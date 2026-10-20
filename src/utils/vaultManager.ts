import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import crypto from 'node:crypto';
import { spawn } from 'node:child_process';
import type { Express, Request, Response } from 'express';
import { parseAnsibleCfg } from './ansibleConfigManager.ts';
import { getAnsibleExecutionEnv } from './ansibleVenvManager.ts';
import type { VaultAuditEvent } from '../types/index.ts';

export interface VaultPasswordSource {
  source: 'file' | 'env' | 'ask_password';
  hasPassword: boolean;
  password?: string;
  filePath?: string;
}

export interface ResolveVaultPasswordOptions {
  vaultPasswordFile?: string;
  ansibleCfgPath?: string;
  env?: NodeJS.ProcessEnv;
}

/**
 * Resolves the vault password using a strict 3-tier hierarchy:
 * 1. vault_password_file (explicit option or parsed from ansible.cfg)
 * 2. ANSIBLE_VAULT_PASSWORD or ANSIBLE_VAULT_PASSWORD_FILE environment variable
 * 3. Interactive ask_password prompt fallback (hasPassword: false)
 */
export function resolveVaultPasswordSource(options?: ResolveVaultPasswordOptions): VaultPasswordSource {
  // Tier 1: Check vaultPasswordFile (explicit or from ansible.cfg)
  let candidatePassFile = options?.vaultPasswordFile;

  if (!candidatePassFile) {
    const defaultCfgPath = options?.ansibleCfgPath || path.resolve(process.cwd(), 'ansible/ansible.cfg');
    if (fs.existsSync(defaultCfgPath)) {
      try {
        const content = fs.readFileSync(defaultCfgPath, 'utf8');
        const cfg = parseAnsibleCfg(content);
        if (cfg.vaultPasswordFile) {
          candidatePassFile = cfg.vaultPasswordFile;
        }
      } catch {
        // ignore config read error, fall through
      }
    }
  }

  if (candidatePassFile) {
    let resolvedPath = path.isAbsolute(candidatePassFile)
      ? candidatePassFile
      : path.resolve(process.cwd(), candidatePassFile);

    if (!fs.existsSync(resolvedPath) && !path.isAbsolute(candidatePassFile)) {
      const ansibleDirFallback = path.resolve(process.cwd(), 'ansible', candidatePassFile);
      if (fs.existsSync(ansibleDirFallback)) {
        resolvedPath = ansibleDirFallback;
      }
    }

    if (fs.existsSync(resolvedPath)) {
      try {
        const password = fs.readFileSync(resolvedPath, 'utf8').trim();
        return {
          source: 'file',
          hasPassword: true,
          password,
          filePath: resolvedPath,
        };
      } catch {
        // file could not be read, fall through
      }
    }
  }

  // Tier 2: Check environment variables
  const env = options?.env ?? process.env;

  if (env.ANSIBLE_VAULT_PASSWORD && env.ANSIBLE_VAULT_PASSWORD.length > 0) {
    return {
      source: 'env',
      hasPassword: true,
      password: env.ANSIBLE_VAULT_PASSWORD,
    };
  }

  if (env.ANSIBLE_VAULT_PASSWORD_FILE) {
    const resolvedEnvPath = path.isAbsolute(env.ANSIBLE_VAULT_PASSWORD_FILE)
      ? env.ANSIBLE_VAULT_PASSWORD_FILE
      : path.resolve(process.cwd(), env.ANSIBLE_VAULT_PASSWORD_FILE);

    if (fs.existsSync(resolvedEnvPath)) {
      try {
        const password = fs.readFileSync(resolvedEnvPath, 'utf8').trim();
        return {
          source: 'file',
          hasPassword: true,
          password,
          filePath: resolvedEnvPath,
        };
      } catch {
        // file could not be read, fall through
      }
    }
  }

  // Tier 3: Fall back to ask_password
  return {
    source: 'ask_password',
    hasPassword: false,
  };
}

/** Persistent audit trail for vault decryptions (implemented by the SQLite store). */
export interface VaultAuditStore {
  recordVaultAuditEvent(event: VaultAuditEvent): void;
  getVaultAuditEvents(filter?: { variableName?: string; host?: string; limit?: number }): VaultAuditEvent[];
}

/**
 * Extracts the vault variable name from a `name: !vault |` block so the audit
 * trail can name the secret without ever touching its plaintext.
 */
export function extractVaultVariableName(vaultBlock: string): string | null {
  const match = vaultBlock.match(/^\s*([A-Za-z0-9_.\-]+)\s*:\s*!vault\s*\|/m);
  return match ? match[1] : null;
}

/**
 * Extracts raw $ANSIBLE_VAULT payload lines from a YAML or text string,
 * stripping variable prefixes or YAML block tags.
 */
export function extractRawVaultPayload(input: string): string {
  const vaultIdx = input.indexOf('$ANSIBLE_VAULT;');
  if (vaultIdx === -1) {
    throw new Error('INVALID_VAULT_PAYLOAD: Missing $ANSIBLE_VAULT header');
  }

  const remaining = input.slice(vaultIdx);
  const lines = remaining.split(/\r?\n/);
  const resultLines: string[] = [];

  for (let i = 0; i < lines.length; i++) {
    const trimmed = lines[i].trim();
    if (i === 0) {
      resultLines.push(trimmed);
      continue;
    }
    if (/^[0-9a-fA-F]+$/.test(trimmed)) {
      resultLines.push(trimmed);
    } else {
      break;
    }
  }

  return resultLines.join('\n');
}

/**
 * Encrypts a plaintext string into a standard YAML !vault block using ansible-vault encrypt_string.
 * Streams password via stdin so secret values never appear in process arguments or logs.
 */
export async function encryptVaultString(
  plaintext: string,
  varName?: string,
  options?: {
    password?: string;
    passwordFile?: string;
    ansibleCfgPath?: string;
    env?: Record<string, string>;
  }
): Promise<string> {
  let password = options?.password;

  if (!password) {
    const resolved = resolveVaultPasswordSource({
      vaultPasswordFile: options?.passwordFile,
      ansibleCfgPath: options?.ansibleCfgPath,
      env: options?.env as NodeJS.ProcessEnv,
    });
    if (resolved.hasPassword && resolved.password) {
      password = resolved.password;
    }
  }

  if (!password) {
    throw new Error('PASSWORD_REQUIRED: Vault password source required for encryption');
  }

  const executionEnv = getAnsibleExecutionEnv(options?.env || process.env);
  const targetVarName = varName || 'secret';
  const id = crypto.randomUUID ? crypto.randomUUID() : Math.random().toString(36).substring(2);
  const tempPassFile = path.resolve(os.tmpdir(), `.vault-pass-${id}`);

  try {
    fs.writeFileSync(tempPassFile, password, { mode: 0o600 });

    return await new Promise<string>((resolve, reject) => {
      const proc = spawn(
        'ansible-vault',
        [
          'encrypt_string',
          '--vault-password-file',
          tempPassFile,
          '--stdin-name',
          targetVarName,
        ],
        {
          env: executionEnv,
          stdio: ['pipe', 'pipe', 'pipe'],
        }
      );

      let stdout = '';
      let stderr = '';

      proc.stdout.on('data', (chunk) => {
        stdout += chunk.toString();
      });

      proc.stderr.on('data', (chunk) => {
        stderr += chunk.toString();
      });

      proc.on('error', (err) => {
        reject(err);
      });

      proc.on('close', (code) => {
        if (code !== 0) {
          reject(new Error(`ansible-vault encrypt_string failed with exit code ${code}: ${stderr.trim()}`));
          return;
        }

        const vaultIdx = stdout.indexOf('!vault |');
        if (vaultIdx === -1) {
          reject(new Error(`Failed to parse encrypted vault block: ${stdout}`));
          return;
        }

        const before = stdout.substring(0, vaultIdx);
        const lastNewline = before.lastIndexOf('\n');
        const start = lastNewline === -1 ? 0 : lastNewline + 1;
        resolve(stdout.substring(start).trim());
      });

      proc.stdin.write(plaintext);
      proc.stdin.end();
    });
  } finally {
    try {
      if (fs.existsSync(tempPassFile)) {
        fs.unlinkSync(tempPassFile);
      }
    } catch {
      // ignore cleanup errors
    }
  }
}

/**
 * Decrypts an Ansible Vault payload back to plaintext using an ephemeral temporary file.
 * Cleans up temporary files in all execution paths.
 */
export async function decryptVaultString(
  vaultPayload: string,
  options?: {
    password?: string;
    passwordFile?: string;
    ansibleCfgPath?: string;
    env?: Record<string, string>;
  }
): Promise<string> {
  const rawPayload = extractRawVaultPayload(vaultPayload);

  let password = options?.password;

  if (!password) {
    const resolved = resolveVaultPasswordSource({
      vaultPasswordFile: options?.passwordFile,
      ansibleCfgPath: options?.ansibleCfgPath,
      env: options?.env as NodeJS.ProcessEnv,
    });
    if (resolved.hasPassword && resolved.password) {
      password = resolved.password;
    }
  }

  if (!password) {
    throw new Error('PASSWORD_REQUIRED: Vault password source required for decryption');
  }

  const executionEnv = getAnsibleExecutionEnv(options?.env || process.env);
  const id = crypto.randomUUID ? crypto.randomUUID() : Math.random().toString(36).substring(2);
  const tempFile = path.resolve(os.tmpdir(), `.vault-decrypt-${id}`);

  try {
    fs.writeFileSync(tempFile, rawPayload, { mode: 0o600 });

    return await new Promise<string>((resolve, reject) => {
      // Pipe through bash cat to avoid Node socketpair 0o140777 permission check in ansible-vault
      const proc = spawn(
        'bash',
        [
          '-c',
          'cat | ansible-vault decrypt --vault-password-file /dev/stdin "$1"',
          '_',
          tempFile,
        ],
        {
          env: executionEnv,
          stdio: ['pipe', 'pipe', 'pipe'],
        }
      );

      let stdout = '';
      let stderr = '';

      proc.stdout.on('data', (chunk) => {
        stdout += chunk.toString();
      });

      proc.stderr.on('data', (chunk) => {
        stderr += chunk.toString();
      });

      proc.on('error', (err) => {
        reject(err);
      });

      proc.on('close', (code) => {
        if (code !== 0) {
          reject(new Error(`ansible-vault decrypt failed: ${stderr.trim() || stdout.trim() || 'Decryption failed'}`));
          return;
        }

        try {
          const decrypted = fs.readFileSync(tempFile, 'utf8');
          resolve(decrypted);
        } catch (readErr) {
          reject(readErr);
        }
      });

      proc.stdin.write(password + '\n');
      proc.stdin.end();
    });
  } finally {
    if (fs.existsSync(tempFile)) {
      try {
        fs.unlinkSync(tempFile);
      } catch {
        // ignore cleanup error
      }
    }
  }
}

export interface RegisterVaultRoutesOptions {
  /** Persistent audit sink. When absent, decryptions are not audited (tests). */
  auditStore?: VaultAuditStore;
  /** Fallback actor label when a request does not identify one. */
  actor?: string;
  /** Fallback target host when a request does not identify one. */
  host?: string;
  /** Fallback action label when a request does not identify one. */
  action?: string;
}

/**
 * Registers REST API endpoints for Ansible Vault management:
 * - GET /api/vault/status: returns current password resolution status
 * - GET /api/vault/audit: newest-first decryption audit trail (filterable)
 * - POST /api/vault/encrypt: encrypts plaintext into vault block
 * - POST /api/vault/decrypt: decrypts vault block back to plaintext and records
 *   exactly one audit row (variable name + metadata, never the plaintext)
 */
export function registerVaultRoutes(app: Express, options: RegisterVaultRoutesOptions = {}): void {
  const { auditStore } = options;
  const defaultActor = options.actor || process.env.SUDO_USER || process.env.USER || 'atc-coordinator';
  const defaultHost = options.host || 'n/a';
  const defaultAction = options.action || 'POST /api/vault/decrypt';

  const safeActor = (value: unknown): string => {
    const text = typeof value === 'string' ? value.trim() : '';
    return text || defaultActor;
  };

  app.get('/api/vault/status', (_req: Request, res: Response) => {
    try {
      const source = resolveVaultPasswordSource();
      res.json({
        success: true,
        hasPasswordSource: source.hasPassword,
        source: source.source,
      });
    } catch (err: any) {
      res.status(500).json({ success: false, error: err?.message || 'Failed to resolve vault status' });
    }
  });

  app.get('/api/vault/audit', (req: Request, res: Response) => {
    try {
      if (!auditStore) {
        res.json({ success: true, events: [], total: 0 });
        return;
      }
      const variableName = typeof req.query.variable === 'string' ? req.query.variable : undefined;
      const host = typeof req.query.host === 'string' ? req.query.host : undefined;
      const limitParam = typeof req.query.limit === 'string' ? Number(req.query.limit) : undefined;

      const events = auditStore.getVaultAuditEvents({
        variableName,
        host,
        limit: Number.isFinite(limitParam) ? limitParam : undefined,
      });
      res.json({ success: true, events, total: events.length });
    } catch (err: any) {
      res.status(500).json({ success: false, error: err?.message || 'Failed to read vault audit trail' });
    }
  });

  app.post('/api/vault/encrypt', async (req: Request, res: Response) => {
    try {
      const { plaintext, varName, password } = req.body || {};
      if (plaintext === undefined || typeof plaintext !== 'string') {
        res.status(400).json({ success: false, error: 'Missing or invalid plaintext' });
        return;
      }

      const vaultBlock = await encryptVaultString(plaintext, varName, { password });
      res.json({ success: true, vaultBlock });
    } catch (err: any) {
      res.status(400).json({ success: false, error: err?.message || 'Encryption failed' });
    }
  });

  app.post('/api/vault/decrypt', async (req: Request, res: Response) => {
    try {
      const { vaultBlock, password, varName, host, actor, action } = req.body || {};
      if (!vaultBlock || typeof vaultBlock !== 'string') {
        res.status(400).json({ success: false, error: 'Missing or invalid vaultBlock' });
        return;
      }

      const plaintext = await decryptVaultString(vaultBlock, { password });

      // Audit the decryption: name + metadata only, never the plaintext.
      if (auditStore) {
        try {
          const passwordSource = password ? 'inline' : resolveVaultPasswordSource().source;
          auditStore.recordVaultAuditEvent({
            id: crypto.randomUUID ? crypto.randomUUID() : `${Date.now()}-${Math.random().toString(36).slice(2)}`,
            timestamp: Date.now(),
            variableName:
              (typeof varName === 'string' && varName.trim()) ||
              extractVaultVariableName(vaultBlock) ||
              'unnamed',
            actor: safeActor(actor),
            host: (typeof host === 'string' && host.trim()) || defaultHost,
            action: (typeof action === 'string' && action.trim()) || defaultAction,
            passwordSource,
          });
        } catch (auditErr) {
          // Never leak the plaintext through an error path; surface the failure instead.
          console.error('[vault-audit] failed to record decryption event:', (auditErr as Error)?.message);
        }
      }

      res.json({ success: true, plaintext });
    } catch (err: any) {
      res.status(400).json({ success: false, error: err?.message || 'Decryption failed' });
    }
  });
}
