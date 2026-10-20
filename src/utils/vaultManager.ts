import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import crypto from 'node:crypto';
import { spawn } from 'node:child_process';
import type { Express, Request, Response } from 'express';
import { parseAnsibleCfg } from './ansibleConfigManager.ts';
import { getAnsibleExecutionEnv } from './ansibleVenvManager.ts';
import type { VaultAuditEvent, VaultVariable } from '../types/index.ts';
import { isValidVaultVariableName, maskVaultValue } from './vaultMasking.ts';

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
 * Persistent store for named Ansible Vault variables. Only AES256 ciphertext is
 * kept here; implementations must not have a plaintext column or field.
 */
export interface VaultVariableStore {
  listVaultVariables(): VaultVariable[];
  getVaultVariableBlock(name: string): string | null;
  upsertVaultVariable(name: string, vaultBlock: string): void;
  deleteVaultVariable(name: string): boolean;
}

/** Wire-safe view of a vault variable: metadata plus the mask, never the value. */
export interface VaultVariableView extends VaultVariable {
  /** `•••••••• (vault: <name>)` — the only value representation the API emits. */
  maskedValue: string;
}

/**
 * Renders one stored entry as a YAML `name: !vault |` block. The payload is
 * ciphertext, so building the file never requires the vault password.
 */
export function formatVaultEntry(name: string, payload: string): string {
  const indented = payload
    .split(/\r?\n/)
    .map(line => `  ${line}`)
    .join('\n');
  return `${name}: !vault |\n${indented}`;
}

export interface MaterializedVaultVarsFile {
  /** Path to the ephemeral YAML file passed to ansible as `--extra-vars @path`. */
  filePath: string;
  cleanup: () => void;
}

/**
 * Materializes every stored vault variable into an ephemeral, owner-only YAML
 * vars file so playbooks can consume the vault store without copying secrets
 * into the repository. Returns null when the store is empty or absent.
 */
export function materializeVaultVarsFile(
  store: VaultVariableStore | undefined
): MaterializedVaultVarsFile | null {
  if (!store) return null;

  let variables: VaultVariable[];
  try {
    variables = store.listVaultVariables();
  } catch {
    return null;
  }
  if (variables.length === 0) return null;

  const entries: string[] = [];
  for (const variable of variables) {
    const block = store.getVaultVariableBlock(variable.name);
    if (!block) continue;
    try {
      entries.push(formatVaultEntry(variable.name, extractRawVaultPayload(block)));
    } catch (err) {
      // Name-only diagnostic: the ciphertext/plaintext never reaches the log.
      console.error(
        `[vault] skipping malformed vault entry "${variable.name}": ${(err as Error)?.message}`
      );
    }
  }
  if (entries.length === 0) return null;

  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'atc-vault-vars-'));
  const filePath = path.join(dir, 'vault.yml');
  const header =
    '# Generated by Air Traffic Control from the SQLite vault store.\n' +
    '# Every value below is ansible-vault AES256 ciphertext.\n';
  fs.writeFileSync(filePath, `---\n${header}${entries.join('\n')}\n`, { mode: 0o600 });
  try {
    fs.chmodSync(filePath, 0o600);
  } catch {
    // best effort: the file was created with 0o600 already
  }

  return {
    filePath,
    cleanup: () => {
      try {
        fs.rmSync(dir, { recursive: true, force: true });
      } catch {
        // ignore cleanup errors
      }
    },
  };
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
 * Strips conflicting Ansible vault environment variables from the execution
 * environment when passing --vault-password-file on the CLI.
 *
 * If ANSIBLE_VAULT_PASSWORD_FILE, ANSIBLE_VAULT_IDENTITY_LIST, DEFAULT_VAULT_PASSWORD_FILE,
 * or ANSIBLE_CONFIG are present in the child process env, ansible-vault sees multiple
 * default vault-ids and fails with exit code 5 ("default,default are available to encrypt").
 */
export function getVaultExecutionEnv(baseEnv?: NodeJS.ProcessEnv): Record<string, string> {
  const executionEnv = getAnsibleExecutionEnv(baseEnv);
  delete executionEnv.ANSIBLE_VAULT_PASSWORD_FILE;
  delete executionEnv.ANSIBLE_VAULT_IDENTITY_LIST;
  delete executionEnv.DEFAULT_VAULT_PASSWORD_FILE;
  delete executionEnv.ANSIBLE_CONFIG;
  return executionEnv;
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

  const executionEnv = getVaultExecutionEnv(options?.env || process.env);
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

  const executionEnv = getVaultExecutionEnv(options?.env || process.env);
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
  /** Persistent vault variable store (SQLite). Absent = variable routes disabled. */
  variableStore?: VaultVariableStore;
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
  const { auditStore, variableStore } = options;
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

  // ---------------------------------------------------------------
  // Named vault variables (full-page editor backing store)
  // Every response is metadata + mask only. Plaintext and ciphertext
  // never leave the process through these endpoints.
  // ---------------------------------------------------------------

  const toVariableView = (variable: VaultVariable): VaultVariableView => ({
    name: variable.name,
    createdAt: variable.createdAt,
    updatedAt: variable.updatedAt,
    maskedValue: maskVaultValue(variable.name),
  });

  const requireVariableStore = (res: Response): VaultVariableStore | null => {
    if (!variableStore) {
      res.status(503).json({ success: false, error: 'Vault variable store is not configured' });
      return null;
    }
    return variableStore;
  };

  const readBodyPassword = (body: any): string | undefined => {
    const password = body?.password;
    return typeof password === 'string' && password.length > 0 ? password : undefined;
  };

  const findVariable = (store: VaultVariableStore, name: string): VaultVariable | null =>
    store.listVaultVariables().find(variable => variable.name === name) || null;

  app.get('/api/vault/variables', (_req: Request, res: Response) => {
    const store = requireVariableStore(res);
    if (!store) return;
    try {
      const variables = store.listVaultVariables().map(toVariableView);
      res.json({ success: true, variables, total: variables.length });
    } catch (err: any) {
      res.status(500).json({ success: false, error: err?.message || 'Failed to list vault variables' });
    }
  });

  app.post('/api/vault/variables', async (req: Request, res: Response) => {
    const store = requireVariableStore(res);
    if (!store) return;
    try {
      const { name, value } = req.body || {};
      if (!isValidVaultVariableName(name)) {
        res.status(400).json({
          success: false,
          error: 'Invalid variable name: must match [A-Za-z_][A-Za-z0-9_]*',
        });
        return;
      }
      if (typeof value !== 'string' || value.length === 0) {
        res.status(400).json({ success: false, error: 'A non-empty value is required' });
        return;
      }
      if (store.getVaultVariableBlock(name)) {
        res.status(409).json({ success: false, error: `Vault variable "${name}" already exists` });
        return;
      }

      const vaultBlock = await encryptVaultString(value, name, { password: readBodyPassword(req.body) });
      store.upsertVaultVariable(name, vaultBlock);

      const stored = findVariable(store, name) || { name, createdAt: Date.now(), updatedAt: Date.now() };
      res.status(201).json({ success: true, variable: toVariableView(stored) });
    } catch (err: any) {
      res.status(400).json({ success: false, error: err?.message || 'Failed to create vault variable' });
    }
  });

  app.put('/api/vault/variables/:name', async (req: Request, res: Response) => {
    const store = requireVariableStore(res);
    if (!store) return;
    try {
      const currentName = String(req.params.name || '');
      if (!isValidVaultVariableName(currentName)) {
        res.status(400).json({ success: false, error: 'Invalid variable name in path' });
        return;
      }
      const existingBlock = store.getVaultVariableBlock(currentName);
      if (!existingBlock) {
        res.status(404).json({ success: false, error: `Vault variable "${currentName}" not found` });
        return;
      }

      const { value, newName } = req.body || {};
      let targetName = currentName;
      if (newName !== undefined && newName !== currentName) {
        if (!isValidVaultVariableName(newName)) {
          res.status(400).json({
            success: false,
            error: 'Invalid new name: must match [A-Za-z_][A-Za-z0-9_]*',
          });
          return;
        }
        if (store.getVaultVariableBlock(newName)) {
          res.status(409).json({ success: false, error: `Vault variable "${newName}" already exists` });
          return;
        }
        targetName = newName;
      }

      let vaultBlock: string;
      if (value === undefined) {
        // Rename only: re-frame the existing ciphertext under the new name.
        vaultBlock = formatVaultEntry(targetName, extractRawVaultPayload(existingBlock));
      } else {
        if (typeof value !== 'string' || value.length === 0) {
          res.status(400).json({ success: false, error: 'A non-empty value is required' });
          return;
        }
        vaultBlock = await encryptVaultString(value, targetName, {
          password: readBodyPassword(req.body),
        });
      }

      store.upsertVaultVariable(targetName, vaultBlock);
      if (targetName !== currentName) {
        store.deleteVaultVariable(currentName);
      }

      const stored = findVariable(store, targetName) || {
        name: targetName,
        createdAt: Date.now(),
        updatedAt: Date.now(),
      };
      res.json({ success: true, variable: toVariableView(stored) });
    } catch (err: any) {
      res.status(400).json({ success: false, error: err?.message || 'Failed to update vault variable' });
    }
  });

  app.delete('/api/vault/variables/:name', (req: Request, res: Response) => {
    const store = requireVariableStore(res);
    if (!store) return;
    try {
      const name = String(req.params.name || '');
      const deleted = store.deleteVaultVariable(name);
      if (!deleted) {
        res.status(404).json({ success: false, error: `Vault variable "${name}" not found` });
        return;
      }
      res.json({ success: true, deleted: true, name });
    } catch (err: any) {
      res.status(500).json({ success: false, error: err?.message || 'Failed to delete vault variable' });
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
