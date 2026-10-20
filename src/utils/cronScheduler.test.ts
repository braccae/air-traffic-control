import { describe, it, after } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {
  initCronScheduleStore,
  createSchedule,
  getSchedule,
  listDueSchedules,
  setScheduleNextRun,
  updateSchedule,
} from './cronScheduleStore.ts';
import { createCronScheduler } from './cronScheduler.ts';

const tempDirs: string[] = [];

function tempDbPath(): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'atc-cron-sched-'));
  tempDirs.push(dir);
  return path.join(dir, 'hosts-cache.sqlite');
}

after(() => {
  for (const dir of tempDirs) {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

const baseInput = {
  name: 'Nightly baseline',
  cronExpression: '0 3 * * *',
  scope: { kind: 'host' as const, target: 'alpha' },
  taskKind: 'playbook' as const,
  payload: { playbook: 'site.yml' },
};

describe('cron scheduler', () => {
  it('fires a due schedule once, records the run, and advances the next-run clock', async () => {
    initCronScheduleStore(tempDbPath());
    const now = new Date(2026, 2, 11, 3, 0, 5).getTime();
    const created = createSchedule(baseInput, { now: now - 86_400_000 });
    setScheduleNextRun(created.id, now - 2000);

    const executed: string[] = [];
    const scheduler = createCronScheduler({
      now: () => now,
      execute: async schedule => {
        executed.push(schedule.id);
        return { status: 'OK', taskId: 'task-cron-1' };
      },
    });

    const records = await scheduler.tick();
    assert.equal(executed.length, 1);
    assert.equal(records.length, 1);
    assert.equal(records[0].status, 'OK');
    assert.equal(records[0].taskId, 'task-cron-1');

    const reloaded = getSchedule(created.id)!;
    assert.equal(reloaded.lastRunStatus, 'OK');
    assert.equal(reloaded.lastRunTaskId, 'task-cron-1');
    assert.equal(reloaded.nextRunAt, new Date(2026, 2, 12, 3, 0).getTime());

    // A second tick at the same instant must not re-fire the same slot.
    const again = await scheduler.tick();
    assert.equal(again.length, 0);
    assert.equal(executed.length, 1);
  });

  it('does not fire disabled schedules', async () => {
    initCronScheduleStore(tempDbPath());
    const now = new Date(2026, 2, 11, 3, 0, 5).getTime();
    const created = createSchedule(baseInput, { now: now - 86_400_000 });
    setScheduleNextRun(created.id, now - 2000);
    updateSchedule(created.id, { enabled: false }, { now: now - 1000 });

    const executed: string[] = [];
    const scheduler = createCronScheduler({
      now: () => now,
      execute: async schedule => {
        executed.push(schedule.id);
        return { status: 'OK' };
      },
    });

    assert.equal(listDueSchedules(now).length, 0);
    const records = await scheduler.tick();
    assert.equal(records.length, 0);
    assert.equal(executed.length, 0);
  });

  it('records a FAILED run and the executor error without retrying the slot', async () => {
    initCronScheduleStore(tempDbPath());
    const now = new Date(2026, 2, 11, 3, 0, 5).getTime();
    const created = createSchedule(baseInput, { now: now - 86_400_000 });
    setScheduleNextRun(created.id, now - 2000);

    const scheduler = createCronScheduler({
      now: () => now,
      execute: async () => ({ status: 'FAILED', error: 'ansible-playbook exited with code 2' }),
    });

    const records = await scheduler.tick();
    assert.equal(records[0].status, 'FAILED');
    const reloaded = getSchedule(created.id)!;
    assert.equal(reloaded.lastRunStatus, 'FAILED');
    assert.equal(reloaded.lastRunError, 'ansible-playbook exited with code 2');
  });

  it('runNow executes without changing the next-run clock', async () => {
    initCronScheduleStore(tempDbPath());
    const now = new Date(2026, 2, 11, 12, 0, 0).getTime();
    const created = createSchedule(baseInput, { now });
    const before = getSchedule(created.id)!.nextRunAt;

    const scheduler = createCronScheduler({
      now: () => now,
      execute: async () => ({ status: 'OK', taskId: 'task-manual' }),
    });

    const record = await scheduler.runNow(created.id);
    assert.equal(record?.status, 'OK');
    const afterRun = getSchedule(created.id)!;
    assert.equal(afterRun.nextRunAt, before);
    assert.equal(afterRun.lastRunTaskId, 'task-manual');
  });

  it('returns null for an unknown schedule id', async () => {
    initCronScheduleStore(tempDbPath());
    const scheduler = createCronScheduler({ execute: async () => ({ status: 'OK' }) });
    assert.equal(await scheduler.runNow('missing'), null);
  });
});
