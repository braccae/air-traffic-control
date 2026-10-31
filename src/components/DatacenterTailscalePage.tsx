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
const TAILSCALE_SELF_ENDPOINT = '/api/tailscale/self';

/** Live registration state of ATC's own tsnet node (never contains the auth key). */
export interface TsnetSelfStatus {
  configured: boolean;
  connected: boolean;
  online: boolean;
  mode: string; // "tsnet" | "host-socket" | "unconfigured"
  hostname: string;
  requestedHostname?: string;
  nodeId?: string;
  dnsName?: string;
  tailscaleIps: string[];
  backendState?: string;
  authUrl?: string;
  health?: string[];
  keyExpiry?: string;
  peerCount?: number;
  lastUpAt?: string;
  stateDir: string;
  ephemeral: boolean;
  proxy?: {
    mode: string; // "off" | "socks" | "http" | "both"
    socks?: string;
    http?: string;
    authenticated: boolean;
    credentialsFile?: string;
    error?: string;
  };
  error?: string;
}

const StatusRow: React.FC<{ label: string; value: React.ReactNode }> = ({ label, value }) => (
  <div className="flex gap-2 min-w-0">
    <span className="text-[#8d909b] shrink-0">{label}:</span>
    <span className="text-[#dfe1fa] truncate" title={typeof value === 'string' ? value : undefined}>
      {value}
    </span>
  </div>
);

export const DatacenterTailscalePage: React.FC = () => {
  const [page, setPage] = useState<GlobalSettingsPage | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [self, setSelf] = useState<TsnetSelfStatus | null>(null);
  const [selfLoading, setSelfLoading] = useState(true);
  const [selfError, setSelfError] = useState<string | null>(null);
  const [repairing, setRepairing] = useState(false);
  const [cleaning, setCleaning] = useState(false);
  const [cleanupMessage, setCleanupMessage] = useState<string | null>(null);

  const loadSelf = useCallback(async () => {
    setSelfLoading(true);
    setSelfError(null);
    try {
      const res = await fetch(TAILSCALE_SELF_ENDPOINT);
      const data = await res.json().catch(() => null);
      if (!res.ok || !data?.success) {
        throw new Error(data?.error || `Failed to read tsnet status (HTTP ${res.status})`);
      }
      setSelf(data.self as TsnetSelfStatus);
    } catch (err: any) {
      setSelfError(err?.message || 'Failed to read tsnet status');
    } finally {
      setSelfLoading(false);
    }
  }, []);

  const repairMesh = useCallback(async () => {
    setRepairing(true);
    try {
      const res = await fetch('/api/tailscale/repair', { method: 'POST' });
      const data = await res.json().catch(() => null);
      if (data?.self) setSelf(data.self as TsnetSelfStatus);
    } catch {
      // fall through to recheck
    } finally {
      setRepairing(false);
      await loadSelf();
    }
  }, [loadSelf]);

  const cleanupDevices = useCallback(async () => {
    if (!window.confirm('Remove offline Tailscale devices that still hold this hostname, then rename this node? Online devices are never touched.')) {
      return;
    }
    setCleaning(true);
    setCleanupMessage(null);
    try {
      const res = await fetch('/api/tailscale/devices/cleanup', { method: 'POST' });
      const data = await res.json().catch(() => null);
      if (!res.ok || !data?.success) {
        throw new Error(data?.error || `Cleanup failed (HTTP ${res.status})`);
      }
      setCleanupMessage(data.result?.message || 'Cleanup complete');
      await loadSelf();
    } catch (err: any) {
      setCleanupMessage(err?.message || 'Cleanup failed');
    } finally {
      setCleaning(false);
    }
  }, [loadSelf]);

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

  useEffect(() => {
    void loadSelf();
  }, [loadSelf]);

  const save = useCallback(
    async (values: Record<string, unknown>) => {
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
      // Re-resolve the live tsnet node: saving a new auth key registers it now.
      void loadSelf();
      return updated;
    },
    [loadSelf]
  );

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

  const selfBadge = (() => {
    if (selfLoading && !self) {
      return { label: 'Checking…', className: 'bg-[#1b2a38] text-[#8cd6cb] border-[#8cd6cb]/30' };
    }
    if (selfError && !self) {
      return { label: 'Unknown', className: 'bg-[#311c1c] text-[#ffb4ab] border-[#ffb4ab]/30' };
    }
    if (!self) {
      return { label: 'Unknown', className: 'bg-[#1b2a38] text-[#8d909b] border-[#26293c]' };
    }
    if (!self.configured) {
      return { label: 'Not configured', className: 'bg-[#311c1c] text-[#ffb4ab] border-[#ffb4ab]/30' };
    }
    if (self.connected && self.online) {
      return { label: 'Online', className: 'bg-[#162620] text-[#8cd6cb] border-[#8cd6cb]/30' };
    }
    if (self.connected && !self.online) {
      return { label: 'Backend up, control says offline', className: 'bg-[#291e0f] text-[#ffd580] border-[#ffd580]/30' };
    }
    return {
      label: self.backendState || 'Not connected',
      className: 'bg-[#291e0f] text-[#ffd580] border-[#ffd580]/30',
    };
  })();

  return (
    <div className="flex flex-col gap-3">
      <div
        data-testid="tsnet-self-status"
        className="rounded-lg bg-[#121528] border border-[#26293c] p-3.5 shadow-md flex flex-col gap-2"
      >
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[18px] text-[#8cd6cb]">lan</span>
            <h2 className="text-xs font-mono font-bold text-[#dfe1fa] uppercase tracking-wider">
              ATC Mesh Identity
            </h2>
            <span
              data-testid="tsnet-self-badge"
              className={`text-[10px] font-mono px-1.5 py-0.5 rounded border ${selfBadge.className}`}
            >
              {selfBadge.label}
            </span>
          </div>
          <button
            type="button"
            onClick={() => void loadSelf()}
            disabled={selfLoading}
            className="px-2.5 py-1 rounded text-xs font-mono text-[#dfe1fa] bg-[#171a2d] hover:bg-[#21263d] border border-[#26293c] transition-colors flex items-center gap-1.5 cursor-pointer disabled:opacity-50"
            title="Re-read ATC's live tsnet registration state"
          >
            <span className="material-symbols-outlined text-[14px]">sync</span>
            <span>{selfLoading ? 'Checking…' : 'Recheck'}</span>
          </button>
          <button
            type="button"
            onClick={() => void repairMesh()}
            disabled={repairing}
            className="px-2.5 py-1 rounded text-xs font-mono text-[#dfe1fa] bg-[#1f6feb] hover:bg-[#388bfd] border border-[#1f6feb] transition-colors flex items-center gap-1.5 cursor-pointer disabled:opacity-50"
            title="Run the tsnet self-heal ladder (force-netmap-update, rebind, restart)"
          >
            <span className="material-symbols-outlined text-[14px]">build</span>
            <span>{repairing ? 'Repairing…' : 'Repair mesh'}</span>
          </button>
        </div>

        {selfError && (
          <div role="alert" className="text-[11px] font-mono text-[#ffb4ab]">
            Could not read ATC's tsnet status: {selfError}
          </div>
        )}

        {selfLoading && !self && (
          <p className="text-[11px] font-mono text-[#8d909b]">
            Starting ATC's tsnet node and reading its registration state…
          </p>
        )}

        {self && (
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-x-4 gap-y-1 text-[11px] font-mono">
            <StatusRow label="Transport" value={self.mode} />
            <StatusRow label="Node" value={self.hostname || '—'} />
            {self.requestedHostname && self.requestedHostname !== self.hostname && (
              <StatusRow label="Requested name" value={self.requestedHostname} />
            )}
            {self.nodeId && <StatusRow label="Node ID" value={self.nodeId} />}
            {self.dnsName && <StatusRow label="Mesh name" value={self.dnsName} />}
            <StatusRow
              label="Addresses"
              value={self.tailscaleIps.length > 0 ? self.tailscaleIps.join(', ') : 'none assigned'}
            />
            {self.backendState && <StatusRow label="Backend" value={self.backendState} />}
            <StatusRow label="Online (control)" value={self.online ? 'yes' : 'no'} />
            {self.peerCount !== undefined && (
              <StatusRow label="Peers in netmap" value={self.peerCount} />
            )}
            {self.keyExpiry && <StatusRow label="Node key expiry" value={self.keyExpiry} />}
            {self.health && self.health.length > 0 && (
              <StatusRow label="Health" value={self.health.join('; ')} />
            )}
            <StatusRow label="State dir" value={self.stateDir} />
            <StatusRow
              label="Ephemeral"
              value={`${self.ephemeral ? 'yes' : 'no'} (set by auth key at registration)`}
            />
            {self.proxy && self.proxy.mode !== 'off' && (
              <>
                <StatusRow label="SOCKS5 proxy" value={self.proxy.socks || '—'} />
                <StatusRow label="HTTP proxy" value={self.proxy.http || '—'} />
                <StatusRow
                  label="Proxy auth"
                  value={self.proxy.authenticated ? 'required' : 'none (loopback only)'}
                />
                {self.proxy.credentialsFile && (
                  <StatusRow label="Proxy credentials" value={self.proxy.credentialsFile} />
                )}
              </>
            )}
          </div>
        )}

        {self?.proxy?.error && (
          <div role="alert" className="text-[11px] font-mono text-[#ffb4ab]">
            Userspace proxy: {self.proxy.error}
          </div>
        )}

        {self?.requestedHostname && self?.requestedHostname !== self?.hostname && (
          <div className="rounded border border-[#ffd580]/30 bg-[#291e0f] p-2.5 flex flex-col gap-2 text-[11px] font-mono">
            <p className="text-[#ffd580]">
              Tailscale assigned <span className="font-bold">{self.hostname}</span> because{' '}
              <span className="font-bold">{self.requestedHostname}</span> is still held by another device.
              Remove the stale device, then ATC renames this node back.
            </p>
            <div className="flex flex-wrap items-center gap-2">
              <button
                type="button"
                onClick={() => void cleanupDevices()}
                disabled={cleaning}
                className="px-2 py-1 rounded text-[11px] font-mono font-bold text-white bg-[#1f6feb] hover:bg-[#388bfd] disabled:opacity-50 cursor-pointer"
              >
                {cleaning ? 'Cleaning…' : 'Remove stale devices'}
              </button>
              <a
                href="https://login.tailscale.com/admin/machines"
                target="_blank"
                rel="noreferrer"
                className="text-[#79c0ff] underline"
              >
                Open admin console
              </a>
            </div>
            {cleanupMessage && <p className="text-[#dfe1fa]">{cleanupMessage}</p>}
          </div>
        )}

        {self?.error && (
          <div role="alert" className="text-[11px] font-mono text-[#ffb4ab]">
            {self.error}
          </div>
        )}

        {self?.authUrl && (
          <a
            href={self.authUrl}
            target="_blank"
            rel="noreferrer"
            className="text-[11px] font-mono text-[#79c0ff] underline"
          >
            Authorize this node in Tailscale
          </a>
        )}

        {self?.mode === 'host-socket' && (
          <p className="text-[11px] font-mono text-[#ffd580]">
            ATC is borrowing the host's tailscaled socket (local development fallback). It has no
            tsnet identity of its own until an auth key is saved.
          </p>
        )}
      </div>
      <GlobalSettingsPanel page={page} save={save} />
      <p className="text-[11px] text-[#8d909b] font-mono leading-relaxed">
        ATC always runs Tailscale in userspace: no <span className="text-[#dfe1fa]">/dev/net/tun</span> and
        no kernel routing. When the daemon cannot set <span className="text-[#dfe1fa]">CAP_NET_ADMIN</span>,
        the SOCKS5/HTTP proxy listeners give sibling services access to the tailnet. They bind to loopback
        on a host and to <span className="text-[#dfe1fa]">0.0.0.0</span> inside a container, with
        credentials required whenever they are reachable beyond loopback. The host-socket fallback is an
        explicit local-development option and is never enabled automatically.
      </p>
    </div>
  );
};

export default DatacenterTailscalePage;
