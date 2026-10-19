import React, { useState, useEffect } from 'react';
import type { NodeItem } from '../types/index.ts';

export interface NodePluginItem {
  id: string;
  kind: string;
  name: string;
  label: string;
  icon: string;
  section: string;
  order: number;
  detection: {
    detected: boolean;
    status: string;
    details?: string;
    error?: string;
  };
}

export interface CockpitSubSidebarProps {
  node: NodeItem;
  activeItem: string;
  onSelectItem: (item: string) => void;
  onEditNode?: () => void;
  className?: string;
  plugins?: NodePluginItem[];
  storageEngine?: any; // Kept for backwards compatibility
}

// Section display headings and default ordering
const SECTION_ORDER = [
  'system',
  'telemetry',
  'logs',
  'package',
  'storage',
  'share',
  'mount',
  'network',
  'firewall',
  'container',
  'vm',
  'backup',
  'disk',
];

const SECTION_TITLES: Record<string, string> = {
  system: 'SYSTEM',
  telemetry: 'SYSTEM',
  logs: 'SYSTEM',
  package: 'SYSTEM',
  storage: 'STORAGE & FS',
  share: 'SHARES',
  mount: 'MOUNTS',
  network: 'NETWORKING',
  firewall: 'NETWORKING',
  container: 'CONTAINERS & VMS',
  vm: 'CONTAINERS & VMS',
  backup: 'BACKUPS & SYNC',
  disk: 'STORAGE & FS',
};

export const CockpitSubSidebar: React.FC<CockpitSubSidebarProps> = ({
  node,
  activeItem,
  onSelectItem,
  onEditNode,
  className = '',
  plugins: propsPlugins,
}) => {
  const [internalPlugins, setInternalPlugins] = useState<NodePluginItem[] | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (propsPlugins !== undefined) {
      return;
    }
    let active = true;
    setLoading(true);
    fetch(`/api/nodes/${encodeURIComponent(node.id)}/plugins`)
      .then(res => res.json())
      .then((data: NodePluginItem[]) => {
        if (!active) return;
        if (Array.isArray(data)) {
          setInternalPlugins(data);
        } else {
          setInternalPlugins([]);
        }
      })
      .catch(() => {
        if (active) setInternalPlugins([]);
      })
      .finally(() => {
        if (active) setLoading(false);
      });

    return () => {
      active = false;
    };
  }, [node.id, propsPlugins]);

  const activePlugins = propsPlugins !== undefined ? propsPlugins : internalPlugins || [];

  // Group plugins by Section Title
  const groupedSections: Record<string, NodePluginItem[]> = {};

  for (const p of activePlugins) {
    if (!p.detection?.detected) continue;
    const kindKey = (p.section || p.kind || '').toLowerCase();
    const sectionTitle = SECTION_TITLES[kindKey] || kindKey.toUpperCase() || 'OTHER';
    if (!groupedSections[sectionTitle]) {
      groupedSections[sectionTitle] = [];
    }
    groupedSections[sectionTitle].push(p);
  }

  // Sort plugins within each section by their declared Order
  for (const key of Object.keys(groupedSections)) {
    groupedSections[key].sort((a, b) => (a.order || 0) - (b.order || 0));
  }

  // Sections order
  const distinctSections = Object.keys(groupedSections).sort((a, b) => {
    const aOrder = SECTION_ORDER.findIndex(k => SECTION_TITLES[k] === a);
    const bOrder = SECTION_ORDER.findIndex(k => SECTION_TITLES[k] === b);
    return (aOrder === -1 ? 99 : aOrder) - (bOrder === -1 ? 99 : bOrder);
  });

  return (
    <aside className={`flex flex-col gap-2 w-full overflow-x-hidden scrollbar-thin ${className}`.trim()}>
      {/* Cockpit Tree Groupings */}
      <div className="bg-[#171a2d] rounded p-2 flex flex-col gap-2 border border-[#26293c]">
        {/* HARDCODED BASE SYSTEM GROUP: Overview & Terminal */}
        <div>
          <span className="font-mono text-[10px] uppercase tracking-wider text-[#8d909b] px-1 font-bold">
            SYSTEM
          </span>
          <nav className="mt-1 flex flex-col gap-0.5">
            <button
              type="button"
              onClick={() => onSelectItem('overview')}
              className={`w-full flex items-center justify-between px-2 py-1 rounded font-mono text-xs cursor-pointer transition-colors ${
                activeItem === 'overview'
                  ? 'bg-[#26293c] text-[#b0c9ff] font-bold border-l-2 border-[#b0c9ff]'
                  : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
              }`}
            >
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[14px]">insights</span>
                <span>Overview</span>
              </span>
              <span className="w-1.5 h-1.5 rounded-full bg-[#8cd6cb]"></span>
            </button>

            <button
              type="button"
              onClick={() => onSelectItem('terminal')}
              className={`w-full flex items-center justify-between px-2 py-1 rounded font-mono text-xs cursor-pointer transition-colors ${
                activeItem === 'terminal'
                  ? 'bg-[#26293c] text-[#b0c9ff] font-bold border-l-2 border-[#b0c9ff]'
                  : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
              }`}
            >
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[14px]">terminal</span>
                <span>Terminal</span>
              </span>
              <span className="font-mono text-[10px] text-[#8cd6cb]">SSH</span>
            </button>
          </nav>
        </div>

        {/* DYNAMIC PLUGIN SECTIONS */}
        {distinctSections.map(sectionTitle => {
          const plugins = groupedSections[sectionTitle];
          if (!plugins || plugins.length === 0) return null;

          return (
            <div key={sectionTitle}>
              <span className="font-mono text-[10px] uppercase tracking-wider text-[#8d909b] px-1 font-bold">
                {sectionTitle}
              </span>
              <nav className="mt-1 flex flex-col gap-0.5">
                {plugins.map(p => {
                  // Tab selection ID can match either p.name or p.id for compatibility
                  const isSelected =
                    activeItem === p.id ||
                    activeItem === p.name ||
                    (p.name === 'zfs' && (activeItem === 'storage' || activeItem === 'zfs')) ||
                    (p.name === 'interfaces' && activeItem === 'network');

                  return (
                    <button
                      key={p.id}
                      type="button"
                      onClick={() => onSelectItem(p.id)}
                      className={`w-full flex items-center justify-between px-2 py-1 rounded font-mono text-xs cursor-pointer transition-colors ${
                        isSelected
                          ? 'bg-[#26293c] text-[#b0c9ff] font-bold border-l-2 border-[#b0c9ff]'
                          : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
                      }`}
                    >
                      <span className="flex items-center gap-1.5 truncate pr-1">
                        <span className="material-symbols-outlined text-[14px]">
                          {p.icon || 'extension'}
                        </span>
                        <span className="truncate">{p.label || p.name}</span>
                      </span>
                      {p.detection?.status && (
                        <span className="font-mono text-[10px] text-[#8cd6cb] shrink-0">
                          {p.detection.status}
                        </span>
                      )}
                    </button>
                  );
                })}
              </nav>
            </div>
          );
        })}

        {/* PINNED MODULES TAB AT BOTTOM */}
        <div className="pt-1 border-t border-[#26293c]/60">
          <nav className="flex flex-col gap-0.5">
            <button
              type="button"
              onClick={() => onSelectItem('modules')}
              className={`w-full flex items-center justify-between px-2 py-1 rounded font-mono text-xs cursor-pointer transition-colors ${
                activeItem === 'modules'
                  ? 'bg-[#26293c] text-[#b0c9ff] font-bold border-l-2 border-[#b0c9ff]'
                  : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
              }`}
            >
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[14px] text-[#ffd97d]">
                  extension
                </span>
                <span className="font-semibold text-[#ffd97d]">Modules</span>
              </span>
              <span className="font-mono text-[10px] text-[#8d909b]">ALL</span>
            </button>
          </nav>
        </div>
      </div>

      {/* Quick Node Spec Snapshot Card */}
      <div className="bg-[#171a2d] rounded p-2.5 flex flex-col gap-1.5 border border-[#26293c]">
        <div className="flex items-center justify-between">
          <span className="font-mono text-[10px] uppercase tracking-wider text-[#8d909b] font-bold">
            CHASSIS &amp; MOTHERBOARD
          </span>
          {onEditNode && (
            <button
              type="button"
              onClick={onEditNode}
              className="text-[10px] font-mono text-[#b0c9ff] hover:underline cursor-pointer"
            >
              Edit Specs
            </button>
          )}
        </div>
        <div className="flex items-center gap-2">
          <span className="material-symbols-outlined text-[#b0c9ff] text-[20px]">developer_board</span>
          <div className="flex flex-col min-w-0">
            <span className="font-mono text-xs text-[#dfe1fa] font-semibold truncate">
              {node.specs.motherboard}
            </span>
            <span className="font-mono text-[10px] text-[#8d909b]">BIOS: {node.specs.bios}</span>
          </div>
        </div>
        <div className="grid grid-cols-2 gap-1 mt-1 pt-1 bg-[#0a0d1f]/60 p-1.5 rounded font-mono text-[10px] border border-[#26293c]/50">
          <div>
            <span className="text-[#8d909b]">IPMI/BMC:</span>{' '}
            <span className="text-[#8cd6cb]">{node.specs.ipmi}</span>
          </div>
          <div>
            <span className="text-[#8d909b]">ECC:</span>{' '}
            <span className="text-[#8cd6cb]">{node.specs.ecc}</span>
          </div>
          <div>
            <span className="text-[#8d909b]">SecureBoot:</span>{' '}
            <span className="text-[#dfe1fa]">{node.specs.secureBoot}</span>
          </div>
          <div>
            <span className="text-[#8d909b]">IOMMU:</span>{' '}
            <span className="text-[#8cd6cb]">{node.specs.iommu}</span>
          </div>
        </div>
      </div>
    </aside>
  );
};
