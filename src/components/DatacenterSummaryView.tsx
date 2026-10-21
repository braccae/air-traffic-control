import React, { useEffect, useMemo, useState } from 'react';
import type { GlobalTaskItem, NodeItem, QueuedAnsibleTask } from '../types/index.ts';
import type { DatacenterSection } from '../utils/navigationViewModel.ts';
import { calculateClusterHealthScore } from '../utils/navigationViewModel.ts';
import { formatBytes } from '../utils/orchestrator.ts';
import { safeFetchJson } from '../utils/apiDefensive.ts';
import {
  countQueueStatuses,
  formatRelativeTime,
  summarizeRecentActivity,
  summarizeStorageProbes,
  type NodeStorageProbe,
} from '../utils/datacenterSummary.ts';

export interface DatacenterSummaryViewProps {
  datacenterName: string;
  nodes: NodeItem[];
  tasks: GlobalTaskItem[];
  queue?: QueuedAnsibleTask[];
  isQueueRunning?: boolean;
  onSelectNode: (node: NodeItem) => void;
  onOpenPlaybookModal: (playbookName?: string) => void;
  onOpenPlaybookEditor: (playbookName?: string) => void;
  onOpenGroupVars: () => void;
  onOpenConsole?: () => void;
  onOpenSection?: (section: DatacenterSection) => void;
  /** Upper bound on hosts probed for the storage summary (one SSH round-trip each). */
  maxStorageHosts?: number;
}

const STATUS_CLASSES: Record<string, string> = {
  OK: 'bg-[#162620] text-[#8cd6cb] border-[#8cd6cb]/30',
  COMPLETED: 'bg-[#162620] text-[#8cd6cb] border-[#8cd6cb]/30',
  RUNNING: 'bg-[#21263d] text-[#b0c9ff] border-[#b0c9ff]/30 animate-pulse',
  STAGED: 'bg-[#2d281b] text-[#ffd580] border-[#ffd580]/30',
  FAILED: 'bg-[#311c1c] text-[#ffb4ab] border-[#ffb4ab]/30',
  CANCELLED: 'bg-[#21262d] text-[#8d909b] border-[#30363d]',
};

const statusClass = (status: string): string =>
  STATUS_CLASSES[status.toUpperCase()] || STATUS_CLASSES.CANCELLED;

const QuickActionButton: React.FC<{
  icon: string;
  iconClass: string;
  badgeClass: string;
  badge?: string;
  title: string;
  description: string;
  onClick: () => void;
  testId?: string;
}> = ({ icon, iconClass, badgeClass, badge, title, description, onClick, testId }) => (
  <button
    type="button"
    data-testid={testId}
    onClick={onClick}
    className="group flex flex-col p-3 rounded-lg bg-[#171a2d] hover:bg-[#1f2338] border border-[#26293c] hover:border-[#b0c9ff]/50 transition-all text-left cursor-pointer"
  >
    <div className="flex items-center justify-between w-full mb-1">
      <span className={`material-symbols-outlined text-[20px] ${iconClass} group-hover:scale-110 transition-transform`}>
        {icon}
      </span>
      {badge && (
        <span className={`text-[10px] font-mono px-1.5 py-0.5 rounded border ${badgeClass}`}>{badge}</span>
      )}
    </div>
    <span className="font-mono text-xs font-bold text-[#f0f2ff] group-hover:text-[#b0c9ff]">{title}</span>
    <span className="text-[11px] text-[#8d909b] mt-1 leading-tight">{description}</span>
  </button>
);

export const DatacenterSummaryView: React.FC<DatacenterSummaryViewProps> = ({
  datacenterName,
  nodes,
  tasks,
  queue = [],
  isQueueRunning = false,
  onSelectNode,
  onOpenPlaybookModal,
  onOpenPlaybookEditor,
  onOpenGroupVars,
  onOpenConsole,
  onOpenSection,
  maxStorageHosts = 6,
}) => {
  const [probes, setProbes] = useState<NodeStorageProbe[]>([]);
  const [storageLoading, setStorageLoading] = useState(true);

  const onlineNodes = useMemo(() => nodes.filter(n => n.status === 'online'), [nodes]);
  const storageNodes = useMemo(() => onlineNodes.slice(0, maxStorageHosts), [onlineNodes, maxStorageHosts]);

  useEffect(() => {
    let cancelled = false;

    if (storageNodes.length === 0) {
      setProbes([]);
      setStorageLoading(false);
      return () => {
        cancelled = true;
      };
    }

    setStorageLoading(true);

    Promise.all(
      storageNodes.map(async node => {
        const res = await safeFetchJson<any>(`/api/nodes/${encodeURIComponent(node.id)}/storage`);
        const probe: NodeStorageProbe = {
          nodeId: node.id,
          nodeName: node.shortName || node.name,
          payload: res.success ? res.data : undefined,
          fetchError: res.success ? undefined : res.error,
        };
        return probe;
      })
    ).then(results => {
      if (cancelled) return;
      setProbes(results);
      setStorageLoading(false);
    });

    return () => {
      cancelled = true;
    };
  }, [storageNodes]);

  const storage = useMemo(() => summarizeStorageProbes(probes), [probes]);
  const health = calculateClusterHealthScore(nodes);
  const queueCounts = countQueueStatuses(queue);
  const activity = useMemo(() => summarizeRecentActivity(tasks, queue, 6), [tasks, queue]);

  const hostCount = nodes.filter(n => n.group === 'tag:host' || n.nodeType === 'linux').length;
  const vmCount = nodes.filter(n => n.group === 'tag:vm' || n.badge === 'VM').length;
  const lxcCount = nodes.filter(n => n.nodeType === 'lxc' || n.badge === 'LXC').length;
  const containerCount = nodes.filter(n => n.hasPodman || n.badge === 'PODMAN').length;

  const openSection = (section: DatacenterSection) => onOpenSection?.(section);

  return (
    <div id="datacenter-overview" className="flex flex-col gap-4 w-full" data-testid="datacenter-summary-view">
      {/* Header + real cluster stats */}
      <div className="rounded-lg bg-[#121528] border border-[#26293c] p-4 shadow-md flex flex-col gap-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex items-center gap-3">
            <div className="w-10 h-10 rounded-lg bg-[#1b2a38] border border-[#8cd6cb]/40 flex items-center justify-center text-[#8cd6cb]">
              <span className="material-symbols-outlined text-[24px]">dashboard</span>
            </div>
            <div>
              <div className="flex items-center gap-2">
                <h1 className="text-lg font-bold font-mono text-[#f0f2ff] tracking-tight">{datacenterName}</h1>
                <span className="px-2 py-0.5 rounded text-[10px] font-mono font-bold uppercase tracking-wider bg-[#1b2a38] text-[#8cd6cb] border border-[#8cd6cb]/30">
                  Summary
                </span>
              </div>
              <p className="text-xs text-[#8d909b] font-mono mt-0.5">
                Datacenter overview • quick actions • recent console activity • live storage probes
              </p>
            </div>
          </div>

          <div className="flex items-center gap-2">
            <div className="px-3 py-1.5 rounded-md bg-[#171a2d] border border-[#26293c] flex items-center gap-2 text-xs font-mono">
              <span className="text-[#8d909b]">HEALTH:</span>
              <span className={`font-bold ${health.statusColor}`}>
                {health.score}% {health.statusLabel}
              </span>
            </div>
            <div className="px-3 py-1.5 rounded-md bg-[#171a2d] border border-[#26293c] flex items-center gap-2 text-xs font-mono">
              <span className="text-[#8d909b]">ONLINE:</span>
              <span className="font-bold text-[#8cd6cb]">
                {health.onlineCount} / {health.totalCount} NODES
              </span>
            </div>
          </div>
        </div>

        <div className="grid grid-cols-2 sm:grid-cols-4 gap-2 pt-2 border-t border-[#26293c]/60">
          {[
            { label: 'Physical Hosts', value: hostCount, color: 'text-[#b0c9ff]' },
            { label: 'Virtual Machines', value: vmCount, color: 'text-[#d9b9ff]' },
            { label: 'LXC Containers', value: lxcCount, color: 'text-[#8cd6cb]' },
            { label: 'Podman Nodes', value: containerCount, color: 'text-[#ffd580]' },
          ].map(stat => (
            <div
              key={stat.label}
              className="bg-[#171a2d] px-3 py-2 rounded border border-[#26293c] flex items-center justify-between"
            >
              <span className="text-[11px] font-mono text-[#8d909b]">{stat.label}</span>
              <span className={`text-sm font-mono font-bold ${stat.color}`}>{stat.value}</span>
            </div>
          ))}
        </div>
      </div>

      {/* Quick actions */}
      <div className="rounded-lg bg-[#121528] border border-[#26293c] p-4 shadow-md">
        <div className="flex items-center justify-between mb-3">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[18px] text-[#b0c9ff]">flash_on</span>
            <h2 className="text-xs font-mono font-bold text-[#dfe1fa] uppercase tracking-wider">
              Datacenter Quick Actions
            </h2>
          </div>
          <span className="text-[10px] font-mono text-[#8d909b]">Cluster-wide operations</span>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-6 gap-2.5">
          <QuickActionButton
            testId="summary-quick-action-run-playbook"
            icon="play_circle"
            iconClass="text-[#b0c9ff]"
            badge="Run"
            badgeClass="bg-[#21263d] text-[#b0c9ff] border-[#b0c9ff]/30"
            title="Run Ansible Playbook"
            description="Execute baseline, hardening or container-update playbooks"
            onClick={() => onOpenPlaybookModal()}
          />
          <QuickActionButton
            testId="summary-quick-action-manage-playbooks"
            icon="edit_document"
            iconClass="text-[#8cd6cb]"
            badge="Edit"
            badgeClass="bg-[#1b2a38] text-[#8cd6cb] border-[#8cd6cb]/30"
            title="Manage Playbooks"
            description="Create and inspect YAML playbooks in ansible/playbooks"
            onClick={() => onOpenPlaybookEditor()}
          />
          <QuickActionButton
            testId="summary-quick-action-vars-vault"
            icon="key"
            iconClass="text-[#d9b9ff]"
            badge="Vault"
            badgeClass="bg-[#291e3b] text-[#d9b9ff] border-[#d9b9ff]/30"
            title="Cluster Vars & Vault"
            description="Group variables and AES-256 encrypted secrets"
            onClick={onOpenGroupVars}
          />
          <QuickActionButton
            testId="summary-quick-action-console"
            icon="terminal"
            iconClass="text-[#ffd580]"
            badge={`${queue.length} Queued`}
            badgeClass="bg-[#2d281b] text-[#ffd580] border-[#ffd580]/30"
            title="Console & Tasks"
            description="Live job queue, ansible logs and SSH output"
            onClick={() => (onOpenConsole ? onOpenConsole() : openSection('tasks'))}
          />
          <QuickActionButton
            testId="summary-quick-action-storage"
            icon="storage"
            iconClass="text-[#8cd6cb]"
            badge="Pools"
            badgeClass="bg-[#1b2a38] text-[#8cd6cb] border-[#8cd6cb]/30"
            title="Cluster Storage"
            description="ZFS pools, mounts, NFS and Samba exports"
            onClick={() => openSection('storage')}
          />
          <QuickActionButton
            testId="summary-quick-action-playbook-library"
            icon="library_books"
            iconClass="text-[#b0c9ff]"
            badge="Library"
            badgeClass="bg-[#21263d] text-[#b0c9ff] border-[#b0c9ff]/30"
            title="Playbook Library"
            description="Browse the on-disk playbook library"
            onClick={() => openSection('playbooks')}
          />
        </div>
      </div>

      <div className="grid grid-cols-1 xl:grid-cols-3 gap-4">
        {/* Recent console tasks */}
        <div className="xl:col-span-2 rounded-lg bg-[#121528] border border-[#26293c] p-4 shadow-md flex flex-col gap-3">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <span className="material-symbols-outlined text-[18px] text-[#b0c9ff]">history</span>
              <h2 className="text-xs font-mono font-bold text-[#dfe1fa] uppercase tracking-wider">
                Recent Console Tasks
              </h2>
              <span className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-[#21263d] text-[#b0c9ff] border border-[#b0c9ff]/30">
                {queueCounts.staged} staged • {queueCounts.running} running
                {isQueueRunning ? ' • executing' : ''}
              </span>
            </div>
            <button
              type="button"
              data-testid="summary-open-console"
              onClick={() => (onOpenConsole ? onOpenConsole() : openSection('tasks'))}
              className="text-[11px] font-mono text-[#8cd6cb] hover:underline cursor-pointer flex items-center gap-0.5"
            >
              <span>Open Console</span>
              <span className="material-symbols-outlined text-[13px]">open_in_new</span>
            </button>
          </div>

          {activity.length === 0 ? (
            <div
              data-testid="summary-console-empty"
              className="p-4 text-center text-xs font-mono text-[#8d909b] bg-[#171a2d] rounded border border-[#26293c]"
            >
              No console tasks or queued ansible mutations recorded yet.
            </div>
          ) : (
            <div className="space-y-1.5 font-mono text-xs">
              {activity.map(item => (
                <div
                  key={item.id}
                  className="p-2.5 rounded bg-[#171a2d] border border-[#26293c] flex flex-wrap items-center justify-between gap-2"
                >
                  <div className="flex items-center gap-2 min-w-0">
                    <span className={`px-1.5 py-0.5 rounded text-[10px] font-bold border ${statusClass(item.status)}`}>
                      {item.status}
                    </span>
                    <span className="text-[9px] uppercase tracking-wider text-[#8d909b] px-1 py-0.5 rounded bg-[#121528] border border-[#26293c]">
                      {item.source === 'queue' ? 'ansible queue' : 'console'}
                    </span>
                    <span className="text-[#dfe1fa] font-semibold truncate max-w-md" title={item.title}>
                      {item.title}
                    </span>
                  </div>
                  <div className="flex items-center gap-3 text-[11px] text-[#8d909b]">
                    <span>
                      Target: <span className="text-[#b0c9ff]">{item.target || '—'}</span>
                    </span>
                    <span title={item.timestamp ? new Date(item.timestamp).toLocaleString() : 'No timestamp in feed'}>
                      {formatRelativeTime(item.timestamp)}
                    </span>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>

        {/* Storage summary */}
        <div
          data-testid="summary-storage"
          className="rounded-lg bg-[#121528] border border-[#26293c] p-4 shadow-md flex flex-col gap-3"
        >
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <span className="material-symbols-outlined text-[18px] text-[#ffd580]">storage</span>
              <h2 className="text-xs font-mono font-bold text-[#dfe1fa] uppercase tracking-wider">Storage Summary</h2>
            </div>
            <button
              type="button"
              onClick={() => openSection('storage')}
              className="text-[11px] font-mono text-[#8cd6cb] hover:underline cursor-pointer"
            >
              Details
            </button>
          </div>

          {storageLoading ? (
            <div className="p-3 text-center text-xs font-mono text-[#8d909b] animate-pulse">
              Probing {storageNodes.length} online host{storageNodes.length === 1 ? '' : 's'}…
            </div>
          ) : (
            <div className="space-y-2">
              <div className="grid grid-cols-2 gap-2">
                <div className="p-2.5 rounded bg-[#171a2d] border border-[#26293c]">
                  <div className="text-[10px] font-mono uppercase text-[#8d909b]">ZFS Pools</div>
                  <div className="text-sm font-mono font-bold text-[#8cd6cb]">{storage.pools.length}</div>
                </div>
                <div className="p-2.5 rounded bg-[#171a2d] border border-[#26293c]">
                  <div className="text-[10px] font-mono uppercase text-[#8d909b]">Mounts</div>
                  <div className="text-sm font-mono font-bold text-[#b0c9ff]">{storage.filesystemCount}</div>
                </div>
                <div className="p-2.5 rounded bg-[#171a2d] border border-[#26293c]">
                  <div className="text-[10px] font-mono uppercase text-[#8d909b]">Pool Capacity</div>
                  <div className="text-sm font-mono font-bold text-[#dfe1fa]">
                    {storage.poolCapacityBytes > 0 ? formatBytes(storage.poolCapacityBytes) : 'unavailable'}
                  </div>
                </div>
                <div className="p-2.5 rounded bg-[#171a2d] border border-[#26293c]">
                  <div className="text-[10px] font-mono uppercase text-[#8d909b]">Pool Free</div>
                  <div className="text-sm font-mono font-bold text-[#dfe1fa]">
                    {storage.poolCapacityBytes > 0 ? formatBytes(storage.poolFreeBytes) : 'unavailable'}
                  </div>
                </div>
              </div>

              {storage.filesystemsMeasured > 0 && (
                <div className="p-2.5 rounded bg-[#171a2d] border border-[#26293c] font-mono text-[11px]">
                  <div className="flex justify-between text-[#8d909b]">
                    <span>
                      Measured mounts ({storage.filesystemsMeasured}/{storage.filesystemCount})
                    </span>
                    <span className="text-[#dfe1fa]">
                      {formatBytes(storage.filesystemUsedBytes)} / {formatBytes(storage.filesystemSizeBytes)}
                    </span>
                  </div>
                  <div className="flex justify-between text-[#8d909b] mt-0.5">
                    <span>Available</span>
                    <span className="text-[#8cd6cb]">{formatBytes(storage.filesystemAvailBytes)}</span>
                  </div>
                </div>
              )}

              {storage.filesystemCount > 0 && storage.filesystemsMeasured === 0 && (
                <div
                  data-testid="summary-storage-unmeasured-mounts"
                  className="p-2.5 rounded bg-[#2d281b] border border-[#ffd580]/30 font-mono text-[11px] text-[#ffd580]"
                >
                  {storage.filesystemCount} mount{storage.filesystemCount === 1 ? '' : 's'} reported without
                  capacity columns (findmnt returned no SIZE/USED/AVAIL).
                </div>
              )}

              {storage.degradedPools.length > 0 && (
                <div className="p-2.5 rounded bg-[#311c1c] border border-[#ffb4ab]/30 font-mono text-[11px] text-[#ffb4ab]">
                  {storage.degradedPools.length} pool{storage.degradedPools.length === 1 ? '' : 's'} not ONLINE:{' '}
                  {storage.degradedPools.map(p => `${p.name}@${p.host} (${p.health})`).join(', ')}
                </div>
              )}

              {storage.unmeasuredPools.length > 0 && (
                <div className="p-2.5 rounded bg-[#2d281b] border border-[#ffd580]/30 font-mono text-[11px] text-[#ffd580]">
                  Size unavailable for: {storage.unmeasuredPools.map(p => `${p.name}@${p.host}`).join(', ')}
                </div>
              )}

              {storage.pools.length > 0 && (
                <div className="space-y-1 font-mono text-[10px] text-[#c3c6d2] bg-[#121528] p-2 rounded border border-[#26293c]">
                  {storage.pools.slice(0, 4).map(pool => (
                    <div key={`${pool.nodeId}:${pool.name}`} className="flex justify-between gap-2">
                      <span className="text-[#8d909b] truncate">
                        {pool.name} ({pool.host}):
                      </span>
                      <span className="text-[#8cd6cb] font-bold shrink-0">
                        {pool.sizeBytes !== undefined ? formatBytes(pool.sizeBytes) : 'size n/a'} / {pool.health}
                      </span>
                    </div>
                  ))}
                </div>
              )}

              {storage.unavailableHosts.length > 0 && (
                <div className="p-2.5 rounded bg-[#171a2d] border border-[#ffb4ab]/30 font-mono text-[10px] text-[#ffb4ab] space-y-0.5">
                  <div className="uppercase tracking-wider text-[9px] text-[#8d909b]">Probe failures</div>
                  {storage.unavailableHosts.map(host => (
                    <div key={host.nodeId} className="truncate" title={host.error}>
                      {host.nodeName}: {host.error}
                    </div>
                  ))}
                </div>
              )}

              {!storage.hasData && storage.unavailableHosts.length === 0 && (
                <div
                  data-testid="summary-storage-empty"
                  className="p-3 text-center text-[11px] font-mono text-[#8d909b] bg-[#171a2d] rounded border border-[#26293c]"
                >
                  {storageNodes.length === 0
                    ? 'No online hosts to probe for storage.'
                    : 'No ZFS pools or mounted filesystems reported by the probed hosts.'}
                </div>
              )}

              <div className="text-[10px] font-mono text-[#8d909b]">
                Probed {storage.probedHosts}/{storageNodes.length} online host
                {storageNodes.length === 1 ? '' : 's'} via /api/nodes/:id/storage
              </div>
            </div>
          )}
        </div>
      </div>

      {/* Mesh roster (real node inventory) */}
      <div className="rounded-lg bg-[#121528] border border-[#26293c] p-4 shadow-md flex flex-col gap-3">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[18px] text-[#d9b9ff]">hub</span>
            <h2 className="text-xs font-mono font-bold text-[#dfe1fa] uppercase tracking-wider">Mesh Nodes</h2>
            <span className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-[#291e3b] text-[#d9b9ff] border border-[#d9b9ff]/30">
              {nodes.length} registered
            </span>
          </div>
          <button
            type="button"
            onClick={() => document.getElementById('datacenter-topology')?.scrollIntoView({ behavior: 'smooth', block: 'start' })}
            className="text-[11px] font-mono text-[#8cd6cb] hover:underline cursor-pointer"
          >
            Topology
          </button>
        </div>

        {nodes.length === 0 ? (
          <div className="p-4 text-center text-xs font-mono text-[#8d909b]">No nodes registered in this datacenter.</div>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-2">
            {nodes.map(node => (
              <button
                key={node.id}
                type="button"
                onClick={() => onSelectNode(node)}
                className="text-left p-2.5 rounded bg-[#171a2d] hover:bg-[#1e2338] border border-[#26293c] hover:border-[#b0c9ff]/40 transition-all cursor-pointer font-mono"
              >
                <div className="flex items-center gap-2">
                  <span
                    className={`w-2 h-2 rounded-full shrink-0 ${
                      node.status === 'online' ? 'bg-[#8cd6cb] shadow-[0_0_6px_rgba(140,214,203,0.8)]' : 'bg-[#ffb4ab]'
                    }`}
                  />
                  <span className="text-xs font-bold text-[#dfe1fa] truncate">{node.name}</span>
                </div>
                <div className="text-[10px] text-[#8d909b] mt-1 flex items-center justify-between gap-2">
                  <span className="truncate">{node.tailscaleIp || node.ip}</span>
                  <span className="text-[#c3c6d2] shrink-0">{node.status}</span>
                </div>
              </button>
            ))}
          </div>
        )}
      </div>
    </div>
  );
};

export default DatacenterSummaryView;
