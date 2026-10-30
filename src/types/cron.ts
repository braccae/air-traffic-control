/**
 * Cron schedule types shared by the UI.
 *
 * Persistence, scheduling and execution all live in the Go daemon
 * (`backend-go/pkg/cronapi`). TypeScript only describes the JSON shapes the UI
 * renders, so this module intentionally contains types and nothing else.
 */
import type { AnsibleTaskStep } from './index.ts';

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
