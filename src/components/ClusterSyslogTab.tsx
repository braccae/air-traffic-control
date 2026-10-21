import React, { useEffect, useMemo, useState } from 'react';
import type { NodeItem } from '../types/index.ts';
import type { ClusterLogEntry, ClusterLogKind } from '../utils/clusterLogStore.ts';
import type { ConnectionSnapshot, ConnectionKind } from '../utils/connectionRegistry.ts';

interface ClusterSyslogTabProps {
  nodes?: NodeItem[];
}

type SyslogFilter = 'all' | 'app' | 'poll' | 'node';

const FILTERS: { key: SyslogFilter; label: string }[] = [
  { key: 'all', label: 'All' },
  { key: 'app', label: 'ATC App' },
  { key: 'poll', label: 'Polls' },
  { key: 'node', label: 'Node Syslog' },
];

const KIND_BADGES: Record<ClusterLogKind, { label: string; className: string }> = {
  app: { label: 'APP', className: 'bg-[#b0c9ff]/15 text-[#b0c9ff] border-[#b0c9ff]/30' },
  poll: { label: 'POLL', className: 'bg-[#8cd6cb]/15 text-[#8cd6cb] border-[#8cd6cb]/30' },
  node: { label: 'NODE', className: 'bg-[#d9b9ff]/15 text-[#d9b9ff] border-[#d9b9ff]/30' },
};

const POLL_FAILED_BADGE = 'bg-[#ffb4ab]/15 text-[#ffb4ab] border-[#ffb4ab]/30';

const CONNECTION_KINDS: ConnectionKind[] = ['websocket', 'ssh', 'winrm', 'tailscale', 'local'];

const CONNECTION_LABELS: Record<ConnectionKind, string> = {
  websocket: 'WebSocket',
  ssh: 'SSH',
  winrm: 'WinRM',
  tailscale: 'Tailscale',
  local: 'Local',
};

const MAX_DISPLAYED_ENTRIES = 500;

function formatClock(ts: number): string {
  return new Date(ts).toLocaleTimeString('en-GB', { hour12: false });
}

function mergeEntries(previous: ClusterLogEntry[], incoming: ClusterLogEntry[]): ClusterLogEntry[] {
  const byId = new Map<number, ClusterLogEntry>();
  for (const entry of previous) byId.set(entry.id, entry);
  for (const entry of incoming) byId.set(entry.id, entry);
  return Array.from(byId.values())
    .sort((a, b) => a.id - b.id)
    .slice(-MAX_DISPLAYED_ENTRIES);
}

export const ClusterSyslogTab: React.FC<ClusterSyslogTabProps> = ({ nodes = [] }) => {
  const [entries, setEntries] = useState<ClusterLogEntry[]>([]);
  const [connections, setConnections] = useState<ConnectionSnapshot | null>(null);
  const [filter, setFilter] = useState<SyslogFilter>('all');
  const [feedError, setFeedError] = useState<string | null>(null);

  const [selectedNodeId, setSelectedNodeId] = useState<string>(nodes[0]?.id || '');
  const [nodeLines, setNodeLines] = useState<string[]>([]);
  const [nodeLogHost, setNodeLogHost] = useState<string | null>(null);
  const [nodeLogError, setNodeLogError] = useState<string | null>(null);
  const [nodeLogLoading, setNodeLogLoading] = useState(false);
  const [nodeLogRequest, setNodeLogRequest] = useState(0);

  const selectedNode = useMemo(
    () => nodes.find(node => node.id === selectedNodeId) || nodes[0],
    [nodes, selectedNodeId]
  );

  useEffect(() => {
    if (!selectedNodeId && nodes.length > 0) {
      setSelectedNodeId(nodes[0].id);
    }
  }, [nodes, selectedNodeId]);

  // Initial snapshot + live WebSocket feed from the coordinator.
  useEffect(() => {
    let isMounted = true;

    fetch('/api/cluster-log?limit=300')
      .then(res => res.json())
      .then(data => {
        if (!isMounted) return;
        if (data?.success && Array.isArray(data.entries)) {
          setEntries(previous => mergeEntries(previous, data.entries));
          setFeedError(null);
        } else {
          setFeedError(data?.error || 'Cluster log endpoint returned no entries');
        }
      })
      .catch((err: any) => {
        if (isMounted) setFeedError(err?.message || 'Failed to reach /api/cluster-log');
      });

    fetch('/api/connections')
      .then(res => res.json())
      .then(data => {
        if (isMounted && data?.success && data.connections) {
          setConnections(data.connections as ConnectionSnapshot);
        }
      })
      .catch(() => {});

    let ws: WebSocket | null = null;
    let reconnectTimeout: ReturnType<typeof setTimeout> | null = null;

    const connectWS = () => {
      try {
        if (typeof window === 'undefined' || !window.location) return;
        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        ws = new WebSocket(`${protocol}//${window.location.host}/ws`);

        ws.onmessage = event => {
          try {
            const msg = JSON.parse(event.data);
            if (msg.type === 'cluster_log_batch' && Array.isArray(msg.entries)) {
              setEntries(previous => mergeEntries(previous, msg.entries as ClusterLogEntry[]));
            } else if (msg.type === 'connections_updated' && msg.connections) {
              setConnections(msg.connections as ConnectionSnapshot);
            }
          } catch {
            // Ignore non-JSON messages on the shared hub.
          }
        };

        ws.onclose = () => {
          if (isMounted) reconnectTimeout = setTimeout(connectWS, 3000);
        };
      } catch {
        if (isMounted) reconnectTimeout = setTimeout(connectWS, 3000);
      }
    };

    connectWS();

    return () => {
      isMounted = false;
      if (ws) ws.close();
      if (reconnectTimeout) clearTimeout(reconnectTimeout);
    };
  }, []);

  // Real node syslog is fetched on demand from the coordinator (journalctl over SSH).
  useEffect(() => {
    if (filter !== 'node' || !selectedNode) return;
    let cancelled = false;
    setNodeLogLoading(true);
    setNodeLogError(null);

    fetch(`/api/system/logs?node=${encodeURIComponent(selectedNode.id)}&lines=100`)
      .then(async res => {
        const data = await res.json();
        if (cancelled) return;
        if (data?.success) {
          setNodeLines(Array.isArray(data.lines) ? data.lines : []);
          setNodeLogHost(data.host || selectedNode.tailscaleHost || selectedNode.name);
        } else {
          setNodeLines([]);
          setNodeLogError(data?.error || `Failed to retrieve system logs from ${selectedNode.name}`);
        }
      })
      .catch((err: any) => {
        if (!cancelled) {
          setNodeLines([]);
          setNodeLogError(err?.message || `Failed to retrieve system logs from ${selectedNode.name}`);
        }
      })
      .finally(() => {
        if (!cancelled) setNodeLogLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [filter, selectedNode, nodeLogRequest]);

  const visibleEntries = useMemo(() => {
    if (filter === 'app') return entries.filter(entry => entry.kind === 'app');
    if (filter === 'poll') return entries.filter(entry => entry.kind === 'poll');
    if (filter === 'all') return entries.filter(entry => entry.kind === 'app' || entry.kind === 'poll');
    return [];
  }, [entries, filter]);

  return (
    <div className="flex flex-col h-full bg-[#0a0d1f] font-mono text-xs">
      {/* Live connection ticker — real refcounts from the coordinator */}
      <div className="shrink-0 flex items-center gap-2 px-3 py-1 border-b border-[#26293c] bg-[#171a2d] overflow-x-auto scrollbar-none">
        <span className="text-[10px] uppercase tracking-wider text-[#8d909b] shrink-0">Active connections</span>
        {CONNECTION_KINDS.map(kind => {
          const count = connections?.byKind?.[kind] ?? 0;
          const labels = connections?.details?.[kind] || [];
          return (
            <span
              key={kind}
              title={labels.length > 0 ? labels.join('\n') : `No active ${CONNECTION_LABELS[kind]} connections`}
              className={`px-1.5 py-0.5 rounded border text-[10px] font-bold shrink-0 ${
                count > 0
                  ? 'bg-[#8cd6cb]/15 text-[#8cd6cb] border-[#8cd6cb]/30'
                  : 'bg-[#26293c]/40 text-[#8d909b] border-[#26293c]'
              }`}
            >
              {CONNECTION_LABELS[kind]} {count}
            </span>
          );
        })}
        <span className="px-1.5 py-0.5 rounded border text-[10px] font-bold bg-[#b0c9ff]/10 text-[#b0c9ff] border-[#b0c9ff]/30 shrink-0">
          Total {connections?.total ?? 0}
        </span>

        <span className="ml-auto flex items-center gap-1 shrink-0">
          {FILTERS.map(item => (
            <button
              key={item.key}
              type="button"
              onClick={() => setFilter(item.key)}
              className={`px-2 py-0.5 rounded text-[10px] font-bold transition-colors cursor-pointer ${
                filter === item.key
                  ? 'bg-[#313447] text-[#b0c9ff]'
                  : 'text-[#c3c6d2] hover:text-[#dfe1fa]'
              }`}
            >
              {item.label}
            </button>
          ))}
        </span>
      </div>

      <div className="flex-1 overflow-y-auto overflow-x-auto px-3 py-2 space-y-0.5">
        {filter !== 'node' && feedError && (
          <div className="text-[#ffb4ab] px-1">{feedError}</div>
        )}

        {filter !== 'node' && !feedError && visibleEntries.length === 0 && (
          <div className="text-[#8d909b] px-1">
            No {filter === 'poll' ? 'backend poll' : filter === 'app' ? 'ATC application' : 'cluster'} log entries
            recorded yet by the coordinator.
          </div>
        )}

        {filter !== 'node' &&
          visibleEntries.map(entry => {
            const badge = KIND_BADGES[entry.kind] || KIND_BADGES.app;
            const failed = entry.ok === false || entry.level === 'error';
            return (
              <div
                key={entry.id}
                className="flex items-start gap-2 px-1 rounded hover:bg-[#171a2d] leading-5"
              >
                <span className="text-[#8d909b] shrink-0">{formatClock(entry.ts)}</span>
                <span
                  className={`px-1 rounded border text-[9px] font-bold shrink-0 mt-0.5 ${failed ? POLL_FAILED_BADGE : badge.className}`}
                >
                  {badge.label}
                </span>
                <span className="text-[#8cd6cb] shrink-0">{entry.source}</span>
                {entry.host && <span className="text-[#d9b9ff] shrink-0">{entry.host}</span>}
                {typeof entry.durationMs === 'number' && (
                  <span className="text-[#8d909b] shrink-0">{entry.durationMs}ms</span>
                )}
                <span className={`whitespace-pre-wrap break-all ${failed ? 'text-[#ffb4ab]' : 'text-[#dfe1fa]'}`}>
                  {entry.message}
                </span>
              </div>
            );
          })}

        {filter === 'node' && (
          <div className="space-y-2">
            <div className="flex items-center gap-2">
              <select
                value={selectedNodeId}
                onChange={event => setSelectedNodeId(event.target.value)}
                className="bg-[#171a2d] border border-[#26293c] text-[#dfe1fa] text-[11px] px-2 py-1 rounded focus:outline-none"
              >
                {nodes.length === 0 && <option value="">No nodes</option>}
                {nodes.map(node => (
                  <option key={node.id} value={node.id}>
                    {node.name} ({node.tailscaleHost || node.ip})
                  </option>
                ))}
              </select>
              <button
                type="button"
                onClick={() => setNodeLogRequest(value => value + 1)}
                disabled={!selectedNode || nodeLogLoading}
                className="px-2 py-1 rounded bg-[#313447] text-[#b0c9ff] text-[11px] font-bold disabled:opacity-40 cursor-pointer"
              >
                {nodeLogLoading ? 'Loading…' : 'Refresh'}
              </button>
              {nodeLogHost && <span className="text-[#d9b9ff] text-[11px]">{nodeLogHost}</span>}
            </div>

            {nodeLogError && <div className="text-[#ffb4ab] px-1">{nodeLogError}</div>}

            {!nodeLogError && nodeLogLoading && nodeLines.length === 0 && (
              <div className="text-[#8d909b] px-1">Fetching journalctl output over SSH…</div>
            )}

            {!nodeLogError && !nodeLogLoading && nodeLines.length === 0 && selectedNode && (
              <div className="text-[#8d909b] px-1">No log lines returned by {nodeLogHost || selectedNode.name}.</div>
            )}

            {nodeLines.map((line, idx) => (
              <div key={idx} className="flex items-start gap-2 px-1 rounded hover:bg-[#171a2d] leading-5">
                <span className={`px-1 rounded border text-[9px] font-bold shrink-0 mt-0.5 ${KIND_BADGES.node.className}`}>
                  NODE
                </span>
                <span className="text-[#c3c6d2] whitespace-pre-wrap break-all">{line}</span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
};
