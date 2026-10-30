import { test, expect, type APIRequestContext, type Page } from '@playwright/test';

/**
 * Add Node / Edit Node modal behaviour.
 *
 * The modals are driven by real daemon data (Tailscale peers and vault
 * variables), so these tests open the real UI served by Vite and assert the
 * onboarding contract:
 *   - no tab forces the operator to type a Tailscale tag,
 *   - Follow Tag asks for a plain tag name (`pve`, not `tag:pve`),
 *   - SSH credentials are referenced as named vault variables,
 *   - WinRM cannot pretend to onboard a host.
 */

const API_BASE = process.env.ATC_API_URL || 'http://host.containers.internal:18090';

async function openAddNode(page: Page) {
  await page.goto('/');
  await page.waitForSelector('#root');
  await page.locator('button', { hasText: '+ Add Node' }).first().click();
  const modal = page
    .locator('div.fixed.inset-0')
    .filter({ hasText: 'Add Node & Inventory Onboarding' })
    .first();
  await expect(modal).toBeVisible();
  return modal;
}

test.describe('Add Node modal', () => {
  test('opens with all four onboarding pathways and no SSH-key page', async ({ page }) => {
    const modal = await openAddNode(page);

    await expect(modal.getByRole('button', { name: 'Tailscale Mesh' })).toBeVisible();
    await expect(modal.getByRole('button', { name: 'Manual SSH' })).toBeVisible();
    await expect(modal.getByRole('button', { name: 'WinRM' })).toBeVisible();
    await expect(modal.getByRole('button', { name: 'Follow Tag' })).toBeVisible();

    // The separate SSH key store was retired in favour of vault variables.
    await expect(page.getByText('SSH Key Management')).toHaveCount(0);
    await expect(page.getByText('ssh-keys')).toHaveCount(0);
  });

  test('Tailscale onboarding does not require typing a tag', async ({ page }) => {
    const modal = await openAddNode(page);
    const form = modal.locator('form');

    // The tag control has a sane default and is never required.
    await expect(form.locator('select[required]')).toHaveCount(0);
    await expect(form.locator('input[required]')).toHaveCount(1);

    await form.getByPlaceholder('e.g. likes-it-cold').fill('e2e-tailscale-name');
    await expect(form.locator('button[type="submit"]')).toBeEnabled();
    await expect(form.locator('button[type="submit"]')).toContainText('Add Node');
  });

  test('Manual SSH exposes named vault secrets and manual name/value entry', async ({
    page,
  }) => {
    const modal = await openAddNode(page);
    await modal.getByRole('button', { name: 'Manual SSH' }).click();

    await expect(modal.getByText('SSH Vault Secrets')).toBeVisible();
    await expect(modal.getByText('Use ssh-agent forwarding')).toBeVisible();
    await expect(modal.getByText('Existing secret', { exact: true })).toBeVisible();
    await expect(modal.getByText('New secret', { exact: true })).toBeVisible();

    await modal.getByText('New secret', { exact: true }).click();
    await expect(modal.getByText('SECRET NAME (E.G. ssh_key)')).toBeVisible();
    await expect(modal.getByPlaceholder('ssh_key')).toBeVisible();

    // A secret may be a password or a private key; the name is operator-chosen.
    await expect(modal.getByText('Password', { exact: true })).toBeVisible();
    await expect(modal.getByText('Private SSH Key', { exact: true })).toBeVisible();
    await modal.getByText('Private SSH Key', { exact: true }).click();
    await expect(modal.getByPlaceholder('-----BEGIN OPENSSH PRIVATE KEY-----...')).toBeVisible();

    await expect(modal.getByRole('button', { name: /Test Connection/ })).toBeVisible();
  });

  test('Follow Tag asks for a plain tag name and adds the tag: prefix itself', async ({
    page,
  }) => {
    const modal = await openAddNode(page);
    await modal.getByRole('button', { name: 'Follow Tag' }).click();

    const tagInput = modal.getByPlaceholder('pve');
    await expect(tagInput).toBeVisible();
    await tagInput.fill('pve');

    // The operator types `pve`; the UI shows the prefix separately.
    await expect(tagInput).toHaveValue('pve');
    await expect(modal.getByText('tag:', { exact: true }).first()).toBeVisible();
    await expect(modal.getByText(/prefix is added automatically/)).toBeVisible();

    const submit = modal.getByRole('button', { name: /Follow Tag & Sync Nodes/ });
    await expect(submit).toBeEnabled();

    await tagInput.fill('host');
    await expect(tagInput).toHaveValue('host');
  });

  test('WinRM onboarding is visibly disabled and cannot submit', async ({ page }) => {
    const modal = await openAddNode(page);
    await modal.getByRole('button', { name: 'WinRM' }).click();

    const form = modal.locator('form');
    await expect(modal.getByText('WinRM onboarding is not implemented')).toBeVisible();
    await expect(modal.getByText(/Add Node is disabled for this tab/)).toBeVisible();

    await form.getByPlaceholder('e.g. win-server-01').fill('e2e-winrm-host');
    const submit = form.locator('button[type="submit"]');
    await expect(submit).toBeDisabled();
    await expect(submit).toHaveAttribute('title', 'WinRM onboarding is not implemented');

    const nodes = await (await page.request.get(`${API_BASE}/api/nodes`)).json();
    expect(nodes.some((n: { name: string }) => n.name === 'e2e-winrm-host')).toBe(false);
  });
});

test.describe('Edit Node modal', () => {
  test('lists existing vault variables and preselects the node reference', async ({
    page,
    request,
  }) => {
    const runId = `${Date.now()}${Math.random().toString(36).slice(2, 6)}`;
    const secretName = `e2e_edit_${runId}`;
    const nodeId = `e2e-edit-${runId}`;
    const nodeName = `e2e-edit-${runId}`;

    const createdSecret = await request.post(`${API_BASE}/api/vault/variables`, {
      data: { name: secretName, value: 'e2e-edit-secret-value' },
    });
    expect(createdSecret.status()).toBe(201);

    const createdNode = await request.post(`${API_BASE}/api/nodes`, {
      data: {
        id: nodeId,
        name: nodeName,
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
    expect(createdNode.status()).toBe(200);

    try {
      await page.goto('/');
      await page.waitForSelector('#root');

      // The header search button opens the real search modal; selecting the
      // node mounts the cockpit sidebar, which exposes the Edit Specs button.
      await page.locator('button', { hasText: 'Search hosts, playbooks, vars...' }).first().click();
      const search = page.getByPlaceholder('Search hosts, playbooks, vars, containers, IP...');
      await expect(search).toBeVisible();
      await search.fill(nodeName);

      // Scope to the search modal: the inventory sidebar also renders the node
      // name and sits behind the modal overlay.
      const searchModal = page
        .locator('div.fixed.inset-0')
        .filter({ has: search })
        .first();
      await searchModal.getByText(nodeName, { exact: true }).first().click();

      const editButton = page.locator('button', { hasText: 'Edit Specs' }).first();
      await expect(editButton).toBeVisible();
      await editButton.click();

      await expect(page.getByText(/Edit Node & Connection Settings/)).toBeVisible();
      await expect(page.getByText('VAULT SECRET (SSH KEY)')).toBeVisible();

      const option = page.locator(`option[value="${secretName}"]`);
      await expect(option).toHaveCount(1);

      const select = page.locator('select').filter({ has: option });
      await expect(select).toHaveValue(secretName);
      await expect(page.getByText(/References vault_variables\.name/)).toBeVisible();
    } finally {
      // Use raw fetch for cleanup so a timed-out page cannot strand rows in the
      // developer's real database.
      await fetch(`${API_BASE}/api/nodes/${encodeURIComponent(nodeId)}`, {
        method: 'DELETE',
      }).catch(() => undefined);
      await fetch(`${API_BASE}/api/vault/variables/${encodeURIComponent(secretName)}`, {
        method: 'DELETE',
      }).catch(() => undefined);
    }
  });
});
