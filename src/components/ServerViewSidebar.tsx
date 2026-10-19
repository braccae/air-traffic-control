import React, { useState } from 'react';
import type { NodeItem } from '../types/index.ts';
import {
  groupNodesByDynamicCluster,
  buildHostGroupPills,
  filterNodesBySearchQuery,
  resolveNodeIcon,
  formatNodeTypeBadge,
  formatNodeIpBadge,
  getDatacenterNavSections,
  calculateClusterHealthScore,
  type SidebarViewMode,
  type DatacenterSection,
} from '../utils/navigationViewModel.ts';

interface ServerViewSidebarProps {
  nodes: NodeItem[];
  selectedNodeId: string;
  onSelectNode: (node: NodeItem) => void;
  onEditNode?: (node: NodeItem) => void;
  selectedHostGroup: string;
  onSelectHostGroup: (group: string) => void;
  isOpenMobile?: boolean;
  onCloseMobile?: () => void;
  viewMode?: SidebarViewMode;
  onSelectViewMode?: (mode: SidebarViewMode) => void;
  selectedDatacenterSection?: DatacenterSection;
  onSelectDatacenterSection?: (section: DatacenterSection) => void;
}

export const ServerViewSidebar: React.FC<ServerViewSidebarProps> = ({
  nodes,
  selectedNodeId,
  onSelectNode,
  onEditNode,
  selectedHostGroup,
  onSelectHostGroup,
  isOpenMobile = false,
  onCloseMobile,
  viewMode = 'nodes',
  onSelectViewMode,
  selectedDatacenterSection = 'overview',
  onSelectDatacenterSection,
}) => {
  const [filterOpen, setFilterOpen] = useState(false);
  const [filterText, setFilterText] = useState('');
  const [collapsedGroups, setCollapsedGroups] = useState<Record<string, boolean>>({});

  const groupFilteredNodes =
    selectedHostGroup === 'all'
      ? nodes
      : nodes.filter(
          n =>
            n.group === selectedHostGroup ||
            (Array.isArray(n.tags) && n.tags.includes(selectedHostGroup))
        );

  const visibleNodes = filterNodesBySearchQuery(groupFilteredNodes, filterText);
  const clusterGroups = groupNodesByDynamicCluster(visibleNodes);
  const hostGroups = buildHostGroupPills(nodes);

  const selectedNode = nodes.find(n => n.id === selectedNodeId) || nodes[0];
  const datacenterName = selectedNode?.datacenter || 'leo-banjo.ts.net';
  const onlineCount = nodes.filter(n => n.status === 'online').length;
  const health = calculateClusterHealthScore(nodes);
  const datacenterSections = getDatacenterNavSections(4, 0);

  const toggleGroupCollapse = (grp: string) => {
    setCollapsedGroups(prev => ({ ...prev, [grp]: !prev[grp] }));
  };

  return (
    <>
      {/* Mobile backdrop */}
      {isOpenMobile && (
        <div
          onClick={onCloseMobile}
          className="fixed inset-0 bg-black/60 z-30 lg:hidden backdrop-blur-xs"
        />
      )}

      <aside
        className={`fixed left-0 top-10 bottom-44 w-72 bg-[#171a2d] z-40 flex flex-col shadow-[0_1px_8px_rgba(0,0,0,0.3)] border-r border-[#26293c] transition-transform duration-200 ${
          isOpenMobile ? 'translate-x-0' : '-translate-x-full lg:translate-x-0'
        }`}
      >
        {/* Sidebar Header */}
        <div className="p-2 bg-[#0a0d1f] flex flex-col gap-1.5 border-b border-[#26293c]">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-1.5 text-[#dfe1fa] font-mono text-xs">
              <span className="material-symbols-outlined text-[16px] text-[#b0c9ff]">view_list</span>
              <span className="font-bold">Tailscale Server View</span>
            </div>
            {viewMode === 'nodes' && (
              <button
                type="button"
                aria-label="Filter server view"
                onClick={() => setFilterOpen(prev => !prev)}
                className={`transition-colors p-0.5 cursor-pointer rounded ${
                  filterOpen || filterText
                    ? 'text-[#b0c9ff] bg-[#26293c]'
                    : 'text-[#8d909b] hover:text-[#dfe1fa]'
                }`}
              >
                <span className="material-symbols-outlined text-[16px]">filter_list</span>
              </button>
            )}
          </div>

          {/* Primary View Switcher: Nodes vs Datacenter */}
          <div className="grid grid-cols-2 gap-1 p-1 bg-[#121528] rounded border border-[#26293c]">
            <button
              type="button"
              onClick={() => onSelectViewMode?.('nodes')}
              className={`flex items-center justify-center gap-1.5 py-1 px-2 rounded text-xs font-mono font-bold transition-all cursor-pointer ${
                viewMode === 'nodes'
                  ? 'bg-[#21263d] text-[#b0c9ff] border border-[#b0c9ff]/40 shadow-xs'
                  : 'text-[#8d909b] hover:text-[#dfe1fa] hover:bg-[#1b1e31]'
              }`}
            >
              <span className="material-symbols-outlined text-[14px]">dns</span>
              <span>Nodes ({nodes.length})</span>
            </button>
            <button
              type="button"
              onClick={() => onSelectViewMode?.('datacenter')}
              className={`flex items-center justify-center gap-1.5 py-1 px-2 rounded text-xs font-mono font-bold transition-all cursor-pointer ${
                viewMode === 'datacenter'
                  ? 'bg-[#21263d] text-[#8cd6cb] border border-[#8cd6cb]/40 shadow-xs'
                  : 'text-[#8d909b] hover:text-[#dfe1fa] hover:bg-[#1b1e31]'
              }`}
            >
              <span className="material-symbols-outlined text-[14px]">hub</span>
              <span>Datacenter</span>
            </button>
          </div>

          {viewMode === 'nodes' && filterOpen && (
            <div className="flex items-center gap-1 bg-[#171a2d] px-2 py-1 rounded border border-[#313447]">
              <span className="material-symbols-outlined text-[13px] text-[#8d909b]">search</span>
              <input
                type="text"
                value={filterText}
                onChange={e => setFilterText(e.target.value)}
                placeholder="Filter host, IP, OS, tag..."
                className="w-full bg-transparent text-[#dfe1fa] font-mono text-[11px] outline-none"
              />
              {filterText && (
                <button
                  type="button"
                  onClick={() => setFilterText('')}
                  className="text-[#8d909b] hover:text-[#dfe1fa] text-[11px] cursor-pointer"
                >
                  ×
                </button>
              )}
            </div>
          )}
        </div>

        {/* Navigation Body */}
        {viewMode === 'datacenter' ? (
          <nav className="flex-1 overflow-y-auto p-2 font-mono text-xs space-y-2">
            {/* Datacenter Root / Tailnet Summary */}
            <div className="p-2 bg-[#121528] rounded border border-[#26293c] flex items-center justify-between">
              <div className="flex items-center gap-1.5 min-w-0">
                <span className="material-symbols-outlined text-[16px] text-[#8cd6cb]">cloud_queue</span>
                <div className="flex flex-col min-w-0">
                  <span className="font-bold text-[#dfe1fa] truncate text-xs">{datacenterName}</span>
                  <span className="text-[10px] text-[#8d909b]">{onlineCount}/{nodes.length} nodes online</span>
                </div>
              </div>
              <span className={`text-[10px] font-bold px-1.5 py-0.5 rounded ${health.statusColor} bg-[#1f2238] border border-current/20`}>
                {health.score}%
              </span>
            </div>

            {/* Global Options Navigation Sections */}
            <div className="space-y-1">
              <div className="px-1 text-[10px] font-bold uppercase tracking-wider text-[#8d909b]">
                Datacenter Global Options
              </div>
              {datacenterSections.map(sec => {
                const isActive = selectedDatacenterSection === sec.id;
                return (
                  <button
                    key={sec.id}
                    type="button"
                    onClick={() => onSelectDatacenterSection?.(sec.id)}
                    className={`w-full flex items-center gap-2 px-2.5 py-2 rounded transition-all text-left cursor-pointer ${
                      isActive
                        ? 'bg-[#21263d] text-[#8cd6cb] font-bold border-l-2 border-[#8cd6cb] shadow-[inset_0_0_12px_rgba(140,214,203,0.08)]'
                        : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
                    }`}
                  >
                    <span className="material-symbols-outlined text-[16px] shrink-0 text-[#8cd6cb]">
                      {sec.icon}
                    </span>
                    <div className="flex-1 min-w-0">
                      <div className="truncate text-xs font-semibold">{sec.title}</div>
                      <div className="text-[10px] text-[#8d909b] truncate">{sec.description}</div>
                    </div>
                    {sec.badge && (
                      <span className="text-[9px] px-1.5 py-0.5 rounded bg-[#26293c] text-[#8cd6cb] font-mono border border-[#8cd6cb]/30 shrink-0">
                        {sec.badge}
                      </span>
                    )}
                  </button>
                );
              })}
            </div>

            {/* Tailscale / Ansible Host Groups Box */}
            <div className="p-1.5 mt-2 bg-[#0a0d1f] rounded border border-[#26293c]/60">
              <div className="flex items-center justify-between text-[#c3c6d2] font-mono text-[10px] px-1.5 py-0.5">
                <span className="font-bold uppercase tracking-wider text-[#8d909b]">
                  Mesh Host Clusters
                </span>
                <span className="material-symbols-outlined text-[14px] text-[#8d909b]">tune</span>
              </div>
              <div className="flex flex-wrap gap-1 p-1 font-mono text-[11px]">
                {hostGroups.map(hg => {
                  const active = selectedHostGroup === hg.id;
                  return (
                    <button
                      key={hg.id}
                      type="button"
                      onClick={() => onSelectHostGroup(hg.id)}
                      className={`px-1.5 py-0.5 rounded transition-colors cursor-pointer ${
                        active
                          ? 'bg-[#313447] text-[#8cd6cb] font-bold border border-[#8cd6cb]/40'
                          : 'bg-[#1b1e31] text-[#c3c6d2] hover:bg-[#26293c] hover:text-[#dfe1fa]'
                      }`}
                    >
                      {hg.name}
                    </button>
                  );
                })}
              </div>
            </div>
          </nav>
        ) : (
          <nav className="flex-1 overflow-y-auto p-1.5 font-mono text-xs space-y-0.5">
            {/* Datacenter / Tailnet Root */}
            <div className="flex items-center gap-1 px-2 py-1 text-[#c3c6d2]">
              <span className="material-symbols-outlined text-[14px]">arrow_drop_down</span>
              <span className="material-symbols-outlined text-[14px] text-[#b0c9ff]">cloud_queue</span>
              <span className="font-bold text-[#dfe1fa] truncate">{datacenterName}</span>
              <span className="ml-auto text-[#8cd6cb] text-[10px]">({onlineCount}/{nodes.length} up)</span>
            </div>

            {/* Dynamic Cluster / Tag Groups */}
            <div className="pl-2 space-y-1">
              {clusterGroups.map(({ group, nodes: groupNodes }) => {
                const isCollapsed = Boolean(collapsedGroups[group]);
                return (
                  <div key={group} className="space-y-0.5">
                    <button
                      type="button"
                      onClick={() => toggleGroupCollapse(group)}
                      className="w-full flex items-center gap-1 px-2 py-0.5 text-[#c3c6d2] hover:text-[#dfe1fa] cursor-pointer"
                    >
                      <span className="material-symbols-outlined text-[14px]">
                        {isCollapsed ? 'arrow_right' : 'arrow_drop_down'}
                      </span>
                      <span className="material-symbols-outlined text-[14px] text-[#d9b9ff]">hub</span>
                      <span className="text-[#dfe1fa] font-medium">{group}</span>
                      <span className="ml-auto text-[#8cd6cb] font-bold">[{groupNodes.length}]</span>
                    </button>

                    {!isCollapsed && (
                      <div className="pl-2 space-y-0.5">
                        {groupNodes.map(node => {
                          const isSelected = node.id === selectedNodeId;
                          const badgeInfo = formatNodeTypeBadge(node);
                          const ipBadge = formatNodeIpBadge(node);

                          return (
                            <div
                              key={node.id}
                              className={`group flex items-center gap-1 px-2 py-1.5 rounded transition-all ${
                                isSelected
                                  ? 'bg-[#21263d] text-[#b0c9ff] font-bold border-l-2 border-[#b0c9ff] shadow-[inset_0_0_12px_rgba(176,201,255,0.08)]'
                                  : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
                              }`}
                            >
                              <button
                                type="button"
                                onClick={() => onSelectNode(node)}
                                className="flex items-center gap-1.5 flex-1 min-w-0 text-left cursor-pointer"
                              >
                                <span
                                  className={`w-2 h-2 rounded-full shrink-0 ${
                                    node.status === 'online'
                                      ? 'bg-[#8cd6cb] shadow-[0_0_6px_rgba(140,214,203,0.8)] animate-pulse'
                                      : 'bg-[#ffb4ab]'
                                  }`}
                                  title={
                                    node.sshConnected
                                      ? `SSH active (${node.sshUser || 'core'}@${node.tailscaleHost || node.ip})`
                                      : `Tailscale ${node.status}`
                                  }
                                ></span>
                                <span className="material-symbols-outlined text-[15px] shrink-0 text-[#b0c9ff]">
                                  {resolveNodeIcon(node)}
                                </span>
                                <span className="truncate flex-1 font-semibold">{node.name}</span>
                              </button>

                              {/* Monospaced Tailscale IP badge */}
                              {ipBadge && (
                                <span
                                  className="font-mono text-[9px] text-[#8d909b] truncate max-w-[68px] shrink-0"
                                  title={`Tailscale IP: ${ipBadge}`}
                                >
                                  {ipBadge}
                                </span>
                              )}

                              {/* Platform tier pill (only if distinct virtualization / platform tier) */}
                              {badgeInfo && (
                                <span
                                  className={`font-mono text-[9px] px-1 py-0.2 rounded font-semibold uppercase tracking-wider shrink-0 ${badgeInfo.badgeClasses}`}
                                >
                                  {badgeInfo.label}
                                </span>
                              )}

                              {onEditNode && (
                                <button
                                  type="button"
                                  onClick={e => {
                                    e.stopPropagation();
                                    onEditNode(node);
                                  }}
                                  title={`Edit settings for ${node.name}`}
                                  aria-label={`Edit settings for ${node.name}`}
                                  className="opacity-70 hover:opacity-100 text-[#8d909b] hover:text-[#b0c9ff] p-0.5 cursor-pointer shrink-0"
                                >
                                  <span className="material-symbols-outlined text-[13px]">settings</span>
                                </button>
                              )}
                            </div>
                          );
                        })}
                      </div>
                    )}
                  </div>
                );
              })}
            </div>

            {/* Ansible / Tailscale Host Groups Box */}
            <div className="p-1.5 mt-2 bg-[#0a0d1f] rounded border border-[#26293c]/60">
              <div className="flex items-center justify-between text-[#c3c6d2] font-mono text-[10px] px-1.5 py-0.5">
                <span className="font-bold uppercase tracking-wider text-[#8d909b]">
                  Tailscale &amp; Ansible Groups
                </span>
                <span className="material-symbols-outlined text-[14px] text-[#8d909b]">tune</span>
              </div>
              <div className="flex flex-wrap gap-1 p-1 font-mono text-[11px]">
                {hostGroups.map(hg => {
                  const active = selectedHostGroup === hg.id;
                  return (
                    <button
                      key={hg.id}
                      type="button"
                      onClick={() => onSelectHostGroup(hg.id)}
                      className={`px-1.5 py-0.5 rounded transition-colors cursor-pointer ${
                        active
                          ? 'bg-[#313447] text-[#b0c9ff] font-bold border border-[#b0c9ff]/40'
                          : 'bg-[#1b1e31] text-[#c3c6d2] hover:bg-[#26293c] hover:text-[#dfe1fa]'
                      }`}
                    >
                      {hg.name}
                    </button>
                  );
                })}
              </div>
            </div>
          </nav>
        )}

        {/* Footer */}
        {viewMode === 'datacenter' ? (
          <div className="p-2 bg-[#0a0d1f] flex items-center justify-between text-[#c3c6d2] font-mono text-xs border-t border-[#26293c]">
            <span className="flex items-center gap-1">
              <span className="material-symbols-outlined text-[14px] text-[#8cd6cb]">cloud_done</span>
              <span className="truncate max-w-[130px]">{datacenterName}</span>
            </span>
            <span className="text-[#8cd6cb]">
              Quorum: {onlineCount}/{nodes.length} online
            </span>
          </div>
        ) : (
          <div className="p-2 bg-[#0a0d1f] flex items-center justify-between text-[#c3c6d2] font-mono text-xs border-t border-[#26293c]">
            <span className="flex items-center gap-1">
              <span className="material-symbols-outlined text-[14px] text-[#8cd6cb]">network_ping</span>
              <span>RTT: {selectedNode?.rttMs || '1.8ms'}</span>
            </span>
            <span
              className="flex items-center gap-1 text-[#8cd6cb]"
              title={`SSH Session: ${selectedNode?.sshUser || 'core'}@${selectedNode?.tailscaleHost || selectedNode?.ip}`}
            >
              <span className="material-symbols-outlined text-[13px]">vpn_key</span>
              <span className="truncate max-w-[110px]">{selectedNode?.shortName || 'host'}</span>
            </span>
          </div>
        )}
      </aside>
    </>
  );
};
