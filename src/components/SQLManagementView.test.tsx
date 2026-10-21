import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import { renderToString } from 'react-dom/server';
import { SQLManagementView, parseTabularOutput } from './SQLManagementView.tsx';
import type { NodeItem } from '../types/index.ts';

const mockNode: NodeItem = {
  id: 'node-sql-test-1',
  name: 'Test Database Node',
  shortName: 'db-node-1',
  group: 'databases',
  datacenter: 'dc1',
  nodeType: 'linux',
  ip: '192.168.1.110',
  port: 22,
  tailscaleIp: '100.64.0.2',
  tailscaleHost: 'db-1.leo-banjo.ts.net',
  osDescription: 'Rocky Linux 9',
  status: 'online',
  uptime: '10 days',
  hasGlances: true,
  hasCockpit: false,
  hasPodman: false,
  hasRestic: false,
  specs: {
    cpu: 'AMD EPYC',
    vcpus: 16,
    ramGiB: 64,
    motherboard: 'Supermicro',
    bios: 'v2.0',
    ipmi: 'none',
    ecc: 'yes',
    secureBoot: 'disabled',
    iommu: 'enabled',
  },
};

test('SQLManagementView renders title and engine selector tabs', () => {
  const html = renderToString(
    React.createElement(SQLManagementView, {
      node: mockNode,
      pluginId: 'database/sql',
    })
  );

  assert.ok(html.includes('SQL Database Management'), 'Must render SQL Database Management title');
  assert.ok(html.includes('PostgreSQL'), 'Must render PostgreSQL tab');
  assert.ok(html.includes('MariaDB / MySQL'), 'Must render MariaDB / MySQL tab');
  assert.ok(html.includes('MSSQL'), 'Must render MSSQL tab');
});

test('SQLManagementView renders databases, users, and SQL Query Workbench', () => {
  const html = renderToString(
    React.createElement(SQLManagementView, {
      node: mockNode,
      pluginId: 'database/sql',
      initialData: {
        engines: [
          {
            engine: 'postgres',
            status: 'active',
            version: 'PostgreSQL 16.2',
            databases: ['postgres', 'analytics_db', 'app_prod'],
            users: [
              { username: 'postgres', roles: ['superuser', 'createdb'] },
              { username: 'app_user', roles: ['createdb'] },
            ],
            metrics: { active_connections: 8 },
          },
        ],
        count: 1,
      },
    })
  );

  assert.ok(html.includes('SQL Query Workbench'), 'Must render SQL Query Workbench');
  assert.ok(html.includes('Run SQL'), 'Must render Run SQL button');
  assert.ok(html.includes('analytics_db'), 'Must list discovered databases');
  assert.ok(html.includes('app_user'), 'Must list discovered users');
  assert.ok(html.includes('superuser'), 'Must display user roles');
});

test('parseTabularOutput parses MySQL ascii borders', () => {
  const mysqlOutput = `
+----+----------+-------------------+
| id | username | email             |
+----+----------+-------------------+
| 1  | alice    | alice@example.com |
| 2  | bob      | NULL              |
+----+----------+-------------------+
2 rows in set (0.00 sec)
`;
  const result = parseTabularOutput(mysqlOutput);
  assert.equal(result.isTabular, true);
  assert.deepEqual(result.columns, ['id', 'username', 'email']);
  assert.equal(result.rowCount, 2);
  assert.deepEqual(result.rows[0], ['1', 'alice', 'alice@example.com']);
  assert.deepEqual(result.rows[1], ['2', 'bob', null]); // NULL parsed properly
});

test('parseTabularOutput parses PostgreSQL pipe format with dashes', () => {
  const psqlOutput = `
 id | datname  | encoding 
----+----------+----------
  1 | postgres | UTF8
  2 | app_db   | UTF8
(2 rows)
`;
  const result = parseTabularOutput(psqlOutput);
  assert.equal(result.isTabular, true);
  assert.deepEqual(result.columns, ['id', 'datname', 'encoding']);
  assert.equal(result.rowCount, 2);
  assert.deepEqual(result.rows[0], ['1', 'postgres', 'UTF8']);
  assert.deepEqual(result.rows[1], ['2', 'app_db', 'UTF8']);
});

test('parseTabularOutput reports honest fallback on non-tabular output', () => {
  const rawText = 'CREATE TABLE completed successfully.\nQuery OK, 0 rows affected (0.01 sec)';
  const result = parseTabularOutput(rawText);
  assert.equal(result.isTabular, false);
  assert.equal(result.rowCount, 0);
  assert.equal(result.rawText, rawText);
});
