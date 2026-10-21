import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import { renderToString } from 'react-dom/server';
import { PlaybookVisualEditor } from './PlaybookVisualEditor.tsx';
import type { AnsibleModuleSpec } from '../utils/playbookVisualModel.ts';

const SERVICE_SPEC: AnsibleModuleSpec = {
  name: 'ansible.builtin.service',
  requestedName: 'ansible.builtin.service',
  collection: 'ansible.builtin',
  shortDescription: 'Manage services',
  description: 'Controls services on remote hosts.',
  options: [
    { name: 'name', type: 'str', required: true, description: 'Name of the service.' },
    { name: 'state', type: 'str', required: false, choices: ['reloaded', 'restarted', 'started', 'stopped'] },
    { name: 'enabled', type: 'bool', required: false },
    { name: 'sleep', type: 'int', required: false },
    { name: 'arguments', type: 'str', required: false },
  ],
};

const DOCUMENT = [
  {
    name: 'Site baseline',
    hosts: 'tag:host',
    tasks: [
      {
        name: 'Restart nginx',
        'ansible.builtin.service': { name: 'nginx', state: 'restarted' },
        tags: ['web'],
      },
      { block: [{ name: 'Nested', 'ansible.builtin.service': { name: 'x' } }] },
    ],
  },
];

test('task parameters render as typed controls sourced from the module spec', () => {
  const html = renderToString(
    React.createElement(PlaybookVisualEditor, {
      document: DOCUMENT as any,
      onChange: () => {},
      preloadedSpecs: { 'ansible.builtin.service': SERVICE_SPEC },
    })
  );

  assert.ok(html.includes('data-testid="playbook-visual-editor"'));
  assert.ok(html.includes('data-testid="visual-play-card"'));
  assert.ok(html.includes('data-module="ansible.builtin.service"'));

  // Required + already-set parameters come straight from the ansible-doc spec.
  assert.ok(html.includes('data-field-kind="select"'), 'state renders as a select from its choices');
  assert.ok(html.includes('>restarted<'), 'the real choice list is offered');
  assert.ok(html.includes('data-field-kind="text"'), 'string parameters render as text inputs');
  assert.ok(html.includes('data-field-name="name"'), 'the task name is a real input');
  assert.ok(html.includes('data-field-name="tags"'), 'task keywords get their own inputs');

  // The form is not a single free-text box: each parameter has its own control.
  assert.ok(html.includes('data-field-kind="number"') === false, 'unset optional int stays hidden until requested');
  assert.ok(html.includes('show'), 'optional parameters can be revealed on demand');
});

test('block tasks are reported instead of being silently flattened', () => {
  const html = renderToString(
    React.createElement(PlaybookVisualEditor, {
      document: DOCUMENT as any,
      onChange: () => {},
      preloadedSpecs: { 'ansible.builtin.service': SERVICE_SPEC },
    })
  );
  assert.ok(html.includes('data-testid="visual-task-card-unsupported"'));
  assert.ok(html.includes('Block tasks are edited in YAML mode.'));
});

test('without a loaded spec the editor waits for ansible-doc instead of inventing fields', () => {
  const html = renderToString(
    React.createElement(PlaybookVisualEditor, {
      document: [
        {
          name: 'Play',
          hosts: 'all',
          tasks: [{ name: 'Something', 'ansible.builtin.copy': { dest: '/tmp/x' } }],
        },
      ] as any,
      onChange: () => {},
      preloadedSpecs: {},
    })
  );

  assert.ok(html.includes('loading module spec'));
  assert.ok(html.includes('data-testid="visual-add-step"'));
  assert.ok(!html.includes('data-field-kind="select"'), 'no fields are fabricated before the spec arrives');
});

test('boolean and numeric parameters use matching controls once a spec is loaded', () => {
  const spec: AnsibleModuleSpec = {
    ...SERVICE_SPEC,
    options: [...SERVICE_SPEC.options, { name: 'use', type: 'str', required: false }],
  };
  const html = renderToString(
    React.createElement(PlaybookVisualEditor, {
      document: [
        {
          name: 'Play',
          hosts: 'all',
          tasks: [
            {
              name: 'Restart nginx',
              'ansible.builtin.service': { name: 'nginx', enabled: false, sleep: 2 },
            },
          ],
        },
      ] as any,
      onChange: () => {},
      preloadedSpecs: { 'ansible.builtin.service': spec },
    })
  );

  assert.ok(html.includes('data-field-kind="boolean"'), 'bool options render as a true/false control');
  assert.ok(html.includes('data-field-kind="number"'), 'int options render as a number input');
  assert.ok(html.includes('data-field-kind="text"'), 'str options render as text inputs');
  assert.ok(html.includes('required'), 'required parameters are marked from the spec');
});
