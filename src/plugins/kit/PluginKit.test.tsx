import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import { renderToString } from 'react-dom/server';
import {
  PluginResourceTable,
  PluginActionForm,
  PluginStateView,
} from './index.ts';

test('PluginResourceTable renders table with columns and rows', () => {
  interface Item {
    id: string;
    name: string;
    status: string;
  }
  const items: Item[] = [
    { id: '1', name: 'tank0', status: 'ONLINE' },
    { id: '2', name: 'tank1', status: 'DEGRADED' },
  ];

  const html = renderToString(
    React.createElement(PluginResourceTable<Item>, {
      columns: [
        { key: 'name', header: 'Pool Name' },
        { key: 'status', header: 'Health Status' },
      ],
      data: items,
      keyExtractor: item => item.id,
      actions: [
        {
          label: 'Scrub',
          icon: 'cleaning_services',
          onClick: () => {},
        },
      ],
    })
  );

  assert.ok(html.includes('Pool Name'));
  assert.ok(html.includes('tank0'));
  assert.ok(html.includes('tank1'));
  assert.ok(html.includes('Scrub'));
  assert.ok(html.includes('cleaning_services'));
});

test('PluginResourceTable renders empty message when data is empty', () => {
  const html = renderToString(
    React.createElement(PluginResourceTable<any>, {
      columns: [{ key: 'name', header: 'Name' }],
      data: [],
      keyExtractor: item => item.id,
      emptyMessage: 'No storage pools found on host.',
    })
  );

  assert.ok(html.includes('No storage pools found on host.'));
});

test('PluginActionForm renders schema inputs including secret mask and options', () => {
  const html = renderToString(
    React.createElement(PluginActionForm, {
      action: {
        name: 'create_pool',
        label: 'Create ZFS Pool',
        description: 'Create a new striped or mirrored pool',
        options: [
          { name: 'pool_name', type: 'string', required: true, description: 'Name of pool' },
          { name: 'raid_type', type: 'select', enum: ['single', 'mirror', 'raidz1'] },
          { name: 'force', type: 'bool', description: 'Force creation' },
          { name: 'passphrase', type: 'string', secret: true, description: 'Encryption passphrase' },
        ],
      },
      onSubmit: () => {},
    })
  );

  assert.ok(html.includes('Create ZFS Pool'));
  assert.ok(html.includes('pool_name'));
  assert.ok(html.includes('mirror'));
  assert.ok(html.includes('SECRET / MASKED'));
  assert.ok(html.includes('type="password"'));
});

test('PluginStateView renders loading, offline, error, and ready states', () => {
  const loadingHtml = renderToString(
    React.createElement(PluginStateView, {
      state: 'loading',
      title: 'Probing ZFS Pools',
    })
  );
  assert.ok(loadingHtml.includes('Probing ZFS Pools'));

  const offlineHtml = renderToString(
    React.createElement(PluginStateView, {
      state: 'offline',
      title: 'Host Unreachable',
      error: 'SSH timeout on 100.64.0.1:22',
    })
  );
  assert.ok(offlineHtml.includes('Host Unreachable'));
  assert.ok(offlineHtml.includes('SSH timeout on 100.64.0.1:22'));

  const errorHtml = renderToString(
    React.createElement(PluginStateView, {
      state: 'error',
      title: 'Command Failed',
      error: 'exit status 1: zpool not found',
    })
  );
  assert.ok(errorHtml.includes('Command Failed'));
  assert.ok(errorHtml.includes('zpool not found'));

  const readyHtml = renderToString(
    React.createElement(
      PluginStateView,
      { state: 'ready' },
      React.createElement('span', null, 'Active Plugin Content')
    )
  );
  assert.ok(readyHtml.includes('Active Plugin Content'));
});
