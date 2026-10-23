import React, { useState, useEffect, useMemo } from 'react';
import type { NodeItem } from '../types/index.ts';
import {
  PluginActionForm,
  type ActionSpec,
} from '../plugins/kit/index.ts';

interface SQLManagementViewProps {
  node: NodeItem;
  pluginId?: string;
  initialData?: SQLReadData;
  initialLoading?: boolean;
  initialDumpModalOpen?: boolean;
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
}

interface SQLReadData {
  engines?: EngineStatus[];
  count?: number;
}

export interface QueryResultTable {
  columns: string[];
  rows: (string | null)[][];
  rowCount: number;
  rawText?: string;
  isTabular: boolean;
}

export interface SQLDumpOptions {
  engine: string;
  database: string;
  format: string;
  scope: 'full' | 'schema_only' | 'data_only';
  clean: boolean;
  compress: boolean;
  outputPath?: string;
  preview?: boolean;
  overrides?: Record<string, string | undefined>;
}

export function resolveSQLEngine(pluginId?: string, engines?: EngineStatus[]): string {
  if (pluginId && pluginId.includes(':')) {
    const raw = pluginId.split(':')[1]?.toLowerCase().trim();
    if (raw === 'postgres' || raw === 'postgresql') return 'postgres';
    if (raw === 'mariadb' || raw === 'mysql') return 'mariadb';
    if (raw === 'mssql' || raw === 'sqlserver') return 'mssql';
  }
  if (engines && engines.length > 0) {
    const active = engines.find(e => e.status === 'active') || engines[0];
    if (active?.engine === 'mysql') return 'mariadb';
    if (active?.engine) return active.engine;
  }
  return 'postgres';
}

export function defaultPortForSQLEngine(eng: string): string {
  if (eng === 'mariadb' || eng === 'mysql') return '3306';
  if (eng === 'mssql') return '1433';
  return '5432';
}

export function defaultUserForSQLEngine(eng: string): string {
  if (eng === 'mariadb' || eng === 'mysql') return 'root';
  if (eng === 'mssql') return 'sa';
  return 'postgres';
}

export function defaultDbForSQLEngine(eng: string): string {
  if (eng === 'mariadb' || eng === 'mysql') return 'mysql';
  if (eng === 'mssql') return 'master';
  return 'postgres';
}

export function suggestSQLDumpPath(
  engine: string,
  database: string,
  format: string,
  compress: boolean
): string {
  const safeDb = (database || defaultDbForSQLEngine(engine)).replace(/[^a-zA-Z0-9_.-]/g, '_');
  if (engine === 'mssql') {
    return `/tmp/${safeDb}_dump.bak`;
  }
  if (engine === 'postgres') {
    if (format === 'custom') return `/tmp/${safeDb}_dump.dump`;
    if (format === 'directory') return `/tmp/${safeDb}_dump_dir`;
    if (format === 'tar') return `/tmp/${safeDb}_dump.tar`;
  }
  return `/tmp/${safeDb}_dump.sql${compress ? '.gz' : ''}`;
}

export function buildSQLClientCommand(
  engine: string,
  sql: string,
  targetDatabase: string,
  overrides: Record<string, string | undefined> = {},
  tabular = true
): string {
  const db =
    targetDatabase ||
    overrides[`${engine}_database`] ||
    (engine === 'mariadb' ? overrides.mysql_database : '') ||
    (engine === 'postgres' ? 'postgres' : '');

  if (engine === 'postgres') {
    const sock = overrides.postgres_socket?.trim();
    let hostArg = '';
    if (sock) {
      const idx = sock.lastIndexOf('/');
      const sockDir = idx > 0 && sock.slice(idx + 1).includes('.s.PGSQL.') ? sock.slice(0, idx) : sock;
      hostArg = `-h ${escapeShell(sockDir)} `;
    } else if (overrides.postgres_host) {
      hostArg = `-h ${escapeShell(overrides.postgres_host)} `;
    }
    const port = overrides.postgres_port ? `-p ${overrides.postgres_port} ` : '';
    const user = overrides.postgres_user ? `-U ${escapeShell(overrides.postgres_user)} ` : '';
    const dbArg = db ? `-d ${escapeShell(db)} ` : '';
    const pwdEnv = overrides.postgres_password
      ? `PGPASSWORD=${escapeShell(overrides.postgres_password)} `
      : '';
    const fmtFlags = tabular ? `-P null='NULL'` : `-t -A`;
    return `${pwdEnv}psql ${hostArg}${port}${user}${dbArg}${fmtFlags} -c ${escapeShell(sql)}`;
  }

  if (engine === 'mariadb' || engine === 'mysql') {
    const sock = (overrides.mariadb_socket || overrides.mysql_socket || '').trim();
    const host = overrides.mariadb_host || overrides.mysql_host || '';
    const portVal = overrides.mariadb_port || overrides.mysql_port || '';
    const userVal = overrides.mariadb_user || overrides.mysql_user || '';
    const pwdVal = overrides.mariadb_password || overrides.mysql_password || '';

    const connArg = sock
      ? `-S ${escapeShell(sock)} `
      : host
      ? `-h ${escapeShell(host)} `
      : '';
    const port = portVal ? `-P ${portVal} ` : '';
    const user = userVal ? `-u ${escapeShell(userVal)} ` : '';
    const pwd = pwdVal ? `-p${escapeShell(pwdVal)} ` : '';
    const dbArg = db ? `-D ${escapeShell(db)} ` : '';
    const fmtFlags = tabular ? '-t' : '-s -N';
    return `mysql ${connArg}${port}${user}${pwd}${dbArg}${fmtFlags} -e ${escapeShell(sql)}`;
  }

  if (engine === 'mssql') {
    const sock = overrides.mssql_socket?.trim();
    let srv = 'localhost';
    if (sock) {
      srv = sock;
    } else if (overrides.mssql_host) {
      srv = overrides.mssql_port
        ? `${overrides.mssql_host},${overrides.mssql_port}`
        : overrides.mssql_host;
    } else if (overrides.mssql_port && overrides.mssql_port !== '1433') {
      srv = `localhost,${overrides.mssql_port}`;
    }
    const user = overrides.mssql_user ? `-U ${escapeShell(overrides.mssql_user)} ` : '';
    const pwd = overrides.mssql_password ? `-P ${escapeShell(overrides.mssql_password)} ` : '';
    const dbArg = db ? `-d ${escapeShell(db)} ` : '';
    const fmtFlags = tabular ? '-W ' : '-h -1 -W ';
    return `sqlcmd -S ${escapeShell(srv)} ${user}${pwd}${dbArg}${fmtFlags}-Q ${escapeShell(sql)}`;
  }

  return '';
}

export function buildSQLDumpCommand(opts: SQLDumpOptions): string {
  const overrides = opts.overrides || {};
  const engine = opts.engine;
  const db =
    opts.database ||
    overrides[`${engine}_database`] ||
    defaultDbForSQLEngine(engine);
  const outPath =
    opts.outputPath ||
    suggestSQLDumpPath(engine, db, opts.format, opts.compress);

  if (engine === 'postgres') {
    const flags: string[] = [];
    const sock = overrides.postgres_socket?.trim();
    if (sock) {
      const idx = sock.lastIndexOf('/');
      const sockDir = idx > 0 && sock.slice(idx + 1).includes('.s.PGSQL.') ? sock.slice(0, idx) : sock;
      flags.push(`-h ${escapeShell(sockDir)}`);
    } else if (overrides.postgres_host) {
      flags.push(`-h ${escapeShell(overrides.postgres_host)}`);
    }
    if (overrides.postgres_port) flags.push(`-p ${overrides.postgres_port}`);
    if (overrides.postgres_user) flags.push(`-U ${escapeShell(overrides.postgres_user)}`);

    if (opts.format === 'custom') flags.push('-Fc');
    else if (opts.format === 'directory') flags.push('-Fd');
    else if (opts.format === 'tar') flags.push('-Ft');
    else flags.push('-Fp');

    if (opts.scope === 'schema_only') flags.push('--schema-only');
    else if (opts.scope === 'data_only') flags.push('--data-only');
    if (opts.clean) flags.push('--clean');

    const pwdEnv = overrides.postgres_password
      ? `PGPASSWORD=${escapeShell(overrides.postgres_password)} `
      : '';
    const flagStr = flags.length > 0 ? `${flags.join(' ')} ` : '';

    if (opts.preview) {
      return `${pwdEnv}pg_dump ${flagStr}${escapeShell(db)}`;
    }
    if (opts.compress && (!opts.format || opts.format === 'plain')) {
      return `${pwdEnv}pg_dump ${flagStr}${escapeShell(db)} | gzip > ${escapeShell(outPath)}`;
    }
    return `${pwdEnv}pg_dump ${flagStr}-f ${escapeShell(outPath)} ${escapeShell(db)}`;
  }

  if (engine === 'mariadb' || engine === 'mysql') {
    const flags: string[] = [];
    const sock = (overrides.mariadb_socket || overrides.mysql_socket || '').trim();
    const host = overrides.mariadb_host || overrides.mysql_host || '';
    const portVal = overrides.mariadb_port || overrides.mysql_port || '';
    const userVal = overrides.mariadb_user || overrides.mysql_user || '';
    const pwdVal = overrides.mariadb_password || overrides.mysql_password || '';

    if (sock) flags.push(`-S ${escapeShell(sock)}`);
    else if (host) flags.push(`-h ${escapeShell(host)}`);
    if (portVal) flags.push(`-P ${portVal}`);
    if (userVal) flags.push(`-u ${escapeShell(userVal)}`);
    if (pwdVal) flags.push(`-p${escapeShell(pwdVal)}`);
    if (opts.format === 'single-transaction') flags.push('--single-transaction');
    if (opts.scope === 'schema_only') flags.push('--no-data');
    else if (opts.scope === 'data_only') flags.push('--no-create-info');
    if (opts.clean) flags.push('--add-drop-table');

    const flagStr = flags.length > 0 ? `${flags.join(' ')} ` : '';
    const baseDump = `(mariadb-dump ${flagStr}${escapeShell(db)} || mysqldump ${flagStr}${escapeShell(db)})`;
    if (opts.preview) {
      return baseDump;
    }
    if (opts.compress) {
      return `${baseDump} | gzip > ${escapeShell(outPath)}`;
    }
    return `${baseDump} > ${escapeShell(outPath)}`;
  }

  if (engine === 'mssql') {
    const backupSql = `BACKUP DATABASE [${db}] TO DISK = N'${outPath}' WITH FORMAT, INIT;`;
    return buildSQLClientCommand('mssql', backupSql, db, overrides, false);
  }

  return '';
}

export function buildSQLTableListQuery(engine: string): string {
  if (engine === 'mariadb' || engine === 'mysql') {
    return 'SHOW TABLES;';
  }
  if (engine === 'mssql') {
    return "SELECT TABLE_SCHEMA + '.' + TABLE_NAME FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_TYPE = 'BASE TABLE' ORDER BY TABLE_SCHEMA, TABLE_NAME;";
  }
  return "SELECT table_schema || '.' || table_name FROM information_schema.tables WHERE table_type = 'BASE TABLE' AND table_schema NOT IN ('pg_catalog', 'information_schema') ORDER BY table_schema, table_name;";
}

export function buildSQLBrowseRowsQuery(
  engine: string,
  tableName: string,
  limit: number,
  offset: number
): string {
  if (engine === 'mssql') {
    return `SELECT * FROM ${tableName} ORDER BY (SELECT NULL) OFFSET ${offset} ROWS FETCH NEXT ${limit} ROWS ONLY;`;
  }
  return `SELECT * FROM ${tableName} LIMIT ${limit} OFFSET ${offset};`;
}

export const SQLManagementView: React.FC<SQLManagementViewProps> = ({
  node,
  pluginId,
  initialData,
  initialLoading,
  initialDumpModalOpen = false,
}) => {
  const [data, setData] = useState<SQLReadData | null>(initialData || null);
  const [actions, setActions] = useState<ActionSpec[]>([]);
  const [loading, setLoading] = useState(initialLoading ?? (initialData ? false : true));
  const [error, setError] = useState<string | null>(null);
  const [activeEngine, setActiveEngine] = useState<string>(() =>
    resolveSQLEngine(pluginId, initialData?.engines)
  );
  const [activeAction, setActiveAction] = useState<ActionSpec | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);

  // Expanded Connection Config state: Username, Password, Default Database, Port, Socket
  const [configUser, setConfigUser] = useState<string>('');
  const [configPassword, setConfigPassword] = useState<string>('');
  const [configDatabase, setConfigDatabase] = useState<string>('');
  const [configPort, setConfigPort] = useState<string>('');
  const [configSocket, setConfigSocket] = useState<string>('');
  const [savingConfig, setSavingConfig] = useState<boolean>(false);

  // Raw query runner state
  const [queryText, setQueryText] = useState<string>('SELECT 1;');
  const [queryDatabase, setQueryDatabase] = useState<string>(() => {
    const eng = resolveSQLEngine(pluginId, initialData?.engines);
    const ovDb = node?.pluginOverrides?.[`${eng}_database`];
    if (ovDb) return ovDb;
    const engEntry = initialData?.engines?.find(e => e.engine === eng) || initialData?.engines?.[0];
    return engEntry?.databases?.[0] || '';
  });
  const [queryRunning, setQueryRunning] = useState<boolean>(false);
  const [queryResult, setQueryResult] = useState<QueryResultTable | null>(null);
  const [queryError, setQueryError] = useState<string | null>(null);

  // Destructive statement confirmation modal
  const [confirmModalOpen, setConfirmModalOpen] = useState(false);
  const [pendingDangerousQuery, setPendingDangerousQuery] = useState<string | null>(null);

  // Sorting state for query workbench table view
  const [sortColIndex, setSortColIndex] = useState<number | null>(null);
  const [sortAsc, setSortAsc] = useState<boolean>(true);

  // Dump Database modal state
  const [dumpModalOpen, setDumpModalOpen] = useState<boolean>(initialDumpModalOpen);
  const [dumpDatabase, setDumpDatabase] = useState<string>('');
  const [dumpFormat, setDumpFormat] = useState<string>('plain');
  const [dumpScope, setDumpScope] = useState<'full' | 'schema_only' | 'data_only'>('full');
  const [dumpClean, setDumpClean] = useState<boolean>(false);
  const [dumpCompress, setDumpCompress] = useState<boolean>(false);
  const [dumpOutputPath, setDumpOutputPath] = useState<string>('');
  const [dumpPathDirty, setDumpPathDirty] = useState<boolean>(false);
  const [dumpRunning, setDumpRunning] = useState<boolean>(false);
  const [dumpOutput, setDumpOutput] = useState<string | null>(null);
  const [dumpError, setDumpError] = useState<string | null>(null);

  // SQL Data Browser state
  const [browserDatabase, setBrowserDatabase] = useState<string>('');
  const [browserTables, setBrowserTables] = useState<string[]>([]);
  const [browserLoadingTables, setBrowserLoadingTables] = useState<boolean>(false);
  const [browserSelectedTable, setBrowserSelectedTable] = useState<string | null>(null);
  const [browserRowsResult, setBrowserRowsResult] = useState<QueryResultTable | null>(null);
  const [browserLoadingRows, setBrowserLoadingRows] = useState<boolean>(false);
  const [browserError, setBrowserError] = useState<string | null>(null);
  const [browserLimit, setBrowserLimit] = useState<number>(25);
  const [browserOffset, setBrowserOffset] = useState<number>(0);
  const [browserFilter, setBrowserFilter] = useState<string>('');
  const [browserSortCol, setBrowserSortCol] = useState<number | null>(null);
  const [browserSortAsc, setBrowserSortAsc] = useState<boolean>(true);

  // Sync activeEngine whenever pluginId or data.engines changes
  useEffect(() => {
    const resolved = resolveSQLEngine(pluginId, data?.engines);
    setActiveEngine(resolved);
    const engEntry = data?.engines?.find(e => e.engine === resolved);
    const ovDb =
      node?.pluginOverrides?.[`${resolved}_database`] ||
      (resolved === 'mariadb' ? node?.pluginOverrides?.mysql_database : '') ||
      '';
    if (ovDb) {
      setQueryDatabase(ovDb);
      setBrowserDatabase(ovDb);
    } else if (engEntry?.databases && engEntry.databases.length > 0) {
      setQueryDatabase(engEntry.databases[0]);
      setBrowserDatabase(engEntry.databases[0]);
    }
    setDumpFormat(resolved === 'mariadb' ? 'sql' : resolved === 'mssql' ? 'bak' : 'plain');
    setDumpPathDirty(false);
    setBrowserSelectedTable(null);
    setBrowserRowsResult(null);
  }, [pluginId, data?.engines, node?.pluginOverrides]);

  const fetchData = async () => {
    if (!node?.id) return;
    setLoading(true);
    setError(null);
    try {
      const metaRes = await fetch('/api/plugins');
      if (metaRes.ok) {
        const summaries = await metaRes.json();
        const found = summaries.find((p: any) => p.id === 'database/sql');
        if (found?.actions) {
          setActions(found.actions.filter((a: ActionSpec) => a.name !== 'database_dump'));
        }
      }
      const res = await fetch(`/api/nodes/${encodeURIComponent(node.id)}/plugins/database/sql`);
      if (!res.ok) {
        const text = await res.text();
        setError(`HTTP ${res.status}: ${text || res.statusText}`);
      } else {
        const readData = await res.json();
        setData(readData);
        const resolved = resolveSQLEngine(pluginId, readData.engines);
        setActiveEngine(resolved);
        const engEntry = readData.engines?.find((e: EngineStatus) => e.engine === resolved);
        const ovDb =
          node?.pluginOverrides?.[`${resolved}_database`] ||
          (resolved === 'mariadb' ? node?.pluginOverrides?.mysql_database : '') ||
          '';
        const initialDb = ovDb || engEntry?.databases?.[0] || '';
        if (initialDb) {
          setQueryDatabase(initialDb);
          setBrowserDatabase(initialDb);
        }
      }
    } catch (err: any) {
      setError(err?.message || 'Failed to fetch SQL database status');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [node?.id]);

  const currentEngineData = data?.engines?.find(e => e.engine === activeEngine) || {
    engine: activeEngine,
    status: 'inactive',
    databases: [],
    users: [],
    metrics: {},
  };

  useEffect(() => {
    const overrides = node?.pluginOverrides || {};
    const metricPort = currentEngineData.metrics?.port ? String(currentEngineData.metrics.port) : '';
    const metricSocket = currentEngineData.metrics?.socket ? String(currentEngineData.metrics.socket) : '';
    const metricUser = currentEngineData.metrics?.user ? String(currentEngineData.metrics.user) : '';
    const metricDb = currentEngineData.metrics?.database ? String(currentEngineData.metrics.database) : '';

    const ovPort = overrides[`${activeEngine}_port`] || (activeEngine === 'mariadb' ? overrides.mysql_port : '') || '';
    const ovSocket = overrides[`${activeEngine}_socket`] || (activeEngine === 'mariadb' ? overrides.mysql_socket : '') || '';
    const ovUser = overrides[`${activeEngine}_user`] || (activeEngine === 'mariadb' ? overrides.mysql_user : '') || '';
    const ovPassword = overrides[`${activeEngine}_password`] || (activeEngine === 'mariadb' ? overrides.mysql_password : '') || '';
    const ovDb = overrides[`${activeEngine}_database`] || (activeEngine === 'mariadb' ? overrides.mysql_database : '') || '';

    setConfigPort(ovPort || metricPort || defaultPortForSQLEngine(activeEngine));
    setConfigSocket(ovSocket || metricSocket || '');
    setConfigUser(ovUser || metricUser || '');
    setConfigPassword(ovPassword || '');
    setConfigDatabase(ovDb || metricDb || '');
  }, [
    activeEngine,
    node?.pluginOverrides,
    currentEngineData.metrics?.port,
    currentEngineData.metrics?.socket,
    currentEngineData.metrics?.user,
    currentEngineData.metrics?.database,
  ]);

  // Keep dump modal defaults synced
  useEffect(() => {
    const defaultTarget =
      queryDatabase ||
      configDatabase ||
      currentEngineData.databases?.[0] ||
      defaultDbForSQLEngine(activeEngine);
    if (!dumpDatabase) {
      setDumpDatabase(defaultTarget);
    }
    if (!dumpPathDirty) {
      setDumpOutputPath(suggestSQLDumpPath(activeEngine, dumpDatabase || defaultTarget, dumpFormat, dumpCompress));
    }
  }, [activeEngine, queryDatabase, configDatabase, currentEngineData.databases, dumpDatabase, dumpFormat, dumpCompress, dumpPathDirty]);

  // Keep browserDatabase synced when queryDatabase changes
  useEffect(() => {
    if (!browserDatabase && (queryDatabase || configDatabase || currentEngineData.databases?.[0])) {
      setBrowserDatabase(queryDatabase || configDatabase || currentEngineData.databases?.[0] || '');
    }
  }, [queryDatabase, configDatabase, currentEngineData.databases, browserDatabase]);

  const effectiveOverrides = useMemo((): Record<string, string | undefined> => {
    return {
      ...(node?.pluginOverrides || {}),
      [`${activeEngine}_port`]: configPort.trim() || defaultPortForSQLEngine(activeEngine),
      [`${activeEngine}_socket`]: configSocket.trim() || undefined,
      [`${activeEngine}_user`]: configUser.trim() || undefined,
      [`${activeEngine}_password`]: configPassword || undefined,
      [`${activeEngine}_database`]: configDatabase.trim() || undefined,
    };
  }, [node?.pluginOverrides, activeEngine, configPort, configSocket, configUser, configPassword, configDatabase]);

  const handleSaveConnectionConfig = async () => {
    if (!node?.id) return;
    setSavingConfig(true);
    setError(null);
    setNotice(null);
    try {
      const updatedOverrides: NonNullable<NodeItem['pluginOverrides']> = {
        ...(node.pluginOverrides || {}),
        [`${activeEngine}_port`]: configPort.trim() || defaultPortForSQLEngine(activeEngine),
      };

      const setOrDelete = (key: string, val: string) => {
        if (val) {
          updatedOverrides[key] = val;
        } else {
          delete updatedOverrides[key];
        }
      };

      setOrDelete(`${activeEngine}_socket`, configSocket.trim());
      setOrDelete(`${activeEngine}_user`, configUser.trim());
      setOrDelete(`${activeEngine}_password`, configPassword);
      setOrDelete(`${activeEngine}_database`, configDatabase.trim());

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
        setBrowserDatabase(configDatabase.trim());
      }
      await fetch(`/api/nodes/${encodeURIComponent(node.id)}/plugins/refresh`, { method: 'POST' });
      setNotice(
        `Saved ${activeEngine.toUpperCase()} connection settings (port ${updatedOverrides[`${activeEngine}_port`]}) and re-probed host.`
      );
      await fetchData();
    } catch (err: any) {
      setError(err?.message || 'Failed to save connection config');
    } finally {
      setSavingConfig(false);
    }
  };

  // Helper to detect destructive statements
  const isDestructive = (sql: string): boolean => {
    const clean = sql.trim().toUpperCase();
    if (/\bDROP\b/.test(clean) || /\bTRUNCATE\b/.test(clean)) {
      return true;
    }
    if (/\bDELETE\s+FROM\b/.test(clean) && !/\bWHERE\b/.test(clean)) {
      return true;
    }
    return false;
  };

  const executeQueryInternal = async (sql: string) => {
    setQueryRunning(true);
    setQueryError(null);
    setQueryResult(null);
    setSortColIndex(null);

    try {
      const targetDb =
        queryDatabase ||
        configDatabase ||
        (activeEngine === 'postgres' ? 'postgres' : '');
      const cmd = buildSQLClientCommand(activeEngine, sql, targetDb, effectiveOverrides, true);

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
        const parsed = parseTabularOutput(rawOutput);
        setQueryResult(parsed);
      }
    } catch (err: any) {
      setQueryError(err?.message || 'Query execution failed');
    } finally {
      setQueryRunning(false);
    }
  };

  const handleRunQuery = () => {
    if (!queryText.trim()) return;
    if (isDestructive(queryText)) {
      setPendingDangerousQuery(queryText);
      setConfirmModalOpen(true);
      return;
    }
    executeQueryInternal(queryText);
  };

  const confirmDangerousQuery = () => {
    if (pendingDangerousQuery) {
      executeQueryInternal(pendingDangerousQuery);
    }
    setConfirmModalOpen(false);
    setPendingDangerousQuery(null);
  };

  const handleActionSubmit = async (values: Record<string, any>) => {
    if (!activeAction) return;
    setSubmitting(true);
    setNotice(null);
    try {
      const compileRes = await fetch(
        `/api/nodes/${encodeURIComponent(node.id)}/plugins/database/sql/actions/${encodeURIComponent(activeAction.name)}`,
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
        const stageErr = await stageRes.text();
        throw new Error(`Failed to stage Ansible task: ${stageErr || stageRes.statusText}`);
      }

      setNotice(`Task staged to Ansible queue: ${taskSpec.name}`);
      setActiveAction(null);
    } catch (err: any) {
      setError(err?.message || 'Failed to stage database action');
    } finally {
      setSubmitting(false);
    }
  };

  // Dump Database handlers
  const handleOpenDumpModal = () => {
    const defaultDb =
      queryDatabase ||
      configDatabase ||
      currentEngineData.databases?.[0] ||
      defaultDbForSQLEngine(activeEngine);
    const fmt = activeEngine === 'mariadb' ? 'sql' : activeEngine === 'mssql' ? 'bak' : 'plain';
    setDumpDatabase(defaultDb);
    setDumpFormat(fmt);
    setDumpScope('full');
    setDumpClean(false);
    setDumpCompress(false);
    setDumpPathDirty(false);
    setDumpOutputPath(suggestSQLDumpPath(activeEngine, defaultDb, fmt, false));
    setDumpOutput(null);
    setDumpError(null);
    setDumpModalOpen(true);
  };

  const handleRunDumpDirect = async (previewOnly = false) => {
    const targetDb = dumpDatabase.trim() || defaultDbForSQLEngine(activeEngine);
    setDumpRunning(true);
    setDumpError(null);
    setDumpOutput(null);
    try {
      const cmd = buildSQLDumpCommand({
        engine: activeEngine,
        database: targetDb,
        format: dumpFormat,
        scope: dumpScope,
        clean: dumpClean,
        compress: dumpCompress,
        outputPath: dumpOutputPath.trim() || suggestSQLDumpPath(activeEngine, targetDb, dumpFormat, dumpCompress),
        preview: previewOnly,
        overrides: effectiveOverrides,
      });

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
        setDumpError(json.stderr || json.stdout || `Dump exited with status ${json.exitCode}`);
      } else {
        const outFile =
          dumpOutputPath.trim() || suggestSQLDumpPath(activeEngine, targetDb, dumpFormat, dumpCompress);
        setDumpOutput(
          previewOnly
            ? json.stdout || '-- Empty SQL dump output --'
            : json.stdout?.trim() || `Database '${targetDb}' dumped successfully to ${outFile} on ${node.name || node.id}.`
        );
      }
    } catch (err: any) {
      setDumpError(err?.message || 'Database dump failed');
    } finally {
      setDumpRunning(false);
    }
  };

  const handleStageDumpTask = async () => {
    const targetDb = dumpDatabase.trim() || defaultDbForSQLEngine(activeEngine);
    const outFile =
      dumpOutputPath.trim() || suggestSQLDumpPath(activeEngine, targetDb, dumpFormat, dumpCompress);
    setDumpRunning(true);
    setDumpError(null);
    try {
      const compileRes = await fetch(
        `/api/nodes/${encodeURIComponent(node.id)}/plugins/database/sql/actions/database_dump`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            options: {
              engine: activeEngine,
              database: targetDb,
              format: dumpFormat,
              scope: dumpScope,
              clean: dumpClean,
              compress: dumpCompress,
              output_path: outFile,
            },
          }),
        }
      );
      if (!compileRes.ok) {
        const errText = await compileRes.text();
        throw new Error(`Failed to compile dump action: ${errText || compileRes.statusText}`);
      }
      const taskSpec = await compileRes.json();

      const stageRes = await fetch('/api/queue/task', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          nodeId: node.id,
          task: taskSpec,
        }),
      });
      if (!stageRes.ok) {
        const stageErr = await stageRes.text();
        throw new Error(`Failed to stage dump task: ${stageErr || stageRes.statusText}`);
      }
      setNotice(`Task staged to Ansible queue: ${taskSpec.name}`);
      setDumpModalOpen(false);
    } catch (err: any) {
      setDumpError(err?.message || 'Failed to stage dump task');
    } finally {
      setDumpRunning(false);
    }
  };

  // SQL Data Browser handlers
  const handleLoadTables = async (dbOverride?: string) => {
    const targetDb =
      (dbOverride ?? browserDatabase).trim() ||
      queryDatabase ||
      configDatabase ||
      currentEngineData.databases?.[0] ||
      defaultDbForSQLEngine(activeEngine);
    setBrowserLoadingTables(true);
    setBrowserError(null);
    try {
      const sql = buildSQLTableListQuery(activeEngine);
      const cmd = buildSQLClientCommand(activeEngine, sql, targetDb, effectiveOverrides, false);
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
        setBrowserError(json.stderr || json.stdout || `Failed to list tables (exit ${json.exitCode})`);
        setBrowserTables([]);
      } else {
        const raw = String(json.stdout || '');
        const tables = raw
          .split('\n')
          .map(l => l.trim())
          .filter(l => l && !l.startsWith('(') && !/^[-+]+$/.test(l));
        setBrowserTables(tables);
      }
    } catch (err: any) {
      setBrowserError(err?.message || 'Failed to load tables');
    } finally {
      setBrowserLoadingTables(false);
    }
  };

  const handleBrowseTableRows = async (
    tableName: string,
    limitVal = browserLimit,
    offsetVal = browserOffset
  ) => {
    const targetDb =
      browserDatabase.trim() ||
      queryDatabase ||
      configDatabase ||
      currentEngineData.databases?.[0] ||
      defaultDbForSQLEngine(activeEngine);
    setBrowserSelectedTable(tableName);
    setBrowserLimit(limitVal);
    setBrowserOffset(offsetVal);
    setBrowserLoadingRows(true);
    setBrowserError(null);
    setBrowserSortCol(null);
    try {
      const sql = buildSQLBrowseRowsQuery(activeEngine, tableName, limitVal, offsetVal);
      const cmd = buildSQLClientCommand(activeEngine, sql, targetDb, effectiveOverrides, true);
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
        setBrowserError(json.stderr || json.stdout || `Failed to fetch table rows (exit ${json.exitCode})`);
        setBrowserRowsResult(null);
      } else {
        setBrowserRowsResult(parseTabularOutput(json.stdout || ''));
      }
    } catch (err: any) {
      setBrowserError(err?.message || 'Failed to load table rows');
    } finally {
      setBrowserLoadingRows(false);
    }
  };

  // Sorted rows calculation for Query Workbench
  const sortedRows = useMemo(() => {
    if (!queryResult || !queryResult.isTabular) return [];
    if (sortColIndex === null) return queryResult.rows;

    const copy = [...queryResult.rows];
    copy.sort((a, b) => {
      const valA = a[sortColIndex];
      const valB = b[sortColIndex];
      if (valA === null && valB === null) return 0;
      if (valA === null) return sortAsc ? 1 : -1;
      if (valB === null) return sortAsc ? -1 : 1;

      const numA = Number(valA);
      const numB = Number(valB);
      if (!isNaN(numA) && !isNaN(numB)) {
        return sortAsc ? numA - numB : numB - numA;
      }
      return sortAsc ? valA.localeCompare(valB) : valB.localeCompare(valA);
    });
    return copy;
  }, [queryResult, sortColIndex, sortAsc]);

  const toggleSort = (colIndex: number) => {
    if (sortColIndex === colIndex) {
      setSortAsc(!sortAsc);
    } else {
      setSortColIndex(colIndex);
      setSortAsc(true);
    }
  };

  // Filtered + sorted rows for Data Browser
  const browserDisplayRows = useMemo(() => {
    if (!browserRowsResult || !browserRowsResult.isTabular) return [];
    let rows = browserRowsResult.rows;
    const q = browserFilter.trim().toLowerCase();
    if (q) {
      rows = rows.filter(r => r.some(cell => (cell ?? 'NULL').toLowerCase().includes(q)));
    }
    if (browserSortCol === null) return rows;
    const copy = [...rows];
    copy.sort((a, b) => {
      const valA = a[browserSortCol];
      const valB = b[browserSortCol];
      if (valA === null && valB === null) return 0;
      if (valA === null) return browserSortAsc ? 1 : -1;
      if (valB === null) return browserSortAsc ? -1 : 1;
      const numA = Number(valA);
      const numB = Number(valB);
      if (!isNaN(numA) && !isNaN(numB)) {
        return browserSortAsc ? numA - numB : numB - numA;
      }
      return browserSortAsc ? valA.localeCompare(valB) : valB.localeCompare(valA);
    });
    return copy;
  }, [browserRowsResult, browserFilter, browserSortCol, browserSortAsc]);

  const toggleBrowserSort = (colIndex: number) => {
    if (browserSortCol === colIndex) {
      setBrowserSortAsc(!browserSortAsc);
    } else {
      setBrowserSortCol(colIndex);
      setBrowserSortAsc(true);
    }
  };

  return (
    <div className="flex flex-col gap-4 p-4 font-mono text-xs text-[#c3c6d2]">
      {/* Top Action Bar (duplicate title block and engine switcher tabs removed) */}
      <div className="flex flex-wrap items-center justify-end gap-2 border-b border-[#26293c] pb-3">
        <div className="flex flex-wrap items-center gap-2">
          {actions.map(act => (
            <button
              key={act.name}
              type="button"
              onClick={() => setActiveAction(activeAction?.name === act.name ? null : act)}
              className={`px-2.5 py-1 rounded border text-[11px] font-bold flex items-center gap-1 cursor-pointer transition-colors ${
                activeAction?.name === act.name
                  ? 'bg-[#b0c9ff] text-[#002e69] border-[#b0c9ff]'
                  : 'bg-[#1b1e31] hover:bg-[#262a42] text-[#c3c6d2] border-[#26293c]'
              }`}
            >
              <span className="material-symbols-outlined text-[13px]">
                {act.name.includes('create') ? 'add' : act.name.includes('drop') ? 'delete' : 'tune'}
              </span>
              {act.label}
            </button>
          ))}
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
          <button
            type="button"
            data-testid="sql-dump-database-btn"
            onClick={handleOpenDumpModal}
            className="px-2.5 py-1 rounded bg-[#21263d] hover:bg-[#313754] text-[#8cd6cb] border border-[#8cd6cb]/40 text-[11px] font-bold flex items-center gap-1 cursor-pointer"
          >
            <span className="material-symbols-outlined text-[13px]">download</span>
            Dump Database
          </button>
        </div>
      </div>

      {/* Notice & Error banners */}
      {notice && (
        <div className="bg-[#102d24] text-[#8cd6cb] p-2.5 rounded border border-[#8cd6cb]/30 flex items-center justify-between">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[16px]">check_circle</span>
            <span>{notice}</span>
          </div>
          <button type="button" onClick={() => setNotice(null)} className="cursor-pointer">
            <span className="material-symbols-outlined text-[14px]">close</span>
          </button>
        </div>
      )}
      {error && (
        <div className="bg-[#3a1b24] text-[#ffb4ab] p-2.5 rounded border border-[#ffb4ab]/30 flex items-center justify-between">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[16px]">error</span>
            <span>{error}</span>
          </div>
          <button type="button" onClick={() => setError(null)} className="cursor-pointer">
            <span className="material-symbols-outlined text-[14px]">close</span>
          </button>
        </div>
      )}

      {/* Action Drawer Form */}
      {activeAction && (
        <div className="bg-[#131627] border border-[#b0c9ff]/40 rounded p-3 mb-2">
          <div className="flex items-center justify-between mb-2">
            <span className="font-bold text-[#b0c9ff] text-xs flex items-center gap-1">
              <span className="material-symbols-outlined text-[14px]">tune</span>
              {activeAction.label}
            </span>
            <button
              type="button"
              onClick={() => setActiveAction(null)}
              className="text-[#8d909b] hover:text-[#dfe1fa] cursor-pointer"
            >
              <span className="material-symbols-outlined text-[14px]">close</span>
            </button>
          </div>
          <PluginActionForm
            action={activeAction}
            submitting={submitting}
            onSubmit={handleActionSubmit}
            onCancel={() => setActiveAction(null)}
          />
        </div>
      )}

      {/* Expanded Connection Config Bar (Username, Password, Default DB, TCP Port, Unix Socket) */}
      <div
        data-testid="sql-connection-config"
        className="bg-[#131627] border border-[#26293c] rounded p-2.5 flex flex-wrap items-center justify-between gap-2 text-[11px]"
      >
        <div className="flex items-center gap-2 text-[#dfe1fa] font-bold">
          <span className="material-symbols-outlined text-[15px] text-[#8cd6cb]">settings_ethernet</span>
          <span>{`Connection Config (${activeEngine.toUpperCase()}):`}</span>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <label className="flex items-center gap-1 text-[#8d909b]">
            <span>Username:</span>
            <input
              type="text"
              aria-label="Username"
              value={configUser}
              onChange={e => setConfigUser(e.target.value)}
              placeholder={defaultUserForSQLEngine(activeEngine)}
              className="bg-[#0a0d1f] border border-[#26293c] rounded px-2 py-0.5 text-[11px] text-[#dfe1fa] w-28"
            />
          </label>
          <label className="flex items-center gap-1 text-[#8d909b]">
            <span>Password:</span>
            <input
              type="password"
              aria-label="Password"
              value={configPassword}
              onChange={e => setConfigPassword(e.target.value)}
              placeholder="•••••••• (optional / vault)"
              className="bg-[#0a0d1f] border border-[#26293c] rounded px-2 py-0.5 text-[11px] text-[#dfe1fa] w-36"
            />
          </label>
          <label className="flex items-center gap-1 text-[#8d909b]">
            <span>Default Database:</span>
            <input
              type="text"
              aria-label="Default Database"
              value={configDatabase}
              onChange={e => setConfigDatabase(e.target.value)}
              placeholder={defaultDbForSQLEngine(activeEngine)}
              className="bg-[#0a0d1f] border border-[#26293c] rounded px-2 py-0.5 text-[11px] text-[#dfe1fa] w-28"
            />
          </label>
          <label className="flex items-center gap-1 text-[#8d909b]">
            <span>TCP Port:</span>
            <input
              type="text"
              aria-label="TCP Port"
              value={configPort}
              onChange={e => setConfigPort(e.target.value)}
              placeholder={defaultPortForSQLEngine(activeEngine)}
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
                activeEngine === 'postgres'
                  ? `/run/postgresql/.s.PGSQL.${configPort || '5432'}`
                  : activeEngine === 'mariadb'
                  ? '/run/mysqld/mysqld.sock'
                  : '/var/opt/mssql/.system/mssql.sock'
              }
              className="bg-[#0a0d1f] border border-[#26293c] rounded px-2 py-0.5 text-[11px] text-[#dfe1fa] w-48"
            />
          </label>
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

      {/* Main Content Layout */}
      <div className="grid grid-cols-1 lg:grid-cols-12 gap-4">
        {/* Left Column: Databases & Users */}
        <div className="lg:col-span-5 flex flex-col gap-4">
          {/* Databases Panel */}
          <div className="bg-[#171a2d] border border-[#26293c] rounded p-3 flex flex-col gap-2">
            <div className="flex items-center justify-between text-[#dfe1fa] font-bold border-b border-[#26293c] pb-2">
              <span className="flex items-center gap-1">
                <span className="material-symbols-outlined text-[15px] text-[#8cd6cb]">folder</span>
                Databases &amp; Schemas ({currentEngineData.databases?.length || 0})
              </span>
              <span className="text-[10px] text-[#8d909b] font-normal uppercase">
                {currentEngineData.status}
              </span>
            </div>

            {currentEngineData.databases && currentEngineData.databases.length > 0 ? (
              <div className="flex flex-col gap-1 max-h-48 overflow-y-auto">
                {currentEngineData.databases.map(db => (
                  <div
                    key={db}
                    onClick={() => {
                      setQueryDatabase(db);
                      setBrowserDatabase(db);
                    }}
                    className={`flex items-center justify-between p-1.5 rounded cursor-pointer transition-colors ${
                      queryDatabase === db ? 'bg-[#21263d] text-[#b0c9ff]' : 'hover:bg-[#1b1e31] text-[#c3c6d2]'
                    }`}
                  >
                    <span className="font-semibold">{db}</span>
                    <span className="text-[10px] text-[#8d909b]">
                      {queryDatabase === db ? 'selected' : 'use'}
                    </span>
                  </div>
                ))}
              </div>
            ) : (
              <div className="text-center py-4 text-[#8d909b] text-[11px]">
                No databases discovered for {activeEngine}.
              </div>
            )}
          </div>

          {/* Users & Roles Panel */}
          <div className="bg-[#171a2d] border border-[#26293c] rounded p-3 flex flex-col gap-2">
            <div className="flex items-center justify-between text-[#dfe1fa] font-bold border-b border-[#26293c] pb-2">
              <span className="flex items-center gap-1">
                <span className="material-symbols-outlined text-[15px] text-[#b0c9ff]">group</span>
                Users &amp; Roles ({currentEngineData.users?.length || 0})
              </span>
            </div>

            {currentEngineData.users && currentEngineData.users.length > 0 ? (
              <div className="flex flex-col gap-1 max-h-48 overflow-y-auto">
                {currentEngineData.users.map(u => (
                  <div
                    key={u.username}
                    className="flex items-center justify-between p-1.5 rounded bg-[#0a0d1f]/50 border border-[#26293c]/50"
                  >
                    <span className="font-semibold text-[#dfe1fa]">{u.username}</span>
                    <div className="flex flex-wrap gap-1">
                      {u.roles && u.roles.length > 0 ? (
                        u.roles.map((r, i) => (
                          <span
                            key={i}
                            className="bg-[#1e2338] text-[#8cd6cb] px-1.5 py-0.5 rounded text-[10px]"
                          >
                            {r}
                          </span>
                        ))
                      ) : (
                        <span className="text-[10px] text-[#8d909b]">standard</span>
                      )}
                    </div>
                  </div>
                ))}
              </div>
            ) : (
              <div className="text-center py-4 text-[#8d909b] text-[11px]">
                No users listed for {activeEngine}.
              </div>
            )}
          </div>
        </div>

        {/* Right Column: SQL Query Workbench & Table View */}
        <div className="lg:col-span-7 flex flex-col gap-4">
          <div className="bg-[#171a2d] border border-[#26293c] rounded p-3 flex flex-col gap-3">
            <div className="flex items-center justify-between">
              <span className="font-bold text-[12px] text-[#dfe1fa] flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[15px] text-[#b0c9ff]">terminal</span>
                {`SQL Query Workbench (${activeEngine.toUpperCase()})`}
              </span>
              <div className="flex items-center gap-2">
                <input
                  type="text"
                  value={queryDatabase}
                  onChange={e => setQueryDatabase(e.target.value)}
                  placeholder="database name"
                  className="bg-[#0a0d1f] border border-[#26293c] rounded px-2 py-1 text-[11px] text-[#dfe1fa] w-36"
                />
                <button
                  type="button"
                  disabled={queryRunning || !queryText.trim()}
                  onClick={handleRunQuery}
                  className="bg-[#21263d] hover:bg-[#313754] disabled:opacity-50 text-[#b0c9ff] border border-[#b0c9ff]/40 font-bold px-3 py-1 rounded text-[11px] flex items-center gap-1 cursor-pointer disabled:cursor-not-allowed"
                >
                  {queryRunning ? (
                    <span className="material-symbols-outlined animate-spin text-[13px]">refresh</span>
                  ) : (
                    <span className="material-symbols-outlined text-[13px]">play_arrow</span>
                  )}
                  Run SQL
                </button>
              </div>
            </div>

            {/* SQL Statement Input Area */}
            <div>
              <textarea
                rows={3}
                value={queryText}
                onChange={e => setQueryText(e.target.value)}
                placeholder="SELECT * FROM table LIMIT 50;"
                className="w-full bg-[#0a0d1f] border border-[#26293c] rounded p-2 text-xs font-mono text-[#dfe1fa] focus:outline-none focus:border-[#b0c9ff]"
              />
            </div>

            {/* Results Table View */}
            <div className="flex flex-col gap-2">
              <div className="flex items-center justify-between text-[11px] text-[#8d909b]">
                <span>Query Results</span>
                {queryResult && (
                  <span className="text-[10px]">
                    {queryResult.rowCount} {queryResult.rowCount === 1 ? 'row' : 'rows'}
                  </span>
                )}
              </div>

              {queryError && (
                <div className="bg-[#3a1b24] text-[#ffb4ab] p-3 rounded border border-[#ffb4ab]/30 text-xs">
                  <span className="font-bold">Error:</span> {queryError}
                </div>
              )}

              {queryResult !== null && (
                <div className="bg-[#0a0d1f] border border-[#26293c] rounded p-2 overflow-x-auto max-h-[450px]">
                  {queryResult.isTabular ? (
                    <table className="w-full text-left border-collapse text-[11px]">
                      <thead>
                        <tr className="border-b border-[#26293c] bg-[#131627]">
                          {queryResult.columns.map((col, idx) => (
                            <th
                              key={idx}
                              onClick={() => toggleSort(idx)}
                              className="p-2 font-bold text-[#b0c9ff] cursor-pointer hover:bg-[#1a1e34] select-none"
                            >
                              <div className="flex items-center gap-1">
                                <span>{col}</span>
                                {sortColIndex === idx && (
                                  <span className="material-symbols-outlined text-[12px]">
                                    {sortAsc ? 'arrow_upward' : 'arrow_downward'}
                                  </span>
                                )}
                              </div>
                            </th>
                          ))}
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-[#26293c]/50">
                        {sortedRows.map((row, rIdx) => (
                          <tr key={rIdx} className="hover:bg-[#171a2d]/80">
                            {row.map((cell, cIdx) => (
                              <td key={cIdx} className="p-2 whitespace-nowrap">
                                {cell === null || cell === 'NULL' ? (
                                  <span className="italic text-[#8d909b] bg-[#1e2338] px-1 py-0.5 rounded text-[10px]">
                                    NULL
                                  </span>
                                ) : (
                                  <span className="text-[#dfe1fa]">{cell}</span>
                                )}
                              </td>
                            ))}
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  ) : (
                    <pre className="text-[#dfe1fa] text-xs font-mono p-2 whitespace-pre-wrap">
                      {queryResult.rawText || 'Statement executed successfully.'}
                    </pre>
                  )}
                </div>
              )}

              {!queryError && queryResult === null && !queryRunning && (
                <div className="bg-[#0a0d1f] border border-[#26293c] rounded p-8 text-center text-[#8d909b] text-xs">
                  Enter an SQL query above and click <span className="text-[#b0c9ff]">Run SQL</span> to view results.
                </div>
              )}
            </div>
          </div>
        </div>
      </div>

      {/* SQL Data Browser Panel (browse tables & rows without writing SQL) */}
      <div
        data-testid="sql-data-browser"
        className="bg-[#171a2d] border border-[#26293c] rounded p-3 flex flex-col gap-3"
      >
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-[#26293c] pb-2">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[16px] text-[#8cd6cb]">table_view</span>
            <span className="font-bold text-xs text-[#dfe1fa]">
              {`SQL Data Browser (${activeEngine.toUpperCase()})`}
            </span>
            <span className="text-[10px] text-[#8d909b]">
              Browse tables and inspect rows directly
            </span>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <label className="flex items-center gap-1 text-[11px] text-[#8d909b]">
              <span>Database:</span>
              {currentEngineData.databases && currentEngineData.databases.length > 0 ? (
                <select
                  aria-label="Browser Database"
                  value={browserDatabase}
                  onChange={e => {
                    setBrowserDatabase(e.target.value);
                    handleLoadTables(e.target.value);
                  }}
                  className="bg-[#0a0d1f] border border-[#26293c] rounded px-2 py-1 text-[11px] text-[#dfe1fa]"
                >
                  {currentEngineData.databases.map(db => (
                    <option key={db} value={db}>
                      {db}
                    </option>
                  ))}
                </select>
              ) : null}
              <input
                type="text"
                aria-label="Custom Browser Database"
                value={browserDatabase}
                onChange={e => setBrowserDatabase(e.target.value)}
                placeholder={defaultDbForSQLEngine(activeEngine)}
                className="bg-[#0a0d1f] border border-[#26293c] rounded px-2 py-1 text-[11px] text-[#dfe1fa] w-32"
              />
            </label>
            <button
              type="button"
              onClick={() => handleLoadTables()}
              disabled={browserLoadingTables}
              className="px-2.5 py-1 rounded bg-[#21263d] hover:bg-[#313754] disabled:opacity-50 text-[#b0c9ff] border border-[#b0c9ff]/40 text-[11px] font-bold flex items-center gap-1 cursor-pointer"
            >
              <span className={`material-symbols-outlined text-[13px] ${browserLoadingTables ? 'animate-spin' : ''}`}>
                refresh
              </span>
              Load Tables
            </button>
          </div>
        </div>

        {browserError && (
          <div className="bg-[#3a1b24] text-[#ffb4ab] p-2.5 rounded border border-[#ffb4ab]/30 text-xs">
            <span className="font-bold">Browser Error:</span> {browserError}
          </div>
        )}

        <div className="grid grid-cols-1 lg:grid-cols-12 gap-3">
          {/* Left Sub-panel: Tables list */}
          <div className="lg:col-span-3 bg-[#0a0d1f] border border-[#26293c] rounded p-2 flex flex-col gap-1 max-h-80 overflow-y-auto">
            <div className="text-[10px] font-bold uppercase text-[#8d909b] px-1 pb-1 border-b border-[#26293c]">
              Tables ({browserTables.length})
            </div>
            {browserTables.length > 0 ? (
              browserTables.map(tbl => (
                <button
                  key={tbl}
                  type="button"
                  onClick={() => handleBrowseTableRows(tbl, browserLimit, 0)}
                  className={`text-left px-2 py-1.5 rounded text-[11px] flex items-center justify-between cursor-pointer transition-colors ${
                    browserSelectedTable === tbl
                      ? 'bg-[#21263d] text-[#b0c9ff] font-bold'
                      : 'text-[#c3c6d2] hover:bg-[#171a2d]'
                  }`}
                >
                  <span className="truncate">{tbl}</span>
                  <span className="material-symbols-outlined text-[13px]">chevron_right</span>
                </button>
              ))
            ) : (
              <div className="text-center py-6 text-[#8d909b] text-[11px]">
                Click <span className="text-[#b0c9ff]">Load Tables</span> to list tables in{' '}
                <span className="text-[#dfe1fa]">
                  {browserDatabase || queryDatabase || defaultDbForSQLEngine(activeEngine)}
                </span>
                .
              </div>
            )}
          </div>

          {/* Right Sub-panel: Table Rows Viewer */}
          <div className="lg:col-span-9 bg-[#0a0d1f] border border-[#26293c] rounded p-2 flex flex-col gap-2">
            <div className="flex flex-wrap items-center justify-between gap-2 border-b border-[#26293c] pb-2">
              <div className="flex items-center gap-2">
                <span className="font-bold text-[#dfe1fa] text-[11px]">
                  {browserSelectedTable ? `Table: ${browserSelectedTable}` : 'Select a table to browse rows'}
                </span>
                {browserRowsResult && (
                  <span className="text-[10px] text-[#8d909b]">
                    ({browserDisplayRows.length} rows shown, offset {browserOffset})
                  </span>
                )}
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <input
                  type="text"
                  aria-label="Filter Table Rows"
                  value={browserFilter}
                  onChange={e => setBrowserFilter(e.target.value)}
                  placeholder="Filter rows..."
                  className="bg-[#131627] border border-[#26293c] rounded px-2 py-0.5 text-[11px] text-[#dfe1fa] w-36"
                />
                <label className="flex items-center gap-1 text-[10px] text-[#8d909b]">
                  <span>Rows:</span>
                  <select
                    aria-label="Browser Row Limit"
                    value={browserLimit}
                    onChange={e => {
                      const nextLimit = Number(e.target.value) || 25;
                      setBrowserLimit(nextLimit);
                      if (browserSelectedTable) {
                        handleBrowseTableRows(browserSelectedTable, nextLimit, 0);
                      }
                    }}
                    className="bg-[#131627] border border-[#26293c] rounded px-1.5 py-0.5 text-[11px] text-[#dfe1fa]"
                  >
                    <option value={25}>25</option>
                    <option value={50}>50</option>
                    <option value={100}>100</option>
                  </select>
                </label>
                <button
                  type="button"
                  disabled={!browserSelectedTable || browserOffset === 0 || browserLoadingRows}
                  onClick={() => {
                    if (browserSelectedTable) {
                      const nextOffset = Math.max(0, browserOffset - browserLimit);
                      handleBrowseTableRows(browserSelectedTable, browserLimit, nextOffset);
                    }
                  }}
                  className="px-2 py-0.5 rounded bg-[#1b1e31] hover:bg-[#262a42] disabled:opacity-40 text-[#c3c6d2] border border-[#26293c] text-[10px] cursor-pointer"
                >
                  Prev
                </button>
                <button
                  type="button"
                  disabled={!browserSelectedTable || browserLoadingRows}
                  onClick={() => {
                    if (browserSelectedTable) {
                      handleBrowseTableRows(browserSelectedTable, browserLimit, browserOffset + browserLimit);
                    }
                  }}
                  className="px-2 py-0.5 rounded bg-[#1b1e31] hover:bg-[#262a42] disabled:opacity-40 text-[#c3c6d2] border border-[#26293c] text-[10px] cursor-pointer"
                >
                  Next
                </button>
              </div>
            </div>

            {browserLoadingRows ? (
              <div className="py-8 text-center text-[#8d909b]">Loading table rows...</div>
            ) : browserRowsResult && browserRowsResult.isTabular ? (
              <div className="overflow-x-auto max-h-72">
                <table className="w-full text-left border-collapse text-[11px]">
                  <thead>
                    <tr className="border-b border-[#26293c] bg-[#131627]">
                      {browserRowsResult.columns.map((col, idx) => (
                        <th
                          key={idx}
                          onClick={() => toggleBrowserSort(idx)}
                          className="p-2 font-bold text-[#b0c9ff] cursor-pointer hover:bg-[#1a1e34] select-none"
                        >
                          <div className="flex items-center gap-1">
                            <span>{col}</span>
                            {browserSortCol === idx && (
                              <span className="material-symbols-outlined text-[12px]">
                                {browserSortAsc ? 'arrow_upward' : 'arrow_downward'}
                              </span>
                            )}
                          </div>
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-[#26293c]/50">
                    {browserDisplayRows.map((row, rIdx) => (
                      <tr key={rIdx} className="hover:bg-[#171a2d]/80">
                        {row.map((cell, cIdx) => (
                          <td key={cIdx} className="p-2 whitespace-nowrap">
                            {cell === null || cell === 'NULL' ? (
                              <span className="italic text-[#8d909b] bg-[#1e2338] px-1 py-0.5 rounded text-[10px]">
                                NULL
                              </span>
                            ) : (
                              <span className="text-[#dfe1fa]">{cell}</span>
                            )}
                          </td>
                        ))}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : browserRowsResult ? (
              <pre className="text-[#dfe1fa] text-xs font-mono p-2 whitespace-pre-wrap">
                {browserRowsResult.rawText || 'No rows returned.'}
              </pre>
            ) : (
              <div className="py-8 text-center text-[#8d909b] text-xs">
                Select a table on the left to inspect its rows.
              </div>
            )}
          </div>
        </div>
      </div>

      {/* Dump Database Modal */}
      {dumpModalOpen && (
        <div
          data-testid="sql-dump-modal"
          className="fixed inset-0 bg-black/70 flex items-center justify-center z-50 p-4"
        >
          <div className="bg-[#171a2d] border border-[#8cd6cb]/50 rounded-lg max-w-lg w-full p-4 flex flex-col gap-3 shadow-xl">
            <div className="flex items-center justify-between border-b border-[#26293c] pb-2">
              <div className="flex items-center gap-2 text-[#8cd6cb]">
                <span className="material-symbols-outlined text-[20px]">download</span>
                <h3 className="text-sm font-bold text-[#dfe1fa]">
                  Dump Database ({activeEngine.toUpperCase()})
                </h3>
              </div>
              <button
                type="button"
                onClick={() => setDumpModalOpen(false)}
                className="text-[#8d909b] hover:text-[#dfe1fa] cursor-pointer"
              >
                <span className="material-symbols-outlined text-[16px]">close</span>
              </button>
            </div>

            <div className="flex flex-col gap-2.5 text-xs">
              <label className="flex flex-col gap-1">
                <span className="text-[#8d909b] font-semibold">Target Database</span>
                <div className="flex items-center gap-2">
                  {currentEngineData.databases && currentEngineData.databases.length > 0 && (
                    <select
                      aria-label="Dump Target Database Select"
                      value={dumpDatabase}
                      onChange={e => {
                        setDumpDatabase(e.target.value);
                        if (!dumpPathDirty) {
                          setDumpOutputPath(
                            suggestSQLDumpPath(activeEngine, e.target.value, dumpFormat, dumpCompress)
                          );
                        }
                      }}
                      className="bg-[#0a0d1f] border border-[#26293c] rounded px-2 py-1.5 text-xs text-[#dfe1fa]"
                    >
                      {currentEngineData.databases.map(db => (
                        <option key={db} value={db}>
                          {db}
                        </option>
                      ))}
                    </select>
                  )}
                  <input
                    type="text"
                    aria-label="Dump Target Database"
                    value={dumpDatabase}
                    onChange={e => {
                      setDumpDatabase(e.target.value);
                      if (!dumpPathDirty) {
                        setDumpOutputPath(
                          suggestSQLDumpPath(activeEngine, e.target.value, dumpFormat, dumpCompress)
                        );
                      }
                    }}
                    placeholder={defaultDbForSQLEngine(activeEngine)}
                    className="flex-1 bg-[#0a0d1f] border border-[#26293c] rounded px-2 py-1.5 text-xs text-[#dfe1fa]"
                  />
                </div>
              </label>

              <div className="grid grid-cols-1 sm:grid-cols-2 gap-2.5">
                <label className="flex flex-col gap-1">
                  <span className="text-[#8d909b] font-semibold">Dump Format</span>
                  <select
                    aria-label="Dump Format"
                    value={dumpFormat}
                    onChange={e => {
                      setDumpFormat(e.target.value);
                      if (!dumpPathDirty) {
                        setDumpOutputPath(
                          suggestSQLDumpPath(activeEngine, dumpDatabase, e.target.value, dumpCompress)
                        );
                      }
                    }}
                    className="bg-[#0a0d1f] border border-[#26293c] rounded px-2 py-1.5 text-xs text-[#dfe1fa]"
                  >
                    {activeEngine === 'postgres' && (
                      <>
                        <option value="plain">Plain SQL (.sql)</option>
                        <option value="custom">Custom Archive (-Fc)</option>
                        <option value="directory">Directory (-Fd)</option>
                        <option value="tar">Tar Archive (-Ft)</option>
                      </>
                    )}
                    {(activeEngine === 'mariadb' || activeEngine === 'mysql') && (
                      <>
                        <option value="sql">Standard SQL (.sql)</option>
                        <option value="single-transaction">Consistent InnoDB (--single-transaction)</option>
                      </>
                    )}
                    {activeEngine === 'mssql' && (
                      <option value="bak">Full Backup (.bak)</option>
                    )}
                  </select>
                </label>

                <label className="flex flex-col gap-1">
                  <span className="text-[#8d909b] font-semibold">Dump Scope</span>
                  <select
                    aria-label="Dump Scope"
                    value={dumpScope}
                    onChange={e => setDumpScope(e.target.value as 'full' | 'schema_only' | 'data_only')}
                    className="bg-[#0a0d1f] border border-[#26293c] rounded px-2 py-1.5 text-xs text-[#dfe1fa]"
                  >
                    <option value="full">Schema + Data</option>
                    <option value="schema_only">Schema Only</option>
                    <option value="data_only">Data Only</option>
                  </select>
                </label>
              </div>

              <div className="flex flex-wrap items-center gap-4 pt-1">
                <label className="flex items-center gap-1.5 cursor-pointer text-[#c3c6d2]">
                  <input
                    type="checkbox"
                    checked={dumpClean}
                    onChange={e => setDumpClean(e.target.checked)}
                  />
                  <span>Include DROP statements before CREATE</span>
                </label>
                <label className="flex items-center gap-1.5 cursor-pointer text-[#c3c6d2]">
                  <input
                    type="checkbox"
                    checked={dumpCompress}
                    onChange={e => {
                      setDumpCompress(e.target.checked);
                      if (!dumpPathDirty) {
                        setDumpOutputPath(
                          suggestSQLDumpPath(activeEngine, dumpDatabase, dumpFormat, e.target.checked)
                        );
                      }
                    }}
                  />
                  <span>Compress output with gzip (.gz)</span>
                </label>
              </div>

              <label className="flex flex-col gap-1">
                <span className="text-[#8d909b] font-semibold">Output File Path on Host</span>
                <input
                  type="text"
                  aria-label="Output File Path on Host"
                  value={dumpOutputPath}
                  onChange={e => {
                    setDumpOutputPath(e.target.value);
                    setDumpPathDirty(true);
                  }}
                  placeholder={`/tmp/${dumpDatabase || 'db'}_dump.sql`}
                  className="bg-[#0a0d1f] border border-[#26293c] rounded px-2 py-1.5 text-xs text-[#dfe1fa]"
                />
              </label>

              {dumpError && (
                <div className="bg-[#3a1b24] text-[#ffb4ab] p-2 rounded border border-[#ffb4ab]/30 text-xs">
                  {dumpError}
                </div>
              )}

              {dumpOutput && (
                <pre className="bg-[#0a0d1f] border border-[#26293c] rounded p-2 text-[11px] text-[#8cd6cb] max-h-40 overflow-y-auto whitespace-pre-wrap">
                  {dumpOutput}
                </pre>
              )}
            </div>

            <div className="flex flex-wrap items-center justify-end gap-2 pt-2 border-t border-[#26293c]">
              <button
                type="button"
                onClick={() => setDumpModalOpen(false)}
                className="px-3 py-1.5 rounded bg-[#1e2338] text-[#c3c6d2] hover:bg-[#2b314d] text-xs cursor-pointer"
              >
                Close
              </button>
              {activeEngine !== 'mssql' && (dumpFormat === 'plain' || dumpFormat === 'sql' || dumpFormat === 'single-transaction') && (
                <button
                  type="button"
                  disabled={dumpRunning}
                  onClick={() => handleRunDumpDirect(true)}
                  className="px-3 py-1.5 rounded bg-[#1b1e31] hover:bg-[#262a42] text-[#b0c9ff] border border-[#b0c9ff]/40 font-bold text-xs cursor-pointer"
                >
                  Preview SQL Dump
                </button>
              )}
              <button
                type="button"
                disabled={dumpRunning}
                onClick={handleStageDumpTask}
                className="px-3 py-1.5 rounded bg-[#21263d] hover:bg-[#313754] text-[#b0c9ff] border border-[#b0c9ff]/40 font-bold text-xs cursor-pointer"
              >
                Stage Dump Task to Ansible Queue
              </button>
              <button
                type="button"
                disabled={dumpRunning}
                onClick={() => handleRunDumpDirect(false)}
                className="px-3 py-1.5 rounded bg-[#102d24] hover:bg-[#194537] text-[#8cd6cb] border border-[#8cd6cb]/40 font-bold text-xs cursor-pointer"
              >
                {dumpRunning ? 'Running...' : 'Run Dump to Host File'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Safety Confirmation Modal for Destructive Statements */}
      {confirmModalOpen && (
        <div className="fixed inset-0 bg-black/70 flex items-center justify-center z-50 p-4">
          <div className="bg-[#171a2d] border border-[#ffb4ab]/50 rounded-lg max-w-md w-full p-4 flex flex-col gap-3 shadow-xl">
            <div className="flex items-center gap-2 text-[#ffb4ab]">
              <span className="material-symbols-outlined text-[22px]">warning</span>
              <h3 className="text-sm font-bold">Destructive Statement Detected</h3>
            </div>
            <p className="text-xs text-[#c3c6d2]">
              You are about to execute a potentially destructive query (DROP, TRUNCATE, or DELETE without WHERE):
            </p>
            <div className="bg-[#0a0d1f] border border-[#26293c] rounded p-2 text-xs font-mono text-[#ffb4ab] overflow-x-auto max-h-32">
              {pendingDangerousQuery}
            </div>
            <p className="text-xs text-[#8d909b]">
              Please confirm you intend to run this statement against host <strong>{node.name || node.id}</strong>.
            </p>
            <div className="flex items-center justify-end gap-2 pt-2 border-t border-[#26293c]">
              <button
                type="button"
                onClick={() => {
                  setConfirmModalOpen(false);
                  setPendingDangerousQuery(null);
                }}
                className="px-3 py-1.5 rounded bg-[#1e2338] text-[#c3c6d2] hover:bg-[#2b314d] text-xs cursor-pointer"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={confirmDangerousQuery}
                className="px-3 py-1.5 rounded bg-[#ba1a1a] text-white hover:bg-[#de3730] font-bold text-xs cursor-pointer flex items-center gap-1"
              >
                <span className="material-symbols-outlined text-[14px]">delete_forever</span>
                Confirm &amp; Execute
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};

function escapeShell(str: string): string {
  return `'${str.replace(/'/g, "'\\''")}'`;
}

// Parses ASCII table format from psql / mysql / sqlcmd output
export function parseTabularOutput(raw: string): QueryResultTable {
  const trimmed = raw.trim();
  if (!trimmed) {
    return { columns: [], rows: [], rowCount: 0, rawText: '', isTabular: false };
  }

  const lines = trimmed.split('\n');

  // Case 1: MySQL / MariaDB standard ASCII table (| col | col |)
  if (lines.some(l => l.startsWith('+--') || l.startsWith('+=='))) {
    const tableLines = lines.filter(l => l.startsWith('|'));
    if (tableLines.length >= 1) {
      const headerLine = tableLines[0];
      const cols = headerLine
        .split('|')
        .slice(1, -1)
        .map(c => c.trim());

      const dataLines = tableLines.slice(1);
      const rows: (string | null)[][] = [];

      for (const dl of dataLines) {
        const cells = dl
          .split('|')
          .slice(1, -1)
          .map(c => {
            const val = c.trim();
            return val === 'NULL' ? null : val;
          });
        rows.push(cells);
      }

      return {
        columns: cols,
        rows,
        rowCount: rows.length,
        rawText: raw,
        isTabular: true,
      };
    }
  }

  // Case 2: psql table with header and delimiter line
  const dashIndex = lines.findIndex(l => /^[-+]{3,}/.test(l.trim()));
  if (dashIndex > 0) {
    const cols = lines[dashIndex - 1]
      .split('|')
      .map(c => c.trim())
      .filter(Boolean);

    const rows: (string | null)[][] = [];
    for (let i = dashIndex + 1; i < lines.length; i++) {
      const line = lines[i].trim();
      if (!line || line.startsWith('(')) {
        // e.g. "(3 rows)"
        continue;
      }
      const cells = lines[i].split('|').map(c => {
        const v = c.trim();
        return v === 'NULL' || v === '' ? null : v;
      });
      if (cells.length === cols.length) {
        rows.push(cells);
      }
    }

    if (cols.length > 0) {
      return {
        columns: cols,
        rows,
        rowCount: rows.length,
        rawText: raw,
        isTabular: true,
      };
    }
  }

  // Fallback: non-tabular output
  return {
    columns: [],
    rows: [],
    rowCount: 0,
    rawText: raw,
    isTabular: false,
  };
}
