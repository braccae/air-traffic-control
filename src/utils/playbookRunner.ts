/**
 * Real Ansible playbook execution used by both the HTTP API and the cron
 * scheduler. Every run spawns a genuine `ansible-playbook` child process,
 * streams its stdout/stderr into a GlobalTaskItem, persists it through the
 * supplied store and broadcasts progress over the WebSocket hub.
 *
 * Zero-Mock Policy: nothing here fabricates progress, output, or status — a
 * spawned runner that cannot execute reports FAILED with the real error.
 */
import fs from 'node:fs';
import path from 'node:path';
import { spawn, type ChildProcess } from 'node:child_process';
import type { GlobalTaskItem } from '../types/index.ts';
import { validatePlaybookName } from './playbookManager.ts';

export interface PlaybookRunRequest {
  playbook: string;
  target?: string;
  checkMode?: boolean;
  tags?: string;
  extraVars?: string | Record<string, unknown>;
  /** Caller-supplied task id (cron runs reuse the scheduler's run id when given). */
  taskId?: string;
  /** Free-form provenance recorded in the task description, e.g. `cron:nightly`. */
  source?: string;
}

export interface PlaybookRunnerDeps {
  cwd: string;
  inventoryPath?: string;
  ansibleBinary?: string;
  saveTask: (task: GlobalTaskItem) => void;
  broadcast?: (message: unknown) => void;
  spawnFn?: typeof spawn;
}

export interface PlaybookCompletion {
  success: boolean;
  exitCode: number | null;
  error?: string;
}

export interface PlaybookRunOutcome {
  success: boolean;
  taskId?: string;
  task?: GlobalTaskItem;
  error?: string;
  /** HTTP-friendly status code for validation failures. */
  code?: number;
  /** Resolves when the spawned ansible-playbook process exits (absent on validation failure). */
  completion?: Promise<PlaybookCompletion>;
}

export interface PlaybookRunner {
  run(request: PlaybookRunRequest): PlaybookRunOutcome;
  cancel(taskId: string): boolean;
  isActive(taskId: string): boolean;
  activeTaskIds(): string[];
}

export function createPlaybookRunner(deps: PlaybookRunnerDeps): PlaybookRunner {
  const activeProcs = new Map<string, ChildProcess>();
  const saveTask = deps.saveTask;
  const broadcast = deps.broadcast ?? (() => {});
  const spawnFn = deps.spawnFn ?? spawn;
  const cwd = deps.cwd;
  const ansibleBinary = deps.ansibleBinary ?? 'ansible-playbook';
  const inventoryPath = deps.inventoryPath ?? path.resolve(cwd, 'inventory/tailscale_inventory.py');

  function run(request: PlaybookRunRequest): PlaybookRunOutcome {
    const { playbook, target = 'all', checkMode = false, tags, extraVars, source } = request;

    const validation = validatePlaybookName(playbook);
    if (!validation.valid) {
      return { success: false, code: 400, error: validation.error || 'Invalid playbook name' };
    }

    const playbookDir = path.resolve(cwd, 'ansible/playbooks');
    const playbookPath = path.resolve(playbookDir, validation.name);

    if (!playbookPath.startsWith(playbookDir + path.sep)) {
      return { success: false, code: 400, error: 'Path traversal detected in playbook name' };
    }

    if (!fs.existsSync(playbookPath)) {
      return { success: false, code: 404, error: `Playbook ${playbook} not found on filesystem` };
    }

    const taskId = request.taskId || `task-ansible-${Date.now()}`;
    const newTask: GlobalTaskItem = {
      id: taskId,
      startTime: new Date().toISOString().replace('T', ' ').substring(0, 19),
      nodeTarget: target,
      user: 'core@ansible',
      description: `ansible-playbook ${playbook} --limit ${target} ${checkMode ? '(--check)' : ''}${
        source ? ` [${source}]` : ''
      }`.trim(),
      progress: 10,
      progressEta: 'Spawning Ansible runner...',
      status: 'RUNNING',
      category: 'ansible',
      logs: [`[ansible] Starting playbook ${playbook} against ${target}${source ? ` (${source})` : ''}`],
    };

    saveTask(newTask);
    broadcast({ type: 'task_started', task: newTask });

    const args = ['-i', inventoryPath, playbookPath];
    if (target && target !== 'all') {
      args.push('--limit', target);
    }
    if (checkMode) {
      args.push('--check');
    }
    if (tags) {
      args.push('--tags', tags);
    }
    if (extraVars) {
      args.push('--extra-vars', typeof extraVars === 'string' ? extraVars : JSON.stringify(extraVars));
    }

    const proc = spawnFn(ansibleBinary, args, {
      cwd,
      env: { ...process.env, ANSIBLE_FORCE_COLOR: '1' },
    });

    activeProcs.set(taskId, proc);

    let settleCompletion: (completion: PlaybookCompletion) => void = () => {};
    const completion = new Promise<PlaybookCompletion>(resolve => {
      settleCompletion = resolve;
    });

    proc.on('error', (err: any) => {
      activeProcs.delete(taskId);
      settleCompletion({ success: false, exitCode: null, error: err?.message || String(err) });
      newTask.status = 'FAILED';
      newTask.progress = 100;
      newTask.progressEta = `Spawn error: ${err?.code || err?.message || 'Failed to spawn runner'}`;
      newTask.logs = newTask.logs || [];
      newTask.logs.push(`[error] Process failed to spawn: ${err?.message || String(err)}`);
      saveTask(newTask);
      broadcast({ type: 'task_output', taskId, line: `[error] Process failed to spawn: ${err?.message || String(err)}` });
      broadcast({ type: 'task_updated', task: newTask });
    });

    const appendLog = (line: string) => {
      newTask.logs = newTask.logs || [];
      newTask.logs.push(line);
      if (newTask.logs.length > 300) {
        newTask.logs.shift();
      }
      broadcast({ type: 'task_output', taskId, line });

      if (line.includes('PLAY [')) {
        newTask.progress = 25;
        newTask.progressEta = 'Starting plays...';
      } else if (line.includes('TASK [')) {
        newTask.progress = Math.min(85, (newTask.progress || 25) + 5);
        const match = line.match(/TASK \[(.*?)\]/);
        if (match) newTask.progressEta = match[1];
      } else if (line.includes('PLAY RECAP')) {
        newTask.progress = 90;
        newTask.progressEta = 'Finalizing recap...';
      }
      saveTask(newTask);
      broadcast({ type: 'task_updated', task: newTask });
    };

    proc.stdout?.on('data', chunk => {
      const lines = chunk.toString().split('\n');
      for (const l of lines) {
        if (l.trim()) appendLog(l);
      }
    });

    proc.stderr?.on('data', chunk => {
      const lines = chunk.toString().split('\n');
      for (const l of lines) {
        if (l.trim()) appendLog(`[stderr] ${l}`);
      }
    });

    proc.on('close', code => {
      activeProcs.delete(taskId);
      settleCompletion({
        success: code === 0,
        exitCode: code,
        error: code === 0 ? undefined : `ansible-playbook exited with code ${code}`,
      });
      if (newTask.status === 'FAILED') {
        return;
      }
      newTask.progress = 100;
      if (code === 0) {
        newTask.status = 'OK';
        newTask.progressEta = checkMode ? 'Completed dry-run (0 failed)' : 'Completed successfully';
      } else {
        newTask.status = 'FAILED';
        newTask.progressEta = `Failed (exit code ${code})`;
      }
      saveTask(newTask);
      broadcast({ type: 'task_updated', task: newTask });
    });

    return { success: true, taskId, task: newTask, completion };
  }

  function cancel(taskId: string): boolean {
    const proc = activeProcs.get(taskId);
    if (!proc) return false;
    proc.kill('SIGTERM');
    activeProcs.delete(taskId);
    return true;
  }

  return {
    run,
    cancel,
    isActive: taskId => activeProcs.has(taskId),
    activeTaskIds: () => [...activeProcs.keys()],
  };
}
