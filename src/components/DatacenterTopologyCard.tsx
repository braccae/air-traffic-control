import React, { useMemo, useState } from 'react';
import type { NodeItem } from '../types/index.ts';
import {
  formatNodeIpBadge,
  formatNodeTypeBadge,
  groupNodesByDynamicCluster,
  resolveNodeIcon,
} from '../utils/navigationViewModel.ts';

export interface DatacenterTopologyCardProps {
  nodes: NodeItem[];
  /** Optional: when omitted the node cards are informational only. */
  onSelectNode?: (node: NodeItem) => void;
}

/**
 * Tailnet mesh topology grouped by the tags each peer actually reports.
 * Lives in the Tailscale Mesh settings page; the overview only summarises.
 */
export const DatacenterTopologyCard: React.FC<DatacenterTopologyCardProps> = ({ nodes, onSelectNode }) => {
  const [filterQuery, setFilterQuery] = useState('');

  const clusterGroups = useMemo(() => groupNodesByDynamicCluster(nodes), [nodes]);

  const filteredClusters = filterQuery.trim()
    ? clusterGroups
        .map(cg => ({
          ...cg,
          nodes: cg.nodes.filter(
            n =>
              n.name.toLowerCase().includes(filterQuery.toLowerCase()) ||
              n.ip.includes(filterQuery) ||
              (n.tailscaleIp || '').includes(filterQuery) ||
              (n.osDescription || '').toLowerCase().includes(filterQuery.toLowerCase())
          ),
        }))
        .filter(cg => cg.nodes.length > 0)
    : clusterGroups;

  return (
    <div
      id="datacenter-topology"
      data-testid="datacenter-topology"
      className="rounded-lg bg-[#121528] border border-[#26293c] p-4 shadow-md flex flex-col gap-3"
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <span className="material-symbols-outlined text-[18px] text-[#d9b9ff]">hub</span>
          <h2 className="text-xs font-mono font-bold text-[#dfe1fa] uppercase tracking-wider">
            Tailnet Mesh Topology &amp; Node Distribution
          </h2>
          <span className="text-[10px] font-mono px-1.5 py-0.2 rounded bg-[#291e3b] text-[#d9b9ff] border border-[#d9b9ff]/30">
            {nodes.length} Nodes
          </span>
        </div>

        <div className="flex items-center gap-1 bg-[#171a2d] px-2 py-1 rounded border border-[#313447]">
          <span className="material-symbols-outlined text-[13px] text-[#8d909b]">search</span>
          <input
            type="text"
            value={filterQuery}
            onChange={e => setFilterQuery(e.target.value)}
            placeholder="Search host, IP, OS..."
            className="w-36 bg-transparent text-[#dfe1fa] font-mono text-[11px] outline-none"
          />
          {filterQuery && (
            <button
              type="button"
              onClick={() => setFilterQuery('')}
              className="text-[#8d909b] hover:text-[#dfe1fa] text-[11px] cursor-pointer"
            >
              ×
            </button>
          )}
        </div>
      </div>

      <div className="space-y-4">
        {filteredClusters.map(cluster => (
          <div key={cluster.group} className="space-y-2">
            <div className="flex items-center gap-2 px-1 text-xs font-mono">
              <span className="text-[#8cd6cb] font-bold">[{cluster.group}]</span>
              <span className="text-[#8d909b] text-[11px]">({cluster.nodes.length} nodes)</span>
            </div>

            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-2">
              {cluster.nodes.map(node => {
                const badgeInfo = formatNodeTypeBadge(node);
                const ipBadge = formatNodeIpBadge(node);

                return (
                  <div
                    key={node.id}
                    onClick={onSelectNode ? () => onSelectNode(node) : undefined}
                    className={`group p-2.5 rounded-lg bg-[#171a2d] border border-[#26293c] transition-all flex flex-col justify-between gap-2 shadow-xs ${
                      onSelectNode ? 'hover:bg-[#1e2338] hover:border-[#b0c9ff]/40 cursor-pointer' : ''
                    }`}
                  >
                    <div className="flex items-start justify-between gap-1">
                      <div className="flex items-center gap-2 min-w-0">
                        <span
                          className={`w-2 h-2 rounded-full shrink-0 ${
                            node.status === 'online'
                              ? 'bg-[#8cd6cb] shadow-[0_0_6px_rgba(140,214,203,0.8)] animate-pulse'
                              : 'bg-[#ffb4ab]'
                          }`}
                        />
                        <span className="material-symbols-outlined text-[16px] text-[#b0c9ff] shrink-0">
                          {resolveNodeIcon(node)}
                        </span>
                        <span className="font-mono text-xs font-bold text-[#dfe1fa] truncate group-hover:text-[#b0c9ff]">
                          {node.name}
                        </span>
                      </div>

                      {badgeInfo && (
                        <span
                          className={`font-mono text-[9px] px-1 py-0.2 rounded font-semibold uppercase tracking-wider shrink-0 ${badgeInfo.badgeClasses}`}
                        >
                          {badgeInfo.label}
                        </span>
                      )}
                    </div>

                    <div className="font-mono text-[10px] text-[#8d909b] space-y-0.5">
                      <div className="flex items-center justify-between">
                        <span>IP:</span>
                        <span className="text-[#dfe1fa]">{ipBadge || node.ip}</span>
                      </div>
                      <div className="flex items-center justify-between">
                        <span>Specs:</span>
                        <span className="text-[#dfe1fa]">
                          {node.specs?.vcpus ?? '—'} vCPUs • {node.specs?.ramGiB ?? '—'} GB
                        </span>
                      </div>
                      <div className="flex items-center justify-between">
                        <span>Uptime:</span>
                        <span className="text-[#8cd6cb]">{node.uptime}</span>
                      </div>
                    </div>

                    <div className="flex items-center justify-between pt-1.5 border-t border-[#26293c]/60 text-[10px] font-mono text-[#8d909b]">
                      <span className="flex items-center gap-1">
                        <span className="material-symbols-outlined text-[12px] text-[#8cd6cb]">
                          {node.sshConnected ? 'vpn_key' : 'lock'}
                        </span>
                        <span>{node.sshConnected ? 'SSH Ready' : 'Tailscale'}</span>
                      </span>
                    </div>
                  </div>
                );
              })}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
};
