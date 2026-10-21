/**
 * In-process ATC cron scheduler.
 *
 * Design choice (documented in docs/CRON_SCHEDULES.md): schedules are driven by
 * the coordinator, NOT installed as systemd timers or crontab entries on target
 * hosts. That keeps the fleet agentless (per AGENTS.md §3), means a schedule
 * survives target rebuilds, and guarantees runs land in the same mutation
 * queue / background task system the UI already streams.
 */
import {
  listDueSchedules,
  listSchedules,
  recordScheduleRun,
  setScheduleNextRun,
  type CronSchedule,
} from './cronScheduleStore.ts';
import { nextCronRun } from './cronExpression.ts';

export interface CronExecutionResult {
  status: 'OK' | 'FAILED';
  taskId?: string;
  error?: string;
}

export interface CronSchedulerDeps {
  execute: (schedule: CronSchedule) => Promise<CronExecutionResult>;
  now?: () => number;
  tickMs?: number;
  log?: (message: string) => void;
}

export interface CronScheduler {
  start(): void;
  stop(): void;
  isRunning(): boolean;
  /** Fires every due schedule once. Exposed for tests and the "run due now" API. */
  tick(): Promise<CronRunRecord[]>;
  /** Executes one schedule immediately without touching its next-run clock. */
  runNow(id: string): Promise<CronRunRecord | null>;
}

export interface CronRunRecord {
  scheduleId: string;
  startedAt: number;
  finishedAt: number;
  status: 'OK' | 'FAILED';
  taskId?: string;
  error?: string;
}

const DEFAULT_TICK_MS = 15000;

export function createCronScheduler(deps: CronSchedulerDeps): CronScheduler {
  const now = deps.now ?? Date.now;
  const tickMs = deps.tickMs ?? DEFAULT_TICK_MS;
  const log = deps.log ?? (() => {});

  let timer: NodeJS.Timeout | null = null;
  let ticking = false;
  const inFlight = new Set<string>();

  async function executeOne(schedule: CronSchedule): Promise<CronRunRecord> {
    const startedAt = now();
    inFlight.add(schedule.id);
    recordScheduleRun(schedule.id, { status: 'RUNNING', at: startedAt });

    try {
      const result = await deps.execute(schedule);
      const finishedAt = now();
      recordScheduleRun(schedule.id, {
        status: result.status,
        at: finishedAt,
        taskId: result.taskId,
        error: result.error,
      });
      log(
        `[cron] ${schedule.name} (${schedule.cronExpression}) -> ${result.status}${
          result.taskId ? ` task=${result.taskId}` : ''
        }${result.error ? ` error=${result.error}` : ''}`
      );
      return {
        scheduleId: schedule.id,
        startedAt,
        finishedAt,
        status: result.status,
        taskId: result.taskId,
        error: result.error,
      };
    } catch (err: any) {
      const finishedAt = now();
      const message = err?.message || String(err);
      recordScheduleRun(schedule.id, { status: 'FAILED', at: finishedAt, error: message });
      log(`[cron] ${schedule.name} failed to execute: ${message}`);
      return {
        scheduleId: schedule.id,
        startedAt,
        finishedAt,
        status: 'FAILED',
        error: message,
      };
    } finally {
      inFlight.delete(schedule.id);
    }
  }

  async function tick(): Promise<CronRunRecord[]> {
    if (ticking) return [];
    ticking = true;
    const records: CronRunRecord[] = [];

    try {
      const due = listDueSchedules(now()).filter(s => !inFlight.has(s.id));
      for (const schedule of due) {
        // Advance the clock BEFORE execution so a crash or restart cannot
        // re-fire the same slot twice. If the expression can never fire again
        // the schedule is parked (nextRunAt = null) rather than spinning.
        const next = nextCronRun(schedule.cronExpression, now());
        setScheduleNextRun(schedule.id, next);
        records.push(await executeOne(schedule));
      }
    } finally {
      ticking = false;
    }

    return records;
  }

  async function runNow(id: string): Promise<CronRunRecord | null> {
    const schedule = listSchedules().find(s => s.id === id);
    if (!schedule) return null;
    if (inFlight.has(id)) {
      return {
        scheduleId: id,
        startedAt: now(),
        finishedAt: now(),
        status: 'FAILED',
        error: 'Schedule is already running',
      };
    }
    return executeOne(schedule);
  }

  return {
    start() {
      if (timer) return;
      timer = setInterval(() => {
        void tick();
      }, tickMs);
      if (typeof timer.unref === 'function') timer.unref();
      void tick();
    },
    stop() {
      if (timer) {
        clearInterval(timer);
        timer = null;
      }
    },
    isRunning() {
      return timer !== null;
    },
    tick,
    runNow,
  };
}
