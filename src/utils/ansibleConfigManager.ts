import fs from 'node:fs';
import fsp from 'node:fs/promises';
import path from 'node:path';

export interface AnsibleConfigSettings {
  stdoutCallback: 'json' | 'yaml' | 'default';
  forks: number;
  timeout: number;
  hostKeyChecking: boolean;
  pipelining: boolean;
  becomeMethod: 'run0' | 'sudo' | 'doas';
  vaultPasswordFile?: string;
}

export const DEFAULT_ANSIBLE_CONFIG: AnsibleConfigSettings = {
  stdoutCallback: 'json',
  forks: 10,
  timeout: 30,
  hostKeyChecking: false,
  pipelining: true,
  becomeMethod: 'run0',
};

/**
 * Validates that vaultPasswordFile does not contain newlines or control characters
 * that could be used for INI injection attacks.
 */
export function validateVaultPasswordFile(pathStr?: string): boolean {
  if (pathStr === undefined || pathStr === null || pathStr === '') return true;
  if (typeof pathStr !== 'string') return false;
  if (/[\r\n\x00-\x1F\x7F]/.test(pathStr)) return false;
  return true;
}

function parseBoolean(val: string, fallback: boolean): boolean {
  const norm = val.trim().toLowerCase();
  if (['true', 'yes', '1', 'on'].includes(norm)) return true;
  if (['false', 'no', '0', 'off'].includes(norm)) return false;
  return fallback;
}

function parseInteger(val: string, fallback: number, min = 1, max = 500): number {
  const parsed = parseInt(val.trim(), 10);
  if (isNaN(parsed)) return fallback;
  return Math.max(min, Math.min(max, parsed));
}

function parseStdoutCallback(val: string): 'json' | 'yaml' | 'default' {
  const norm = val.trim().toLowerCase();
  if (norm === 'json' || norm === 'yaml' || norm === 'default') {
    return norm;
  }
  return 'json';
}

function parseBecomeMethod(val: string): 'run0' | 'sudo' | 'doas' {
  const norm = val.trim().toLowerCase();
  if (norm === 'run0' || norm === 'community.general.run0') return 'run0';
  if (norm === 'sudo' || norm === 'ansible.builtin.sudo') return 'sudo';
  if (norm === 'doas' || norm === 'community.general.doas') return 'doas';
  return 'run0';
}

/**
 * Parses INI formatted ansible.cfg string into typed AnsibleConfigSettings.
 * Automatically strips inline comments (# or ;) from values.
 */
export function parseAnsibleCfg(iniContent: string): AnsibleConfigSettings {
  const settings: AnsibleConfigSettings = { ...DEFAULT_ANSIBLE_CONFIG };
  if (!iniContent || typeof iniContent !== 'string') {
    return settings;
  }

  const lines = iniContent.split(/\r?\n/);
  let currentSection = '';

  for (const rawLine of lines) {
    const line = rawLine.trim();
    if (!line || line.startsWith('#') || line.startsWith(';')) {
      continue;
    }

    const sectionMatch = line.match(/^\[(.*?)\]$/);
    if (sectionMatch) {
      currentSection = sectionMatch[1].trim().toLowerCase();
      continue;
    }

    const eqIdx = line.indexOf('=');
    if (eqIdx === -1) continue;

    const key = line.slice(0, eqIdx).trim().toLowerCase();
    const rawValue = line.slice(eqIdx + 1);
    const value = rawValue.split(/[#;]/)[0].trim();

    if (key === 'stdout_callback') {
      settings.stdoutCallback = parseStdoutCallback(value);
    } else if (key === 'forks') {
      settings.forks = parseInteger(value, 10, 1, 500);
    } else if (key === 'timeout') {
      settings.timeout = parseInteger(value, 30, 5, 600);
    } else if (key === 'host_key_checking') {
      settings.hostKeyChecking = parseBoolean(value, false);
    } else if (key === 'pipelining') {
      settings.pipelining = parseBoolean(value, true);
    } else if (key === 'become_method') {
      settings.becomeMethod = parseBecomeMethod(value);
    } else if (key === 'vault_password_file') {
      if (validateVaultPasswordFile(value)) {
        settings.vaultPasswordFile = value;
      }
    }
  }

  return settings;
}

/**
 * Serializes typed AnsibleConfigSettings into compliant ansible.cfg INI format.
 * Preserves comments and existing structure when baseContent is provided.
 * Throws an error if vaultPasswordFile fails sanitization (INI injection prevention).
 */
export function serializeAnsibleCfg(
  settings: AnsibleConfigSettings,
  baseContent?: string
): string {
  if (settings.vaultPasswordFile && !validateVaultPasswordFile(settings.vaultPasswordFile)) {
    throw new Error('Invalid vaultPasswordFile: newlines and control characters are prohibited to prevent INI injection');
  }

  if (!baseContent || typeof baseContent !== 'string' || !baseContent.trim()) {
    const lines = [
      '[defaults]',
      `stdout_callback = ${settings.stdoutCallback}`,
      `forks = ${settings.forks}`,
      `timeout = ${settings.timeout}`,
      `host_key_checking = ${settings.hostKeyChecking ? 'True' : 'False'}`,
      `pipelining = ${settings.pipelining ? 'True' : 'False'}`,
    ];
    if (settings.vaultPasswordFile) {
      lines.push(`vault_password_file = ${settings.vaultPasswordFile}`);
    }
    lines.push('');
    lines.push('[privilege_escalation]');
    lines.push(`become_method = ${settings.becomeMethod}`);
    lines.push('');
    return lines.join('\n');
  }

  const lines = baseContent.split(/\r?\n/);
  const updatedLines: string[] = [];
  let currentSection = '';
  const handledKeys = new Set<string>();
  const seenSections = new Set<string>();

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const trimmed = line.trim();

    const sectionMatch = trimmed.match(/^\[(.*?)\]$/);
    if (sectionMatch) {
      if (currentSection === 'defaults') {
        appendMissingDefaults(updatedLines, handledKeys, settings);
      } else if (currentSection === 'privilege_escalation') {
        appendMissingPrivilegeEscalation(updatedLines, handledKeys, settings);
      }

      currentSection = sectionMatch[1].trim().toLowerCase();
      seenSections.add(currentSection);
      updatedLines.push(line);
      continue;
    }

    if (trimmed.startsWith('#') || trimmed.startsWith(';') || !trimmed) {
      updatedLines.push(line);
      continue;
    }

    const eqIdx = trimmed.indexOf('=');
    if (eqIdx !== -1) {
      const key = trimmed.slice(0, eqIdx).trim().toLowerCase();
      if (key === 'stdout_callback') {
        updatedLines.push(`stdout_callback = ${settings.stdoutCallback}`);
        handledKeys.add('stdout_callback');
        continue;
      }
      if (key === 'forks') {
        updatedLines.push(`forks = ${settings.forks}`);
        handledKeys.add('forks');
        continue;
      }
      if (key === 'timeout') {
        updatedLines.push(`timeout = ${settings.timeout}`);
        handledKeys.add('timeout');
        continue;
      }
      if (key === 'host_key_checking') {
        updatedLines.push(`host_key_checking = ${settings.hostKeyChecking ? 'True' : 'False'}`);
        handledKeys.add('host_key_checking');
        continue;
      }
      if (key === 'pipelining') {
        updatedLines.push(`pipelining = ${settings.pipelining ? 'True' : 'False'}`);
        handledKeys.add('pipelining');
        continue;
      }
      if (key === 'become_method') {
        updatedLines.push(`become_method = ${settings.becomeMethod}`);
        handledKeys.add('become_method');
        continue;
      }
      if (key === 'vault_password_file') {
        if (settings.vaultPasswordFile) {
          updatedLines.push(`vault_password_file = ${settings.vaultPasswordFile}`);
        }
        handledKeys.add('vault_password_file');
        continue;
      }
    }

    updatedLines.push(line);
  }

  // Handle keys for the trailing section
  if (currentSection === 'defaults') {
    appendMissingDefaults(updatedLines, handledKeys, settings);
  } else if (currentSection === 'privilege_escalation') {
    appendMissingPrivilegeEscalation(updatedLines, handledKeys, settings);
  }

  // If defaults section was never seen, add it
  if (!seenSections.has('defaults')) {
    updatedLines.push('');
    updatedLines.push('[defaults]');
    appendMissingDefaults(updatedLines, handledKeys, settings);
  }

  // If privilege_escalation section was never seen, add it
  if (!seenSections.has('privilege_escalation')) {
    updatedLines.push('');
    updatedLines.push('[privilege_escalation]');
    appendMissingPrivilegeEscalation(updatedLines, handledKeys, settings);
  }

  return updatedLines.join('\n');
}

function appendMissingDefaults(
  lines: string[],
  handledKeys: Set<string>,
  settings: AnsibleConfigSettings
) {
  if (!handledKeys.has('stdout_callback')) {
    lines.push(`stdout_callback = ${settings.stdoutCallback}`);
    handledKeys.add('stdout_callback');
  }
  if (!handledKeys.has('forks')) {
    lines.push(`forks = ${settings.forks}`);
    handledKeys.add('forks');
  }
  if (!handledKeys.has('timeout')) {
    lines.push(`timeout = ${settings.timeout}`);
    handledKeys.add('timeout');
  }
  if (!handledKeys.has('host_key_checking')) {
    lines.push(`host_key_checking = ${settings.hostKeyChecking ? 'True' : 'False'}`);
    handledKeys.add('host_key_checking');
  }
  if (!handledKeys.has('pipelining')) {
    lines.push(`pipelining = ${settings.pipelining ? 'True' : 'False'}`);
    handledKeys.add('pipelining');
  }
  if (settings.vaultPasswordFile && !handledKeys.has('vault_password_file')) {
    lines.push(`vault_password_file = ${settings.vaultPasswordFile}`);
    handledKeys.add('vault_password_file');
  }
}

function appendMissingPrivilegeEscalation(
  lines: string[],
  handledKeys: Set<string>,
  settings: AnsibleConfigSettings
) {
  if (!handledKeys.has('become_method')) {
    lines.push(`become_method = ${settings.becomeMethod}`);
    handledKeys.add('become_method');
  }
}

/**
 * Loads ansible.cfg from filesystem asynchronously using fs.promises.
 * If the file does not exist, creates a default ansible.cfg and returns DEFAULT_ANSIBLE_CONFIG.
 */
export async function loadAnsibleConfig(cfgPath?: string): Promise<AnsibleConfigSettings> {
  const targetPath = cfgPath || path.resolve(process.cwd(), 'ansible/ansible.cfg');
  try {
    let content: string;
    try {
      content = await fsp.readFile(targetPath, 'utf-8');
    } catch (err: any) {
      if (err?.code === 'ENOENT') {
        const defaultSettings = { ...DEFAULT_ANSIBLE_CONFIG };
        const dir = path.dirname(targetPath);
        await fsp.mkdir(dir, { recursive: true });
        const serialized = serializeAnsibleCfg(defaultSettings);
        await fsp.writeFile(targetPath, serialized, 'utf-8');
        return defaultSettings;
      }
      throw err;
    }
    return parseAnsibleCfg(content);
  } catch (_err) {
    return { ...DEFAULT_ANSIBLE_CONFIG };
  }
}

/**
 * Safely saves updated Ansible settings to ansible.cfg asynchronously using fs.promises,
 * merging with existing INI content if present.
 */
export async function saveAnsibleConfig(
  settings: AnsibleConfigSettings,
  cfgPath?: string
): Promise<boolean> {
  const targetPath = cfgPath || path.resolve(process.cwd(), 'ansible/ansible.cfg');
  try {
    const dir = path.dirname(targetPath);
    await fsp.mkdir(dir, { recursive: true });
    let baseContent: string | undefined = undefined;
    try {
      baseContent = await fsp.readFile(targetPath, 'utf-8');
    } catch (err: any) {
      if (err?.code !== 'ENOENT') throw err;
    }
    const serialized = serializeAnsibleCfg(settings, baseContent);
    await fsp.writeFile(targetPath, serialized, 'utf-8');
    return true;
  } catch (_err) {
    return false;
  }
}
