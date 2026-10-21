import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import http from 'node:http';
import {
  clearAnsibleModuleCaches,
  getAnsibleDocBinary,
  getAnsibleModuleSpec,
  isSafeModuleName,
  listAnsibleModules,
  parseModuleCatalog,
  registerAnsibleModuleRoutes,
} from './ansibleModuleSpecs.ts';
import { registerPlaybookYamlRoutes } from './playbookYaml.ts';

const ANSIBLE_DOC = getAnsibleDocBinary();
const hasAnsibleDoc = fs.existsSync(ANSIBLE_DOC);

test('isSafeModuleName only accepts real ansible module identifiers', () => {
  assert.equal(isSafeModuleName('ansible.builtin.service'), true);
  assert.equal(isSafeModuleName('service'), true);
  assert.equal(isSafeModuleName('community.general.ufw'), true);
  assert.equal(isSafeModuleName('../etc/passwd'), false);
  assert.equal(isSafeModuleName('ansible.builtin.ser vice'), false);
  assert.equal(isSafeModuleName('; rm -rf /'), false);
  assert.equal(isSafeModuleName(''), false);
  assert.equal(isSafeModuleName(42), false);
});

test('parseModuleCatalog reads real ansible-doc -l output', () => {
  const stdout = [
    'ansible.builtin.apt                                Manages apt-packages',
    'ansible.builtin.service                            Manage services',
    'community.general.ufw                              Manage firewall rules with ufw',
    '',
    'garbage line without padding',
  ].join('\n');

  const entries = parseModuleCatalog(stdout);
  assert.deepEqual(entries, [
    { name: 'ansible.builtin.apt', description: 'Manages apt-packages' },
    { name: 'ansible.builtin.service', description: 'Manage services' },
    { name: 'community.general.ufw', description: 'Manage firewall rules with ufw' },
  ]);
});

test('the module catalogue and specs come from the Ansible virtualenv', async t => {
  if (!hasAnsibleDoc) {
    t.skip(`ansible-doc not found at ${ANSIBLE_DOC}`);
    return;
  }

  clearAnsibleModuleCaches();
  const modules = listAnsibleModules();
  assert.ok(modules.length > 0, 'ansible-doc should list modules');

  const service = getAnsibleModuleSpec('ansible.builtin.service');
  assert.equal(service.name, 'ansible.builtin.service');
  assert.match(service.shortDescription, /service/i);

  const nameOption = service.options.find(option => option.name === 'name');
  assert.ok(nameOption, 'service has a name option');
  assert.equal(nameOption?.required, true);

  const stateOption = service.options.find(option => option.name === 'state');
  assert.ok(stateOption?.choices?.includes('restarted'), 'state exposes its real choices');

  const enabledOption = service.options.find(option => option.name === 'enabled');
  assert.equal(enabledOption?.type, 'bool');
});

test('unknown modules fail loudly instead of returning an invented spec', async t => {
  if (!hasAnsibleDoc) {
    t.skip(`ansible-doc not found at ${ANSIBLE_DOC}`);
    return;
  }
  assert.throws(() => getAnsibleModuleSpec('ansible.builtin.definitely_not_a_module'));
});

test('module metadata and YAML parse endpoints serve real data', async t => {
  if (!hasAnsibleDoc) {
    t.skip(`ansible-doc not found at ${ANSIBLE_DOC}`);
    return;
  }

  const routes: Array<{ method: string; path: string; handler: Function }> = [];
  const app = {
    get: (p: string, h: Function) => routes.push({ method: 'GET', path: p, handler: h }),
    post: (p: string, h: Function) => routes.push({ method: 'POST', path: p, handler: h }),
    put: (p: string, h: Function) => routes.push({ method: 'PUT', path: p, handler: h }),
    delete: (p: string, h: Function) => routes.push({ method: 'DELETE', path: p, handler: h }),
  };
  registerAnsibleModuleRoutes(app);
  registerPlaybookYamlRoutes(app);

  const server = http.createServer((req, res) => {
    const parsedUrl = new URL(req.url || '/', 'http://127.0.0.1');
    const pathname = parsedUrl.pathname;
    const method = req.method || 'GET';

    let body = '';
    req.on('data', chunk => { body += chunk; });
    req.on('end', async () => {
      let parsedBody = {};
      if (body) {
        try { parsedBody = JSON.parse(body); } catch {}
      }

      for (const r of routes) {
        if (r.method !== method) continue;
        const rParts = r.path.split('/');
        const uParts = pathname.split('/');
        if (rParts.length !== uParts.length) continue;

        const params: Record<string, string> = {};
        let match = true;
        for (let i = 0; i < rParts.length; i++) {
          if (rParts[i].startsWith(':')) {
            params[rParts[i].slice(1)] = decodeURIComponent(uParts[i]);
          } else if (rParts[i] !== uParts[i]) {
            match = false;
            break;
          }
        }

        if (match) {
          const reqMock = { params, body: parsedBody, query: Object.fromEntries(parsedUrl.searchParams) };
          const resMock = {
            status(code: number) {
              res.statusCode = code;
              return resMock;
            },
            json(data: any) {
              res.setHeader('Content-Type', 'application/json');
              res.end(JSON.stringify(data));
              return resMock;
            },
          };
          await r.handler(reqMock, resMock);
          return;
        }
      }

      res.statusCode = 404;
      res.end(JSON.stringify({ error: 'Not found' }));
    });
  });

  server.listen(0, '127.0.0.1');
  await new Promise<void>(resolve => server.once('listening', () => resolve()));
  const address = server.address();
  assert.ok(address && typeof address === 'object');
  const baseUrl = `http://127.0.0.1:${address.port}`;

  try {
    const listRes = await fetch(`${baseUrl}/api/ansible/modules`);
    assert.equal(listRes.status, 200);
    const listBody = await listRes.json();
    assert.equal(listBody.success, true);
    assert.ok(listBody.total > 0);
    assert.ok(
      listBody.modules.some((entry: any) => entry.name === 'ansible.builtin.service'),
      'the catalogue should include ansible.builtin.service'
    );

    const filtered = await (await fetch(`${baseUrl}/api/ansible/modules?q=service`)).json();
    assert.ok(filtered.modules.every((entry: any) => /service/i.test(entry.name + entry.description)));

    const specRes = await fetch(`${baseUrl}/api/ansible/modules/ansible.builtin.service/spec`);
    assert.equal(specRes.status, 200);
    const specBody = await specRes.json();
    assert.equal(specBody.success, true);
    assert.equal(specBody.spec.name, 'ansible.builtin.service');
    assert.ok(specBody.spec.options.some((option: any) => option.name === 'name' && option.required));

    const missingRes = await fetch(`${baseUrl}/api/ansible/modules/ansible.builtin.definitely_not_a_module/spec`);
    assert.equal(missingRes.status, 404);

    const invalidRes = await fetch(`${baseUrl}/api/ansible/modules/not-a-module!/spec`);
    assert.equal(invalidRes.status, 400);

    const parseRes = await fetch(`${baseUrl}/api/playbooks/parse`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        content: '---\n- name: Play\n  hosts: all\n  tasks:\n    - name: Ping\n      ping:\n',
      }),
    });
    assert.equal(parseRes.status, 200);
    const parseBody = await parseRes.json();
    assert.equal(parseBody.success, true);
    assert.deepEqual(parseBody.document, [
      { name: 'Play', hosts: 'all', tasks: [{ name: 'Ping', ping: null }] },
    ]);

    const brokenRes = await fetch(`${baseUrl}/api/playbooks/parse`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ content: '- [unclosed\n' }),
    });
    assert.equal(brokenRes.status, 400);
    const brokenBody = await brokenRes.json();
    assert.equal(brokenBody.success, false);
    assert.ok(brokenBody.error);

    const badRequest = await fetch(`${baseUrl}/api/playbooks/parse`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ content: 42 }),
    });
    assert.equal(badRequest.status, 400);
  } finally {
    await new Promise<void>(resolve => server.close(() => resolve()));
  }
});
