export type VarType = 'string' | 'number' | 'boolean' | 'secret';

export interface VarEntry {
  id: string;
  key: string;
  type: VarType;
  value: any;
  isVault?: boolean;
  comment?: string;
}

export interface ParseResult {
  vars: VarEntry[];
  rawObject: Record<string, any>;
  comments: string[];
  header?: string;
  hasErrors: boolean;
  errorMessage?: string;
}

let idCounter = 0;
export function generateVarId(): string {
  idCounter += 1;
  return `var_${Date.now()}_${idCounter}`;
}

const SECRET_PATTERNS = [
  /password/i,
  /passphrase/i,
  /pass$/i,
  /pass[_-]/i,
  /secret/i,
  /vault/i,
  /token/i,
  /api[_-]?key/i,
  /credential/i,
  /private[_-]?key/i,
  /auth[_-]?(key|token)/i,
];

/**
 * Detects the data type for a variable based on its key name and value.
 */
export function detectVarType(key: string, value: any): VarType {
  const strVal = String(value ?? '').trim();

  // 1. Vault or Ansible secret indicators
  if (
    strVal.startsWith('!vault') ||
    strVal.includes('$ANSIBLE_VAULT;') ||
    SECRET_PATTERNS.some(regex => regex.test(key))
  ) {
    return 'secret';
  }

  // 2. Booleans
  if (typeof value === 'boolean') return 'boolean';
  if (/^(true|false|yes|no)$/i.test(strVal)) return 'boolean';

  // 3. Numbers
  if (typeof value === 'number' && !Number.isNaN(value)) return 'number';
  if (typeof value === 'string' && /^-?\d+(\.\d+)?$/.test(strVal) && strVal !== '') {
    // Avoid classifying leading-zero strings as numbers (like IP fragments "01") unless it's "0"
    if (!/^0\d+/.test(strVal)) {
      return 'number';
    }
  }

  return 'string';
}

/**
 * Coerces a value to the specified VarType.
 */
export function coerceValueByType(type: VarType, value: any): any {
  if (type === 'boolean') {
    if (typeof value === 'boolean') return value;
    const s = String(value ?? '').toLowerCase().trim();
    return s === 'true' || s === 'yes' || s === '1';
  }

  if (type === 'number') {
    if (value === '' || value === null || value === undefined) return '';
    if (typeof value === 'number') return Number.isNaN(value) ? '' : value;
    const num = Number(value);
    return Number.isNaN(num) ? '' : num;
  }

  if (type === 'secret') {
    return typeof value === 'object' ? JSON.stringify(value) : String(value ?? '');
  }

  // string
  if (typeof value === 'object' && value !== null) {
    return value;
  }
  return String(value ?? '');
}

/**
 * Strips quotes from a string value if enclosed in matching single or double quotes.
 */
function unquote(str: string): string {
  const trimmed = str.trim();
  if (
    (trimmed.startsWith('"') && trimmed.endsWith('"')) ||
    (trimmed.startsWith("'") && trimmed.endsWith("'"))
  ) {
    return trimmed.slice(1, -1);
  }
  return trimmed;
}

/**
 * Formats a scalar value safely for YAML output.
 */
function formatYamlScalar(val: any, type: VarType, isVault?: boolean): string {
  if (isVault) {
    const lines = String(val).split('\n');
    return `!vault |\n` + lines.map(l => `  ${l}`).join('\n');
  }

  if (type === 'boolean') {
    return val ? 'true' : 'false';
  }

  if (type === 'number') {
    if (val === '' || val === null || val === undefined) return '0';
    return String(val);
  }

  if (type === 'secret') {
    // Check if the secret value itself is multiline vault
    const str = String(val);
    if (str.includes('$ANSIBLE_VAULT;')) {
      const lines = str.split('\n');
      return `!vault |\n` + lines.map(l => `  ${l}`).join('\n');
    }
    return JSON.stringify(str);
  }

  // Array
  if (Array.isArray(val)) {
    if (val.length === 0) return '[]';
    return '\n' + val.map(item => `  - ${item}`).join('\n');
  }

  // String
  const s = String(val ?? '');
  // If string contains colons, hashes, newlines, quotes, or starts/ends with whitespace or looks like bool/num
  if (
    s.includes(':') ||
    s.includes('#') ||
    s.includes('\n') ||
    s.includes('"') ||
    s.includes("'") ||
    /^(true|false|yes|no|null|~|-?\d+(\.\d+)?)$/i.test(s) ||
    s.startsWith(' ') ||
    s.endsWith(' ') ||
    s === ''
  ) {
    return JSON.stringify(s);
  }

  return s;
}

/**
 * Parses a YAML string into a structured list of typed variables.
 */
export function parseYamlToVars(yamlContent: string): ParseResult {
  const vars: VarEntry[] = [];
  const comments: string[] = [];
  const rawObject: Record<string, any> = {};

  if (!yamlContent || !yamlContent.trim()) {
    return { vars, rawObject, comments, hasErrors: false };
  }

  const trimmed = yamlContent.trim();
  if (trimmed.startsWith('{') && trimmed.endsWith('}')) {
    try {
      const parsedJson = JSON.parse(trimmed);
      if (typeof parsedJson === 'object' && parsedJson !== null && !Array.isArray(parsedJson)) {
        return {
          vars: objectToVars(parsedJson),
          rawObject: parsedJson,
          comments,
          hasErrors: false,
        };
      }
    } catch {
      // Fall through to line-based YAML parser
    }
  }

  try {
    const lines = yamlContent.split('\n');
    let i = 0;
    let currentParentKey: string | null = null;
    let currentParentIndent = 0;

    while (i < lines.length) {
      const line = lines[i];
      const trimmed = line.trim();

      // Skip document separator
      if (trimmed === '---' || trimmed === '...') {
        i++;
        continue;
      }

      // Preserve full-line comments
      if (trimmed.startsWith('#')) {
        comments.push(trimmed);
        i++;
        continue;
      }

      if (!trimmed) {
        i++;
        continue;
      }

      // Check indentation
      const indent = line.search(/\S|$/);

      // Check for !vault block scalar or multiline pipe |
      const vaultMatch = line.match(/^(\s*)([a-zA-Z0-9_.-]+)\s*:\s*(!vault\s*\||\|)\s*$/);
      if (vaultMatch) {
        const key = vaultMatch[2];
        const isVault = vaultMatch[3].includes('!vault');
        const blockIndent = indent;
        const blockLines: string[] = [];
        i++;
        while (i < lines.length) {
          const nextLine = lines[i];
          const nextTrimmed = nextLine.trim();
          if (!nextTrimmed) {
            i++;
            continue;
          }
          const nextIndent = nextLine.search(/\S|$/);
          if (nextIndent <= blockIndent && !nextLine.startsWith('---')) {
            break;
          }
          blockLines.push(nextTrimmed);
          i++;
        }
        const blockVal = blockLines.join('\n');
        vars.push({
          id: generateVarId(),
          key,
          type: 'secret',
          value: blockVal,
          isVault: true,
        });
        rawObject[key] = blockVal;
        continue;
      }

      // Check for list start: key:\n  - item
      const listHeaderMatch = line.match(/^(\s*)([a-zA-Z0-9_.-]+)\s*:\s*$/);
      if (listHeaderMatch && i + 1 < lines.length && lines[i + 1].trim().startsWith('- ')) {
        const key = listHeaderMatch[2];
        const listItems: string[] = [];
        i++;
        while (i < lines.length && lines[i].trim().startsWith('- ')) {
          listItems.push(unquote(lines[i].trim().substring(2).trim()));
          i++;
        }
        vars.push({
          id: generateVarId(),
          key,
          type: 'string',
          value: listItems,
        });
        rawObject[key] = listItems;
        continue;
      }

      // Check for nested parent mapping header: key:\n  nested_key: value
      if (listHeaderMatch && i + 1 < lines.length && !lines[i + 1].trim().startsWith('- ')) {
        const nextIndent = lines[i + 1].search(/\S|$/);
        if (nextIndent > indent) {
          currentParentKey = listHeaderMatch[2];
          currentParentIndent = indent;
          if (!rawObject[currentParentKey]) {
            rawObject[currentParentKey] = {};
          }
          i++;
          continue;
        }
      }

      // Key-value pair: key: value
      // Regex matches key name and everything after the colon
      const kvMatch = line.match(/^(\s*)([a-zA-Z0-9_.-]+)\s*:\s*(.*)$/);
      if (kvMatch) {
        const keyIndent = indent;
        let key = kvMatch[2];
        let rawVal = kvMatch[3].trim();

        // If indent is deeper than current parent, prefix with parent key
        if (currentParentKey && keyIndent > currentParentIndent) {
          key = `${currentParentKey}.${key}`;
        } else if (keyIndent <= currentParentIndent) {
          currentParentKey = null;
        }

        // Inline comment removal if value is not quoted
        if (!rawVal.startsWith('"') && !rawVal.startsWith("'")) {
          const commentIdx = rawVal.indexOf(' #');
          if (commentIdx !== -1) {
            rawVal = rawVal.substring(0, commentIdx).trim();
          }
        }

        let parsedVal: any = unquote(rawVal);
        let detectedType: VarType = detectVarType(key, rawVal);

        if (detectedType === 'boolean') {
          parsedVal = /^(true|yes)$/i.test(rawVal);
        } else if (detectedType === 'number') {
          parsedVal = Number(rawVal);
        }

        const isVault = rawVal.startsWith('!vault') || rawVal.includes('$ANSIBLE_VAULT;');

        vars.push({
          id: generateVarId(),
          key,
          type: detectedType,
          value: parsedVal,
          isVault,
        });

        // Populate rawObject
        if (key.includes('.')) {
          const parts = key.split('.');
          let curr = rawObject;
          for (let p = 0; p < parts.length - 1; p++) {
            curr[parts[p]] = curr[parts[p]] || {};
            curr = curr[parts[p]];
          }
          curr[parts[parts.length - 1]] = parsedVal;
        } else {
          rawObject[key] = parsedVal;
        }

        i++;
        continue;
      }

      i++;
    }

    return {
      vars,
      rawObject,
      comments,
      hasErrors: false,
    };
  } catch (err: any) {
    return {
      vars,
      rawObject,
      comments,
      hasErrors: true,
      errorMessage: err?.message || 'YAML parsing error',
    };
  }
}

/**
 * Serializes a list of VarEntries into formatted Ansible YAML.
 */
export function varsToYaml(vars: VarEntry[], options?: { header?: string }): string {
  // Filter for variables with non-empty keys; return empty string if no variables
  const validVars = vars.filter(v => v.key && v.key.trim());
  if (validVars.length === 0) {
    return '';
  }

  const lines: string[] = ['---'];

  if (options?.header) {
    lines.push(`# ${options.header}`);
  }

  // Group variables by prefix for nested dotted keys
  const topLevelVars: VarEntry[] = [];
  const nestedGroups: Record<string, VarEntry[]> = {};

  for (const v of validVars) {
    if (v.key.includes('.')) {
      const prefix = v.key.split('.')[0];
      nestedGroups[prefix] = nestedGroups[prefix] || [];
      nestedGroups[prefix].push(v);
    } else {
      topLevelVars.push(v);
    }
  }

  // Output top-level variables first
  for (const v of topLevelVars) {
    const formatted = formatYamlScalar(v.value, v.type, v.isVault);
    if (v.isVault || (Array.isArray(v.value) && v.value.length > 0)) {
      lines.push(`${v.key}:${formatted.startsWith('\n') ? formatted : ' ' + formatted}`);
    } else {
      lines.push(`${v.key}: ${formatted}`);
    }
  }

  // Output nested groupings
  for (const [prefix, group] of Object.entries(nestedGroups)) {
    lines.push(`${prefix}:`);
    for (const item of group) {
      const subKey = item.key.slice(prefix.length + 1);
      const formatted = formatYamlScalar(item.value, item.type, item.isVault);
      lines.push(`  ${subKey}: ${formatted}`);
    }
  }

  return lines.join('\n') + '\n';
}

/**
 * Converts a JS Record into typed VarEntry array.
 */
export function objectToVars(obj: Record<string, any>, prefix = '', existingVars?: VarEntry[]): VarEntry[] {
  const existingMap = new Map(existingVars?.map(v => [v.key, v.id]));
  const vars: VarEntry[] = [];

  for (const [k, v] of Object.entries(obj)) {
    const fullKey = prefix ? `${prefix}.${k}` : k;
    if (v !== null && typeof v === 'object' && !Array.isArray(v)) {
      vars.push(...objectToVars(v, fullKey, existingVars));
    } else {
      const type = detectVarType(fullKey, v);
      const stableId = existingMap.get(fullKey) || `var_${fullKey.replace(/[^a-zA-Z0-9_]/g, '_')}`;
      vars.push({
        id: stableId,
        key: fullKey,
        type,
        value: v,
        isVault: type === 'secret' && String(v).includes('$ANSIBLE_VAULT;'),
      });
    }
  }

  return vars;
}

/**
 * Converts typed VarEntry array back to a nested JS object.
 */
export function varsToObject(vars: VarEntry[]): Record<string, any> {
  const obj: Record<string, any> = {};

  for (const v of vars) {
    if (!v.key || !v.key.trim()) continue;
    const parts = v.key.split('.');
    let target = obj;
    for (let i = 0; i < parts.length - 1; i++) {
      target[parts[i]] = target[parts[i]] || {};
      target = target[parts[i]];
    }
    const val = coerceValueByType(v.type, v.value);
    target[parts[parts.length - 1]] = (v.type === 'number' && val === '') ? 0 : val;
  }

  return obj;
}

/**
 * Parses Ansible extra vars from JSON, YAML, or key=value string into VarEntry[].
 */
export function parseExtraVars(input: string): VarEntry[] {
  const trimmed = input.trim();
  if (!trimmed) return [];

  // 1. Try JSON parsing
  if (trimmed.startsWith('{') && trimmed.endsWith('}')) {
    try {
      const parsed = JSON.parse(trimmed);
      if (typeof parsed === 'object' && parsed !== null) {
        return objectToVars(parsed);
      }
    } catch {
      // Fall through to other formats
    }
  }

  // 2. Try YAML parsing if contains newlines or colons
  if (trimmed.includes(':') || trimmed.includes('\n')) {
    const res = parseYamlToVars(trimmed);
    if (!res.hasErrors && res.vars.length > 0) {
      return res.vars;
    }
  }

  // 3. Try key=value token pairs (e.g. "target=prod debug=true retries=5")
  const kvTokens = trimmed.split(/\s+/);
  const vars: VarEntry[] = [];
  for (const token of kvTokens) {
    const eqIdx = token.indexOf('=');
    if (eqIdx !== -1) {
      const key = token.substring(0, eqIdx).trim();
      const valStr = token.substring(eqIdx + 1).trim();
      const type = detectVarType(key, valStr);
      let coerced: any = valStr;
      if (type === 'boolean') {
        coerced = /^(true|yes)$/i.test(valStr);
      } else if (type === 'number') {
        coerced = Number(valStr);
      }
      vars.push({
        id: generateVarId(),
        key,
        type,
        value: coerced,
      });
    }
  }

  return vars;
}

/**
 * Formats variables into JSON string or YAML string for Ansible CLI execution.
 */
export function formatExtraVars(vars: VarEntry[] | Record<string, any>, format: 'json' | 'yaml' = 'json'): string {
  let list: VarEntry[];
  if (Array.isArray(vars)) {
    list = vars;
  } else {
    list = objectToVars(vars);
  }

  if (list.length === 0) return '';

  if (format === 'json') {
    const obj = varsToObject(list);
    return JSON.stringify(obj, null, 2);
  }

  return varsToYaml(list);
}
