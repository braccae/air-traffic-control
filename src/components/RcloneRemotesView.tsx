import React, { useCallback, useEffect, useMemo, useState } from 'react';
import type {
  GlobalSettingsPage,
  NodeItem,
  RcloneMountPolicy,
  RcloneMountRecord,
} from '../types/index.ts';
import type { PluginViewProps } from '../plugins/views/index.tsx';
import { previewMountArtifacts } from '../utils/rclonePolicies.ts';
import { fetchGlobalSettingsPages } from '../utils/globalSettingsManager.ts';
import { GlobalSettingsPanel } from './GlobalSettingsPanel.tsx';

interface ProviderField {
  name: string;
  label: string;
  required: boolean;
  secret: boolean;
  obscure: boolean;
  default?: string;
  description?: string;
}
interface Provider {
  id: string;
  label: string;
  type: string;
  fields: ProviderField[];
}
interface RemoteView {
  id: string;
  name: string;
  provider: string;
  type: string;
  fields: Record<string, string>;
  createdAt: number;
  updatedAt: number;
}

const POLICIES: Array<{ id: RcloneMountPolicy; label: string; hint: string }> = [
  {
    id: 'systemd-mount',
    label: 'systemd mount',
    hint: 'rclone-<name>.service + .service.d/atc-rclone.conf drop-in (ansible.builtin.systemd_service)',
  },
  {
    id: 'systemd-automount',
    label: 'systemd automount',
    hint: '<escaped-target>.mount + .mount.d/atc-rclone.conf + .automount pair, mounted on first access',
  },
  { id: 'fstab', label: 'fstab', hint: '/etc/fstab entry with x-systemd.automount,_netdev' },
  {
    id: 'script',
    label: 'service script',
    hint: '/usr/local/bin/rclone-mount-<name>.sh run by rclone-<name>.service',
  },
];

const card = 'bg-[#171a2d] border border-[#26293c] rounded p-4';
const btn =
  'px-2.5 py-1 rounded bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] text-xs font-semibold border border-[#434750]/40 cursor-pointer disabled:opacity-50';
const btnPrimary =
  'px-2.5 py-1 rounded bg-[#2f6fed] hover:bg-[#3b7bfa] text-white text-xs font-semibold border border-[#3b7bfa]/40 cursor-pointer disabled:opacity-50';
const input = 'w-full bg-[#0f1220] border border-[#2a2e44] rounded px-2 py-1 text-xs text-[#dfe1fa]';

export interface RcloneRemotesViewProps extends Partial<PluginViewProps> {
  /**
   * `client` (default when `node` is passed): per-client mount policy configuration
   * (`systemd-mount` vs `systemd-automount` vs `fstab` vs `script`) plus remote
   * installation to this node.
   *
   * `datacenter` (default when `node` is omitted): datacenter-wide rclone remote
   * management (guided `rclone.conf` setup, Ansible Vault DB storage, global plugin
   * settings defaults, and installing selected remotes to connected devices).
   */
  mode?: 'client' | 'datacenter';
}

export const RcloneRemotesView: React.FC<RcloneRemotesViewProps> = ({ node, mode }) => {
  const effectiveMode: 'client' | 'datacenter' = mode ?? (node ? 'client' : 'datacenter');

  const [providers, setProviders] = useState<Provider[]>([]);
  const [remotes, setRemotes] = useState<RemoteView[]>([]);
  const [mounts, setMounts] = useState<RcloneMountRecord[]>([]);
  const [nodes, setNodes] = useState<NodeItem[]>([]);
  const [globalSettingsPage, setGlobalSettingsPage] = useState<GlobalSettingsPage | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Guided setup state
  const [providerId, setProviderId] = useState('sftp');
  const [remoteName, setRemoteName] = useState('');
  const [fieldValues, setFieldValues] = useState<Record<string, string>>({});
  const [verifyOnSave, setVerifyOnSave] = useState(true);

  // Install state
  const [installRemoteId, setInstallRemoteId] = useState<string | null>(null);
  const [installNodeId, setInstallNodeId] = useState(node?.id || '');
  const [mountTarget, setMountTarget] = useState('/mnt/rclone/');
  const [policy, setPolicy] = useState<RcloneMountPolicy>('systemd-mount');
  const [mountOptions, setMountOptions] = useState('--vfs-cache-mode writes --dir-cache-time 5m');

  const provider = useMemo(() => providers.find(p => p.id === providerId), [providers, providerId]);

  const refresh = useCallback(async () => {
    try {
      const [pRes, rRes, nRes] = await Promise.all([
        fetch('/api/rclone/providers'),
        fetch('/api/rclone/remotes'),
        fetch('/api/nodes'),
      ]);
      if (pRes.ok) setProviders((await pRes.json()).providers || []);
      if (rRes.ok) setRemotes((await rRes.json()).remotes || []);
      if (nRes.ok) {
        const nJson = await nRes.json();
        const list = Array.isArray(nJson) ? nJson : nJson?.nodes || [];
        setNodes(Array.isArray(list) ? list : []);
      }
    } catch (err: any) {
      setNotice(`Error: ${err?.message || 'Failed to load rclone data'}`);
    }

    if (effectiveMode === 'datacenter') {
      try {
        const pages = await fetchGlobalSettingsPages();
        const rclonePage = pages.find(p => p.id === 'mount/rclone') || null;
        setGlobalSettingsPage(rclonePage);
      } catch {
        /* global settings optional when daemon is offline */
      }
    }

    if (node?.id) {
      try {
        const mRes = await fetch(`/api/rclone/nodes/${encodeURIComponent(node.id)}/mounts`);
        if (mRes.ok) setMounts((await mRes.json()).mounts || []);
      } catch {
        /* mounts are best-effort */
      }
    }
  }, [node?.id, effectiveMode]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  useEffect(() => {
    if (node?.id) {
      setInstallNodeId(node.id);
    }
  }, [node?.id]);

  // Seed default field values when the selected provider changes.
  useEffect(() => {
    if (!provider) return;
    const next: Record<string, string> = {};
    for (const f of provider.fields) next[f.name] = f.default ?? '';
    setFieldValues(next);
  }, [providerId, provider]);

  const saveRemote = async () => {
    setBusy(true);
    setNotice(null);
    try {
      const res = await fetch('/api/rclone/remotes', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: remoteName,
          provider: providerId,
          fields: fieldValues,
          verify: verifyOnSave,
        }),
      });
      const data = await res.json();
      if (!res.ok || !data.success) {
        throw new Error(data.error || `Save failed (HTTP ${res.status})`);
      }
      setNotice(
        verifyOnSave
          ? `Remote "${data.remote.name}" verified against the provider and stored in Ansible Vault.`
          : `Remote "${data.remote.name}" stored in Ansible Vault.`
      );
      setRemoteName('');
      await refresh();
    } catch (err: any) {
      setNotice(`Error: ${err?.message || 'Failed to save remote'}`);
    } finally {
      setBusy(false);
    }
  };

  const verifyRemote = async (id: string) => {
    setBusy(true);
    setNotice(null);
    try {
      const res = await fetch(`/api/rclone/remotes/${encodeURIComponent(id)}/verify`, { method: 'POST' });
      const data = await res.json();
      if (data.success) setNotice(`Verified: ${data.verification.output || 'remote reachable'}`);
      else setNotice(`Error: verification failed — ${data.verification?.error || data.error}`);
    } catch (err: any) {
      setNotice(`Error: ${err?.message || 'Verification failed'}`);
    } finally {
      setBusy(false);
    }
  };

  const deleteRemote = async (id: string) => {
    setBusy(true);
    try {
      await fetch(`/api/rclone/remotes/${encodeURIComponent(id)}`, { method: 'DELETE' });
      await refresh();
    } finally {
      setBusy(false);
    }
  };

  const installRemote = async () => {
    if (!installRemoteId) return;
    const targetNodeId = effectiveMode === 'client' && node?.id ? node.id : installNodeId;
    if (!targetNodeId) return;
    setBusy(true);
    setNotice(null);
    try {
      const res = await fetch(`/api/rclone/remotes/${encodeURIComponent(installRemoteId)}/install`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ nodeId: targetNodeId, mountTarget, policy, mountOptions }),
      });
      const data = await res.json();
      if (!res.ok || !data.success) {
        throw new Error(data.error || `Install failed (HTTP ${res.status})`);
      }
      setNotice(
        `Staged "${data.task?.title || 'install task'}" (${policy}) into the Ansible queue for ${
          data.mount?.nodeId || targetNodeId
        }.`
      );
      setInstallRemoteId(null);
      await refresh();
    } catch (err: any) {
      setNotice(`Error: ${err?.message || 'Install failed'}`);
    } finally {
      setBusy(false);
    }
  };

  const changePolicy = async (mount: RcloneMountRecord, nextPolicy: RcloneMountPolicy) => {
    setBusy(true);
    setNotice(null);
    try {
      const res = await fetch(`/api/rclone/remotes/${encodeURIComponent(mount.remoteId)}/install`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          nodeId: mount.nodeId,
          mountTarget: mount.mountTarget,
          policy: nextPolicy,
          mountOptions,
        }),
      });
      const data = await res.json();
      if (!res.ok || !data.success) throw new Error(data.error || 'Policy change failed');
      setNotice(`Re-staged "${mount.remoteName}" with policy ${nextPolicy}.`);
      await refresh();
    } catch (err: any) {
      setNotice(`Error: ${err?.message || 'Policy change failed'}`);
    } finally {
      setBusy(false);
    }
  };

  const preview =
    installRemoteId && remotes.find(r => r.id === installRemoteId)
      ? previewMountArtifacts(
          policy,
          remotes.find(r => r.id === installRemoteId)!.name,
          mountTarget || '/mnt/rclone/x',
          mountOptions
        )
      : [];

  return (
    <div
      className="flex flex-col gap-3 font-mono"
      data-testid={effectiveMode === 'datacenter' ? 'datacenter-rclone-remotes-settings' : 'client-rclone-mounts-view'}
    >
      <div className={`${card} flex items-center justify-between`}>
        <div className="flex items-center gap-2">
          <span className="material-symbols-outlined text-[#b0c9ff] text-[22px]">cloud_queue</span>
          <div>
            <h3 className="text-sm font-bold text-[#dfe1fa]">
              {effectiveMode === 'datacenter'
                ? 'Datacenter rclone Remotes & Vault Store'
                : `rclone Mount Policies — ${node?.name || 'this client'}`}
            </h3>
            <span className="text-[11px] text-[#8d909b]">
              {effectiveMode === 'datacenter'
                ? `${remotes.length} remote(s) stored in ATC DB · Secrets encrypted in Ansible Vault (vault_variables)`
                : `${mounts.length} mount(s) on ${node?.name || 'this client'} · ${remotes.length} datacenter remote(s) available`}
            </span>
          </div>
        </div>
        <button className={btn} onClick={refresh} disabled={busy}>
          <span className="material-symbols-outlined text-[14px] align-middle">refresh</span> Refresh
        </button>
      </div>

      {notice && (
        <div
          className={`p-3 rounded text-xs border ${
            notice.startsWith('Error')
              ? 'bg-[#331114] text-[#ffb4ab] border-[#ffb4ab]/30'
              : 'bg-[#0f231e] text-[#8cd6cb] border-[#8cd6cb]/30'
          }`}
        >
          {notice}
        </div>
      )}

      {/* Datacenter global module settings (contributed by mount/rclone SettingsPageProvider) */}
      {effectiveMode === 'datacenter' && globalSettingsPage && (
        <GlobalSettingsPanel
          page={globalSettingsPage}
          onSaved={updated => setGlobalSettingsPage(updated)}
        />
      )}

      {/* Guided remote setup (datacenter-wide rclone.conf wizard) */}
      <div className={card} data-testid="rclone-guided-setup-card">
        <div className="flex items-center justify-between mb-2">
          <span className="text-[10px] uppercase font-bold text-[#8d909b]">
            Guided remote setup (datacenter rclone.conf)
          </span>
          {effectiveMode === 'client' && (
            <a
              href="#/datacenter/settings/storage/rclone-remotes"
              className="text-[10px] text-[#79c0ff] hover:underline"
            >
              Open in Datacenter Settings →
            </a>
          )}
        </div>
        <div className="grid grid-cols-1 gap-2">
          <label className="text-[11px] text-[#8d909b]">
            Provider
            <select className={input} value={providerId} onChange={e => setProviderId(e.target.value)}>
              {providers.map(p => (
                <option key={p.id} value={p.id}>
                  {p.label}
                </option>
              ))}
            </select>
          </label>
          <label className="text-[11px] text-[#8d909b]">
            Remote name
            <input
              className={input}
              value={remoteName}
              placeholder="hetzner"
              onChange={e => setRemoteName(e.target.value)}
            />
          </label>
          {provider?.fields.map(f => (
            <label key={f.name} className="text-[11px] text-[#8d909b]">
              {f.label}
              {f.required ? ' *' : ''}
              <input
                className={input}
                type={f.secret ? 'password' : 'text'}
                value={fieldValues[f.name] ?? ''}
                placeholder={f.description || ''}
                onChange={e => setFieldValues(v => ({ ...v, [f.name]: e.target.value }))}
              />
            </label>
          ))}
        </div>
        <div className="flex items-center gap-3 mt-3">
          <label className="text-[11px] text-[#8d909b] flex items-center gap-1">
            <input type="checkbox" checked={verifyOnSave} onChange={e => setVerifyOnSave(e.target.checked)} />
            Verify against the provider before saving
          </label>
          <button className={btnPrimary} onClick={saveRemote} disabled={busy || !remoteName}>
            {verifyOnSave ? 'Verify & Save' : 'Save remote'}
          </button>
        </div>
        <p className="text-[10px] text-[#6f727d] mt-2">
          Secrets are encrypted in ATC's Ansible Vault store (<code>vault_variables</code>) and masked as{' '}
          <code>•••••••• (vault: rclone_&lt;remote&gt;_&lt;field&gt;)</code>. Every decryption is logged to the Vault
          Audit trail.
        </p>
      </div>

      {/* Remote list */}
      <div className={card} data-testid="rclone-configured-remotes-card">
        <span className="text-[10px] uppercase font-bold text-[#8d909b] block mb-2">
          Configured datacenter remotes
        </span>
        {remotes.length === 0 ? (
          <p className="text-xs text-[#6f727d]">No remotes yet. Create one above.</p>
        ) : (
          <table className="w-full text-xs">
            <thead>
              <tr className="text-left text-[#8d909b]">
                <th className="py-1">Name</th>
                <th>Provider</th>
                <th>Vault Secrets</th>
                <th className="text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {remotes.map(r => (
                <tr key={r.id} className="border-t border-[#26293c]">
                  <td className="py-1 text-[#dfe1fa]">{r.name}</td>
                  <td className="text-[#b0c9ff]">{r.provider}</td>
                  <td className="text-[#8cd6cb]">
                    {Object.entries(r.fields)
                      .filter(([, v]) => String(v).startsWith('••••••••'))
                      .map(([k, v]) => `${k}: ${v}`)
                      .join(', ') || '—'}
                  </td>
                  <td className="text-right">
                    <button className={btn} onClick={() => verifyRemote(r.id)} disabled={busy}>
                      Verify
                    </button>{' '}
                    <button
                      className={btn}
                      onClick={() => {
                        setInstallRemoteId(r.id);
                        if (node?.id) setInstallNodeId(node.id);
                        setMountTarget(`/mnt/rclone/${r.name}`);
                      }}
                      disabled={busy}
                    >
                      {effectiveMode === 'client' && node ? `Install on ${node.name}` : 'Install to Device'}
                    </button>{' '}
                    <button className={btn} onClick={() => deleteRemote(r.id)} disabled={busy}>
                      Delete
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {/* Install panel */}
      {installRemoteId && (
        <div className={card} data-testid="rclone-install-panel">
          <span className="text-[10px] uppercase font-bold text-[#8d909b] block mb-2">
            Install to device — mount policy
          </span>
          <div className="grid grid-cols-2 gap-2">
            <label className="text-[11px] text-[#8d909b]">
              Target device
              <select
                className={input}
                value={effectiveMode === 'client' && node?.id ? node.id : installNodeId}
                onChange={e => setInstallNodeId(e.target.value)}
                disabled={effectiveMode === 'client' && Boolean(node?.id)}
              >
                <option value="">Select a device…</option>
                {nodes.map(n => (
                  <option key={n.id} value={n.id}>
                    {n.name}
                  </option>
                ))}
              </select>
            </label>
            <label className="text-[11px] text-[#8d909b]">
              Mount target
              <input className={input} value={mountTarget} onChange={e => setMountTarget(e.target.value)} />
            </label>
            <label className="text-[11px] text-[#8d909b]">
              Policy
              <select className={input} value={policy} onChange={e => setPolicy(e.target.value as RcloneMountPolicy)}>
                {POLICIES.map(p => (
                  <option key={p.id} value={p.id}>
                    {p.label} — {p.hint}
                  </option>
                ))}
              </select>
            </label>
            <label className="text-[11px] text-[#8d909b]">
              Mount options
              <input className={input} value={mountOptions} onChange={e => setMountOptions(e.target.value)} />
            </label>
          </div>
          <div className="text-[10px] text-[#6f727d] mt-2">
            Installs: {preview.join(', ') || '—'}
          </div>
          <div className="flex gap-2 mt-3">
            <button
              className={btnPrimary}
              onClick={installRemote}
              disabled={busy || !(effectiveMode === 'client' && node?.id ? node.id : installNodeId)}
            >
              Stage install
            </button>
            <button className={btn} onClick={() => setInstallRemoteId(null)} disabled={busy}>
              Cancel
            </button>
          </div>
        </div>
      )}

      {/* Per-client mounts + policy (shown on client node pages) */}
      {(effectiveMode === 'client' || Boolean(node)) && (
        <div className={card} data-testid="rclone-client-mounts-card">
          <span className="text-[10px] uppercase font-bold text-[#8d909b] block mb-2">
            Per-client mount policy on {node?.name || 'this client'}
          </span>
          {mounts.length === 0 ? (
            <p className="text-xs text-[#6f727d]">
              No remotes installed on this client. Select a remote above to install and configure its mount policy.
            </p>
          ) : (
            <table className="w-full text-xs">
              <thead>
                <tr className="text-left text-[#8d909b]">
                  <th className="py-1">Remote</th>
                  <th>Target</th>
                  <th>Status</th>
                  <th>Mount policy (systemd-mount / systemd-automount / fstab / script)</th>
                  <th>Artifacts</th>
                </tr>
              </thead>
              <tbody>
                {mounts.map(m => (
                  <tr key={`${m.nodeId}-${m.remoteId}`} className="border-t border-[#26293c]">
                    <td className="py-1 text-[#dfe1fa]">{m.remoteName}</td>
                    <td className="text-[#b0c9ff]">{m.mountTarget}</td>
                    <td className={m.status === 'failed' ? 'text-[#ffb4ab]' : 'text-[#8cd6cb]'}>
                      {m.status}
                      {m.error ? ` — ${m.error}` : ''}
                    </td>
                    <td>
                      <select
                        className={input}
                        value={m.policy}
                        onChange={e => changePolicy(m, e.target.value as RcloneMountPolicy)}
                        disabled={busy}
                      >
                        {POLICIES.map(p => (
                          <option key={p.id} value={p.id}>
                            {p.label}
                          </option>
                        ))}
                      </select>
                    </td>
                    <td className="text-[10px] text-[#8d909b]">
                      {previewMountArtifacts(m.policy, m.remoteName, m.mountTarget, mountOptions).join(', ')}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}
    </div>
  );
};
