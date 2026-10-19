import React, { useState, useEffect, useRef, useMemo } from 'react';
import type { NodeItem, ParsedLogEntry } from '../types/index.ts';
import { parseLogLine, filterLogEntries } from '../utils/systemLogs.ts';

interface SystemLogsViewProps {
  node: NodeItem;
  initialUnit?: string;
}

const UNIT_QUICK_PILLS = ['sshd', 'tailscaled', 'podman', 'NetworkManager', 'firewalld', 'caddy'];
const USER_QUICK_PILLS = ['root', 'core', 'apps', 'media'];
const TIME_RANGES = [
  { label: '15m', value: '15m' },
  { label: '1h', value: '1h' },
  { label: '6h', value: '6h' },
  { label: '24h', value: '24h' },
  { label: '7d', value: '7d' },
  { label: 'All', value: 'all' },
];
const PRIORITIES = [
  { label: 'All Levels', value: 'all' },
  { label: 'Error & Higher', value: 'err' },
  { label: 'Warning & Higher', value: 'warning' },
  { label: 'Info & Higher', value: 'info' },
  { label: 'Debug Only', value: 'debug' },
];
const LINE_LIMITS = [50, 100, 250, 500, 1000];

export const SystemLogsView: React.FC<SystemLogsViewProps> = ({ node, initialUnit }) => {
  // Filter states
  const [source, setSource] = useState<'journald' | 'dmesg' | 'audit' | 'sshd'>('journald');
  const [unit, setUnit] = useState<string>(initialUnit || '');
  const [user, setUser] = useState<string>('');
  const [priority, setPriority] = useState<string>('all');
  const [since, setSince] = useState<string>('1h');
  const [linesLimit, setLinesLimit] = useState<number>(100);
  const [searchTerm, setSearchTerm] = useState<string>('');
  const [clientFilterLevel, setClientFilterLevel] = useState<string>('all');

  // Logs data and UI states
  const [rawLines, setRawLines] = useState<string[]>([]);
  const [loading, setLoading] = useState<boolean>(false);
  const [error, setError] = useState<string | null>(null);
  const [autoRefresh, setAutoRefresh] = useState<boolean>(false);
  const [lastRefreshed, setLastRefreshed] = useState<string>('');
  const [copied, setCopied] = useState<boolean>(false);
  const [stickToBottom, setStickToBottom] = useState<boolean>(true);

  const terminalEndRef = useRef<HTMLDivElement>(null);
  const terminalScrollRef = useRef<HTMLDivElement>(null);

  // Fetch logs from backend
  const fetchLogs = async (isManual = false) => {
    if (isManual) setLoading(true);
    setError(null);

    const params = new URLSearchParams();
    if (source !== 'journald') params.set('source', source);
    if (unit.trim()) params.set('unit', unit.trim());
    if (user.trim()) params.set('user', user.trim());
    if (priority !== 'all') params.set('priority', priority);
    if (since !== 'all') params.set('since', since);
    params.set('lines', String(linesLimit));

    try {
      const res = await fetch(`/api/nodes/${encodeURIComponent(node.id)}/system-logs?${params.toString()}`);
      const data = await res.json();
      if (data.success && Array.isArray(data.lines)) {
        setRawLines(data.lines);
        setError(null);
      } else {
        setRawLines(data.lines || []);
        setError(data.error || 'Failed to retrieve logs from host');
      }
      setLastRefreshed(new Date().toLocaleTimeString());
    } catch (err: any) {
      setError(err?.message || 'Network error fetching system logs');
    } finally {
      if (isManual) setLoading(false);
    }
  };

  // Initial fetch and dependency trigger
  useEffect(() => {
    fetchLogs(true);
  }, [node.id, source, unit, user, priority, since, linesLimit]);

  // Auto-refresh timer (every 5 seconds)
  useEffect(() => {
    if (!autoRefresh) return;
    const interval = setInterval(() => {
      fetchLogs(false);
    }, 5000);
    return () => clearInterval(interval);
  }, [autoRefresh, node.id, source, unit, user, priority, since, linesLimit]);

  // Parse raw lines
  const parsedEntries: ParsedLogEntry[] = useMemo(() => {
    return rawLines.map((line, idx) => parseLogLine(line, idx));
  }, [rawLines]);

  // Filter entries client-side
  const visibleEntries: ParsedLogEntry[] = useMemo(() => {
    return filterLogEntries(parsedEntries, {
      search: searchTerm,
      level: clientFilterLevel,
    });
  }, [parsedEntries, searchTerm, clientFilterLevel]);

  // Scroll to bottom if stickToBottom enabled
  useEffect(() => {
    if (stickToBottom && terminalEndRef.current) {
      terminalEndRef.current.scrollIntoView({ behavior: 'smooth' });
    }
  }, [visibleEntries, stickToBottom]);

  // Counts by severity
  const severityCounts = useMemo(() => {
    let err = 0;
    let warn = 0;
    let inf = 0;
    let dbg = 0;
    for (const e of parsedEntries) {
      if (e.level === 'error') err++;
      else if (e.level === 'warning') warn++;
      else if (e.level === 'debug') dbg++;
      else inf++;
    }
    return { err, warn, inf, dbg, total: parsedEntries.length };
  }, [parsedEntries]);

  // Copy logs to clipboard
  const handleCopyLogs = () => {
    const text = visibleEntries.map(e => e.raw).join('\n');
    navigator.clipboard.writeText(text).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  };

  // Export logs to download
  const handleExportLogs = () => {
    const text = visibleEntries.map(e => e.raw).join('\n');
    const blob = new Blob([text], { type: 'text/plain;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `${node.shortName}-logs-${source}-${Date.now()}.log`;
    a.click();
    URL.revokeObjectURL(url);
  };

  // Reset all filters
  const handleClearFilters = () => {
    setUnit('');
    setUser('');
    setPriority('all');
    setSince('1h');
    setSearchTerm('');
    setClientFilterLevel('all');
  };

  // Highlight matches
  const renderHighlightedMessage = (text: string, term: string) => {
    if (!term.trim()) return text;
    const regex = new RegExp(`(${term.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')})`, 'gi');
    const parts = text.split(regex);
    return parts.map((part, i) =>
      regex.test(part) ? (
        <mark key={i} className="bg-[#f9c068]/30 text-[#f9c068] font-bold px-0.5 rounded">
          {part}
        </mark>
      ) : (
        part
      )
    );
  };

  return (
    <section className="bg-[#171a2d] rounded-lg p-4 flex flex-col border border-[#313447] shadow-xl text-[#dfe1fa]">
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-3 pb-3 border-b border-[#26293c]">
        <div className="flex items-center gap-2.5">
          <span className="material-symbols-outlined text-[#8cd6cb] text-[24px]">article</span>
          <div>
            <div className="flex items-center gap-2">
              <h3 className="font-sans text-sm text-[#dfe1fa] font-bold">
                System Logs &amp; Journald Telemetry
              </h3>
              <span className="font-mono text-[10px] px-2 py-0.5 rounded bg-[#26293c] text-[#8cd6cb] border border-[#8cd6cb]/30">
                {node.shortName} ({node.tailscaleIp})
              </span>
            </div>
            <p className="font-mono text-[11px] text-[#8d909b]">
              Deep journalctl / dmesg / audit telemetry filtering with real-time severity parsing
            </p>
          </div>
        </div>

        {/* Action buttons */}
        <div className="flex items-center gap-2 font-mono text-xs">
          {/* Auto Refresh Toggle */}
          <button
            type="button"
            onClick={() => setAutoRefresh(!autoRefresh)}
            className={`flex items-center gap-1.5 px-2.5 py-1 rounded transition-colors border cursor-pointer ${
              autoRefresh
                ? 'bg-[#8cd6cb]/20 text-[#8cd6cb] border-[#8cd6cb]/40 font-bold'
                : 'bg-[#26293c] hover:bg-[#35384c] text-[#8d909b] border-[#434750]/40'
            }`}
            title="Auto-refresh every 5s"
          >
            <span className={`material-symbols-outlined text-[14px] ${autoRefresh ? 'animate-spin' : ''}`}>
              sync
            </span>
            <span>{autoRefresh ? 'Auto 5s' : 'Auto-off'}</span>
          </button>

          {/* Manual Refresh */}
          <button
            type="button"
            onClick={() => fetchLogs(true)}
            disabled={loading}
            className="flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] px-2.5 py-1 rounded transition-colors border border-[#434750]/40 cursor-pointer disabled:opacity-50"
            title="Manual reload from host"
          >
            <span className={`material-symbols-outlined text-[14px] ${loading ? 'animate-spin' : ''}`}>
              refresh
            </span>
            <span>Reload</span>
          </button>

          {/* Copy logs */}
          <button
            type="button"
            onClick={handleCopyLogs}
            disabled={visibleEntries.length === 0}
            className="flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] px-2 py-1 rounded transition-colors border border-[#434750]/40 cursor-pointer disabled:opacity-40"
            title="Copy logs to clipboard"
          >
            <span className="material-symbols-outlined text-[14px]">
              {copied ? 'check' : 'content_copy'}
            </span>
            <span>{copied ? 'Copied' : 'Copy'}</span>
          </button>

          {/* Export logs */}
          <button
            type="button"
            onClick={handleExportLogs}
            disabled={visibleEntries.length === 0}
            className="flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] px-2 py-1 rounded transition-colors border border-[#434750]/40 cursor-pointer disabled:opacity-40"
            title="Download logs file"
          >
            <span className="material-symbols-outlined text-[14px]">download</span>
            <span>Export</span>
          </button>
        </div>
      </div>

      {/* Main Filter Toolbar */}
      <div className="bg-[#121528] rounded-md p-3 my-3 border border-[#26293c] flex flex-col gap-2.5 font-mono text-xs">
        {/* Source Selector & Range Selector */}
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex items-center gap-2">
            <span className="text-[#8d909b] text-[11px] font-bold">SOURCE:</span>
            <div className="flex items-center rounded bg-[#171a2d] p-0.5 border border-[#313447]">
              {(['journald', 'dmesg', 'audit', 'sshd'] as const).map(s => (
                <button
                  key={s}
                  type="button"
                  onClick={() => setSource(s)}
                  className={`px-2.5 py-1 rounded text-xs transition-colors cursor-pointer capitalize ${
                    source === s
                      ? 'bg-[#26293c] text-[#b0c9ff] font-bold shadow'
                      : 'text-[#8d909b] hover:text-[#dfe1fa]'
                  }`}
                >
                  {s === 'dmesg' ? 'dmesg (Kernel)' : s === 'audit' ? 'audit / PAM' : s}
                </button>
              ))}
            </div>
          </div>

          {/* Time Range Selector */}
          <div className="flex items-center gap-2">
            <span className="text-[#8d909b] text-[11px] font-bold">SINCE:</span>
            <div className="flex items-center gap-1">
              {TIME_RANGES.map(tr => (
                <button
                  key={tr.value}
                  type="button"
                  onClick={() => setSince(tr.value)}
                  className={`px-2 py-0.5 rounded text-xs transition-colors cursor-pointer ${
                    since === tr.value
                      ? 'bg-[#b0c9ff] text-[#002f68] font-bold'
                      : 'bg-[#171a2d] text-[#8d909b] hover:text-[#dfe1fa] border border-[#26293c]'
                  }`}
                >
                  {tr.label}
                </button>
              ))}
            </div>
          </div>

          {/* Priority Dropdown */}
          <div className="flex items-center gap-2">
            <span className="text-[#8d909b] text-[11px] font-bold">PRIORITY:</span>
            <select
              value={priority}
              onChange={e => setPriority(e.target.value)}
              className="bg-[#171a2d] text-[#dfe1fa] px-2 py-1 rounded border border-[#313447] text-xs focus:outline-none focus:border-[#b0c9ff]"
            >
              {PRIORITIES.map(p => (
                <option key={p.value} value={p.value}>
                  {p.label}
                </option>
              ))}
            </select>
          </div>

          {/* Lines Limit */}
          <div className="flex items-center gap-2">
            <span className="text-[#8d909b] text-[11px] font-bold">LINES:</span>
            <select
              value={linesLimit}
              onChange={e => setLinesLimit(Number(e.target.value))}
              className="bg-[#171a2d] text-[#dfe1fa] px-2 py-1 rounded border border-[#313447] text-xs focus:outline-none focus:border-[#b0c9ff]"
            >
              {LINE_LIMITS.map(l => (
                <option key={l} value={l}>
                  {l} lines
                </option>
              ))}
            </select>
          </div>
        </div>

        {/* Unit & User Input Filters with Quick Pills */}
        <div className="grid grid-cols-1 md:grid-cols-2 gap-3 pt-2 border-t border-[#1b1e31]">
          {/* Unit Filter Input & Pills */}
          <div className="flex flex-col gap-1.5">
            <div className="flex items-center justify-between">
              <label className="text-[#8d909b] text-[11px] font-bold">
                SYSTEMD UNIT (-u):
              </label>
              {unit && (
                <button
                  type="button"
                  onClick={() => setUnit('')}
                  className="text-[10px] text-[#ffb4ab] hover:underline cursor-pointer"
                >
                  Clear unit
                </button>
              )}
            </div>
            <div className="relative">
              <input
                type="text"
                value={unit}
                onChange={e => setUnit(e.target.value)}
                placeholder="Filter by unit (e.g. sshd, tailscaled, podman)..."
                className="w-full bg-[#171a2d] text-[#dfe1fa] px-2.5 py-1 rounded border border-[#313447] text-xs focus:outline-none focus:border-[#8cd6cb] font-mono"
              />
            </div>
            {/* Quick Pills */}
            <div className="flex flex-wrap items-center gap-1.5 pt-0.5">
              <span className="text-[10px] text-[#8d909b]">Pills:</span>
              {UNIT_QUICK_PILLS.map(pill => (
                <button
                  key={pill}
                  type="button"
                  onClick={() => setUnit(unit === pill ? '' : pill)}
                  className={`px-1.5 py-0.5 rounded text-[10px] cursor-pointer transition-colors ${
                    unit === pill
                      ? 'bg-[#8cd6cb] text-[#003731] font-bold'
                      : 'bg-[#1b1e31] text-[#c3c6d2] hover:bg-[#26293c] border border-[#313447]'
                  }`}
                >
                  {pill}
                </button>
              ))}
            </div>
          </div>

          {/* User Filter Input & Pills */}
          <div className="flex flex-col gap-1.5">
            <div className="flex items-center justify-between">
              <label className="text-[#8d909b] text-[11px] font-bold">
                USER / UID (_UID):
              </label>
              {user && (
                <button
                  type="button"
                  onClick={() => setUser('')}
                  className="text-[10px] text-[#ffb4ab] hover:underline cursor-pointer"
                >
                  Clear user
                </button>
              )}
            </div>
            <div className="relative">
              <input
                type="text"
                value={user}
                onChange={e => setUser(e.target.value)}
                placeholder="Filter by user or UID (e.g. root, core, apps, 1000)..."
                className="w-full bg-[#171a2d] text-[#dfe1fa] px-2.5 py-1 rounded border border-[#313447] text-xs focus:outline-none focus:border-[#8cd6cb] font-mono"
              />
            </div>
            {/* Quick Pills */}
            <div className="flex flex-wrap items-center gap-1.5 pt-0.5">
              <span className="text-[10px] text-[#8d909b]">Pills:</span>
              {USER_QUICK_PILLS.map(u => (
                <button
                  key={u}
                  type="button"
                  onClick={() => setUser(user === u ? '' : u)}
                  className={`px-1.5 py-0.5 rounded text-[10px] cursor-pointer transition-colors ${
                    user === u
                      ? 'bg-[#b0c9ff] text-[#002f68] font-bold'
                      : 'bg-[#1b1e31] text-[#c3c6d2] hover:bg-[#26293c] border border-[#313447]'
                  }`}
                >
                  {u}
                </button>
              ))}
            </div>
          </div>
        </div>

        {/* Real-time search filter and instant highlights */}
        <div className="pt-2 border-t border-[#1b1e31] flex flex-wrap items-center justify-between gap-3">
          <div className="flex items-center gap-2 flex-1 min-w-[200px]">
            <span className="material-symbols-outlined text-[16px] text-[#8d909b]">search</span>
            <input
              type="text"
              value={searchTerm}
              onChange={e => setSearchTerm(e.target.value)}
              placeholder="Instant grep/search inside fetched lines..."
              className="w-full bg-[#171a2d] text-[#dfe1fa] px-2 py-1 rounded border border-[#313447] text-xs focus:outline-none focus:border-[#f9c068] font-mono"
            />
            {searchTerm && (
              <button
                type="button"
                onClick={() => setSearchTerm('')}
                className="text-[#8d909b] hover:text-[#dfe1fa] cursor-pointer text-xs"
              >
                Clear
              </button>
            )}
          </div>

          {/* Client-side Severity Quick Toggle */}
          <div className="flex items-center gap-1">
            <span className="text-[#8d909b] text-[10px]">Filter View:</span>
            {(['all', 'error', 'warning', 'info', 'debug'] as const).map(lvl => (
              <button
                key={lvl}
                type="button"
                onClick={() => setClientFilterLevel(lvl)}
                className={`px-2 py-0.5 rounded text-[10px] uppercase font-bold cursor-pointer transition-colors ${
                  clientFilterLevel === lvl
                    ? lvl === 'error'
                      ? 'bg-[#ffb4ab] text-[#690005]'
                      : lvl === 'warning'
                      ? 'bg-[#f9c068] text-[#462a00]'
                      : 'bg-[#26293c] text-[#b0c9ff] border border-[#b0c9ff]/40'
                    : 'bg-[#171a2d] text-[#8d909b] hover:text-[#dfe1fa] border border-[#26293c]'
                }`}
              >
                {lvl}
              </button>
            ))}
          </div>

          {/* Reset all filters */}
          <button
            type="button"
            onClick={handleClearFilters}
            className="text-[11px] text-[#8d909b] hover:text-[#ffb4ab] underline cursor-pointer"
          >
            Reset All
          </button>
        </div>
      </div>

      {/* Error Banner */}
      {error && (
        <div className="mb-3 px-3 py-2 rounded bg-[#93000a]/20 border border-[#ffb4ab]/40 flex items-center justify-between text-xs text-[#ffb4ab]">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[16px]">error</span>
            <span className="font-mono">{error}</span>
          </div>
          <button
            type="button"
            onClick={() => fetchLogs(true)}
            className="underline hover:text-white cursor-pointer font-mono"
          >
            Retry
          </button>
        </div>
      )}

      {/* Stats and Terminal Toolbar */}
      <div className="flex flex-wrap items-center justify-between gap-2 px-2 py-1.5 bg-[#0e1122] rounded-t-md border-t border-x border-[#26293c] font-mono text-[11px] text-[#8d909b]">
        <div className="flex items-center gap-3">
          <span className="text-[#dfe1fa] font-bold">LOG STREAM</span>
          <span>
            Shown: <strong className="text-[#dfe1fa]">{visibleEntries.length}</strong> / {severityCounts.total}
          </span>
          <span className="flex items-center gap-1 text-[#ffb4ab]">
            <span className="w-2 h-2 rounded-full bg-[#ffb4ab]"></span>
            {severityCounts.err} errors
          </span>
          <span className="flex items-center gap-1 text-[#f9c068]">
            <span className="w-2 h-2 rounded-full bg-[#f9c068]"></span>
            {severityCounts.warn} warnings
          </span>
          <span className="flex items-center gap-1 text-[#8cd6cb]">
            <span className="w-2 h-2 rounded-full bg-[#8cd6cb]"></span>
            {severityCounts.inf} info
          </span>
        </div>

        <div className="flex items-center gap-3">
          {lastRefreshed && <span>Updated: {lastRefreshed}</span>}
          <label className="flex items-center gap-1 cursor-pointer hover:text-[#dfe1fa]">
            <input
              type="checkbox"
              checked={stickToBottom}
              onChange={e => setStickToBottom(e.target.checked)}
              className="rounded accent-[#8cd6cb]"
            />
            <span>Scroll Bottom</span>
          </label>
        </div>
      </div>

      {/* Terminal Monospace Logs Container */}
      <div
        ref={terminalScrollRef}
        className="bg-[#0a0d1b] rounded-b-md border border-[#26293c] p-3 font-mono text-[11px] leading-relaxed overflow-x-auto max-h-[550px] min-h-[300px] overflow-y-auto select-text scrollbar-thin scrollbar-thumb-[#26293c]"
      >
        {loading && visibleEntries.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-16 text-[#8d909b] gap-2">
            <span className="material-symbols-outlined text-[32px] animate-spin text-[#8cd6cb]">
              sync
            </span>
            <p>Fetching remote logs from {node.shortName} via journalctl / SSH...</p>
          </div>
        ) : visibleEntries.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-16 text-[#8d909b] gap-2">
            <span className="material-symbols-outlined text-[32px] text-[#434750]">
              content_paste_off
            </span>
            <p className="font-bold text-[#dfe1fa]">No log lines matched current filters</p>
            <p className="text-[10px]">
              Try widening the time window, removing the unit/user filter, or switching source to journald.
            </p>
            <button
              type="button"
              onClick={handleClearFilters}
              className="mt-2 px-3 py-1 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] rounded border border-[#434750]/40 cursor-pointer text-xs"
            >
              Clear Filters
            </button>
          </div>
        ) : (
          <div className="space-y-0.5">
            {visibleEntries.map((entry, idx) => {
              const isError = entry.level === 'error';
              const isWarning = entry.level === 'warning';
              const isDebug = entry.level === 'debug';

              const severityColor = isError
                ? 'text-[#ffb4ab] bg-[#93000a]/10'
                : isWarning
                ? 'text-[#f9c068] bg-[#462a00]/10'
                : isDebug
                ? 'text-[#8d909b]'
                : 'text-[#dfe1fa]';

              const badgeColor = isError
                ? 'bg-[#93000a]/40 text-[#ffb4ab] border-[#ffb4ab]/30'
                : isWarning
                ? 'bg-[#5e4200]/40 text-[#f9c068] border-[#f9c068]/30'
                : isDebug
                ? 'bg-[#26293c] text-[#8d909b] border-[#434750]/40'
                : 'bg-[#003731]/40 text-[#8cd6cb] border-[#8cd6cb]/30';

              return (
                <div
                  key={entry.id || idx}
                  className={`flex items-start gap-2 py-0.5 px-1.5 rounded transition-colors hover:bg-[#1b1e31]/60 ${severityColor}`}
                >
                  {/* Line Number */}
                  <span className="w-8 shrink-0 text-right text-[#555866] select-none text-[10px]">
                    {idx + 1}
                  </span>

                  {/* Severity Badge */}
                  <span
                    className={`shrink-0 uppercase text-[9px] px-1 py-0.2 rounded border font-bold select-none ${badgeColor}`}
                  >
                    {entry.level.slice(0, 4)}
                  </span>

                  {/* Timestamp */}
                  {entry.timestamp && (
                    <span className="shrink-0 text-[#8d909b] select-none">
                      {entry.timestamp}
                    </span>
                  )}

                  {/* Unit / Service */}
                  {entry.unit && (
                    <span className="shrink-0 text-[#b0c9ff] font-semibold">
                      [{entry.unit}]
                    </span>
                  )}

                  {/* Message body */}
                  <span className="flex-1 break-all whitespace-pre-wrap">
                    {renderHighlightedMessage(entry.message || entry.raw, searchTerm)}
                  </span>
                </div>
              );
            })}
            <div ref={terminalEndRef} />
          </div>
        )}
      </div>
    </section>
  );
};

export default SystemLogsView;
