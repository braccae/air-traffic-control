import test from 'node:test';
import assert from 'node:assert/strict';
import {
  settingsSectionId,
  isSettingsSection,
  fetchGlobalSettingsPages,
  saveGlobalSettingsPage,
} from './globalSettingsManager.ts';

test('settingsSectionId maps a page id to a deterministic sidebar section id', () => {
  assert.equal(settingsSectionId('backup/restic'), 'settings-backup-restic');
  assert.equal(settingsSectionId('demo/alpha'), 'settings-demo-alpha');
  assert.equal(settingsSectionId('single'), 'settings-single');
});

test('isSettingsSection recognises module settings sections', () => {
  assert.equal(isSettingsSection('settings-backup-restic'), true);
  assert.equal(isSettingsSection('overview'), false);
  assert.equal(isSettingsSection('playbooks'), false);
});

test('fetchGlobalSettingsPages returns the parsed page list', async () => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = (async () =>
    ({
      ok: true,
      status: 200,
      json: async () => [
        {
          id: 'backup/restic',
          title: 'Restic Backup Defaults',
          icon: 'cloud_sync',
          description: 'defaults',
          order: 20,
          fields: [{ name: 'repoPath', label: 'Default Repository', type: 'string' }],
          values: { repoPath: '/var/backups/restic' },
        },
      ],
    }) as Response) as typeof fetch;

  try {
    const pages = await fetchGlobalSettingsPages();
    assert.equal(pages.length, 1);
    assert.equal(pages[0].id, 'backup/restic');
    assert.equal(pages[0].values.repoPath, '/var/backups/restic');
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test('fetchGlobalSettingsPages tolerates a non-array payload', async () => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = (async () =>
    ({ ok: true, status: 200, json: async () => ({}) }) as Response) as typeof fetch;
  try {
    const pages = await fetchGlobalSettingsPages();
    assert.deepEqual(pages, []);
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test('saveGlobalSettingsPage PUTs the values and returns the page', async () => {
  const originalFetch = globalThis.fetch;
  let capturedUrl = '';
  let capturedBody = '';
  globalThis.fetch = (async (url: string, init?: RequestInit) => {
    capturedUrl = String(url);
    capturedBody = String(init?.body ?? '');
    return {
      ok: true,
      status: 200,
      json: async () => ({
        id: 'backup/restic',
        title: 'Restic Backup Defaults',
        icon: 'cloud_sync',
        description: 'defaults',
        order: 20,
        fields: [],
        values: { repoPath: '/srv/restic' },
      }),
    } as Response;
  }) as typeof fetch;

  try {
    const page = await saveGlobalSettingsPage('backup/restic', { repoPath: '/srv/restic' });
    assert.equal(capturedUrl, '/api/settings/pages/backup/restic');
    assert.deepEqual(JSON.parse(capturedBody), { values: { repoPath: '/srv/restic' } });
    assert.equal(page.values.repoPath, '/srv/restic');
  } finally {
    globalThis.fetch = originalFetch;
  }
});
