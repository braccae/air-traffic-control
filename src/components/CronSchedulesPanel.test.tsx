import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import { renderToString } from 'react-dom/server';
import {
  CronSchedulesPanel,
  buildSchedulePayload,
  describeScheduleScope,
  describeScheduleWork,
  formatCronRunTime,
} from './CronSchedulesPanel.tsx';

const baseForm = {
  name: 'Nightly baseline',
  cronExpression: '0 3 * * *',
  enabled: true,
  scopeKind: 'host' as const,
  scopeTarget: 'alpha',
  taskKind: 'playbook' as const,
  playbook: 'site.yml',
  stepsJson: '[]',
};

test('CronSchedulesPanel renders the schedule section header', () => {
  const html = renderToString(<CronSchedulesPanel nodes={[]} />);
  assert.ok(html.includes('Scheduled Playbooks'));
  assert.ok(html.includes('datacenter-schedules'));
  assert.ok(html.includes('New Schedule'));
});

test('buildSchedulePayload builds a playbook schedule body', () => {
  const { body, error } = buildSchedulePayload(baseForm);
  assert.equal(error, undefined);
  assert.deepEqual(body, {
    name: 'Nightly baseline',
    cronExpression: '0 3 * * *',
    enabled: true,
    scope: { kind: 'host', target: 'alpha' },
    taskKind: 'playbook',
    payload: { playbook: 'site.yml' },
  });
});

test('buildSchedulePayload requires a target for host and group scopes', () => {
  assert.match(
    buildSchedulePayload({ ...baseForm, scopeTarget: '  ' }).error || '',
    /Target is required/
  );
  const datacenter = buildSchedulePayload({
    ...baseForm,
    scopeKind: 'datacenter',
    scopeTarget: '',
  });
  assert.equal(datacenter.error, undefined);
  assert.deepEqual((datacenter.body as any).scope, { kind: 'datacenter', target: 'all' });
});

test('buildSchedulePayload parses ansible_steps JSON and rejects invalid input', () => {
  const steps = [
    { name: 'Restart', module: 'ansible.builtin.systemd', parameters: { name: 'podman.socket' } },
  ];
  const ok = buildSchedulePayload({
    ...baseForm,
    taskKind: 'ansible_steps',
    stepsJson: JSON.stringify(steps),
  });
  assert.equal(ok.error, undefined);
  assert.equal((ok.body as any).taskKind, 'ansible_steps');
  assert.equal((ok.body as any).payload.steps.length, 1);

  assert.match(
    buildSchedulePayload({ ...baseForm, taskKind: 'ansible_steps', stepsJson: '{oops' }).error || '',
    /invalid/i
  );
  assert.match(
    buildSchedulePayload({ ...baseForm, taskKind: 'ansible_steps', stepsJson: '[]' }).error || '',
    /At least one Ansible step/
  );
});

test('describeScheduleScope and describeScheduleWork describe real schedule fields', () => {
  assert.equal(
    describeScheduleScope({ scope: { kind: 'group', target: 'worker' } }),
    'Group: tag:worker'
  );
  assert.equal(
    describeScheduleScope({ scope: { kind: 'datacenter', target: 'all' } }),
    'Datacenter: all'
  );
  assert.equal(
    describeScheduleWork({ taskKind: 'playbook', payload: { playbook: 'site.yml' } }),
    'ansible-playbook site.yml'
  );
  assert.equal(
    describeScheduleWork({
      taskKind: 'ansible_steps',
      payload: {
        title: 'Restart',
        steps: [
          { name: 'a', module: 'ansible.builtin.systemd', parameters: {} },
          { name: 'b', module: 'ansible.builtin.systemd', parameters: {} },
        ],
      },
    }),
    'Restart (2 steps)'
  );
});

test('formatCronRunTime renders placeholders for missing values', () => {
  assert.equal(formatCronRunTime(undefined), '—');
  assert.equal(formatCronRunTime(null), '—');
  assert.notEqual(formatCronRunTime(Date.now()), '—');
});
