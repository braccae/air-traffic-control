import { test, expect, type APIRequestContext } from '@playwright/test';

/**
 * Vault credentials + SSH onboarding API contract.
 *
 * These tests run against the live Go daemon and its real SQLite database.
 * Everything they create is namespaced with `e2e_` / `e2e-` and deleted again,
 * so the developer's real inventory is never modified.
 */

const API_BASE = process.env.ATC_API_URL || 'http://host.containers.internal:18090';

const RUN_ID = `${Date.now()}_${Math.random().toString(36).slice(2, 8)}`;
const uniqueName = (label: string) => `e2e_${label}_${RUN_ID}`;
const uniqueID = (label: string) => `e2e-${label}-${RUN_ID.replace(/_/g, '-')}`;

const createdSecrets = new Set<string>();

async function createSecret(
  request: APIRequestContext,
  name: string,
  value = 'e2e-not-a-real-credential',
) {
  const res = await request.post(`${API_BASE}/api/vault/variables`, {
    data: { name, value },
  });
  if (res.status() === 201) {
    createdSecrets.add(name);
  }
  return res;
}

async function deleteSecret(request: APIRequestContext, name: string) {
  createdSecrets.delete(name);
  await request.delete(`${API_BASE}/api/vault/variables/${encodeURIComponent(name)}`);
}

test.afterAll(async ({ request }) => {
  for (const name of createdSecrets) {
    await request
      .delete(`${API_BASE}/api/vault/variables/${encodeURIComponent(name)}`)
      .catch(() => undefined);
  }
});

test.describe('Vault variable CRUD', () => {
  test('creates a secret, masks it in list responses, and deletes it', async ({ request }) => {
    const name = uniqueName('secret_crud');
    const secretValue = 'hunter2-e2e-plaintext';

    const created = await createSecret(request, name, secretValue);
    expect(created.status()).toBe(201);
    const createdBody = await created.json();
    expect(createdBody.success).toBe(true);
    expect(createdBody.variable.name).toBe(name);

    const list = await request.get(`${API_BASE}/api/vault/variables`);
    expect(list.status()).toBe(200);
    const listBody = await list.json();
    expect(listBody.success).toBe(true);
    expect(Array.isArray(listBody.variables)).toBe(true);
    const entry = listBody.variables.find((v: { name: string }) => v.name === name);
    expect(entry).toBeTruthy();
    expect(entry.maskedValue).toBeTruthy();
    expect(JSON.stringify(listBody)).not.toContain(secretValue);

    const removed = await request.delete(
      `${API_BASE}/api/vault/variables/${encodeURIComponent(name)}`,
    );
    createdSecrets.delete(name);
    expect(removed.status()).toBe(200);

    const after = await request.get(`${API_BASE}/api/vault/variables`);
    const afterBody = await after.json();
    expect(afterBody.variables.some((v: { name: string }) => v.name === name)).toBe(false);
  });

  test('rejects duplicate variable names with 409', async ({ request }) => {
    const name = uniqueName('duplicate');
    expect((await createSecret(request, name)).status()).toBe(201);

    const duplicate = await request.post(`${API_BASE}/api/vault/variables`, {
      data: { name, value: 'second-value' },
    });
    expect(duplicate.status()).toBe(409);
    const body = await duplicate.json();
    expect(body.success).toBe(false);
    expect(body.error).toContain(name);
  });

  test('rejects invalid names and empty values with 400', async ({ request }) => {
    const invalidName = await request.post(`${API_BASE}/api/vault/variables`, {
      data: { name: `e2e-bad-name-${RUN_ID}`, value: 'x' },
    });
    expect(invalidName.status()).toBe(400);
    expect((await invalidName.json()).success).toBe(false);

    const emptyValue = await request.post(`${API_BASE}/api/vault/variables`, {
      data: { name: uniqueName('empty_value'), value: '' },
    });
    expect(emptyValue.status()).toBe(400);
    expect((await emptyValue.json()).success).toBe(false);
  });

  test('exports a vault password source so onboarding can encrypt unattended', async ({
    request,
  }) => {
    const res = await request.get(`${API_BASE}/api/vault/status`);
    expect(res.status()).toBe(200);
    const body = await res.json();
    expect(body.success).toBe(true);
    expect(body.hasPasswordSource).toBe(true);
  });

  test('the retired /api/ssh-keys endpoints are gone', async ({ request }) => {
    expect((await request.get(`${API_BASE}/api/ssh-keys`)).status()).toBe(404);
    expect((await request.get(`${API_BASE}/api/ssh-keys/anything`)).status()).toBe(404);
    expect(
      (
        await request.post(`${API_BASE}/api/ssh-keys`, {
          data: { name: 'nope', privateKey: 'nope' },
        })
      ).status(),
    ).toBe(404);
  });
});

test.describe('POST /api/ssh/test', () => {
  test('returns 400 when host is missing', async ({ request }) => {
    const res = await request.post(`${API_BASE}/api/ssh/test`, {
      data: { sshUser: 'core' },
    });
    expect(res.status()).toBe(400);
    const body = await res.json();
    expect(body.success).toBe(false);
    expect(body.error).toContain('host');
  });

  test('names an unresolved vault secret instead of silently failing', async ({ request }) => {
    const missing = uniqueName('does_not_exist');
    const res = await request.post(`${API_BASE}/api/ssh/test`, {
      data: {
        host: '127.0.0.1',
        port: 22,
        sshUser: 'core',
        authMethod: 'vault-sshkey',
        vaultSecretId: missing,
      },
    });
    expect(res.status()).toBe(502);
    const body = await res.json();
    expect(body.success).toBe(false);
    expect(body.vaultSecretId).toBe(missing);
    expect(body.error).toContain(missing);
    expect(body.error).toContain('SSH Vault Secrets');
  });

  test('reports the real transport error for an unreachable port', async ({ request }) => {
    const name = uniqueName('unreachable');
    expect((await createSecret(request, name, 'not-a-key-but-a-password')).status()).toBe(201);

    const res = await request.post(`${API_BASE}/api/ssh/test`, {
      data: {
        host: '127.0.0.1',
        port: 1,
        sshUser: 'core',
        authMethod: 'vault-sshkey',
        vaultSecretId: name,
        timeoutMs: 3000,
      },
    });
    expect(res.status()).toBe(502);
    const body = await res.json();
    expect(body.success).toBe(false);
    expect(body.error).toBeTruthy();
    // No synthetic success: the response identifies the host it actually tried.
    expect(body.host).toBe('127.0.0.1');
    expect(body.port).toBe(1);
  });
});

test.describe('Node onboarding references', () => {
  test('rejects a node whose vaultSecretId does not exist (422) and writes nothing', async ({
    request,
  }) => {
    const missing = uniqueName('missing_node_ref');
    const id = uniqueID('node-missing-ref');

    const res = await request.post(`${API_BASE}/api/nodes`, {
      data: {
        id,
        name: 'e2e-missing-ref',
        ip: '127.0.0.1',
        sshUser: 'core',
        authMethod: 'vault-sshkey',
        vaultSecretId: missing,
      },
    });
    expect(res.status()).toBe(422);
    const body = await res.json();
    expect(body.success).toBe(false);
    expect(body.error).toContain(missing);

    const nodes = await (await request.get(`${API_BASE}/api/nodes`)).json();
    expect(nodes.some((n: { id: string }) => n.id === id)).toBe(false);
  });

  test('accepts a node that references a real vault secret, then deletes it', async ({
    request,
  }) => {
    const secretName = uniqueName('node_ref_ok');
    const id = uniqueID('node-ref-ok');
    expect((await createSecret(request, secretName)).status()).toBe(201);

    try {
      const res = await request.post(`${API_BASE}/api/nodes`, {
        data: {
          id,
          name: 'e2e-ref-ok',
          ip: '127.0.0.1',
          port: 22,
          sshUser: 'core',
          group: 'tag:host',
          datacenter: 'e2e',
          status: 'unmonitored',
          authMethod: 'vault-sshkey',
          vaultSecretId: secretName,
          connectionProtocol: 'ssh',
        },
      });
      expect(res.status()).toBe(200);
      const body = await res.json();
      expect(body.success).toBe(true);
      expect(body.node.vaultSecretId).toBe(secretName);
      expect(body.node.status).toBe('unmonitored');

      const nodes = await (await request.get(`${API_BASE}/api/nodes`)).json();
      const stored = nodes.find((n: { id: string }) => n.id === id);
      expect(stored).toBeTruthy();
      expect(stored.vaultSecretId).toBe(secretName);
    } finally {
      await request.delete(`${API_BASE}/api/nodes/${encodeURIComponent(id)}`);
      await deleteSecret(request, secretName);
    }
  });

  test('rejects an update pointing at a missing vault secret (422)', async ({ request }) => {
    const secretName = uniqueName('node_update_ok');
    const id = uniqueID('node-update');
    expect((await createSecret(request, secretName)).status()).toBe(201);

    try {
      const created = await request.post(`${API_BASE}/api/nodes`, {
        data: {
          id,
          name: 'e2e-update',
          ip: '127.0.0.1',
          sshUser: 'core',
          authMethod: 'vault-sshkey',
          vaultSecretId: secretName,
        },
      });
      expect(created.status()).toBe(200);

      const missing = uniqueName('missing_update_ref');
      const update = await request.put(`${API_BASE}/api/nodes/${encodeURIComponent(id)}`, {
        data: { vaultSecretId: missing },
      });
      expect(update.status()).toBe(422);
      expect((await update.json()).error).toContain(missing);

      const nodes = await (await request.get(`${API_BASE}/api/nodes`)).json();
      const stored = nodes.find((n: { id: string }) => n.id === id);
      expect(stored.vaultSecretId).toBe(secretName);
    } finally {
      await request.delete(`${API_BASE}/api/nodes/${encodeURIComponent(id)}`);
      await deleteSecret(request, secretName);
    }
  });
});

test.describe('Tag inventory chunks', () => {
  test('lists chunks with a success envelope', async ({ request }) => {
    const res = await request.get(`${API_BASE}/api/inventory/chunks`);
    expect(res.status()).toBe(200);
    const body = await res.json();
    expect(body.success).toBe(true);
    expect(Array.isArray(body.chunks)).toBe(true);
  });

  test('rejects a missing vault secret (422) and an unknown auth method (400)', async ({
    request,
  }) => {
    const id = uniqueID('chunk-bad');
    const missing = uniqueName('missing_chunk_ref');

    const badRef = await request.post(`${API_BASE}/api/inventory/chunks`, {
      data: {
        id,
        tag: `e2e-${RUN_ID}`,
        sshUser: 'root',
        authMethod: 'vault-sshkey',
        vaultSecretId: missing,
      },
    });
    expect(badRef.status()).toBe(422);
    expect((await badRef.json()).error).toContain(missing);

    const badAuth = await request.post(`${API_BASE}/api/inventory/chunks`, {
      data: {
        id,
        tag: `e2e-${RUN_ID}`,
        sshUser: 'root',
        authMethod: 'not-a-real-method',
      },
    });
    expect(badAuth.status()).toBe(400);
  });

  test('normalises a plain tag name to tag:<name> and cleans up', async ({ request }) => {
    const secretName = uniqueName('chunk_ref_ok');
    const id = uniqueID('chunk-ok');
    const plainTag = `e2e-pve-${RUN_ID}`.replace(/_/g, '-');
    expect((await createSecret(request, secretName)).status()).toBe(201);

    try {
      const res = await request.post(`${API_BASE}/api/inventory/chunks`, {
        data: {
          id,
          tag: plainTag,
          sshUser: 'root',
          authMethod: 'vault-sshkey',
          vaultSecretId: secretName,
        },
      });
      expect(res.status()).toBe(200);
      const body = await res.json();
      expect(body.success).toBe(true);
      expect(body.chunk.tag).toBe(`tag:${plainTag}`);
      expect(body.chunk.sshUser).toBe('root');
      expect(body.chunk.vaultSecretId).toBe(secretName);

      const list = await (await request.get(`${API_BASE}/api/inventory/chunks`)).json();
      const listed = list.chunks.find((c: { id: string }) => c.id === id);
      expect(listed).toBeTruthy();
      expect(listed.tag).toBe(`tag:${plainTag}`);
    } finally {
      await request.delete(`${API_BASE}/api/inventory/chunks/${encodeURIComponent(id)}`);
      await deleteSecret(request, secretName);
    }
  });
});
