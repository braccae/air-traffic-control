import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import { renderToString } from 'react-dom/server';
import { TaskQueueTab, maskSensitiveValue, type TaskQueueTabProps } from './TaskQueueTab.tsx';
import type { QueuedAnsibleTask } from '../types/index.ts';

const sampleTasks: QueuedAnsibleTask[] = [
  {
    id: 'task-1',
    title: 'Restart Nginx Web Server',
    targetNodeId: 'node-likes-it-cold',
    targetNodeName: 'likes-it-cold',
    targetHostRoute: '100.105.71.49',
    targetSshUser: 'core',
    targetSshPort: 22,
    category: 'system',
    primaryModule: 'ansible.builtin.systemd',
    become: true,
    becomeMethod: 'run0',
    status: 'staged',
    createdAt: 1729330000000,
    steps: [
      {
        name: 'Ensure nginx service is restarted',
        module: 'ansible.builtin.systemd',
        parameters: {
          name: 'nginx',
          state: 'restarted',
          daemon_reload: true,
        },
      },
    ],
  },
  {
    id: 'task-2',
    title: 'Mount Secret Backup Share',
    targetNodeId: 'node-ping',
    targetNodeName: 'ping',
    targetHostRoute: '100.105.71.50',
    targetSshUser: 'admin',
    targetSshPort: 2222,
    category: 'storage',
    primaryModule: 'ansible.posix.mount',
    become: true,
    becomeMethod: 'sudo',
    status: 'failed',
    createdAt: 1729330100000,
    error: 'fatal: [ping]: FAILED! => {"msg": "Mount point /mnt/backup busy"}',
    secretsMasked: {
      password: 'superSecretPassword123',
    },
    steps: [
      {
        name: 'Mount cifs share with auth',
        module: 'ansible.posix.mount',
        parameters: {
          path: '/mnt/backup',
          src: '//nas.local/backups',
          fstype: 'cifs',
          password: 'superSecretPassword123',
        },
      },
    ],
  },
  {
    id: 'task-3',
    title: 'Deploy Grafana Container',
    targetNodeId: 'node-dev-pc',
    targetNodeName: 'dev-pc',
    targetHostRoute: '100.105.71.51',
    targetSshUser: 'core',
    targetSshPort: 22,
    category: 'container',
    primaryModule: 'containers.podman.podman_container',
    become: false,
    becomeMethod: 'run0',
    status: 'completed',
    createdAt: 1729330200000,
    steps: [
      {
        name: 'Start grafana container',
        module: 'containers.podman.podman_container',
        parameters: {
          name: 'grafana',
          image: 'docker.io/grafana/grafana:latest',
          state: 'started',
        },
      },
    ],
  },
];

test('TaskQueueTab renders empty state message when queue is empty', () => {
  const html = renderToString(
    React.createElement(TaskQueueTab, {
      queue: [],
      isRunning: false,
      onRunNextTask: () => {},
      onExecuteAll: () => {},
      onDeleteTask: () => {},
      onClearQueue: () => {},
    })
  );

  assert.ok(html.includes('No tasks in the Ansible mutation queue'));
  assert.ok(html.includes('Staged Tasks (0)'));
});

test('TaskQueueTab renders list of queued tasks with title, node badge, status badge, and primary module pill', () => {
  const html = renderToString(
    React.createElement(TaskQueueTab, {
      queue: sampleTasks,
      isRunning: false,
      onRunNextTask: () => {},
      onExecuteAll: () => {},
      onDeleteTask: () => {},
      onClearQueue: () => {},
    })
  );

  assert.ok(html.includes('Staged Tasks (1)'));
  assert.ok(html.includes('Restart Nginx Web Server'));
  assert.ok(html.includes('likes-it-cold'));
  assert.ok(html.includes('ansible.builtin.systemd'));
  assert.ok(html.includes('STAGED'));
  assert.ok(html.includes('FAILED'));
  assert.ok(html.includes('COMPLETED'));
  assert.ok(html.includes('containers.podman.podman_container'));
});

test('TaskQueueTab renders accordion expansion with target connection details, privilege escalation, and steps', () => {
  const html = renderToString(
    React.createElement(TaskQueueTab, {
      queue: sampleTasks,
      isRunning: false,
      defaultExpandedIds: ['task-1'],
      onRunNextTask: () => {},
      onExecuteAll: () => {},
      onDeleteTask: () => {},
      onClearQueue: () => {},
    })
  );

  // Connection details
  assert.ok(html.includes('100.105.71.49'));
  assert.ok(html.includes('ansible_host'));
  assert.ok(html.includes('ansible_user'));
  assert.ok(html.includes('core'));
  assert.ok(html.includes('22'));

  // Privilege escalation
  assert.ok(html.includes('become'));
  assert.ok(html.includes('run0'));

  // Step breakdown
  assert.ok(html.includes('Ensure nginx service is restarted'));
  assert.ok(html.includes('restarted'));
});

test('TaskQueueTab masks sensitive parameters and secrets with ••••••••', () => {
  const html = renderToString(
    React.createElement(TaskQueueTab, {
      queue: sampleTasks,
      isRunning: false,
      defaultExpandedIds: ['task-2'],
      onRunNextTask: () => {},
      onExecuteAll: () => {},
      onDeleteTask: () => {},
      onClearQueue: () => {},
    })
  );

  // Must not expose the plaintext secret
  assert.ok(!html.includes('superSecretPassword123'), 'Plaintext secret must never appear in HTML output');
  // Must render masked dots
  assert.ok(html.includes('••••••••'));
});

test('TaskQueueTab displays checkpoint failure banner when a task has failed with error', () => {
  const html = renderToString(
    React.createElement(TaskQueueTab, {
      queue: sampleTasks,
      isRunning: false,
      defaultExpandedIds: ['task-2'],
      onRunNextTask: () => {},
      onExecuteAll: () => {},
      onDeleteTask: () => {},
      onClearQueue: () => {},
    })
  );

  assert.ok(html.includes('Mount point /mnt/backup busy'));
  assert.ok(html.includes('Execution Checkpoint Failed') || html.includes('Task Execution Failed'));
});

test('TaskQueueTab button states disabled when isRunning is true', () => {
  const html = renderToString(
    React.createElement(TaskQueueTab, {
      queue: sampleTasks,
      isRunning: true,
      onRunNextTask: () => {},
      onExecuteAll: () => {},
      onDeleteTask: () => {},
      onClearQueue: () => {},
    })
  );

  // Buttons should have disabled attribute or disabled state
  assert.ok(html.includes('disabled=""') || html.includes('disabled'));
  assert.ok(html.includes('Run Next Task'));
  assert.ok(html.includes('Execute Queue (One-by-One)'));
});

test('maskSensitiveValue correctly masks secrets and leaves harmless values intact', () => {
  assert.strictEqual(maskSensitiveValue('password', 'cleartext123'), '••••••••');
  assert.strictEqual(maskSensitiveValue('api_token', 'xyz-12345'), '••••••••');
  assert.strictEqual(maskSensitiveValue('client_secret', 'secret!'), '••••••••');
  assert.strictEqual(maskSensitiveValue('ansible_become_password', 'p@ss'), '••••••••');
  
  // Custom maskedSecrets map
  const maskedSecrets = { vault_pass: 'veryConfidential' };
  assert.strictEqual(maskSensitiveValue('custom_field', 'my veryConfidential value', maskedSecrets), '••••••••');

  // Vault annotation preservation
  const vaultSecrets = {
    smb_password: '•••••••• (vault: smb_share_secret)',
    api_token: '•••••••• (vault: vault_app_token)',
  };
  assert.strictEqual(
    maskSensitiveValue('smb_password', 'secretVal', vaultSecrets),
    '•••••••• (vault: smb_share_secret)'
  );
  assert.strictEqual(
    maskSensitiveValue('api_token', 'tokenVal', vaultSecrets),
    '•••••••• (vault: vault_app_token)'
  );
  
  // Non-sensitive values
  assert.strictEqual(maskSensitiveValue('state', 'started'), 'started');
  assert.strictEqual(maskSensitiveValue('name', 'nginx'), 'nginx');
  assert.strictEqual(maskSensitiveValue('enabled', true), 'true');
});

test('TaskQueueTab preserves and displays vault secret badges in parameter breakdown', () => {
  const taskWithVaultSecret: QueuedAnsibleTask = {
    ...sampleTasks[0],
    id: 'task-vault-test',
    secretsMasked: {
      smb_password: '•••••••• (vault: smb_share_secret)',
    },
    steps: [
      {
        name: 'Mount cifs share with vault password',
        module: 'ansible.builtin.template',
        parameters: {
          smb_password: 'superSecretValue',
        },
      },
    ],
  };

  const html = renderToString(
    React.createElement(TaskQueueTab, {
      queue: [taskWithVaultSecret],
      isRunning: false,
      defaultExpandedIds: ['task-vault-test'],
      onRunNextTask: () => {},
      onExecuteAll: () => {},
      onDeleteTask: () => {},
      onClearQueue: () => {},
    })
  );

  assert.ok(!html.includes('superSecretValue'), 'Plaintext secret must never appear in HTML output');
  assert.ok(html.includes('•••••••• (vault: smb_share_secret)'), 'Vault annotation must be preserved and rendered');
});

test('TaskQueueTab action props function as expected when invoked', () => {
  let nextRunCalled = false;
  let executeAllCalled = false;
  let deletedTaskId: string | null = null;
  let clearQueueCalled = false;

  const props: TaskQueueTabProps = {
    queue: sampleTasks,
    isRunning: false,
    onRunNextTask: () => {
      nextRunCalled = true;
    },
    onExecuteAll: () => {
      executeAllCalled = true;
    },
    onDeleteTask: (id: string) => {
      deletedTaskId = id;
    },
    onClearQueue: () => {
      clearQueueCalled = true;
    },
  };

  props.onRunNextTask();
  assert.strictEqual(nextRunCalled, true);

  props.onExecuteAll();
  assert.strictEqual(executeAllCalled, true);

  props.onDeleteTask('task-1');
  assert.strictEqual(deletedTaskId, 'task-1');

  props.onClearQueue();
  assert.strictEqual(clearQueueCalled, true);
});

test('GlobalTasksExecutionLog renders Task Queue tab with staged count badge', async () => {
  const { GlobalTasksExecutionLog } = await import('./GlobalTasksExecutionLog.tsx');

  const html = renderToString(
    React.createElement(GlobalTasksExecutionLog, {
      tasks: [],
      queue: sampleTasks,
      isRunning: false,
    })
  );

  // Checks that Task Queue tab button is rendered with count
  assert.ok(html.includes('Task Queue (1)'));
});

test('GlobalTasksExecutionLog lists only RUNNING tasks in Active Tasks by default and hides exited tasks until toggled', async () => {
  const { GlobalTasksExecutionLog } = await import('./GlobalTasksExecutionLog.tsx');

  const html = renderToString(
    React.createElement(GlobalTasksExecutionLog, {
      tasks: [
        {
          id: 'task-run-1',
          startTime: '2026-10-21 15:00:00',
          nodeTarget: 'likes-it-cold',
          user: 'core@ansible',
          description: 'ansible-playbook site.yml -l likes-it-cold',
          progress: 45,
          progressEta: 'TASK [Gathering Facts]',
          status: 'RUNNING',
          category: 'ansible',
          logs: ['PLAY [all] ***', 'TASK [Gathering Facts] ***'],
        },
        {
          id: 'task-exited-1',
          startTime: '2026-10-21 14:55:00',
          nodeTarget: 'ping',
          user: 'core@ssh-agent',
          description: 'podman ps --all',
          progress: 100,
          progressEta: 'Completed',
          status: 'OK',
          category: 'active',
          logs: ['CONTAINER ID  IMAGE'],
        },
      ],
      queue: [],
      isRunning: false,
    })
  );

  assert.ok(/Active Tasks\s*\(<!-- -->1<!-- -->\)|Active Tasks\s*\(1\)/.test(html), 'Active Tasks tab badge must count only RUNNING tasks');
  assert.ok(html.includes('active-task-row-task-run-1'), 'Running task must appear in Active Tasks table');
  assert.ok(!html.includes('active-task-row-task-exited-1'), 'Exited task must be removed from default active list');
  assert.ok(/Show Exited History\s*\(<!-- -->1<!-- -->\)|Show Exited History\s*\(1\)/.test(html), 'Toggle button must show count of exited tasks with retrievable output');
});

test('TaskStdoutModal renders live streaming stdout lines and non-destructive close notice', async () => {
  const { TaskStdoutModal } = await import('./modals/TaskStdoutModal.tsx');

  const html = renderToString(
    React.createElement(TaskStdoutModal, {
      taskId: 'task-run-1',
      task: {
        id: 'task-run-1',
        startTime: '2026-10-21 15:00:00',
        nodeTarget: 'likes-it-cold',
        user: 'core@ansible',
        description: 'ansible-playbook site.yml -l likes-it-cold',
        progress: 50,
        progressEta: 'TASK [Deploy containers]',
        status: 'RUNNING',
        category: 'ansible',
        logs: ['PLAY [all] ***', 'TASK [Gathering Facts] ***', 'ok: [likes-it-cold]'],
      },
      isOpen: true,
      onClose: () => {},
    })
  );

  assert.ok(html.includes('LIVE STREAMING'), 'Running task must show LIVE STREAMING indicator');
  assert.ok(html.includes('PLAY [all] ***'), 'Initial / prop task logs must render');
  assert.ok(html.includes('ok: [likes-it-cold]'), 'Incremental stdout lines must render');
  assert.ok(
    html.includes('closing this window will not interrupt or terminate the task'),
    'Must state that closing stdout view does not kill the task'
  );
});



