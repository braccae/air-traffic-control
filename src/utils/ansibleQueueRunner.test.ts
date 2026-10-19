import { describe, it, beforeEach } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import {
  parseAnsibleJsonStdout,
  executeQueueTaskOneByOne,
  initQueueStore,
  getQueue,
  enqueueTask,
  removeTask,
  clearQueue,
  updateTaskStatus,
  executeNextQueueTask,
  type QueueExecutionResult,
} from './ansibleQueueRunner.ts';
import type { QueuedAnsibleTask } from '../types/index.ts';

function expect<T>(actual: T) {
  return {
    toBe(expected: any) {
      assert.strictEqual(actual, expected);
    },
    toEqual(expected: any) {
      assert.deepStrictEqual(actual, expected);
    },
    toBeGreaterThan(expected: number) {
      assert.ok(
        typeof actual === 'number' && actual > expected,
        `Expected ${actual} to be greater than ${expected}`
      );
    },
    toBeDefined() {
      assert.notStrictEqual(actual, undefined);
    },
    toBeUndefined() {
      assert.strictEqual(actual, undefined);
    },
    toBeNull() {
      assert.strictEqual(actual, null);
    },
    toBeTruthy() {
      assert.ok(actual);
    },
    toBeFalsy() {
      assert.ok(!actual);
    },
    toContain(expected: string) {
      assert.ok(
        typeof actual === 'string' && actual.includes(expected),
        `Expected ${JSON.stringify(actual)} to contain ${JSON.stringify(expected)}`
      );
    },
  };
}

const sampleTask1: QueuedAnsibleTask = {
  id: 'task-restart-caddy',
  title: 'Restart Caddy on likes-it-cold',
  targetNodeId: 'node-1',
  targetNodeName: 'likes-it-cold',
  targetHostRoute: '100.115.92.19',
  targetSshUser: 'core',
  targetSshPort: 22,
  category: 'system',
  primaryModule: 'ansible.builtin.systemd',
  steps: [
    {
      name: 'Restart Caddy web service',
      module: 'ansible.builtin.systemd',
      parameters: {
        name: 'caddy',
        state: 'restarted',
      },
    },
  ],
  become: true,
  becomeMethod: 'run0',
  status: 'staged',
  createdAt: Date.now(),
};

const sampleTask2: QueuedAnsibleTask = {
  id: 'task-allow-firewall-8080',
  title: 'Allow TCP 8080 on likes-it-cold',
  targetNodeId: 'node-1',
  targetNodeName: 'likes-it-cold',
  targetHostRoute: '100.115.92.19',
  targetSshUser: 'core',
  targetSshPort: 22,
  category: 'network',
  primaryModule: 'ansible.posix.firewalld',
  steps: [
    {
      name: 'Open port 8080',
      module: 'ansible.posix.firewalld',
      parameters: {
        port: '8080/tcp',
        state: 'enabled',
        permanent: true,
        immediate: true,
      },
    },
  ],
  become: true,
  becomeMethod: 'run0',
  status: 'staged',
  createdAt: Date.now() + 1000,
};

const mockAnsibleSuccessOutput = JSON.stringify({
  plays: [
    {
      play: { id: 'play-1', name: 'Restart Caddy' },
      tasks: [
        {
          task: { id: 'task-1', name: 'Restart Caddy web service' },
          hosts: {
            '100.115.92.19': {
              changed: true,
              failed: false,
              state: 'started',
              status: { ActiveState: 'active' },
            },
          },
        },
      ],
    },
  ],
  stats: {
    '100.115.92.19': {
      changed: 1,
      failures: 0,
      ignored: 0,
      ok: 1,
      rescued: 0,
      skipped: 0,
      unreachable: 0,
    },
  },
});

const mockAnsibleFailedOutput = JSON.stringify({
  plays: [
    {
      play: { id: 'play-1', name: 'Restart Caddy' },
      tasks: [
        {
          task: { id: 'task-1', name: 'Restart Caddy web service' },
          hosts: {
            '100.115.92.19': {
              changed: false,
              failed: true,
              msg: 'Failed to restart caddy.service: Unit caddy.service not found.',
            },
          },
        },
      ],
    },
  ],
  stats: {
    '100.115.92.19': {
      changed: 0,
      failures: 1,
      ignored: 0,
      ok: 0,
      rescued: 0,
      skipped: 0,
      unreachable: 0,
    },
  },
});

describe('ansibleQueueRunner', () => {
  beforeEach(() => {
    // Reinitialize in-memory SQLite database before each test
    initQueueStore(':memory:');
    clearQueue();
  });

  describe('parseAnsibleJsonStdout', () => {
    it('parses clean Ansible JSON stdout', () => {
      const parsed = parseAnsibleJsonStdout(mockAnsibleSuccessOutput);
      expect(parsed.plays.length).toBe(1);
      expect(parsed.stats['100.115.92.19'].changed).toBe(1);
    });

    it('parses Ansible JSON stdout preceded by warnings and deprecation notices', () => {
      const noisyStdout = [
        `[WARNING]: While constructing a Python object, the following warning occurred: ...`,
        `[DEPRECATION WARNING]: DEFAULT_HASH_BEHAVIOUR is deprecated.`,
        mockAnsibleSuccessOutput,
        `\n`,
      ].join('\n');

      const parsed = parseAnsibleJsonStdout(noisyStdout);
      expect(parsed.plays.length).toBe(1);
      expect(parsed.plays[0].tasks.length).toBe(1);
    });

    it('throws when stdout does not contain a valid JSON object', () => {
      assert.throws(() => {
        parseAnsibleJsonStdout('fatal: [100.115.92.19]: UNREACHABLE! => {"msg": "Failed to connect"}');
      }, /No JSON object found|Failed to parse Ansible JSON/);
    });
  });

  describe('Queue State Manager (In-Memory & SQLite Persistence)', () => {
    it('initializes with an empty queue and isRunning=false', () => {
      const state = getQueue();
      expect(state.tasks).toEqual([]);
      expect(state.isRunning).toBe(false);
    });

    it('enqueues tasks and persists them', () => {
      const t1 = enqueueTask(sampleTask1);
      expect(t1.id).toBe('task-restart-caddy');
      expect(t1.status).toBe('staged');

      const t2 = enqueueTask(sampleTask2);
      expect(t2.id).toBe('task-allow-firewall-8080');

      const state = getQueue();
      expect(state.tasks.length).toBe(2);
      expect(state.tasks[0].id).toBe('task-restart-caddy');
      expect(state.tasks[1].id).toBe('task-allow-firewall-8080');
    });

    it('updates task status and records error message on failure', () => {
      enqueueTask(sampleTask1);
      const updated = updateTaskStatus('task-restart-caddy', 'failed', 'Service unit missing');
      expect(updated?.status).toBe('failed');
      expect(updated?.error).toBe('Service unit missing');

      const state = getQueue();
      expect(state.tasks[0].status).toBe('failed');
      expect(state.tasks[0].error).toBe('Service unit missing');
    });

    it('removes a specific task by id', () => {
      enqueueTask(sampleTask1);
      enqueueTask(sampleTask2);

      const removed = removeTask('task-restart-caddy');
      expect(removed).toBe(true);

      const state = getQueue();
      expect(state.tasks.length).toBe(1);
      expect(state.tasks[0].id).toBe('task-allow-firewall-8080');

      const notFound = removeTask('non-existent');
      expect(notFound).toBe(false);
    });

    it('clears all non-running tasks on clearQueue', () => {
      enqueueTask(sampleTask1);
      enqueueTask(sampleTask2);

      clearQueue();
      const state = getQueue();
      expect(state.tasks.length).toBe(0);
    });
  });

  describe('executeQueueTaskOneByOne', () => {
    it('writes ephemeral playbook, executes via runner, parses JSON, and cleans up playbook', async () => {
      let executedCommand = '';
      let executedArgs: string[] = [];
      let playbookExistedDuringExec = false;

      const result = await executeQueueTaskOneByOne(sampleTask1, undefined, {
        execCommand: async (cmd, args) => {
          executedCommand = cmd;
          executedArgs = args;
          // Verify ephemeral playbook exists during execution
          const playbookPath = args[args.length - 1];
          playbookExistedDuringExec = fs.existsSync(playbookPath);

          return {
            stdout: mockAnsibleSuccessOutput,
            stderr: '',
            exitCode: 0,
          };
        },
      });

      expect(result.success).toBe(true);
      expect(result.taskId).toBe('task-restart-caddy');
      expect(result.changed).toBe(true);
      expect(result.failed).toBe(false);
      expect(result.jsonOutput).toBeDefined();
      expect(playbookExistedDuringExec).toBe(true);

      // Verify ephemeral file is cleaned up after execution
      const ephemeralPath = executedArgs[executedArgs.length - 1];
      expect(fs.existsSync(ephemeralPath)).toBe(false);
    });

    it('cleans up ephemeral playbook even if execution throws an error', async () => {
      let ephemeralPath = '';
      try {
        await executeQueueTaskOneByOne(sampleTask1, undefined, {
          execCommand: async (_cmd, args) => {
            ephemeralPath = args[args.length - 1];
            throw new Error('Simulated runner crash');
          },
        });
      } catch (err: any) {
        expect(err.message).toBe('Simulated runner crash');
      }

      if (ephemeralPath) {
        expect(fs.existsSync(ephemeralPath)).toBe(false);
      }
    });

    it('captures failed task execution and extracts structured error', async () => {
      const result = await executeQueueTaskOneByOne(sampleTask1, undefined, {
        execCommand: async () => {
          return {
            stdout: mockAnsibleFailedOutput,
            stderr: 'TASK [Restart Caddy web service] failed',
            exitCode: 2,
          };
        },
      });

      expect(result.success).toBe(false);
      expect(result.failed).toBe(true);
      expect(result.changed).toBe(false);
      expect(result.error).toContain('Unit caddy.service not found');
    });
  });

  describe('executeNextQueueTask & Checkpoint Halting', () => {
    it('executes staged tasks one-by-one in sequence when successful', async () => {
      enqueueTask(sampleTask1);
      enqueueTask(sampleTask2);

      // Execute first task
      const firstRun = await executeNextQueueTask(undefined, {
        execCommand: async () => ({
          stdout: mockAnsibleSuccessOutput,
          stderr: '',
          exitCode: 0,
        }),
      });

      expect(firstRun.result?.taskId).toBe('task-restart-caddy');
      expect(firstRun.result?.success).toBe(true);
      expect(firstRun.remainingCount).toBe(1);
      expect(firstRun.haltedOnError).toBe(false);

      const queueAfterFirst = getQueue();
      expect(queueAfterFirst.tasks.find(t => t.id === 'task-restart-caddy')?.status).toBe('completed');
      expect(queueAfterFirst.tasks.find(t => t.id === 'task-allow-firewall-8080')?.status).toBe('staged');

      // Execute second task
      const secondRun = await executeNextQueueTask(undefined, {
        execCommand: async () => ({
          stdout: mockAnsibleSuccessOutput,
          stderr: '',
          exitCode: 0,
        }),
      });

      expect(secondRun.result?.taskId).toBe('task-allow-firewall-8080');
      expect(secondRun.remainingCount).toBe(0);
      expect(secondRun.haltedOnError).toBe(false);

      const queueAfterSecond = getQueue();
      expect(queueAfterSecond.tasks.every(t => t.status === 'completed')).toBe(true);
    });

    it('HALTS immediately on error, marking failed task and leaving subsequent tasks in staged state', async () => {
      enqueueTask(sampleTask1);
      enqueueTask(sampleTask2);

      // Execute first task which fails
      const failedRun = await executeNextQueueTask(undefined, {
        execCommand: async () => ({
          stdout: mockAnsibleFailedOutput,
          stderr: 'Task execution failed',
          exitCode: 2,
        }),
      });

      expect(failedRun.result?.taskId).toBe('task-restart-caddy');
      expect(failedRun.result?.success).toBe(false);
      expect(failedRun.haltedOnError).toBe(true);
      expect(failedRun.remainingCount).toBe(1);

      // Verify queue state: first task failed, second task remains staged!
      const currentQueue = getQueue();
      const task1 = currentQueue.tasks.find(t => t.id === 'task-restart-caddy');
      const task2 = currentQueue.tasks.find(t => t.id === 'task-allow-firewall-8080');

      expect(task1?.status).toBe('failed');
      expect(task1?.error).toContain('Unit caddy.service not found');
      expect(task2?.status).toBe('staged'); // Untouched checkpoint!
      expect(currentQueue.isRunning).toBe(false);
    });
  });
});
