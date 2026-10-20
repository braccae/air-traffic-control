/**
 * OWNERSHIP NOTE (Ansible mutation queue migration)
 *
 * The Go engine `backend-go/pkg/ansqueue` is now authoritative for queue
 * storage, execution and checkpoint halting. This module remains as the
 * Express fallback until the coordinator is retired (Phase 3): it is kept
 * behaviourally unchanged and is not the source of truth for new work.
 */
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { DatabaseSync } from 'node:sqlite';
import type { QueuedAnsibleTask } from '../types/index.ts';
import { compileOnTheFlyPlaybook, generateTaskInventory } from './ansibleQueueCompiler.ts';
import { beginConnection } from './connectionRegistry.ts';

export interface QueueExecutionResult {
  success: boolean;
  taskId: string;
  changed: boolean;
  failed: boolean;
  jsonOutput: any;
  error?: string;
  durationMs?: number;
}

export interface ExecuteQueueOptions {
  ansibleBinary?: string;
  env?: Record<string, string>;
  cwd?: string;
  taskId?: string;
  /**
   * Ephemeral YAML vars file (ansible-vault ciphertext) built from the vault
   * store. Passed as `--extra-vars @path`; the caller owns its cleanup.
   */
  vaultVarsFile?: string;
  execCommand?: (
    command: string,
    args: string[],
    options: { cwd?: string; env?: Record<string, string> }
  ) => Promise<{ stdout: string; stderr: string; exitCode: number }>;
}

let dbInstance: DatabaseSync | null = null;
let memoryQueue: QueuedAnsibleTask[] = [];
let queueIsRunning = false;

/**
 * Parses Ansible JSON callback stdout, safely extracting the outer JSON object
 * even when surrounded by warnings, deprecation notices, or terminal noise.
 */
export function parseAnsibleJsonStdout(stdout: string): any {
  if (!stdout || typeof stdout !== 'string') {
    throw new Error('Ansible stdout is empty or not a string');
  }

  const trimmed = stdout.trim();
  try {
    const direct = JSON.parse(trimmed);
    if (direct && typeof direct === 'object' && Array.isArray(direct.plays)) {
      return direct;
    }
  } catch {
    // Fall back to robust slice extraction
  }

  // Find JSON object containing "plays"
  const playsIndex = stdout.indexOf('"plays"');
  if (playsIndex === -1) {
    throw new Error('No JSON object found in Ansible stdout (missing plays)');
  }

  const openBrace = stdout.lastIndexOf('{', playsIndex);
  if (openBrace === -1) {
    throw new Error('No JSON object found in Ansible stdout');
  }

  let depth = 0;
  let inString = false;
  let escape = false;

  for (let i = openBrace; i < stdout.length; i++) {
    const char = stdout[i];
    if (escape) {
      escape = false;
      continue;
    }
    if (char === '\\') {
      escape = true;
      continue;
    }
    if (char === '"') {
      inString = !inString;
      continue;
    }
    if (!inString) {
      if (char === '{') {
        depth++;
      } else if (char === '}') {
        depth--;
        if (depth === 0) {
          const jsonSlice = stdout.slice(openBrace, i + 1);
          try {
            const parsed = JSON.parse(jsonSlice);
            if (parsed && typeof parsed === 'object' && Array.isArray(parsed.plays)) {
              return parsed;
            }
          } catch (err: any) {
            throw new Error(`Failed to parse Ansible JSON output: ${err.message}`);
          }
        }
      }
    }
  }

  throw new Error('Failed to parse Ansible JSON output');
}

/**
 * Initializes or binds the SQLite persistence database for queued Ansible tasks.
 */
export function initQueueStore(dbOrPath?: DatabaseSync | string): DatabaseSync {
  if (dbOrPath instanceof DatabaseSync) {
    dbInstance = dbOrPath;
  } else {
    const targetPath = dbOrPath || process.env.HOST_CACHE_DB_PATH || path.resolve(process.cwd(), 'data/hosts-cache.sqlite');
    if (targetPath !== ':memory:') {
      const dir = path.dirname(targetPath);
      if (!fs.existsSync(dir)) {
        fs.mkdirSync(dir, { recursive: true });
      }
    }
    dbInstance = new DatabaseSync(targetPath);
  }

  dbInstance.exec(`
    PRAGMA journal_mode = WAL;
    PRAGMA synchronous = NORMAL;

    CREATE TABLE IF NOT EXISTS ansible_queue_tasks (
      id TEXT PRIMARY KEY,
      title TEXT NOT NULL,
      target_node_id TEXT NOT NULL,
      target_node_name TEXT NOT NULL,
      target_host_route TEXT NOT NULL,
      target_ssh_user TEXT NOT NULL,
      target_ssh_port INTEGER NOT NULL,
      category TEXT NOT NULL,
      primary_module TEXT NOT NULL,
      become INTEGER NOT NULL DEFAULT 1,
      become_method TEXT NOT NULL DEFAULT 'run0',
      status TEXT NOT NULL DEFAULT 'staged',
      created_at INTEGER NOT NULL,
      task_json TEXT NOT NULL,
      error TEXT,
      updated_at INTEGER NOT NULL
    );

    CREATE INDEX IF NOT EXISTS idx_ansible_queue_status ON ansible_queue_tasks(status, created_at ASC);
  `);

  // Recover running tasks from previous crash / server restart in SQLite
  dbInstance.exec(`
    UPDATE ansible_queue_tasks
    SET status = 'failed',
        error = 'Task execution interrupted by server restart',
        updated_at = ${Date.now()}
    WHERE status = 'running'
  `);

  // Load existing tasks into memoryQueue
  const stmt = dbInstance.prepare('SELECT task_json, status, error FROM ansible_queue_tasks ORDER BY created_at ASC');
  const rows = stmt.all() as Array<{ task_json: string; status: string; error?: string }>;
  
  memoryQueue = rows.map(r => {
    try {
      const task = JSON.parse(r.task_json) as QueuedAnsibleTask;
      task.status = r.status as any;
      if (r.error) task.error = r.error;
      return task;
    } catch {
      return null;
    }
  }).filter((t): t is QueuedAnsibleTask => t !== null);

  queueIsRunning = false;
  return dbInstance;
}

function ensureDb(): DatabaseSync {
  if (!dbInstance) {
    initQueueStore();
  }
  return dbInstance!;
}

/**
 * Returns current snapshot of the task queue and running state.
 */
export function getQueue(): { tasks: QueuedAnsibleTask[]; isRunning: boolean } {
  ensureDb();
  return {
    tasks: [...memoryQueue],
    isRunning: queueIsRunning,
  };
}

/**
 * Enqueues a task in-memory and persists it into SQLite.
 */
export function enqueueTask(task: QueuedAnsibleTask): QueuedAnsibleTask {
  const db = ensureDb();
  const normalizedTask: QueuedAnsibleTask = {
    ...task,
    id: task.id || `task-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
    title: task.title || 'Ansible Task',
    targetNodeId: task.targetNodeId || 'localhost',
    targetNodeName: task.targetNodeName || task.targetNodeId || 'localhost',
    targetHostRoute: task.targetHostRoute || task.targetNodeName || task.targetNodeId || '127.0.0.1',
    targetSshUser: task.targetSshUser || 'core',
    targetSshPort: task.targetSshPort || 22,
    category: task.category || 'system',
    primaryModule: task.primaryModule || task.steps?.[0]?.module || 'ansible.builtin.command',
    steps: task.steps || [],
    become: task.become !== undefined ? Boolean(task.become) : true,
    becomeMethod: (task.becomeMethod as any) || 'run0',
    status: task.status || 'staged',
    createdAt: task.createdAt || Date.now(),
  };

  const idx = memoryQueue.findIndex(t => t.id === normalizedTask.id);
  if (idx >= 0) {
    memoryQueue[idx] = { ...normalizedTask };
  } else {
    memoryQueue.push({ ...normalizedTask });
  }

  const stmt = db.prepare(`
    INSERT INTO ansible_queue_tasks (
      id, title, target_node_id, target_node_name, target_host_route,
      target_ssh_user, target_ssh_port, category, primary_module,
      become, become_method, status, created_at, task_json, error, updated_at
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    ON CONFLICT(id) DO UPDATE SET
      title = excluded.title,
      target_node_id = excluded.target_node_id,
      target_node_name = excluded.target_node_name,
      target_host_route = excluded.target_host_route,
      target_ssh_user = excluded.target_ssh_user,
      target_ssh_port = excluded.target_ssh_port,
      category = excluded.category,
      primary_module = excluded.primary_module,
      become = excluded.become,
      become_method = excluded.become_method,
      status = excluded.status,
      task_json = excluded.task_json,
      error = excluded.error,
      updated_at = excluded.updated_at
  `);

  stmt.run(
    normalizedTask.id,
    normalizedTask.title,
    normalizedTask.targetNodeId,
    normalizedTask.targetNodeName,
    normalizedTask.targetHostRoute || '',
    normalizedTask.targetSshUser || 'core',
    normalizedTask.targetSshPort || 22,
    normalizedTask.category,
    normalizedTask.primaryModule,
    normalizedTask.become ? 1 : 0,
    normalizedTask.becomeMethod || 'run0',
    normalizedTask.status,
    normalizedTask.createdAt,
    JSON.stringify(normalizedTask),
    normalizedTask.error || null,
    Date.now()
  );

  return normalizedTask;
}

/**
 * Removes a task by ID from memory and SQLite.
 */
export function removeTask(taskId: string): boolean {
  const db = ensureDb();
  const idx = memoryQueue.findIndex(t => t.id === taskId);
  if (idx === -1) {
    return false;
  }

  memoryQueue.splice(idx, 1);
  const stmt = db.prepare('DELETE FROM ansible_queue_tasks WHERE id = ?');
  stmt.run(taskId);
  return true;
}

/**
 * Clears all non-running tasks from queue and SQLite.
 */
export function clearQueue(): void {
  const db = ensureDb();
  memoryQueue = memoryQueue.filter(t => t.status === 'running');
  const stmt = db.prepare('DELETE FROM ansible_queue_tasks WHERE status != ?');
  stmt.run('running');
}

/**
 * Updates status and optional error message of a task in memory and SQLite.
 */
export function updateTaskStatus(
  taskId: string,
  status: QueuedAnsibleTask['status'],
  error?: string
): QueuedAnsibleTask | undefined {
  const db = ensureDb();
  const task = memoryQueue.find(t => t.id === taskId);
  if (!task) {
    return undefined;
  }

  task.status = status;
  if (error !== undefined) {
    task.error = error;
  }
  task.executedAt = Date.now();

  const stmt = db.prepare(`
    UPDATE ansible_queue_tasks
    SET status = ?, error = ?, task_json = ?, updated_at = ?
    WHERE id = ?
  `);
  stmt.run(status, task.error || null, JSON.stringify(task), Date.now(), taskId);

  return task;
}

/**
 * Executes a single QueuedAnsibleTask by generating an ephemeral playbook,
 * running it with ANSIBLE_STDOUT_CALLBACK=json, parsing output, and cleaning up.
 */
export async function executeQueueTaskOneByOne(
  task: QueuedAnsibleTask,
  inventoryPath?: string,
  options?: ExecuteQueueOptions
): Promise<QueueExecutionResult> {
  const startTime = Date.now();
  const cwd = options?.cwd || process.cwd();
  const playbooksDir = path.resolve(cwd, 'ansible/playbooks');

  if (!fs.existsSync(playbooksDir)) {
    fs.mkdirSync(playbooksDir, { recursive: true });
  }

  const safeId = task.id.replace(/[^a-zA-Z0-9_-]/g, '_');
  const ephemeralFile = path.join(playbooksDir, `.queue-${safeId}.yml`);
  const playbookYaml = compileOnTheFlyPlaybook(task);
  fs.writeFileSync(ephemeralFile, playbookYaml, 'utf-8');

  let ephemeralInventoryFile: string | null = null;

  try {
    let invPath = inventoryPath;
    if (!invPath) {
      if (task.connection) {
        // Generate temporary per-task inventory from the connection block
        const inventoryDir = path.resolve(cwd, 'ansible/inventory');
        if (!fs.existsSync(inventoryDir)) {
          fs.mkdirSync(inventoryDir, { recursive: true });
        }
        ephemeralInventoryFile = path.join(inventoryDir, `.inv-${safeId}.json`);
        fs.writeFileSync(ephemeralInventoryFile, generateTaskInventory(task), 'utf-8');
        invPath = ephemeralInventoryFile;
      } else {
        const defaultInventory = path.resolve(cwd, 'inventory/tailscale_inventory.py');
        if (fs.existsSync(defaultInventory)) {
          invPath = defaultInventory;
        } else {
          invPath = `${task.targetHostRoute},`;
        }
      }
    }

    const args = ['-i', invPath];
    if ((task.targetNodeName || task.targetNodeId) && !invPath.endsWith(',')) {
      args.push('--limit', task.targetNodeName || task.targetNodeId);
    }
    args.push(ephemeralFile);

    if (options?.vaultVarsFile) {
      args.push('--extra-vars', `@${options.vaultVarsFile}`);
    }

    const env: Record<string, string> = {
      ...(process.env as Record<string, string>),
      ANSIBLE_STDOUT_CALLBACK: 'json',
      ...options?.env,
    };

    const configCandidates = [
      path.resolve(cwd, 'ansible/ansible.cfg'),
      path.resolve(cwd, 'ansible.cfg'),
    ];
    for (const cfg of configCandidates) {
      if (fs.existsSync(cfg)) {
        env.ANSIBLE_CONFIG = cfg;
        break;
      }
    }

    const ansibleBin = options?.ansibleBinary || 'ansible-playbook';

    let stdout = '';
    let stderr = '';
    let exitCode = 0;

    // WinRM tasks hold a real WinRM session for the lifetime of the playbook run.
    const releaseWinrm =
      task.connection?.plugin === 'connection/winrm'
        ? beginConnection('winrm', task.targetNodeName || task.targetNodeId || task.targetHostRoute || 'winrm')
        : null;

    try {
      if (options?.execCommand) {
        const res = await options.execCommand(ansibleBin, args, { cwd, env });
        stdout = res.stdout;
        stderr = res.stderr;
        exitCode = res.exitCode;
      } else {
        const procResult = await new Promise<{ stdout: string; stderr: string; exitCode: number }>(resolve => {
          const proc = spawn(ansibleBin, args, { cwd, env });
          let out = '';
          let err = '';
          proc.stdout?.on('data', chunk => {
            out += chunk.toString();
          });
          proc.stderr?.on('data', chunk => {
            err += chunk.toString();
          });
          proc.on('error', spawnErr => {
            resolve({ stdout: out, stderr: spawnErr.message || String(spawnErr), exitCode: 1 });
          });
          proc.on('close', code => {
            resolve({ stdout: out, stderr: err, exitCode: code ?? 0 });
          });
        });
        stdout = procResult.stdout;
        stderr = procResult.stderr;
        exitCode = procResult.exitCode;
      }
    } finally {
      releaseWinrm?.();
    }

    let jsonOutput: any = null;
    try {
      jsonOutput = parseAnsibleJsonStdout(stdout);
    } catch (parseErr: any) {
      return {
        success: false,
        taskId: task.id,
        changed: false,
        failed: true,
        jsonOutput: null,
        error: stderr.trim() || parseErr.message || 'Failed to parse Ansible JSON output',
        durationMs: Date.now() - startTime,
      };
    }

    let changed = false;
    let failed = false;
    let error: string | undefined;

    // Inspect stats block if present
    if (jsonOutput.stats && typeof jsonOutput.stats === 'object') {
      for (const host of Object.keys(jsonOutput.stats)) {
        const stat = jsonOutput.stats[host];
        if (stat.failures > 0 || stat.unreachable > 0) {
          failed = true;
        }
        if (stat.changed > 0) {
          changed = true;
        }
      }
    }

    // Inspect plays and tasks for task-level errors and changes
    if (Array.isArray(jsonOutput.plays)) {
      for (const play of jsonOutput.plays) {
        if (Array.isArray(play.tasks)) {
          for (const stepTask of play.tasks) {
            if (stepTask.hosts && typeof stepTask.hosts === 'object') {
              for (const host of Object.keys(stepTask.hosts)) {
                const hostRes = stepTask.hosts[host];
                if (hostRes.failed === true || hostRes.unreachable === true) {
                  failed = true;
                  if (!error) {
                    error = hostRes.msg || hostRes.stderr || hostRes.exception || (typeof hostRes === 'string' ? hostRes : undefined);
                  }
                }
                if (hostRes.changed === true) {
                  changed = true;
                }
              }
            }
          }
        }
      }
    }

    if (exitCode !== 0) {
      failed = true;
      if (!error) {
        error = stderr.trim() || `Ansible playbook exited with code ${exitCode}`;
      }
    }

    const success = !failed && exitCode === 0;

    return {
      success,
      taskId: task.id,
      changed,
      failed,
      jsonOutput,
      error,
      durationMs: Date.now() - startTime,
    };
  } finally {
    try {
      if (fs.existsSync(ephemeralFile)) {
        fs.unlinkSync(ephemeralFile);
      }
    } catch {
      // Ignore cleanup error
    }
    try {
      if (ephemeralInventoryFile && fs.existsSync(ephemeralInventoryFile)) {
        fs.unlinkSync(ephemeralInventoryFile);
      }
    } catch {
      // Ignore cleanup error
    }
  }
}

/**
 * Executes the next staged task in the queue, enforcing step-by-step
 * one-by-one execution and immediately halting on any failure.
 */
export async function executeNextQueueTask(
  inventoryPath?: string,
  options?: ExecuteQueueOptions
): Promise<{
  result: QueueExecutionResult | null;
  remainingCount: number;
  haltedOnError: boolean;
}> {
  ensureDb();

  if (queueIsRunning) {
    return {
      result: null,
      remainingCount: memoryQueue.filter(t => t.status === 'staged').length,
      haltedOnError: false,
    };
  }

  let targetTask: QueuedAnsibleTask | undefined;
  if (options?.taskId) {
    targetTask = memoryQueue.find(t => t.id === options.taskId && t.status === 'staged');
  } else {
    targetTask = memoryQueue.find(t => t.status === 'staged');
  }

  if (!targetTask) {
    return {
      result: null,
      remainingCount: memoryQueue.filter(t => t.status === 'staged').length,
      haltedOnError: false,
    };
  }

  queueIsRunning = true;
  updateTaskStatus(targetTask.id, 'running');

  try {
    const result = await executeQueueTaskOneByOne(targetTask, inventoryPath, options);

    if (result.success && !result.failed) {
      updateTaskStatus(targetTask.id, 'completed');
      queueIsRunning = false;
      const remainingCount = memoryQueue.filter(t => t.status === 'staged').length;
      return {
        result,
        remainingCount,
        haltedOnError: false,
      };
    } else {
      const errText = result.error || 'Ansible task execution failed';
      updateTaskStatus(targetTask.id, 'failed', errText);
      queueIsRunning = false;
      // Enforce checkpoint halt: stop immediately, do not proceed to next task
      const remainingCount = memoryQueue.filter(t => t.status === 'staged').length;
      return {
        result,
        remainingCount,
        haltedOnError: true,
      };
    }
  } catch (err: any) {
    const errMsg = err?.message || String(err);
    const failResult: QueueExecutionResult = {
      success: false,
      taskId: targetTask.id,
      changed: false,
      failed: true,
      jsonOutput: null,
      error: errMsg,
    };
    updateTaskStatus(targetTask.id, 'failed', errMsg);
    queueIsRunning = false;
    const remainingCount = memoryQueue.filter(t => t.status === 'staged').length;
    return {
      result: failResult,
      remainingCount,
      haltedOnError: true,
    };
  }
}
