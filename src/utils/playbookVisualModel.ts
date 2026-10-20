/**
 * Pure model helpers for the visual (form based) playbook editor.
 *
 * The visual editor works directly on the document a real YAML parser returns
 * (see src/utils/playbookYaml.ts): a playbook is a list of plays, a play holds
 * `tasks`, and a task is `{ name, <module>: <args>, <directives...> }`. Edits
 * mutate that structure and `serializePlaybook` turns it back into YAML, so the
 * form view and the YAML view can never drift apart — both are the same text.
 *
 * This module is intentionally free of node/browser APIs so it can be unit
 * tested directly and imported by both the coordinator and the UI.
 */

export type JsonValue = null | boolean | number | string | JsonValue[] | { [key: string]: JsonValue };

/** Keys on a task that are Ansible task keywords rather than modules. */
export const TASK_DIRECTIVES: ReadonlySet<string> = new Set([
  'name',
  'when',
  'register',
  'changed_when',
  'failed_when',
  'loop',
  'loop_control',
  'with_items',
  'with_dict',
  'until',
  'retries',
  'delay',
  'tags',
  'notify',
  'become',
  'become_user',
  'become_method',
  'delegate_to',
  'run_once',
  'ignore_errors',
  'ignore_unreachable',
  'no_log',
  'check_mode',
  'environment',
  'vars',
  'async',
  'poll',
  'args',
  'listen',
  'any_errors_fatal',
  'throttle',
  'timeout',
  'connection',
  'remote_user',
  'block',
  'rescue',
  'always',
  'action',
  'local_action',
  'collections',
  'module_defaults',
]);

/** Field types as reported by `ansible-doc -j`. */
export interface ModuleSpecOption {
  name: string;
  type: string;
  required: boolean;
  default?: JsonValue;
  choices?: string[];
  aliases?: string[];
  description?: string;
}

export interface AnsibleModuleSpec {
  /** Fully qualified name returned by ansible-doc (e.g. ansible.builtin.service). */
  name: string;
  /** The name the caller asked for. */
  requestedName: string;
  collection?: string;
  shortDescription: string;
  description: string;
  options: ModuleSpecOption[];
}

export interface ModuleCatalogEntry {
  name: string;
  description: string;
}

/** How a spec option should be rendered in the form. */
export type FieldKind = 'boolean' | 'number' | 'select' | 'list' | 'dict' | 'text';

export function optionFieldKind(option: Pick<ModuleSpecOption, 'type' | 'choices'>): FieldKind {
  if (option.choices && option.choices.length > 0) return 'select';
  const type = (option.type || 'str').toLowerCase();
  if (type === 'bool' || type === 'boolean') return 'boolean';
  if (type === 'int' || type === 'integer' || type === 'float') return 'number';
  if (type === 'list' || type === 'elements') return 'list';
  if (type === 'dict' || type === 'dictionary') return 'dict';
  return 'text';
}

function isPlainObject(value: unknown): value is Record<string, any> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/** True when a task entry is inline `{ module: args }` rather than a block/action. */
export function getTaskModule(task: unknown): string | null {
  if (!isPlainObject(task)) return null;
  for (const key of Object.keys(task)) {
    if (!TASK_DIRECTIVES.has(key)) return key;
  }
  return null;
}

export function getTaskArgs(task: unknown, moduleName: string): Record<string, any> {
  if (!isPlainObject(task)) return {};
  const raw = task[moduleName];
  // `- ping:` (no args) parses to null; `- command: uptime` parses to a string.
  if (raw === null || raw === undefined) return {};
  if (isPlainObject(raw)) return raw;
  return { _raw: raw };
}

/** Directives on a task (`when`, `register`, `tags`, ...), module args excluded. */
export function getTaskDirectives(task: unknown, moduleName: string | null): Record<string, any> {
  if (!isPlainObject(task)) return {};
  const out: Record<string, any> = {};
  for (const [key, value] of Object.entries(task)) {
    if (key === moduleName) continue;
    out[key] = value;
  }
  return out;
}

export function getPlayTasks(play: unknown): any[] {
  if (!isPlainObject(play)) return [];
  return Array.isArray(play.tasks) ? play.tasks : [];
}

export function getPlayLabel(play: unknown, index: number): string {
  if (isPlainObject(play)) {
    if (typeof play.name === 'string' && play.name.trim()) return play.name.trim();
    if (typeof play.hosts === 'string' && play.hosts.trim()) return `hosts: ${play.hosts.trim()}`;
  }
  return `Play ${index + 1}`;
}

export function isPlaybookDocument(document: unknown): document is any[] {
  return Array.isArray(document);
}

/**
 * Describes why a parsed document cannot be shown in the visual editor.
 * Returns null when it can be edited field-by-field.
 */
export function describeVisualEditorSupport(document: unknown): string | null {
  if (document === null || document === undefined) {
    return 'The playbook is empty — add a play before switching to the visual editor.';
  }
  if (!Array.isArray(document)) {
    return 'A playbook must be a list of plays (top-level YAML sequence).';
  }
  for (let i = 0; i < document.length; i += 1) {
    if (!isPlainObject(document[i])) {
      return `Play ${i + 1} is not a mapping, so it cannot be edited as fields.`;
    }
  }
  return null;
}

/** Outcome of a task that is not a plain single-module task (block/action form). */
export function describeTaskShape(task: unknown): string | null {
  if (!isPlainObject(task)) return 'Task is not a mapping.';
  if (Array.isArray(task.block) || Array.isArray(task.rescue) || Array.isArray(task.always)) {
    return 'Block tasks are edited in YAML mode.';
  }
  if (task.action !== undefined && task.action !== null) {
    return 'Tasks using the generic `action:` form are edited in YAML mode.';
  }
  return null;
}

/** Builds a fresh task for `moduleName`, prefilling required options with defaults. */
export function createTaskForModule(
  moduleName: string,
  spec: AnsibleModuleSpec | null,
  name?: string
): Record<string, any> {
  const args: Record<string, any> = {};
  if (spec) {
    for (const option of spec.options) {
      if (option.required && option.default !== undefined && option.default !== null) {
        args[option.name] = option.default as any;
      }
    }
  }
  return {
    name: name || `Task using ${moduleName}`,
    [moduleName]: args,
  };
}

/** Sets a module argument (or removes it when `value` is undefined). */
export function setTaskArg(task: Record<string, any>, moduleName: string, argName: string, value: any): void {
  if (!isPlainObject(task[moduleName])) {
    const existing = task[moduleName];
    task[moduleName] = existing === null || existing === undefined ? {} : { _raw: existing };
  }
  const container = task[moduleName] as Record<string, any>;

  if (value === undefined) {
    delete container[argName];
    return;
  }
  container[argName] = value;
}

export function setTaskDirective(task: Record<string, any>, key: string, value: any): void {
  if (value === undefined || value === '') {
    delete task[key];
    return;
  }
  task[key] = value;
}

/** Converts a form control's string input into the value Ansible expects. */
export function coerceFieldValue(kind: FieldKind, raw: string): any {
  const trimmed = raw.trim();
  if (trimmed === '') return undefined;

  if (kind === 'number') {
    const num = Number(trimmed);
    return Number.isFinite(num) ? num : trimmed;
  }
  if (kind === 'list') {
    return trimmed
      .split('\n')
      .map(line => line.trim())
      .filter(Boolean);
  }
  if (kind === 'dict') {
    try {
      const parsed = JSON.parse(trimmed);
      return parsed;
    } catch {
      // Keep the raw text so a half-typed object is still visible; saving runs
      // the real syntax check and the YAML text shows exactly what is stored.
      return trimmed;
    }
  }
  return raw;
}

/** Renders a stored value back into the form control's text. */
export function fieldValueToText(value: any): string {
  if (value === undefined || value === null) return '';
  if (Array.isArray(value)) {
    return value.map(item => (typeof item === 'string' ? item : JSON.stringify(item))).join('\n');
  }
  if (typeof value === 'object') return JSON.stringify(value, null, 2);
  return String(value);
}

// ---------------------------------------------------------------------------
// YAML serialisation
// ---------------------------------------------------------------------------

const PLAIN_STRING = /^[A-Za-z_][A-Za-z0-9_./:@+= ,()%<>-]*$/;
const RESERVED_WORDS = new Set([
  'true',
  'false',
  'yes',
  'no',
  'on',
  'off',
  'null',
  'nil',
  '~',
  'y',
  'n',
]);

function quoteString(value: string): string {
  return `"${value
    .replace(/\\/g, '\\\\')
    .replace(/"/g, '\\"')
    .replace(/\n/g, '\\n')
    .replace(/\r/g, '\\r')
    .replace(/\t/g, '\\t')}"`;
}

export function serializeScalar(value: unknown): string {
  if (value === null || value === undefined) return 'null';
  if (typeof value === 'boolean') return value ? 'true' : 'false';
  if (typeof value === 'number') return Number.isFinite(value) ? String(value) : quoteString(String(value));
  if (typeof value === 'string') {
    const text = value;
    if (text === '') return '""';
    const lower = text.toLowerCase();
    const needsQuotes =
      RESERVED_WORDS.has(lower) ||
      !PLAIN_STRING.test(text) ||
      /^\s|\s$/.test(text) ||
      /:(\s|$)/.test(text) ||
      /\s#/.test(text) ||
      /^[-?].*$/.test(text) ||
      /^[0-9.+-]+$/.test(text) ||
      // Multi-line strings are emitted as escaped double-quoted scalars so a
      // round-trip cannot silently change whitespace.
      text.includes('\n') ||
      text.includes('\t');
    return needsQuotes ? quoteString(text) : text;
  }
  return quoteString(JSON.stringify(value));
}

function isEmpty(value: unknown): boolean {
  if (value === undefined || value === null) return true;
  if (Array.isArray(value)) return value.length === 0;
  if (typeof value === 'object') return Object.keys(value as Record<string, unknown>).length === 0;
  return false;
}

function indentOf(level: number): string {
  return '  '.repeat(level);
}

/** Serialises a plain JSON-ish value into block-style YAML at `level`. */
export function serializeYamlValue(value: unknown, level = 0): string {
  if (Array.isArray(value)) {
    if (value.length === 0) return '[]';
    return value.map(item => serializeListEntry(item, level)).join('\n');
  }
  if (typeof value === 'object' && value !== null) {
    return serializeMap(value as Record<string, unknown>, level);
  }
  return serializeScalar(value);
}

function serializeMap(map: Record<string, unknown>, level: number): string {
  const entries = Object.entries(map).filter(([, v]) => v !== undefined);
  if (entries.length === 0) return '{}';
  const pad = indentOf(level);
  return entries
    .map(([key, value]) => {
      const safeKey = serializeKey(key);
      if (isEmpty(value)) {
        if (Array.isArray(value)) return `${pad}${safeKey}: []`;
        if (value && typeof value === 'object') return `${pad}${safeKey}: {}`;
      }
      if (Array.isArray(value)) {
        return `${pad}${safeKey}:\n${serializeYamlValue(value, level + 1)}`;
      }
      if (value && typeof value === 'object') {
        return `${pad}${safeKey}:\n${serializeYamlValue(value, level + 1)}`;
      }
      return `${pad}${safeKey}: ${serializeScalar(value)}`;
    })
    .join('\n');
}

function serializeListEntry(item: unknown, level: number): string {
  const pad = indentOf(level);
  if (item === undefined) return `${pad}- null`;
  if (Array.isArray(item)) {
    if (item.length === 0) return `${pad}- []`;
    return `${pad}-\n${serializeYamlValue(item, level + 1)}`;
  }
  if (typeof item === 'object' && item !== null) {
    const entries = Object.entries(item as Record<string, unknown>).filter(([, v]) => v !== undefined);
    if (entries.length === 0) return `${pad}- {}`;
    const inner = serializeMap(item as Record<string, unknown>, level + 1);
    // First key rides on the dash ("- name: ..."); following keys align under it.
    return `${pad}- ${inner.slice(indentOf(level + 1).length)}`;
  }
  return `${pad}- ${serializeScalar(item)}`;
}

function serializeKey(key: string): string {
  return /^[A-Za-z0-9_.-]+$/.test(key) && !RESERVED_WORDS.has(key.toLowerCase()) ? key : quoteString(key);
}

/** Serialises a whole playbook document (list of plays) back to YAML text. */
export function serializePlaybook(document: unknown): string {
  const body = serializeYamlValue(document, 0);
  return `---\n${body}\n`;
}

/** Deep-clones a parsed document so edits never mutate the last-loaded state. */
export function cloneDocument<T>(document: T): T {
  return JSON.parse(JSON.stringify(document)) as T;
}
