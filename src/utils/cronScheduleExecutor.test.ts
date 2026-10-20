import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import type { GlobalTaskItem, NodeItem, QueuedAnsibleTask } from '../types/index.ts';
import { createCronExecutor } from './cronScheduleExecutor.ts';
import type { CronSchedule } from './cronScheduleStore.ts';
import type { PlaybookRunner } from './playbookRunner.ts';

function makeNode(overrides: Partial<NodeItem>): NodeItem {
  return {
    id: 'n1',
    name: 'alpha',
    shortName: 'alpha',
    group: 'tag:host',
    datacenter: 'homelab',
    nodeType: 'linux',
    ip: '100.64.0.10',
    port: 22,
    sshUser: 'core',
    tags: ['tag:host'],
    tailscaleIp: '100.64.0.10',
    tailscaleHost: 'alpha.ts.net',
    osDescription: 'Fedora',
    status: 'online',
    uptime: '1d',
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

function makeSchedule(overrides: Partial<CronSchedule>): CronSchedule {
  return {
    id: 'cron-1',
    name: 'Test schedule',
    cronExpression: '0 3 * * *',
    enabled: true,
    scope: { kind: 'host', target: 'alpha' },
    taskKind: 'playbook',
    payload: { playbook: 'site.yml' },
    createdAt: 0,
    updatedAt: 0,
    nextRunAt: null,
    ...overrides,
  };
}

function fakeRunner(outcome: { success: boolean; exitCode?: number; error?: string }): PlaybookRunner & {
  calls: Array<Record<string, unknown>>;
} {
  const calls: Array<Record<string, unknown>> = [];
  return {
    calls,
    run(request) {
      calls.push(request as unknown as Record<string, unknown>);
      return {
        success: true,
        taskId: 'task-ansible-1',
        completion: Promise.resolve({
          success: outcome.success,
          exitCode: outcome.exitCode ?? (outcome.success ? 0 : 2),
          error: outcome.error,
        }),
      };
    },
    cancel: () => true,
    isActive: () => false,
    activeTaskIds: () => [],
  };
}

describe('cron schedule executor — playbook schedules', () => {
  it('derives --limit from a host scope and reports OK on runner success', async () => {
    const runner = fakeRunner({ success: true });
    const executor = createCronExecutor({
      cwd: '/tmp',
      playbookRunner: runner,
      listNodes: () => [makeNode({ id: 'n1', shortName: 'alpha', name: 'alpha' })],
      enqueueTask: task => task,
      executeQueuedTask: async () => ({ result: null, haltedOnError: false }),
    });

    const result = await executor.execute(makeSchedule({}));
    assert.equal(result.status, 'OK');
    assert.equal(result.taskId, 'task-ansible-1');
    assert.equal(runner.calls.length, 1);
    assert.equal(runner.calls[0].target, 'alpha');
    assert.equal(runner.calls[0].playbook, 'site.yml');
    assert.equal(runner.calls[0].source, 'cron:Test schedule');
  });

  it('uses tag: limit for group scopes and all for datacenter scopes', async () => {
    const runner = fakeRunner({ success: true });
    const executor = createCronExecutor({
      cwd: '/tmp',
      playbookRunner: runner,
      listNodes: () => [makeNode({ id: 'n1', tags: ['tag:worker'] })],
      enqueueTask: task => task,
      executeQueuedTask: async () => ({ result: null, haltedOnError: false }),
    });

    await executor.execute(makeSchedule({ scope: { kind: 'group', target: 'worker' } }));
    await executor.execute(makeSchedule({ scope: { kind: 'datacenter', target: 'all' } }));

    assert.equal(runner.calls[0].target, 'tag:worker');
    assert.equal(runner.calls[1].target, 'all');
  });

  it('reports FAILED when the playbook process exits non-zero', async () => {
    const runner = fakeRunner({ success: false, exitCode: 4, error: 'playbook failed' });
    const executor = createCronExecutor({
      cwd: '/tmp',
      playbookRunner: runner,
      listNodes: () => [],
      enqueueTask: task => task,
      executeQueuedTask: async () => ({ result: null, haltedOnError: false }),
    });

    const result = await executor.execute(makeSchedule({}));
    assert.equal(result.status, 'FAILED');
    assert.equal(result.error, 'playbook failed');
  });

  it('reports FAILED when the runner refuses to start', async () => {
    const runner: PlaybookRunner = {
      run: () => ({ success: false, error: 'Playbook missing.yml not found on filesystem', code: 404 }),
      cancel: () => false,
      isActive: () => false,
      activeTaskIds: () => [],
    };
    const executor = createCronExecutor({
      cwd: '/tmp',
      playbookRunner: runner,
      listNodes: () => [],
      enqueueTask: task => task,
      executeQueuedTask: async () => ({ result: null, haltedOnError: false }),
    });

    const result = await executor.execute(makeSchedule({}));
    assert.equal(result.status, 'FAILED');
    assert.match(result.error || '', /not found/);
  });
});

describe('cron schedule executor — ansible_steps schedules', () => {
  const stepsSchedule = makeSchedule({
    taskKind: 'ansible_steps',
    scope: { kind: 'group', target: 'tag:worker' },
    payload: {
      title: 'Restart podman socket',
      steps: [{ name: 'Restart', module: 'ansible.builtin.systemd', parameters: { name: 'podman.socket' } }],
    },
  });

  it('enqueues one real queue task per scoped node and executes them', async () => {
    const enqueued: QueuedAnsibleTask[] = [];
    const executedIds: string[] = [];
    const saved: GlobalTaskItem[] = [];

    const executor = createCronExecutor({
      cwd: '/tmp',
      playbookRunner: fakeRunner({ success: true }),
      listNodes: () => [
        makeNode({ id: 'w1', shortName: 'worker-1', name: 'worker-1', tags: ['tag:worker'] }),
        makeNode({ id: 'w2', shortName: 'worker-2', name: 'worker-2', tags: ['tag:worker'] }),
      ],
      enqueueTask: task => {
        enqueued.push(task);
        return task;
      },
      executeQueuedTask: async (_inv, options) => {
        executedIds.push(options.taskId);
        return {
          result: {
            success: true,
            taskId: options.taskId,
            changed: true,
            failed: false,
            jsonOutput: {
              plays: [
                {
                  tasks: [
                    {
                      task: { name: 'Restart' },
                      hosts: { 'worker-1': { changed: true } },
                    },
                  ],
                },
              ],
              stats: { 'worker-1': { ok: 1, changed: 1, failures: 0, unreachable: 0 } },
            },
          },
          haltedOnError: false,
        };
      },
      saveTask: task => saved.push(task),
    });

    const result = await executor.execute(stepsSchedule);

    assert.equal(result.status, 'OK');
    assert.equal(enqueued.length, 2);
    assert.equal(executedIds.length, 2);
    assert.equal(enqueued[0].primaryModule, 'ansible.builtin.systemd');
    assert.equal(enqueued[0].targetNodeId, 'w1');
    assert.equal(saved.length >= 1, true);
    const finalTask = saved[saved.length - 1];
    assert.equal(finalTask.status, 'OK');
    assert.ok((finalTask.logs || []).some(line => line.includes('[recap] worker-1')));
  });

  it('halts on the first failing task and records FAILED', async () => {
    let call = 0;
    const executor = createCronExecutor({
      cwd: '/tmp',
      playbookRunner: fakeRunner({ success: true }),
      listNodes: () => [
        makeNode({ id: 'w1', shortName: 'worker-1', tags: ['tag:worker'] }),
        makeNode({ id: 'w2', shortName: 'worker-2', tags: ['tag:worker'] }),
      ],
      enqueueTask: task => task,
      executeQueuedTask: async (_inv, options) => {
        call++;
        return {
          result: {
            success: false,
            taskId: options.taskId,
            changed: false,
            failed: true,
            jsonOutput: null,
            error: 'Unit podman.socket not found',
          },
          haltedOnError: true,
        };
      },
      saveTask: () => {},
    });

    const result = await executor.execute(stepsSchedule);
    assert.equal(result.status, 'FAILED');
    assert.match(result.error || '', /not found/);
    assert.equal(call, 1);
  });

  it('fails loudly when no node matches the scope (zero-mock: no synthetic targets)', async () => {
    const executor = createCronExecutor({
      cwd: '/tmp',
      playbookRunner: fakeRunner({ success: true }),
      listNodes: () => [],
      enqueueTask: task => task,
      executeQueuedTask: async () => ({ result: null, haltedOnError: false }),
    });

    const result = await executor.execute(stepsSchedule);
    assert.equal(result.status, 'FAILED');
    assert.match(result.error || '', /No target nodes matched/);
  });
});
