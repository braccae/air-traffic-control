import { describe, it, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import http from 'node:http';
import os from 'node:os';
import {
  validatePlaybookName,
  normalizePlaybookName,
  getStarterPlaybookTemplate,
  isProtectedPlaybook,
  listPlaybooks,
  readPlaybook,
  createPlaybook,
  updatePlaybook,
  deletePlaybook,
} from './playbookManager.ts';

describe('Ansible Playbook Manager & Validation', () => {
  describe('validatePlaybookName', () => {
    it('accepts valid playbook names with .yml and .yaml extensions', () => {
      const validNames = [
        'site.yml',
        'security-hardening.yml',
        'tailscale-status.yaml',
        'backup_job_01.yml',
        'update-all.yml',
      ];
      for (const name of validNames) {
        const res = validatePlaybookName(name);
        assert.equal(res.valid, true, `Expected ${name} to be valid`);
        assert.equal(res.name, name);
      }
    });

    it('rejects empty, non-string, or whitespace-only names', () => {
      const invalid = ['', '   ', null as any, undefined as any];
      for (const val of invalid) {
        const res = validatePlaybookName(val);
        assert.equal(res.valid, false);
        assert.ok(res.error);
      }
    });

    it('rejects directory traversal and slashes', () => {
      const traversalNames = [
        '../secret.yml',
        '../../etc/shadow.yml',
        'subdir/play.yml',
        'subdir\\play.yml',
        '..\\secret.yml',
        'foo/../bar.yml',
      ];
      for (const name of traversalNames) {
        const res = validatePlaybookName(name);
        assert.equal(res.valid, false, `Expected traversal attempt ${name} to be rejected`);
        assert.ok(res.error?.includes('traversal') || res.error?.includes('path'));
      }
    });

    it('rejects names without .yml or .yaml extension', () => {
      const nonYaml = ['script.sh', 'site.json', 'playbook.txt', 'site', '.yml'];
      for (const name of nonYaml) {
        const res = validatePlaybookName(name);
        assert.equal(res.valid, false, `Expected non-yaml name ${name} to be rejected`);
        assert.ok(res.error?.includes('extension') || res.error?.includes('.yml'));
      }
    });

    it('rejects names containing dangerous shell or filesystem characters', () => {
      const dangerous = ['play;rm -rf.yml', 'play|cat.yml', 'play`id`.yml', 'play$HOME.yml', 'play\0.yml'];
      for (const name of dangerous) {
        const res = validatePlaybookName(name);
        assert.equal(res.valid, false, `Expected dangerous character in ${name} to be rejected`);
      }
    });
  });

  describe('normalizePlaybookName', () => {
    it('appends .yml if extension is omitted', () => {
      assert.equal(normalizePlaybookName('wireguard-restart'), 'wireguard-restart.yml');
      assert.equal(normalizePlaybookName('backup_db'), 'backup_db.yml');
    });

    it('preserves existing .yml or .yaml extension', () => {
      assert.equal(normalizePlaybookName('wireguard-restart.yml'), 'wireguard-restart.yml');
      assert.equal(normalizePlaybookName('status.yaml'), 'status.yaml');
    });
  });

  describe('getStarterPlaybookTemplate', () => {
    it('generates valid starter playbook YAML with custom or default name', () => {
      const template1 = getStarterPlaybookTemplate('wireguard-restart.yml');
      assert.ok(template1.includes('---'));
      assert.ok(template1.includes('name: Wireguard Restart'));
      assert.ok(template1.includes('hosts:'));
      assert.ok(template1.includes('tasks:'));

      const templateDefault = getStarterPlaybookTemplate();
      assert.ok(templateDefault.includes('---'));
      assert.ok(templateDefault.includes('hosts:'));
      assert.ok(templateDefault.includes('tasks:'));
    });
  });

  describe('isProtectedPlaybook', () => {
    it('identifies baseline cluster playbooks as protected from deletion', () => {
      assert.equal(isProtectedPlaybook('site.yml'), true);
      assert.equal(isProtectedPlaybook('site.yaml'), true);
      assert.equal(isProtectedPlaybook('custom.yml'), false);
      assert.equal(isProtectedPlaybook('wireguard-restart.yml'), false);
    });
  });

  describe('CRUD File Operations', () => {
    let tempDir: string;

    beforeEach(() => {
      tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'atc-playbooks-test-'));
    });

    afterEach(() => {
      if (fs.existsSync(tempDir)) {
        fs.rmSync(tempDir, { recursive: true, force: true });
      }
    });

    it('lists playbooks in directory and ignores non-yaml files', () => {
      fs.writeFileSync(path.join(tempDir, 'site.yml'), '---');
      fs.writeFileSync(path.join(tempDir, 'deploy.yaml'), '---');
      fs.writeFileSync(path.join(tempDir, 'notes.txt'), 'ignore me');
      fs.writeFileSync(path.join(tempDir, '.hidden.yml'), '---');

      const list = listPlaybooks(tempDir);
      assert.deepEqual(list, ['site.yml', 'deploy.yaml']);
    });

    it('handles non-existent directory gracefully in listPlaybooks', () => {
      const list = listPlaybooks(path.join(tempDir, 'does-not-exist'));
      assert.deepEqual(list, []);
    });

    it('creates a playbook with default starter template if content is omitted', () => {
      const res = createPlaybook(tempDir, 'test-new.yml');
      assert.equal(res.name, 'test-new.yml');
      assert.ok(res.content.includes('---'));
      assert.ok(res.content.includes('tasks:'));

      const onDisk = fs.readFileSync(path.join(tempDir, 'test-new.yml'), 'utf-8');
      assert.equal(onDisk, res.content);
    });

    it('creates a playbook with provided content', () => {
      const customContent = '---\n- name: Custom\n  hosts: all\n';
      const res = createPlaybook(tempDir, 'custom.yml', customContent);
      assert.equal(res.name, 'custom.yml');
      assert.equal(res.content, customContent);
    });

    it('throws error when creating a playbook that already exists', () => {
      createPlaybook(tempDir, 'exists.yml', 'first');
      assert.throws(() => {
        createPlaybook(tempDir, 'exists.yml', 'second');
      }, /already exists/i);
    });

    it('throws error when creating a playbook with path traversal', () => {
      assert.throws(() => {
        createPlaybook(tempDir, '../escaped.yml', 'hack');
      }, /traversal|invalid/i);
    });

    it('reads existing playbook content', () => {
      const content = '---\n- name: Read Test\n';
      fs.writeFileSync(path.join(tempDir, 'read-test.yml'), content);

      const res = readPlaybook(tempDir, 'read-test.yml');
      assert.equal(res.name, 'read-test.yml');
      assert.equal(res.content, content);
    });

    it('throws 404-like error when reading a non-existent playbook', () => {
      assert.throws(() => {
        readPlaybook(tempDir, 'ghost.yml');
      }, /not found/i);
    });

    it('updates playbook content', () => {
      fs.writeFileSync(path.join(tempDir, 'update.yml'), 'old content');
      const updated = updatePlaybook(tempDir, 'update.yml', 'new content');
      assert.equal(updated.name, 'update.yml');
      assert.equal(updated.content, 'new content');

      const onDisk = fs.readFileSync(path.join(tempDir, 'update.yml'), 'utf-8');
      assert.equal(onDisk, 'new content');
    });

    it('throws when updating a non-existent playbook', () => {
      assert.throws(() => {
        updatePlaybook(tempDir, 'ghost.yml', 'content');
      }, /not found/i);
    });

    it('deletes an existing non-protected playbook', () => {
      fs.writeFileSync(path.join(tempDir, 'deleteme.yml'), 'bye');
      const res = deletePlaybook(tempDir, 'deleteme.yml');
      assert.equal(res.name, 'deleteme.yml');
      assert.equal(fs.existsSync(path.join(tempDir, 'deleteme.yml')), false);
    });

    it('prevents deleting protected baseline playbooks', () => {
      fs.writeFileSync(path.join(tempDir, 'site.yml'), '---');
      assert.throws(() => {
        deletePlaybook(tempDir, 'site.yml');
      }, /cannot delete protected/i);
      assert.equal(fs.existsSync(path.join(tempDir, 'site.yml')), true);
    });

    it('throws when deleting a non-existent playbook', () => {
      assert.throws(() => {
        deletePlaybook(tempDir, 'ghost.yml');
      }, /not found/i);
    });
  });

  describe('Playbook Express HTTP API Endpoints', () => {
    let tempDir: string;
    let server: any;
    let baseUrl: string;

    beforeEach(async () => {
      tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'atc-api-test-'));
      // Seed site.yml
      fs.writeFileSync(path.join(tempDir, 'site.yml'), '---\n- name: Baseline Site\n');

      const { registerPlaybookRoutes } = await import('./playbookManager.ts');
      const routes: Array<{ method: string; path: string; handler: Function }> = [];
      const app = {
        get: (p: string, h: Function) => routes.push({ method: 'GET', path: p, handler: h }),
        post: (p: string, h: Function) => routes.push({ method: 'POST', path: p, handler: h }),
        put: (p: string, h: Function) => routes.push({ method: 'PUT', path: p, handler: h }),
        delete: (p: string, h: Function) => routes.push({ method: 'DELETE', path: p, handler: h }),
      };
      registerPlaybookRoutes(app, tempDir);

      server = http.createServer((req, res) => {
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

          // Match routes
          for (const r of routes) {
            if (r.method !== method) continue;
            // Handle param matching (e.g. /api/playbooks/:name)
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

      await new Promise<void>((resolve) => {
        server.listen(0, '127.0.0.1', () => {
          const addr = server.address();
          baseUrl = `http://127.0.0.1:${addr.port}`;
          resolve();
        });
      });
    });

    afterEach(async () => {
      if (server) {
        await new Promise<void>((resolve) => server.close(resolve));
      }
      if (fs.existsSync(tempDir)) {
        fs.rmSync(tempDir, { recursive: true, force: true });
      }
    });

    it('GET /api/playbooks returns list of playbooks', async () => {
      const res = await fetch(`${baseUrl}/api/playbooks`);
      assert.equal(res.status, 200);
      const data = await res.json();
      assert.deepEqual(data.playbooks, ['site.yml']);
    });

    it('GET /api/playbooks/:name returns playbook content', async () => {
      const res = await fetch(`${baseUrl}/api/playbooks/site.yml`);
      assert.equal(res.status, 200);
      const data = await res.json();
      assert.equal(data.success, true);
      assert.equal(data.name, 'site.yml');
      assert.ok(data.content.includes('Baseline Site'));
    });

    it('GET /api/playbooks/:name rejects directory traversal with 400', async () => {
      const res = await fetch(`${baseUrl}/api/playbooks/..%2fsecrets.yml`);
      assert.equal(res.status, 400);
      const data = await res.json();
      assert.ok(data.error);
    });

    it('GET /api/playbooks/:name returns 404 for missing playbook', async () => {
      const res = await fetch(`${baseUrl}/api/playbooks/missing.yml`);
      assert.equal(res.status, 404);
    });

    it('POST /api/playbooks creates a new playbook with starter template', async () => {
      const res = await fetch(`${baseUrl}/api/playbooks`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: 'wireguard-restart' }),
      });
      assert.equal(res.status, 201);
      const data = await res.json();
      assert.equal(data.success, true);
      assert.equal(data.name, 'wireguard-restart.yml');
      assert.ok(data.content.includes('Wireguard Restart'));
      assert.equal(fs.existsSync(path.join(tempDir, 'wireguard-restart.yml')), true);
    });

    it('POST /api/playbooks returns 409 if playbook already exists', async () => {
      const res = await fetch(`${baseUrl}/api/playbooks`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: 'site.yml' }),
      });
      assert.equal(res.status, 409);
    });

    it('PUT /api/playbooks/:name updates existing playbook content', async () => {
      const updatedContent = '---\n- name: Updated Site\n  hosts: all\n';
      const res = await fetch(`${baseUrl}/api/playbooks/site.yml`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ content: updatedContent }),
      });
      assert.equal(res.status, 200);
      const data = await res.json();
      assert.equal(data.success, true);
      assert.equal(data.name, 'site.yml');
      const diskContent = fs.readFileSync(path.join(tempDir, 'site.yml'), 'utf-8');
      assert.equal(diskContent, updatedContent);
    });

    it('PUT /api/playbooks/:name returns 404 if playbook does not exist', async () => {
      const res = await fetch(`${baseUrl}/api/playbooks/nonexistent.yml`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ content: 'new content' }),
      });
      assert.equal(res.status, 404);
    });

    it('DELETE /api/playbooks/:name prevents deleting protected site.yml (403)', async () => {
      const res = await fetch(`${baseUrl}/api/playbooks/site.yml`, {
        method: 'DELETE',
      });
      assert.equal(res.status, 403);
      assert.equal(fs.existsSync(path.join(tempDir, 'site.yml')), true);
    });

    it('DELETE /api/playbooks/:name deletes non-protected playbook (200)', async () => {
      fs.writeFileSync(path.join(tempDir, 'custom.yml'), 'custom');
      const res = await fetch(`${baseUrl}/api/playbooks/custom.yml`, {
        method: 'DELETE',
      });
      assert.equal(res.status, 200);
      assert.equal(fs.existsSync(path.join(tempDir, 'custom.yml')), false);
    });
  });
});

