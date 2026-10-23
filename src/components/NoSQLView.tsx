import React, { useState, useEffect, useMemo } from 'react';
import type { NodeItem } from '../types/index.ts';
import {
  PluginActionForm,
  type ActionSpec,
} from '../plugins/kit/index.ts';

interface NoSQLViewProps {
  node: NodeItem;
  pluginId?: string;
  initialData?: NoSQLReadData;
  initialLoading?: boolean;
}

interface EngineUser {
  username: string;
  roles?: string[];
  database?: string;
}

interface EngineStatus {
  engine: string;
  status: string;
  version?: string;
  databases?: string[];
  users?: EngineUser[];
  metrics?: Record<string, any>;
  dataFormat: 'keyvalue' | 'document' | 'text';
}

interface NoSQLReadData {
  engines?: EngineStatus[];
  count?: number;
}

export interface RedisInspectedKey {
  key: string;
  type: string;
  ttl: string;
  entries: Array<{ field: string; value: string }>;
  rawValue: string;
}

export function resolveNoSQLEngine(pluginId?: string, engines?: EngineStatus[]): string {
  if (pluginId && pluginId.includes(':')) {
    const raw = pluginId.split(':')[1]?.toLowerCase().trim();
    if (raw === 'redis' || raw === 'valkey') return 'redis';
    if (raw === 'mongodb' || raw === 'mongo') return 'mongodb';
    if (raw === 'couchdb') return 'couchdb';
    if (raw === 'rocksdb') return 'rocksdb';
  }
  if (engines && engines.length > 0) {
    const active = engines.find(e => e.status === 'active') || engines[0];
    if (active?.engine === 'valkey') return 'redis';
    if (active?.engine) return active.engine;
  }
  return 'redis';
}

export function defaultPortForNoSQLEngine(eng: string): string {
  if (eng === 'mongodb') return '27017';
  if (eng === 'couchdb') return '5984';
  if (eng === 'rocksdb') return '';
  return '6379';
}

export function defaultDbForNoSQLEngine(eng: string): string {
  if (eng === 'mongodb') return 'admin';
  if (eng === 'couchdb') return '_users';
  if (eng === 'rocksdb') return '/var/lib/rocksdb';
  return 'db0';
}

export function buildNoSQLClientCommand(
  engine: string,
  commandOrPath: string,
  targetDatabase: string,
  overrides: Record<string, string | undefined> = {}
): string {
  if (engine === 'redis' || engine === 'valkey') {
    const sock = (overrides.redis_socket || overrides.valkey_socket || '').trim();
    const host = (overrides.redis_host || overrides.valkey_host || '').trim();
    const port = (overrides.redis_port || overrides.valkey_port || '').trim();
    const user = (overrides.redis_user || overrides.valkey_user || '').trim();
    const pwd = overrides.redis_password || overrides.valkey_password || '';
    const dbRaw = (
      targetDatabase ||
      overrides.redis_database ||
      overrides.valkey_database ||
      ''
    ).trim();
    const dbNum = dbRaw.toLowerCase().replace(/^db/, '');

    const connPart = sock
      ? `-s ${escapeShell(sock)} `
      : `${host ? `-h ${escapeShell(host)} ` : ''}${port ? `-p ${port} ` : ''}`;
    const userPart = user ? `--user ${escapeShell(user)} ` : '';
    const authPart = pwd ? `-a ${escapeShell(pwd)} ` : '';
    const dbPart = dbNum && /^\d+$/.test(dbNum) ? `-n ${dbNum} ` : '';
    return `redis-cli ${connPart}${userPart}${authPart}${dbPart}${commandOrPath}`;
  }

  if (engine === 'mongodb') {
    const db =
      targetDatabase ||
      overrides.mongodb_database ||
      'admin';
    const sock = (overrides.mongodb_socket || '').trim();
    const host = (overrides.mongodb_host || '').trim();
    const port = (overrides.mongodb_port || '').trim();
    const user = (overrides.mongodb_user || '').trim();
    const pwd = overrides.mongodb_password || '';

    const connFlags: string[] = [];
    if (sock) {
      connFlags.push(`--host ${escapeShell(sock)}`);
    } else {
      if (host) connFlags.push(`--host ${escapeShell(host)}`);
      if (port) connFlags.push(`--port ${port}`);
    }
    if (user) connFlags.push(`-u ${escapeShell(user)}`);
    if (pwd) connFlags.push(`-p ${escapeShell(pwd)}`);
    const flagStr = connFlags.length > 0 ? `${connFlags.join(' ')} ` : '';
    return `mongosh --quiet ${flagStr}${escapeShell(db)} --eval ${escapeShell(commandOrPath)}`;
  }

  if (engine === 'couchdb') {
    const host = (overrides.couchdb_host || '127.0.0.1').trim();
    const port = (overrides.couchdb_port || '5984').trim();
    const sock = (overrides.couchdb_socket || '').trim();
    const user = (overrides.couchdb_user || '').trim();
    const pwd = overrides.couchdb_password || '';

    const sockFlag = sock ? `--unix-socket ${escapeShell(sock)} ` : '';
    const authFlag = user ? `-u ${escapeShell(`${user}:${pwd}`)} ` : '';
    const path = commandOrPath.startsWith('/') ? commandOrPath : `/${commandOrPath}`;
    return `curl -s -m 5 ${sockFlag}${authFlag}http://${host}:${port}${path}`;
  }

  if (engine === 'rocksdb') {
    const dbPath =
      targetDatabase ||
      overrides.rocksdb_path ||
      overrides.rocksdb_database ||
      '/var/lib/rocksdb';
    return `ldb --db=${escapeShell(dbPath)} ${commandOrPath}`;
  }

  return '';
}

export const NoSQLView: React.FC<NoSQLViewProps> = ({
  node,
  pluginId,
  initialData,
  initialLoading,
}) => {
  const [data, setData] = useState<NoSQLReadData | null>(initialData || null);
  const [actions, setActions] = useState<ActionSpec[]>([]);
  const [loading, setLoading] = useState(initialLoading ?? (initialData ? false : true));
  const [error, setError] = useState<string | null>(null);
  const [activeEngine, setActiveEngine] = useState<string>(() =>
    resolveNoSQLEngine(pluginId, initialData?.engines)
  );
  const [activeAction, setActiveAction] = useState<ActionSpec | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);

  // Expanded Connection Config state: Host, Username, Password, Default DB / Keyspace / Path, TCP Port, Unix Socket
  const [configHost, setConfigHost] = useState<string>('');
  const [configUser, setConfigUser] = useState<string>('');
  const [configPassword, setConfigPassword] = useState<string>('');
  const [configDatabase, setConfigDatabase] = useState<string>('');
  const [configPort, setConfigPort] = useState<string>('');
  const [configSocket, setConfigSocket] = useState<string>('');
  const [savingConfig, setSavingConfig] = useState<boolean>(false);

  // Raw query workbench state
  const [queryCommand, setQueryCommand] = useState<string>('');
  const [queryDatabase, setQueryDatabase] = useState<string>('');
  const [queryRunning, setQueryRunning] = useState<boolean>(false);
  const [queryResult, setQueryResult] = useState<any>(null);
  const [queryError, setQueryError] = useState<string | null>(null);

  // NoSQL Data Browser state
  const [browserDb, setBrowserDb] = useState<string>('');
  const [browserPattern, setBrowserPattern] = useState<string>('*');
  const [browserLimit, setBrowserLimit] = useState<number>(50);
  const [browserLoading, setBrowserLoading] = useState<boolean>(false);
  const [browserError, setBrowserError] = useState<string | null>(null);

  // Redis key-value browser state
  const [redisKeys, setRedisKeys] = useState<string[]>([]);
  const [selectedRedisKey, setSelectedRedisKey] = useState<string | null>(null);
  const [inspectedRedisKey, setInspectedRedisKey] = useState<RedisInspectedKey | null>(null);

  // MongoDB collection & document browser state
  const [mongoCollections, setMongoCollections] = useState<string[]>([]);
  const [selectedMongoCollection, setSelectedMongoCollection] = useState<string | null>(null);
  const [mongoDocuments, setMongoDocuments] = useState<any[]>([]);

  // CouchDB document browser state
  const [couchDocs, setCouchDocs] = useState<any[]>([]);

  // RocksDB key-value scan browser state
  const [rocksEntries, setRocksEntries] = useState<Array<{ key: string; value: string }>>([]);

  useEffect(() => {
    const resolved = resolveNoSQLEngine(pluginId, data?.engines);
    setActiveEngine(resolved);
    setQueryResult(null);
    setQueryError(null);
    setBrowserError(null);
  }, [pluginId, data?.engines]);

  const fetchData = async () => {
    if (!node?.id) return;
    setLoading(true);
    setError(null);
    try {
      const metaRes = await fetch('/api/plugins');
      if (metaRes.ok) {
        const summaries = await metaRes.json();
        const found = summaries.find((p: any) => p.id === 'database/nosql');
        if (found?.actions) setActions(found.actions);
      }
      const res = await fetch(`/api/nodes/${encodeURIComponent(node.id)}/plugins/database/nosql`);
      if (!res.ok) {
        const text = await res.text();
        setError(`HTTP ${res.status}: ${text || res.statusText}`);
      } else {
        const readData = await res.json();
        setData(readData);
        const resolved = resolveNoSQLEngine(pluginId, readData.engines);
        setActiveEngine(resolved);
      }
    } catch (err: any) {
      setError(err?.message || 'Failed to fetch NoSQL plugin status');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [node?.id]);

  const currentEngineData = data?.engines?.find(
    e => e.engine === activeEngine || (activeEngine === 'redis' && e.engine === 'valkey')
  ) || {
    engine: activeEngine,
    status: 'inactive',
    databases: [],
    users: [],
    metrics: {},
    dataFormat:
      activeEngine === 'redis' || activeEngine === 'valkey'
        ? 'keyvalue'
        : activeEngine === 'rocksdb'
        ? 'text'
        : 'document',
  };

  useEffect(() => {
    const overrides = node?.pluginOverrides || {};
    const metricPort = currentEngineData.metrics?.port ? String(currentEngineData.metrics.port) : '';
    const metricSocket = currentEngineData.metrics?.socket ? String(currentEngineData.metrics.socket) : '';
    const metricHost = currentEngineData.metrics?.host ? String(currentEngineData.metrics.host) : '';
    const metricUser = currentEngineData.metrics?.user ? String(currentEngineData.metrics.user) : '';
    const metricDb =
      currentEngineData.metrics?.database || currentEngineData.metrics?.path
        ? String(currentEngineData.metrics.database || currentEngineData.metrics.path)
        : '';

    const ovPort =
      overrides[`${activeEngine}_port`] ||
      (activeEngine === 'redis' ? overrides.valkey_port : '') ||
      '';
    const ovSocket =
      overrides[`${activeEngine}_socket`] ||
      (activeEngine === 'redis' ? overrides.valkey_socket : '') ||
      '';
    const ovHost =
      overrides[`${activeEngine}_host`] ||
      (activeEngine === 'redis' ? overrides.valkey_host : '') ||
      '';
    const ovUser =
      overrides[`${activeEngine}_user`] ||
      (activeEngine === 'redis' ? overrides.valkey_user : '') ||
      '';
    const ovPassword =
      overrides[`${activeEngine}_password`] ||
      (activeEngine === 'redis' ? overrides.valkey_password : '') ||
      '';
    const ovDb =
      (activeEngine === 'rocksdb'
        ? overrides.rocksdb_path || overrides.rocksdb_database
        : overrides[`${activeEngine}_database`] ||
          (activeEngine === 'redis' ? overrides.valkey_database : '')) || '';

    setConfigPort(ovPort || metricPort || defaultPortForNoSQLEngine(activeEngine));
    setConfigSocket(ovSocket || metricSocket || '');
    setConfigHost(ovHost || metricHost || '');
    setConfigUser(ovUser || metricUser || '');
    setConfigPassword(ovPassword || '');
    const resolvedDb =
      ovDb ||
      metricDb ||
      currentEngineData.databases?.[0] ||
      defaultDbForNoSQLEngine(activeEngine);
    setConfigDatabase(ovDb || metricDb || '');
    setQueryDatabase(resolvedDb);
    setBrowserDb(resolvedDb);
  }, [
    activeEngine,
    node?.pluginOverrides,
    currentEngineData.metrics?.port,
    currentEngineData.metrics?.socket,
    currentEngineData.metrics?.host,
    currentEngineData.metrics?.user,
    currentEngineData.metrics?.database,
    currentEngineData.metrics?.path,
  ]);

  const effectiveOverrides = useMemo((): Record<string, string | undefined> => {
    const base: Record<string, string | undefined> = {
      ...(node?.pluginOverrides || {}),
      [`${activeEngine}_host`]: configHost.trim() || undefined,
      [`${activeEngine}_user`]: configUser.trim() || undefined,
      [`${activeEngine}_password`]: configPassword || undefined,
      [`${activeEngine}_database`]: configDatabase.trim() || undefined,
      [`${activeEngine}_port`]: configPort.trim() || defaultPortForNoSQLEngine(activeEngine) || undefined,
      [`${activeEngine}_socket`]: configSocket.trim() || undefined,
    };
    if (activeEngine === 'rocksdb' && configDatabase.trim()) {
      base.rocksdb_path = configDatabase.trim();
    }
    return base;
  }, [
    node?.pluginOverrides,
    activeEngine,
    configHost,
    configUser,
    configPassword,
    configDatabase,
    configPort,
    configSocket,
  ]);

  const handleSaveConnectionConfig = async () => {
    if (!node?.id) return;
    setSavingConfig(true);
    setError(null);
    setNotice(null);
    try {
      const updatedOverrides: NonNullable<NodeItem['pluginOverrides']> = {
        ...(node.pluginOverrides || {}),
      };

      const setOrDelete = (key: string, val: string) => {
        if (val) {
          updatedOverrides[key] = val;
        } else {
          delete updatedOverrides[key];
        }
      };

      if (activeEngine !== 'rocksdb') {
        updatedOverrides[`${activeEngine}_port`] =
          configPort.trim() || defaultPortForNoSQLEngine(activeEngine);
        setOrDelete(`${activeEngine}_socket`, configSocket.trim());
      } else {
        setOrDelete('rocksdb_port', configPort.trim());
        setOrDelete('rocksdb_socket', configSocket.trim());
      }

      setOrDelete(`${activeEngine}_host`, configHost.trim());
      setOrDelete(`${activeEngine}_user`, configUser.trim());
      setOrDelete(`${activeEngine}_password`, configPassword);
      setOrDelete(`${activeEngine}_database`, configDatabase.trim());
      if (activeEngine === 'rocksdb') {
        setOrDelete('rocksdb_path', configDatabase.trim());
      }

      const putRes = await fetch(`/api/nodes/${encodeURIComponent(node.id)}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          ...node,
          pluginOverrides: updatedOverrides,
        }),
      });
      if (!putRes.ok) {
        const errText = await putRes.text();
        throw new Error(`Failed to save connection config: ${errText || putRes.statusText}`);
      }
      if (configDatabase.trim()) {
        setQueryDatabase(configDatabase.trim());
        setBrowserDb(configDatabase.trim());
      }
      await fetch(`/api/nodes/${encodeURIComponent(node.id)}/plugins/refresh`, { method: 'POST' });
      setNotice(`Saved ${activeEngine.toUpperCase()} connection settings and re-probed host.`);
      await fetchData();
    } catch (err: any) {
      setError(err?.message || 'Failed to save connection config');
    } finally {
      setSavingConfig(false);
    }
  };

  const handleActionSubmit = async (values: Record<string, any>) => {
    if (!activeAction) return;
    setSubmitting(true);
    setNotice(null);
    try {
      const compileRes = await fetch(
        `/api/nodes/${encodeURIComponent(node.id)}/plugins/database/nosql/actions/${encodeURIComponent(activeAction.name)}`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ options: { ...values, engine: values.engine || activeEngine } }),
        }
      );
      if (!compileRes.ok) {
        const errText = await compileRes.text();
        throw new Error(`Failed to compile action: ${errText || compileRes.statusText}`);
      }
      const taskSpec = await compileRes.json();

      // Stage task into Ansible queue
      const stageRes = await fetch('/api/queue/task', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          nodeId: node.id,
          task: taskSpec,
        }),
      });
      if (!stageRes.ok) {
        const errText = await stageRes.text();
        throw new Error(`Failed to stage task: ${errText || stageRes.statusText}`);
      }

      setNotice(`Task staged to Ansible queue: ${taskSpec.name}`);
      setActiveAction(null);
      fetchData();
    } catch (err: any) {
      setError(err?.message || 'Action failed');
    } finally {
      setSubmitting(false);
    }
  };

  const runQuery = async () => {
    if (!queryCommand.trim()) return;
    setQueryRunning(true);
    setQueryError(null);
    setQueryResult(null);
    try {
      const cmd = buildNoSQLClientCommand(
        activeEngine,
        queryCommand,
        queryDatabase,
        effectiveOverrides
      );

      const res = await fetch('/api/terminal/exec', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          nodeId: node.id,
          command: cmd,
        }),
      });

      if (!res.ok) {
        const text = await res.text();
        throw new Error(`HTTP ${res.status}: ${text || res.statusText}`);
      }
      const json = await res.json();
      if (json.exitCode !== 0 && json.exitCode !== undefined) {
        setQueryError(json.stderr || json.stdout || `Command exited with status ${json.exitCode}`);
      } else {
        const rawOutput = json.stdout || '';
        if (currentEngineData.dataFormat === 'document') {
          try {
            setQueryResult(JSON.parse(rawOutput));
          } catch {
            setQueryResult(rawOutput);
          }
        } else if (currentEngineData.dataFormat === 'keyvalue') {
          const lines = rawOutput.trim().split('\n');
          setQueryResult(lines);
        } else {
          setQueryResult(rawOutput);
        }
      }
    } catch (err: any) {
      setQueryError(err?.message || 'Query execution failed');
    } finally {
      setQueryRunning(false);
    }
  };

  // NoSQL Data Browser Handlers
  const handleBrowseRedisKeys = async () => {
    setBrowserLoading(true);
    setBrowserError(null);
    setSelectedRedisKey(null);
    setInspectedRedisKey(null);
    try {
      const pat = browserPattern.trim() || '*';
      const scanCmd = `${buildNoSQLClientCommand(
        'redis',
        `--scan --pattern ${escapeShell(pat)}`,
        browserDb,
        effectiveOverrides
      )} | head -n ${browserLimit}`;
      const res = await fetch('/api/terminal/exec', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ nodeId: node.id, command: scanCmd }),
      });
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}: ${(await res.text()) || res.statusText}`);
      }
      const json = await res.json();
      if (json.exitCode !== 0 && json.exitCode !== undefined) {
        setBrowserError(json.stderr || json.stdout || `Scan failed (${json.exitCode})`);
      } else {
        const keys = String(json.stdout || '')
          .split('\n')
          .map(k => k.trim())
          .filter(Boolean);
        setRedisKeys(keys);
      }
    } catch (err: any) {
      setBrowserError(err?.message || 'Failed to scan Redis keys');
    } finally {
      setBrowserLoading(false);
    }
  };

  const handleInspectRedisKey = async (key: string) => {
    setSelectedRedisKey(key);
    setBrowserLoading(true);
    setBrowserError(null);
    try {
      const kEsc = escapeShell(key);
      const typeCmd = buildNoSQLClientCommand('redis', `TYPE ${kEsc}`, browserDb, effectiveOverrides);
      const ttlCmd = buildNoSQLClientCommand('redis', `TTL ${kEsc}`, browserDb, effectiveOverrides);
      const inspectScript = `t=$(${typeCmd}); ttl=$(${ttlCmd}); echo "TYPE:$t"; echo "TTL:$ttl"; echo "===VAL==="; case "$t" in string) ${buildNoSQLClientCommand(
        'redis',
        `GET ${kEsc}`,
        browserDb,
        effectiveOverrides
      )} ;; hash) ${buildNoSQLClientCommand(
        'redis',
        `HGETALL ${kEsc}`,
        browserDb,
        effectiveOverrides
      )} ;; list) ${buildNoSQLClientCommand(
        'redis',
        `LRANGE ${kEsc} 0 99`,
        browserDb,
        effectiveOverrides
      )} ;; set) ${buildNoSQLClientCommand(
        'redis',
        `SMEMBERS ${kEsc}`,
        browserDb,
        effectiveOverrides
      )} ;; zset) ${buildNoSQLClientCommand(
        'redis',
        `ZRANGE ${kEsc} 0 99 WITHSCORES`,
        browserDb,
        effectiveOverrides
      )} ;; *) ${buildNoSQLClientCommand('redis', `GET ${kEsc}`, browserDb, effectiveOverrides)} ;; esac`;

      const res = await fetch('/api/terminal/exec', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ nodeId: node.id, command: inspectScript }),
      });
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}: ${(await res.text()) || res.statusText}`);
      }
      const json = await res.json();
      const stdout = String(json.stdout || '');
      const [metaPart, valPart = ''] = stdout.split('===VAL===');
      const typeMatch = metaPart.match(/TYPE:(\S+)/);
      const ttlMatch = metaPart.match(/TTL:(\S+)/);
      const keyType = typeMatch ? typeMatch[1].trim() : 'string';
      const keyTtl = ttlMatch ? ttlMatch[1].trim() : '-1';
      const rawVal = valPart.trim();
      const lines = rawVal ? rawVal.split('\n') : [];
      const entries: Array<{ field: string; value: string }> = [];

      if (keyType === 'hash' || keyType === 'zset') {
        for (let i = 0; i < lines.length; i += 2) {
          entries.push({
            field: lines[i] ?? '',
            value: lines[i + 1] ?? '',
          });
        }
      } else {
        lines.forEach((line, idx) => {
          entries.push({
            field: keyType === 'string' ? 'value' : `[${idx}]`,
            value: line,
          });
        });
      }

      setInspectedRedisKey({
        key,
        type: keyType,
        ttl: keyTtl,
        entries,
        rawValue: rawVal,
      });
    } catch (err: any) {
      setBrowserError(err?.message || 'Failed to inspect Redis key');
    } finally {
      setBrowserLoading(false);
    }
  };

  const handleLoadMongoCollections = async () => {
    setBrowserLoading(true);
    setBrowserError(null);
    setSelectedMongoCollection(null);
    setMongoDocuments([]);
    try {
      const targetDb = browserDb.trim() || 'admin';
      const cmd = buildNoSQLClientCommand(
        'mongodb',
        'JSON.stringify(db.getCollectionNames())',
        targetDb,
        effectiveOverrides
      );
      const res = await fetch('/api/terminal/exec', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ nodeId: node.id, command: cmd }),
      });
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}: ${(await res.text()) || res.statusText}`);
      }
      const json = await res.json();
      if (json.exitCode !== 0 && json.exitCode !== undefined) {
        setBrowserError(json.stderr || json.stdout || `Failed to list collections (${json.exitCode})`);
      } else {
        const raw = String(json.stdout || '').trim();
        try {
          const parsed = JSON.parse(raw);
          setMongoCollections(Array.isArray(parsed) ? parsed.map(String) : []);
        } catch {
          setMongoCollections(
            raw
              .split('\n')
              .map(s => s.trim())
              .filter(Boolean)
          );
        }
      }
    } catch (err: any) {
      setBrowserError(err?.message || 'Failed to load MongoDB collections');
    } finally {
      setBrowserLoading(false);
    }
  };

  const handleBrowseMongoCollection = async (colName: string) => {
    setSelectedMongoCollection(colName);
    setBrowserLoading(true);
    setBrowserError(null);
    try {
      const targetDb = browserDb.trim() || 'admin';
      const safeCol = colName.replace(/"/g, '\\"');
      const evalExpr = `JSON.stringify(db.getCollection("${safeCol}").find({}).limit(${browserLimit}).toArray())`;
      const cmd = buildNoSQLClientCommand('mongodb', evalExpr, targetDb, effectiveOverrides);
      const res = await fetch('/api/terminal/exec', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ nodeId: node.id, command: cmd }),
      });
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}: ${(await res.text()) || res.statusText}`);
      }
      const json = await res.json();
      if (json.exitCode !== 0 && json.exitCode !== undefined) {
        setBrowserError(json.stderr || json.stdout || `Failed to query collection (${json.exitCode})`);
      } else {
        const raw = String(json.stdout || '').trim();
        try {
          const parsed = JSON.parse(raw);
          setMongoDocuments(Array.isArray(parsed) ? parsed : [parsed]);
        } catch {
          setMongoDocuments([{ raw }]);
        }
      }
    } catch (err: any) {
      setBrowserError(err?.message || 'Failed to browse MongoDB documents');
    } finally {
      setBrowserLoading(false);
    }
  };

  const handleBrowseCouchDocuments = async () => {
    setBrowserLoading(true);
    setBrowserError(null);
    setCouchDocs([]);
    try {
      const targetDb = browserDb.trim() || currentEngineData.databases?.[0] || '_users';
      const cmd = buildNoSQLClientCommand(
        'couchdb',
        `/${encodeURIComponent(targetDb)}/_all_docs?include_docs=true&limit=${browserLimit}`,
        targetDb,
        effectiveOverrides
      );
      const res = await fetch('/api/terminal/exec', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ nodeId: node.id, command: cmd }),
      });
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}: ${(await res.text()) || res.statusText}`);
      }
      const json = await res.json();
      if (json.exitCode !== 0 && json.exitCode !== undefined) {
        setBrowserError(json.stderr || json.stdout || `CouchDB browse failed (${json.exitCode})`);
      } else {
        const raw = String(json.stdout || '').trim();
        try {
          const parsed = JSON.parse(raw);
          setCouchDocs(Array.isArray(parsed?.rows) ? parsed.rows : [parsed]);
        } catch {
          setCouchDocs([{ id: 'raw', doc: raw }]);
        }
      }
    } catch (err: any) {
      setBrowserError(err?.message || 'Failed to browse CouchDB documents');
    } finally {
      setBrowserLoading(false);
    }
  };

  const handleScanRocksDBKeys = async () => {
    setBrowserLoading(true);
    setBrowserError(null);
    setRocksEntries([]);
    try {
      const dbPath =
        browserDb.trim() ||
        configDatabase.trim() ||
        effectiveOverrides.rocksdb_path ||
        '/var/lib/rocksdb';
      const prefixFlag =
        browserPattern.trim() && browserPattern.trim() !== '*'
          ? ` --from=${escapeShell(browserPattern.trim())}`
          : '';
      const cmd = buildNoSQLClientCommand(
        'rocksdb',
        `scan --max_keys=${browserLimit}${prefixFlag}`,
        dbPath,
        effectiveOverrides
      );
      const res = await fetch('/api/terminal/exec', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ nodeId: node.id, command: cmd }),
      });
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}: ${(await res.text()) || res.statusText}`);
      }
      const json = await res.json();
      if (json.exitCode !== 0 && json.exitCode !== undefined) {
        setBrowserError(json.stderr || json.stdout || `RocksDB scan failed (${json.exitCode})`);
      } else {
        const lines = String(json.stdout || '')
          .split('\n')
          .map(l => l.trim())
          .filter(Boolean);
        const entries = lines.map(line => {
          const arrowIdx = line.indexOf(' ==> ');
          if (arrowIdx !== -1) {
            return { key: line.slice(0, arrowIdx), value: line.slice(arrowIdx + 5) };
          }
          const colonIdx = line.indexOf(':');
          if (colonIdx !== -1) {
            return { key: line.slice(0, colonIdx).trim(), value: line.slice(colonIdx + 1).trim() };
          }
          return { key: line, value: '' };
        });
        setRocksEntries(entries);
      }
    } catch (err: any) {
      setBrowserError(err?.message || 'Failed to scan RocksDB keys');
    } finally {
      setBrowserLoading(false);
    }
  };

  return (
    <div className="flex flex-col gap-4 p-4 font-mono text-xs text-[#c3c6d2]">
      {/* Top Header (duplicate in-view engine switcher tabs removed) */}
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[#26293c] pb-3">
        <div className="flex items-center gap-2">
          <span className="material-symbols-outlined text-lg text-[#8cd6cb]">database</span>
          <span className="font-bold text-sm text-[#dfe1fa]">NoSQL Database Management</span>
          <span className="text-[10px] text-[#8d909b] uppercase ml-2 tracking-wider">
            {node.name} ({node.tailscaleHost || node.ip || 'local'})
          </span>
        </div>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={fetchData}
            disabled={loading}
            className="px-2.5 py-1 rounded bg-[#1b1e31] hover:bg-[#262a42] text-[#c3c6d2] border border-[#26293c] text-[11px] flex items-center gap-1 cursor-pointer"
          >
            <span className={`material-symbols-outlined text-[13px] ${loading ? 'animate-spin' : ''}`}>
              refresh
            </span>
            Refresh
          </button>
        </div>
      </div>

      {/* Expanded Connection Config Bar for ALL engines */}
      <div
        data-testid="nosql-connection-config"
        className="bg-[#131627] border border-[#26293c] rounded p-2.5 flex flex-wrap items-center justify-between gap-2 text-[11px]"
      >
        <div className="flex items-center gap-2 text-[#dfe1fa] font-bold">
          <span className="material-symbols-outlined text-[15px] text-[#8cd6cb]">settings_ethernet</span>
          <span>{`Connection Config (${activeEngine.toUpperCase()}):`}</span>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <label className="flex items-center gap-1 text-[#8d909b]">
            <span>Host:</span>
            <input
              type="text"
              aria-label="Host"
              value={configHost}
              onChange={e => setConfigHost(e.target.value)}
              placeholder="127.0.0.1"
              className="bg-[#0a0d1f] border border-[#26293c] rounded px-2 py-0.5 text-[11px] text-[#dfe1fa] w-28"
            />
          </label>
          <label className="flex items-center gap-1 text-[#8d909b]">
            <span>Username:</span>
            <input
              type="text"
              aria-label="Username"
              value={configUser}
              onChange={e => setConfigUser(e.target.value)}
              placeholder={activeEngine === 'redis' ? 'default' : 'admin'}
              className="bg-[#0a0d1f] border border-[#26293c] rounded px-2 py-0.5 text-[11px] text-[#dfe1fa] w-24"
            />
          </label>
          <label className="flex items-center gap-1 text-[#8d909b]">
            <span>Password:</span>
            <input
              type="password"
              aria-label="Password"
              value={configPassword}
              onChange={e => setConfigPassword(e.target.value)}
              placeholder="••••••••"
              className="bg-[#0a0d1f] border border-[#26293c] rounded px-2 py-0.5 text-[11px] text-[#dfe1fa] w-28"
            />
          </label>
          <label className="flex items-center gap-1 text-[#8d909b]">
            <span>{activeEngine === 'rocksdb' ? 'DB Path:' : 'Default DB / Keyspace:'}</span>
            <input
              type="text"
              aria-label="Default DB / Keyspace / Path"
              value={configDatabase}
              onChange={e => setConfigDatabase(e.target.value)}
              placeholder={defaultDbForNoSQLEngine(activeEngine)}
              className="bg-[#0a0d1f] border border-[#26293c] rounded px-2 py-0.5 text-[11px] text-[#dfe1fa] w-32"
            />
          </label>
          {activeEngine !== 'rocksdb' && (
            <>
              <label className="flex items-center gap-1 text-[#8d909b]">
                <span>TCP Port:</span>
                <input
                  type="text"
                  aria-label="TCP Port"
                  value={configPort}
                  onChange={e => setConfigPort(e.target.value)}
                  placeholder={defaultPortForNoSQLEngine(activeEngine)}
                  className="bg-[#0a0d1f] border border-[#26293c] rounded px-2 py-0.5 text-[11px] text-[#dfe1fa] w-20"
                />
              </label>
              <label className="flex items-center gap-1 text-[#8d909b]">
                <span>Unix Socket (optional):</span>
                <input
                  type="text"
                  aria-label="Unix Socket"
                  value={configSocket}
                  onChange={e => setConfigSocket(e.target.value)}
                  placeholder={
                    activeEngine === 'redis'
                      ? '/run/redis/redis.sock'
                      : activeEngine === 'mongodb'
                      ? `/tmp/mongodb-${configPort || '27017'}.sock`
                      : '/run/couchdb/couchdb.sock'
                  }
                  className="bg-[#0a0d1f] border border-[#26293c] rounded px-2 py-0.5 text-[11px] text-[#dfe1fa] w-44"
                />
              </label>
            </>
          )}
          <button
            type="button"
            disabled={savingConfig}
            onClick={handleSaveConnectionConfig}
            className="px-2.5 py-0.5 rounded bg-[#21263d] hover:bg-[#313754] disabled:opacity-50 text-[#b0c9ff] border border-[#b0c9ff]/40 font-bold cursor-pointer"
          >
            {savingConfig ? 'Saving...' : 'Save Connection / Re-probe'}
          </button>
        </div>
      </div>

      {notice && (
        <div className="bg-[#1b263b] text-[#8cd6cb] p-2 rounded border border-[#8cd6cb]/30 flex items-center justify-between">
          <span>{notice}</span>
          <button type="button" onClick={() => setNotice(null)} className="text-xs hover:text-[#dfe1fa] cursor-pointer">✕</button>
        </div>
      )}

      {error && (
        <div className="bg-[#3a1b24] text-[#ffb4ab] p-2 rounded border border-[#ffb4ab]/30 flex items-center justify-between">
          <span>{error}</span>
          <button type="button" onClick={() => setError(null)} className="text-xs hover:text-[#dfe1fa] cursor-pointer">✕</button>
        </div>
      )}

      {loading && !data ? (
        <div className="flex items-center justify-center p-8 text-[#8d909b]">
          <span className="material-symbols-outlined animate-spin mr-2">refresh</span>
          Probing NoSQL engines...
        </div>
      ) : (
        <>
          <div className="grid grid-cols-1 lg:grid-cols-12 gap-4">
            {/* Left Column: Databases & Users Management */}
            <div className="lg:col-span-5 flex flex-col gap-4">
              {/* Engine Overview Card */}
              <div className="bg-[#171a2d] border border-[#26293c] rounded p-3 flex flex-col gap-2">
                <div className="flex items-center justify-between">
                  <span className="text-[11px] font-bold uppercase text-[#b0c9ff]">
                    {activeEngine.toUpperCase()} ENGINE
                  </span>
                  <span
                    className={`px-2 py-0.5 rounded text-[10px] font-bold uppercase ${
                      currentEngineData.status === 'active'
                        ? 'bg-[#1b3b2b] text-[#8cd6cb] border border-[#8cd6cb]/30'
                        : 'bg-[#26293c] text-[#8d909b]'
                    }`}
                  >
                    {currentEngineData.status}
                  </span>
                </div>
                <div className="text-[11px] text-[#8d909b] flex flex-col gap-1">
                  <div>Version: <span className="text-[#dfe1fa]">{currentEngineData.version || 'unknown'}</span></div>
                  <div>Data Format: <span className="text-[#dfe1fa] uppercase">{currentEngineData.dataFormat}</span></div>
                  {currentEngineData.metrics && Object.entries(currentEngineData.metrics).map(([k, v]) => (
                    <div key={k}>{k}: <span className="text-[#dfe1fa]">{String(v)}</span></div>
                  ))}
                </div>
              </div>

              {/* Databases / Keyspaces / Collections */}
              <div className="bg-[#171a2d] border border-[#26293c] rounded p-3 flex flex-col gap-2">
                <div className="flex items-center justify-between border-b border-[#26293c] pb-2">
                  <span className="font-bold text-[11px] text-[#dfe1fa]">
                    Databases / Keyspaces ({currentEngineData.databases?.length || 0})
                  </span>
                  <div className="flex items-center gap-1">
                    <button
                      type="button"
                      onClick={() => {
                        const act = actions.find(a => a.name === 'database_create');
                        if (act) setActiveAction(act);
                      }}
                      className="px-2 py-0.5 rounded bg-[#21263d] hover:bg-[#313754] text-[#b0c9ff] text-[10px] font-bold border border-[#b0c9ff]/30 cursor-pointer"
                    >
                      + Create
                    </button>
                    <button
                      type="button"
                      onClick={() => {
                        const act = actions.find(a => a.name === 'database_drop');
                        if (act) setActiveAction(act);
                      }}
                      className="px-2 py-0.5 rounded bg-[#3a1b24] hover:bg-[#522533] text-[#ffb4ab] text-[10px] font-bold border border-[#ffb4ab]/30 cursor-pointer"
                    >
                      Drop
                    </button>
                  </div>
                </div>
                <div className="max-h-48 overflow-y-auto divide-y divide-[#26293c]/50">
                  {currentEngineData.databases && currentEngineData.databases.length > 0 ? (
                    currentEngineData.databases.map(db => (
                      <div
                        key={db}
                        className="py-1 px-2 flex items-center justify-between hover:bg-[#21263d]/40 rounded cursor-pointer"
                        onClick={() => {
                          setQueryDatabase(db);
                          setBrowserDb(db);
                        }}
                      >
                        <span className="text-[#dfe1fa]">{db}</span>
                        <span className="text-[10px] text-[#8d909b]">select</span>
                      </div>
                    ))
                  ) : (
                    <div className="text-center py-4 text-[#8d909b] text-[11px]">No databases or keyspaces listed</div>
                  )}
                </div>
              </div>

              {/* Users & ACL Management */}
              <div className="bg-[#171a2d] border border-[#26293c] rounded p-3 flex flex-col gap-2">
                <div className="flex items-center justify-between border-b border-[#26293c] pb-2">
                  <span className="font-bold text-[11px] text-[#dfe1fa]">
                    Users &amp; Access Controls ({currentEngineData.users?.length || 0})
                  </span>
                  <div className="flex items-center gap-1">
                    <button
                      type="button"
                      onClick={() => {
                        const act = actions.find(a => a.name === 'user_create');
                        if (act) setActiveAction(act);
                      }}
                      className="px-2 py-0.5 rounded bg-[#21263d] hover:bg-[#313754] text-[#b0c9ff] text-[10px] font-bold border border-[#b0c9ff]/30 cursor-pointer"
                    >
                      + Add User
                    </button>
                    <button
                      type="button"
                      onClick={() => {
                        const act = actions.find(a => a.name === 'user_delete');
                        if (act) setActiveAction(act);
                      }}
                      className="px-2 py-0.5 rounded bg-[#3a1b24] hover:bg-[#522533] text-[#ffb4ab] text-[10px] font-bold border border-[#ffb4ab]/30 cursor-pointer"
                    >
                      Delete
                    </button>
                  </div>
                </div>
                <div className="max-h-48 overflow-y-auto divide-y divide-[#26293c]/50">
                  {currentEngineData.users && currentEngineData.users.length > 0 ? (
                    currentEngineData.users.map((u, idx) => (
                      <div key={`${u.username}-${idx}`} className="py-1 px-2 flex items-center justify-between">
                        <div className="flex flex-col">
                          <span className="text-[#dfe1fa] font-bold">{u.username}</span>
                          {u.database && <span className="text-[9px] text-[#8d909b]">db: {u.database}</span>}
                        </div>
                        <span className="text-[10px] text-[#8cd6cb] truncate max-w-[120px]" title={u.roles?.join(', ')}>
                          {u.roles?.join(', ') || 'user'}
                        </span>
                      </div>
                    ))
                  ) : (
                    <div className="text-center py-4 text-[#8d909b] text-[11px]">No users or ACL rules found</div>
                  )}
                </div>
              </div>

              {/* Action Form Modal / Inline */}
              {activeAction && (
                <div className="bg-[#121528] border border-[#b0c9ff]/40 rounded p-3">
                  <div className="flex items-center justify-between mb-2">
                    <span className="font-bold text-[#b0c9ff]">{activeAction.label}</span>
                    <button type="button" onClick={() => setActiveAction(null)} className="text-xs text-[#8d909b] hover:text-[#dfe1fa] cursor-pointer">✕</button>
                  </div>
                  <PluginActionForm
                    action={activeAction}
                    onSubmit={handleActionSubmit}
                    onCancel={() => setActiveAction(null)}
                    submitting={submitting}
                  />
                </div>
              )}
            </div>

            {/* Right Column: Raw Query Workbench & Data-Shape Specific Results */}
            <div className="lg:col-span-7 flex flex-col gap-4">
              <div className="bg-[#171a2d] border border-[#26293c] rounded p-3 flex flex-col gap-3">
                <div className="flex items-center justify-between">
                  <span className="font-bold text-[12px] text-[#dfe1fa] flex items-center gap-1.5">
                    <span className="material-symbols-outlined text-[15px] text-[#b0c9ff]">terminal</span>
                    {`Raw Query Workbench (${activeEngine.toUpperCase()})`}
                  </span>
                  <div className="flex items-center gap-2">
                    <input
                      type="text"
                      value={queryDatabase}
                      onChange={e => setQueryDatabase(e.target.value)}
                      placeholder={activeEngine === 'mongodb' ? 'db name (e.g. admin)' : activeEngine === 'rocksdb' ? 'db path' : 'keyspace / db'}
                      className="bg-[#0a0d1f] border border-[#26293c] rounded px-2 py-1 text-[11px] text-[#dfe1fa] w-36"
                    />
                    <button
                      type="button"
                      disabled={queryRunning || !queryCommand.trim()}
                      onClick={runQuery}
                      className="bg-[#21263d] hover:bg-[#313754] disabled:opacity-50 text-[#b0c9ff] border border-[#b0c9ff]/40 font-bold px-3 py-1 rounded text-[11px] flex items-center gap-1 cursor-pointer disabled:cursor-not-allowed"
                    >
                      {queryRunning ? (
                        <span className="material-symbols-outlined animate-spin text-[13px]">refresh</span>
                      ) : (
                        <span className="material-symbols-outlined text-[13px]">play_arrow</span>
                      )}
                      Run Query
                    </button>
                  </div>
                </div>

                {/* Command Input Area */}
                <div>
                  <textarea
                    rows={3}
                    value={queryCommand}
                    onChange={e => setQueryCommand(e.target.value)}
                    placeholder={
                      activeEngine === 'redis'
                        ? 'KEYS * | GET mykey | INFO | DBSIZE'
                        : activeEngine === 'mongodb'
                        ? 'db.users.find().limit(5) | db.adminCommand({listDatabases: 1})'
                        : activeEngine === 'couchdb'
                        ? '/_all_dbs | /<db>/_all_docs?limit=10'
                        : 'scan | get <key>'
                    }
                    className="w-full bg-[#0a0d1f] border border-[#26293c] rounded p-2 text-xs font-mono text-[#dfe1fa] focus:outline-none focus:border-[#b0c9ff]"
                  />
                </div>

                {/* Results View - Dedicated Data Formats */}
                <div className="flex flex-col gap-2">
                  <div className="flex items-center justify-between text-[11px] text-[#8d909b]">
                    <span>Query Results</span>
                    <span className="uppercase text-[10px]">
                      Render View: {currentEngineData.dataFormat}
                    </span>
                  </div>

                  {queryError && (
                    <div className="bg-[#3a1b24] text-[#ffb4ab] p-3 rounded border border-[#ffb4ab]/30 text-xs">
                      <span className="font-bold">Error:</span> {queryError}
                    </div>
                  )}

                  {queryResult !== null && (
                    <div className="bg-[#0a0d1f] border border-[#26293c] rounded p-3 overflow-x-auto max-h-[450px]">
                      {/* View Type 1: Key / Value View (Redis / Valkey) */}
                      {currentEngineData.dataFormat === 'keyvalue' && (
                        <div className="flex flex-col divide-y divide-[#26293c]/60">
                          {Array.isArray(queryResult) ? (
                            queryResult.map((line: string, i: number) => {
                              const [k, ...vParts] = line.split(':');
                              const v = vParts.join(':');
                              return (
                                <div key={i} className="py-1 px-1 flex items-start gap-3 hover:bg-[#1b1e31]">
                                  <span className="text-[#8cd6cb] font-bold shrink-0">{k}</span>
                                  {v ? (
                                    <span className="text-[#dfe1fa] break-all">{v}</span>
                                  ) : (
                                    <span className="text-[#dfe1fa]">{line}</span>
                                  )}
                                </div>
                              );
                            })
                          ) : (
                            <pre className="text-[#dfe1fa] whitespace-pre-wrap">{String(queryResult)}</pre>
                          )}
                        </div>
                      )}

                      {/* View Type 2: Document / JSON Tree View (Mongo / CouchDB) */}
                      {currentEngineData.dataFormat === 'document' && (
                        <pre className="text-[#8cd6cb] text-xs font-mono leading-relaxed whitespace-pre-wrap">
                          {typeof queryResult === 'object'
                            ? JSON.stringify(queryResult, null, 2)
                            : String(queryResult)}
                        </pre>
                      )}

                      {/* View Type 3: Text / Stream Key Enumeration View (RocksDB) */}
                      {currentEngineData.dataFormat === 'text' && (
                        <pre className="text-[#dfe1fa] text-xs font-mono leading-relaxed whitespace-pre-wrap">
                          {String(queryResult)}
                        </pre>
                      )}
                    </div>
                  )}

                  {!queryError && queryResult === null && !queryRunning && (
                    <div className="bg-[#0a0d1f] border border-[#26293c] rounded p-8 text-center text-[#8d909b] text-xs">
                      Enter a query or command above and click <span className="text-[#b0c9ff]">Run Query</span> to view engine results.
                    </div>
                  )}
                </div>
              </div>
            </div>
          </div>

          {/* NoSQL Data Browser Panel (browse KV pairs, documents, collections, etc.) */}
          <div
            data-testid="nosql-data-browser"
            className="bg-[#171a2d] border border-[#26293c] rounded p-3 flex flex-col gap-3"
          >
            <div className="flex flex-wrap items-center justify-between gap-2 border-b border-[#26293c] pb-2">
              <div className="flex items-center gap-2">
                <span className="material-symbols-outlined text-[16px] text-[#8cd6cb]">manage_search</span>
                <span className="font-bold text-xs text-[#dfe1fa]">
                  {`NoSQL Data Browser (${activeEngine.toUpperCase()})`}
                </span>
                <span className="text-[10px] text-[#8d909b]">
                  {activeEngine === 'redis'
                    ? 'Browse key-value pairs, hashes, lists, sets & TTLs'
                    : activeEngine === 'mongodb'
                    ? 'Browse collections & JSON documents'
                    : activeEngine === 'couchdb'
                    ? 'Browse CouchDB databases & document revisions'
                    : 'Scan RocksDB key-value entries'}
                </span>
              </div>

              <div className="flex flex-wrap items-center gap-2">
                <label className="flex items-center gap-1 text-[11px] text-[#8d909b]">
                  <span>{activeEngine === 'rocksdb' ? 'Path:' : 'DB / Keyspace:'}</span>
                  <input
                    type="text"
                    aria-label="Browser Target Database"
                    value={browserDb}
                    onChange={e => setBrowserDb(e.target.value)}
                    placeholder={defaultDbForNoSQLEngine(activeEngine)}
                    className="bg-[#0a0d1f] border border-[#26293c] rounded px-2 py-1 text-[11px] text-[#dfe1fa] w-28"
                  />
                </label>

                {(activeEngine === 'redis' || activeEngine === 'rocksdb') && (
                  <label className="flex items-center gap-1 text-[11px] text-[#8d909b]">
                    <span>{activeEngine === 'redis' ? 'Pattern:' : 'Prefix:'}</span>
                    <input
                      type="text"
                      aria-label="Key Pattern"
                      value={browserPattern}
                      onChange={e => setBrowserPattern(e.target.value)}
                      placeholder="*"
                      className="bg-[#0a0d1f] border border-[#26293c] rounded px-2 py-1 text-[11px] text-[#dfe1fa] w-24"
                    />
                  </label>
                )}

                <label className="flex items-center gap-1 text-[11px] text-[#8d909b]">
                  <span>Limit:</span>
                  <select
                    aria-label="Browser Limit"
                    value={browserLimit}
                    onChange={e => setBrowserLimit(Number(e.target.value) || 50)}
                    className="bg-[#0a0d1f] border border-[#26293c] rounded px-1.5 py-1 text-[11px] text-[#dfe1fa]"
                  >
                    <option value={50}>50</option>
                    <option value={100}>100</option>
                    <option value={250}>250</option>
                  </select>
                </label>

                {activeEngine === 'redis' && (
                  <button
                    type="button"
                    disabled={browserLoading}
                    onClick={handleBrowseRedisKeys}
                    className="px-2.5 py-1 rounded bg-[#21263d] hover:bg-[#313754] disabled:opacity-50 text-[#b0c9ff] border border-[#b0c9ff]/40 text-[11px] font-bold cursor-pointer"
                  >
                    Browse Keys
                  </button>
                )}

                {activeEngine === 'mongodb' && (
                  <button
                    type="button"
                    disabled={browserLoading}
                    onClick={handleLoadMongoCollections}
                    className="px-2.5 py-1 rounded bg-[#21263d] hover:bg-[#313754] disabled:opacity-50 text-[#b0c9ff] border border-[#b0c9ff]/40 text-[11px] font-bold cursor-pointer"
                  >
                    Load Collections
                  </button>
                )}

                {activeEngine === 'couchdb' && (
                  <button
                    type="button"
                    disabled={browserLoading}
                    onClick={handleBrowseCouchDocuments}
                    className="px-2.5 py-1 rounded bg-[#21263d] hover:bg-[#313754] disabled:opacity-50 text-[#b0c9ff] border border-[#b0c9ff]/40 text-[11px] font-bold cursor-pointer"
                  >
                    Browse Documents
                  </button>
                )}

                {activeEngine === 'rocksdb' && (
                  <button
                    type="button"
                    disabled={browserLoading}
                    onClick={handleScanRocksDBKeys}
                    className="px-2.5 py-1 rounded bg-[#21263d] hover:bg-[#313754] disabled:opacity-50 text-[#b0c9ff] border border-[#b0c9ff]/40 text-[11px] font-bold cursor-pointer"
                  >
                    Scan Keys
                  </button>
                )}
              </div>
            </div>

            {browserError && (
              <div className="bg-[#3a1b24] text-[#ffb4ab] p-2.5 rounded border border-[#ffb4ab]/30 text-xs">
                <span className="font-bold">Browser Error:</span> {browserError}
              </div>
            )}

            {/* Redis / Valkey Browser Content */}
            {activeEngine === 'redis' && (
              <div className="grid grid-cols-1 lg:grid-cols-12 gap-3">
                <div className="lg:col-span-4 bg-[#0a0d1f] border border-[#26293c] rounded p-2 flex flex-col gap-1 max-h-72 overflow-y-auto">
                  <div className="text-[10px] font-bold uppercase text-[#8d909b] px-1 pb-1 border-b border-[#26293c]">
                    Keys ({redisKeys.length})
                  </div>
                  {redisKeys.length > 0 ? (
                    redisKeys.map(k => (
                      <button
                        key={k}
                        type="button"
                        onClick={() => handleInspectRedisKey(k)}
                        className={`text-left px-2 py-1 rounded text-[11px] flex items-center justify-between cursor-pointer ${
                          selectedRedisKey === k
                            ? 'bg-[#21263d] text-[#b0c9ff] font-bold'
                            : 'text-[#c3c6d2] hover:bg-[#171a2d]'
                        }`}
                      >
                        <span className="truncate">{k}</span>
                        <span className="material-symbols-outlined text-[12px]">chevron_right</span>
                      </button>
                    ))
                  ) : (
                    <div className="text-center py-6 text-[#8d909b] text-[11px]">
                      Click <span className="text-[#b0c9ff]">Browse Keys</span> to scan keys in {browserDb || 'db0'}.
                    </div>
                  )}
                </div>

                <div className="lg:col-span-8 bg-[#0a0d1f] border border-[#26293c] rounded p-2 flex flex-col gap-2 max-h-72 overflow-y-auto">
                  {inspectedRedisKey ? (
                    <>
                      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-[#26293c] pb-1.5">
                        <span className="font-bold text-[#dfe1fa]">{inspectedRedisKey.key}</span>
                        <div className="flex items-center gap-2 text-[10px]">
                          <span className="px-1.5 py-0.5 rounded bg-[#1e2338] text-[#8cd6cb] uppercase font-bold">
                            TYPE: {inspectedRedisKey.type}
                          </span>
                          <span className="px-1.5 py-0.5 rounded bg-[#1e2338] text-[#b0c9ff]">
                            TTL: {inspectedRedisKey.ttl}
                          </span>
                        </div>
                      </div>
                      <table className="w-full text-left border-collapse text-[11px]">
                        <thead>
                          <tr className="border-b border-[#26293c] bg-[#131627] text-[#b0c9ff]">
                            <th className="p-1.5 w-1/3">Field / Index</th>
                            <th className="p-1.5">Value</th>
                          </tr>
                        </thead>
                        <tbody className="divide-y divide-[#26293c]/50">
                          {inspectedRedisKey.entries.map((entry, idx) => (
                            <tr key={idx} className="hover:bg-[#171a2d]/60">
                              <td className="p-1.5 font-semibold text-[#8cd6cb]">{entry.field}</td>
                              <td className="p-1.5 text-[#dfe1fa] break-all">{entry.value}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </>
                  ) : (
                    <div className="py-8 text-center text-[#8d909b]">
                      Select a Redis key on the left to inspect its type, TTL, and value.
                    </div>
                  )}
                </div>
              </div>
            )}

            {/* MongoDB Browser Content */}
            {activeEngine === 'mongodb' && (
              <div className="grid grid-cols-1 lg:grid-cols-12 gap-3">
                <div className="lg:col-span-3 bg-[#0a0d1f] border border-[#26293c] rounded p-2 flex flex-col gap-1 max-h-72 overflow-y-auto">
                  <div className="text-[10px] font-bold uppercase text-[#8d909b] px-1 pb-1 border-b border-[#26293c]">
                    Collections ({mongoCollections.length})
                  </div>
                  {mongoCollections.length > 0 ? (
                    mongoCollections.map(col => (
                      <button
                        key={col}
                        type="button"
                        onClick={() => handleBrowseMongoCollection(col)}
                        className={`text-left px-2 py-1 rounded text-[11px] flex items-center justify-between cursor-pointer ${
                          selectedMongoCollection === col
                            ? 'bg-[#21263d] text-[#b0c9ff] font-bold'
                            : 'text-[#c3c6d2] hover:bg-[#171a2d]'
                        }`}
                      >
                        <span className="truncate">{col}</span>
                        <span className="material-symbols-outlined text-[12px]">chevron_right</span>
                      </button>
                    ))
                  ) : (
                    <div className="text-center py-6 text-[#8d909b] text-[11px]">
                      Click <span className="text-[#b0c9ff]">Load Collections</span> to list collections.
                    </div>
                  )}
                </div>
                <div className="lg:col-span-9 bg-[#0a0d1f] border border-[#26293c] rounded p-2 max-h-72 overflow-y-auto">
                  {mongoDocuments.length > 0 ? (
                    <div className="flex flex-col gap-2">
                      {mongoDocuments.map((doc, idx) => (
                        <pre
                          key={idx}
                          className="bg-[#131627] border border-[#26293c] rounded p-2 text-[11px] text-[#8cd6cb] whitespace-pre-wrap"
                        >
                          {JSON.stringify(doc, null, 2)}
                        </pre>
                      ))}
                    </div>
                  ) : (
                    <div className="py-8 text-center text-[#8d909b]">
                      Select a collection on the left to inspect its documents.
                    </div>
                  )}
                </div>
              </div>
            )}

            {/* CouchDB Browser Content */}
            {activeEngine === 'couchdb' && (
              <div className="bg-[#0a0d1f] border border-[#26293c] rounded p-2 max-h-72 overflow-y-auto">
                {couchDocs.length > 0 ? (
                  <div className="flex flex-col gap-2">
                    {couchDocs.map((row, idx) => (
                      <div key={row.id || idx} className="bg-[#131627] border border-[#26293c] rounded p-2">
                        <div className="flex items-center justify-between text-[11px] border-b border-[#26293c] pb-1 mb-1">
                          <span className="font-bold text-[#b0c9ff]">_id: {row.id || row._id || idx}</span>
                          {row.value?.rev && <span className="text-[#8d909b]">_rev: {row.value.rev}</span>}
                        </div>
                        <pre className="text-[11px] text-[#8cd6cb] whitespace-pre-wrap">
                          {JSON.stringify(row.doc || row, null, 2)}
                        </pre>
                      </div>
                    ))}
                  </div>
                ) : (
                  <div className="py-6 text-center text-[#8d909b]">
                    Click <span className="text-[#b0c9ff]">Browse Documents</span> to inspect CouchDB documents.
                  </div>
                )}
              </div>
            )}

            {/* RocksDB Browser Content */}
            {activeEngine === 'rocksdb' && (
              <div className="bg-[#0a0d1f] border border-[#26293c] rounded p-2 max-h-72 overflow-y-auto">
                {rocksEntries.length > 0 ? (
                  <table className="w-full text-left border-collapse text-[11px]">
                    <thead>
                      <tr className="border-b border-[#26293c] bg-[#131627] text-[#b0c9ff]">
                        <th className="p-1.5 w-1/3">Key</th>
                        <th className="p-1.5">Value</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-[#26293c]/50">
                      {rocksEntries.map((entry, idx) => (
                        <tr key={idx} className="hover:bg-[#171a2d]/60">
                          <td className="p-1.5 font-semibold text-[#8cd6cb] break-all">{entry.key}</td>
                          <td className="p-1.5 text-[#dfe1fa] break-all">{entry.value}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                ) : (
                  <div className="py-6 text-center text-[#8d909b]">
                    Click <span className="text-[#b0c9ff]">Scan Keys</span> to browse key-value entries in RocksDB.
                  </div>
                )}
              </div>
            )}
          </div>
        </>
      )}
    </div>
  );
};

function escapeShell(arg: string): string {
  return `'${arg.replace(/'/g, `'\\''`)}'`;
}
