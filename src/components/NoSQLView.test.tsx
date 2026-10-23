import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import { renderToString } from 'react-dom/server';
import {
  NoSQLView,
  resolveNoSQLEngine,
  buildNoSQLClientCommand,
} from './NoSQLView.tsx';
import type { NodeItem } from '../types/index.ts';

const mockNode: NodeItem = {
  id: 'node-test-1',
  name: 'Test Node',
  shortName: 'test-1',
  group: 'servers',
  datacenter: 'dc1',
  nodeType: 'linux',
  ip: '192.168.1.100',
  port: 22,
  tailscaleIp: '100.64.0.1',
  tailscaleHost: 'test-1.leo-banjo.ts.net',
  osDescription: 'Fedora 40',
  status: 'online',
  uptime: '2 days',
  hasGlances: true,
  hasCockpit: false,
  hasPodman: true,
  hasRestic: true,
  specs: {
    cpu: 'AMD Ryzen',
    vcpus: 8,
    ramGiB: 32,
    motherboard: 'ASRock',
    bios: 'v1.0',
    ipmi: 'none',
    ecc: 'yes',
    secureBoot: 'disabled',
    iommu: 'enabled',
  },
};

test('NoSQLView removes duplicate engine switcher tabs and syncs engine from pluginId', () => {
  assert.equal(resolveNoSQLEngine('database/nosql:redis'), 'redis');
  assert.equal(resolveNoSQLEngine('database/nosql:valkey'), 'redis');
  assert.equal(resolveNoSQLEngine('database/nosql:mongodb'), 'mongodb');
  assert.equal(resolveNoSQLEngine('database/nosql:couchdb'), 'couchdb');
  assert.equal(resolveNoSQLEngine('database/nosql:rocksdb'), 'rocksdb');

  const html = renderToString(
    React.createElement(NoSQLView, {
      node: mockNode,
      pluginId: 'database/nosql:mongodb',
      initialData: {
        engines: [
          {
            engine: 'mongodb',
            status: 'active',
            version: '7.0.5',
            databases: ['admin', 'events'],
            users: [{ username: 'admin', roles: ['root'] }],
            metrics: { port: '27017' },
            dataFormat: 'document',
          },
        ],
        count: 1,
      },
    })
  );

  assert.ok(html.includes('NoSQL Database Management'), 'Must render NoSQL Database Management header');
  assert.equal(html.includes('Redis / Valkey'), false, 'Duplicate in-view engine switcher tabs must be removed');
  assert.ok(html.includes('Connection Config (MONGODB):'), 'Must sync engine to MONGODB from pluginId');
  assert.ok(html.includes('NoSQL Data Browser (MONGODB)'), 'Must render MongoDB Data Browser');
  assert.ok(html.includes('Load Collections'), 'Must render Load Collections button for MongoDB');
});

test('NoSQLView renders expanded Connection Config for all engines including RocksDB', () => {
  const redisHtml = renderToString(
    React.createElement(NoSQLView, {
      node: mockNode,
      pluginId: 'database/nosql:redis',
      initialData: {
        engines: [
          {
            engine: 'redis',
            status: 'active',
            version: '7.2.4',
            databases: ['db0', 'db1'],
            users: [{ username: 'default', roles: ['on', '~*', '+@all'] }],
            metrics: { used_memory_human: '14M' },
            dataFormat: 'keyvalue',
          },
        ],
        count: 1,
      },
    })
  );

  assert.ok(redisHtml.includes('nosql-connection-config'), 'Must render Connection Config bar');
  assert.ok(redisHtml.includes('Host:'), 'Must render Host field');
  assert.ok(redisHtml.includes('Username:'), 'Must render Username field');
  assert.ok(redisHtml.includes('Password:'), 'Must render Password field');
  assert.ok(redisHtml.includes('Default DB / Keyspace:'), 'Must render Default DB / Keyspace field');
  assert.ok(redisHtml.includes('TCP Port:'), 'Must render TCP Port field');
  assert.ok(redisHtml.includes('Unix Socket (optional):'), 'Must render Unix Socket field');
  assert.ok(redisHtml.includes('nosql-data-browser'), 'Must render NoSQL Data Browser');
  assert.ok(redisHtml.includes('Browse Keys'), 'Must render Browse Keys button for Redis');

  const rocksHtml = renderToString(
    React.createElement(NoSQLView, {
      node: mockNode,
      pluginId: 'database/nosql:rocksdb',
      initialData: {
        engines: [
          {
            engine: 'rocksdb',
            status: 'active',
            version: 'embedded',
            databases: ['/var/lib/rocksdb'],
            users: [],
            metrics: { path: '/var/lib/rocksdb' },
            dataFormat: 'text',
          },
        ],
        count: 1,
      },
    })
  );

  assert.ok(rocksHtml.includes('Connection Config (ROCKSDB):'), 'Must render Connection Config for RocksDB');
  assert.ok(rocksHtml.includes('DB Path:'), 'Must render DB Path field for RocksDB');
  assert.ok(rocksHtml.includes('Scan Keys'), 'Must render Scan Keys button for RocksDB Data Browser');
});

test('buildNoSQLClientCommand respects host, user, password, database/path, port, and socket', () => {
  const redisCmd = buildNoSQLClientCommand('redis', 'GET mykey', 'db2', {
    redis_host: '10.0.0.5',
    redis_port: '6380',
    redis_user: 'acluser',
    redis_password: 'secret',
  });
  assert.ok(redisCmd.includes("-h '10.0.0.5'"), 'Redis command must include host');
  assert.ok(redisCmd.includes('-p 6380'), 'Redis command must include port');
  assert.ok(redisCmd.includes("--user 'acluser'"), 'Redis command must include ACL user');
  assert.ok(redisCmd.includes("-a 'secret'"), 'Redis command must include password');
  assert.ok(redisCmd.includes('-n 2'), 'Redis command must map db2 to -n 2');

  const mongoCmd = buildNoSQLClientCommand('mongodb', 'db.stats()', 'analytics', {
    mongodb_host: '127.0.0.1',
    mongodb_port: '27018',
    mongodb_user: 'admin',
    mongodb_password: 'pw',
  });
  assert.ok(mongoCmd.includes("--host '127.0.0.1'"), 'Mongo command must include host');
  assert.ok(mongoCmd.includes('--port 27018'), 'Mongo command must include port');
  assert.ok(mongoCmd.includes("-u 'admin'"), 'Mongo command must include user');
  assert.ok(mongoCmd.includes("-p 'pw'"), 'Mongo command must include password');
  assert.ok(mongoCmd.includes("'analytics'"), 'Mongo command must include target database');

  const couchCmd = buildNoSQLClientCommand('couchdb', '/_all_dbs', '_users', {
    couchdb_host: '127.0.0.1',
    couchdb_port: '5984',
    couchdb_user: 'admin',
    couchdb_password: 'pw',
  });
  assert.ok(couchCmd.includes("-u 'admin:pw'"), 'CouchDB command must include basic auth');
  assert.ok(couchCmd.includes('http://127.0.0.1:5984/_all_dbs'), 'CouchDB command must target endpoint');

  const rocksCmd = buildNoSQLClientCommand('rocksdb', 'scan --max_keys=50', '/data/rocks', {});
  assert.ok(rocksCmd.includes("ldb --db='/data/rocks' scan --max_keys=50"), 'RocksDB command must use ldb with --db');
});
