/**
 * Executes a cron schedule against real infrastructure.
 *
 * Two task kinds are supported:
 *  - `playbook`      — spawns a real `ansible-playbook` run through the shared
 *                      playbook runner (same path as POST /api/playbook/run).
 *  - `ansible_steps` — compiles declarative steps into the existing on-the-fly
 *                      Ansible mutation queue (one real task per target node)
 *                      and executes them one-by-one with checkpoint halting.
 *
 * Both land in the coordinator's existing background task system (GlobalTaskItem
 * console tasks and/or ansible_queue_tasks) so results and logs stay visible.
 */
import path from 'node:path';
import type {
  AnsibleTaskStep,
  GlobalTaskItem,
  NodeItem,
  QueuedAnsibleTask,
} from '../types/index.ts';
import {
  resolveBecomeMethod,
  resolveNodeConnection,
} from './ansibleQueueCompiler.ts';
import type { QueueExecutionResult } from './ansibleQueueRunner.ts';
import type { PlaybookCompletion, PlaybookRunner } from './playbookRunner.ts';
import {
  resolveScopeTargets,
  type CronAnsibleStepsPayload,
  type CronPlaybookPayload,
  type CronSchedule,
} from './cronScheduleStore.ts';

export interface CronExecutorDeps {
  cwd: string;
  inventoryPath?: string;
  playbookRunner: PlaybookRunner;
  listNodes: () => NodeItem[];
  enqueueTask: (task: QueuedAnsibleTask) => QueuedAnsibleTask;
  executeQueuedTask: (
    inventoryPath: string | undefined,
    options: { cwd: string; taskId: string }
  ) => Promise<{ result: QueueExecutionResult | null; haltedOnError: boolean }>;
  saveTask?: (task: GlobalTaskItem) => void;
  broadcast?: (message: unknown) => void;
  now?: () => number;
}

export interface CronExecutionOutcome {
  status: 'OK' | 'FAILED';
  taskId?: string;
  error?: string;
}

function describeResult(jsonOutput: any): string[] {
  const lines: string[] = [];
  if (!jsonOutput || typeof jsonOutput !== 'object') {
    return ['[ansible] No JSON callback output captured'];
  }
  if (jsonOutput.stats && typeof jsonOutput.stats === 'object') {
    for (const host of Object.keys(jsonOutput.stats)) {
      const stat = jsonOutput.stats[host];
      lines.push(
        `[recap] ${host}: ok=${stat.ok ?? 0} changed=${stat.changed ?? 0} failed=${stat.failures ?? 0} unreachable=${stat.unreachable ?? 0}`
      );
    }
  }
  if (Array.isArray(jsonOutput.plays)) {
    for (const play of jsonOutput.plays) {
      if (!Array.isArray(play.tasks)) continue;
      for (const task of play.tasks) {
        const name = task.task?.name || task.task?.action || 'task';
        for (const host of Object.keys(task.hosts || {})) {
          const res = task.hosts[host];
          if (res && typeof res === 'object') {
            const state = res.failed ? 'FAILED' : res.changed ? 'changed' : 'ok';
            lines.push(`[ansible] ${name} on ${host}: ${state}${res.msg ? ` — ${res.msg}` : ''}`);
          }
        }
      }
    }
  }
  return lines;
}

export function createCronExecutor(deps: CronExecutorDeps) {
  const now = deps.now ?? Date.now;
  const saveTask = deps.saveTask ?? (() => {});
  const broadcast = deps.broadcast ?? (() => {});
  const inventoryPath =
    deps.inventoryPath ?? path.resolve(deps.cwd, 'inventory/tailscale_inventory.py');

  function runPlaybookSchedule(schedule: CronSchedule): Promise<CronExecutionOutcome> {
    const payload = schedule.payload as CronPlaybookPayload;
    const { limit } = resolveScopeTargets(schedule.scope, deps.listNodes());
    const target = payload.limit?.trim() || limit;

    const outcome = deps.playbookRunner.run({
      playbook: payload.playbook,
      target,
      checkMode: payload.checkMode,
      tags: payload.tags,
      extraVars: payload.extraVars,
      source: `cron:${schedule.name}`,
    });

    if (!outcome.success) {
      return Promise.resolve({
        status: 'FAILED',
        error: outcome.error || 'Failed to start playbook run',
      });
    }

    const completionPromise: Promise<PlaybookCompletion> =
      outcome.completion ?? Promise.resolve({ success: true as const, exitCode: 0 });

    return completionPromise.then(completion => ({
      status: completion.success ? ('OK' as const) : ('FAILED' as const),
      taskId: outcome.taskId,
      error: completion.error,
    }));
  }

  async function runStepsSchedule(schedule: CronSchedule): Promise<CronExecutionOutcome> {
    const payload = schedule.payload as CronAnsibleStepsPayload;
    const { nodes, limit } = resolveScopeTargets(schedule.scope, deps.listNodes());

    if (nodes.length === 0) {
      return {
        status: 'FAILED',
        error: `No target nodes matched scope ${schedule.scope.kind}:${schedule.scope.target || limit}`,
      };
    }

    const startedAt = now();
    const globalTaskId = `task-cron-${schedule.id}-${startedAt}`;
    const globalTask: GlobalTaskItem = {
      id: globalTaskId,
      startTime: new Date(startedAt).toISOString().replace('T', ' ').substring(0, 19),
      nodeTarget: limit,
      user: 'core@ansible',
      description: `cron:${schedule.name} — ${payload.title} (${nodes.length} node${nodes.length === 1 ? '' : 's'})`,
      progress: 5,
      progressEta: 'Staging Ansible tasks...',
      status: 'RUNNING',
      category: 'ansible',
      logs: [`[cron] Staging ${payload.steps.length} step(s) across ${nodes.length} node(s)`],
    };
    saveTask(globalTask);
    broadcast({ type: 'task_started', task: globalTask });

    const results: Array<{ node: NodeItem; taskId: string; result: QueueExecutionResult | null }> = [];
    let failure: string | undefined;

    for (const [idx, node] of nodes.entries()) {
      const connection = resolveNodeConnection(node);
      const steps: AnsibleTaskStep[] = payload.steps;
      const task: QueuedAnsibleTask = {
        id: `task-cron-${schedule.id}-${startedAt}-${idx}`,
        title: `${payload.title} (${node.shortName || node.name || node.id})`,
        targetNodeId: node.id,
        targetNodeName: node.shortName || node.name || node.id,
        targetHostRoute: connection.hostRoute,
        targetSshUser: connection.sshUser,
        targetSshPort: connection.sshPort,
        category: (payload.category as QueuedAnsibleTask['category']) || 'system',
        primaryModule: steps[0]?.module || 'ansible.builtin.command',
        steps,
        become: payload.become !== false,
        becomeMethod: payload.becomeMethod || resolveBecomeMethod(node),
        status: 'staged',
        createdAt: now(),
      };

      deps.enqueueTask(task);
      broadcast({ type: 'queue_updated', source: `cron:${schedule.name}` });

      const exec = await deps.executeQueuedTask(inventoryPath, {
        cwd: deps.cwd,
        taskId: task.id,
      });

      results.push({ node, taskId: task.id, result: exec.result });

      globalTask.logs = globalTask.logs || [];
      if (exec.result) {
        globalTask.logs.push(
          `[queue] ${task.title}: ${exec.result.success ? 'OK' : 'FAILED'}${
            exec.result.error ? ` — ${exec.result.error}` : ''
          }`
        );
        globalTask.logs.push(...describeResult(exec.result.jsonOutput));
      } else {
        globalTask.logs.push(`[queue] ${task.title}: no execution result (queue busy)`);
      }
      globalTask.progress = Math.round(((idx + 1) / nodes.length) * 95);

      if (!exec.result) {
        failure = `Ansible queue was busy; ${task.title} was staged but not executed`;
        break;
      }
      if (!exec.result.success) {
        failure = exec.result.error || `${task.title} failed`;
        break;
      }
    }

    globalTask.progress = 100;
    globalTask.status = failure ? 'FAILED' : 'OK';
    globalTask.progressEta = failure ? `Failed: ${failure}` : `Completed ${results.length} task(s)`;
    saveTask(globalTask);
    broadcast({ type: 'task_updated', task: globalTask });

    return {
      status: failure ? 'FAILED' : 'OK',
      taskId: globalTaskId,
      error: failure,
    };
  }

  return {
    execute(schedule: CronSchedule): Promise<CronExecutionOutcome> {
      if (schedule.taskKind === 'playbook') {
        return runPlaybookSchedule(schedule);
      }
      return runStepsSchedule(schedule);
    },
  };
}
