import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import { renderToString } from 'react-dom/server';
import {
  SQLManagementView,
  parseTabularOutput,
  resolveSQLEngine,
  buildSQLClientCommand,
  buildSQLDumpCommand,
  buildSQLTableListQuery,
  buildSQLBrowseRowsQuery,
  suggestSQLDumpPath,
} from './SQLManagementView.tsx';
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

test('SQLManagementView removes duplicate header and engine switcher tabs while keeping Refresh and Dump Database', () => {
  const html = renderToString(
    React.createElement(SQLManagementView, {
      node: mockNode,
      pluginId: 'database/sql:postgres',
    })
  );

  // Red-crossed elements must be absent
  assert.equal(html.includes('SQL Database Management'), false, 'Crossed-out header title must be removed');
  assert.equal(
    html.includes('Manage PostgreSQL, MariaDB/MySQL, and MSSQL instances, users, and schemas'),
    false,
    'Crossed-out header subtitle must be removed'
  );
  assert.equal(html.includes('MariaDB / MySQL'), false, 'Duplicate in-view engine switcher tabs must be removed');

  // Top action bar buttons must be present
  assert.ok(html.includes('Refresh'), 'Must render Refresh button');
  assert.ok(html.includes('sql-dump-database-btn'), 'Must render Dump Database button testid');
  assert.ok(html.includes('Dump Database'), 'Must render Dump Database button label');
});

test('SQLManagementView syncs active engine from pluginId and falls back to first detected engine', () => {
  assert.equal(resolveSQLEngine('database/sql:postgres'), 'postgres');
  assert.equal(resolveSQLEngine('database/sql:mariadb'), 'mariadb');
  assert.equal(resolveSQLEngine('database/sql:mysql'), 'mariadb');
  assert.equal(resolveSQLEngine('database/sql:mssql'), 'mssql');
  assert.equal(
    resolveSQLEngine('database/sql', [{ engine: 'mariadb', status: 'active' }]),
    'mariadb'
  );
  assert.equal(resolveSQLEngine('database/sql', []), 'postgres');

  const mariaHtml = renderToString(
    React.createElement(SQLManagementView, {
      node: mockNode,
      pluginId: 'database/sql:mariadb',
    })
  );
  assert.ok(mariaHtml.includes('Connection Config (MARIADB):'), 'Must select MariaDB when pluginId=database/sql:mariadb');
  // The default view is the Browse Data tab, which carries the engine name.
  assert.ok(mariaHtml.includes('SQL Data Browser (MARIADB)'), 'Data browser must reflect MariaDB');
  // Tabs replace the old stacked panels.
  assert.ok(mariaHtml.includes('sql-view-tabs'), 'Must render the top-level view tabs');
});

test('SQLManagementView renders expanded Connection Config (username, password, default database, port, socket)', () => {
  const html = renderToString(
    React.createElement(SQLManagementView, {
      node: {
        ...mockNode,
        pluginOverrides: {
          postgres_user: 'pgadmin',
          postgres_database: 'analytics_db',
          postgres_port: '5433',
          postgres_socket: '/run/postgresql/.s.PGSQL.5433',
        },
      },
      pluginId: 'database/sql:postgres',
    })
  );

  assert.ok(html.includes('sql-connection-config'), 'Must render connection config bar');
  assert.ok(html.includes('Username:'), 'Must render Username input');
  assert.ok(html.includes('Password:'), 'Must render Password input');
  assert.ok(html.includes('Default Database:'), 'Must render Default Database input');
  assert.ok(html.includes('TCP Port:'), 'Must render TCP Port input');
  assert.ok(html.includes('Unix Socket (optional):'), 'Must render Unix Socket input');
  assert.ok(html.includes('analytics_db'), 'Must populate default database override');
});

test('SQLManagementView renders Dump Database modal and SQL Data Browser panel', () => {
  const html = renderToString(
    React.createElement(SQLManagementView, {
      node: mockNode,
      pluginId: 'database/sql:postgres',
      initialDumpModalOpen: true,
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

  // Dump modal checks
  assert.ok(html.includes('sql-dump-modal'), 'Must render Dump Database modal when open');
  assert.ok(html.includes('Target Database'), 'Must render Target Database field');
  assert.ok(html.includes('Dump Format'), 'Must render Dump Format field');
  assert.ok(html.includes('Plain SQL (.sql)'), 'Must render Postgres plain dump format');
  assert.ok(html.includes('Custom Archive (-Fc)'), 'Must render Postgres custom dump format');
  assert.ok(html.includes('Dump Scope'), 'Must render Dump Scope field');
  assert.ok(html.includes('Include DROP statements before CREATE'), 'Must render clean option');
  assert.ok(html.includes('Compress output with gzip (.gz)'), 'Must render compress option');
  assert.ok(html.includes('Run Dump to Host File'), 'Must render Run Dump to Host File action');
  assert.ok(html.includes('Stage Dump Task to Ansible Queue'), 'Must render Stage Dump Task action');

  // Data Browser checks
  assert.ok(html.includes('sql-data-browser'), 'Must render SQL Data Browser panel');
  assert.ok(html.includes('SQL Data Browser (POSTGRES)'), 'Must render SQL Data Browser title');
  assert.ok(html.includes('Load Tables'), 'Must render Load Tables button');
  assert.ok(html.includes('Select a table to browse rows'), 'Must render table rows viewer');
});

test('buildSQLClientCommand and buildSQLDumpCommand respect user, password, database, port, and socket overrides', () => {
  const pgCmd = buildSQLClientCommand(
    'postgres',
    'SELECT * FROM users;',
    'app_db',
    {
      postgres_user: 'admin',
      postgres_password: 'secret',
      postgres_port: '5433',
      postgres_socket: '/run/postgresql/.s.PGSQL.5433',
    },
    true
  );
  assert.ok(pgCmd.startsWith("PGPASSWORD='secret' psql "), 'Postgres command must prefix PGPASSWORD');
  assert.ok(pgCmd.includes("-h '/run/postgresql'"), 'Postgres command must pass socket dir to -h');
  assert.ok(pgCmd.includes('-p 5433'), 'Postgres command must include port');
  assert.ok(pgCmd.includes("-U 'admin'"), 'Postgres command must include username');
  assert.ok(pgCmd.includes("-d 'app_db'"), 'Postgres command must include database');

  const mariaCmd = buildSQLClientCommand(
    'mariadb',
    'SHOW TABLES;',
    'shop',
    {
      mariadb_user: 'root',
      mariadb_password: 'pw',
      mariadb_port: '3307',
      mariadb_socket: '/run/mysqld/mysqld.sock',
    },
    false
  );
  assert.ok(mariaCmd.includes("-S '/run/mysqld/mysqld.sock'"), 'MariaDB command must include socket');
  assert.ok(mariaCmd.includes("-u 'root'"), 'MariaDB command must include user');
  assert.ok(mariaCmd.includes("-p'pw'"), 'MariaDB command must include password');
  assert.ok(mariaCmd.includes("-D 'shop'"), 'MariaDB command must include database');

  const dumpCmd = buildSQLDumpCommand({
    engine: 'postgres',
    database: 'app_db',
    format: 'custom',
    scope: 'schema_only',
    clean: true,
    compress: false,
    outputPath: '/tmp/app_db.dump',
    overrides: {
      postgres_user: 'postgres',
      postgres_password: 'pw',
      postgres_port: '5432',
    },
  });
  assert.ok(dumpCmd.includes('pg_dump'), 'Must use pg_dump');
  assert.ok(dumpCmd.includes('-Fc'), 'Must pass -Fc for custom archive');
  assert.ok(dumpCmd.includes('--schema-only'), 'Must pass --schema-only');
  assert.ok(dumpCmd.includes('--clean'), 'Must pass --clean');
  assert.ok(dumpCmd.includes("-f '/tmp/app_db.dump'"), 'Must write to output file');

  assert.equal(suggestSQLDumpPath('postgres', 'mydb', 'plain', true), '/tmp/mydb_dump.sql.gz');
  assert.ok(buildSQLTableListQuery('postgres').includes('information_schema.tables'));
  assert.equal(buildSQLTableListQuery('mariadb'), 'SHOW TABLES;');
  assert.ok(buildSQLBrowseRowsQuery('postgres', 'public.users', 25, 50).includes('LIMIT 25 OFFSET 50'));
});

test('parseTabularOutput parses MySQL ascii borders and PostgreSQL pipe format', () => {
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
  assert.deepEqual(result.rows[1], ['2', 'bob', null]);

  const psqlOutput = `
 id | datname  | encoding 
----+----------+----------
  1 | postgres | UTF8
  2 | app_db   | UTF8
(2 rows)
`;
  const pgResult = parseTabularOutput(psqlOutput);
  assert.equal(pgResult.isTabular, true);
  assert.deepEqual(pgResult.columns, ['id', 'datname', 'encoding']);
  assert.equal(pgResult.rowCount, 2);
});
