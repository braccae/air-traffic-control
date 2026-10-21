import { describe, it, after } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {
  initCronScheduleStore,
  listSchedules,
  getSchedule,
  createSchedule,
  updateSchedule,
  removeSchedule,
  listDueSchedules,
  setScheduleNextRun,
  recordScheduleRun,
  resolveScopeTargets,
  validateCronScheduleInput,
} from './cronScheduleStore.ts';
import type { NodeItem } from '../types/index.ts';

const tempDirs: string[] = [];

function tempDbPath(): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'atc-cron-store-'));
  tempDirs.push(dir);
  return path.join(dir, 'hosts-cache.sqlite');
}

after(() => {
  for (const dir of tempDirs) {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

const playbookInput = {
  name: 'Nightly baseline',
  cronExpression: '0 3 * * *',
  scope: { kind: 'host' as const, target: 'host-1' },
  taskKind: 'playbook' as const,
  payload: { playbook: 'site.yml' },
};

function makeNode(overrides: Partial<NodeItem>): NodeItem {
  return {
    id: 'node-1',
    name: 'node-1',
    shortName: 'node-1',
    group: 'tag:host',
    datacenter: 'homelab',
    nodeType: 'linux',
    ip: '100.64.0.1',
    port: 22,
    sshUser: 'core',
    tags: ['tag:host'],
    tailscaleIp: '100.64.0.1',
    tailscaleHost: 'node-1.ts.net',
    osDescription: 'Fedora CoreOS',
    status: 'online',
    uptime: '1 day',
    hasGlances: false,
    hasCockpit: false,
    hasPodman: true,
    hasRestic: false,
    specs: {
      cpu: 'test',
      vcpus: 2,
      ramGiB: 8,
      motherboard: '',
      bios: '',
      ipmi: '',
      ecc: '',
      secureBoot: '',
      iommu: '',
    },
    ...overrides,
  };
}

describe('cron schedule store', () => {
  it('persists schedules across store re-initialisation', () => {
    const dbPath = tempDbPath();
    initCronScheduleStore(dbPath);

    const created = createSchedule(playbookInput, { now: new Date(2026, 2, 10, 4, 0).getTime(), id: 'cron-1' });
    assert.equal(created.id, 'cron-1');
    assert.equal(created.enabled, true);
    assert.equal(created.nextRunAt, new Date(2026, 2, 11, 3, 0).getTime());

    // Simulate a restart: rebind the same file and read again.
    initCronScheduleStore(dbPath);
    const reloaded = listSchedules();
    assert.equal(reloaded.length, 1);
    assert.equal(reloaded[0].name, 'Nightly baseline');
    assert.deepEqual(reloaded[0].scope, { kind: 'host', target: 'host-1' });
    assert.equal(reloaded[0].lastRunAt, undefined);
  });

  it('stores ansible_steps payloads and rejects empty step lists', () => {
    initCronScheduleStore(tempDbPath());

    assert.throws(
      () =>
        createSchedule({
          name: 'Bad steps',
          cronExpression: '*/5 * * * *',
          scope: { kind: 'group', target: 'tag:host' },
          taskKind: 'ansible_steps',
          payload: { title: 'Bad', steps: [] },
        }),
      /At least one Ansible step/
    );

    const created = createSchedule({
      name: 'Restart podman socket',
      cronExpression: '*/5 * * * *',
      scope: { kind: 'group', target: 'tag:host' },
      taskKind: 'ansible_steps',
      payload: {
        title: 'Restart podman socket',
        steps: [{ name: 'Restart', module: 'ansible.builtin.systemd', parameters: { name: 'podman.socket' } }],
      },
    });
    assert.equal(created.taskKind, 'ansible_steps');
    assert.equal((created.payload as any).steps.length, 1);
  });

  it('disabling clears nextRunAt; re-enabling recomputes it', () => {
    initCronScheduleStore(tempDbPath());
    const now = new Date(2026, 2, 10, 4, 0).getTime();
    const created = createSchedule(playbookInput, { now });

    const disabled = updateSchedule(created.id, { enabled: false }, { now });
    assert.equal(disabled?.enabled, false);
    assert.equal(disabled?.nextRunAt, null);

    const enabled = updateSchedule(created.id, { enabled: true }, { now });
    assert.equal(enabled?.enabled, true);
    assert.equal(enabled?.nextRunAt, new Date(2026, 2, 11, 3, 0).getTime());
  });

  it('removes schedules', () => {
    initCronScheduleStore(tempDbPath());
    const created = createSchedule(playbookInput);
    assert.equal(removeSchedule(created.id), true);
    assert.equal(getSchedule(created.id), null);
    assert.equal(removeSchedule(created.id), false);
  });

  it('lists only enabled schedules whose next run is due', () => {
    initCronScheduleStore(tempDbPath());
    const now = new Date(2026, 2, 10, 4, 0).getTime();
    const due = createSchedule(playbookInput, { now, id: 'due' });
    createSchedule({ ...playbookInput, name: 'Paused', enabled: false }, { now, id: 'paused' });

    setScheduleNextRun(due.id, now - 1000);
    const listed = listDueSchedules(now);
    assert.equal(listed.length, 1);
    assert.equal(listed[0].id, 'due');
  });

  it('records the last run outcome', () => {
    initCronScheduleStore(tempDbPath());
    const created = createSchedule(playbookInput);
    recordScheduleRun(created.id, { status: 'FAILED', taskId: 'task-7', error: 'boom' });
    const reloaded = getSchedule(created.id)!;
    assert.equal(reloaded.lastRunStatus, 'FAILED');
    assert.equal(reloaded.lastRunTaskId, 'task-7');
    assert.equal(reloaded.lastRunError, 'boom');
    assert.ok(reloaded.lastRunAt && reloaded.lastRunAt > 0);
  });

  it('validates input', () => {
    assert.match(validateCronScheduleInput({ ...playbookInput, name: ' ' }) || '', /name/i);
    assert.match(validateCronScheduleInput({ ...playbookInput, cronExpression: 'nope' }) || '', /cron/i);
    assert.match(
      validateCronScheduleInput({ ...playbookInput, payload: { playbook: '../etc/passwd' } }) || '',
      /playbook/i
    );
    assert.equal(validateCronScheduleInput(playbookInput), null);
  });
});

describe('cron schedule scope resolution', () => {
  const nodes = [
    makeNode({ id: 'n1', shortName: 'alpha', name: 'alpha', tags: ['tag:host'] }),
    makeNode({ id: 'n2', shortName: 'beta', name: 'beta', tags: ['tag:worker'] }),
    makeNode({ id: 'n3', shortName: 'gamma', name: 'gamma', tags: ['tag:worker'] }),
  ];

  it('resolves a single host by id, short name, or name', () => {
    const byShort = resolveScopeTargets({ kind: 'host', target: 'beta' }, nodes);
    assert.equal(byShort.nodes.length, 1);
    assert.equal(byShort.nodes[0].id, 'n2');
    assert.equal(byShort.limit, 'beta');

    const byId = resolveScopeTargets({ kind: 'host', target: 'n3' }, nodes);
    assert.equal(byId.nodes.length, 1);
    assert.equal(byId.nodes[0].id, 'n3');
  });

  it('resolves a tag group with or without tag: prefix', () => {
    const prefixed = resolveScopeTargets({ kind: 'group', target: 'tag:worker' }, nodes);
    assert.deepEqual(prefixed.nodes.map(n => n.id).sort(), ['n2', 'n3']);
    assert.equal(prefixed.limit, 'tag:worker');

    const bare = resolveScopeTargets({ kind: 'group', target: 'worker' }, nodes);
    assert.equal(bare.nodes.length, 2);
    assert.equal(bare.limit, 'tag:worker');
  });

  it('resolves datacenter scope to all nodes', () => {
    const resolved = resolveScopeTargets({ kind: 'datacenter', target: 'all' }, nodes);
    assert.equal(resolved.limit, 'all');
    assert.equal(resolved.nodes.length, 3);
  });
});
