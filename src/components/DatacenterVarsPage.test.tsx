import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import { renderToString } from 'react-dom/server';
import { DatacenterVarsPage, isValidVarFile } from './DatacenterVarsPage.tsx';

test('DatacenterVarsPage renders an honest loading state with no fabricated vars', () => {
  const html = renderToString(<DatacenterVarsPage />);

  assert.ok(html.includes('datacenter-vars-page'), 'page root is rendered');
  assert.ok(html.includes('Datacenter Vars'));
  assert.ok(html.includes('source: /api/group-vars (SQLite store)'));
  // Before the store responds there is nothing to list — no seeded defaults.
  assert.ok(html.includes('Reading vars store'), 'file list shows the loading state');
  assert.ok(!html.includes('group_vars/all.yml'), 'no hardcoded var files are rendered');
  assert.ok(!html.includes('datacenter_name'), 'no hardcoded var values are rendered');
});

test('isValidVarFile accepts relative ansible vars paths and rejects escapes', () => {
  assert.equal(isValidVarFile('group_vars/all.yml'), true);
  assert.equal(isValidVarFile('group_vars/tag-host.yaml'), true);
  assert.equal(isValidVarFile('host_vars/dev-pc.yml'), true);

  assert.equal(isValidVarFile(''), false);
  assert.equal(isValidVarFile('group_vars/all.yml.bak'), false);
  assert.equal(isValidVarFile('/etc/group_vars/all.yml'), false);
  assert.equal(isValidVarFile('../group_vars/all.yml'), false);
  assert.equal(isValidVarFile('group_vars\\all.yml'), false);
  assert.equal(isValidVarFile('group_vars/all.txt'), false);
});
