import React, { useCallback, useEffect, useState } from 'react';
import type { GlobalSettingsPage } from '../types/index.ts';
import { GlobalSettingsPanel } from './GlobalSettingsPanel.tsx';

/**
 * Datacenter-scoped settings page for ATC's own Tailscale node.
 *
 * Values are read from and written to GET/PUT /api/settings/datacenter/connection/tailscale,
 * the same SQLite-backed settings row the connection/tailscale plugin resolves its
 * live tsnet configuration from. The auth key stays write-only: the API only ever
 * returns its mask, and GlobalSettingsPanel never loads a mask into the input.
 */
const TAILSCALE_SETTINGS_ENDPOINT = '/api/settings/datacenter/connection/tailscale';

export const DatacenterTailscalePage: React.FC = () => {
  const [page, setPage] = useState<GlobalSettingsPage | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await fetch(TAILSCALE_SETTINGS_ENDPOINT);
      if (!res.ok) {
        const text = await res.text().catch(() => '');
        throw new Error(text || `Failed to load Tailscale settings (HTTP ${res.status})`);
      }
      const data = (await res.json()) as GlobalSettingsPage;
      setPage(data);
    } catch (err: any) {
      setError(err?.message || 'Failed to load Tailscale settings');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const save = useCallback(async (values: Record<string, unknown>) => {
    const res = await fetch(TAILSCALE_SETTINGS_ENDPOINT, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ values }),
    });
    if (!res.ok) {
      const text = await res.text().catch(() => '');
      throw new Error(text || `Failed to save Tailscale settings (HTTP ${res.status})`);
    }
    const updated = (await res.json()) as GlobalSettingsPage;
    setPage(updated);
    return updated;
  }, []);

  if (loading) {
    return (
      <div className="p-3 rounded-lg bg-[#171a2d] border border-[#26293c] text-xs font-mono text-[#8d909b]">
        Loading Tailscale mesh settings...
      </div>
    );
  }

  if (error) {
    return (
      <div
        role="alert"
        className="p-3 rounded-lg bg-[#311c1c] border border-[#ffb4ab]/30 text-xs font-mono text-[#ffb4ab] flex flex-wrap items-center justify-between gap-2"
      >
        <span>{error}</span>
        <button
          type="button"
          onClick={load}
          className="px-2 py-1 rounded text-[11px] font-mono font-bold text-white bg-[#1f6feb] hover:bg-[#388bfd] cursor-pointer"
        >
          Retry
        </button>
      </div>
    );
  }

  if (!page) {
    return (
      <div className="p-3 rounded-lg bg-[#311c1c] border border-[#ffb4ab]/30 text-xs font-mono text-[#ffb4ab]">
        The daemon returned no Tailscale settings page.
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-3">
      <GlobalSettingsPanel page={page} save={save} />
      <p className="text-[11px] text-[#8d909b] font-mono leading-relaxed">
        With an auth key set, ATC joins the Tailscale mesh as its own node (direct mesh connectivity).
        The host-socket fallback is a local-development convenience only: it borrows the host's
        tailscaled socket when no auth key is configured. Leave it disabled in production.
      </p>
    </div>
  );
};

export default DatacenterTailscalePage;
