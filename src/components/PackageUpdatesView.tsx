import React, { useState, useEffect } from 'react';
import type { NodeItem } from '../types/index.ts';
import {
  type PackageManager,
  type PackageUpdate,
  type PackageCheckResult,
  isKernelOrSystemUpdate,
} from '../utils/packageManager.ts';
import { safeFetchJson } from '../utils/apiDefensive.ts';
import { compilePackageTask } from '../utils/ansibleQueueCompiler.ts';
import { stageTaskInQueue } from '../utils/drivers/index.ts';

interface PackageUpdatesViewProps {
  node: NodeItem;
  onOpenTask?: (taskId: string) => void;
}

export const PackageUpdatesView: React.FC<PackageUpdatesViewProps> = ({ node, onOpenTask }) => {
  const [loading, setLoading] = useState<boolean>(false);
  const [applying, setApplying] = useState<boolean>(false);
  const [error, setError] = useState<string | null>(null);
  const [checkResult, setCheckResult] = useState<PackageCheckResult | null>(null);
  const [searchQuery, setSearchQuery] = useState<string>('');
  const [securityFilter, setSecurityFilter] = useState<boolean>(false);
  const [showConfirmModal, setShowConfirmModal] = useState<boolean>(false);
  const [appliedTaskId, setAppliedTaskId] = useState<string | null>(null);
  const [statusMessage, setStatusMessage] = useState<string | null>(null);

  const fetchUpdates = async (refresh = false) => {
    setLoading(true);
    setError(null);
    setStatusMessage(null);
    try {
      const result = await safeFetchJson<PackageCheckResult>(
        `/api/nodes/${encodeURIComponent(node.id)}/packages/check${refresh ? '?refresh=true' : ''}`
      );
      if (result.success && result.data) {
        setCheckResult(result.data);
      } else {
        setError(result.error || 'Failed to check for updates on host');
      }
    } catch (err: any) {
      setError(err?.message || 'Network error querying package updates');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchUpdates(false);
  }, [node.id]);

  const handleApplyAll = async () => {
    setShowConfirmModal(false);
    setApplying(true);
    setError(null);
    try {
      const result = await safeFetchJson<{ success: boolean; taskId: string; message?: string }>(
        `/api/nodes/${encodeURIComponent(node.id)}/packages/apply`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            manager: checkResult?.manager,
          }),
        }
      );
      if (result.success && result.data) {
        setAppliedTaskId(result.data.taskId);
        setStatusMessage(result.data.message || `Updates dispatched to ${node.name}`);
        // Refresh updates listing after dispatching
        setTimeout(() => {
          fetchUpdates(true);
        }, 2500);
      } else {
        setError(result.error || 'Failed to apply package updates');
      }
    } catch (err: any) {
      setError(err?.message || 'Network error applying updates');
    } finally {
      setApplying(false);
    }
  };

  const [queueing, setQueueing] = useState<boolean>(false);

  const handleQueueUpdates = async () => {
    setQueueing(true);
    setError(null);
    try {
      const pkgName = updates.length === 1 ? updates[0].name : '*';
      const task = compilePackageTask(node, pkgName, 'latest');
      task.title = `Apply ${updates.length} package update(s) on ${node.shortName || node.name}`;
      const res = await stageTaskInQueue(task);
      if (res.success) {
        setStatusMessage(`Package updates task staged in Ansible Mutation Queue`);
      } else {
        setError(res.error || 'Failed to queue package updates');
      }
    } catch (err: any) {
      setError(err?.message || 'Error queueing package updates');
    } finally {
      setQueueing(false);
    }
  };

  const getManagerBadge = (mgr?: PackageManager) => {
    switch (mgr) {
      case 'rpm-ostree':
        return { label: 'rpm-ostree', color: 'bg-[#b0c9ff]/15 text-[#b0c9ff] border-[#b0c9ff]/30' };
      case 'bootc':
        return { label: 'bootc (container)', color: 'bg-[#ffd97d]/15 text-[#ffd97d] border-[#ffd97d]/30' };
      case 'dnf':
        return { label: 'dnf / RPM', color: 'bg-[#8cd6cb]/15 text-[#8cd6cb] border-[#8cd6cb]/30' };
      case 'apt':
        return { label: 'apt / DEB', color: 'bg-[#f48fb1]/15 text-[#f48fb1] border-[#f48fb1]/30' };
      case 'apk':
        return { label: 'apk / Alpine', color: 'bg-[#80cbc4]/15 text-[#80cbc4] border-[#80cbc4]/30' };
      default:
        return { label: 'Probing...', color: 'bg-[#8d909b]/15 text-[#8d909b] border-[#8d909b]/30' };
    }
  };

  const updates = checkResult?.updates || [];
  const securityCount = updates.filter(u => u.security).length;
  const criticalCount = updates.filter(u => isKernelOrSystemUpdate(u.name)).length;

  const filteredUpdates = updates.filter(pkg => {
    if (securityFilter && !pkg.security) return false;
    if (!searchQuery.trim()) return true;
    const q = searchQuery.toLowerCase();
    return (
      pkg.name.toLowerCase().includes(q) ||
      pkg.newVersion.toLowerCase().includes(q) ||
      (pkg.currentVersion && pkg.currentVersion.toLowerCase().includes(q)) ||
      (pkg.repository && pkg.repository.toLowerCase().includes(q)) ||
      (pkg.arch && pkg.arch.toLowerCase().includes(q))
    );
  });

  const managerBadge = getManagerBadge(checkResult?.manager);

  return (
    <div className="flex flex-col gap-3">
      {/* Header Bar */}
      <div className="bg-[#171a2d] border border-[#26293c] rounded p-3 flex flex-wrap items-center justify-between gap-2 shadow-sm">
        <div className="flex items-center gap-2.5">
          <span className="material-symbols-outlined text-[#8cd6cb] text-[22px]">system_update</span>
          <div>
            <div className="flex items-center gap-2">
              <h2 className="text-sm font-semibold text-[#dfe1fa]">Package &amp; Update Management</h2>
              <span className={`px-2 py-0.5 rounded font-mono text-[10px] font-bold border ${managerBadge.color}`}>
                {managerBadge.label}
              </span>
              {checkResult?.isImmutable && (
                <span className="px-2 py-0.5 rounded font-mono text-[10px] font-bold bg-[#b0c9ff]/20 text-[#b0c9ff] border border-[#b0c9ff]/40 flex items-center gap-1">
                  <span className="material-symbols-outlined text-[11px]">layers</span>
                  ATOMIC / IMMUTABLE
                </span>
              )}
            </div>
            <p className="font-mono text-[11px] text-[#8d909b]">
              Target Host: <span className="text-[#b0c9ff]">{node.shortName}</span>
              {node.tailscaleIp && <span> ({node.tailscaleIp})</span>}
              {node.osDescription && <span className="text-[#8d909b]"> &bull; {node.osDescription}</span>}
            </p>
          </div>
        </div>

        <div className="flex items-center gap-2 font-mono text-xs">
          {checkResult?.rebootRequired && (
            <span className="px-2 py-0.5 rounded bg-[#ffd97d]/15 text-[#ffd97d] font-bold border border-[#ffd97d]/40 flex items-center gap-1">
              <span className="material-symbols-outlined text-[13px]">restart_alt</span>
              REBOOT REQUIRED
            </span>
          )}

          <button
            type="button"
            onClick={() => fetchUpdates(true)}
            disabled={loading || applying}
            className="flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] px-2.5 py-1 rounded transition-colors border border-[#434750]/40 cursor-pointer disabled:opacity-50"
            title="Force refresh updates from remote repositories"
          >
            <span className={`material-symbols-outlined text-[14px] ${loading ? 'animate-spin' : ''}`}>sync</span>
            <span>Check for Updates</span>
          </button>

          {updates.length > 0 && (
            <>
              <button
                type="button"
                onClick={handleQueueUpdates}
                disabled={loading || applying || queueing}
                className="flex items-center gap-1.5 bg-[#171a2d] hover:bg-[#26293c] text-[#ffd97d] border border-[#ffd97d]/40 font-bold px-3 py-1 rounded transition-colors shadow-sm cursor-pointer disabled:opacity-50"
                title="Stage package update task into Ansible Mutation Queue"
              >
                <span className="material-symbols-outlined text-[15px]">queue</span>
                <span>{queueing ? 'Queueing...' : `Queue Updates (${updates.length})`}</span>
              </button>
              <button
                type="button"
                onClick={() => setShowConfirmModal(true)}
                disabled={loading || applying || queueing}
                className="flex items-center gap-1.5 bg-[#8cd6cb] hover:bg-[#a0e4d9] text-[#121528] font-bold px-3 py-1 rounded transition-colors shadow-sm cursor-pointer disabled:opacity-50"
              >
                <span className="material-symbols-outlined text-[15px]">upgrade</span>
                <span>Apply All Updates ({updates.length})</span>
              </button>
            </>
          )}
        </div>
      </div>

      {/* Reboot Alert Banner */}
      {checkResult?.rebootRequired && (
        <div className="bg-[#ffd97d]/10 border border-[#ffd97d]/30 rounded p-2.5 flex items-center justify-between text-xs font-mono text-[#ffd97d]">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[18px]">warning</span>
            <span>
              {checkResult.isImmutable
                ? 'An atomic OS deployment is available or staged. A system reboot will activate the new OS image.'
                : 'Pending updates include critical kernel or core system daemons. A reboot is recommended after applying.'}
            </span>
          </div>
          <span className="text-[10px] text-[#ffd97d]/80 uppercase font-bold tracking-wider">Host Action Pending</span>
        </div>
      )}

      {/* Status or Error Notifications */}
      {error && (
        <div className="bg-[#f87171]/10 border border-[#f87171]/30 rounded p-2.5 text-xs font-mono text-[#f87171] flex items-center gap-2">
          <span className="material-symbols-outlined text-[16px]">error</span>
          <span>{error}</span>
        </div>
      )}

      {statusMessage && (
        <div className="bg-[#8cd6cb]/10 border border-[#8cd6cb]/30 rounded p-2.5 text-xs font-mono text-[#8cd6cb] flex items-center justify-between">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[16px]">check_circle</span>
            <span>{statusMessage}</span>
          </div>
          {appliedTaskId && onOpenTask && (
            <button
              type="button"
              onClick={() => onOpenTask(appliedTaskId)}
              className="underline hover:text-[#dfe1fa] cursor-pointer text-[11px]"
            >
              View Task &rarr;
            </button>
          )}
        </div>
      )}

      {/* Summary Cards */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-2">
        <div className="bg-[#171a2d] border border-[#26293c] rounded p-2.5 flex flex-col gap-0.5">
          <span className="font-mono text-[10px] uppercase text-[#8d909b] tracking-wider">AVAILABLE UPDATES</span>
          <div className="flex items-baseline gap-1.5">
            <span className={`font-mono text-xl font-bold ${updates.length > 0 ? 'text-[#b0c9ff]' : 'text-[#8cd6cb]'}`}>
              {loading ? '...' : updates.length}
            </span>
            <span className="text-[11px] text-[#8d909b] font-mono">packages</span>
          </div>
        </div>

        <div className="bg-[#171a2d] border border-[#26293c] rounded p-2.5 flex flex-col gap-0.5">
          <span className="font-mono text-[10px] uppercase text-[#8d909b] tracking-wider">SECURITY ADVISORIES</span>
          <div className="flex items-baseline gap-1.5">
            <span className={`font-mono text-xl font-bold ${securityCount > 0 ? 'text-[#f87171]' : 'text-[#8cd6cb]'}`}>
              {loading ? '...' : securityCount}
            </span>
            <span className="text-[11px] text-[#8d909b] font-mono">patches</span>
          </div>
        </div>

        <div className="bg-[#171a2d] border border-[#26293c] rounded p-2.5 flex flex-col gap-0.5">
          <span className="font-mono text-[10px] uppercase text-[#8d909b] tracking-wider">KERNEL &amp; CORE SYSTEM</span>
          <div className="flex items-baseline gap-1.5">
            <span className={`font-mono text-xl font-bold ${criticalCount > 0 ? 'text-[#ffd97d]' : 'text-[#dfe1fa]'}`}>
              {loading ? '...' : criticalCount}
            </span>
            <span className="text-[11px] text-[#8d909b] font-mono">critical</span>
          </div>
        </div>

        <div className="bg-[#171a2d] border border-[#26293c] rounded p-2.5 flex flex-col gap-0.5">
          <span className="font-mono text-[10px] uppercase text-[#8d909b] tracking-wider">LAST CHECKED</span>
          <div className="flex items-baseline gap-1.5">
            <span className="font-mono text-xs font-semibold text-[#dfe1fa] truncate">
              {checkResult?.lastChecked ? new Date(checkResult.lastChecked).toLocaleTimeString() : 'Never'}
            </span>
            <span className="text-[10px] text-[#8d909b] font-mono">local</span>
          </div>
        </div>
      </div>

      {/* Filter and Search Bar */}
      <div className="bg-[#171a2d] border border-[#26293c] rounded p-2.5 flex flex-wrap items-center justify-between gap-2 shadow-sm font-mono text-xs">
        <div className="flex items-center gap-2 flex-1 min-w-[240px]">
          <span className="material-symbols-outlined text-[#8d909b] text-[16px]">search</span>
          <input
            type="text"
            value={searchQuery}
            onChange={e => setSearchQuery(e.target.value)}
            placeholder="Filter by package name, version, arch, repository..."
            className="w-full bg-[#121528] border border-[#26293c] rounded px-2.5 py-1 text-xs text-[#dfe1fa] placeholder-[#8d909b]/60 focus:outline-none focus:border-[#b0c9ff]"
          />
          {searchQuery && (
            <button
              type="button"
              onClick={() => setSearchQuery('')}
              className="text-[#8d909b] hover:text-[#dfe1fa] px-1 cursor-pointer"
            >
              &times;
            </button>
          )}
        </div>

        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => setSecurityFilter(!securityFilter)}
            className={`flex items-center gap-1 px-2 py-1 rounded transition-colors cursor-pointer border ${
              securityFilter
                ? 'bg-[#f87171]/20 text-[#f87171] border-[#f87171]/40 font-bold'
                : 'bg-[#26293c] text-[#8d909b] border-[#434750]/30 hover:text-[#dfe1fa]'
            }`}
          >
            <span className="material-symbols-outlined text-[13px]">shield</span>
            <span>Security Only</span>
            {securityCount > 0 && <span className="ml-0.5 text-[10px]">({securityCount})</span>}
          </button>

          <span className="text-[11px] text-[#8d909b]">
            Showing <strong className="text-[#dfe1fa]">{filteredUpdates.length}</strong> of{' '}
            <strong className="text-[#dfe1fa]">{updates.length}</strong> updates
          </span>
        </div>
      </div>

      {/* Updates Table */}
      <div className="bg-[#171a2d] border border-[#26293c] rounded overflow-hidden shadow-sm">
        <div className="overflow-x-auto">
          <table className="w-full text-left font-mono text-xs border-collapse">
            <thead>
              <tr className="bg-[#121528] text-[#8d909b] text-[11px] border-b border-[#26293c]">
                <th className="p-2.5">PACKAGE NAME</th>
                <th className="p-2.5">VERSION TRANSITION</th>
                <th className="p-2.5">ARCH</th>
                <th className="p-2.5">REPOSITORY / CHANNEL</th>
                <th className="p-2.5 text-right">TYPE</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[#26293c]">
              {loading && updates.length === 0 ? (
                <tr>
                  <td colSpan={5} className="p-6 text-center text-[#8d909b]">
                    <div className="flex items-center justify-center gap-2">
                      <span className="material-symbols-outlined animate-spin text-[18px]">sync</span>
                      <span>Probing host and checking repository updates...</span>
                    </div>
                  </td>
                </tr>
              ) : filteredUpdates.length === 0 ? (
                <tr>
                  <td colSpan={5} className="p-6 text-center text-[#8d909b]">
                    {updates.length === 0 ? (
                      <div className="flex flex-col items-center gap-1.5 py-2">
                        <span className="material-symbols-outlined text-[#8cd6cb] text-[28px]">verified</span>
                        <span className="text-sm font-semibold text-[#dfe1fa]">System is fully up to date</span>
                        <span className="text-[11px]">No package upgrades available for {node.name}.</span>
                      </div>
                    ) : (
                      <span>No package updates match your search criteria.</span>
                    )}
                  </td>
                </tr>
              ) : (
                filteredUpdates.map((pkg, idx) => {
                  const isKernel = isKernelOrSystemUpdate(pkg.name);
                  return (
                    <tr key={`${pkg.name}-${idx}`} className="hover:bg-[#1f2238] transition-colors">
                      <td className="p-2.5 font-bold text-[#dfe1fa] flex items-center gap-1.5">
                        {isKernel ? (
                          <span
                            className="material-symbols-outlined text-[#ffd97d] text-[15px]"
                            title="Core system or kernel component"
                          >
                            memory
                          </span>
                        ) : (
                          <span className="material-symbols-outlined text-[#8d909b] text-[15px]">inventory_2</span>
                        )}
                        <span>{pkg.name}</span>
                        {pkg.security && (
                          <span className="ml-1 px-1.5 py-0.2 rounded text-[9px] bg-[#f87171]/20 text-[#f87171] border border-[#f87171]/40 font-bold">
                            SEC
                          </span>
                        )}
                      </td>

                      <td className="p-2.5">
                        <div className="flex items-center gap-1.5 text-[11px]">
                          {pkg.currentVersion ? (
                            <span className="text-[#8d909b] line-through">{pkg.currentVersion}</span>
                          ) : (
                            <span className="text-[#8d909b]">installed</span>
                          )}
                          <span className="material-symbols-outlined text-[12px] text-[#8cd6cb]">arrow_forward</span>
                          <span className="text-[#8cd6cb] font-semibold">{pkg.newVersion}</span>
                        </div>
                      </td>

                      <td className="p-2.5 text-[#8d909b] text-[11px]">
                        {pkg.arch ? (
                          <span className="px-1.5 py-0.5 rounded bg-[#121528] border border-[#26293c]">
                            {pkg.arch}
                          </span>
                        ) : (
                          <span>&mdash;</span>
                        )}
                      </td>

                      <td className="p-2.5 text-[11px]">
                        {pkg.repository ? (
                          <span
                            className={`px-1.5 py-0.5 rounded border ${
                              pkg.security
                                ? 'bg-[#f87171]/10 text-[#f87171] border-[#f87171]/30 font-semibold'
                                : 'bg-[#121528] text-[#b0c9ff] border-[#26293c]'
                            }`}
                          >
                            {pkg.repository}
                          </span>
                        ) : (
                          <span className="text-[#8d909b]">&mdash;</span>
                        )}
                      </td>

                      <td className="p-2.5 text-right">
                        {pkg.security ? (
                          <span className="px-2 py-0.5 rounded bg-[#f87171]/15 text-[#f87171] font-bold border border-[#f87171]/30 text-[10px]">
                            Security Fix
                          </span>
                        ) : isKernel ? (
                          <span className="px-2 py-0.5 rounded bg-[#ffd97d]/15 text-[#ffd97d] font-bold border border-[#ffd97d]/30 text-[10px]">
                            Kernel / Core
                          </span>
                        ) : (
                          <span className="px-2 py-0.5 rounded bg-[#8d909b]/15 text-[#c3c6d2] border border-[#8d909b]/30 text-[10px]">
                            Standard
                          </span>
                        )}
                      </td>
                    </tr>
                  );
                })
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* Confirmation Modal */}
      {showConfirmModal && (
        <div className="fixed inset-0 z-50 bg-black/60 flex items-center justify-center p-4 backdrop-blur-xs">
          <div className="bg-[#171a2d] border border-[#26293c] rounded-lg max-w-md w-full p-4 shadow-xl flex flex-col gap-3 font-mono">
            <div className="flex items-center gap-2 text-[#dfe1fa]">
              <span className="material-symbols-outlined text-[#8cd6cb] text-[22px]">system_update</span>
              <h3 className="text-sm font-bold">Confirm System Updates</h3>
            </div>

            <p className="text-xs text-[#c3c6d2] leading-relaxed">
              Are you sure you want to apply <strong className="text-[#8cd6cb]">{updates.length}</strong> available
              updates on <strong className="text-[#b0c9ff]">{node.name}</strong> using{' '}
              <strong className="text-[#ffd97d]">{checkResult?.manager}</strong>?
            </p>

            {criticalCount > 0 && (
              <div className="bg-[#ffd97d]/10 border border-[#ffd97d]/30 rounded p-2 text-xs text-[#ffd97d] flex items-center gap-2">
                <span className="material-symbols-outlined text-[16px]">warning</span>
                <span>Includes {criticalCount} kernel or system updates. A reboot will be recommended afterwards.</span>
              </div>
            )}

            <div className="bg-[#121528] p-2.5 rounded border border-[#26293c] text-[11px] text-[#8d909b]">
              <span>Elevated execution command:</span>
              <div className="text-[#dfe1fa] font-bold mt-1">
                {checkResult?.manager === 'dnf' && 'dnf upgrade -y'}
                {checkResult?.manager === 'apt' && 'DEBIAN_FRONTEND=noninteractive apt-get upgrade -y'}
                {checkResult?.manager === 'apk' && 'apk upgrade'}
                {checkResult?.manager === 'rpm-ostree' && 'rpm-ostree upgrade'}
                {checkResult?.manager === 'bootc' && 'bootc upgrade'}
              </div>
            </div>

            <div className="flex justify-end gap-2 mt-2">
              <button
                type="button"
                onClick={() => setShowConfirmModal(false)}
                className="px-3 py-1.5 rounded bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] text-xs transition-colors cursor-pointer"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={() => {
                  setShowConfirmModal(false);
                  handleQueueUpdates();
                }}
                disabled={queueing}
                className="px-3 py-1.5 rounded bg-[#171a2d] hover:bg-[#26293c] text-[#ffd97d] border border-[#ffd97d]/40 font-bold text-xs transition-colors cursor-pointer flex items-center gap-1.5"
              >
                <span className="material-symbols-outlined text-[14px]">queue</span>
                <span>Stage in Queue</span>
              </button>
              <button
                type="button"
                onClick={handleApplyAll}
                disabled={applying}
                className="px-3 py-1.5 rounded bg-[#8cd6cb] hover:bg-[#a0e4d9] text-[#121528] font-bold text-xs transition-colors cursor-pointer flex items-center gap-1.5"
              >
                {applying && <span className="material-symbols-outlined text-[14px] animate-spin">sync</span>}
                <span>Confirm &amp; Apply Updates</span>
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
