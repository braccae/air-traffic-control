import React, { useState, useEffect } from 'react';
import type { NodeItem } from '../types/index.ts';
import { resolvePluginIcon } from '../utils/pluginIcons.ts';

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
  'database',
  'storage',
  'disk',
  'share',
  'mount',
  'network',
  'firewall',
  'container',
  'vm',
  'backup',
];

const SECTION_TITLES: Record<string, string> = {
  system: 'SYSTEM',
  telemetry: 'OBSERVABILITY',
  logs: 'OBSERVABILITY',
  package: 'PACKAGES & UPDATES',
  database: 'DATABASES & NOSQL',
  storage: 'STORAGE & FS',
  disk: 'STORAGE & FS',
  share: 'SHARES',
  mount: 'MOUNTS',
  network: 'NETWORKING',
  firewall: 'NETWORKING',
  container: 'CONTAINERS & VMS',
  vm: 'CONTAINERS & VMS',
  backup: 'BACKUPS & SYNC',
};

// Shorten plugin labels for cleaner sidebar display
function shortenPluginLabel(label: string, name: string, section: string): string {
  // Container plugins
  if (name === 'podman') return 'Podman';
  if (name === 'docker') return 'Docker';
  if (name === 'lxc') return 'LXC';

  // Storage plugins
  if (section === 'storage' && name === 'zfs') return 'ZFS';
  if (section === 'storage' && name === 'btrfs') return 'Btrfs';
  if (section === 'storage' && name === 'lvm') return 'LVM';

  // Database plugins - show just the name
  if (section === 'database' && name === 'sql') return 'SQL';
  if (section === 'database' && name === 'nosql') return 'NoSQL';

  // System plugins
  if (name === 'service') return 'Services';
  if (name === 'journald') return 'Logs';
  if (name === 'glances') return 'Glances';

  // Network plugins
  if (name === 'interfaces') return 'Interfaces';
  if (name === 'tailscale') return 'Tailscale';

  // Firewall
  if (name === 'firewalld') return 'Firewall';

  // Default: return as-is
  return label;
}

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

  // Group plugins by Section Title (excluding raw connection and elevation plugins)
  const groupedSections: Record<string, NodePluginItem[]> = {};

  // Package manager precedence: bootc > rpmostree/ostree > traditional (dnf/rpm/pacman/apt/apk)
  const packageOrder = ['bootc', 'rpmostree', 'ostree', 'dnf', 'rpm', 'pacman', 'apt', 'apk'];
  let chosenPackagePlugin: NodePluginItem | null = null;
  const nonPackagePlugins: NodePluginItem[] = [];

  for (const p of activePlugins) {
    if (!p.detection?.detected) continue;
    // Connection transports and elevation methods belong in Modules / Edit Node modal
    if (p.kind === 'connection' || p.kind === 'elevation') continue;
    // Hide redundant/hidden plugins per UI cleanup (marked with red X)
    const hiddenNames = [
      'system', 'logs', 'telemetry', 'journald', 'share/nfs', 'share/smb', 'mount/rclone',
      'firewalld', 'podman', 'docker', 'sql', 'nosql',
    ];
    if (hiddenNames.includes(p.name.toLowerCase()) || hiddenNames.includes((p.kind || '').toLowerCase())) continue;

    if (p.kind === 'package') {
      if (!chosenPackagePlugin) {
        chosenPackagePlugin = p;
      } else {
        const currentIdx = packageOrder.indexOf(chosenPackagePlugin.name.toLowerCase());
        const candidateIdx = packageOrder.indexOf(p.name.toLowerCase());
        const effectiveCurrent = currentIdx === -1 ? 999 : currentIdx;
        const effectiveCandidate = candidateIdx === -1 ? 999 : candidateIdx;
        if (effectiveCandidate < effectiveCurrent) {
          chosenPackagePlugin = p;
        }
      }
    } else {
      nonPackagePlugins.push(p);
    }
  }

  const filteredPlugins = chosenPackagePlugin
    ? [...nonPackagePlugins, chosenPackagePlugin]
    : nonPackagePlugins;

  for (const p of filteredPlugins) {
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

  const systemPlugins = groupedSections['SYSTEM'] || [];

  // Sections order (excluding SYSTEM since it is merged into the top base SYSTEM group)
  const distinctSections = Object.keys(groupedSections)
    .filter(sectionTitle => sectionTitle !== 'SYSTEM')
    .sort((a, b) => {
      const aOrder = SECTION_ORDER.findIndex(k => SECTION_TITLES[k] === a);
      const bOrder = SECTION_ORDER.findIndex(k => SECTION_TITLES[k] === b);
      return (aOrder === -1 ? 99 : aOrder) - (bOrder === -1 ? 99 : bOrder);
    });

  return (
    <aside className={`flex flex-col gap-2 w-full overflow-x-hidden scrollbar-thin ${className}`.trim()}>
      {/* Cockpit Tree Groupings */}
      <div className="bg-[#171a2d] rounded p-1.5 flex flex-col gap-1.5 border border-[#26293c]">
        {/* HARDCODED BASE SYSTEM GROUP: Overview + detected SYSTEM plugins */}
        <div>
          <span className="font-mono text-[9px] uppercase tracking-wider text-[#8d909b] px-1 font-bold">
            SYSTEM
          </span>
          <nav className="mt-0.5 flex flex-col gap-0.5">
            <button
              type="button"
              onClick={() => onSelectItem('overview')}
              className={`w-full flex items-center justify-between px-2 py-1 rounded font-mono text-[11px] leading-tight cursor-pointer transition-colors ${
                activeItem === 'overview'
                  ? 'bg-[#21263d] text-[#b0c9ff] font-bold border-l-2 border-[#b0c9ff] shadow-[inset_0_0_12px_rgba(176,201,255,0.08)]'
                  : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
              }`}
            >
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[13px] text-[#8cd6cb]">insights</span>
                <span>Overview</span>
              </span>
              <span className="w-1.5 h-1.5 rounded-full bg-[#8cd6cb]"></span>
            </button>

            {systemPlugins.map(p => {
              const isSelected = activeItem === p.id || activeItem === p.name;
              return (
                <button
                  key={p.id}
                  type="button"
                  onClick={() => onSelectItem(p.id)}
                  className={`w-full flex items-center justify-between px-2 py-1 rounded font-mono text-[11px] leading-tight cursor-pointer transition-colors ${
                    isSelected
                      ? 'bg-[#21263d] text-[#b0c9ff] font-bold border-l-2 border-[#b0c9ff] shadow-[inset_0_0_12px_rgba(176,201,255,0.08)]'
                      : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
                  }`}
                >
                  <span className="flex items-center gap-1.5 truncate pr-1">
                    <span className="material-symbols-outlined text-[13px] text-[#8d909b] shrink-0">
                      {resolvePluginIcon(p.icon)}
                    </span>
                    <span className="truncate">{shortenPluginLabel(p.label || p.name, p.name, p.section || p.kind)}</span>
                  </span>
                </button>
              );
            })}
          </nav>
        </div>

        {/* DYNAMIC PLUGIN SECTIONS */}
        {distinctSections.map(sectionTitle => {
          const plugins = groupedSections[sectionTitle];
          if (!plugins || plugins.length === 0) return null;

          return (
            <div key={sectionTitle}>
              <span className="font-mono text-[9px] uppercase tracking-wider text-[#8d909b] px-1 font-bold">
                {sectionTitle}
              </span>
              <nav className="mt-0.5 flex flex-col gap-0.5">
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
                      className={`w-full flex items-center justify-between px-2 py-1 rounded font-mono text-[11px] leading-tight cursor-pointer transition-colors ${
                        isSelected
                          ? 'bg-[#21263d] text-[#b0c9ff] font-bold border-l-2 border-[#b0c9ff] shadow-[inset_0_0_12px_rgba(176,201,255,0.08)]'
                          : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
                      }`}
                    >
                      <span className="flex items-center gap-1.5 truncate pr-1">
                        <span className="material-symbols-outlined text-[13px] text-[#8d909b] shrink-0">
                          {resolvePluginIcon(p.icon)}
                        </span>
                        <span className="truncate">{shortenPluginLabel(p.label || p.name, p.name, p.section || p.kind)}</span>
                      </span>
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
              className={`w-full flex items-center justify-between px-2 py-1 rounded font-mono text-[11px] leading-tight cursor-pointer transition-colors ${
                activeItem === 'modules'
                  ? 'bg-[#21263d] text-[#ffd97d] font-bold border-l-2 border-[#ffd97d] shadow-[inset_0_0_12px_rgba(255,217,125,0.08)]'
                  : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
              }`}
            >
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[13px] text-[#ffd97d]">
                  extension
                </span>
                <span className="font-semibold text-[#ffd97d]">Modules</span>
              </span>
              <span className="font-mono text-[9px] text-[#8d909b] bg-[#121528] px-1 py-0.2 rounded border border-[#26293c]">ALL</span>
            </button>
          </nav>
        </div>
      </div>

      {/* Quick Node Spec Snapshot Card */}
      <div className="bg-[#171a2d] rounded p-2 flex flex-col gap-1 border border-[#26293c]">
        <div className="flex items-center justify-between">
          <span className="font-mono text-[9px] uppercase tracking-wider text-[#8d909b] font-bold">
            CHASSIS &amp; MOTHERBOARD
          </span>
          {onEditNode && (
            <button
              type="button"
              onClick={onEditNode}
              className="text-[9px] font-mono text-[#b0c9ff] hover:underline cursor-pointer"
            >
              Edit Specs
            </button>
          )}
        </div>
        <div className="flex items-center gap-1.5">
          <span className="material-symbols-outlined text-[#b0c9ff] text-[16px]">developer_board</span>
          <div className="flex flex-col min-w-0">
            <span className="font-mono text-[11px] text-[#dfe1fa] font-semibold truncate">
              {node.specs?.motherboard || 'Standard Server Platform'}
            </span>
            <span className="font-mono text-[9px] text-[#8d909b]">BIOS: {node.specs?.bios || 'UEFI'}</span>
          </div>
        </div>
        <div className="grid grid-cols-2 gap-1 mt-0.5 bg-[#0a0d1f]/60 p-1.5 rounded font-mono text-[9px] border border-[#26293c]/50">
          <div>
            <span className="text-[#8d909b]">IPMI/BMC:</span>{' '}
            <span className="text-[#8cd6cb]">{node.specs?.ipmi || 'N/A'}</span>
          </div>
          <div>
            <span className="text-[#8d909b]">ECC:</span>{' '}
            <span className="text-[#8cd6cb]">{node.specs?.ecc || 'N/A'}</span>
          </div>
          <div>
            <span className="text-[#8d909b]">SecureBoot:</span>{' '}
            <span className="text-[#dfe1fa]">{node.specs?.secureBoot || 'N/A'}</span>
          </div>
          <div>
            <span className="text-[#8d909b]">IOMMU:</span>{' '}
            <span className="text-[#8cd6cb]">{node.specs?.iommu || 'N/A'}</span>
          </div>
        </div>
      </div>
    </aside>
  );
};
