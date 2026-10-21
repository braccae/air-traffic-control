import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { getAnsibleVenvPath } from './ansibleVenvManager.ts';
import type { AnsibleModuleSpec, ModuleCatalogEntry, ModuleSpecOption } from './playbookVisualModel.ts';

/**
 * Real Ansible module metadata for the visual playbook editor.
 *
 * Specs come from `ansible-doc -j <module>` executed with the coordinator's
 * persistent Ansible virtualenv — the same install that runs the playbooks.
 * Zero-Mock Policy (AGENTS.md §2): if ansible-doc is unavailable or the module
 * does not exist, the caller receives the real error, never a canned field list.
 */

const SAFE_MODULE_NAME = /^[A-Za-z0-9_][A-Za-z0-9_.]*$/;

export function isSafeModuleName(name: unknown): name is string {
  return typeof name === 'string' && SAFE_MODULE_NAME.test(name) && !name.includes('..');
}

export function getAnsibleDocBinary(): string {
  return process.env.ANSIBLE_DOC_PATH || path.join(getAnsibleVenvPath(), 'bin', 'ansible-doc');
}

interface CacheEntry<T> {
  value: T;
  expiresAt: number;
}

const CATALOG_TTL_MS = 10 * 60 * 1000;
const SPEC_TTL_MS = 60 * 60 * 1000;

let catalogCache: CacheEntry<ModuleCatalogEntry[]> | null = null;
const specCache = new Map<string, CacheEntry<AnsibleModuleSpec>>();

/** Clears memoised ansible-doc output (tests, or after installing collections). */
export function clearAnsibleModuleCaches(): void {
  catalogCache = null;
  specCache.clear();
}

function requireAnsibleDoc(): string {
  const binary = getAnsibleDocBinary();
  if (!fs.existsSync(binary)) {
    throw new Error(
      `ansible-doc not found at ${binary}. The Ansible virtualenv (data/ansible-venv) has not been initialised.`
    );
  }
  return binary;
}

/**
 * Parses `ansible-doc -t module -l` output:
 *   ansible.builtin.apt                                Manages apt-packages
 */
export function parseModuleCatalog(stdout: string): ModuleCatalogEntry[] {
  const entries: ModuleCatalogEntry[] = [];
  for (const line of stdout.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    const match = trimmed.match(/^(\S+)\s{2,}(.*)$/);
    if (!match) continue;
    const [, name, description] = match;
    if (!isSafeModuleName(name)) continue;
    entries.push({ name, description: description.trim() });
  }
  return entries;
}

/** Lists every module ansible-doc can describe, memoised for 10 minutes. */
export function listAnsibleModules(options?: { force?: boolean }): ModuleCatalogEntry[] {
  const now = Date.now();
  if (!options?.force && catalogCache && catalogCache.expiresAt > now) {
    return catalogCache.value;
  }

  const binary = requireAnsibleDoc();
  const stdout = execFileSync(binary, ['-t', 'module', '-l'], {
    encoding: 'utf-8',
    timeout: 30000,
    maxBuffer: 16 * 1024 * 1024,
  });
  const entries = parseModuleCatalog(stdout);
  if (entries.length === 0) {
    throw new Error('ansible-doc returned no modules — check that ansible-core is installed in data/ansible-venv.');
  }
  catalogCache = { value: entries, expiresAt: now + CATALOG_TTL_MS };
  return entries;
}

function asStringList(value: unknown): string[] | undefined {
  if (!Array.isArray(value)) return undefined;
  const out = value.filter((item): item is string => typeof item === 'string');
  return out.length > 0 ? out : undefined;
}

/** Normalises one `ansible-doc -j` document into the editor's spec shape. */
export function normalizeModuleDoc(moduleName: string, payload: any): AnsibleModuleSpec {
  const key = Object.keys(payload || {})[0];
  if (!key) {
    throw new Error(`ansible-doc returned no documentation for ${moduleName}`);
  }
  const entry = payload[key];
  const doc = entry?.doc || {};

  const options: ModuleSpecOption[] = Object.entries(doc.options || {}).map(([name, raw]: [string, any]) => {
    const option: ModuleSpecOption = {
      name,
      type: typeof raw?.type === 'string' ? raw.type : 'str',
      required: raw?.required === true,
    };
    if (raw && 'default' in raw && raw.default !== undefined) {
      option.default = raw.default;
    }
    const choices = asStringList(raw?.choices);
    if (choices) option.choices = choices;
    const aliases = asStringList(raw?.aliases);
    if (aliases) option.aliases = aliases;
    if (typeof raw?.description === 'string') option.description = raw.description;
    return option;
  });

  const description = Array.isArray(doc.description)
    ? doc.description.map(String).join('\n')
    : typeof doc.description === 'string'
    ? doc.description
    : '';

  return {
    name: key,
    requestedName: moduleName,
    collection: typeof doc.collection === 'string' ? doc.collection : undefined,
    shortDescription: typeof doc.short_description === 'string' ? doc.short_description : '',
    description,
    options,
  };
}

/** Real parameter spec for one module, memoised for an hour. */
export function getAnsibleModuleSpec(moduleName: string, options?: { force?: boolean }): AnsibleModuleSpec {
  if (!isSafeModuleName(moduleName)) {
    throw new Error(`Invalid module name: ${String(moduleName)}`);
  }

  const now = Date.now();
  const cached = specCache.get(moduleName);
  if (!options?.force && cached && cached.expiresAt > now) {
    return cached.value;
  }

  const binary = requireAnsibleDoc();
  let stdout: string;
  try {
    stdout = execFileSync(binary, ['-j', moduleName], {
      encoding: 'utf-8',
      timeout: 30000,
      maxBuffer: 32 * 1024 * 1024,
    });
  } catch (err: any) {
    const stderr = String(err?.stderr || '').trim();
    throw new Error(stderr || err?.message || `ansible-doc failed for ${moduleName}`);
  }

  const spec = normalizeModuleDoc(moduleName, JSON.parse(stdout));
  specCache.set(moduleName, { value: spec, expiresAt: now + SPEC_TTL_MS });
  return spec;
}

/**
 * Registers the module metadata endpoints used by the visual editor:
 *   GET /api/ansible/modules              -> { success, modules: [{name, description}] }
 *   GET /api/ansible/modules/:name/spec   -> { success, spec }
 */
export function registerAnsibleModuleRoutes(app: any): void {
  app.get('/api/ansible/modules', (req: any, res: any) => {
    try {
      const query = String(req.query?.q || '').toLowerCase();
      const all = listAnsibleModules();
      const modules = query
        ? all.filter(
            entry => entry.name.toLowerCase().includes(query) || entry.description.toLowerCase().includes(query)
          )
        : all;
      res.json({ success: true, total: all.length, modules: modules.slice(0, 500) });
    } catch (err: any) {
      res.status(503).json({ success: false, error: err?.message || 'ansible-doc unavailable' });
    }
  });

  app.get('/api/ansible/modules/:name/spec', (req: any, res: any) => {
    const { name } = req.params;
    if (!isSafeModuleName(name)) {
      res.status(400).json({ success: false, error: `Invalid module name: ${name}` });
      return;
    }
    try {
      const spec = getAnsibleModuleSpec(name);
      res.json({ success: true, spec });
    } catch (err: any) {
      const message = err?.message || 'Failed to read module spec';
      const status = /not found|unable to find|no documentation/i.test(message) ? 404 : 503;
      res.status(status).json({ success: false, error: message });
    }
  });
}
