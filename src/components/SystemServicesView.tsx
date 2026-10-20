import React, { useState, useEffect, useMemo } from 'react';
import type { NodeItem } from '../types/index.ts';

export interface ServiceUnitItem {
  unit: string;
  load: string;
  active: string;
  sub: string;
  description: string;
}

export interface ServiceReadResponse {
  manager?: string;
  services?: ServiceUnitItem[];
}

interface SystemServicesViewProps {
  node: NodeItem;
  onRefresh?: () => void;
  onOpenTask?: (taskId: string) => void;
}

export const SystemServicesView: React.FC<SystemServicesViewProps> = ({
  node,
  onRefresh,
}) => {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [services, setServices] = useState<ServiceUnitItem[]>([]);
  const [manager, setManager] = useState<string>('systemd');
  const [searchQuery, setSearchQuery] = useState('');
  const [statusFilter, setStatusFilter] = useState<'all' | 'running' | 'failed' | 'inactive'>('all');
  const [actionInProgress, setActionInProgress] = useState<string | null>(null);
  const [toastMessage, setToastMessage] = useState<{ text: string; type: 'success' | 'error' } | null>(null);
  const [showRebootModal, setShowRebootModal] = useState(false);

  const fetchServices = async () => {
    if (!node?.id) return;
    setLoading(true);
    setError(null);
    try {
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
      if (data.manager) {
        setManager(data.manager);
      }
    } catch (err: any) {
      setError(err?.message || 'Failed to load system services from host');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchServices();
  }, [node?.id]);

  const handleServiceAction = async (unitName: string, actionName: string) => {
    setActionInProgress(`${actionName}:${unitName}`);
    setToastMessage(null);
    try {
      const compileRes = await fetch(
        `/api/nodes/${encodeURIComponent(node.id)}/plugins/system/service/actions/${encodeURIComponent(actionName)}`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ options: { name: unitName } }),
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
          primaryModule: taskSpec.steps?.[0]?.module || 'ansible.builtin.systemd',
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
          primaryModule: 'ansible.builtin.systemd',
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
  }, [services, statusFilter, searchQuery]);

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
                      key={srv.unit}
                      className="hover:bg-[#1b1e31] transition-colors group"
                    >
                      <td className="py-2 px-3 font-semibold text-[#dfe1fa] flex items-center gap-2 min-w-[200px]">
                        <span className="material-symbols-outlined text-[14px] text-[#8d909b]">
                          settings_suggest
                        </span>
                        <span className="truncate">{srv.unit}</span>
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
                          {/* Restart Button */}
                          <button
                            type="button"
                            onClick={() => handleServiceAction(srv.unit, 'restart')}
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
                              onClick={() => handleServiceAction(srv.unit, 'stop')}
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
                              onClick={() => handleServiceAction(srv.unit, 'start')}
                              disabled={inProgress}
                              className="p-1 rounded bg-[#0f231e] hover:bg-[#193a32] text-[#8cd6cb] text-[10px] cursor-pointer disabled:opacity-40 transition-colors"
                              title={`Start ${srv.unit}`}
                            >
                              <span className="material-symbols-outlined text-[13px]">
                                play_arrow
                              </span>
                            </button>
                          )}

                          {/* Enable / Disable */}
                          <button
                            type="button"
                            onClick={() => handleServiceAction(srv.unit, 'enable')}
                            disabled={inProgress}
                            className="px-1.5 py-0.5 rounded bg-[#26293c] hover:bg-[#35384c] text-[#c3c6d2] hover:text-[#dfe1fa] text-[9px] uppercase font-bold cursor-pointer disabled:opacity-40 transition-colors"
                            title={`Enable ${srv.unit} on boot`}
                          >
                            Enable
                          </button>
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
