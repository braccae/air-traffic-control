import React, { useState, useEffect } from 'react';
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

export const NoSQLView: React.FC<NoSQLViewProps> = ({ node, initialData, initialLoading }) => {
  const [data, setData] = useState<NoSQLReadData | null>(initialData || null);
  const [actions, setActions] = useState<ActionSpec[]>([]);
  const [loading, setLoading] = useState(initialLoading ?? (initialData ? false : true));
  const [error, setError] = useState<string | null>(null);
  const [activeEngine, setActiveEngine] = useState<string>('redis');
  const [activeAction, setActiveAction] = useState<ActionSpec | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);

  // Raw query workbench state
  const [queryCommand, setQueryCommand] = useState<string>('');
  const [queryDatabase, setQueryDatabase] = useState<string>('');
  const [queryRunning, setQueryRunning] = useState<boolean>(false);
  const [queryResult, setQueryResult] = useState<any>(null);
  const [queryError, setQueryError] = useState<string | null>(null);

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
        if (readData.engines && readData.engines.length > 0) {
          setActiveEngine(readData.engines[0].engine);
        }
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

  const currentEngineData = data?.engines?.find(e => e.engine === activeEngine) || {
    engine: activeEngine,
    status: 'inactive',
    databases: [],
    users: [],
    dataFormat: activeEngine === 'redis' || activeEngine === 'valkey' ? 'keyvalue' : activeEngine === 'rocksdb' ? 'text' : 'document',
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
          body: JSON.stringify({ ...values, engine: values.engine || activeEngine }),
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
      // Execute command via /api/terminal/exec
      let cmd = '';
      const overrides = node.pluginOverrides || {};
      if (activeEngine === 'redis' || activeEngine === 'valkey') {
        const auth = overrides.redis_password ? `-a '${overrides.redis_password}' ` : '';
        const port = overrides.redis_port ? `-p ${overrides.redis_port} ` : '';
        const host = overrides.redis_host ? `-h ${overrides.redis_host} ` : '';
        cmd = `redis-cli ${host}${port}${auth}${queryCommand}`;
      } else if (activeEngine === 'mongodb') {
        const dbPart = queryDatabase ? escapeShell(queryDatabase) : 'admin';
        cmd = `mongosh --quiet ${dbPart} --eval ${escapeShell(queryCommand)}`;
      } else if (activeEngine === 'couchdb') {
        const path = queryCommand.startsWith('/') ? queryCommand : `/${queryCommand}`;
        cmd = `curl -s -m 5 http://127.0.0.1:5984${path}`;
      } else if (activeEngine === 'rocksdb') {
        const dbPath = queryDatabase || overrides.rocksdb_path || '/var/lib/rocksdb';
        cmd = `ldb --db=${escapeShell(dbPath)} ${queryCommand}`;
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
        // Parse according to data format
        if (currentEngineData.dataFormat === 'document') {
          try {
            setQueryResult(JSON.parse(rawOutput));
          } catch {
            setQueryResult(rawOutput);
          }
        } else if (currentEngineData.dataFormat === 'keyvalue') {
          // Parse lines
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

  return (
    <div className="flex flex-col gap-4 p-4 font-mono text-xs text-[#c3c6d2]">
      {/* Top Header & Engine Selector */}
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[#26293c] pb-3">
        <div className="flex items-center gap-2">
          <span className="material-symbols-outlined text-lg text-[#8cd6cb]">database</span>
          <span className="font-bold text-sm text-[#dfe1fa]">NoSQL Database Management</span>
          <span className="text-[10px] text-[#8d909b] uppercase ml-2 tracking-wider">
            {node.name} ({node.tailscaleHost || node.ip || 'local'})
          </span>
        </div>

        {/* Engine Tabs */}
        <div className="flex items-center gap-1 bg-[#121528] p-1 rounded border border-[#26293c]">
          {['redis', 'mongodb', 'couchdb', 'rocksdb'].map(eng => {
            const detected = data?.engines?.some(e => e.engine === eng && e.status === 'active');
            const isSelected = activeEngine === eng;
            return (
              <button
                key={eng}
                type="button"
                onClick={() => {
                  setActiveEngine(eng);
                  setQueryResult(null);
                  setQueryError(null);
                }}
                className={`px-3 py-1 rounded text-[11px] font-bold uppercase transition-colors flex items-center gap-1.5 cursor-pointer ${
                  isSelected
                    ? 'bg-[#21263d] text-[#b0c9ff] border border-[#b0c9ff]/40 shadow-sm'
                    : 'text-[#8d909b] hover:text-[#dfe1fa] hover:bg-[#1b1e31]'
                }`}
              >
                <span>{eng === 'redis' ? 'Redis / Valkey' : eng}</span>
                {detected && <span className="w-1.5 h-1.5 rounded-full bg-[#8cd6cb]" title="Engine active" />}
              </button>
            );
          })}
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
                      onClick={() => setQueryDatabase(db)}
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
                  Raw Query Workbench ({activeEngine.toUpperCase()})
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
      )}
    </div>
  );
};

function escapeShell(arg: string): string {
  return `'${arg.replace(/'/g, `'\\''`)}'`;
}
