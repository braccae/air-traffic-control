import React, { useCallback, useEffect, useState } from 'react';
import type { VaultAuditEvent } from '../types/index.ts';

interface VaultAuditPanelProps {
  /** Poll interval while the tab is visible. 0 disables polling. */
  pollIntervalMs?: number;
}

function formatTimestamp(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return '—';
  const d = new Date(ms);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(
    d.getMinutes()
  )}:${pad(d.getSeconds())}`;
}

/**
 * Vault Audit tab: renders the persistent decryption audit trail served by
 * GET /api/vault/audit. Each row is metadata only — variable name, actor,
 * target host, triggering action, password source and timestamp. The decrypted
 * plaintext is never sent to this component or stored anywhere in the trail.
 */
export const VaultAuditPanel: React.FC<VaultAuditPanelProps> = ({ pollIntervalMs = 5000 }) => {
  const [events, setEvents] = useState<VaultAuditEvent[]>([]);
  const [variableFilter, setVariableFilter] = useState('');
  const [hostFilter, setHostFilter] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    const params = new URLSearchParams();
    if (variableFilter.trim()) params.set('variable', variableFilter.trim());
    if (hostFilter.trim()) params.set('host', hostFilter.trim());
    const qs = params.toString();

    try {
      const res = await fetch(`/api/vault/audit${qs ? `?${qs}` : ''}`);
      const data = await res.json();
      if (!res.ok || data?.success === false) {
        throw new Error(data?.error || `Request failed (HTTP ${res.status})`);
      }
      const list: VaultAuditEvent[] = Array.isArray(data?.events) ? data.events : [];
      // Server returns newest-first; sort defensively so the UI contract holds.
      list.sort((a, b) => b.timestamp - a.timestamp);
      setEvents(list);
      setError(null);
    } catch (err: any) {
      setError(err?.message || 'Failed to load vault audit trail');
    } finally {
      setLoading(false);
    }
  }, [variableFilter, hostFilter]);

  useEffect(() => {
    setLoading(true);
    load();
  }, [load]);

  useEffect(() => {
    if (!pollIntervalMs) return;
    const timer = setInterval(load, pollIntervalMs);
    return () => clearInterval(timer);
  }, [load, pollIntervalMs]);

  return (
    <div className="flex flex-col h-full bg-[#0a0d1f] font-mono text-xs text-[#c3c6d2]">
      <div className="flex items-center gap-2 px-3 py-1.5 bg-[#121528] border-b border-[#26293c] shrink-0">
        <div className="relative">
          <input
            type="text"
            value={variableFilter}
            onChange={e => setVariableFilter(e.target.value)}
            placeholder="Filter variable..."
            aria-label="Filter vault audit by variable"
            className="bg-[#171a2d] border border-[#26293c] focus:border-[#b0c9ff] rounded px-2 py-0.5 text-xs text-[#dfe1fa] placeholder-[#8d909b] outline-none w-40"
          />
        </div>
        <input
          type="text"
          value={hostFilter}
          onChange={e => setHostFilter(e.target.value)}
          placeholder="Filter host..."
          aria-label="Filter vault audit by host"
          className="bg-[#171a2d] border border-[#26293c] focus:border-[#b0c9ff] rounded px-2 py-0.5 text-xs text-[#dfe1fa] placeholder-[#8d909b] outline-none w-40"
        />
        <button
          type="button"
          onClick={load}
          className="px-2 py-0.5 rounded border border-[#313447] text-[#b0c9ff] hover:bg-[#26293c] cursor-pointer"
        >
          Refresh
        </button>
        <span className="text-[10px] text-[#8d909b] ml-auto">
          {events.length} decryption {events.length === 1 ? 'event' : 'events'}
        </span>
      </div>

      <div className="flex-1 overflow-auto p-3 space-y-1">
        {error && (
          <div className="text-[#ffb4ab] flex items-center gap-1.5">
            <span className="material-symbols-outlined text-[14px]">error</span>
            <span>{error}</span>
          </div>
        )}

        {!error && loading && events.length === 0 && (
          <div className="text-[#8d909b]">Loading vault audit trail…</div>
        )}

        {!error && !loading && events.length === 0 && (
          <div className="text-[#8d909b] flex items-center gap-1.5">
            <span className="material-symbols-outlined text-[14px]">lock</span>
            <span>No vault decryption events recorded yet.</span>
          </div>
        )}

        {events.map(ev => (
          <div
            key={ev.id}
            className="hover:bg-[#171a2d] px-1 rounded flex items-center gap-2 whitespace-nowrap"
            data-testid="vault-audit-row"
          >
            <span className="material-symbols-outlined text-[14px] text-[#d9b9ff]">key</span>
            <span className="text-[#8d909b]">{formatTimestamp(ev.timestamp)}</span>
            <span className="text-[#d9b9ff] font-bold">{ev.variableName}</span>
            <span className="text-[#8d909b]">host:</span>
            <span className="text-[#8cd6cb]">{ev.host}</span>
            <span className="text-[#8d909b]">actor:</span>
            <span className="text-[#dfe1fa]">{ev.actor}</span>
            <span className="text-[#8d909b]">action:</span>
            <span className="text-[#dfe1fa]">{ev.action}</span>
            <span className="text-[#8d909b]">pw:</span>
            <span className="text-[#8d909b]">{ev.passwordSource}</span>
          </div>
        ))}
      </div>
    </div>
  );
};
