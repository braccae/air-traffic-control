import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import {
  cloneDocument,
  coerceFieldValue,
  createTaskForModule,
  describeTaskShape,
  describeVisualEditorSupport,
  fieldValueToText,
  getPlayLabel,
  getPlayTasks,
  getTaskArgs,
  getTaskDirectives,
  getTaskModule,
  optionFieldKind,
  serializePlaybook,
  serializeScalar,
  setTaskArg,
  setTaskDirective,
  type AnsibleModuleSpec,
} from './playbookVisualModel.ts';
import { getVenvPythonBinary, parsePlaybookYaml } from './playbookYaml.ts';

const VENV_PYTHON = getVenvPythonBinary();
const hasVenv = fs.existsSync(VENV_PYTHON);

const SERVICE_SPEC: AnsibleModuleSpec = {
  name: 'ansible.builtin.service',
  requestedName: 'ansible.builtin.service',
  collection: 'ansible.builtin',
  shortDescription: 'Manage services',
  description: '',
  options: [
    { name: 'name', type: 'str', required: true },
    {
      name: 'state',
      type: 'str',
      required: false,
      choices: ['reloaded', 'restarted', 'started', 'stopped'],
    },
    { name: 'enabled', type: 'bool', required: false },
    { name: 'sleep', type: 'int', required: false },
    { name: 'arguments', type: 'str', required: false, default: '' },
  ],
};

const PLAYBOOK = `---
- name: Site baseline
  hosts: tag:host
  gather_facts: false
  tasks:
    - name: Verify host reachability
      ping:

    - name: Restart nginx
      ansible.builtin.service:
        name: nginx
        state: restarted
      tags:
        - web
`;

test('task structure helpers find the module and keep directives separate', () => {
  const plays = [
    {
      name: 'Site baseline',
      hosts: 'tag:host',
      tasks: [
        { name: 'Verify host reachability', ping: null },
        {
          name: 'Restart nginx',
          'ansible.builtin.service': { name: 'nginx', state: 'restarted' },
          tags: ['web'],
          when: 'ansible_os_family == "Debian"',
        },
        { name: 'Uptime', command: 'uptime -p', register: 'uptime' },
        { block: [{ name: 'nested', ping: null }] },
      ],
    },
  ];

  const tasks = getPlayTasks(plays[0]);
  assert.equal(tasks.length, 4);
  assert.equal(getPlayLabel(plays[0], 0), 'Site baseline');

  assert.equal(getTaskModule(tasks[0]), 'ping');
  assert.deepEqual(getTaskArgs(tasks[0], 'ping'), {});

  assert.equal(getTaskModule(tasks[1]), 'ansible.builtin.service');
  assert.deepEqual(getTaskArgs(tasks[1], 'ansible.builtin.service'), { name: 'nginx', state: 'restarted' });
  assert.deepEqual(getTaskDirectives(tasks[1], 'ansible.builtin.service'), {
    name: 'Restart nginx',
    tags: ['web'],
    when: 'ansible_os_family == "Debian"',
  });

  // Free-form module arguments (`command: uptime -p`) survive as a raw string.
  assert.deepEqual(getTaskArgs(tasks[2], 'command'), { _raw: 'uptime -p' });

  assert.match(String(describeTaskShape(tasks[3])), /Block tasks/);
  assert.match(String(describeTaskShape({ name: 'x', action: 'ping' })), /action:/);
});

test('optionFieldKind picks real control types from the module spec', () => {
  assert.equal(optionFieldKind({ type: 'bool' }), 'boolean');
  assert.equal(optionFieldKind({ type: 'int' }), 'number');
  assert.equal(optionFieldKind({ type: 'float' }), 'number');
  assert.equal(optionFieldKind({ type: 'list' }), 'list');
  assert.equal(optionFieldKind({ type: 'dict' }), 'dict');
  assert.equal(optionFieldKind({ type: 'str' }), 'text');
  assert.equal(optionFieldKind({ type: 'path' }), 'text');
  // Choices always win: they become a select even for string options.
  assert.equal(optionFieldKind({ type: 'str', choices: ['a', 'b'] }), 'select');
});

test('createTaskForModule prefills defaults and required fields are preserved', () => {
  const task = createTaskForModule('ansible.builtin.service', SERVICE_SPEC);
  assert.equal(getTaskModule(task), 'ansible.builtin.service');
  assert.equal(task.name, 'Task using ansible.builtin.service');
  // `name` is required but has no default, so it starts empty rather than invented.
  assert.deepEqual(task['ansible.builtin.service'], {});
});

test('setTaskArg and setTaskDirective mutate and clear fields', () => {
  const task: Record<string, any> = { name: 'Restart nginx', 'ansible.builtin.service': { name: 'nginx' } };

  setTaskArg(task, 'ansible.builtin.service', 'state', 'restarted');
  assert.equal(task['ansible.builtin.service'].state, 'restarted');

  setTaskArg(task, 'ansible.builtin.service', 'state', undefined);
  assert.equal('state' in task['ansible.builtin.service'], false);

  setTaskDirective(task, 'when', 'ansible_facts.os_family == "Debian"');
  assert.equal(task.when, 'ansible_facts.os_family == "Debian"');
  setTaskDirective(task, 'when', '');
  assert.equal('when' in task, false);

  // A task written in the free-form style gains a structured argument map.
  const shorthand: Record<string, any> = { command: 'uptime -p' };
  setTaskArg(shorthand, 'command', 'cmd', 'uptime -p');
  setTaskArg(shorthand, 'command', '_raw', undefined);
  assert.deepEqual(shorthand.command, { cmd: 'uptime -p' });
});

test('coerceFieldValue and fieldValueToText round-trip form values', () => {
  assert.equal(coerceFieldValue('number', '5'), 5);
  assert.equal(coerceFieldValue('number', 'abc'), 'abc');
  assert.equal(coerceFieldValue('text', 'nginx'), 'nginx');
  assert.deepEqual(coerceFieldValue('list', 'web\n db\n\n'), ['web', 'db']);
  assert.deepEqual(coerceFieldValue('dict', '{"a":1}'), { a: 1 });
  assert.equal(coerceFieldValue('text', '   '), undefined);

  assert.equal(fieldValueToText(undefined), '');
  assert.equal(fieldValueToText(false), 'false');
  assert.equal(fieldValueToText(['a', 'b']), 'a\nb');
  assert.equal(fieldValueToText({ a: 1 }), '{\n  "a": 1\n}');
});

test('serializeScalar quotes only what YAML would otherwise reinterpret', () => {
  assert.equal(serializeScalar('nginx'), 'nginx');
  assert.equal(serializeScalar('ansible.builtin.service'), 'ansible.builtin.service');
  assert.equal(serializeScalar('true'), '"true"');
  assert.equal(serializeScalar('123'), '"123"');
  assert.equal(serializeScalar(''), '""');
  assert.equal(serializeScalar(null), 'null');
  assert.equal(serializeScalar(true), 'true');
  assert.equal(serializeScalar(5), '5');
  assert.equal(serializeScalar('{{ host_uptime.stdout }}'), '"{{ host_uptime.stdout }}"');
  assert.equal(serializeScalar('multi\nline'), '"multi\\nline"');
});

test('serializePlaybook emits the standard playbook layout', () => {
  const doc = [
    {
      name: 'Site baseline',
      hosts: 'tag:host',
      gather_facts: false,
      tasks: [
        { name: 'Verify host reachability', ping: null },
        {
          name: 'Restart nginx',
          'ansible.builtin.service': { name: 'nginx', state: 'restarted' },
          tags: ['web'],
        },
      ],
    },
  ];
  const text = serializePlaybook(doc);
  assert.ok(text.startsWith('---\n'));
  assert.ok(text.includes('- name: Site baseline'));
  assert.ok(text.includes('  gather_facts: false'));
  assert.ok(text.includes('    - name: Verify host reachability'));
  assert.ok(text.includes('      ping: null'));
  assert.ok(text.includes('        state: restarted'));
  assert.ok(text.includes('      tags:\n        - web'));
});

test('visual editor support checks reject non-playbook documents with a reason', () => {
  assert.equal(describeVisualEditorSupport([{ hosts: 'all' }]), null);
  assert.match(String(describeVisualEditorSupport('nope')), /list of plays/);
  assert.match(String(describeVisualEditorSupport(null)), /empty/);
  assert.match(String(describeVisualEditorSupport(['scalar'])), /Play 1 is not a mapping/);
});

test('cloneDocument isolates edits from the loaded document', () => {
  const original = [{ name: 'A', tasks: [{ ping: null }] }];
  const copy = cloneDocument(original);
  copy[0].tasks.push({ ping: null });
  assert.equal(original[0].tasks.length, 1);
});

test('the visual editor edits stay in sync with real YAML parsing', async t => {
  if (!hasVenv) {
    t.skip(`Ansible venv Python not found at ${VENV_PYTHON}`);
    return;
  }

  // Parse a real repository playbook, edit it the way the form does, and check
  // the rewritten YAML parses back to exactly the edited document.
  const playbookPath = path.resolve(process.cwd(), 'ansible/playbooks/site.yml');
  if (!fs.existsSync(playbookPath)) {
    t.skip('ansible/playbooks/site.yml is not present');
    return;
  }

  const original = fs.readFileSync(playbookPath, 'utf-8');
  const parsed = parsePlaybookYaml(original);
  assert.equal(parsed.success, true, parsed.success ? '' : parsed.error);
  if (!parsed.success) return;

  const doc = cloneDocument(parsed.document as any[]);
  const firstTask = getPlayTasks(doc[0])[0];
  assert.ok(firstTask, 'site.yml should have at least one task');

  const moduleName = getTaskModule(firstTask) || 'ansible.builtin.debug';
  const task: Record<string, any> = {
    name: 'Restart service from the field editor',
    [moduleName]: { name: 'nginx', state: 'restarted' },
  };
  setTaskDirective(task, 'when', 'ansible_facts.os_family == "Debian"');
  setTaskArg(task, moduleName, 'enabled', false);
  doc[0].tasks.push(task);

  const rewritten = serializePlaybook(doc);
  const reparsed = parsePlaybookYaml(rewritten);
  assert.equal(reparsed.success, true, reparsed.success ? '' : reparsed.error);
  if (!reparsed.success) return;

  assert.deepEqual(reparsed.document, doc);
});

test('inline playbook text round-trips through the real parser', async t => {
  if (!hasVenv) {
    t.skip(`Ansible venv Python not found at ${VENV_PYTHON}`);
    return;
  }

  const parsed = parsePlaybookYaml(PLAYBOOK);
  assert.equal(parsed.success, true, parsed.success ? '' : parsed.error);
  if (!parsed.success) return;

  assert.deepEqual(parsed.document, [
    {
      name: 'Site baseline',
      hosts: 'tag:host',
      gather_facts: false,
      tasks: [
        { name: 'Verify host reachability', ping: null },
        {
          name: 'Restart nginx',
          'ansible.builtin.service': { name: 'nginx', state: 'restarted' },
          tags: ['web'],
        },
      ],
    },
  ]);

  const reparsed = parsePlaybookYaml(serializePlaybook(parsed.document));
  assert.equal(reparsed.success, true, reparsed.success ? '' : reparsed.error);
  if (!reparsed.success) return;
  assert.deepEqual(reparsed.document, parsed.document);
});

test('invalid YAML reports the real parser error with a line number', async t => {
  if (!hasVenv) {
    t.skip(`Ansible venv Python not found at ${VENV_PYTHON}`);
    return;
  }

  const result = parsePlaybookYaml('- name: broken\n  hosts: all\n  tasks:\n   - name: x\n     ping: [\n');
  assert.equal(result.success, false);
  if (result.success) return;
  assert.ok(result.error.length > 0);
  assert.equal(typeof result.line, 'number');
});
