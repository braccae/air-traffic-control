import React, { useState } from 'react';
import type { NodeItem, TagInventoryChunk, CustomFolder } from '../types/index.ts';
import { useContextMenu } from './ContextMenu.tsx';
import { buildNodeContextMenu, buildTagContextMenu, buildFolderContextMenu, type ContextMenuItem } from '../utils/contextMenuModel.ts';
import { copyTextToClipboard } from '../utils/clipboard.ts';
import { buildHostGroupPills, filterNodesBySearchQuery, resolveNodeIcon, formatNodeTypeBadge, getDatacenterSettingCategories, formatDatacenterRoute, calculateClusterHealthScore, buildInventoryTree, type SidebarViewMode, type DatacenterSection, type DatacenterSettingPageId, } from '../utils/navigationViewModel.ts';

interface ServerViewSidebarProps {
  nodes: NodeItem[];
  selectedNodeId: string;
  onSelectNode: (node: NodeItem) => void;
  onEditNode?: (node: NodeItem) => void;
  onOpenTerminal?: (node: NodeItem) => void;
  onOpenLogs?: (node: NodeItem) => void;
  onDeleteNode?: (nodeId: string) => void;
  onDeleteChunk?: (chunkId: string) => void;
  onMoveNodeToFolder?: (nodeId: string, folderId: string | null) => void;
  selectedHostGroup: string;
  onSelectHostGroup: (group: string) => void;
  isOpenMobile?: boolean;
  onCloseMobile?: () => void;
  viewMode?: SidebarViewMode;
  onSelectViewMode?: (mode: SidebarViewMode) => void;
  selectedDatacenterSection?: DatacenterSection;
  onSelectDatacenterSection?: (section: DatacenterSection) => void;
  selectedDatacenterSetting?: DatacenterSettingPageId | null;
  onSelectDatacenterSetting?: (page: DatacenterSettingPageId) => void;
  chunks?: TagInventoryChunk[];
  folders?: CustomFolder[];
  onCreateFolder?: (name: string) => void;
  onDeleteFolder?: (id: string) => void;
}

export const ServerViewSidebar: React.FC<ServerViewSidebarProps> = ({
  nodes,
  selectedNodeId,
  onSelectNode,
  onEditNode,
  onOpenTerminal,
  onOpenLogs,
  onDeleteNode,
  selectedHostGroup,
  onSelectHostGroup,
  isOpenMobile = false,
  onCloseMobile,
  viewMode = 'nodes',
  onSelectViewMode,
  selectedDatacenterSection = 'overview',
  onSelectDatacenterSection,
  selectedDatacenterSetting = null,
  onSelectDatacenterSetting,
  chunks = [],
  folders = [],
  onCreateFolder,
  onDeleteFolder,
  onDeleteChunk,
  onMoveNodeToFolder,
}) => {
  const [filterOpen, setFilterOpen] = useState(false);
  const [filterText, setFilterText] = useState('');
  const [tailnetCollapsed, setTailnetCollapsed] = useState(false);
  const [collapsedTags, setCollapsedTags] = useState<Record<string, boolean>>({});
  const [collapsedFolders, setCollapsedFolders] = useState<Record<string, boolean>>({});
  const [isCreatingFolder, setIsCreatingFolder] = useState(false);
  const [newFolderName, setNewFolderName] = useState('');
  const nodeMenu = useContextMenu();
  const tagMenu = useContextMenu();
  const folderMenu = useContextMenu();

  const handleNodeCopy = (text: string, label: string) => {
    void copyTextToClipboard(text, label);
  };

  const buildNodeMenu = (node: NodeItem): ContextMenuItem[] =>
    buildNodeContextMenu(node, {
      onSelect: onSelectNode,
      onOpenTerminal,
      onOpenLogs,
      onEdit: onEditNode,
      onDelete: onDeleteNode
        ? (target: NodeItem) => {
            const confirmed =
              typeof window === 'undefined' ||
              window.confirm(`Remove "${target.name}" from the inventory? The host itself is not modified.`);
            if (confirmed) onDeleteNode(target.id);
          }
        : undefined,
      onCopy: handleNodeCopy,
      onRemoveFromFolder:
        onMoveNodeToFolder && node.folder
          ? (target: NodeItem) => onMoveNodeToFolder(target.id, null)
          : undefined,
    });

  const handleNodeContextMenu = (event: React.MouseEvent, node: NodeItem) => {
    nodeMenu.open(event, buildNodeMenu(node));
  };

  const groupFilteredNodes =
    selectedHostGroup === 'all'
      ? nodes
      : nodes.filter(
          n =>
            n.group === selectedHostGroup ||
            (Array.isArray(n.tags) && n.tags.includes(selectedHostGroup)) ||
            n.followedTag === selectedHostGroup
        );

  const visibleNodes = filterNodesBySearchQuery(groupFilteredNodes, filterText);
  const hostGroups = buildHostGroupPills(nodes);

  const selectedNode = nodes.find(n => n.id === selectedNodeId);
  const tailscaleDc = nodes[0]?.tailscaleHost?.split('.').slice(1).join('.');
  const autodetectedDc = nodes.find(n => n.datacenter)?.datacenter || tailscaleDc || null;
  const datacenterName = selectedNode?.datacenter || autodetectedDc || 'unknown';
  const onlineCount = nodes.filter(n => n.status === 'online').length;
  const health = calculateClusterHealthScore(nodes);
  const datacenterSettingCategories = getDatacenterSettingCategories();

  const inventoryTree = buildInventoryTree(visibleNodes, chunks, folders, datacenterName);
  const tsNodes = inventoryTree.tailscale.tags.flatMap(t => t.nodes);
  const tsOnlineCount = tsNodes.filter(n => n.status === 'online').length;
  const tsTotalCount = tsNodes.length;

  const renderNodeItem = (node: NodeItem) => {
    const isSelected = node.id === selectedNodeId;
    const badgeInfo = formatNodeTypeBadge(node);

    return (
      <div
        key={node.id}
        draggable
        onDragStart={event => {
          event.dataTransfer?.setData('text/plain', node.id);
          if (event.dataTransfer) event.dataTransfer.effectAllowed = 'move';
        }}
        onContextMenu={event => handleNodeContextMenu(event, node)}
        data-context-menu="node-row"
        data-testid={`node-row-${node.id}`}
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
                : `Status: ${node.status}`
            }
          />
          <span className="material-symbols-outlined text-[15px] shrink-0 text-[#b0c9ff]">
            {resolveNodeIcon(node)}
          </span>
          <span className="truncate flex-1 font-semibold">{node.name}</span>
        </button>

        {/* Platform tier pill (only if distinct virtualization / platform tier) */}
        {badgeInfo && (
          <span
            className={`font-mono text-[9px] px-1 py-0.2 rounded font-semibold uppercase tracking-wider shrink-0 ${badgeInfo.badgeClasses}`}
          >
            {badgeInfo.label}
          </span>
        )}
      </div>
    );
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
        className={`fixed left-0 top-10 bottom-[var(--console-height,176px)] w-72 bg-[#171a2d] z-40 flex flex-col shadow-[0_1px_8px_rgba(0,0,0,0.3)] border-r border-[#26293c] transition-transform duration-200 ${
          isOpenMobile ? 'translate-x-0' : '-translate-x-full lg:translate-x-0'
        }`}
      >
        {/* Sidebar Header */}
        <div className="p-2 bg-[#0a0d1f] flex flex-col gap-1.5 border-b border-[#26293c]">
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
        </div>

        {/* Navigation Body */}
        {viewMode === 'datacenter' ? (
          <nav className="flex-1 overflow-y-auto overflow-x-hidden p-2 font-mono text-xs space-y-2">
            {/* Datacenter Overview Quick Button */}
            <button
              type="button"
              onClick={() => {
                onSelectDatacenterSection?.('overview');
                onSelectDatacenterSetting?.(null as unknown as DatacenterSettingPageId);
              }}
              title={formatDatacenterRoute({ kind: 'section', section: 'overview' })}
              className={`w-full flex items-center gap-2 px-2.5 py-2 rounded transition-all text-left cursor-pointer ${
                !selectedDatacenterSetting && selectedDatacenterSection === 'overview'
                  ? 'bg-[#21263d] text-[#8cd6cb] font-bold border-l-2 border-[#8cd6cb] shadow-[inset_0_0_12px_rgba(140,214,203,0.08)]'
                  : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
              }`}
            >
              <span className="material-symbols-outlined text-[16px] shrink-0 text-[#8cd6cb]">
                dashboard
              </span>
              <div className="flex-1 min-w-0">
                <div className="truncate text-xs font-semibold">Datacenter Overview</div>
                <div className="text-[10px] text-[#8d909b] truncate">Mesh, health & quick actions</div>
              </div>
            </button>

            {/* Category: Settings — one page per individual setting (deep-linkable) */}
            <div className="space-y-1.5 pt-2 border-t border-[#26293c]/60" data-testid="datacenter-nav-category-settings">
              <div className="px-1 text-[10px] font-bold uppercase tracking-wider text-[#8d909b] flex items-center gap-1">
                <span className="material-symbols-outlined text-[13px] text-[#b0c9ff]">settings</span>
                <span>Settings</span>
              </div>

              {datacenterSettingCategories.map(category => (
                <div key={category.id} className="space-y-0.5" data-testid={`datacenter-nav-subcategory-${category.id}`}>
                  <div
                    className="px-2 py-1 flex items-center gap-1 text-[10px] font-bold uppercase tracking-wider text-[#b0c9ff]/90"
                    title={category.description}
                  >
                    <span className="w-1 h-1 rounded-full bg-[#b0c9ff]/60 shrink-0" />
                    <span className="truncate">{category.title}</span>
                  </div>

                  {category.pages.map(setting => {
                    const isActive = selectedDatacenterSetting === setting.id;
                    return (
                      <button
                        key={setting.id}
                        type="button"
                        onClick={() => onSelectDatacenterSetting?.(setting.id)}
                        title={`${setting.description} — ${formatDatacenterRoute({ kind: 'setting', setting: setting.id })}`}
                        data-testid={`datacenter-nav-setting-${setting.id}`}
                        className={`w-full flex items-center gap-2 pl-4 pr-2 py-1.5 rounded transition-all text-left cursor-pointer ${
                          isActive
                            ? 'bg-[#21263d] text-[#b0c9ff] font-bold border-l-2 border-[#b0c9ff] shadow-[inset_0_0_12px_rgba(176,201,255,0.08)]'
                            : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
                        }`}
                      >
                        <span className="material-symbols-outlined text-[14px] shrink-0 text-[#b0c9ff]">
                          {setting.icon}
                        </span>
                        <span className="flex-1 min-w-0 truncate text-[11px] font-semibold">
                          {setting.title}
                        </span>
                      </button>
                    );
                  })}
                </div>
              ))}
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
          <nav className="flex-1 overflow-y-auto overflow-x-hidden p-1.5 font-mono text-xs space-y-1">
            {/* Top Inventory Title */}
            <div className="px-2 pt-1 pb-0.5 text-[10px] font-bold uppercase tracking-wider text-[#8d909b] flex items-center justify-between">
              <span>Inventory</span>
              <div className="flex items-center gap-1.5">
                <span className="text-[10px] text-[#c3c6d2] font-normal">
                  {visibleNodes.length} {visibleNodes.length === 1 ? 'host' : 'hosts'}
                </span>
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
                  <span className="material-symbols-outlined text-[15px]">filter_list</span>
                </button>
              </div>
            </div>

            {filterOpen && (
              <div className="mx-1 mb-1 flex items-center gap-1 bg-[#171a2d] px-2 py-1 rounded border border-[#313447]">
                <span className="material-symbols-outlined text-[13px] text-[#8d909b]">search</span>
                <input
                  type="text"
                  autoFocus
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

            {/* SECTION 1: Tailscale */}
            <div className="space-y-0.5">
              {/* Tailnet root item <ts-shortname> */}
              <button
                type="button"
                onClick={() => setTailnetCollapsed(prev => !prev)}
                className="w-full flex items-center gap-1 px-1.5 py-1 text-[#c3c6d2] hover:text-[#dfe1fa] cursor-pointer rounded hover:bg-[#1b1e31] transition-colors"
              >
                <span className="material-symbols-outlined text-[14px]">
                  {tailnetCollapsed ? 'arrow_right' : 'arrow_drop_down'}
                </span>
                <span className="material-symbols-outlined text-[14px] text-[#b0c9ff]">cloud_queue</span>
                <span className="font-bold text-[#dfe1fa] truncate">
                  {inventoryTree.tailscale.shortName?.replace(/\.ts\.net$/i, '') || inventoryTree.tailscale.shortName}
                </span>
                <span className="ml-auto text-[#8cd6cb] text-[10px] font-bold">
                  ({tsOnlineCount}/{tsTotalCount} up)
                </span>
              </button>

              {/* Collapsible Tag Groups */}
              {!tailnetCollapsed && (
                <div className="pl-2 space-y-1">
                  {inventoryTree.tailscale.tags.map(tagGroup => {
                    const isCollapsed = Boolean(collapsedTags[tagGroup.tag]);
                    if (
                      filterText &&
                      tagGroup.nodes.length === 0 &&
                      !tagGroup.tag.toLowerCase().includes(filterText.toLowerCase().trim())
                    ) {
                      return null;
                    }
                    if (
                      selectedHostGroup !== 'all' &&
                      tagGroup.tag !== selectedHostGroup &&
                      tagGroup.nodes.length === 0
                    ) {
                      return null;
                    }

                    return (
                      <div key={tagGroup.tag} className="space-y-0.5">
                        <button
                          type="button"
                          data-testid={`tag-row-${tagGroup.tag}`}
                          onDragOver={event => {
                            event.preventDefault();
                            if (event.dataTransfer) event.dataTransfer.dropEffect = 'move';
                          }}
                          onDrop={event => {
                            event.preventDefault();
                            const nodeId = event.dataTransfer?.getData('text/plain');
                            // Dropping a host back on a tag row takes it out of any
                            // custom folder so it returns to its tag group.
                            if (nodeId && onMoveNodeToFolder) onMoveNodeToFolder(nodeId, null);
                          }}
                          onClick={() =>
                            setCollapsedTags(prev => ({
                              ...prev,
                              [tagGroup.tag]: !prev[tagGroup.tag],
                            }))
                          }
                          onContextMenu={event => {
                            event.preventDefault();
                            tagMenu.open(event, buildTagContextMenu(tagGroup.tag, tagGroup.chunk?.id ?? null, {
                              onDeleteChunk: onDeleteChunk ? (chunkId: string) => onDeleteChunk(chunkId) : undefined,
                              onCopy: handleNodeCopy,
                            }));
                          }}
                          className="w-full flex items-center gap-1 px-2 py-0.5 text-[#c3c6d2] hover:text-[#dfe1fa] cursor-pointer rounded hover:bg-[#1b1e31]"
                        >
                          <span className="material-symbols-outlined text-[14px]">
                            {isCollapsed ? 'arrow_right' : 'arrow_drop_down'}
                          </span>
                          <span className="material-symbols-outlined text-[14px] text-[#d9b9ff]">sell</span>
                          <span className="text-[#dfe1fa] font-medium truncate">{tagGroup.tag}</span>
                          <span className="ml-auto text-[#8cd6cb] font-bold">[{tagGroup.nodes.length}]</span>
                        </button>

                        {/* Indented Member Nodes */}
                        {!isCollapsed && (
                          <div className="pl-3 space-y-0.5">
                            {tagGroup.nodes.length === 0 ? (
                              <div className="px-2 py-1 text-[10px] text-[#8d909b] italic">
                                No discovered hosts
                              </div>
                            ) : (
                              tagGroup.nodes.map(node => renderNodeItem(node))
                            )}
                          </div>
                        )}
                      </div>
                    );
                  })}
                </div>
              )}
            </div>

            {/* SECTION 2: Non-Tailscale Infrastructure */}
            {(inventoryTree.nonTailscale.standalone.length > 0 ||
              inventoryTree.nonTailscale.folders.length > 0 ||
              onCreateFolder) && (
              <div className="pt-2 space-y-1">
                {/* Standalone Non-Tailscale Hosts */}
                {inventoryTree.nonTailscale.standalone.map(node => renderNodeItem(node))}

                {/* Custom Folders */}
                {inventoryTree.nonTailscale.folders.map(folderGroup => {
                  const isCollapsed = Boolean(collapsedFolders[folderGroup.folder.id]);
                  if (
                    filterText &&
                    folderGroup.nodes.length === 0 &&
                    !folderGroup.folder.name.toLowerCase().includes(filterText.toLowerCase().trim())
                  ) {
                    return null;
                  }

                  return (
                    <div
                      key={folderGroup.folder.id}
                      data-testid={`folder-row-${folderGroup.folder.id}`}
                      className="space-y-0.5"
                      onDragOver={event => {
                        event.preventDefault();
                        if (event.dataTransfer) event.dataTransfer.dropEffect = 'move';
                      }}
                      onDrop={event => {
                        event.preventDefault();
                        const nodeId = event.dataTransfer?.getData('text/plain');
                        if (nodeId && onMoveNodeToFolder) onMoveNodeToFolder(nodeId, folderGroup.folder.id);
                      }}
                    >
                      <div
                        className="group flex items-center justify-between px-1.5 py-0.5 text-[#c3c6d2] hover:text-[#dfe1fa] rounded hover:bg-[#1b1e31]"
                        onContextMenu={event => {
                          event.preventDefault();
                          folderMenu.open(event, buildFolderContextMenu(folderGroup.folder, {
                            onDelete: onDeleteFolder,
                            onCopy: handleNodeCopy,
                          }));
                        }}
                      >
                        <button
                          type="button"
                          onClick={() =>
                            setCollapsedFolders(prev => ({
                              ...prev,
                              [folderGroup.folder.id]: !prev[folderGroup.folder.id],
                            }))
                          }
                          className="flex items-center gap-1 min-w-0 flex-1 text-left cursor-pointer"
                        >
                          <span className="material-symbols-outlined text-[14px]">
                            {isCollapsed ? 'arrow_right' : 'arrow_drop_down'}
                          </span>
                          <span className="material-symbols-outlined text-[14px] text-[#ffd580]">folder</span>
                          <span className="text-[#dfe1fa] font-medium truncate">{folderGroup.folder.name}</span>
                          <span className="text-[#8cd6cb] font-bold text-[10px] ml-1">
                            [{folderGroup.nodes.length}]
                          </span>
                        </button>

                        {onDeleteFolder && (
                          <button
                            type="button"
                            onClick={() => onDeleteFolder(folderGroup.folder.id)}
                            className="opacity-0 group-hover:opacity-100 transition-opacity text-[#8d909b] hover:text-[#ffb4ab] p-0.5 cursor-pointer"
                            title={`Delete folder "${folderGroup.folder.name}"`}
                          >
                            <span className="material-symbols-outlined text-[13px]">delete</span>
                          </button>
                        )}
                      </div>

                      {/* Nested Host Nodes */}
                      {!isCollapsed && (
                        <div className="pl-3 space-y-0.5">
                          {folderGroup.nodes.length === 0 ? (
                            <div className="px-2 py-1 text-[10px] text-[#8d909b] italic">
                              Folder is empty
                            </div>
                          ) : (
                            folderGroup.nodes.map(node => renderNodeItem(node))
                          )}
                        </div>
                      )}
                    </div>
                  );
                })}

                {/* "+ New Folder" Action Button / Inline Prompt */}
                {onCreateFolder && (
                  <div className="pt-1 px-1">
                    {isCreatingFolder ? (
                      <form
                        onSubmit={e => {
                          e.preventDefault();
                          if (newFolderName.trim()) {
                            onCreateFolder(newFolderName.trim());
                            setNewFolderName('');
                            setIsCreatingFolder(false);
                          }
                        }}
                        className="flex items-center gap-1 bg-[#121528] p-1 rounded border border-[#26293c]"
                      >
                        <span className="material-symbols-outlined text-[14px] text-[#ffd580]">folder</span>
                        <input
                          type="text"
                          autoFocus
                          value={newFolderName}
                          onChange={e => setNewFolderName(e.target.value)}
                          placeholder="Folder name..."
                          className="bg-transparent text-[#dfe1fa] text-[11px] px-1 py-0.5 outline-none flex-1 font-mono min-w-0"
                        />
                        <button
                          type="submit"
                          disabled={!newFolderName.trim()}
                          className="text-[#8cd6cb] hover:text-[#b0c9ff] text-[11px] font-bold px-1 cursor-pointer disabled:opacity-40"
                        >
                          Add
                        </button>
                        <button
                          type="button"
                          onClick={() => {
                            setIsCreatingFolder(false);
                            setNewFolderName('');
                          }}
                          className="text-[#8d909b] hover:text-[#dfe1fa] text-[11px] px-1 cursor-pointer"
                        >
                          Cancel
                        </button>
                      </form>
                    ) : (
                      <button
                        type="button"
                        onClick={() => setIsCreatingFolder(true)}
                        className="flex items-center gap-1 text-[11px] text-[#8d909b] hover:text-[#dfe1fa] cursor-pointer py-1 px-1.5 rounded hover:bg-[#1b1e31] transition-colors w-full"
                      >
                        <span className="material-symbols-outlined text-[14px] text-[#8cd6cb]">create_new_folder</span>
                        <span>+ New Folder</span>
                      </button>
                    )}
                  </div>
                )}
              </div>
            )}
          </nav>
        )}

        {/* Footer */}
        {viewMode === 'datacenter' ? null : (
          <div className="p-2 bg-[#0a0d1f] flex items-center justify-between text-[#c3c6d2] font-mono text-xs border-t border-[#26293c]">
            <span className="flex items-center gap-1">
              <span className="material-symbols-outlined text-[14px] text-[#8cd6cb]">network_ping</span>
              <span>RTT: {selectedNode?.rttMs || '1.8ms'}</span>
            </span>
          </div>
        )}
      </aside>
      {nodeMenu.menu}
      {tagMenu.menu}
      {folderMenu.menu}
    </>
  );
};
