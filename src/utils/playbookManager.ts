import fs from 'node:fs';
import path from 'node:path';
import { execSync } from 'node:child_process';

export const PROTECTED_PLAYBOOKS = new Set<string>(['site.yml', 'site.yaml']);

export interface PlaybookValidationResult {
  valid: boolean;
  name: string;
  error?: string;
}

export interface PlaybookItemResult {
  name: string;
  content: string;
}

/**
 * Validates that a playbook filename is safe:
 * - Non-empty string
 * - No path traversal (path.basename(name) === name)
 * - No slashes or backslashes
 * - Ends with .yml or .yaml
 * - Contains only safe filename characters
 */
export function validatePlaybookName(name: unknown): PlaybookValidationResult {
  if (typeof name !== 'string' || !name.trim()) {
    return {
      valid: false,
      name: String(name || ''),
      error: 'Playbook name must be a non-empty string',
    };
  }

  const trimmed = name.trim();

  // Guard against path traversal and directory separators
  if (
    path.basename(trimmed) !== trimmed ||
    trimmed.includes('..') ||
    trimmed.includes('/') ||
    trimmed.includes('\\')
  ) {
    return {
      valid: false,
      name: trimmed,
      error: 'Directory traversal and path separators are not permitted in playbook name',
    };
  }

  // Must end with .yml or .yaml
  if (!trimmed.endsWith('.yml') && !trimmed.endsWith('.yaml')) {
    return {
      valid: false,
      name: trimmed,
      error: 'Playbook filename must have a .yml or .yaml extension',
    };
  }

  // Must have a basename before the extension
  const baseWithoutExt = trimmed.replace(/\.(ya?ml)$/, '');
  if (!baseWithoutExt) {
    return {
      valid: false,
      name: trimmed,
      error: 'Playbook filename must have a name before the extension',
    };
  }

  // Allow only alphanumeric characters, hyphens, underscores, and dots
  const validPattern = /^[a-zA-Z0-9_\-\.]+$/;
  if (!validPattern.test(trimmed)) {
    return {
      valid: false,
      name: trimmed,
      error: 'Playbook filename contains invalid characters. Use letters, numbers, hyphens, and underscores.',
    };
  }

  return {
    valid: true,
    name: trimmed,
  };
}

/**
 * Normalizes a user-provided playbook name by appending .yml if extension was omitted.
 */
export function normalizePlaybookName(name: string): string {
  const trimmed = name.trim();
  if (trimmed.endsWith('.yml') || trimmed.endsWith('.yaml')) {
    return trimmed;
  }
  return `${trimmed}.yml`;
}

/**
 * Checks whether a playbook is protected from deletion (e.g. cluster root baseline).
 */
export function isProtectedPlaybook(name: string): boolean {
  return PROTECTED_PLAYBOOKS.has(path.basename(name));
}

/**
 * Generates starter YAML content for a newly created Ansible playbook.
 */
export function getStarterPlaybookTemplate(playbookName?: string): string {
  let readableTitle = 'New Playbook';
  if (playbookName) {
    const raw = playbookName.replace(/\.(ya?ml)$/, '').replace(/[-_]/g, ' ');
    readableTitle = raw
      .split(' ')
      .filter(Boolean)
      .map(word => word.charAt(0).toUpperCase() + word.slice(1))
      .join(' ');
  }

  return `---
- name: ${readableTitle || 'New Playbook'}
  hosts: tag:host
  gather_facts: false
  tasks:
    - name: Verify host reachability
      ping:

    - name: Check system uptime
      command: uptime -p
      register: host_uptime
      changed_when: false

    - name: Report host status
      debug:
        msg: "{{ inventory_hostname }} uptime is {{ host_uptime.stdout }}."
`;
}

/**
 * Helper to safely resolve a playbook path inside the playbooks directory.
 */
function resolveSafePath(playbooksDir: string, name: string): string {
  const validation = validatePlaybookName(name);
  if (!validation.valid) {
    throw new Error(validation.error || 'Invalid playbook name');
  }

  const resolvedDir = path.resolve(playbooksDir);
  const resolvedPath = path.resolve(resolvedDir, validation.name);

  if (!resolvedPath.startsWith(resolvedDir + path.sep)) {
    throw new Error('Path traversal detected');
  }

  return resolvedPath;
}

/**
 * Lists all playbook files (.yml / .yaml) in the specified directory.
 */
export function listPlaybooks(playbooksDir: string): string[] {
  if (!fs.existsSync(playbooksDir)) {
    return [];
  }

  const files = fs.readdirSync(playbooksDir);
  return files
    .filter(f => !f.startsWith('.') && (f.endsWith('.yml') || f.endsWith('.yaml')))
    .sort((a, b) => {
      // Prioritize site.yml at top, then alphabetical
      if (a === 'site.yml') return -1;
      if (b === 'site.yml') return 1;
      return a.localeCompare(b);
    });
}

/**
 * Reads the content of an existing playbook.
 */
export function readPlaybook(playbooksDir: string, name: string): PlaybookItemResult {
  const filePath = resolveSafePath(playbooksDir, name);
  if (!fs.existsSync(filePath)) {
    throw new Error(`Playbook ${name} not found`);
  }

  const content = fs.readFileSync(filePath, 'utf-8');
  return {
    name: path.basename(filePath),
    content,
  };
}

/**
 * Creates a new playbook file. Fails if the file already exists.
 */
export function createPlaybook(
  playbooksDir: string,
  name: string,
  content?: string
): PlaybookItemResult {
  const filePath = resolveSafePath(playbooksDir, name);

  if (!fs.existsSync(playbooksDir)) {
    fs.mkdirSync(playbooksDir, { recursive: true });
  }

  if (fs.existsSync(filePath)) {
    throw new Error(`Playbook ${name} already exists`);
  }

  const finalContent =
    typeof content === 'string' && content.trim().length > 0
      ? content
      : getStarterPlaybookTemplate(name);

  fs.writeFileSync(filePath, finalContent, 'utf-8');

  return {
    name: path.basename(filePath),
    content: finalContent,
  };
}

/**
 * Updates the content of an existing playbook.
 */
export function updatePlaybook(
  playbooksDir: string,
  name: string,
  content: string
): PlaybookItemResult {
  if (typeof content !== 'string') {
    throw new Error('Playbook content must be a string');
  }

  const filePath = resolveSafePath(playbooksDir, name);
  if (!fs.existsSync(filePath)) {
    throw new Error(`Playbook ${name} not found`);
  }

  fs.writeFileSync(filePath, content, 'utf-8');

  return {
    name: path.basename(filePath),
    content,
  };
}

/**
 * Deletes an existing playbook file, unless it is protected.
 */
export function deletePlaybook(playbooksDir: string, name: string): { name: string } {
  if (isProtectedPlaybook(name)) {
    throw new Error(`Cannot delete protected baseline playbook ${name}`);
  }

  const filePath = resolveSafePath(playbooksDir, name);
  if (!fs.existsSync(filePath)) {
    throw new Error(`Playbook ${name} not found`);
  }

  fs.unlinkSync(filePath);

  return {
    name: path.basename(filePath),
  };
}

/**
 * Registers Express routes for Ansible playbook CRUD and syntax check.
 */
export function registerPlaybookRoutes(
  app: any,
  playbooksDir: string,
  options?: { inventoryScriptPath?: string }
): void {
  app.get('/api/playbooks', (_req: any, res: any) => {
    try {
      const files = listPlaybooks(playbooksDir);
      res.json({ playbooks: files });
    } catch (err: any) {
      res.status(500).json({ error: err?.message || 'Failed to list playbooks' });
    }
  });

  app.get('/api/playbooks/:name', (req: any, res: any) => {
    const { name } = req.params;
    const validation = validatePlaybookName(name);
    if (!validation.valid) {
      res.status(400).json({ error: validation.error });
      return;
    }
    try {
      const result = readPlaybook(playbooksDir, name);
      res.json({ success: true, ...result });
    } catch (err: any) {
      const status = err?.message?.includes('not found') ? 404 : 500;
      res.status(status).json({ error: err?.message || 'Failed to read playbook' });
    }
  });

  app.post('/api/playbooks', (req: any, res: any) => {
    let { name, content } = req.body || {};
    if (!name || typeof name !== 'string') {
      res.status(400).json({ error: 'Playbook name is required' });
      return;
    }
    name = normalizePlaybookName(name);
    const validation = validatePlaybookName(name);
    if (!validation.valid) {
      res.status(400).json({ error: validation.error });
      return;
    }
    try {
      const result = createPlaybook(playbooksDir, name, content);
      res.status(201).json({ success: true, ...result });
    } catch (err: any) {
      const status = err?.message?.includes('already exists') ? 409 : 500;
      res.status(status).json({ error: err?.message || 'Failed to create playbook' });
    }
  });

  app.put('/api/playbooks/:name', (req: any, res: any) => {
    const { name } = req.params;
    const { content } = req.body || {};
    const validation = validatePlaybookName(name);
    if (!validation.valid) {
      res.status(400).json({ error: validation.error });
      return;
    }
    if (typeof content !== 'string') {
      res.status(400).json({ error: 'Playbook content must be a string' });
      return;
    }
    try {
      const result = updatePlaybook(playbooksDir, name, content);
      res.json({ success: true, ...result });
    } catch (err: any) {
      const status = err?.message?.includes('not found') ? 404 : 500;
      res.status(status).json({ error: err?.message || 'Failed to update playbook' });
    }
  });

  app.delete('/api/playbooks/:name', (req: any, res: any) => {
    const { name } = req.params;
    const validation = validatePlaybookName(name);
    if (!validation.valid) {
      res.status(400).json({ error: validation.error });
      return;
    }
    if (isProtectedPlaybook(name)) {
      res.status(403).json({ error: `Cannot delete protected baseline playbook ${name}` });
      return;
    }
    try {
      const result = deletePlaybook(playbooksDir, name);
      res.json({ success: true, ...result });
    } catch (err: any) {
      const status = err?.message?.includes('not found') ? 404 : 500;
      res.status(status).json({ error: err?.message || 'Failed to delete playbook' });
    }
  });

  app.post('/api/playbook/syntax-check', (req: any, res: any) => {
    const { playbook = 'site.yml', content } = req.body || {};
    const val = validatePlaybookName(playbook);
    if (!val.valid) {
      res.status(400).json({ success: false, playbook, error: val.error || 'Invalid playbook name' });
      return;
    }

    let tempFilePath: string | null = null;
    let targetPath = path.resolve(playbooksDir, playbook);

    if (typeof content === 'string') {
      tempFilePath = path.resolve(
        playbooksDir,
        `.tmp_syntax_${Date.now()}_${Math.random().toString(36).slice(2, 6)}.yml`
      );
      try {
        fs.writeFileSync(tempFilePath, content, 'utf-8');
        targetPath = tempFilePath;
      } catch (writeErr: any) {
        res.status(500).json({ success: false, error: `Failed to prepare syntax check: ${writeErr?.message}` });
        return;
      }
    }

    const inventoryPath =
      options?.inventoryScriptPath ||
      path.resolve(process.cwd(), 'inventory/tailscale_inventory.py');

    try {
      const out = execSync(`ansible-playbook -i ${inventoryPath} ${targetPath} --syntax-check`, {
        encoding: 'utf-8',
        timeout: 10000,
      });
      res.json({ success: true, playbook, output: out.trim() });
    } catch (err: any) {
      res.status(400).json({
        success: false,
        playbook,
        error: err?.stdout || err?.stderr || err?.message || 'Syntax check failed',
      });
    } finally {
      if (tempFilePath && fs.existsSync(tempFilePath)) {
        try {
          fs.unlinkSync(tempFilePath);
        } catch {}
      }
    }
  });
}

