import { describe, it, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { createHostCacheStore, registerInventoryRoutes, type HostCacheStore } from './hostCacheDb.ts';
import type { NodeItem, CustomFolder, TagInventoryChunk } from '../types/index.ts';

const baseTestNode: NodeItem = {
  id: 'node-test-1',
  name: 'test-node-1',
  shortName: 'test1',
  group: 'tag:worker',
  datacenter: 'leo-banjo.ts.net',
  nodeType: 'linux',
  badge: 'SSH',
  badgeColor: 'text-[#8cd6cb]',
  ip: '192.168.1.50',
  port: 22,
  sshUser: 'core',
  authMethod: 'ssh-ed25519',
  tags: ['tag:worker'],
  sshConnected: true,
  tailscaleIp: '100.64.0.1',
  tailscaleHost: 'test1.ts.net',
  osDescription: 'Linux',
  status: 'online',
  uptime: '1d',
  hasGlances: true,
  hasCockpit: false,
  hasPodman: true,
  hasRestic: false,
  specs: {
    cpu: 'AMD',
    vcpus: 4,
    ramGiB: 8,
    motherboard: 'Standard',
    bios: '1.0',
    ipmi: 'DHCP',
    ecc: 'None',
    secureBoot: 'Disabled',
    iommu: 'Enabled',
  },
};

describe('Inventory Manager: Tag Chunks & Custom Folders', () => {
  let store: HostCacheStore;

  beforeEach(() => {
    store = createHostCacheStore(':memory:');
  });

  afterEach(() => {
    store.close();
  });

  describe('Custom Folders SQLite Persistence', () => {
    it('creates, retrieves sorted by name, updates, and deletes custom folders', () => {
      const folderB: CustomFolder = {
        id: 'folder-b',
        name: 'Zeta Cold Storage',
        createdAt: 1000,
      };
      const folderA: CustomFolder = {
        id: 'folder-a',
        name: 'Alpha Hardware',
        createdAt: 2000,
      };

      store.upsertCustomFolder(folderB);
      store.upsertCustomFolder(folderA);

      // Verify sorted by name ASC
      const folders = store.getAllCustomFolders();
      assert.equal(folders.length, 2);
      assert.equal(folders[0].id, 'folder-a');
      assert.equal(folders[0].name, 'Alpha Hardware');
      assert.equal(folders[1].id, 'folder-b');
      assert.equal(folders[1].name, 'Zeta Cold Storage');

      // Update folder name
      store.upsertCustomFolder({
        id: 'folder-b',
        name: 'Beta Lab Hardware',
        createdAt: 1000,
      });
      const updatedFolders = store.getAllCustomFolders();
      assert.equal(updatedFolders.length, 2);
      assert.equal(updatedFolders[1].name, 'Beta Lab Hardware');

      // Delete folder
      const deleted = store.deleteCustomFolder('folder-a');
      assert.equal(deleted, true);
      const remaining = store.getAllCustomFolders();
      assert.equal(remaining.length, 1);
      assert.equal(remaining[0].id, 'folder-b');

      // Deleting non-existent folder returns false
      const nonExistentDeleted = store.deleteCustomFolder('folder-does-not-exist');
      assert.equal(nonExistentDeleted, false);
    });

    it('unsets folder attribute from assigned hosts when a custom folder is deleted', () => {
      const folder: CustomFolder = {
        id: 'folder-cold-storage',
        name: 'Cold Storage',
        createdAt: 1000,
      };
      store.upsertCustomFolder(folder);

      const hostInFolder: NodeItem = {
        ...baseTestNode,
        id: 'node-archived-1',
        folder: 'folder-cold-storage',
      };
      const hostOther: NodeItem = {
        ...baseTestNode,
        id: 'node-active-2',
        folder: 'folder-other',
      };

      store.upsertHost(hostInFolder);
      store.upsertHost(hostOther);

      // Verify folder property is stored on host
      const fetchedBefore = store.getHostById('node-archived-1');
      assert.equal(fetchedBefore?.folder, 'folder-cold-storage');

      // Delete the folder
      const deleted = store.deleteCustomFolder('folder-cold-storage');
      assert.equal(deleted, true);

      // Verify folder is unset on node-archived-1
      const fetchedAfter = store.getHostById('node-archived-1');
      assert.equal(fetchedAfter?.folder, undefined);

      // Verify other host remains untouched
      const fetchedOther = store.getHostById('node-active-2');
      assert.equal(fetchedOther?.folder, 'folder-other');
    });
  });

  describe('Tag Inventory Chunks SQLite Persistence', () => {
    it('creates, retrieves sorted by createdAt, updates, and deletes tag inventory chunks', () => {
      const chunk1: TagInventoryChunk = {
        id: 'chunk-tag:worker',
        tag: 'tag:worker',
        sshUser: 'apps',
        vaultSecretId: 'secret-vault-1',
        authMethod: 'vault-password',
        createdAt: 1000,
      };
      const chunk2: TagInventoryChunk = {
        id: 'chunk-tag:pve',
        tag: 'tag:pve',
        authMethod: 'tailscale-ssh',
        createdAt: 2000,
      };

      store.upsertTagChunk(chunk2);
      store.upsertTagChunk(chunk1);

      // Verify sorted by createdAt ASC
      const chunks = store.getAllTagChunks();
      assert.equal(chunks.length, 2);
      assert.equal(chunks[0].id, 'chunk-tag:worker');
      assert.equal(chunks[0].tag, 'tag:worker');
      assert.equal(chunks[0].sshUser, 'apps');
      assert.equal(chunks[0].vaultSecretId, 'secret-vault-1');
      assert.equal(chunks[0].authMethod, 'vault-password');
      assert.equal(chunks[0].createdAt, 1000);

      assert.equal(chunks[1].id, 'chunk-tag:pve');
      assert.equal(chunks[1].tag, 'tag:pve');
      assert.equal(chunks[1].sshUser, undefined);
      assert.equal(chunks[1].vaultSecretId, undefined);
      assert.equal(chunks[1].authMethod, 'tailscale-ssh');
      assert.equal(chunks[1].createdAt, 2000);

      // Update chunk
      store.upsertTagChunk({
        id: 'chunk-tag:worker',
        tag: 'tag:worker',
        sshUser: 'root',
        vaultSecretId: 'secret-vault-key-2',
        authMethod: 'vault-sshkey',
        createdAt: 1000,
      });
      const updatedChunks = store.getAllTagChunks();
      assert.equal(updatedChunks[0].sshUser, 'root');
      assert.equal(updatedChunks[0].vaultSecretId, 'secret-vault-key-2');
      assert.equal(updatedChunks[0].authMethod, 'vault-sshkey');

      // Delete chunk
      const deleted = store.deleteTagChunk('chunk-tag:worker');
      assert.equal(deleted, true);
      const remaining = store.getAllTagChunks();
      assert.equal(remaining.length, 1);
      assert.equal(remaining[0].id, 'chunk-tag:pve');

      // Deleting non-existent chunk returns false
      const nonExistentDeleted = store.deleteTagChunk('chunk-missing');
      assert.equal(nonExistentDeleted, false);
    });
  });

  describe('NodeItem Hierarchy & Protocol Extension Fields', () => {
    it('persists connectionProtocol, vaultSecretId, folder, isDynamicTagMember, and followedTag in SQLite', () => {
      const nodeWithHierarchy: NodeItem = {
        ...baseTestNode,
        id: 'node-win-hyperv',
        connectionProtocol: 'winrm',
        vaultSecretId: 'vault-creds-win',
        folder: 'folder-lab-win',
        isDynamicTagMember: true,
        followedTag: 'tag:windows',
      };

      store.upsertHost(nodeWithHierarchy);

      const retrieved = store.getHostById('node-win-hyperv');
      assert.ok(retrieved);
      assert.equal(retrieved?.connectionProtocol, 'winrm');
      assert.equal(retrieved?.vaultSecretId, 'vault-creds-win');
      assert.equal(retrieved?.folder, 'folder-lab-win');
      assert.equal(retrieved?.isDynamicTagMember, true);
      assert.equal(retrieved?.followedTag, 'tag:windows');

      const all = store.getAllHosts();
      const match = all.find(n => n.id === 'node-win-hyperv');
      assert.ok(match);
      assert.equal(match?.connectionProtocol, 'winrm');
      assert.equal(match?.vaultSecretId, 'vault-creds-win');
      assert.equal(match?.folder, 'folder-lab-win');
      assert.equal(match?.isDynamicTagMember, true);
      assert.equal(match?.followedTag, 'tag:windows');
    });
  });

  describe('Inventory REST Endpoints', () => {
    let server: http.Server;
    let baseUrl: string;
    let nodes: NodeItem[];
    const broadcastMessages: any[] = [];

    beforeEach(async () => {
      nodes = [{ ...baseTestNode, id: 'node-1', folder: 'folder-alpha' }];
      broadcastMessages.length = 0;

      const routes: Array<{ method: string; path: string; handler: Function }> = [];
      const app = {
        get: (p: string, h: Function) => routes.push({ method: 'GET', path: p, handler: h }),
        post: (p: string, h: Function) => routes.push({ method: 'POST', path: p, handler: h }),
        put: (p: string, h: Function) => routes.push({ method: 'PUT', path: p, handler: h }),
        delete: (p: string, h: Function) => routes.push({ method: 'DELETE', path: p, handler: h }),
      };

      registerInventoryRoutes(app, {
        hostCache: store,
        broadcast: (msg) => broadcastMessages.push(msg),
        getNodes: () => nodes,
        setNodes: (newNodes) => { nodes = newNodes; },
      });

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

      await new Promise<void>((resolve) => {
        server.listen(0, '127.0.0.1', () => {
          const addr = server.address();
          if (typeof addr === 'object' && addr) {
            baseUrl = `http://127.0.0.1:${addr.port}`;
          }
          resolve();
        });
      });
    });

    afterEach(async () => {
      if (server) {
        await new Promise<void>((resolve) => server.close(() => resolve()));
      }
    });

    it('GET /api/inventory/chunks returns initial empty chunks array', async () => {
      const res = await fetch(`${baseUrl}/api/inventory/chunks`);
      assert.equal(res.status, 200);
      const data = await res.json();
      assert.equal(data.success, true);
      assert.deepEqual(data.chunks, []);
    });

    it('POST /api/inventory/chunks validates input and upserts chunk with broadcast', async () => {
      // Rejects missing tag or authMethod
      const badRes = await fetch(`${baseUrl}/api/inventory/chunks`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ tag: 'tag:worker' }),
      });
      assert.equal(badRes.status, 400);

      // Creates chunk
      const res = await fetch(`${baseUrl}/api/inventory/chunks`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          tag: 'tag:worker',
          authMethod: 'vault-password',
          sshUser: 'core',
          vaultSecretId: 'secret-123',
        }),
      });
      assert.equal(res.status, 200);
      const data = await res.json();
      assert.equal(data.success, true);
      assert.equal(data.chunk.tag, 'tag:worker');
      assert.equal(data.chunk.id, 'chunk-tag:worker');

      // Verify broadcast
      assert.ok(broadcastMessages.some(m => m.type === 'chunks_updated'));

      // Verify chunks listed via GET
      const listRes = await fetch(`${baseUrl}/api/inventory/chunks`);
      const listData = await listRes.json();
      assert.equal(listData.chunks.length, 1);
      assert.equal(listData.chunks[0].tag, 'tag:worker');
    });

    it('DELETE /api/inventory/chunks/:id deletes chunk and broadcasts update', async () => {
      store.upsertTagChunk({
        id: 'chunk-tag:pve',
        tag: 'tag:pve',
        authMethod: 'tailscale-ssh',
        createdAt: 1000,
      });

      const delRes = await fetch(`${baseUrl}/api/inventory/chunks/chunk-tag:pve`, {
        method: 'DELETE',
      });
      assert.equal(delRes.status, 200);
      const delData = await delRes.json();
      assert.equal(delData.success, true);
      assert.equal(delData.deleted, true);

      assert.ok(broadcastMessages.some(m => m.type === 'chunks_updated'));

      const listRes = await fetch(`${baseUrl}/api/inventory/chunks`);
      const listData = await listRes.json();
      assert.equal(listData.chunks.length, 0);
    });

    it('GET /api/inventory/folders and POST /api/inventory/folders manage custom folders', async () => {
      // Empty list
      const emptyRes = await fetch(`${baseUrl}/api/inventory/folders`);
      const emptyData = await emptyRes.json();
      assert.equal(emptyData.success, true);
      assert.deepEqual(emptyData.folders, []);

      // Validate name required
      const badRes = await fetch(`${baseUrl}/api/inventory/folders`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: '' }),
      });
      assert.equal(badRes.status, 400);

      // Create folder
      const createRes = await fetch(`${baseUrl}/api/inventory/folders`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: 'Lab Hardware' }),
      });
      assert.equal(createRes.status, 200);
      const createData = await createRes.json();
      assert.equal(createData.success, true);
      assert.equal(createData.folder.name, 'Lab Hardware');
      assert.ok(createData.folder.id.startsWith('folder-'));

      assert.ok(broadcastMessages.some(m => m.type === 'folders_updated'));
    });

    it('DELETE /api/inventory/folders/:id deletes folder, unsets folder on nodes, and broadcasts update', async () => {
      const folder: CustomFolder = {
        id: 'folder-alpha',
        name: 'Alpha Folder',
        createdAt: 1000,
      };
      store.upsertCustomFolder(folder);
      store.upsertHost(nodes[0]);

      assert.equal(nodes[0].folder, 'folder-alpha');

      const delRes = await fetch(`${baseUrl}/api/inventory/folders/folder-alpha`, {
        method: 'DELETE',
      });
      assert.equal(delRes.status, 200);
      const delData = await delRes.json();
      assert.equal(delData.success, true);
      assert.equal(delData.deleted, true);

      // In-memory node should have folder unset
      assert.equal(nodes[0].folder, undefined);

      // Broadcast sent
      assert.ok(broadcastMessages.some(m => m.type === 'folders_updated'));
    });
  });
});
