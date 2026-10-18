import React, { useState } from 'react';
import type { NodeItem } from '../types/index.ts';
import {
  groupNodesByDynamicCluster,
  buildHostGroupPills,
  filterNodesBySearchQuery,
  resolveNodeIcon,
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
          </div>

          {filterOpen && (
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

        {/* Navigation Tree */}
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
                    <div className="pl-3 space-y-0.5">
                      {groupNodes.map(node => {
                        const isSelected = node.id === selectedNodeId;
                        return (
                          <div
                            key={node.id}
                            className={`group flex items-center gap-1.5 px-2 py-1 rounded transition-colors ${
                              isSelected
                                ? 'bg-[#26293c] text-[#b0c9ff] font-bold border-l-2 border-[#b0c9ff]'
                                : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
                            }`}
                          >
                            <button
                              type="button"
                              onClick={() => onSelectNode(node)}
                              className="flex items-center gap-1.5 flex-1 min-w-0 text-left cursor-pointer"
                            >
                              <span
                                className={`w-1.5 h-1.5 rounded-full shrink-0 ${
                                  node.sshConnected
                                    ? 'bg-[#8cd6cb]'
                                    : node.status === 'online'
                                    ? 'bg-[#b0c9ff]'
                                    : 'bg-[#8d909b]'
                                }`}
                                title={
                                  node.sshConnected
                                    ? `SSH connected (${node.sshUser || 'core'}@${node.tailscaleHost})`
                                    : `Tailscale ${node.status}`
                                }
                              ></span>
                              <span className="material-symbols-outlined text-[14px] shrink-0">
                                {resolveNodeIcon(node)}
                              </span>
                              <span className="truncate flex-1">{node.name}</span>
                            </button>

                            <span className="text-[10px] text-[#8d909b] font-mono">
                              {node.sshUser || 'core'}
                            </span>

                            {node.badge && (
                              <span
                                className={`font-mono text-[10px] ${
                                  node.badgeColor || 'text-[#8d909b]'
                                }`}
                              >
                                {node.badge}
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
                                className="opacity-70 hover:opacity-100 text-[#8d909b] hover:text-[#b0c9ff] p-0.5 cursor-pointer"
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

        {/* Footer Network Ping & Quorum */}
        <div className="p-2 bg-[#0a0d1f] flex items-center justify-between text-[#c3c6d2] font-mono text-xs border-t border-[#26293c]">
          <span className="flex items-center gap-1">
            <span className="material-symbols-outlined text-[14px] text-[#8cd6cb]">network_ping</span>
            <span>RTT: {selectedNode?.rttMs || '1.8ms'}</span>
          </span>
          <span className="text-[#8cd6cb]">
            SSH: {selectedNode?.sshUser || 'core'}@{selectedNode?.shortName || 'host'}
          </span>
        </div>
      </aside>
    </>
  );
};
