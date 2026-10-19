import React, { useState, useEffect } from 'react';
import type { NodeItem } from '../types/index.ts';

export interface NodeModuleItem {
  id: string;
  kind: string;
  name: string;
  label: string;
  icon: string;
  section: string;
  order: number;
  status: 'active' | 'available' | 'unsupported' | string;
  reason?: string;
  requirements: {
    hostPackages?: Record<string, string[]>;
    controllerCollections?: string[];
  };
  detection?: {
    detected: boolean;
    status: string;
    details?: string;
    error?: string;
  };
}

export interface HostModulesViewProps {
  node: NodeItem;
  initialModules?: NodeModuleItem[];
  onOpenQueueTask?: (taskId: string) => void;
  className?: string;
}

export const HostModulesView: React.FC<HostModulesViewProps> = ({
  node,
  initialModules,
  onOpenQueueTask,
  className = '',
}) => {
  const [modules, setModules] = useState<NodeModuleItem[]>(initialModules || []);
  const [loading, setLoading] = useState<boolean>(!initialModules);
  const [installingPlugin, setInstallingPlugin] = useState<string | null>(null);
  const [notice, setNotice] = useState<{ type: 'success' | 'error'; message: string } | null>(null);
  const [activeFilter, setActiveFilter] = useState<'all' | 'active' | 'available' | 'unsupported'>('all');

  const fetchModules = async () => {
    if (!node?.id) return;
    setLoading(true);
    setNotice(null);
    try {
      const res = await fetch(`/api/nodes/${encodeURIComponent(node.id)}/modules`);
      if (res.ok) {
        const data = await res.json();
        if (Array.isArray(data)) {
          setModules(data);
        }
      } else {
        const err = await res.text();
        setNotice({ type: 'error', message: `Failed to load modules: HTTP ${res.status} ${err}` });
      }
    } catch (err: any) {
      setNotice({ type: 'error', message: err?.message || 'Network error fetching modules' });
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (initialModules !== undefined) {
      setModules(initialModules);
      return;
    }
    fetchModules();
  }, [node?.id, initialModules]);

  // Handle staging install action for available plugin
  const handleInstall = async (mod: NodeModuleItem) => {
    setInstallingPlugin(mod.id);
    setNotice(null);

    try {
      // 1. Identify package manager plugin
      // Preferred order: node.pluginOverrides?.package, node.activePackage, or fallback dnf/apt
      let pkgPlugin = 'package/dnf';
      if (node.pluginOverrides && node.pluginOverrides['package']) {
        pkgPlugin = node.pluginOverrides['package'];
      } else if (node.osDescription?.toLowerCase().includes('debian') || node.osDescription?.toLowerCase().includes('ubuntu')) {
        pkgPlugin = 'package/apt';
      } else if (node.osDescription?.toLowerCase().includes('alpine')) {
        pkgPlugin = 'package/apk';
      }

      const [pkgKind, pkgName] = pkgPlugin.split('/');

      // Collect required packages for this module
      let targetPackages: string[] = [];
      if (mod.requirements?.hostPackages) {
        // Collect packages matching the module or first group
        const groups = Object.values(mod.requirements.hostPackages);
        if (groups.length > 0 && Array.isArray(groups[0])) {
          targetPackages = groups[0];
        }
      }
      const packageName = targetPackages.length > 0 ? targetPackages.join(' ') : mod.name;

      // 2. Call POST /api/nodes/:id/plugins/:pkgKind/:pkgName/actions/install
      const compileRes = await fetch(
        `/api/nodes/${encodeURIComponent(node.id)}/plugins/${encodeURIComponent(pkgKind)}/${encodeURIComponent(pkgName)}/actions/install`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            options: {
              name: packageName,
            },
          }),
        }
      );

      if (!compileRes.ok) {
        const errJson = await compileRes.json().catch(() => ({}));
        throw new Error(errJson.error || `Failed to compile install task via ${pkgPlugin} (HTTP ${compileRes.status})`);
      }

      const taskSpec = await compileRes.json();

      // 3. Stage task into Ansible Queue
      const queueRes = await fetch('/api/queue/task', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          title: taskSpec.name || `Install ${mod.label} packages on ${node.name}`,
          targetNodeId: node.id,
          targetNodeName: node.name,
          targetHostRoute: node.tailscaleHost || node.tailscaleIp || node.ip || 'localhost',
          targetSshUser: node.sshUser || 'core',
          targetSshPort: node.port || 22,
          category: 'package',
          primaryModule: taskSpec.steps?.[0]?.module || 'ansible.builtin.package',
          steps: taskSpec.steps || [],
          become: true,
          status: 'staged',
        }),
      });

      if (!queueRes.ok) {
        const qErr = await queueRes.json().catch(() => ({}));
        throw new Error(qErr.error || 'Failed to stage installation task into queue');
      }

      const qData = await queueRes.json();
      const taskId = qData.task?.id;

      // Trigger custom toast event for real-time notification
      if (typeof window !== 'undefined') {
        window.dispatchEvent(
          new CustomEvent('air_traffic_control_toast', {
            detail: {
              type: 'success',
              title: 'Installation Task Staged',
              message: `Queued installation for ${mod.label} (${packageName}) on ${node.name}`,
              actionText: taskId ? 'View Queue' : undefined,
              onAction: taskId && onOpenQueueTask ? () => onOpenQueueTask(taskId) : undefined,
            },
          })
        );
      }

      setNotice({
        type: 'success',
        message: `Installation for ${mod.label} staged into Task Queue (${taskId || 'Task Staged'}).`,
      });
    } catch (err: any) {
      if (typeof window !== 'undefined') {
        window.dispatchEvent(
          new CustomEvent('air_traffic_control_toast', {
            detail: {
              type: 'error',
              title: 'Installation Failed',
              message: err?.message || 'Could not stage installation task',
            },
          })
        );
      }
      setNotice({
        type: 'error',
        message: err?.message || 'Failed to stage package installation',
      });
    } finally {
      setInstallingPlugin(null);
    }
  };

  // Group modules by kind / section
  const filteredModules = modules.filter(m => {
    if (activeFilter === 'all') return true;
    return m.status === activeFilter;
  });

  const groupedByKind: Record<string, NodeModuleItem[]> = {};
  for (const m of filteredModules) {
    const groupKey = (m.section || m.kind || 'other').toUpperCase();
    if (!groupedByKind[groupKey]) {
      groupedByKind[groupKey] = [];
    }
    groupedByKind[groupKey].push(m);
  }

  const counts = {
    all: modules.length,
    active: modules.filter(m => m.status === 'active').length,
    available: modules.filter(m => m.status === 'available').length,
    unsupported: modules.filter(m => m.status === 'unsupported').length,
  };

  return (
    <section className={`bg-[#171a2d] rounded-lg p-4 flex flex-col border border-[#313447] shadow-lg font-mono ${className}`.trim()}>
      {/* Header bar */}
      <div className="flex flex-wrap items-center justify-between gap-3 pb-3 border-b border-[#26293c]">
        <div className="flex items-center gap-2">
          <span className="material-symbols-outlined text-[#ffd97d] text-[24px]">extension</span>
          <div>
            <h2 className="text-sm font-bold text-[#dfe1fa] flex items-center gap-2">
              <span>Host Modules &amp; Plugin Capability Matrix</span>
              <span className="text-[10px] font-normal px-2 py-0.5 rounded bg-[#21263d] text-[#b0c9ff]">
                {node.name} ({node.osDescription || 'Linux'})
              </span>
            </h2>
            <p className="text-[11px] text-[#8d909b]">
              Inspect active, available, and unsupported capabilities discovered by host daemon probes
            </p>
          </div>
        </div>

        <div className="flex items-center gap-2">
          {/* Status filter pills */}
          <div className="flex items-center bg-[#0a0d1f] p-0.5 rounded border border-[#26293c] text-[11px]">
            <button
              type="button"
              onClick={() => setActiveFilter('all')}
              className={`px-2 py-0.5 rounded transition-colors cursor-pointer ${
                activeFilter === 'all'
                  ? 'bg-[#26293c] text-[#dfe1fa] font-bold'
                  : 'text-[#8d909b] hover:text-[#c3c6d2]'
              }`}
            >
              All ({counts.all})
            </button>
            <button
              type="button"
              onClick={() => setActiveFilter('active')}
              className={`px-2 py-0.5 rounded transition-colors cursor-pointer ${
                activeFilter === 'active'
                  ? 'bg-[#1b332b] text-[#8cd6cb] font-bold'
                  : 'text-[#8d909b] hover:text-[#c3c6d2]'
              }`}
            >
              Active ({counts.active})
            </button>
            <button
              type="button"
              onClick={() => setActiveFilter('available')}
              className={`px-2 py-0.5 rounded transition-colors cursor-pointer ${
                activeFilter === 'available'
                  ? 'bg-[#332a12] text-[#ffd97d] font-bold'
                  : 'text-[#8d909b] hover:text-[#c3c6d2]'
              }`}
            >
              Available ({counts.available})
            </button>
            <button
              type="button"
              onClick={() => setActiveFilter('unsupported')}
              className={`px-2 py-0.5 rounded transition-colors cursor-pointer ${
                activeFilter === 'unsupported'
                  ? 'bg-[#26293c] text-[#8d909b] font-bold'
                  : 'text-[#8d909b] hover:text-[#c3c6d2]'
              }`}
            >
              Unsupported ({counts.unsupported})
            </button>
          </div>

          <button
            type="button"
            onClick={fetchModules}
            disabled={loading}
            className="p-1.5 rounded bg-[#26293c] hover:bg-[#35384c] text-[#8d909b] hover:text-[#dfe1fa] cursor-pointer transition-colors"
            title="Refresh Modules"
          >
            <span className={`material-symbols-outlined text-[16px] ${loading ? 'animate-spin' : ''}`}>
              refresh
            </span>
          </button>
        </div>
      </div>

      {notice && (
        <div
          className={`mt-3 p-2.5 rounded text-xs border flex items-center gap-2 ${
            notice.type === 'error'
              ? 'bg-[#331114] text-[#ffb4ab] border-[#ffb4ab]/30'
              : 'bg-[#0f231e] text-[#8cd6cb] border-[#8cd6cb]/30'
          }`}
        >
          <span className="material-symbols-outlined text-[16px]">
            {notice.type === 'error' ? 'error' : 'check_circle'}
          </span>
          <span>{notice.message}</span>
        </div>
      )}

      {loading && modules.length === 0 ? (
        <div className="py-12 flex flex-col items-center justify-center gap-2 text-[#8d909b]">
          <span className="material-symbols-outlined text-2xl text-[#b0c9ff] animate-spin">
            progress_activity
          </span>
          <span className="text-xs">Probing host plugins and module dependencies...</span>
        </div>
      ) : Object.keys(groupedByKind).length === 0 ? (
        <div className="py-8 text-center text-xs text-[#8d909b]">
          No modules match the selected filter.
        </div>
      ) : (
        <div className="mt-4 flex flex-col gap-5">
          {Object.entries(groupedByKind).map(([kindGroup, list]) => (
            <div key={kindGroup} className="flex flex-col gap-2">
              <span className="text-[10px] font-bold text-[#8d909b] uppercase tracking-wider px-1">
                {kindGroup}
              </span>
              <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-2.5">
                {list.map(mod => {
                  const isActive = mod.status === 'active';
                  const isAvailable = mod.status === 'available';
                  const isUnsupported = mod.status === 'unsupported';

                  const allPackages: string[] = [];
                  if (mod.requirements?.hostPackages) {
                    for (const pkgs of Object.values(mod.requirements.hostPackages)) {
                      allPackages.push(...pkgs);
                    }
                  }
                  const collections = mod.requirements?.controllerCollections || [];

                  return (
                    <div
                      key={mod.id}
                      className={`p-3 rounded-md border flex flex-col justify-between transition-colors ${
                        isUnsupported
                          ? 'bg-[#101220]/60 border-[#26293c]/40 opacity-60 text-[#6d7182]'
                          : isActive
                          ? 'bg-[#141829] border-[#2f3554] hover:border-[#414872]'
                          : 'bg-[#141829] border-[#383120] hover:border-[#52462b]'
                      }`}
                    >
                      <div>
                        {/* Header: Icon, Label, Status pill */}
                        <div className="flex items-start justify-between gap-2">
                          <div className="flex items-center gap-2 min-w-0">
                            <span
                              className={`material-symbols-outlined text-[18px] shrink-0 ${
                                isActive
                                  ? 'text-[#8cd6cb]'
                                  : isAvailable
                                  ? 'text-[#ffd97d]'
                                  : 'text-[#6d7182]'
                              }`}
                            >
                              {mod.icon || 'extension'}
                            </span>
                            <div className="min-w-0">
                              <h4 className="text-xs font-bold text-[#dfe1fa] truncate">
                                {mod.label || mod.name}
                              </h4>
                              <span className="text-[10px] text-[#8d909b] font-mono block truncate">
                                {mod.id}
                              </span>
                            </div>
                          </div>

                          {/* Status Pill */}
                          <span
                            className={`px-1.5 py-0.5 rounded text-[9px] font-bold uppercase shrink-0 border ${
                              isActive
                                ? 'bg-[#1b332b] text-[#8cd6cb] border-[#295446]'
                                : isAvailable
                                ? 'bg-[#332a12] text-[#ffd97d] border-[#59471d]'
                                : 'bg-[#26293c]/50 text-[#8d909b] border-[#434750]/30'
                            }`}
                          >
                            {mod.status.toUpperCase()}
                          </span>
                        </div>

                        {/* Details / Reason */}
                        {mod.detection?.details && (
                          <div className="mt-2 text-[11px] text-[#c3c6d2] font-mono">
                            {mod.detection.details}
                          </div>
                        )}
                        {mod.reason && (
                          <div className="mt-1 text-[10px] text-[#8d909b] italic">
                            {mod.reason}
                          </div>
                        )}

                        {/* Requirements: Packages & Ansible Collections */}
                        <div className="mt-3 pt-2 border-t border-[#26293c]/60 flex flex-col gap-1.5 text-[10px]">
                          {allPackages.length > 0 && (
                            <div>
                              <span className="text-[#8d909b] block mb-0.5">Host Packages:</span>
                              <div className="flex flex-wrap gap-1">
                                {allPackages.map(pkg => (
                                  <span
                                    key={pkg}
                                    className="px-1 py-0.2 rounded bg-[#0a0d1f] text-[#b0c9ff] border border-[#26293c]"
                                  >
                                    {pkg}
                                  </span>
                                ))}
                              </div>
                            </div>
                          )}

                          {collections.length > 0 && (
                            <div>
                              <span className="text-[#8d909b] block mb-0.5">Ansible Collections:</span>
                              <div className="flex flex-wrap gap-1">
                                {collections.map(col => (
                                  <span
                                    key={col}
                                    className="px-1 py-0.2 rounded bg-[#0a0d1f] text-[#d9b9ff] border border-[#26293c]"
                                  >
                                    {col}
                                  </span>
                                ))}
                              </div>
                            </div>
                          )}
                        </div>
                      </div>

                      {/* Card Footer: Install action for available plugins */}
                      {isAvailable && (
                        <div className="mt-3 pt-2 border-t border-[#26293c]/60 flex items-center justify-end">
                          <button
                            type="button"
                            onClick={() => handleInstall(mod)}
                            disabled={installingPlugin === mod.id}
                            className="flex items-center gap-1 px-2.5 py-1 rounded bg-[#ffd97d] hover:bg-[#ffe39c] text-[#241a00] font-bold text-[11px] transition-colors cursor-pointer disabled:opacity-50"
                          >
                            {installingPlugin === mod.id ? (
                              <>
                                <span className="material-symbols-outlined text-[13px] animate-spin">
                                  sync
                                </span>
                                <span>Staging...</span>
                              </>
                            ) : (
                              <>
                                <span className="material-symbols-outlined text-[13px]">
                                  download
                                </span>
                                <span>Install</span>
                              </>
                            )}
                          </button>
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>
            </div>
          ))}
        </div>
      )}
    </section>
  );
};
