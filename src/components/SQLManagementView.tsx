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

interface QueryResultTable {
  columns: string[];
  rows: (string | null)[][];
  rowCount: number;
  rawText?: string;
  isTabular: boolean;
}

export const SQLManagementView: React.FC<SQLManagementViewProps> = ({
  node,
  pluginId,
  initialData,
  initialLoading,
}) => {
  const parseEngineFromPluginId = (pid?: string): string | null => {
    if (!pid || !pid.includes(':')) return null;
    const raw = pid.split(':')[1]?.toLowerCase().trim();
    if (raw === 'postgres' || raw === 'postgresql') return 'postgres';
    if (raw === 'mariadb' || raw === 'mysql') return 'mariadb';
    if (raw === 'mssql' || raw === 'sqlserver') return 'mssql';
    return null;
  };

  const defaultPortForEngine = (eng: string): string => {
    if (eng === 'mariadb') return '3306';
    if (eng === 'mssql') return '1433';
    return '5432';
  };

  const [data, setData] = useState<SQLReadData | null>(initialData || null);
  const [actions, setActions] = useState<ActionSpec[]>([]);
  const [loading, setLoading] = useState(initialLoading ?? (initialData ? false : true));
  const [error, setError] = useState<string | null>(null);
  const [activeEngine, setActiveEngine] = useState<string>(() => parseEngineFromPluginId(pluginId) || 'postgres');
  const [activeAction, setActiveAction] = useState<ActionSpec | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);

  // Configurable Port & Socket state
  const [configPort, setConfigPort] = useState<string>('');
  const [configSocket, setConfigSocket] = useState<string>('');
  const [savingConfig, setSavingConfig] = useState<boolean>(false);

  // Raw query runner state
  const [queryText, setQueryText] = useState<string>('SELECT 1;');
  const [queryDatabase, setQueryDatabase] = useState<string>('');
  const [queryRunning, setQueryRunning] = useState<boolean>(false);
  const [queryResult, setQueryResult] = useState<QueryResultTable | null>(null);
  const [queryError, setQueryError] = useState<string | null>(null);

  // Destructive statement confirmation modal
  const [confirmModalOpen, setConfirmModalOpen] = useState(false);
  const [pendingDangerousQuery, setPendingDangerousQuery] = useState<string | null>(null);

  // Sorting state for table view
  const [sortColIndex, setSortColIndex] = useState<number | null>(null);
  const [sortAsc, setSortAsc] = useState<boolean>(true);

  useEffect(() => {
    const pinned = parseEngineFromPluginId(pluginId);
    if (pinned) {
      setActiveEngine(pinned);
    }
  }, [pluginId]);

  const fetchData = async () => {
    if (!node?.id) return;
    setLoading(true);
    setError(null);
    try {
      const metaRes = await fetch('/api/plugins');
      if (metaRes.ok) {
        const summaries = await metaRes.json();
        const found = summaries.find((p: any) => p.id === 'database/sql');
        if (found?.actions) setActions(found.actions);
      }
      const res = await fetch(`/api/nodes/${encodeURIComponent(node.id)}/plugins/database/sql`);
      if (!res.ok) {
        const text = await res.text();
        setError(`HTTP ${res.status}: ${text || res.statusText}`);
      } else {
        const readData = await res.json();
        setData(readData);
        const pinned = parseEngineFromPluginId(pluginId);
        if (pinned) {
          setActiveEngine(pinned);
          const engEntry = readData.engines?.find((e: EngineStatus) => e.engine === pinned);
          if (engEntry?.databases && engEntry.databases.length > 0) {
            setQueryDatabase(engEntry.databases[0]);
          }
        } else if (readData.engines && readData.engines.length > 0) {
          setActiveEngine(readData.engines[0].engine);
          if (readData.engines[0].databases && readData.engines[0].databases.length > 0) {
            setQueryDatabase(readData.engines[0].databases[0]);
          }
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
    const ovPort = overrides[`${activeEngine}_port`] || (activeEngine === 'mariadb' ? overrides.mysql_port : '') || '';
    const ovSocket = overrides[`${activeEngine}_socket`] || (activeEngine === 'mariadb' ? overrides.mysql_socket : '') || '';
    setConfigPort(ovPort || metricPort || defaultPortForEngine(activeEngine));
    setConfigSocket(ovSocket || metricSocket || '');
  }, [activeEngine, node?.pluginOverrides, currentEngineData.metrics?.port, currentEngineData.metrics?.socket]);

  const handleSaveConnectionConfig = async () => {
    if (!node?.id) return;
    setSavingConfig(true);
    setError(null);
    setNotice(null);
    try {
      const updatedOverrides: NonNullable<NodeItem['pluginOverrides']> = {
        ...(node.pluginOverrides || {}),
        [`${activeEngine}_port`]: configPort.trim() || defaultPortForEngine(activeEngine),
      };
      if (configSocket.trim()) {
        updatedOverrides[`${activeEngine}_socket`] = configSocket.trim();
      } else {
        delete updatedOverrides[`${activeEngine}_socket`];
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
      await fetch(`/api/nodes/${encodeURIComponent(node.id)}/plugins/refresh`, { method: 'POST' });
      setNotice(`Saved ${activeEngine.toUpperCase()} connection settings (port ${updatedOverrides[`${activeEngine}_port`]}) and re-probed host.`);
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
      let cmd = '';
      const overrides = node.pluginOverrides || {};
      const targetDb = queryDatabase || (activeEngine === 'postgres' ? 'postgres' : '');

      if (activeEngine === 'postgres') {
        const host = overrides.postgres_host ? `-h '${overrides.postgres_host}' ` : '';
        const port = overrides.postgres_port ? `-p ${overrides.postgres_port} ` : '';
        const user = overrides.postgres_user ? `-U '${overrides.postgres_user}' ` : '';
        const dbArg = targetDb ? `-d '${targetDb}' ` : '';
        cmd = `psql ${host}${port}${user}${dbArg}-P null='NULL' -c ${escapeShell(sql)}`;
      } else if (activeEngine === 'mariadb' || activeEngine === 'mysql') {
        const host = overrides.mariadb_host ? `-h '${overrides.mariadb_host}' ` : '';
        const port = overrides.mariadb_port ? `-P ${overrides.mariadb_port} ` : '';
        const user = overrides.mariadb_user ? `-u '${overrides.mariadb_user}' ` : '';
        const pwd = overrides.mariadb_password ? `-p'${overrides.mariadb_password}' ` : '';
        const dbArg = targetDb ? `-D '${targetDb}' ` : '';
        cmd = `mysql ${host}${port}${user}${pwd}${dbArg}-t -e ${escapeShell(sql)}`;
      } else if (activeEngine === 'mssql') {
        const srv = overrides.mssql_host || 'localhost';
        const user = overrides.mssql_user ? `-U '${overrides.mssql_user}' ` : '';
        const pwd = overrides.mssql_password ? `-P '${overrides.mssql_password}' ` : '';
        const dbArg = targetDb ? `-d '${targetDb}' ` : '';
        cmd = `sqlcmd -S '${srv}' ${user}${pwd}${dbArg}-Q ${escapeShell(sql)}`;
      }

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
      const stageRes = await fetch('/api/queue/tasks', {
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

  // Sorted rows calculation
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

  return (
    <div className="flex flex-col gap-4 p-4 font-mono text-xs text-[#c3c6d2]">
      {/* Header bar */}
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-[#26293c] pb-3">
        <div className="flex items-center gap-2">
          <span className="material-symbols-outlined text-[20px] text-[#b0c9ff]">database</span>
          <div>
            <h2 className="text-sm font-bold text-[#dfe1fa]">SQL Database Management</h2>
            <p className="text-[11px] text-[#8d909b]">
              Manage PostgreSQL, MariaDB/MySQL, and MSSQL instances, users, and schemas
            </p>
          </div>
        </div>

        <div className="flex items-center gap-2">
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

      {/* Engine Selection Tabs */}
      <div className="flex items-center gap-1 border-b border-[#26293c]">
        {['postgres', 'mariadb', 'mssql'].map(eng => {
          const det = data?.engines?.find(e => e.engine === eng);
          const isAct = activeEngine === eng;
          return (
            <button
              key={eng}
              type="button"
              onClick={() => {
                setActiveEngine(eng);
                if (det?.databases && det.databases.length > 0) {
                  setQueryDatabase(det.databases[0]);
                } else {
                  setQueryDatabase('');
                }
              }}
              className={`px-3 py-2 border-b-2 font-bold text-xs flex items-center gap-1.5 cursor-pointer transition-colors ${
                isAct
                  ? 'border-[#b0c9ff] text-[#b0c9ff] bg-[#1a1e34]'
                  : 'border-transparent text-[#8d909b] hover:text-[#dfe1fa]'
              }`}
            >
              <span className="material-symbols-outlined text-[15px]">
                {eng === 'postgres' ? 'dns' : eng === 'mariadb' ? 'storage' : 'computer'}
              </span>
              <span className="uppercase">{eng === 'postgres' ? 'PostgreSQL' : eng === 'mariadb' ? 'MariaDB / MySQL' : 'MSSQL'}</span>
              {det?.status === 'active' && (
                <span className="w-1.5 h-1.5 rounded-full bg-[#8cd6cb]" title="Engine Active" />
              )}
            </button>
          );
        })}
      </div>

      {/* Connection Config (Port & Socket) Bar */}
      <div className="bg-[#131627] border border-[#26293c] rounded p-2.5 flex flex-wrap items-center justify-between gap-2 text-[11px]">
        <div className="flex items-center gap-2 text-[#dfe1fa] font-bold">
          <span className="material-symbols-outlined text-[15px] text-[#8cd6cb]">settings_ethernet</span>
          <span>Connection Config ({activeEngine.toUpperCase()}):</span>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <label className="flex items-center gap-1 text-[#8d909b]">
            <span>TCP Port:</span>
            <input
              type="text"
              value={configPort}
              onChange={e => setConfigPort(e.target.value)}
              placeholder={defaultPortForEngine(activeEngine)}
              className="bg-[#0a0d1f] border border-[#26293c] rounded px-2 py-0.5 text-[11px] text-[#dfe1fa] w-20"
            />
          </label>
          <label className="flex items-center gap-1 text-[#8d909b]">
            <span>Unix Socket (optional):</span>
            <input
              type="text"
              value={configSocket}
              onChange={e => setConfigSocket(e.target.value)}
              placeholder={
                activeEngine === 'postgres'
                  ? `/run/postgresql/.s.PGSQL.${configPort || '5432'}`
                  : activeEngine === 'mariadb'
                  ? '/run/mysqld/mysqld.sock'
                  : '/var/opt/mssql/.system/mssql.sock'
              }
              className="bg-[#0a0d1f] border border-[#26293c] rounded px-2 py-0.5 text-[11px] text-[#dfe1fa] w-56"
            />
          </label>
          <button
            type="button"
            disabled={savingConfig}
            onClick={handleSaveConnectionConfig}
            className="px-2.5 py-0.5 rounded bg-[#21263d] hover:bg-[#313754] disabled:opacity-50 text-[#b0c9ff] border border-[#b0c9ff]/40 font-bold cursor-pointer"
          >
            {savingConfig ? 'Saving...' : 'Save Port / Re-probe'}
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
                Databases & Schemas ({currentEngineData.databases?.length || 0})
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
                    onClick={() => setQueryDatabase(db)}
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
                Users & Roles ({currentEngineData.users?.length || 0})
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
                SQL Query Workbench ({activeEngine.toUpperCase()})
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
