import React from 'react';
import type { NodeItem } from '../types/index.ts';

interface ServerViewSidebarProps {
  nodes: NodeItem[];
  selectedNodeId: string;
  onSelectNode: (node: NodeItem) => void;
  selectedHostGroup: string;
  onSelectHostGroup: (group: string) => void;
  isOpenMobile?: boolean;
  onCloseMobile?: () => void;
}

export const ServerViewSidebar: React.FC<ServerViewSidebarProps> = ({
  nodes,
  selectedNodeId,
  onSelectNode,
  selectedHostGroup,
  onSelectHostGroup,
  isOpenMobile = false,
  onCloseMobile,
}) => {
  // Group nodes by group key
  const coldWarNodes = nodes.filter(n => n.group === 'cold-war');
  const noodleNodes = nodes.filter(n => n.group === 'noodle');
  const otherNodes = nodes.filter(n => n.group !== 'cold-war' && n.group !== 'noodle');

  const hostGroups = [
    { name: 'all: 14', id: 'all' },
    { name: 'webservers: 4', id: 'webservers' },
    { name: 'db_cluster: 3', id: 'db_cluster' },
    { name: 'storage_nodes: 2', id: 'storage_nodes' },
    { name: 'edge_gateways: 2', id: 'edge_gateways' },
  ];

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
      <div className="p-2 bg-[#0a0d1f] flex items-center justify-between border-b border-[#26293c]">
        <div className="flex items-center gap-1.5 text-[#dfe1fa] font-mono text-xs">
          <span className="material-symbols-outlined text-[16px] text-[#b0c9ff]">view_list</span>
          <span className="font-bold">Server View</span>
        </div>
        <button
          type="button"
          aria-label="Filter server view"
          className="text-[#8d909b] hover:text-[#dfe1fa] transition-colors p-0.5"
        >
          <span className="material-symbols-outlined text-[16px]">filter_list</span>
        </button>
      </div>

      {/* Navigation Tree */}
      <nav className="flex-1 overflow-y-auto p-1.5 font-mono text-xs space-y-0.5">
        {/* Datacenter Root */}
        <div className="flex items-center gap-1 px-2 py-1 text-[#c3c6d2]">
          <span className="material-symbols-outlined text-[14px]">arrow_drop_down</span>
          <span className="material-symbols-outlined text-[14px] text-[#b0c9ff]">cloud_queue</span>
          <span className="font-bold text-[#dfe1fa]">homelab-dc1</span>
          <span className="ml-auto text-[#8d909b] text-[10px]">(Datacenter)</span>
        </div>

        {/* Cold-War Cluster Group */}
        <div className="pl-3 space-y-0.5">
          <div className="flex items-center gap-1 px-2 py-0.5 text-[#c3c6d2]">
            <span className="material-symbols-outlined text-[14px]">arrow_drop_down</span>
            <span className="material-symbols-outlined text-[14px] text-[#d9b9ff]">hub</span>
            <span className="text-[#dfe1fa] font-medium">cold-war</span>
            <span className="ml-auto text-[#8cd6cb] font-bold">[{coldWarNodes.length}]</span>
          </div>

          <div className="pl-3 space-y-0.5">
            {coldWarNodes.map(node => {
              const isSelected = node.id === selectedNodeId;
              return (
                <button
                  key={node.id}
                  type="button"
                  onClick={() => onSelectNode(node)}
                  className={`w-full flex items-center gap-1.5 px-2 py-1 rounded text-left transition-colors cursor-pointer ${
                    isSelected
                      ? 'bg-[#26293c] text-[#b0c9ff] font-bold border-l-2 border-[#b0c9ff]'
                      : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
                  }`}
                >
                  <span
                    className={`w-1.5 h-1.5 rounded-full ${
                      node.status === 'online' ? 'bg-[#8cd6cb]' : 'bg-[#8d909b]'
                    }`}
                  ></span>
                  <span className="material-symbols-outlined text-[14px]">
                    {node.nodeType === 'windows'
                      ? 'desktop_windows'
                      : node.badge === 'GPU'
                      ? 'memory'
                      : node.badge === 'LXC'
                      ? 'deployed_code'
                      : node.badge === 'K3S'
                      ? 'lan'
                      : node.badge === 'PBS'
                      ? 'storage'
                      : 'dns'}
                  </span>
                  <span className="truncate flex-1">{node.name}</span>
                  {node.badge && (
                    <span className={`ml-auto font-mono text-[10px] ${node.badgeColor || 'text-[#8d909b]'}`}>
                      {node.badge}
                    </span>
                  )}
                </button>
              );
            })}
          </div>

          {/* Noodle Cluster Group */}
          <div className="flex items-center gap-1 px-2 py-0.5 text-[#c3c6d2] mt-1">
            <span className="material-symbols-outlined text-[14px]">arrow_drop_down</span>
            <span className="material-symbols-outlined text-[14px] text-[#d9b9ff]">hub</span>
            <span className="text-[#dfe1fa] font-medium">noodle</span>
            <span className="ml-auto text-[#8cd6cb] font-bold">[{noodleNodes.length}]</span>
          </div>

          <div className="pl-3 space-y-0.5">
            {noodleNodes.map(node => {
              const isSelected = node.id === selectedNodeId;
              return (
                <button
                  key={node.id}
                  type="button"
                  onClick={() => onSelectNode(node)}
                  className={`w-full flex items-center gap-1.5 px-2 py-1 rounded text-left transition-colors cursor-pointer ${
                    isSelected
                      ? 'bg-[#26293c] text-[#b0c9ff] font-bold border-l-2 border-[#b0c9ff]'
                      : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
                  }`}
                >
                  <span
                    className={`w-1.5 h-1.5 rounded-full ${
                      node.status === 'online' ? 'bg-[#8cd6cb]' : 'bg-[#8d909b]'
                    }`}
                  ></span>
                  <span className="material-symbols-outlined text-[14px]">
                    {node.nodeType === 'router_switch'
                      ? 'settings_ethernet'
                      : node.name.includes('postgres')
                      ? 'database'
                      : 'router'}
                  </span>
                  <span className="truncate flex-1">{node.name}</span>
                  {node.badge && (
                    <span className={`ml-auto font-mono text-[10px] ${node.badgeColor || 'text-[#8d909b]'}`}>
                      {node.badge}
                    </span>
                  )}
                </button>
              );
            })}
          </div>

          {/* Other / Network Nodes */}
          {otherNodes.map(node => {
            const isSelected = node.id === selectedNodeId;
            return (
              <button
                key={node.id}
                type="button"
                onClick={() => onSelectNode(node)}
                className={`w-full flex items-center gap-1.5 px-2 py-1 rounded text-left transition-colors cursor-pointer ${
                  isSelected
                    ? 'bg-[#26293c] text-[#b0c9ff] font-bold border-l-2 border-[#b0c9ff]'
                    : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
                }`}
              >
                <span className="w-1.5 h-1.5 rounded-full bg-[#8cd6cb]"></span>
                <span className="material-symbols-outlined text-[14px] text-[#8cd6cb]">settings_ethernet</span>
                <span className="truncate flex-1">{node.name}</span>
              </button>
            );
          })}
        </div>

        {/* Ansible Host Groups Box */}
        <div className="p-1.5 mt-2 bg-[#0a0d1f] rounded border border-[#26293c]/60">
          <div className="flex items-center justify-between text-[#c3c6d2] font-mono text-[10px] px-1.5 py-0.5">
            <span className="font-bold uppercase tracking-wider text-[#8d909b]">Ansible Host Groups</span>
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
          <span>RTT: 2.1ms</span>
        </span>
        <span className="text-[#8d909b]">Cluster: Quorate</span>
      </div>
    </aside>
    </>
  );
};
