import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import { renderToString } from 'react-dom/server';
import { DatacenterSettingsView } from './DatacenterSettingsView.tsx';
import { RcloneRemotesView } from './RcloneRemotesView.tsx';
import { getDatacenterSettingPages } from '../utils/navigationViewModel.ts';

test('each catalog setting renders its own page with a stable deep-link URL', () => {
  const pages = getDatacenterSettingPages();
  assert.ok(pages.length >= 6);

  for (const page of pages) {
    const html = renderToString(React.createElement(DatacenterSettingsView, { page: page.id }));
    assert.ok(html.includes(`data-setting-id="${page.id}"`), `${page.id} page must render`);
    assert.ok(html.includes(`#/datacenter/settings/${page.category}/${page.slug}`), `${page.id} must show its deep link`);
    assert.ok(html.includes(page.title), `${page.id} must render its title`);
  }
});

test('ansible setting pages render only their own field, not the whole ansible.cfg form', () => {
  const forksHtml = renderToString(
    React.createElement(DatacenterSettingsView, { page: 'ansible-forks' })
  );
  assert.ok(forksHtml.includes('forks'));
  assert.ok(!forksHtml.includes('become_method'), 'forks page must not show unrelated settings');
  assert.ok(!forksHtml.includes('stdout_callback'), 'forks page must not show unrelated settings');

  const stdoutHtml = renderToString(
    React.createElement(DatacenterSettingsView, { page: 'ansible-stdout-callback' })
  );
  assert.ok(stdoutHtml.includes('stdout_callback'));
  assert.ok(!stdoutHtml.includes('become_method'));
  assert.ok(!stdoutHtml.includes('pipelining'));
});

test('SSH transport page groups host_key_checking and pipelining only', () => {
  const html = renderToString(
    React.createElement(DatacenterSettingsView, { page: 'ansible-ssh-transport' })
  );
  assert.ok(html.includes('host_key_checking'));
  assert.ok(html.includes('pipelining'));
  assert.ok(!html.includes('stdout_callback'));
});

test('quick commands page keeps the existing settings editor and test id', () => {
  const html = renderToString(
    React.createElement(DatacenterSettingsView, { page: 'quick-commands' })
  );
  assert.ok(html.includes('data-testid="datacenter-quick-commands-settings"'));
  assert.ok(html.includes('Add Command'));
  assert.ok(html.includes('Reset to Defaults'));
});

test('rclone-remotes datacenter settings page renders guided setup and vault store info', () => {
  const html = renderToString(
    React.createElement(DatacenterSettingsView, { page: 'rclone-remotes' })
  );
  assert.ok(html.includes('data-setting-id="rclone-remotes"'));
  assert.ok(html.includes('#/datacenter/settings/storage/rclone-remotes'));
  assert.ok(html.includes('data-testid="datacenter-rclone-remotes-settings"'));
  assert.ok(html.includes('data-testid="rclone-guided-setup-card"'));
  assert.ok(html.includes('vault_variables'));
});

test('RcloneRemotesView in client mode hides guided setup card and shows link to datacenter settings', () => {
  const html = renderToString(
    React.createElement(RcloneRemotesView, {
      mode: 'client',
      node: { id: 'node-1', name: 'worker-01', ip: '10.0.0.10', status: 'online' } as any,
    })
  );
  assert.ok(html.includes('data-testid="client-rclone-mounts-view"'));
  assert.ok(!html.includes('data-testid="rclone-guided-setup-card"'));
  assert.ok(html.includes('data-testid="rclone-configured-remotes-card"'));
  assert.ok(html.includes('data-testid="rclone-client-mounts-card"'));
  assert.ok(html.includes('Open in Datacenter Settings →'));
  assert.ok(html.includes('#/datacenter/settings/storage/rclone-remotes'));
});

test('ansible-collections datacenter settings page renders collection manager with install and remove controls', () => {
  const html = renderToString(
    React.createElement(DatacenterSettingsView, { page: 'ansible-collections' })
  );
  assert.ok(html.includes('data-setting-id="ansible-collections"'));
  assert.ok(html.includes('#/datacenter/settings/automation/ansible-collections'));
  assert.ok(html.includes('data-testid="datacenter-ansible-collections-settings"'));
  assert.ok(html.includes('community.general.run0'));
  assert.ok(html.includes('Install Collection'));
});
