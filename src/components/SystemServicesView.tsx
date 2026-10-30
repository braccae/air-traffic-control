import React, { useState, useEffect, useMemo } from 'react';
import type { NodeItem } from '../types/index.ts';

export interface ServiceUnitItem {
  unit: string;
  load: string;
  active: string;
  sub: string;
  description: string;
  /** systemd unit-file state: enabled / disabled / static / masked / ... */
  unitFileState?: string;
  enabled?: boolean;
  /** which manager owns the unit: "system" or "user" */
  scope?: 'system' | 'user';
}

export interface ServiceReadResponse {
  manager?: string;
  services?: ServiceUnitItem[];
  warnings?: string[];
}

/** GET /api/nodes/:id/systemd/units — systemd's own JSON unit listing. */
interface SystemdUnitListResponse {
  nodeId: string;
  manager: string;
  scope: string;
  units: ServiceUnitItem[];
  warnings?: string[];
}

/** GET /api/nodes/:id/systemd/unit — per-unit property projection. */
export interface SystemdUnitState {
  unit: string;
  scope: string;
  description?: string;
  loadState: string;
  activeState: string;
  subState: string;
  unitFileState?: string;
  enabled: boolean;
  result?: string;
  fragmentPath?: string;
  dropInPaths?: string[];
  mainPid?: number;
  nRestarts?: number;
  activeEnterTimestamp?: string;
  stateChangeTimestamp?: string;
  inactiveEnterTimestamp?: string;
}

interface SystemdUnitDetailResponse {
  nodeId: string;
  scope: string;
  state: SystemdUnitState;
}

/** GET /api/nodes/:id/systemd/journal — structured journald records. */
export interface SystemdJournalEntry {
  timestamp: string;
  priority: number;
  level: string;
  unit?: string;
  identifier?: string;
  pid?: string;
  message: string;
}

interface SystemdJournalResponse {
  nodeId: string;
  scope: string;
  unit?: string;
  count: number;
  entries: SystemdJournalEntry[];
}

interface SystemServicesViewProps {
  node: NodeItem | null;
  onRefresh?: () => void;
  onOpenTask?: (taskId: string) => void;
}

export const SystemServicesView: React.FC<SystemServicesViewProps> = ({
  node,
  onRefresh,
}) => {
  if (!node) {
    return (
      <div className="flex flex-col gap-3 font-mono">
        <div className="bg-[#171a2d] border border-[#26293c] rounded p-3">
          <div className="text-[11px] text-[#8d909b]">No node selected</div>
        </div>
      </div>
    );
  }
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [services, setServices] = useState<ServiceUnitItem[]>([]);
  const [manager, setManager] = useState<string>('systemd');
  const [searchQuery, setSearchQuery] = useState('');
  const [statusFilter, setStatusFilter] = useState<'all' | 'running' | 'failed' | 'inactive'>('all');
  const [actionInProgress, setActionInProgress] = useState<string | null>(null);
  const [toastMessage, setToastMessage] = useState<{ text: string; type: 'success' | 'error' } | null>(null);
  const [showRebootModal, setShowRebootModal] = useState(false);
  const [scopeFilter, setScopeFilter] = useState<'all' | 'system' | 'user'>('all');
  const [warnings, setWarnings] = useState<string[]>([]);
  const [selectedUnit, setSelectedUnit] = useState<ServiceUnitItem | null>(null);
  const [unitState, setUnitState] = useState<SystemdUnitState | null>(null);
  const [unitStateError, setUnitStateError] = useState<string | null>(null);
  const [journal, setJournal] = useState<SystemdJournalEntry[]>([]);
  const [journalError, setJournalError] = useState<string | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [userManagers, setUserManagers] = useState<Array<{ uid: string; name: string }>>([]);
  const [selectedUid, setSelectedUid] = useState<string>('');

  // Unit list comes from the daemon's systemd routes, which read systemd's own
  // JSON (list-units + list-unit-files) for both the system and user managers.
  // The plugin endpoint remains the fallback for non-systemd init systems.
  const fetchServices = async () => {
    if (!node?.id) return;
    setLoading(true);
    setError(null);
    try {
      const scopeParam =
        scopeFilter === 'system' ? 'system' : scopeFilter === 'user' ? `user${selectedUid ? `&uid=${encodeURIComponent(selectedUid)}` : ''}` : 'all';
      const sysdRes = await fetch(
        `/api/nodes/${encodeURIComponent(node.id)}/systemd/units?scope=${scopeParam}`
      );
      if (sysdRes.ok) {
        const data: SystemdUnitListResponse = await sysdRes.json();
        if (Array.isArray(data.units)) {
          setServices(data.units);
          setManager(data.manager || 'systemd');
          setWarnings(Array.isArray(data.warnings) ? data.warnings : []);
          return;
        }
      }

      const res = await fetch(`/api/nodes/${encodeURIComponent(node.id)}/plugins/system/service`);
      if (!res.ok) {
        const text = await res.text();
        throw new Error(text || `Failed to fetch services (HTTP ${res.status})`);
      }
      const data: ServiceReadResponse = await res.json();
      if (Array.isArray(data.services)) {
        setServices(data.services);
      } else {
        setServices([]);
      }
      setWarnings(Array.isArray(data.warnings) ? data.warnings : []);
      if (data.manager) {
        setManager(data.manager);
      }
    } catch (err: any) {
      setError(err?.message || 'Failed to load system services from host');
    } finally {
      setLoading(false);
    }
  };

  // Per-user managers present on the host (drives the user selector).
  useEffect(() => {
    let cancelled = false;
    if (!node?.id) {
      setUserManagers([]);
      setSelectedUid('');
      return;
    }
    fetch(`/api/nodes/${encodeURIComponent(node.id)}/systemd/users`)
      .then(res => (res.ok ? res.json() : null))
      .then(data => {
        if (cancelled) return;
        const users = Array.isArray(data?.users) ? data.users : [];
        setUserManagers(users);
        setSelectedUid(prev => (prev && users.some((u: any) => u.uid === prev) ? prev : users[0]?.uid ?? ''));
      })
      .catch(() => {
        if (!cancelled) setUserManagers([]);
      });
    return () => {
      cancelled = true;
    };
  }, [node?.id]);

  // Per-unit detail: systemd property state plus that unit's journal.
  const loadUnitDetail = async (unit: ServiceUnitItem) => {
    if (!node?.id) return;
    const scope = unit.scope === 'user' ? 'user' : 'system';
    const uidParam = scope === 'user' && selectedUid ? `&uid=${encodeURIComponent(selectedUid)}` : '';
    setSelectedUnit(unit);
    setUnitState(null);
    setJournal([]);
    setUnitStateError(null);
    setJournalError(null);
    setDetailLoading(true);
    try {
      const [stateRes, journalRes] = await Promise.all([
        fetch(
          `/api/nodes/${encodeURIComponent(node.id)}/systemd/unit?unit=${encodeURIComponent(unit.unit)}&scope=${scope}${uidParam}`
        ),
        fetch(
          `/api/nodes/${encodeURIComponent(node.id)}/systemd/journal?unit=${encodeURIComponent(unit.unit)}&scope=${scope}${uidParam}&lines=200`
        ),
      ]);

      if (stateRes.ok) {
        const detail: SystemdUnitDetailResponse = await stateRes.json();
        setUnitState(detail.state);
      } else {
        const errJson = await stateRes.json().catch(() => ({}));
        setUnitStateError(errJson.error || `Failed to read unit state (HTTP ${stateRes.status})`);
      }

      if (journalRes.ok) {
        const data: SystemdJournalResponse = await journalRes.json();
        setJournal(Array.isArray(data.entries) ? data.entries : []);
      } else {
        const errJson = await journalRes.json().catch(() => ({}));
        setJournalError(errJson.error || `Failed to read journal (HTTP ${journalRes.status})`);
      }
    } catch (err: any) {
      setUnitStateError(err?.message || 'Failed to read unit detail');
    } finally {
      setDetailLoading(false);
    }
  };

  useEffect(() => {
    fetchServices();
  }, [node?.id, scopeFilter, selectedUid]);

  const handleServiceAction = async (
    unitName: string,
    actionName: string,
    scope: 'system' | 'user' = 'system'
  ) => {
    setActionInProgress(`${actionName}:${unitName}`);
    setToastMessage(null);
    try {
      const compileRes = await fetch(
        `/api/nodes/${encodeURIComponent(node.id)}/plugins/system/service/actions/${encodeURIComponent(actionName)}`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ options: { name: unitName, scope } }),
        }
      );

      if (!compileRes.ok) {
        const errJson = await compileRes.json().catch(() => ({}));
        throw new Error(errJson.error || `Failed to compile ${actionName} action`);
      }

      const taskSpec = await compileRes.json();

      const queueRes = await fetch('/api/queue/task', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          title: taskSpec.name || `${actionName} ${unitName} on ${node.name}`,
          targetNodeId: node.id,
          targetNodeName: node.name,
          targetHostRoute: node.tailscaleHost || node.tailscaleIp || node.ip || 'localhost',
          targetSshUser: node.sshUser || 'core',
          targetSshPort: node.port || 22,
          category: 'system',
          primaryModule: taskSpec.steps?.[0]?.module || 'ansible.builtin.systemd_service',
          steps: taskSpec.steps || [],
          become: true,
          status: 'staged',
        }),
      });

      if (!queueRes.ok) {
        throw new Error('Failed to stage service task into Ansible Queue');
      }

      setToastMessage({
        text: `Action "${actionName}" for ${unitName} staged into mutation queue.`,
        type: 'success',
      });
      setTimeout(() => fetchServices(), 1500);
    } catch (err: any) {
      setToastMessage({
        text: err?.message || `Failed to perform ${actionName} on ${unitName}`,
        type: 'error',
      });
    } finally {
      setActionInProgress(null);
    }
  };

  const handleDaemonReload = async () => {
    setActionInProgress('daemon-reload');
    setToastMessage(null);
    try {
      const compileRes = await fetch(
        `/api/nodes/${encodeURIComponent(node.id)}/plugins/system/service/actions/daemon-reload`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ options: {} }),
        }
      );

      if (!compileRes.ok) {
        throw new Error('Failed to compile daemon-reload task');
      }

      const taskSpec = await compileRes.json();

      const queueRes = await fetch('/api/queue/task', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          title: taskSpec.name || `Reload systemd daemon on ${node.name}`,
          targetNodeId: node.id,
          targetNodeName: node.name,
          targetHostRoute: node.tailscaleHost || node.tailscaleIp || node.ip || 'localhost',
          targetSshUser: node.sshUser || 'core',
          targetSshPort: node.port || 22,
          category: 'system',
          primaryModule: 'ansible.builtin.systemd_service',
          steps: taskSpec.steps || [],
          become: true,
          status: 'staged',
        }),
      });

      if (!queueRes.ok) throw new Error('Failed to stage daemon-reload task');

      setToastMessage({
        text: 'systemctl daemon-reload staged into mutation queue.',
        type: 'success',
      });
    } catch (err: any) {
      setToastMessage({
        text: err?.message || 'Failed to trigger daemon reload',
        type: 'error',
      });
    } finally {
      setActionInProgress(null);
    }
  };

  const handleReboot = async () => {
    setShowRebootModal(false);
    setActionInProgress('reboot');
    setToastMessage(null);
    try {
      const compileRes = await fetch(
        `/api/nodes/${encodeURIComponent(node.id)}/plugins/system/service/actions/reboot`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ options: { msg: 'Reboot initiated via Cockpit Services view', timeout: 300 } }),
        }
      );

      if (!compileRes.ok) throw new Error('Failed to compile reboot task');
      const taskSpec = await compileRes.json();

      const queueRes = await fetch('/api/queue/task', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          title: taskSpec.name || `Reboot ${node.name}`,
          targetNodeId: node.id,
          targetNodeName: node.name,
          targetHostRoute: node.tailscaleHost || node.tailscaleIp || node.ip || 'localhost',
          targetSshUser: node.sshUser || 'core',
          targetSshPort: node.port || 22,
          category: 'system',
          primaryModule: 'ansible.builtin.reboot',
          steps: taskSpec.steps || [],
          become: true,
          status: 'staged',
        }),
      });

      if (!queueRes.ok) throw new Error('Failed to stage reboot task');

      setToastMessage({
        text: `Reboot task for ${node.name} staged into mutation queue.`,
        type: 'success',
      });
    } catch (err: any) {
      setToastMessage({
        text: err?.message || 'Failed to schedule host reboot',
        type: 'error',
      });
    } finally {
      setActionInProgress(null);
    }
  };

  // Stats calculation
  const totalCount = services.length;
  const runningCount = services.filter(s => s.active === 'active' && (s.sub === 'running' || s.sub === 'exited')).length;
  const failedCount = services.filter(s => s.active === 'failed' || s.sub === 'failed').length;
  const inactiveCount = services.filter(s => s.active !== 'active' && s.active !== 'failed').length;

  // Filtered services
  const filteredServices = useMemo(() => {
    return services.filter(s => {
      // Manager scope filter
      if (scopeFilter !== 'all' && (s.scope || 'system') !== scopeFilter) return false;

      // Status filter
      if (statusFilter === 'running' && !(s.active === 'active' && (s.sub === 'running' || s.sub === 'exited'))) return false;
      if (statusFilter === 'failed' && !(s.active === 'failed' || s.sub === 'failed')) return false;
      if (statusFilter === 'inactive' && (s.active === 'active' || s.active === 'failed')) return false;

      // Text search
      if (!searchQuery.trim()) return true;
      const q = searchQuery.toLowerCase().trim();
      return (
        s.unit.toLowerCase().includes(q) ||
        (s.description && s.description.toLowerCase().includes(q))
      );
    });
  }, [services, statusFilter, scopeFilter, searchQuery]);

  return (
    <div className="flex flex-col gap-3 font-mono">
      {/* Header Bar */}
      <div className="bg-[#171a2d] border border-[#26293c] rounded p-3 flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2.5">
          <div className="p-2 rounded bg-[#26293c]/60 border border-[#434750]/30 text-[#8cd6cb]">
            <span className="material-symbols-outlined text-[20px]">activity</span>
          </div>
          <div>
            <div className="flex items-center gap-2">
              <h3 className="text-sm font-bold text-[#dfe1fa]">System Services &amp; Lifecycle</h3>
              <span className="px-1.5 py-0.2 rounded text-[10px] uppercase font-bold bg-[#26293c] text-[#8cd6cb] border border-[#8cd6cb]/30">
                {manager}
              </span>
            </div>
            <p className="text-[11px] text-[#8d909b]">
              Target: <span className="text-[#dfe1fa] font-medium">{node.name}</span> ({node.tailscaleHost || node.ip})
            </p>
          </div>
        </div>

        {/* Global Action Buttons */}
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={handleDaemonReload}
            disabled={Boolean(actionInProgress)}
            className="flex items-center gap-1.5 px-2.5 py-1 rounded bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] text-[11px] font-semibold border border-[#434750]/40 cursor-pointer disabled:opacity-50 transition-colors"
            title="Reload systemd unit configuration"
          >
            <span className="material-symbols-outlined text-[13px] text-[#8cd6cb]">sync</span>
            <span>Daemon Reload</span>
          </button>

          <button
            type="button"
            onClick={() => setShowRebootModal(true)}
            disabled={Boolean(actionInProgress)}
            className="flex items-center gap-1.5 px-2.5 py-1 rounded bg-[#331114] hover:bg-[#4d161a] text-[#ffb4ab] text-[11px] font-semibold border border-[#ffb4ab]/30 cursor-pointer disabled:opacity-50 transition-colors"
            title="Safely restart the host operating system"
          >
            <span className="material-symbols-outlined text-[13px]">power_settings_new</span>
            <span>Reboot</span>
          </button>

          <button
            type="button"
            onClick={() => {
              fetchServices();
              onRefresh?.();
            }}
            disabled={loading}
            className="p-1 rounded bg-[#26293c] hover:bg-[#35384c] text-[#8d909b] hover:text-[#dfe1fa] cursor-pointer transition-colors"
            title="Refresh Services"
          >
            <span className={`material-symbols-outlined text-[16px] ${loading ? 'animate-spin' : ''}`}>
              refresh
            </span>
          </button>
        </div>
      </div>

      {/* Non-fatal read warnings (e.g. no reachable user manager) */}
      {warnings.length > 0 && (
        <div className="p-2 rounded text-[10px] bg-[#2a2414] text-[#e8c78c] border border-[#e8c78c]/30 flex flex-col gap-1">
          {warnings.map(w => (
            <span key={w}>⚠ {w}</span>
          ))}
        </div>
      )}

      {/* Toast Notice */}
      {toastMessage && (
        <div
          className={`p-2.5 rounded text-[11px] flex items-center justify-between border ${
            toastMessage.type === 'error'
              ? 'bg-[#331114] text-[#ffb4ab] border-[#ffb4ab]/30'
              : 'bg-[#0f231e] text-[#8cd6cb] border-[#8cd6cb]/30'
          }`}
        >
          <span>{toastMessage.text}</span>
          <button
            type="button"
            onClick={() => setToastMessage(null)}
            className="text-[12px] opacity-70 hover:opacity-100 cursor-pointer ml-2"
          >
            ✕
          </button>
        </div>
      )}

      {/* Stat Metric Cards */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-2">
        <button
          type="button"
          onClick={() => setStatusFilter('all')}
          className={`p-2 rounded text-left border cursor-pointer transition-all ${
            statusFilter === 'all'
              ? 'bg-[#21263d] border-[#b0c9ff]/60 shadow-xs'
              : 'bg-[#171a2d] border-[#26293c] hover:bg-[#1c2035]'
          }`}
        >
          <div className="text-[10px] text-[#8d909b] uppercase font-semibold">Total Services</div>
          <div className="text-base font-bold text-[#dfe1fa] mt-0.5">{totalCount}</div>
        </button>

        <button
          type="button"
          onClick={() => setStatusFilter('running')}
          className={`p-2 rounded text-left border cursor-pointer transition-all ${
            statusFilter === 'running'
              ? 'bg-[#0e2a24] border-[#8cd6cb]/60 shadow-xs'
              : 'bg-[#171a2d] border-[#26293c] hover:bg-[#1c2035]'
          }`}
        >
          <div className="text-[10px] text-[#8d909b] uppercase font-semibold flex items-center gap-1.5">
            <span className="w-1.5 h-1.5 rounded-full bg-[#8cd6cb]"></span>
            <span>Running</span>
          </div>
          <div className="text-base font-bold text-[#8cd6cb] mt-0.5">{runningCount}</div>
        </button>

        <button
          type="button"
          onClick={() => setStatusFilter('failed')}
          className={`p-2 rounded text-left border cursor-pointer transition-all ${
            statusFilter === 'failed'
              ? 'bg-[#331114] border-[#ffb4ab]/60 shadow-xs'
              : 'bg-[#171a2d] border-[#26293c] hover:bg-[#1c2035]'
          }`}
        >
          <div className="text-[10px] text-[#8d909b] uppercase font-semibold flex items-center gap-1.5">
            <span className={`w-1.5 h-1.5 rounded-full bg-[#ffb4ab] ${failedCount > 0 ? 'animate-ping' : ''}`}></span>
            <span>Failed</span>
          </div>
          <div className="text-base font-bold text-[#ffb4ab] mt-0.5">{failedCount}</div>
        </button>

        <button
          type="button"
          onClick={() => setStatusFilter('inactive')}
          className={`p-2 rounded text-left border cursor-pointer transition-all ${
            statusFilter === 'inactive'
              ? 'bg-[#21263d] border-[#8d909b]/60 shadow-xs'
              : 'bg-[#171a2d] border-[#26293c] hover:bg-[#1c2035]'
          }`}
        >
          <div className="text-[10px] text-[#8d909b] uppercase font-semibold flex items-center gap-1.5">
            <span className="w-1.5 h-1.5 rounded-full bg-[#8d909b]"></span>
            <span>Inactive / Dead</span>
          </div>
          <div className="text-base font-bold text-[#8d909b] mt-0.5">{inactiveCount}</div>
        </button>
      </div>

      {/* Filter and Search Bar */}
      <div className="bg-[#171a2d] border border-[#26293c] rounded p-2 flex items-center justify-between gap-2">
        <div className="flex items-center gap-2 flex-1 min-w-0 bg-[#0a0d1f] px-2.5 py-1 rounded border border-[#26293c]/80">
          <span className="material-symbols-outlined text-[14px] text-[#8d909b]">search</span>
          <input
            type="text"
            value={searchQuery}
            onChange={e => setSearchQuery(e.target.value)}
            placeholder="Search service name, description (e.g. sshd, tailscaled, caddy)..."
            className="w-full bg-transparent text-[#dfe1fa] text-[11px] outline-none"
          />
          {searchQuery && (
            <button
              type="button"
              onClick={() => setSearchQuery('')}
              className="text-[#8d909b] hover:text-[#dfe1fa] text-[11px] cursor-pointer"
            >
              ✕
            </button>
          )}
        </div>

        {/* Manager scope filter: system units vs the user manager's units */}
        <div className="flex items-center gap-1 shrink-0">
          {scopeFilter === 'user' && (
            <label className="flex items-center gap-1 mr-1 text-[9px] text-[#8d909b] uppercase font-bold">
              user
              <select
                value={selectedUid}
                onChange={e => setSelectedUid(e.target.value)}
                data-testid="systemd-user-selector"
                disabled={userManagers.length === 0}
                className="bg-[#121528] border border-[#26293c] rounded px-1 py-0.5 text-[9px] font-mono text-[#dfe1fa] normal-case font-normal disabled:opacity-50"
                title="Per-user systemd manager (/run/user/<uid>)"
              >
                {userManagers.length === 0 && <option value="">no user manager</option>}
                {userManagers.map(u => (
                  <option key={u.uid} value={u.uid}>
                    {u.name} ({u.uid})
                  </option>
                ))}
              </select>
            </label>
          )}
          {(['all', 'system', 'user'] as const).map(s => (
            <button
              key={s}
              type="button"
              onClick={() => setScopeFilter(s)}
              className={`px-1.5 py-0.5 rounded text-[9px] uppercase font-bold border cursor-pointer transition-colors ${
                scopeFilter === s
                  ? 'bg-[#21263d] text-[#b0c9ff] border-[#b0c9ff]/50'
                  : 'bg-[#171a2d] text-[#8d909b] border-[#26293c] hover:text-[#dfe1fa]'
              }`}
              title={s === 'user' ? 'Units from the per-user systemd manager' : s === 'system' ? 'Units from the host systemd manager' : 'All units from both managers'}
            >
              {s}
            </button>
          ))}
        </div>

        <div className="text-[10px] text-[#8d909b] shrink-0 font-medium">
          Showing {filteredServices.length} of {totalCount} units
        </div>
      </div>

      {/* Services Table or Empty/Error State */}
      {loading ? (
        <div className="p-8 bg-[#171a2d] border border-[#26293c] rounded text-center text-[11px] text-[#8d909b] flex flex-col items-center gap-2">
          <span className="material-symbols-outlined text-[24px] text-[#8cd6cb] animate-spin">sync</span>
          <span>Querying system service states from host daemon...</span>
        </div>
      ) : error ? (
        <div className="p-4 bg-[#331114] border border-[#ffb4ab]/30 rounded text-[11px] text-[#ffb4ab] flex flex-col gap-2">
          <div className="flex items-center gap-2 font-bold">
            <span className="material-symbols-outlined text-[16px]">error</span>
            <span>Failed to read service units</span>
          </div>
          <div className="font-mono text-[10px] break-all">{error}</div>
          <button
            type="button"
            onClick={fetchServices}
            className="self-start px-2 py-0.5 rounded bg-[#4d161a] text-[#dfe1fa] text-[10px] hover:bg-[#661e23] cursor-pointer"
          >
            Retry
          </button>
        </div>
      ) : filteredServices.length === 0 ? (
        <div className="p-8 bg-[#171a2d] border border-[#26293c] rounded text-center text-[11px] text-[#8d909b]">
          No system services matching current filter.
        </div>
      ) : (
        <div className="bg-[#171a2d] border border-[#26293c] rounded overflow-hidden">
          <div className="overflow-x-auto">
            <table className="w-full text-left text-[11px] border-collapse">
              <thead>
                <tr className="bg-[#121528] text-[#8d909b] text-[10px] uppercase font-bold border-b border-[#26293c]">
                  <th className="py-2 px-3">Service Unit</th>
                  <th className="py-2 px-3">Active State</th>
                  <th className="py-2 px-3">Sub</th>
                  <th className="py-2 px-3">Description</th>
                  <th className="py-2 px-3 text-right">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-[#26293c]/50">
                {filteredServices.map(srv => {
                  const isRunning = srv.active === 'active' && (srv.sub === 'running' || srv.sub === 'exited');
                  const isFailed = srv.active === 'failed' || srv.sub === 'failed';
                  const inProgress = actionInProgress?.includes(srv.unit);

                  return (
                    <tr
                      key={`${srv.unit}-${srv.scope || 'system'}`}
                      className="hover:bg-[#1b1e31] transition-colors group"
                    >
                      <td className="py-2 px-3 font-semibold text-[#dfe1fa] min-w-[220px]">
                        <button
                          type="button"
                          onClick={() => loadUnitDetail(srv)}
                          className="flex items-center gap-2 cursor-pointer text-left hover:text-[#b0c9ff] transition-colors"
                          title={`Open ${srv.unit} state and journal`}
                        >
                          <span className="material-symbols-outlined text-[14px] text-[#8d909b]">
                            settings_suggest
                          </span>
                          <span className="truncate">{srv.unit}</span>
                          {srv.scope === 'user' && (
                            <span className="px-1 rounded text-[8px] uppercase font-bold bg-[#26293c] text-[#b0c9ff] border border-[#b0c9ff]/30">
                              user
                            </span>
                          )}
                          {srv.unitFileState && (
                            <span className="px-1 rounded text-[8px] uppercase font-bold bg-[#1f2238] text-[#8d909b] border border-[#26293c]">
                              {srv.unitFileState}
                            </span>
                          )}
                        </button>
                      </td>
                      <td className="py-2 px-3 whitespace-nowrap">
                        <span
                          className={`px-1.5 py-0.2 rounded text-[9px] font-bold uppercase tracking-wider inline-flex items-center gap-1 border ${
                            isRunning
                              ? 'bg-[#0f231e] text-[#8cd6cb] border-[#8cd6cb]/30'
                              : isFailed
                              ? 'bg-[#331114] text-[#ffb4ab] border-[#ffb4ab]/30'
                              : 'bg-[#1f2238] text-[#8d909b] border-[#26293c]'
                          }`}
                        >
                          <span
                            className={`w-1 h-1 rounded-full ${
                              isRunning
                                ? 'bg-[#8cd6cb]'
                                : isFailed
                                ? 'bg-[#ffb4ab] animate-pulse'
                                : 'bg-[#8d909b]'
                            }`}
                          />
                          {srv.active}
                        </span>
                      </td>
                      <td className="py-2 px-3 whitespace-nowrap text-[#c3c6d2] font-mono text-[10px]">
                        {srv.sub}
                      </td>
                      <td className="py-2 px-3 text-[#8d909b] truncate max-w-xs" title={srv.description}>
                        {srv.description || '—'}
                      </td>
                      <td className="py-2 px-3 text-right whitespace-nowrap">
                        <div className="flex items-center justify-end gap-1">
                          {/* Journal / detail */}
                          <button
                            type="button"
                            onClick={() => loadUnitDetail(srv)}
                            className="p-1 rounded bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] text-[10px] cursor-pointer transition-colors"
                            title={`Show ${srv.unit} state and journal`}
                          >
                            <span className="material-symbols-outlined text-[13px]">description</span>
                          </button>

                          {/* Restart Button */}
                          <button
                            type="button"
                            onClick={() => handleServiceAction(srv.unit, 'restart', srv.scope || 'system')}
                            disabled={inProgress}
                            className="p-1 rounded bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] text-[10px] cursor-pointer disabled:opacity-40 transition-colors"
                            title={`Restart ${srv.unit}`}
                          >
                            <span className="material-symbols-outlined text-[13px] text-[#b0c9ff]">
                              restart_alt
                            </span>
                          </button>

                          {/* Stop / Start Button */}
                          {isRunning ? (
                            <button
                              type="button"
                              onClick={() => handleServiceAction(srv.unit, 'stop', srv.scope || 'system')}
                              disabled={inProgress}
                              className="p-1 rounded bg-[#331114] hover:bg-[#4d161a] text-[#ffb4ab] text-[10px] cursor-pointer disabled:opacity-40 transition-colors"
                              title={`Stop ${srv.unit}`}
                            >
                              <span className="material-symbols-outlined text-[13px]">
                                stop
                              </span>
                            </button>
                          ) : (
                            <button
                              type="button"
                              onClick={() => handleServiceAction(srv.unit, 'start', srv.scope || 'system')}
                              disabled={inProgress}
                              className="p-1 rounded bg-[#0f231e] hover:bg-[#193a32] text-[#8cd6cb] text-[10px] cursor-pointer disabled:opacity-40 transition-colors"
                              title={`Start ${srv.unit}`}
                            >
                              <span className="material-symbols-outlined text-[13px]">
                                play_arrow
                              </span>
                            </button>
                          )}

                          {/* Enable / Disable, driven by systemd's unit-file state */}
                          {srv.enabled ? (
                            <button
                              type="button"
                              onClick={() => handleServiceAction(srv.unit, 'disable', srv.scope || 'system')}
                              disabled={inProgress}
                              className="px-1.5 py-0.5 rounded bg-[#26293c] hover:bg-[#35384c] text-[#c3c6d2] hover:text-[#dfe1fa] text-[9px] uppercase font-bold cursor-pointer disabled:opacity-40 transition-colors"
                              title={`Disable ${srv.unit} at boot`}
                            >
                              Disable
                            </button>
                          ) : (
                            <button
                              type="button"
                              onClick={() => handleServiceAction(srv.unit, 'enable', srv.scope || 'system')}
                              disabled={inProgress}
                              className="px-1.5 py-0.5 rounded bg-[#26293c] hover:bg-[#35384c] text-[#c3c6d2] hover:text-[#dfe1fa] text-[9px] uppercase font-bold cursor-pointer disabled:opacity-40 transition-colors"
                              title={`Enable ${srv.unit} at boot`}
                            >
                              Enable
                            </button>
                          )}
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Unit detail drawer: systemd property state + that unit's journal */}
      {selectedUnit && (
        <div className="fixed inset-0 bg-black/70 flex items-center justify-center z-50 p-4">
          <div className="bg-[#171a2d] border border-[#26293c] rounded-lg w-full max-w-4xl max-h-[85vh] flex flex-col gap-3 p-4 shadow-xl overflow-hidden">
            <div className="flex items-start justify-between gap-3">
              <div>
                <div className="flex items-center gap-2 text-[#dfe1fa]">
                  <span className="material-symbols-outlined text-[18px] text-[#8cd6cb]">description</span>
                  <h4 className="text-sm font-bold break-all">{selectedUnit.unit}</h4>
                  <span className="px-1.5 py-0.2 rounded text-[9px] uppercase font-bold bg-[#26293c] text-[#b0c9ff] border border-[#b0c9ff]/30">
                    {unitState?.scope || selectedUnit.scope || 'system'}
                  </span>
                </div>
                <p className="text-[10px] text-[#8d909b] mt-0.5">
                  state from systemctl show, journal from journalctl -o json
                </p>
              </div>
              <button
                type="button"
                onClick={() => setSelectedUnit(null)}
                className="text-[#8d909b] hover:text-[#dfe1fa] text-[12px] cursor-pointer"
                title="Close"
              >
                ✕
              </button>
            </div>

            {detailLoading ? (
              <div className="p-6 text-center text-[11px] text-[#8d909b] flex items-center justify-center gap-2">
                <span className="material-symbols-outlined text-[18px] text-[#8cd6cb] animate-spin">sync</span>
                <span>Reading unit state and journal from host...</span>
              </div>
            ) : (
              <div className="flex flex-col gap-3 overflow-y-auto">
                {unitStateError && (
                  <div className="p-2 rounded bg-[#331114] border border-[#ffb4ab]/30 text-[10px] text-[#ffb4ab] break-all">
                    {unitStateError}
                  </div>
                )}

                {unitState && (
                  <div className="grid grid-cols-2 md:grid-cols-4 gap-2 text-[10px]">
                    {[
                      ['Load', unitState.loadState],
                      ['Active', unitState.activeState],
                      ['Sub', unitState.subState],
                      ['Unit file', unitState.unitFileState || '—'],
                      ['Result', unitState.result || '—'],
                      ['Main PID', unitState.mainPid ? String(unitState.mainPid) : '—'],
                      ['Restarts', String(unitState.nRestarts ?? 0)],
                      ['Since', unitState.activeEnterTimestamp || '—'],
                    ].map(([label, value]) => (
                      <div key={label} className="p-1.5 rounded bg-[#0f1224] border border-[#26293c]/80">
                        <div className="text-[9px] uppercase font-bold text-[#8d909b]">{label}</div>
                        <div className="text-[#dfe1fa] break-all">{value}</div>
                      </div>
                    ))}
                    <div className="md:col-span-4 p-1.5 rounded bg-[#0f1224] border border-[#26293c]/80">
                      <div className="text-[9px] uppercase font-bold text-[#8d909b]">Fragment / drop-ins</div>
                      <div className="text-[#c3c6d2] break-all font-mono">
                        {unitState.fragmentPath || '—'}
                        {(unitState.dropInPaths || []).map(p => (
                          <div key={p} className="text-[#8cd6cb]">↳ {p}</div>
                        ))}
                      </div>
                    </div>
                  </div>
                )}

                <div className="flex items-center justify-between">
                  <div className="text-[10px] uppercase font-bold text-[#8d909b]">
                    Journal ({journal.length})
                  </div>
                  <button
                    type="button"
                    onClick={() => loadUnitDetail(selectedUnit)}
                    className="px-2 py-0.5 rounded bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] text-[10px] cursor-pointer"
                  >
                    Reload
                  </button>
                </div>

                {journalError ? (
                  <div className="p-2 rounded bg-[#331114] border border-[#ffb4ab]/30 text-[10px] text-[#ffb4ab] break-all">
                    {journalError}
                  </div>
                ) : journal.length === 0 ? (
                  <div className="p-6 text-center text-[10px] text-[#8d909b] bg-[#0f1224] border border-[#26293c]/80 rounded">
                    No journal records for this unit in the requested window.
                  </div>
                ) : (
                  <div className="bg-[#0a0d1f] border border-[#26293c] rounded overflow-auto max-h-[45vh]">
                    <table className="w-full text-left text-[10px] border-collapse">
                      <tbody className="divide-y divide-[#26293c]/40">
                        {journal.map((entry, idx) => (
                          <tr key={`${entry.timestamp}-${idx}`} className="align-top hover:bg-[#141830]">
                            <td className="py-1 px-2 whitespace-nowrap text-[#8d909b] font-mono">
                              {entry.timestamp || '—'}
                            </td>
                            <td className="py-1 px-2 whitespace-nowrap">
                              <span
                                className={`px-1 rounded text-[8px] uppercase font-bold border ${
                                  entry.priority <= 3
                                    ? 'bg-[#331114] text-[#ffb4ab] border-[#ffb4ab]/30'
                                    : entry.priority <= 4
                                    ? 'bg-[#2a2414] text-[#e8c78c] border-[#e8c78c]/30'
                                    : 'bg-[#1f2238] text-[#8d909b] border-[#26293c]'
                                }`}
                              >
                                {entry.level || String(entry.priority)}
                              </span>
                            </td>
                            <td className="py-1 px-2 whitespace-nowrap text-[#8cd6cb] font-mono">
                              {entry.identifier || entry.unit || '—'}
                              {entry.pid ? `[${entry.pid}]` : ''}
                            </td>
                            <td className="py-1 px-2 text-[#dfe1fa] font-mono break-all whitespace-pre-wrap">
                              {entry.message}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
              </div>
            )}
          </div>
        </div>
      )}

      {/* Reboot Host Confirmation Modal */}
      {showRebootModal && (
        <div className="fixed inset-0 bg-black/70 flex items-center justify-center z-50 p-4">
          <div className="bg-[#171a2d] border border-[#26293c] rounded-lg max-w-md w-full p-4 flex flex-col gap-3 shadow-xl">
            <div className="flex items-center gap-2 text-[#ffb4ab]">
              <span className="material-symbols-outlined text-[20px]">warning</span>
              <h4 className="text-sm font-bold">Confirm Host Reboot</h4>
            </div>
            <p className="text-[11px] text-[#c3c6d2]">
              Are you sure you want to reboot <strong className="text-[#dfe1fa]">{node.name}</strong> ({node.tailscaleHost || node.ip})?
              All running containers, sessions, and services on this node will temporarily disconnect.
            </p>
            <div className="flex items-center justify-end gap-2 pt-2 border-t border-[#26293c]">
              <button
                type="button"
                onClick={() => setShowRebootModal(false)}
                className="px-3 py-1 rounded bg-[#26293c] text-[#dfe1fa] text-[11px] hover:bg-[#35384c] cursor-pointer"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={handleReboot}
                className="px-3 py-1 rounded bg-[#4d161a] text-[#ffb4ab] text-[11px] font-bold hover:bg-[#661e23] cursor-pointer"
              >
                Reboot Now
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
