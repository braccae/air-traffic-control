import React, { useState, useEffect, useCallback } from 'react';
import {
  findDatacenterSettingCategory,
  findDatacenterSettingPage,
  formatDatacenterRoute,
  getDatacenterSettingPages,
  type DatacenterSettingPageId,
} from '../utils/navigationViewModel.ts';
import {
  loadQuickCommands,
  addQuickCommand,
  updateQuickCommand,
  deleteQuickCommand,
  resetQuickCommands,
  QUICK_COMMAND_EVENT,
  type QuickCommandItem,
  type QuickCommandCategory,
} from '../utils/quickCommandsManager.ts';
import type { AnsibleConfigSettings } from '../utils/ansibleConfigManager.ts';
import type { NodeItem } from '../types/index.ts';
import { CronSchedulesPanel } from './CronSchedulesPanel.tsx';
import { DatacenterTopologyCard } from './DatacenterTopologyCard.tsx';
import { DatacenterVarsPage } from './DatacenterVarsPage.tsx';
import { DatacenterPlaybooksPage } from './DatacenterPlaybooksPage.tsx';
import { RcloneRemotesView } from './RcloneRemotesView.tsx';
import { DatacenterTailscalePage } from './DatacenterTailscalePage.tsx';

interface VaultVariable {
  name: string;
  createdAt: number;
  updatedAt: number;
  maskedValue: string;
}

interface VaultStatusResponse {
  success: boolean;
  hasPasswordSource: boolean;
  source: string;
}

interface VaultVariablesResponse {
  success: boolean;
  variables: VaultVariable[];
  total: number;
}

interface VaultActionResponse {
  success: boolean;
  variable?: VaultVariable;
  error?: string;
}

export interface DatacenterSettingsViewProps {
  page: DatacenterSettingPageId;
  /** Cluster inventory, used by the mesh topology and scheduler pages. */
  nodes?: NodeItem[];
  onSelectNode?: (node: NodeItem) => void;
}

const QUICK_CMD_CATEGORY_COLORS: Record<QuickCommandCategory, { bg: string; text: string; border: string }> = {
  system: { bg: 'bg-[#1b2a38]', text: 'text-[#8cd6cb]', border: 'border-[#8cd6cb]/30' },
  containers: { bg: 'bg-[#291e3b]', text: 'text-[#d9b9ff]', border: 'border-[#d9b9ff]/30' },
  storage: { bg: 'bg-[#2d281b]', text: 'text-[#ffd580]', border: 'border-[#ffd580]/30' },
  network: { bg: 'bg-[#182030]', text: 'text-[#79c0ff]', border: 'border-[#79c0ff]/30' },
  custom: { bg: 'bg-[#21262d]', text: 'text-[#8d909b]', border: 'border-[#30363d]' },
};

const DEFAULT_ANSIBLE_CONFIG: AnsibleConfigSettings = {
  stdoutCallback: 'json',
  forks: 10,
  timeout: 30,
  hostKeyChecking: false,
  pipelining: true,
  becomeMethod: 'run0',
};

/** Page frame: breadcrumb, title, stable deep-link URL, and the single setting body. */
const SettingsPageFrame: React.FC<{
  page: DatacenterSettingPageId;
  actions?: React.ReactNode;
  children: React.ReactNode;
}> = ({ page, actions, children }) => {
  const meta = findDatacenterSettingPage(page) ?? getDatacenterSettingPages()[0];
  const category = findDatacenterSettingCategory(meta.category);
  const route = formatDatacenterRoute({ kind: 'setting', setting: meta.id });

  return (
    <div
      className="flex flex-col gap-3 w-full"
      data-testid="datacenter-settings-page"
      data-setting-id={meta.id}
    >
      <div className="rounded-lg bg-[#121528] border border-[#26293c] p-4 shadow-md flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-3 min-w-0">
          <div className="w-10 h-10 rounded-lg bg-[#1b2a38] border border-[#8cd6cb]/40 flex items-center justify-center text-[#8cd6cb] shadow-sm shrink-0">
            <span className="material-symbols-outlined text-[22px]">{meta.icon}</span>
          </div>
          <div className="min-w-0">
            <div className="flex items-center gap-2 font-mono text-[10px] uppercase tracking-wider text-[#8d909b]">
              <span>Datacenter Settings</span>
              <span className="material-symbols-outlined text-[12px]">chevron_right</span>
              <span className="text-[#b0c9ff]">{category?.title ?? meta.category}</span>
            </div>
            <h1 className="text-lg font-bold font-mono text-[#f0f2ff] tracking-tight truncate">
              {meta.title}
            </h1>
            <p className="text-[11px] text-[#8d909b] font-mono mt-0.5">{meta.description}</p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          {actions}
          <span
            className="px-2 py-1 rounded bg-[#171a2d] border border-[#26293c] font-mono text-[10px] text-[#8d909b]"
            title="Stable deep-link for this setting page"
          >
            {route}
          </span>
        </div>
      </div>

      {children}
    </div>
  );
};

interface GalaxyCollectionItem {
  name: string;
  version: string;
  path?: string;
}

/**
 * First-paint fallback only: the authoritative catalog lives in the Go daemon
 * (`pkg/collections.Catalog`) and is fetched from
 * `GET /api/ansible/collections/catalog`. The daemon auto-installs everything
 * in that catalog on startup, plus any collection a detected host capability
 * needs (PostgreSQL, MariaDB/MySQL, MongoDB, Podman, Docker, Proxmox, Windows).
 */
const SUGGESTED_COLLECTIONS: Array<{ name: string; description: string }> = [
  { name: 'community.general', description: 'Core community modules & become plugins (community.general.run0, doas, systemd, filesystems)' },
  { name: 'ansible.posix', description: 'POSIX system modules (firewalld, mount, sysctl, synchronize, acl)' },
  { name: 'ansible.windows', description: 'Windows host automation over WinRM (win_* modules, PowerShell remoting)' },
  { name: 'community.docker', description: 'Docker Engine & Docker Compose lifecycle modules' },
  { name: 'containers.podman', description: 'Rootless and rootful Podman container & pod management' },
  { name: 'community.postgresql', description: 'PostgreSQL databases, roles, privileges, and extensions' },
  { name: 'community.mysql', description: 'MariaDB / MySQL databases, users, and replication' },
  { name: 'community.mongodb', description: 'MongoDB users, roles, replicasets, and sharding' },
  { name: 'community.proxmox', description: 'Proxmox VE KVM/QEMU and LXC container automation' },
];

const AnsibleCollectionsSettingsPage: React.FC<{ page: DatacenterSettingPageId }> = ({ page }) => {
  const [collections, setCollections] = useState<GalaxyCollectionItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [newCollection, setNewCollection] = useState('');
  const [installing, setInstalling] = useState<string | null>(null);
  const [removing, setRemoving] = useState<string | null>(null);
  const [feedback, setFeedback] = useState<{ type: 'success' | 'error'; message: string } | null>(null);
  const [catalog, setCatalog] = useState(SUGGESTED_COLLECTIONS);
  const [disabled, setDisabled] = useState<string[]>([]);
  const [required, setRequired] = useState<string[]>([]);
  const [updates, setUpdates] = useState<
    Record<string, { latest?: string; updateAvailable: boolean; checkError?: string }>
  >({});
  const [checking, setChecking] = useState(false);
  const [updatingAll, setUpdatingAll] = useState(false);
  const [pinned, setPinned] = useState<Record<string, string>>({});
  const [autoUpdate, setAutoUpdate] = useState(false);
  const [pinning, setPinning] = useState<string | null>(null);

  const fetchCatalog = async () => {
    try {
      const res = await fetch('/api/ansible/collections/catalog');
      const data = await res.json();
      if (data?.success && Array.isArray(data.catalog) && data.catalog.length > 0) {
        setCatalog(data.catalog);
      }
    } catch {
      // Keep the fallback catalog; the daemon installs the same set regardless.
    }
  };

  const fetchCollections = async () => {
    setLoading(true);
    try {
      const res = await fetch('/api/ansible/collections');
      const data = await res.json();
      if (data?.success && Array.isArray(data.collections)) {
        setCollections(data.collections);
        setDisabled(Array.isArray(data.disabled) ? data.disabled : []);
        setRequired(Array.isArray(data.required) ? data.required : []);
        setPinned(data.pinned && typeof data.pinned === 'object' ? data.pinned : {});
        setAutoUpdate(Boolean(data.autoUpdate));
      }
    } catch (err: any) {
      setFeedback({ type: 'error', message: `Failed to load Galaxy collections: ${err?.message || err}` });
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchCatalog();
    fetchCollections();
  }, []);

  const handleInstall = async (collectionName: string) => {
    const trimmed = collectionName.trim();
    if (!trimmed) return;
    setInstalling(trimmed);
    setFeedback(null);
    try {
      const res = await fetch('/api/ansible/collections/install', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ collection: trimmed }),
      });
      const data = await res.json();
      if (res.ok && data?.success) {
        if (Array.isArray(data.collections)) {
          setCollections(data.collections);
        } else {
          await fetchCollections();
        }
        if (trimmed === newCollection.trim()) {
          setNewCollection('');
        }
        setFeedback({ type: 'success', message: data.message || `Installed ${trimmed} into managed Ansible environment.` });
      } else {
        setFeedback({ type: 'error', message: data?.error || `Failed to install ${trimmed}` });
      }
    } catch (err: any) {
      setFeedback({ type: 'error', message: err?.message || `Network error installing ${trimmed}` });
    } finally {
      setInstalling(null);
    }
  };

  const handleRemove = async (collectionName: string) => {
    setRemoving(collectionName);
    setFeedback(null);
    try {
      const res = await fetch('/api/ansible/collections/remove', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ collection: collectionName }),
      });
      const data = await res.json();
      if (res.ok && data?.success) {
        if (Array.isArray(data.disabled)) {
          setDisabled(data.disabled);
        }
        if (Array.isArray(data.collections)) {
          setCollections(data.collections);
        } else {
          await fetchCollections();
        }
        setUpdates(prev => {
          const next = { ...prev };
          delete next[collectionName];
          return next;
        });
        setFeedback({
          type: 'success',
          message:
            data.message ||
            `Removed ${collectionName}. It stays removed: auto-install will not re-add it until you enable it again.`,
        });
      } else {
        setFeedback({ type: 'error', message: data?.error || `Failed to remove ${collectionName}` });
      }
    } catch (err: any) {
      setFeedback({ type: 'error', message: err?.message || `Network error removing ${collectionName}` });
    } finally {
      setRemoving(null);
    }
  };

  const refreshAll = async () => {
    await fetchCollections();
  };

  const handleCheckUpdates = async () => {
    setChecking(true);
    setFeedback(null);
    try {
      const res = await fetch('/api/ansible/collections/updates');
      const data = await res.json();
      if (!res.ok || !data?.success) {
        setFeedback({ type: 'error', message: data?.error || 'Could not check Galaxy for updates' });
        return;
      }
      const next: Record<string, { latest?: string; updateAvailable: boolean; checkError?: string }> = {};
      for (const entry of data.updates ?? []) {
        next[entry.name] = {
          latest: entry.latest,
          updateAvailable: Boolean(entry.updateAvailable),
          checkError: entry.checkError,
        };
      }
      setUpdates(next);
      const count = Number(data.updatesAvailable ?? 0);
      setFeedback({
        type: 'success',
        message:
          count > 0
            ? `${count} collection${count === 1 ? '' : 's'} can be updated.`
            : 'All collections are up to date with Galaxy.',
      });
    } catch (err: any) {
      setFeedback({ type: 'error', message: err?.message || 'Network error checking for updates' });
    } finally {
      setChecking(false);
    }
  };

  const reportUpdate = (results: any[]) => {
    const failed = results.filter(r => r.error);
    if (failed.length > 0) {
      setFeedback({ type: 'error', message: `Update failed for ${failed.map(r => r.name).join(', ')}: ${failed[0].error}` });
      return;
    }
    const changed = results.filter(r => r.changed);
    const skipped = results.filter(r => r.skipped);
    const parts: string[] = [];
    if (changed.length > 0) {
      parts.push(changed.map(r => `${r.name} ${r.from || '—'} → ${r.to}`).join(', '));
    }
    if (skipped.length > 0) {
      parts.push(`skipped ${skipped.map(r => `${r.name} (${r.skipped})`).join(', ')}`);
    }
    setFeedback({
      type: 'success',
      message: parts.length > 0 ? parts.join(' · ') : 'Everything was already at the newest version.',
    });
  };

  const handleUpdate = async (collectionName: string) => {
    setInstalling(collectionName);
    setFeedback(null);
    try {
      const res = await fetch('/api/ansible/collections/update', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ collection: collectionName }),
      });
      const data = await res.json();
      reportUpdate(data?.results ?? []);
      await refreshAll();
      setUpdates(prev => {
        const next = { ...prev };
        delete next[collectionName];
        return next;
      });
    } catch (err: any) {
      setFeedback({ type: 'error', message: err?.message || `Network error updating ${collectionName}` });
    } finally {
      setInstalling(null);
    }
  };

  const handleUpdateAll = async () => {
    setUpdatingAll(true);
    setFeedback(null);
    try {
      const res = await fetch('/api/ansible/collections/update', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ all: true }),
      });
      const data = await res.json();
      reportUpdate(data?.results ?? []);
      await refreshAll();
      setUpdates({});
    } catch (err: any) {
      setFeedback({ type: 'error', message: err?.message || 'Network error updating collections' });
    } finally {
      setUpdatingAll(false);
    }
  };

  const handlePin = async (collectionName: string) => {
    setPinning(collectionName);
    setFeedback(null);
    try {
      const res = await fetch('/api/ansible/collections/pin', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ collection: collectionName }),
      });
      const data = await res.json();
      if (res.ok && data?.success) {
        setPinned(data.pinned && typeof data.pinned === 'object' ? data.pinned : {});
        setFeedback({ type: 'success', message: `${collectionName} pinned at ${data.version}.` });
        setUpdates(prev => {
          const next = { ...prev };
          delete next[collectionName];
          return next;
        });
      } else {
        setFeedback({ type: 'error', message: data?.error || `Failed to pin ${collectionName}` });
      }
    } catch (err: any) {
      setFeedback({ type: 'error', message: err?.message || `Network error pinning ${collectionName}` });
    } finally {
      setPinning(null);
    }
  };

  const handleUnpin = async (collectionName: string) => {
    setPinning(collectionName);
    setFeedback(null);
    try {
      const res = await fetch('/api/ansible/collections/unpin', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ collection: collectionName }),
      });
      const data = await res.json();
      if (res.ok && data?.success) {
        setPinned(data.pinned && typeof data.pinned === 'object' ? data.pinned : {});
        setFeedback({ type: 'success', message: `${collectionName} is no longer pinned.` });
      } else {
        setFeedback({ type: 'error', message: data?.error || `Failed to unpin ${collectionName}` });
      }
    } catch (err: any) {
      setFeedback({ type: 'error', message: err?.message || `Network error unpinning ${collectionName}` });
    } finally {
      setPinning(null);
    }
  };

  const handleToggleAutoUpdate = async () => {
    const next = !autoUpdate;
    setAutoUpdate(next);
    setFeedback(null);
    try {
      const res = await fetch('/api/ansible/collections/auto-update', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ enabled: next }),
      });
      const data = await res.json();
      if (!res.ok || !data?.success) {
        setAutoUpdate(!next);
        setFeedback({ type: 'error', message: data?.error || 'Failed to change the auto-update setting' });
        return;
      }
      setAutoUpdate(Boolean(data.autoUpdate));
      setFeedback({
        type: 'success',
        message: data.autoUpdate
          ? 'Weekly auto-update enabled: unpinned collections upgrade to the newest published version.'
          : 'Weekly auto-update disabled. Updates stay manual.',
      });
    } catch (err: any) {
      setAutoUpdate(!next);
      setFeedback({ type: 'error', message: err?.message || 'Network error changing the auto-update setting' });
    }
  };

  const handleEnable = async (collectionName: string) => {
    setInstalling(collectionName);
    setFeedback(null);
    try {
      const res = await fetch('/api/ansible/collections/enable', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ collection: collectionName }),
      });
      const data = await res.json();
      if (res.ok && data?.success) {
        setDisabled(Array.isArray(data.disabled) ? data.disabled : []);
        setFeedback({
          type: 'success',
          message: data.reinstalled
            ? `${collectionName} re-enabled and installed.`
            : `${collectionName} re-enabled; it was already installed.`,
        });
        await refreshAll();
      } else {
        setFeedback({ type: 'error', message: data?.error || `Failed to enable ${collectionName}` });
      }
    } catch (err: any) {
      setFeedback({ type: 'error', message: err?.message || `Network error enabling ${collectionName}` });
    } finally {
      setInstalling(null);
    }
  };

  const installedNames = new Set(collections.map(c => c.name));

  return (
    <SettingsPageFrame
      page={page}
      actions={
        <div className="flex items-center gap-1.5">
          <button
            type="button"
            onClick={fetchCollections}
            disabled={loading}
            className="px-2.5 py-1 rounded text-xs font-mono text-[#dfe1fa] bg-[#171a2d] hover:bg-[#21263d] border border-[#26293c] transition-colors flex items-center gap-1.5 cursor-pointer disabled:opacity-50"
          >
            <span className="material-symbols-outlined text-[14px]">refresh</span>
            <span>Refresh</span>
          </button>
          <button
            type="button"
            onClick={handleCheckUpdates}
            disabled={checking || loading}
            data-testid="galaxy-check-updates"
            className="px-2.5 py-1 rounded text-xs font-mono text-[#8cd6cb] bg-[#171a2d] hover:bg-[#21263d] border border-[#26293c] transition-colors flex items-center gap-1.5 cursor-pointer disabled:opacity-50"
          >
            <span className="material-symbols-outlined text-[14px]">cloud_sync</span>
            <span>{checking ? 'Checking…' : 'Check for updates'}</span>
          </button>
          <button
            type="button"
            onClick={handleUpdateAll}
            disabled={updatingAll || loading}
            data-testid="galaxy-update-all"
            className="px-2.5 py-1 rounded text-xs font-mono font-bold text-white bg-[#1f6feb] hover:bg-[#388bfd] border border-[#1f6feb] transition-colors flex items-center gap-1.5 cursor-pointer disabled:opacity-50"
          >
            <span className="material-symbols-outlined text-[14px]">upgrade</span>
            <span>{updatingAll ? 'Updating…' : 'Update All'}</span>
          </button>
        </div>
      }
    >
      <div
        id="datacenter-ansible-collections"
        data-testid="datacenter-ansible-collections-settings"
        className="rounded-lg bg-[#121528] border border-[#26293c] p-4 shadow-md flex flex-col gap-4"
      >
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[18px] text-[#8cd6cb]">extension</span>
            <h2 className="text-xs font-mono font-bold text-[#dfe1fa] uppercase tracking-wider">
              Managed Ansible Galaxy Collections
            </h2>
            <span className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-[#162620] text-[#8cd6cb] border border-[#8cd6cb]/30">
              {collections.length} Installed
            </span>
          </div>
          <span className="text-[10px] font-mono text-[#8d909b]">
            Managed via <code className="text-[#79c0ff]">data/ansible-venv/bin/ansible-galaxy</code>
          </span>
        </div>

        <p className="text-[11px] text-[#8d909b] font-mono">
          The catalog below is installed automatically, and any capability detected on a host (PostgreSQL, MariaDB/MySQL,
          MongoDB, Podman, Docker, Proxmox, Windows) pulls its module tree on first probe. Removing a collection records an
          opt-out so auto-install stops re-adding it; <code className="text-[#8cd6cb]">community.general</code> and{' '}
          <code className="text-[#8cd6cb]">ansible.posix</code> are required by the mutation pipeline and cannot be removed.
        </p>

        <div className="flex flex-wrap items-center justify-between gap-2 p-2.5 rounded-lg bg-[#171a2d] border border-[#26293c]">
          <div className="flex items-center gap-2 min-w-0">
            <span className="material-symbols-outlined text-[16px] text-[#8d909b]">schedule</span>
            <div className="min-w-0">
              <div className="text-[11px] font-mono font-bold text-[#dfe1fa]">Weekly auto-update</div>
              <div className="text-[10px] font-mono text-[#8d909b]">
                Opt-in. Unpinned, non-required collections upgrade to the newest published version once a week.
              </div>
            </div>
          </div>
          <button
            type="button"
            onClick={handleToggleAutoUpdate}
            data-testid="galaxy-auto-update-toggle"
            aria-pressed={autoUpdate}
            className={`px-2.5 py-1 rounded text-[10px] font-mono font-bold border transition-colors shrink-0 cursor-pointer ${
              autoUpdate
                ? 'bg-[#162620] text-[#8cd6cb] border-[#8cd6cb]/40'
                : 'bg-[#121528] text-[#8d909b] border-[#26293c]'
            }`}
          >
            {autoUpdate ? 'ON' : 'OFF'}
          </button>
        </div>

        {feedback && (
          <div
            className={`p-2.5 rounded text-xs font-mono border flex items-center justify-between ${
              feedback.type === 'success'
                ? 'bg-[#162620] text-[#8cd6cb] border-[#8cd6cb]/30'
                : 'bg-[#311c1c] text-[#ffb4ab] border-[#ffb4ab]/30'
            }`}
          >
            <div className="flex items-center gap-2">
              <span className="material-symbols-outlined text-[16px]">
                {feedback.type === 'success' ? 'check_circle' : 'error'}
              </span>
              <span>{feedback.message}</span>
            </div>
            <button
              type="button"
              onClick={() => setFeedback(null)}
              className="text-[#8d909b] hover:text-[#dfe1fa] cursor-pointer"
            >
              <span className="material-symbols-outlined text-[14px]">close</span>
            </button>
          </div>
        )}

        {/* Install form */}
        <form
          onSubmit={e => {
            e.preventDefault();
            handleInstall(newCollection);
          }}
          className="p-3 rounded-lg bg-[#171a2d] border border-[#26293c] flex flex-wrap items-end gap-2.5"
        >
          <div className="flex-1 min-w-[240px]">
            <label className="block text-[10px] font-mono text-[#8d909b] uppercase mb-1">
              Install Galaxy Collection (<code className="text-[#79c0ff]">&lt;namespace&gt;.&lt;collection&gt;</code>)
            </label>
            <input
              type="text"
              value={newCollection}
              onChange={e => setNewCollection(e.target.value)}
              placeholder="e.g. community.general or containers.podman"
              data-testid="galaxy-collection-input"
              className="w-full bg-[#121528] border border-[#26293c] focus:border-[#58a6ff] rounded px-2.5 py-1.5 text-xs font-mono text-[#dfe1fa] outline-none"
            />
          </div>
          <button
            type="submit"
            disabled={Boolean(installing) || !newCollection.trim()}
            data-testid="galaxy-collection-install-btn"
            className="px-3 py-1.5 rounded text-xs font-mono font-bold text-white bg-[#1f6feb] hover:bg-[#388bfd] disabled:opacity-50 transition-colors flex items-center gap-1.5 cursor-pointer"
          >
            <span className="material-symbols-outlined text-[15px]">
              {installing === newCollection.trim() ? 'sync' : 'download'}
            </span>
            <span>{installing === newCollection.trim() ? 'Installing...' : 'Install Collection'}</span>
          </button>
        </form>

        {/* Installed Collections Table */}
        <div className="flex flex-col gap-2">
          <h3 className="text-[11px] font-mono font-bold text-[#8d909b] uppercase tracking-wider">
            Installed Collections ({collections.length})
          </h3>
          {loading ? (
            <div className="p-4 text-center text-xs font-mono text-[#8d909b] bg-[#171a2d] rounded border border-[#26293c]">
              Querying ansible-galaxy collection list...
            </div>
          ) : collections.length === 0 ? (
            <div className="p-4 text-center text-xs font-mono text-[#8d909b] bg-[#171a2d] rounded border border-[#26293c]">
              No external Ansible Galaxy collections detected.
            </div>
          ) : (
            <div className="overflow-x-auto rounded border border-[#26293c]">
              <table className="w-full text-left border-collapse font-mono text-xs">
                <thead>
                  <tr className="bg-[#171a2d] text-[#8d909b] text-[10px] uppercase border-b border-[#26293c]">
                    <th className="py-2 px-3">Collection</th>
                    <th className="py-2 px-3">Installed</th>
                    <th className="py-2 px-3">Latest</th>
                    <th className="py-2 px-3">Collection Path</th>
                    <th className="py-2 px-3 text-right">Actions</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-[#26293c]/60">
                  {collections.map(col => (
                    <tr key={col.name} className="hover:bg-[#171a2d]/60 transition-colors">
                      <td className="py-2 px-3 font-bold text-[#dfe1fa]">
                        <div className="flex items-center gap-2">
                          <span>{col.name}</span>
                          {col.name === 'community.general' && (
                            <span className="text-[9px] px-1.5 py-0.2 rounded bg-[#291e3b] text-[#d9b9ff] border border-[#d9b9ff]/30">
                              run0 provider
                            </span>
                          )}
                        </div>
                      </td>
                      <td className="py-2 px-3 text-[#8cd6cb]">{col.version}</td>
                      <td className="py-2 px-3">
                        {(() => {
                          const pin = pinned[col.name];
                          if (pin) {
                            return (
                              <span className="text-[#d9b9ff]">
                                {pin}
                                <span className="ml-1.5 text-[9px] px-1 py-0.2 rounded bg-[#291e3b] text-[#d9b9ff] border border-[#d9b9ff]/30">
                                  PINNED
                                </span>
                              </span>
                            );
                          }
                          const info = updates[col.name];
                          if (!info) {
                            return <span className="text-[#8d909b]">—</span>;
                          }
                          if (info.checkError || !info.latest) {
                            return (
                              <span className="text-[#ffd580] text-[10px]" title={info.checkError}>
                                unknown
                              </span>
                            );
                          }
                          return (
                            <span className={info.updateAvailable ? 'text-[#ffd580] font-bold' : 'text-[#8d909b]'}>
                              {info.latest}
                              {info.updateAvailable && (
                                <span className="ml-1.5 text-[9px] px-1 py-0.2 rounded bg-[#2d281b] text-[#ffd580] border border-[#ffd580]/30">
                                  UPDATE
                                </span>
                              )}
                            </span>
                          );
                        })()}
                      </td>
                      <td className="py-2 px-3 text-[#8d909b] text-[11px] truncate max-w-xs" title={col.path}>
                        {col.path || '~/.ansible/collections/ansible_collections'}
                      </td>
                      <td className="py-2 px-3 text-right">
                        <div className="inline-flex items-center gap-1.5">
                          {pinned[col.name] ? (
                            <button
                              type="button"
                              onClick={() => handleUnpin(col.name)}
                              disabled={pinning === col.name}
                              data-testid={`unpin-collection-${col.name}`}
                              className="px-2 py-0.5 rounded text-[10px] bg-[#291e3b] text-[#d9b9ff] hover:bg-[#38284f] border border-[#d9b9ff]/30 disabled:opacity-50 cursor-pointer"
                              title={`Release the pin on ${col.name}`}
                            >
                              {pinning === col.name ? 'Unpinning…' : 'Unpin'}
                            </button>
                          ) : (
                            <button
                              type="button"
                              onClick={() => handlePin(col.name)}
                              disabled={pinning === col.name}
                              data-testid={`pin-collection-${col.name}`}
                              className="px-2 py-0.5 rounded text-[10px] bg-[#171a2d] text-[#c3c6d2] hover:bg-[#21263d] border border-[#26293c] disabled:opacity-50 cursor-pointer"
                              title={`Pin ${col.name} at the installed version (${col.version})`}
                            >
                              {pinning === col.name ? 'Pinning…' : 'Pin'}
                            </button>
                          )}
                          <button
                            type="button"
                            onClick={() => handleUpdate(col.name)}
                            disabled={Boolean(installing) || Boolean(removing) || updatingAll}
                            data-testid={`update-collection-${col.name}`}
                            className="px-2 py-0.5 rounded text-[10px] bg-[#1b2a38] text-[#79c0ff] hover:bg-[#24384b] border border-[#79c0ff]/30 disabled:opacity-50 cursor-pointer"
                            title={
                              updates[col.name]?.updateAvailable
                                ? `Upgrade ${col.name} to ${updates[col.name]?.latest}`
                                : `Reinstall ${col.name} at the newest published version`
                            }
                          >
                            {installing === col.name
                              ? 'Updating...'
                              : updates[col.name]?.updateAvailable
                              ? `Update to ${updates[col.name]?.latest}`
                              : 'Update'}
                          </button>
                          {required.includes(col.name) ? (
                            <span
                              className="px-2 py-0.5 rounded text-[10px] bg-[#21263d] text-[#b0c9ff] border border-[#b0c9ff]/30"
                              title="Required by ATC's mutation pipeline — removal is blocked"
                            >
                              Required
                            </span>
                          ) : (
                            <button
                              type="button"
                              onClick={() => handleRemove(col.name)}
                              disabled={Boolean(removing) || Boolean(installing)}
                              data-testid={`remove-collection-${col.name}`}
                              className="px-2 py-0.5 rounded text-[10px] bg-[#311c1c] text-[#ffb4ab] hover:bg-[#442424] border border-[#ffb4ab]/30 disabled:opacity-50 cursor-pointer"
                              title={`Remove ${col.name} and stop auto-installing it`}
                            >
                              {removing === col.name ? 'Removing...' : 'Remove'}
                            </button>
                          )}
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>

        {/* Recommended Homelab Collections */}
        <div className="flex flex-col gap-2 pt-2 border-t border-[#26293c]/60">
          <h3 className="text-[11px] font-mono font-bold text-[#8d909b] uppercase tracking-wider">
            Recommended Collections Catalog
          </h3>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
            {catalog.map(item => {
              const isInstalled = installedNames.has(item.name);
              const isDisabled = disabled.includes(item.name);
              const isRequired = required.includes(item.name);
              return (
                <div
                  key={item.name}
                  className="p-2.5 rounded bg-[#171a2d] border border-[#26293c] flex items-center justify-between gap-2"
                  data-testid={`catalog-collection-${item.name}`}
                >
                  <div className="min-w-0">
                    <div className="flex items-center gap-1.5">
                      <span className="text-xs font-mono font-bold text-[#79c0ff]">{item.name}</span>
                      {isInstalled && (
                        <span className="text-[9px] font-mono px-1.5 py-0.2 rounded bg-[#162620] text-[#8cd6cb] border border-[#8cd6cb]/30">
                          INSTALLED
                        </span>
                      )}
                      {isDisabled && !isInstalled && (
                        <span className="text-[9px] font-mono px-1.5 py-0.2 rounded bg-[#2d281b] text-[#ffd580] border border-[#ffd580]/30">
                          DISABLED
                        </span>
                      )}
                      {isRequired && (
                        <span
                          className="text-[9px] font-mono px-1.5 py-0.2 rounded bg-[#21263d] text-[#b0c9ff] border border-[#b0c9ff]/30"
                          title="Required by ATC's mutation pipeline"
                        >
                          REQUIRED
                        </span>
                      )}
                    </div>
                    <p className="text-[10px] font-mono text-[#8d909b] truncate" title={item.description}>
                      {item.description}
                    </p>
                  </div>
                  {isDisabled && !isInstalled ? (
                    <button
                      type="button"
                      onClick={() => handleEnable(item.name)}
                      disabled={Boolean(installing) || Boolean(removing)}
                      data-testid={`enable-collection-${item.name}`}
                      className="px-2.5 py-1 rounded text-[10px] font-mono font-bold text-[#8cd6cb] bg-[#162620] hover:bg-[#1d332a] border border-[#8cd6cb]/30 shrink-0 cursor-pointer disabled:opacity-50"
                    >
                      {installing === item.name ? 'Enabling...' : 'Enable'}
                    </button>
                  ) : isInstalled ? (
                    isRequired ? (
                      <span className="text-[10px] font-mono text-[#8d909b] shrink-0 px-2">kept</span>
                    ) : (
                      <button
                        type="button"
                        onClick={() => handleRemove(item.name)}
                        disabled={Boolean(removing) || Boolean(installing)}
                        className="px-2.5 py-1 rounded text-[10px] font-mono text-[#ffb4ab] bg-[#311c1c] hover:bg-[#442424] border border-[#ffb4ab]/30 shrink-0 cursor-pointer disabled:opacity-50"
                      >
                        {removing === item.name ? 'Removing...' : 'Remove'}
                      </button>
                    )
                  ) : (
                    <button
                      type="button"
                      onClick={() => handleInstall(item.name)}
                      disabled={Boolean(installing) || Boolean(removing)}
                      className="px-2.5 py-1 rounded text-[10px] font-mono font-bold text-white bg-[#1f6feb] hover:bg-[#388bfd] shrink-0 cursor-pointer disabled:opacity-50"
                    >
                      {installing === item.name ? 'Installing...' : '+ Install'}
                    </button>
                  )}
                </div>
              );
            })}
          </div>
        </div>
      </div>
    </SettingsPageFrame>
  );
};

const SSHVaultPage: React.FC<{ page: DatacenterSettingPageId }> = ({ page }) => {
  const [variables, setVariables] = useState<VaultVariable[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [feedback, setFeedback] = useState<{ type: 'success' | 'error'; message: string } | null>(null);
  const [vaultStatus, setVaultStatus] = useState<VaultStatusResponse | null>(null);

  // Form state for adding new vault variable
  const [isAdding, setIsAdding] = useState(false);
  const [newVarName, setNewVarName] = useState('');
  const [newVarValue, setNewVarValue] = useState('');
  const [vaultPassword, setVaultPassword] = useState('');

  // State for editing a variable
  const [editingVar, setEditingVar] = useState<VaultVariable | null>(null);
  const [editValue, setEditValue] = useState('');
  const [editPassword, setEditPassword] = useState('');

  // Confirm delete state
  const [confirmDeleteVar, setConfirmDeleteVar] = useState<string | null>(null);

  const loadVariables = useCallback(async () => {
    setLoading(true);
    try {
      const [varsRes, statusRes] = await Promise.all([
        fetch('/api/vault/variables'),
        fetch('/api/vault/status'),
      ]);

      if (varsRes.ok) {
        const data: VaultVariablesResponse = await varsRes.json();
        if (data.success) {
          setVariables(data.variables);
        }
      }

      if (statusRes.ok) {
        const statusData: VaultStatusResponse = await statusRes.json();
        setVaultStatus(statusData);
      }
    } catch (err: any) {
      setFeedback({ type: 'error', message: `Failed to load SSH vault variables: ${err?.message || err}` });
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadVariables();
  }, [loadVariables]);

  const handleAddVariable = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newVarName.trim() || !newVarValue.trim()) {
      setFeedback({ type: 'error', message: 'Variable name and value are required.' });
      return;
    }
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(newVarName.trim())) {
      setFeedback({ type: 'error', message: 'Variable name must match [A-Za-z_][A-Za-z0-9_]* (e.g., ssh_key_webserver).' });
      return;
    }
    setBusy(true);
    setFeedback(null);
    try {
      const res = await fetch('/api/vault/variables', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: newVarName.trim(),
          value: newVarValue,
          password: vaultPassword || undefined,
        }),
      });
      const data: VaultActionResponse = await res.json();
      if (res.ok && data.success) {
        setFeedback({ type: 'success', message: `SSH vault variable "${newVarName.trim()}" created and encrypted.` });
        setNewVarName('');
        setNewVarValue('');
        setVaultPassword('');
        setIsAdding(false);
        loadVariables();
      } else {
        setFeedback({ type: 'error', message: data.error || 'Failed to create vault variable.' });
      }
    } catch (err: any) {
      setFeedback({ type: 'error', message: `Network error: ${err?.message || err}` });
    } finally {
      setBusy(false);
    }
  };

  const handleUpdateVariable = async (name: string) => {
    if (!editValue.trim()) {
      setFeedback({ type: 'error', message: 'A non-empty value is required.' });
      return;
    }
    setBusy(true);
    setFeedback(null);
    try {
      const res = await fetch(`/api/vault/variables/${encodeURIComponent(name)}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          value: editValue,
          password: editPassword || undefined,
        }),
      });
      const data: VaultActionResponse = await res.json();
      if (res.ok && data.success) {
        setFeedback({ type: 'success', message: `SSH vault variable "${name}" updated.` });
        setEditingVar(null);
        setEditValue('');
        setEditPassword('');
        loadVariables();
      } else {
        setFeedback({ type: 'error', message: data.error || 'Failed to update vault variable.' });
      }
    } catch (err: any) {
      setFeedback({ type: 'error', message: `Network error: ${err?.message || err}` });
    } finally {
      setBusy(false);
    }
  };

  const handleDeleteVariable = async (name: string) => {
    setBusy(true);
    setFeedback(null);
    try {
      const res = await fetch(`/api/vault/variables/${encodeURIComponent(name)}`, {
        method: 'DELETE',
      });
      const data = await res.json();
      if (res.ok && data.success) {
        setFeedback({ type: 'success', message: `SSH vault variable "${name}" deleted.` });
        setConfirmDeleteVar(null);
        loadVariables();
      } else {
        setFeedback({ type: 'error', message: data.error || 'Failed to delete vault variable.' });
      }
    } catch (err: any) {
      setFeedback({ type: 'error', message: `Network error: ${err?.message || err}` });
    } finally {
      setBusy(false);
    }
  };

  const handleOpenEdit = (variable: VaultVariable) => {
    setEditingVar(variable);
    setEditValue('');
    setEditPassword('');
    setFeedback(null);
  };

  const handleCancelEdit = () => {
    setEditingVar(null);
    setEditValue('');
    setEditPassword('');
    setFeedback(null);
  };

  const formatTimestamp = (ts: number) => {
    return new Date(ts).toLocaleString(undefined, {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    });
  };

  const headerActions = (
    <div className="flex items-center gap-2">
      <button
        type="button"
        onClick={loadVariables}
        disabled={loading || busy}
        className="px-2.5 py-1 rounded text-xs font-mono text-[#dfe1fa] bg-[#171a2d] hover:bg-[#21263d] border border-[#26293c] transition-colors flex items-center gap-1.5 cursor-pointer disabled:opacity-50"
        title="Refresh vault variables from store"
      >
        <span className="material-symbols-outlined text-[14px]">refresh</span>
        <span>Refresh</span>
      </button>
      <button
        type="button"
        onClick={() => {
          setIsAdding(!isAdding);
          setFeedback(null);
        }}
        disabled={busy || loading}
        className="px-2.5 py-1 rounded text-xs font-mono font-bold text-white bg-[#1f6feb] hover:bg-[#388bfd] transition-colors flex items-center gap-1 cursor-pointer shadow-xs disabled:opacity-50"
        title="Add a new SSH private key or secret to ansible-vault"
      >
        <span className="material-symbols-outlined text-[15px]">{isAdding ? 'remove' : 'add'}</span>
        <span>{isAdding ? 'Cancel' : 'Add SSH Key / Secret'}</span>
      </button>
    </div>
  );

  return (
    <SettingsPageFrame page={page} actions={headerActions}>
      <div
        id="datacenter-ssh-vault"
        data-testid="datacenter-ssh-vault-settings"
        className="rounded-lg bg-[#121528] border border-[#26293c] p-4 shadow-md flex flex-col gap-4"
      >
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[18px] text-[#d9b9ff]">vpn_key</span>
            <h2 className="text-xs font-mono font-bold text-[#dfe1fa] uppercase tracking-wider">
              SSH Vault Secrets
            </h2>
            <span className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-[#291e3b] text-[#d9b9ff] border border-[#d9b9ff]/30">
              {variables.length} Variable{variables.length !== 1 ? 's' : ''}
            </span>
          </div>
          <span className="text-[10px] font-mono text-[#8d909b]">
            Source: /api/vault/variables (SQLite vault store) · Encrypted via ansible-vault AES256
          </span>
        </div>

        <p className="text-[11px] text-[#8d909b] font-mono">
          Manage SSH private keys and sensitive secrets stored in the Ansible Vault database. Values are encrypted with{' '}
          <code className="text-[#8cd6cb]">ansible-vault encrypt_string</code> and persisted to the coordinator's SQLite vault store.
          Every decryption is logged to the Vault Audit trail.
        </p>

        {vaultStatus && (
          <div
            className={`p-2.5 rounded text-xs font-mono border flex items-center justify-between gap-2 ${
              vaultStatus.hasPasswordSource
                ? 'bg-[#162620] text-[#8cd6cb] border-[#8cd6cb]/30'
                : 'bg-[#291717] text-[#ffb4ab] border-[#ffb4ab]/30'
            }`}
          >
            <div className="flex items-center gap-2">
              <span className="material-symbols-outlined text-[15px]">
                {vaultStatus.hasPasswordSource ? 'lock_open' : 'lock'}
              </span>
              <span>
                Vault password source: <code className="text-[#79c0ff]">{vaultStatus.source}</code>
                {vaultStatus.hasPasswordSource ? ' (encryption available)' : ' — set a password source to enable encryption'}
              </span>
            </div>
          </div>
        )}

        {feedback && (
          <div
            className={`p-2.5 rounded text-xs font-mono border flex items-center justify-between ${
              feedback.type === 'success'
                ? 'bg-[#162620] text-[#8cd6cb] border-[#8cd6cb]/30'
                : 'bg-[#311c1c] text-[#ffb4ab] border-[#ffb4ab]/30'
            }`}
          >
            <div className="flex items-center gap-2">
              <span className="material-symbols-outlined text-[16px]">
                {feedback.type === 'success' ? 'check_circle' : 'error'}
              </span>
              <span>{feedback.message}</span>
            </div>
            <button
              type="button"
              onClick={() => setFeedback(null)}
              className="text-[#8d909b] hover:text-[#dfe1fa] cursor-pointer"
            >
              <span className="material-symbols-outlined text-[14px]">close</span>
            </button>
          </div>
        )}

        {/* Add new variable form */}
        {isAdding && (
          <form
            onSubmit={handleAddVariable}
            className="p-3.5 rounded-lg bg-[#171a2d] border border-[#58a6ff]/40 flex flex-col gap-3 shadow-md"
          >
            <div className="flex items-center justify-between border-b border-[#26293c] pb-2">
              <span className="text-xs font-mono font-bold text-[#dfe1fa] flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[16px] text-[#79c0ff]">add_circle</span>
                Add New SSH Vault Variable
              </span>
              <button
                type="button"
                onClick={() => {
                  setIsAdding(false);
                  setNewVarName('');
                  setNewVarValue('');
                  setVaultPassword('');
                  setFeedback(null);
                }}
                className="text-gray-400 hover:text-white cursor-pointer"
                title="Cancel"
              >
                <span className="material-symbols-outlined text-[16px]">close</span>
              </button>
            </div>

            <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
              <div>
                <label className="block text-[10px] font-mono text-[#8d909b] uppercase mb-1">
                  Variable Name (Ansible identifier) *
                </label>
                <input
                  type="text"
                  value={newVarName}
                  onChange={e => setNewVarName(e.target.value)}
                  placeholder="ssh_key_webserver"
                  data-testid="ssh-vault-var-name-input"
                  className="w-full bg-[#121528] border border-[#26293c] focus:border-[#58a6ff] rounded px-2.5 py-1.5 text-xs font-mono text-[#dfe1fa] outline-none"
                  required
                />
                <span className="text-[10px] font-mono text-[#8d909b]">Must match [A-Za-z_][A-Za-z0-9_]*</span>
              </div>

              <div>
                <label className="block text-[10px] font-mono text-[#8d909b] uppercase mb-1">
                  Vault Password (optional)
                </label>
                <input
                  type="password"
                  value={vaultPassword}
                  onChange={e => setVaultPassword(e.target.value)}
                  placeholder="Leave empty to use configured vault password source"
                  data-testid="ssh-vault-password-input"
                  className="w-full bg-[#121528] border border-[#26293c] focus:border-[#58a6ff] rounded px-2.5 py-1.5 text-xs font-mono text-[#dfe1fa] outline-none"
                />
                <span className="text-[10px] font-mono text-[#8d909b]">Uses ansible.cfg / ANSIBLE_VAULT_PASSWORD if empty</span>
              </div>
            </div>

            <div>
              <label className="block text-[10px] font-mono text-[#8d909b] uppercase mb-1">
                SSH Private Key / Secret Value *
              </label>
              <textarea
                value={newVarValue}
                onChange={e => setNewVarValue(e.target.value)}
                placeholder="-----BEGIN OPENSSH PRIVATE KEY-----\n...\n-----END OPENSSH PRIVATE KEY-----"
                rows={6}
                data-testid="ssh-vault-value-input"
                className="w-full bg-[#121528] border border-[#26293c] focus:border-[#58a6ff] rounded px-2.5 py-1.5 text-xs font-mono text-[#dfe1fa] outline-none resize-y min-h-[120px]"
                required
              />
              <span className="text-[10px] font-mono text-[#8d909b]">Paste the full SSH private key or any secret value to encrypt</span>
            </div>

            <div className="flex items-center justify-end gap-2 pt-1">
              <button
                type="button"
                onClick={() => {
                  setIsAdding(false);
                  setNewVarName('');
                  setNewVarValue('');
                  setVaultPassword('');
                  setFeedback(null);
                }}
                className="px-3 py-1 rounded text-xs font-mono text-gray-400 hover:text-white bg-[#121528] border border-[#26293c] cursor-pointer"
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={busy || !newVarName.trim() || !newVarValue.trim()}
                className="px-3 py-1 rounded text-xs font-mono font-bold text-white bg-[#1f6feb] hover:bg-[#388bfd] cursor-pointer disabled:opacity-50"
              >
                {busy ? 'Encrypting & Saving...' : 'Encrypt & Save'}
              </button>
            </div>
          </form>
        )}

        {/* Edit variable form */}
        {editingVar && (
          <form
            onSubmit={e => { e.preventDefault(); handleUpdateVariable(editingVar.name); }}
            className="p-3.5 rounded-lg bg-[#171a2d] border border-[#d9b9ff]/40 flex flex-col gap-3 shadow-md"
          >
            <div className="flex items-center justify-between border-b border-[#26293c] pb-2">
              <span className="text-xs font-mono font-bold text-[#dfe1fa] flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[16px] text-[#d9b9ff]">edit</span>
                Edit SSH Vault Variable: <code className="text-[#79c0ff]">{editingVar.name}</code>
              </span>
              <button
                type="button"
                onClick={handleCancelEdit}
                className="text-gray-400 hover:text-white cursor-pointer"
                title="Cancel"
              >
                <span className="material-symbols-outlined text-[16px]">close</span>
              </button>
            </div>

            <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
              <div>
                <label className="block text-[10px] font-mono text-[#8d909b] uppercase mb-1">
                  Vault Password (optional)
                </label>
                <input
                  type="password"
                  value={editPassword}
                  onChange={e => setEditPassword(e.target.value)}
                  placeholder="Leave empty to use configured vault password source"
                  className="w-full bg-[#121528] border border-[#26293c] focus:border-[#d9b9ff] rounded px-2.5 py-1.5 text-xs font-mono text-[#dfe1fa] outline-none"
                />
              </div>
            </div>

            <div>
              <label className="block text-[10px] font-mono text-[#8d909b] uppercase mb-1">
                New SSH Private Key / Secret Value *
              </label>
              <textarea
                value={editValue}
                onChange={e => setEditValue(e.target.value)}
                placeholder="-----BEGIN OPENSSH PRIVATE KEY-----\n...\n-----END OPENSSH PRIVATE KEY-----"
                rows={6}
                className="w-full bg-[#121528] border border-[#26293c] focus:border-[#d9b9ff] rounded px-2.5 py-1.5 text-xs font-mono text-[#dfe1fa] outline-none resize-y min-h-[120px]"
                required
              />
            </div>

            <div className="flex items-center justify-end gap-2 pt-1">
              <button
                type="button"
                onClick={handleCancelEdit}
                className="px-3 py-1 rounded text-xs font-mono text-gray-400 hover:text-white bg-[#121528] border border-[#26293c] cursor-pointer"
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={busy || !editValue.trim()}
                className="px-3 py-1 rounded text-xs font-mono font-bold text-white bg-[#d9b9ff] hover:bg-[#e8d0ff] text-[#121528] cursor-pointer disabled:opacity-50"
              >
                {busy ? 'Updating...' : 'Update Variable'}
              </button>
            </div>
          </form>
        )}

        {/* Variables table */}
        <div className="flex flex-col gap-2">
          <h3 className="text-[11px] font-mono font-bold text-[#8d909b] uppercase tracking-wider">
            Stored SSH Vault Variables ({variables.length})
          </h3>
          {loading ? (
            <div className="p-4 text-center text-xs font-mono text-[#8d909b] bg-[#171a2d] rounded border border-[#26293c]">
              Querying vault variable store…
            </div>
          ) : variables.length === 0 ? (
            <div className="p-4 text-center text-xs font-mono text-[#8d909b] bg-[#171a2d] rounded border border-[#26293c]">
              No SSH vault variables stored yet. Click "Add SSH Key / Secret" to create one.
            </div>
          ) : (
            <div className="overflow-x-auto rounded border border-[#26293c]">
              <table className="w-full text-left border-collapse font-mono text-xs">
                <thead>
                  <tr className="bg-[#171a2d] text-[#8d909b] text-[10px] uppercase border-b border-[#26293c]">
                    <th className="py-2 px-3">Variable Name</th>
                    <th className="py-2 px-3">Masked Value</th>
                    <th className="py-2 px-3">Created</th>
                    <th className="py-2 px-3">Updated</th>
                    <th className="py-2 px-3 text-right">Actions</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-[#26293c]/60">
                  {variables.map(variable => (
                    <tr key={variable.name} className="hover:bg-[#171a2d]/60 transition-colors">
                      <td className="py-2 px-3 font-bold text-[#dfe1fa]">
                        <div className="flex items-center gap-2">
                          <span>{variable.name}</span>
                          {variable.name.startsWith('ssh_') && (
                            <span className="text-[9px] px-1.5 py-0.2 rounded bg-[#1b2a38] text-[#8cd6cb] border border-[#8cd6cb]/30">
                              SSH Key
                            </span>
                          )}
                        </div>
                      </td>
                      <td className="py-2 px-3 text-[#8cd6cb] font-mono text-[11px]">
                        {variable.maskedValue}
                      </td>
                      <td className="py-2 px-3 text-[#8d909b] text-[11px]">
                        {formatTimestamp(variable.createdAt)}
                      </td>
                      <td className="py-2 px-3 text-[#8d909b] text-[11px]">
                        {formatTimestamp(variable.updatedAt)}
                      </td>
                      <td className="py-2 px-3 text-right">
                        <div className="inline-flex items-center gap-1.5">
                          <button
                            type="button"
                            onClick={() => handleOpenEdit(variable)}
                            disabled={busy}
                            className="px-2 py-0.5 rounded text-[10px] bg-[#1b2a38] text-[#d9b9ff] hover:bg-[#291e3b] border border-[#d9b9ff]/30 disabled:opacity-50 cursor-pointer"
                            title={`Update ${variable.name}`}
                          >
                            Edit
                          </button>
                          {confirmDeleteVar === variable.name ? (
                            <span className="flex items-center gap-1">
                              <button
                                type="button"
                                onClick={() => handleDeleteVariable(variable.name)}
                                disabled={busy}
                                className="px-2 py-0.5 rounded text-[10px] bg-[#ffb4ab] text-[#121528] font-bold cursor-pointer disabled:opacity-50"
                                title={`Delete ${variable.name}`}
                              >
                                Delete
                              </button>
                              <button
                                type="button"
                                onClick={() => setConfirmDeleteVar(null)}
                                className="px-2 py-0.5 rounded text-[10px] text-[#8d909b] hover:text-[#dfe1fa] cursor-pointer"
                              >
                                No
                              </button>
                            </span>
                          ) : (
                            <button
                              type="button"
                              onClick={() => setConfirmDeleteVar(variable.name)}
                              disabled={busy}
                              className="px-2 py-0.5 rounded text-[10px] bg-[#311c1c] text-[#ffb4ab] hover:bg-[#442424] border border-[#ffb4ab]/30 disabled:opacity-50 cursor-pointer"
                              title={`Delete ${variable.name}`}
                            >
                              Delete
                            </button>
                          )}
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>

        {/* Usage hint */}
        <div className="pt-2 border-t border-[#26293c]/60 p-3 rounded bg-[#171a2d] border border-[#26293c]">
          <h4 className="text-[11px] font-mono font-bold text-[#ffd580] uppercase tracking-wider mb-2">
            Usage in Ansible Playbooks
          </h4>
          <p className="text-[11px] text-[#8d909b] font-mono mb-2">
            Reference these vault variables in your playbooks using standard Ansible syntax:
          </p>
          <pre className="bg-[#0f1220] border border-[#26293c] rounded p-3 text-[10px] font-mono text-[#8cd6cb] overflow-x-auto">
{`# In group_vars or playbook vars:
ansible_ssh_private_key_file: "{{ vault_ssh_key_webserver }}"

# Or use the vault variable directly:
- name: Use SSH key from vault
  ansible.builtin.copy:
    dest: /tmp/id_ed25519
    content: "{{ vault_ssh_key_webserver }}"
    mode: "0600"`}
          </pre>
        </div>
      </div>
    </SettingsPageFrame>
  );
};

const AnsibleSettingsPage: React.FC<{ page: DatacenterSettingPageId }> = ({ page }) => {
  const [config, setConfig] = useState<AnsibleConfigSettings>(DEFAULT_ANSIBLE_CONFIG);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [feedback, setFeedback] = useState<{ type: 'success' | 'error'; message: string } | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    fetch('/api/ansible/config')
      .then(res => res.json())
      .then(data => {
        if (!cancelled && data?.success && data?.settings) {
          setConfig(data.settings);
        }
      })
      .catch(err => {
        if (!cancelled) {
          setFeedback({ type: 'error', message: `Failed to load ansible.cfg: ${err?.message || err}` });
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const save = async () => {
    setSaving(true);
    setFeedback(null);
    try {
      const res = await fetch('/api/ansible/config', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ settings: config }),
      });
      const data = await res.json();
      if (data?.success && data?.settings) {
        setConfig(data.settings);
        setFeedback({ type: 'success', message: 'ansible.cfg synchronized successfully to disk.' });
      } else {
        setFeedback({ type: 'error', message: data?.error || 'Failed to save ansible.cfg' });
      }
    } catch (err: any) {
      setFeedback({ type: 'error', message: err?.message || 'Network error saving ansible.cfg' });
    } finally {
      setSaving(false);
    }
  };

  const saveButton = (
    <button
      type="button"
      onClick={save}
      disabled={saving || loading}
      className="px-3 py-1 rounded text-xs font-mono font-bold text-white bg-[#1f6feb] hover:bg-[#388bfd] disabled:opacity-50 transition-colors flex items-center gap-1.5 cursor-pointer shadow-xs"
      title="Save and write updated settings to ansible/ansible.cfg"
    >
      <span className="material-symbols-outlined text-[15px]">{saving ? 'sync' : 'save'}</span>
      <span>{saving ? 'Syncing...' : 'Save & Sync ansible.cfg'}</span>
    </button>
  );

  const fieldCard = (children: React.ReactNode) => (
    <div
      id="datacenter-ansible-config"
      data-testid="datacenter-ansible-config-card"
      className="rounded-lg bg-[#121528] border border-[#26293c] p-4 shadow-md flex flex-col gap-3"
    >
      <p className="text-[11px] text-[#8d909b] font-mono">
        These values are written to <code className="text-[#8cd6cb] font-mono">ansible/ansible.cfg</code> on the
        coordinator and are applied to every on-the-fly and baseline playbook run.
      </p>

      {feedback && (
        <div
          className={`p-2.5 rounded text-xs font-mono border flex items-center justify-between ${
            feedback.type === 'success'
              ? 'bg-[#162620] text-[#8cd6cb] border-[#8cd6cb]/30'
              : 'bg-[#311c1c] text-[#ffb4ab] border-[#ffb4ab]/30'
          }`}
        >
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[16px]">
              {feedback.type === 'success' ? 'check_circle' : 'error'}
            </span>
            <span>{feedback.message}</span>
          </div>
          <button
            type="button"
            onClick={() => setFeedback(null)}
            className="text-[#8d909b] hover:text-[#dfe1fa] cursor-pointer"
          >
            <span className="material-symbols-outlined text-[14px]">close</span>
          </button>
        </div>
      )}

      <div className="max-w-xl flex flex-col gap-3">{children}</div>

      <div className="flex items-center justify-between pt-1 border-t border-[#26293c]/60">
        <span className="text-[10px] font-mono text-[#8d909b]">
          {loading ? 'Reading ansible/ansible.cfg...' : 'Loaded from ansible/ansible.cfg'}
        </span>
        {saveButton}
      </div>
    </div>
  );

  switch (page) {
    case 'ansible-stdout-callback':
      return (
        <SettingsPageFrame page={page} actions={saveButton}>
          {fieldCard(
            <div className="p-3 rounded-lg bg-[#171a2d] border border-[#26293c] flex flex-col gap-1.5">
              <label className="text-xs font-mono font-bold text-[#dfe1fa] flex items-center justify-between">
                <span>
                  STDOUT Driver (<code className="text-[#79c0ff]">stdout_callback</code>)
                </span>
                <span className="text-[10px] text-[#8d909b] uppercase">Output</span>
              </label>
              <select
                value={config.stdoutCallback}
                onChange={e =>
                  setConfig(prev => ({
                    ...prev,
                    stdoutCallback: e.target.value as 'json' | 'yaml' | 'default',
                  }))
                }
                className="w-full bg-[#121528] border border-[#26293c] rounded px-2.5 py-1.5 text-xs font-mono text-[#dfe1fa] focus:border-[#79c0ff] focus:outline-hidden cursor-pointer"
              >
                <option value="json">json (Structured for Web UI)</option>
                <option value="yaml">yaml (Formatted CLI)</option>
                <option value="default">default (Standard prose)</option>
              </select>
              <span className="text-[10px] font-mono text-[#8d909b]">
                Driver used by the runner to format task payloads and the recap.
              </span>
            </div>
          )}
        </SettingsPageFrame>
      );

    case 'ansible-become-method':
      return (
        <SettingsPageFrame page={page} actions={saveButton}>
          {fieldCard(
            <div className="p-3 rounded-lg bg-[#171a2d] border border-[#26293c] flex flex-col gap-1.5">
              <label className="text-xs font-mono font-bold text-[#dfe1fa] flex items-center justify-between">
                <span>
                  Privilege Elevation (<code className="text-[#d9b9ff]">become_method</code>)
                </span>
                <span className="text-[10px] text-[#8d909b] uppercase">Security</span>
              </label>
              <select
                value={config.becomeMethod}
                onChange={e =>
                  setConfig(prev => ({
                    ...prev,
                    becomeMethod: e.target.value as 'run0' | 'sudo' | 'doas',
                  }))
                }
                className="w-full bg-[#121528] border border-[#26293c] rounded px-2.5 py-1.5 text-xs font-mono text-[#dfe1fa] focus:border-[#d9b9ff] focus:outline-hidden cursor-pointer"
              >
                <option value="run0">run0 (systemd 256+ polkit preferred)</option>
                <option value="sudo">sudo (Standard Linux fallback)</option>
                <option value="doas">doas (Lightweight BSD/Alpine)</option>
              </select>
              <span className="text-[10px] font-mono text-[#8d909b]">
                Method used to execute elevated commands on remote nodes.
              </span>
            </div>
          )}
        </SettingsPageFrame>
      );

    case 'ansible-forks':
      return (
        <SettingsPageFrame page={page} actions={saveButton}>
          {fieldCard(
            <div className="p-3 rounded-lg bg-[#171a2d] border border-[#26293c] flex flex-col gap-1.5">
              <label className="text-xs font-mono font-bold text-[#dfe1fa] flex items-center justify-between">
                <span>
                  Parallel Host Forks (<code className="text-[#ffd580]">forks</code>)
                </span>
                <span className="text-[10px] font-mono text-[#ffd580]">{config.forks} forks</span>
              </label>
              <input
                type="number"
                min={1}
                max={50}
                value={config.forks}
                onChange={e => {
                  const val = parseInt(e.target.value, 10);
                  setConfig(prev => ({
                    ...prev,
                    forks: isNaN(val) ? 1 : Math.max(1, Math.min(50, val)),
                  }));
                }}
                className="w-full bg-[#121528] border border-[#26293c] rounded px-2.5 py-1.5 text-xs font-mono text-[#dfe1fa] focus:border-[#ffd580] focus:outline-hidden"
              />
              <span className="text-[10px] font-mono text-[#8d909b]">
                Max parallel SSH worker processes (1 - 50).
              </span>
            </div>
          )}
        </SettingsPageFrame>
      );

    case 'ansible-timeout':
      return (
        <SettingsPageFrame page={page} actions={saveButton}>
          {fieldCard(
            <div className="p-3 rounded-lg bg-[#171a2d] border border-[#26293c] flex flex-col gap-1.5">
              <label className="text-xs font-mono font-bold text-[#dfe1fa] flex items-center justify-between">
                <span>
                  Connection Timeout (<code className="text-[#ffb4ab]">timeout</code>)
                </span>
                <span className="text-[10px] font-mono text-[#ffb4ab]">{config.timeout}s</span>
              </label>
              <input
                type="number"
                min={10}
                max={300}
                value={config.timeout}
                onChange={e => {
                  const val = parseInt(e.target.value, 10);
                  setConfig(prev => ({
                    ...prev,
                    timeout: isNaN(val) ? 10 : Math.max(10, Math.min(300, val)),
                  }));
                }}
                className="w-full bg-[#121528] border border-[#26293c] rounded px-2.5 py-1.5 text-xs font-mono text-[#dfe1fa] focus:border-[#ffb4ab] focus:outline-hidden"
              />
              <span className="text-[10px] font-mono text-[#8d909b]">
                Default SSH connection timeout in seconds (10 - 300).
              </span>
            </div>
          )}
        </SettingsPageFrame>
      );

    case 'ansible-ssh-transport':
      return (
        <SettingsPageFrame page={page} actions={saveButton}>
          {fieldCard(
            <div className="flex flex-col gap-3">
              <div className="p-3 rounded-lg bg-[#171a2d] border border-[#26293c] flex flex-col gap-2">
                <div className="text-xs font-mono font-bold text-[#dfe1fa] flex items-center justify-between">
                  <span>
                    Host Key Checking (<code className="text-[#8cd6cb]">host_key_checking</code>)
                  </span>
                  <span
                    className={`text-[10px] font-mono px-1.5 py-0.2 rounded ${
                      config.hostKeyChecking
                        ? 'bg-[#162620] text-[#8cd6cb] border border-[#8cd6cb]/30'
                        : 'bg-[#1b2a38] text-[#79c0ff] border border-[#79c0ff]/30'
                    }`}
                  >
                    {config.hostKeyChecking ? 'Strict' : 'Disabled'}
                  </span>
                </div>
                <p className="text-[10px] font-mono text-[#8d909b]">
                  Disable for zero-touch Tailscale node provisioning without host fingerprint prompts.
                </p>
                <label className="inline-flex items-center gap-2 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={config.hostKeyChecking}
                    onChange={e =>
                      setConfig(prev => ({ ...prev, hostKeyChecking: e.target.checked }))
                    }
                    className="rounded bg-[#121528] border-[#26293c] text-[#1f6feb] focus:ring-0 cursor-pointer"
                  />
                  <span className="text-xs font-mono text-[#dfe1fa]">Enforce host key verification</span>
                </label>
              </div>

              <div className="p-3 rounded-lg bg-[#171a2d] border border-[#26293c] flex flex-col gap-2">
                <div className="text-xs font-mono font-bold text-[#dfe1fa] flex items-center justify-between">
                  <span>
                    SSH Pipelining (<code className="text-[#8cd6cb]">pipelining</code>)
                  </span>
                  <span
                    className={`text-[10px] font-mono px-1.5 py-0.2 rounded ${
                      config.pipelining
                        ? 'bg-[#162620] text-[#8cd6cb] border border-[#8cd6cb]/30'
                        : 'bg-[#291717] text-[#ffb4ab] border border-[#ffb4ab]/30'
                    }`}
                  >
                    {config.pipelining ? 'Enabled' : 'Disabled'}
                  </span>
                </div>
                <p className="text-[10px] font-mono text-[#8d909b]">
                  Pipes Python modules directly into the remote SSH session; drastically reduces network
                  roundtrips.
                </p>
                <label className="inline-flex items-center gap-2 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={config.pipelining}
                    onChange={e => setConfig(prev => ({ ...prev, pipelining: e.target.checked }))}
                    className="rounded bg-[#121528] border-[#26293c] text-[#1f6feb] focus:ring-0 cursor-pointer"
                  />
                  <span className="text-xs font-mono text-[#dfe1fa]">Enable SSH pipelining</span>
                </label>
              </div>
            </div>
          )}
        </SettingsPageFrame>
      );

    default:
      return null;
  }
};

const QuickCommandsSettingsPage: React.FC<{ page: DatacenterSettingPageId }> = ({ page }) => {
  const [quickCommands, setQuickCommands] = useState<QuickCommandItem[]>(() => loadQuickCommands());
  const [editingCommand, setEditingCommand] = useState<QuickCommandItem | null>(null);
  const [isAddingCommand, setIsAddingCommand] = useState(false);
  const [formLabel, setFormLabel] = useState('');
  const [formCmd, setFormCmd] = useState('');
  const [formDescription, setFormDescription] = useState('');
  const [formCategory, setFormCategory] = useState<QuickCommandCategory>('custom');
  const [confirmReset, setConfirmReset] = useState(false);
  const [formError, setFormError] = useState('');

  useEffect(() => {
    const handleQuickCommandsUpdated = (event: Event) => {
      const customEvt = event as CustomEvent<QuickCommandItem[]>;
      if (customEvt.detail && Array.isArray(customEvt.detail)) {
        setQuickCommands(customEvt.detail);
      } else {
        setQuickCommands(loadQuickCommands());
      }
    };

    window.addEventListener(QUICK_COMMAND_EVENT, handleQuickCommandsUpdated);
    return () => {
      window.removeEventListener(QUICK_COMMAND_EVENT, handleQuickCommandsUpdated);
    };
  }, []);

  const handleOpenAdd = () => {
    setEditingCommand(null);
    setFormLabel('');
    setFormCmd('');
    setFormDescription('');
    setFormCategory('custom');
    setFormError('');
    setIsAddingCommand(true);
  };

  const handleOpenEdit = (cmd: QuickCommandItem) => {
    setIsAddingCommand(false);
    setEditingCommand(cmd);
    setFormLabel(cmd.label);
    setFormCmd(cmd.cmd);
    setFormDescription(cmd.description || '');
    setFormCategory(cmd.category || 'custom');
    setFormError('');
  };

  const handleCancelForm = () => {
    setEditingCommand(null);
    setIsAddingCommand(false);
    setFormError('');
  };

  const handleSaveForm = (e: React.FormEvent) => {
    e.preventDefault();
    const trimmedLabel = formLabel.trim();
    const trimmedCmd = formCmd.trim();
    if (!trimmedLabel || !trimmedCmd) {
      setFormError('Label and Command are required.');
      return;
    }

    if (editingCommand) {
      setQuickCommands(
        updateQuickCommand(editingCommand.id, {
          label: trimmedLabel,
          cmd: trimmedCmd,
          description: formDescription.trim() || undefined,
          category: formCategory,
        })
      );
      setEditingCommand(null);
    } else {
      setQuickCommands(
        addQuickCommand({
          label: trimmedLabel,
          cmd: trimmedCmd,
          description: formDescription.trim() || undefined,
          category: formCategory,
        })
      );
      setIsAddingCommand(false);
    }
    setFormError('');
  };

  const handleDeleteCommand = (id: string) => {
    setQuickCommands(deleteQuickCommand(id));
    if (editingCommand?.id === id) {
      setEditingCommand(null);
    }
  };

  const handleResetDefaults = () => {
    setQuickCommands(resetQuickCommands());
    setConfirmReset(false);
    setEditingCommand(null);
    setIsAddingCommand(false);
  };

  const headerActions = (
    <div className="flex items-center gap-2">
      {confirmReset ? (
        <div className="flex items-center gap-1.5 bg-[#291717] px-2 py-1 rounded border border-[#ffb4ab]/40 text-xs font-mono">
          <span className="text-[#ffb4ab]">Reset all to defaults?</span>
          <button
            type="button"
            onClick={handleResetDefaults}
            className="px-2 py-0.5 rounded bg-[#ffb4ab] text-[#121528] font-bold hover:bg-white transition-colors cursor-pointer"
          >
            Yes, Reset
          </button>
          <button
            type="button"
            onClick={() => setConfirmReset(false)}
            className="px-1.5 py-0.5 rounded text-gray-400 hover:text-white cursor-pointer"
          >
            Cancel
          </button>
        </div>
      ) : (
        <button
          type="button"
          onClick={() => setConfirmReset(true)}
          className="px-2.5 py-1 rounded text-xs font-mono text-[#8d909b] hover:text-[#dfe1fa] bg-[#171a2d] hover:bg-[#21263d] border border-[#26293c] transition-colors flex items-center gap-1 cursor-pointer"
          title="Reset quick commands to homelab defaults"
        >
          <span className="material-symbols-outlined text-[14px]">restart_alt</span>
          <span>Reset to Defaults</span>
        </button>
      )}

      <button
        type="button"
        onClick={handleOpenAdd}
        className="px-2.5 py-1 rounded text-xs font-mono font-bold text-white bg-[#1f6feb] hover:bg-[#388bfd] transition-colors flex items-center gap-1 cursor-pointer shadow-xs"
        title="Add a new quick command"
      >
        <span className="material-symbols-outlined text-[15px]">add</span>
        <span>Add Command</span>
      </button>
    </div>
  );

  return (
    <SettingsPageFrame page={page} actions={headerActions}>
      <div
        id="datacenter-quick-commands"
        data-testid="datacenter-quick-commands-settings"
        className="rounded-lg bg-[#121528] border border-[#26293c] p-4 shadow-md flex flex-col gap-3"
      >
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[18px] text-[#79c0ff]">terminal</span>
            <h2 className="text-xs font-mono font-bold text-[#dfe1fa] uppercase tracking-wider">
              Terminal Quick Commands
            </h2>
            <span className="text-[10px] font-mono px-1.5 py-0.2 rounded bg-[#1b2a38] text-[#79c0ff] border border-[#79c0ff]/30">
              {quickCommands.length} Configured
            </span>
          </div>
        </div>

        <p className="text-[11px] text-[#8d909b] font-mono">
          Configure quick command shortcuts available in operator terminal sessions across all nodes. Changes
          apply reactively in real time.
        </p>

        {(isAddingCommand || editingCommand) && (
          <form
            onSubmit={handleSaveForm}
            className="p-3.5 rounded-lg bg-[#171a2d] border border-[#58a6ff]/40 flex flex-col gap-3 shadow-md"
          >
            <div className="flex items-center justify-between border-b border-[#26293c] pb-2">
              <span className="text-xs font-mono font-bold text-[#dfe1fa] flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[16px] text-[#79c0ff]">
                  {editingCommand ? 'edit' : 'add_circle'}
                </span>
                {editingCommand ? `Edit Quick Command: ${editingCommand.label}` : 'Add New Quick Command'}
              </span>
              <button
                type="button"
                onClick={handleCancelForm}
                className="text-gray-400 hover:text-white cursor-pointer"
                title="Cancel"
              >
                <span className="material-symbols-outlined text-[16px]">close</span>
              </button>
            </div>

            {formError && (
              <div className="text-xs font-mono text-[#ffb4ab] bg-[#311c1c] p-2 rounded border border-[#ffb4ab]/30">
                {formError}
              </div>
            )}

            <div className="grid grid-cols-1 md:grid-cols-4 gap-3">
              <div>
                <label className="block text-[10px] font-mono text-[#8d909b] uppercase mb-1">
                  Label (Button Text) *
                </label>
                <input
                  type="text"
                  value={formLabel}
                  onChange={e => setFormLabel(e.target.value)}
                  placeholder="e.g. zpool status"
                  className="w-full bg-[#121528] border border-[#26293c] focus:border-[#58a6ff] rounded px-2.5 py-1.5 text-xs font-mono text-[#dfe1fa] outline-none"
                  required
                />
              </div>

              <div className="md:col-span-2">
                <label className="block text-[10px] font-mono text-[#8d909b] uppercase mb-1">
                  Command (CLI string) *
                </label>
                <input
                  type="text"
                  value={formCmd}
                  onChange={e => setFormCmd(e.target.value)}
                  placeholder="e.g. zpool status -v"
                  className="w-full bg-[#121528] border border-[#26293c] focus:border-[#58a6ff] rounded px-2.5 py-1.5 text-xs font-mono text-[#dfe1fa] outline-none"
                  required
                />
              </div>

              <div>
                <label className="block text-[10px] font-mono text-[#8d909b] uppercase mb-1">Category</label>
                <select
                  value={formCategory}
                  onChange={e => setFormCategory(e.target.value as QuickCommandCategory)}
                  className="w-full bg-[#121528] border border-[#26293c] focus:border-[#58a6ff] rounded px-2 py-1.5 text-xs font-mono text-[#dfe1fa] outline-none"
                >
                  <option value="system">System</option>
                  <option value="containers">Containers</option>
                  <option value="storage">Storage</option>
                  <option value="network">Network</option>
                  <option value="custom">Custom</option>
                </select>
              </div>
            </div>

            <div>
              <label className="block text-[10px] font-mono text-[#8d909b] uppercase mb-1">
                Description (Tooltip &amp; Note)
              </label>
              <input
                type="text"
                value={formDescription}
                onChange={e => setFormDescription(e.target.value)}
                placeholder="e.g. Inspect ZFS storage pool health and vdev status"
                className="w-full bg-[#121528] border border-[#26293c] focus:border-[#58a6ff] rounded px-2.5 py-1.5 text-xs font-mono text-[#dfe1fa] outline-none"
              />
            </div>

            <div className="flex items-center justify-end gap-2 pt-1">
              <button
                type="button"
                onClick={handleCancelForm}
                className="px-3 py-1 rounded text-xs font-mono text-gray-400 hover:text-white bg-[#121528] border border-[#26293c] cursor-pointer"
              >
                Cancel
              </button>
              <button
                type="submit"
                className="px-3 py-1 rounded text-xs font-mono font-bold text-white bg-[#1f6feb] hover:bg-[#388bfd] cursor-pointer"
              >
                {editingCommand ? 'Save Changes' : 'Add Command'}
              </button>
            </div>
          </form>
        )}

        {quickCommands.length === 0 ? (
          <div className="p-4 text-center text-xs font-mono text-[#8d909b] bg-[#171a2d] rounded border border-[#26293c]">
            No quick commands configured. Click "+ Add Command" or "Reset to Defaults".
          </div>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-2.5">
            {quickCommands.map(qc => {
              const catColor =
                QUICK_CMD_CATEGORY_COLORS[qc.category || 'custom'] || QUICK_CMD_CATEGORY_COLORS.custom;
              return (
                <div
                  key={qc.id}
                  className="p-3 rounded-lg bg-[#171a2d] border border-[#26293c] hover:border-[#388bfd]/50 transition-colors flex flex-col justify-between gap-2.5"
                >
                  <div className="flex items-start justify-between gap-2">
                    <div className="flex items-center gap-1.5 flex-wrap">
                      <span className="px-2 py-0.5 rounded text-xs font-mono font-bold bg-[#1b2a38] text-[#79c0ff] border border-[#79c0ff]/30">
                        {qc.label}
                      </span>
                      <span
                        className={`px-1.5 py-0.2 rounded text-[10px] font-mono uppercase tracking-wider border ${catColor.bg} ${catColor.text} ${catColor.border}`}
                      >
                        {qc.category || 'custom'}
                      </span>
                    </div>

                    <div className="flex items-center gap-1 shrink-0">
                      <button
                        type="button"
                        onClick={() => handleOpenEdit(qc)}
                        className="p-1 rounded text-gray-400 hover:text-[#79c0ff] hover:bg-[#21262d] transition-colors cursor-pointer"
                        title={`Edit command '${qc.label}'`}
                      >
                        <span className="material-symbols-outlined text-[15px]">edit</span>
                      </button>
                      <button
                        type="button"
                        onClick={() => handleDeleteCommand(qc.id)}
                        className="p-1 rounded text-gray-400 hover:text-red-400 hover:bg-[#21262d] transition-colors cursor-pointer"
                        title={`Delete command '${qc.label}'`}
                      >
                        <span className="material-symbols-outlined text-[15px]">delete</span>
                      </button>
                    </div>
                  </div>

                  <div className="bg-[#121528] px-2.5 py-1.5 rounded border border-[#26293c] font-mono text-xs text-[#58a6ff] truncate">
                    <code>{qc.cmd}</code>
                  </div>

                  {qc.description && (
                    <div className="text-[11px] text-[#8d909b] font-mono truncate" title={qc.description}>
                      {qc.description}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </div>
    </SettingsPageFrame>
  );
};

/**
 * Renders exactly one datacenter setting (or setting group) page.
 * The sidebar drives `page`; the parent keeps this component mounted while the
 * route stays inside the settings catalog so unsaved form state survives
 * navigation between settings pages.
 */
export const DatacenterSettingsView: React.FC<DatacenterSettingsViewProps> = ({
  page,
  nodes = [],
  onSelectNode,
}) => {
  const meta = findDatacenterSettingPage(page);
  if (!meta) return null;

  if (meta.category === 'playbooks') {
    return (
      <SettingsPageFrame page={meta.id}>
        <DatacenterPlaybooksPage showHeader={false} />
      </SettingsPageFrame>
    );
  }
  if (meta.id === 'ssh-vault') {
    return <SSHVaultPage page={meta.id} />;
  }
  if (meta.category === 'variables') {
    return (
      <SettingsPageFrame page={meta.id}>
        <DatacenterVarsPage showHeader={false} />
      </SettingsPageFrame>
    );
  }
  if (meta.category === 'storage') {
    return (
      <SettingsPageFrame page={meta.id}>
        <RcloneRemotesView mode="datacenter" />
      </SettingsPageFrame>
    );
  }
  if (meta.category === 'terminal') {
    return <QuickCommandsSettingsPage page={meta.id} />;
  }
  if (meta.id === 'connection/tailscale') {
    return (
      <SettingsPageFrame page={meta.id}>
        <div className="flex flex-col gap-3">
          <DatacenterTailscalePage />
          <DatacenterTopologyCard nodes={nodes} onSelectNode={onSelectNode} />
        </div>
      </SettingsPageFrame>
    );
  }
  if (meta.id === 'ansible-schedules') {
    return (
      <SettingsPageFrame page={meta.id}>
        <CronSchedulesPanel nodes={nodes} />
      </SettingsPageFrame>
    );
  }
  if (meta.id === 'ansible-collections') {
    return <AnsibleCollectionsSettingsPage page={meta.id} />;
  }
  return <AnsibleSettingsPage page={meta.id} />;
};
