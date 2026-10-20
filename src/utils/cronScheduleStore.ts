/**
 * Persistent store for ATC cron schedules.
 *
 * Schedules live in the coordinator's SQLite database (`data/hosts-cache.sqlite`,
 * table `cron_schedules`) so they survive restarts. Execution is driven by the
 * in-process ATC scheduler (see cronScheduler.ts / docs/CRON_SCHEDULES.md) — no
 * cron daemon or systemd timer is installed on target hosts.
 */
import fs from 'node:fs';
import path from 'node:path';
import { DatabaseSync } from 'node:sqlite';
import type { AnsibleTaskStep, NodeItem } from '../types/index.ts';
import { nextCronRun, validateCronExpression } from './cronExpression.ts';
import { validatePlaybookName } from './playbookManager.ts';

export type CronScopeKind = 'host' | 'group' | 'datacenter';
export type CronTaskKind = 'playbook' | 'ansible_steps';
export type CronRunStatus = 'OK' | 'FAILED' | 'RUNNING';

export interface CronScheduleScope {
  kind: CronScopeKind;
  /** Host id/shortName for `host`, tag (with or without `tag:` prefix) for `group`, datacenter label or 'all' for `datacenter`. */
  target: string;
}

export interface CronPlaybookPayload {
  playbook: string;
  /** Optional --limit override; defaults to the scope-derived target. */
  limit?: string;
  checkMode?: boolean;
  tags?: string;
  extraVars?: string | Record<string, unknown>;
}

export interface CronAnsibleStepsPayload {
  title: string;
  category?: string;
  steps: AnsibleTaskStep[];
  become?: boolean;
  becomeMethod?: 'run0' | 'sudo' | 'doas' | 'none';
}

export type CronTaskPayload = CronPlaybookPayload | CronAnsibleStepsPayload;

export interface CronSchedule {
  id: string;
  name: string;
  cronExpression: string;
  enabled: boolean;
  scope: CronScheduleScope;
  taskKind: CronTaskKind;
  payload: CronTaskPayload;
  createdAt: number;
  updatedAt: number;
  nextRunAt: number | null;
  lastRunAt?: number;
  lastRunStatus?: CronRunStatus;
  lastRunTaskId?: string;
  lastRunError?: string;
}

export interface CronScheduleInput {
  name: string;
  cronExpression: string;
  enabled?: boolean;
  scope: CronScheduleScope;
  taskKind: CronTaskKind;
  payload: CronTaskPayload;
}

let dbInstance: DatabaseSync | null = null;

function ensureDb(): DatabaseSync {
  if (!dbInstance) {
    initCronScheduleStore();
  }
  return dbInstance!;
}

export function initCronScheduleStore(dbOrPath?: DatabaseSync | string): DatabaseSync {
  if (dbOrPath instanceof DatabaseSync) {
    dbInstance = dbOrPath;
  } else {
    const targetPath =
      dbOrPath || process.env.HOST_CACHE_DB_PATH || path.resolve(process.cwd(), 'data/hosts-cache.sqlite');
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

    CREATE TABLE IF NOT EXISTS cron_schedules (
      id TEXT PRIMARY KEY,
      name TEXT NOT NULL,
      cron_expression TEXT NOT NULL,
      enabled INTEGER NOT NULL DEFAULT 1,
      scope_kind TEXT NOT NULL,
      scope_target TEXT NOT NULL,
      task_kind TEXT NOT NULL,
      payload_json TEXT NOT NULL,
      created_at INTEGER NOT NULL,
      updated_at INTEGER NOT NULL,
      next_run_at INTEGER,
      last_run_at INTEGER,
      last_run_status TEXT,
      last_run_task_id TEXT,
      last_run_error TEXT
    );

    CREATE INDEX IF NOT EXISTS idx_cron_schedules_due ON cron_schedules(enabled, next_run_at);
  `);

  return dbInstance;
}

interface CronRow {
  id: string;
  name: string;
  cron_expression: string;
  enabled: number;
  scope_kind: string;
  scope_target: string;
  task_kind: string;
  payload_json: string;
  created_at: number;
  updated_at: number;
  next_run_at: number | null;
  last_run_at: number | null;
  last_run_status: string | null;
  last_run_task_id: string | null;
  last_run_error: string | null;
}

function rowToSchedule(row: CronRow): CronSchedule {
  let payload: CronTaskPayload;
  try {
    payload = JSON.parse(row.payload_json);
  } catch {
    payload = { title: row.name, steps: [] } as CronAnsibleStepsPayload;
  }

  return {
    id: row.id,
    name: row.name,
    cronExpression: row.cron_expression,
    enabled: row.enabled === 1,
    scope: { kind: row.scope_kind as CronScopeKind, target: row.scope_target },
    taskKind: row.task_kind as CronTaskKind,
    payload,
    createdAt: row.created_at,
    updatedAt: row.updated_at,
    nextRunAt: row.next_run_at ?? null,
    lastRunAt: row.last_run_at ?? undefined,
    lastRunStatus: (row.last_run_status as CronRunStatus) || undefined,
    lastRunTaskId: row.last_run_task_id || undefined,
    lastRunError: row.last_run_error || undefined,
  };
}

export function validateCronScheduleInput(input: CronScheduleInput): string | null {
  if (!input || typeof input !== 'object') return 'Schedule payload is required';

  if (typeof input.name !== 'string' || input.name.trim() === '') {
    return 'Schedule name is required';
  }

  const cronCheck = validateCronExpression(input.cronExpression);
  if (!cronCheck.valid) {
    return `Invalid cron expression: ${cronCheck.error}`;
  }

  if (!input.scope || typeof input.scope !== 'object') return 'Schedule scope is required';
  if (!['host', 'group', 'datacenter'].includes(input.scope.kind)) {
    return `Invalid scope kind "${input.scope.kind}" — expected host, group, or datacenter`;
  }
  if (typeof input.scope.target !== 'string') return 'Scope target must be a string';
  if (input.scope.kind !== 'datacenter' && input.scope.target.trim() === '') {
    return `Scope target is required for ${input.scope.kind} scopes`;
  }

  if (input.taskKind !== 'playbook' && input.taskKind !== 'ansible_steps') {
    return `Invalid task kind "${input.taskKind}" — expected playbook or ansible_steps`;
  }

  if (!input.payload || typeof input.payload !== 'object') return 'Schedule payload is required';

  if (input.taskKind === 'playbook') {
    const payload = input.payload as CronPlaybookPayload;
    if (typeof payload.playbook !== 'string' || payload.playbook.trim() === '') {
      return 'Playbook name is required for playbook schedules';
    }
    const check = validatePlaybookName(payload.playbook);
    if (!check.valid) return check.error || 'Invalid playbook name';
  } else {
    const payload = input.payload as CronAnsibleStepsPayload;
    if (typeof payload.title !== 'string' || payload.title.trim() === '') {
      return 'Title is required for ansible_steps schedules';
    }
    if (!Array.isArray(payload.steps) || payload.steps.length === 0) {
      return 'At least one Ansible step is required for ansible_steps schedules';
    }
    for (const [idx, step] of payload.steps.entries()) {
      if (!step || typeof step.module !== 'string' || step.module.trim() === '') {
        return `Step ${idx + 1} is missing an Ansible module`;
      }
    }
  }

  return null;
}

export function normalizeCronScheduleInput(input: CronScheduleInput): CronScheduleInput {
  return {
    name: input.name.trim(),
    cronExpression: validateCronExpression(input.cronExpression).normalized || input.cronExpression.trim(),
    enabled: input.enabled !== false,
    scope: {
      kind: input.scope.kind,
      target: input.scope.kind === 'datacenter' && !input.scope.target?.trim() ? 'all' : input.scope.target.trim(),
    },
    taskKind: input.taskKind,
    payload: input.payload,
  };
}

function computeNextRun(cronExpression: string, from: number): number | null {
  return nextCronRun(cronExpression, from);
}

export function listSchedules(): CronSchedule[] {
  const db = ensureDb();
  const rows = db.prepare('SELECT * FROM cron_schedules ORDER BY created_at ASC').all() as unknown as CronRow[];
  return rows.map(rowToSchedule);
}

export function getSchedule(id: string): CronSchedule | null {
  const db = ensureDb();
  const row = db.prepare('SELECT * FROM cron_schedules WHERE id = ?').get(id) as unknown as CronRow | undefined;
  return row ? rowToSchedule(row) : null;
}

export function createSchedule(
  input: CronScheduleInput,
  options: { now?: number; id?: string } = {}
): CronSchedule {
  const db = ensureDb();
  const error = validateCronScheduleInput(input);
  if (error) throw new Error(error);

  const normalized = normalizeCronScheduleInput(input);
  const now = options.now ?? Date.now();
  const id = options.id || `cron-${now}-${Math.random().toString(36).slice(2, 8)}`;
  const nextRunAt = normalized.enabled ? computeNextRun(normalized.cronExpression, now) : null;

  db.prepare(`
    INSERT INTO cron_schedules (
      id, name, cron_expression, enabled, scope_kind, scope_target, task_kind,
      payload_json, created_at, updated_at, next_run_at
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
  `).run(
    id,
    normalized.name,
    normalized.cronExpression,
    normalized.enabled ? 1 : 0,
    normalized.scope.kind,
    normalized.scope.target,
    normalized.taskKind,
    JSON.stringify(normalized.payload),
    now,
    now,
    nextRunAt
  );

  return getSchedule(id)!;
}

export function updateSchedule(
  id: string,
  patch: Partial<CronScheduleInput>,
  options: { now?: number } = {}
): CronSchedule | null {
  const db = ensureDb();
  const existing = getSchedule(id);
  if (!existing) return null;

  const merged: CronScheduleInput = {
    name: patch.name ?? existing.name,
    cronExpression: patch.cronExpression ?? existing.cronExpression,
    enabled: patch.enabled ?? existing.enabled,
    scope: patch.scope ?? existing.scope,
    taskKind: patch.taskKind ?? existing.taskKind,
    payload: patch.payload ?? existing.payload,
  };

  const error = validateCronScheduleInput(merged);
  if (error) throw new Error(error);

  const normalized = normalizeCronScheduleInput(merged);
  const now = options.now ?? Date.now();
  const nextRunAt = normalized.enabled ? computeNextRun(normalized.cronExpression, now) : null;

  db.prepare(`
    UPDATE cron_schedules
    SET name = ?, cron_expression = ?, enabled = ?, scope_kind = ?, scope_target = ?,
        task_kind = ?, payload_json = ?, updated_at = ?, next_run_at = ?
    WHERE id = ?
  `).run(
    normalized.name,
    normalized.cronExpression,
    normalized.enabled ? 1 : 0,
    normalized.scope.kind,
    normalized.scope.target,
    normalized.taskKind,
    JSON.stringify(normalized.payload),
    now,
    nextRunAt,
    id
  );

  return getSchedule(id);
}

export function removeSchedule(id: string): boolean {
  const db = ensureDb();
  const result = db.prepare('DELETE FROM cron_schedules WHERE id = ?').run(id);
  return Number(result.changes) > 0;
}

export function setScheduleNextRun(id: string, nextRunAt: number | null): void {
  const db = ensureDb();
  db.prepare('UPDATE cron_schedules SET next_run_at = ?, updated_at = ? WHERE id = ?').run(
    nextRunAt,
    Date.now(),
    id
  );
}

export function recordScheduleRun(
  id: string,
  run: { status: CronRunStatus; at?: number; taskId?: string; error?: string }
): void {
  const db = ensureDb();
  const at = run.at ?? Date.now();
  db.prepare(`
    UPDATE cron_schedules
    SET last_run_at = ?, last_run_status = ?, last_run_task_id = ?, last_run_error = ?, updated_at = ?
    WHERE id = ?
  `).run(at, run.status, run.taskId || null, run.error || null, at, id);
}

export function listDueSchedules(now: number = Date.now()): CronSchedule[] {
  const db = ensureDb();
  const rows = db
    .prepare('SELECT * FROM cron_schedules WHERE enabled = 1 AND next_run_at IS NOT NULL AND next_run_at <= ?')
    .all(now) as unknown as CronRow[];
  return rows.map(rowToSchedule);
}

/**
 * Resolves the target nodes for a schedule scope using the live node list.
 * Used by ansible_steps schedules (which need one real connection per node)
 * and by playbook schedules to derive a default --limit.
 */
export function resolveScopeTargets(
  scope: CronScheduleScope,
  nodes: NodeItem[]
): { limit: string; nodes: NodeItem[] } {
  const target = (scope.target || '').trim();
  const tagName = target.replace(/^tag:/, '');

  if (scope.kind === 'datacenter') {
    return { limit: target && target !== 'all' ? target : 'all', nodes };
  }

  if (scope.kind === 'group') {
    const groupNodes = nodes.filter(node =>
      (node.tags || []).some(tag => tag.replace(/^tag:/, '') === tagName) ||
      (node.tailscaleTag || '').replace(/^tag:/, '') === tagName
    );
    return { limit: `tag:${tagName}`, nodes: groupNodes };
  }

  const hostNodes = nodes.filter(
    node => node.id === target || node.shortName === target || node.name === target
  );
  return { limit: target, nodes: hostNodes };
}
