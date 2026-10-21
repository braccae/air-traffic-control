import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { getAnsibleVenvPath } from './ansibleVenvManager.ts';

/**
 * Real YAML parsing for the visual playbook editor.
 *
 * Playbooks are parsed by the Ansible virtualenv's Python with PyYAML (a hard
 * dependency of ansible-core), so the structure the visual editor shows is
 * exactly what ansible-playbook will load. Zero-Mock Policy (AGENTS.md §2):
 * when the bridge cannot run, the caller gets the real failure — never an
 * empty or approximated document.
 */

export interface PlaybookParseOk {
  success: true;
  document: unknown;
}

export interface PlaybookParseError {
  success: false;
  error: string;
  line?: number;
  column?: number;
  problem?: string;
  context?: string;
}

export type PlaybookParseResult = PlaybookParseOk | PlaybookParseError;

export interface PlaybookYamlBridgeOptions {
  /** Override for tests / non-standard layouts. */
  bridgeScriptPath?: string;
  /** Override for tests / non-standard layouts. */
  pythonBinary?: string;
  timeoutMs?: number;
}

export function getBridgeScriptPath(): string {
  return process.env.PLAYBOOK_YAML_BRIDGE || path.resolve(process.cwd(), 'scripts', 'playbook_yaml_bridge.py');
}

export function getVenvPythonBinary(): string {
  return path.join(getAnsibleVenvPath(), 'bin', 'python');
}

/**
 * Parses playbook YAML through the real PyYAML bridge.
 *
 * Throws when the bridge itself cannot be executed (missing venv, missing
 * script); returns a `success: false` result when the YAML is invalid so the
 * UI can show the actual parser complaint with line/column.
 */
export function parsePlaybookYaml(content: string, options?: PlaybookYamlBridgeOptions): PlaybookParseResult {
  const scriptPath = options?.bridgeScriptPath || getBridgeScriptPath();
  const pythonBinary = options?.pythonBinary || getVenvPythonBinary();
  const timeout = options?.timeoutMs ?? 20000;

  if (!fs.existsSync(pythonBinary)) {
    throw new Error(
      `Ansible virtualenv Python not found at ${pythonBinary}. Run the daemon once so the uv bootstrapper creates data/ansible-venv.`
    );
  }
  if (!fs.existsSync(scriptPath)) {
    throw new Error(`Playbook YAML bridge script not found at ${scriptPath}`);
  }

  const stdout = execFileSync(pythonBinary, [scriptPath], {
    input: JSON.stringify({ action: 'parse', content }),
    encoding: 'utf-8',
    timeout,
    maxBuffer: 32 * 1024 * 1024,
  });

  const parsed = JSON.parse(stdout.trim() || '{}');

  if (parsed.success) {
    return { success: true, document: parsed.document };
  }

  return {
    success: false,
    error: String(parsed.error || 'Unable to parse playbook YAML'),
    line: typeof parsed.line === 'number' ? parsed.line : undefined,
    column: typeof parsed.column === 'number' ? parsed.column : undefined,
    problem: typeof parsed.problem === 'string' ? parsed.problem : undefined,
    context: typeof parsed.context === 'string' ? parsed.context : undefined,
  };
}

/**
 * Registers POST /api/playbooks/parse — real YAML parsing for the visual editor.
 */
export function registerPlaybookYamlRoutes(app: any, options?: PlaybookYamlBridgeOptions): void {
  app.post('/api/playbooks/parse', (req: any, res: any) => {
    const { content } = req.body || {};
    if (typeof content !== 'string') {
      res.status(400).json({ success: false, error: 'Playbook content must be a string' });
      return;
    }
    try {
      const result = parsePlaybookYaml(content, options);
      if (!result.success) {
        res.status(400).json(result);
        return;
      }
      res.json(result);
    } catch (err: any) {
      // A broken bridge is an infrastructure failure, not invalid YAML.
      res.status(503).json({ success: false, error: err?.message || 'Playbook YAML bridge unavailable' });
    }
  });
}
