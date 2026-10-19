import React from 'react';
import type { NodeItem } from '../types/index.ts';

interface CockpitSubSidebarProps {
  node: NodeItem;
  activeItem: string;
  onSelectItem: (item: string) => void;
  onEditNode?: () => void;
  className?: string;
}

export const CockpitSubSidebar: React.FC<CockpitSubSidebarProps> = ({
  node,
  activeItem,
  onSelectItem,
  onEditNode,
  className = '',
}) => {
  return (
    <aside className={`flex flex-col gap-2 w-full scrollbar-thin ${className}`.trim()}>
      {/* Cockpit Tree Groupings */}
      <div className="bg-[#171a2d] rounded p-2 flex flex-col gap-2 border border-[#26293c]">
        {/* SYSTEM */}
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
                  ? 'bg-[#26293c] text-[#b0c9ff] font-bold'
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
              onClick={() => onSelectItem('logs')}
              className={`w-full flex items-center justify-between px-2 py-1 rounded font-mono text-xs cursor-pointer transition-colors ${
                activeItem === 'logs'
                  ? 'bg-[#26293c] text-[#b0c9ff] font-bold'
                  : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
              }`}
            >
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[14px]">article</span>
                <span>System Logs</span>
              </span>
              <span className="font-mono text-[10px] text-[#8d909b]">journald</span>
            </button>

            <button
              type="button"
              onClick={() => onSelectItem('metrics')}
              className={`w-full flex items-center justify-between px-2 py-1 rounded font-mono text-xs cursor-pointer transition-colors ${
                activeItem === 'metrics'
                  ? 'bg-[#26293c] text-[#b0c9ff] font-bold'
                  : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
              }`}
            >
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[14px]">query_stats</span>
                <span>Metrics &amp; History</span>
              </span>
              <span className="font-mono text-[10px] text-[#8cd6cb]">PCP/Glances</span>
            </button>

            <button
              type="button"
              onClick={() => onSelectItem('updates')}
              className={`w-full flex items-center justify-between px-2 py-1 rounded font-mono text-xs cursor-pointer transition-colors ${
                activeItem === 'updates'
                  ? 'bg-[#26293c] text-[#b0c9ff] font-bold'
                  : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
              }`}
            >
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[14px]">system_update</span>
                <span>Software Updates</span>
              </span>
              <span className="font-mono text-[10px] text-[#8cd6cb]">pkg/ostree</span>
            </button>
          </nav>
        </div>

        {/* STORAGE & FS */}
        <div>
          <span className="font-mono text-[10px] uppercase tracking-wider text-[#8d909b] px-1 font-bold">
            STORAGE &amp; FS
          </span>
          <nav className="mt-1 flex flex-col gap-0.5">
            <button
              type="button"
              onClick={() => onSelectItem('zfs')}
              className={`w-full flex items-center justify-between px-2 py-1 rounded font-mono text-xs cursor-pointer transition-colors ${
                activeItem === 'zfs'
                  ? 'bg-[#26293c] text-[#b0c9ff] font-bold border-l-2 border-[#b0c9ff]'
                  : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
              }`}
            >
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[14px]">inventory_2</span>
                <span>ZFS Pools</span>
              </span>
              <span className="font-mono text-[10px] text-[#8cd6cb]">ONLINE</span>
            </button>

            <button
              type="button"
              onClick={() => onSelectItem('shares')}
              className={`w-full flex items-center justify-between px-2 py-1 rounded font-mono text-xs cursor-pointer transition-colors ${
                activeItem === 'shares'
                  ? 'bg-[#26293c] text-[#b0c9ff] font-bold border-l-2 border-[#b0c9ff]'
                  : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
              }`}
            >
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[14px]">share</span>
                <span>NFS &amp; SMB Shares</span>
              </span>
              <span className="font-mono text-[10px] text-[#8d909b]">Active</span>
            </button>

            <button
              type="button"
              onClick={() => onSelectItem('smart')}
              className={`w-full flex items-center justify-between px-2 py-1 rounded font-mono text-xs cursor-pointer transition-colors ${
                activeItem === 'smart'
                  ? 'bg-[#26293c] text-[#b0c9ff] font-bold border-l-2 border-[#b0c9ff]'
                  : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
              }`}
            >
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[14px]">disc_full</span>
                <span>Disks &amp; SMART</span>
              </span>
              <span className="font-mono text-[10px] text-[#8cd6cb]">PASSED</span>
            </button>
          </nav>
        </div>

        {/* NETWORKING */}
        <div>
          <span className="font-mono text-[10px] uppercase tracking-wider text-[#8d909b] px-1 font-bold">
            NETWORKING
          </span>
          <nav className="mt-1 flex flex-col gap-0.5">
            <button
              type="button"
              onClick={() => onSelectItem('interfaces')}
              className={`w-full flex items-center justify-between px-2 py-1 rounded font-mono text-xs cursor-pointer transition-colors ${
                activeItem === 'interfaces'
                  ? 'bg-[#26293c] text-[#b0c9ff] font-bold border-l-2 border-[#b0c9ff]'
                  : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
              }`}
            >
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[14px]">cable</span>
                <span>Network Interfaces</span>
              </span>
              <span className="font-mono text-[10px] text-[#b0c9ff]">Active</span>
            </button>

            <button
              type="button"
              onClick={() => onSelectItem('firewall')}
              className={`w-full flex items-center justify-between px-2 py-1 rounded font-mono text-xs cursor-pointer transition-colors ${
                activeItem === 'firewall'
                  ? 'bg-[#26293c] text-[#b0c9ff] font-bold border-l-2 border-[#b0c9ff]'
                  : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
              }`}
            >
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[14px]">security</span>
                <span>Firewall (nftables)</span>
              </span>
              <span className="font-mono text-[10px] text-[#8cd6cb]">ENFORCED</span>
            </button>

            <button
              type="button"
              onClick={() => onSelectItem('tailscale')}
              className={`w-full flex items-center justify-between px-2 py-1 rounded font-mono text-xs cursor-pointer transition-colors ${
                activeItem === 'tailscale'
                  ? 'bg-[#26293c] text-[#b0c9ff] font-bold border-l-2 border-[#b0c9ff]'
                  : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
              }`}
            >
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[14px] text-[#8cd6cb]">vpn_lock</span>
                <span>Tailscale VPN</span>
              </span>
              <span className="font-mono text-[10px] text-[#8cd6cb] font-bold">MESH</span>
            </button>
          </nav>
        </div>

        {/* CONTAINERS & VMS */}
        <div>
          <span className="font-mono text-[10px] uppercase tracking-wider text-[#8d909b] px-1 font-bold">
            CONTAINERS &amp; VMS
          </span>
          <nav className="mt-1 flex flex-col gap-0.5">
            <button
              type="button"
              onClick={() => onSelectItem('pods')}
              className={`w-full flex items-center justify-between px-2 py-1 rounded font-mono text-xs cursor-pointer transition-colors ${
                activeItem === 'pods' || activeItem === 'containers'
                  ? 'bg-[#26293c] text-[#b0c9ff] font-bold border-l-2 border-[#b0c9ff]'
                  : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
              }`}
            >
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[14px] text-[#d9b9ff]">token</span>
                <span>Podman &amp; Pods</span>
              </span>
              <span className="font-mono text-[10px] text-[#d9b9ff] font-bold">Containers</span>
            </button>

            <button
              type="button"
              onClick={() => onSelectItem('vms')}
              className={`w-full flex items-center justify-between px-2 py-1 rounded font-mono text-xs cursor-pointer transition-colors ${
                activeItem === 'vms'
                  ? 'bg-[#26293c] text-[#b0c9ff] font-bold border-l-2 border-[#b0c9ff]'
                  : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
              }`}
            >
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[14px] text-[#b0c9ff]">laptop_mac</span>
                <span>Virtual Machines</span>
              </span>
              <span className="font-mono text-[10px] text-[#8cd6cb]">KVM/QEMU</span>
            </button>
          </nav>
        </div>

        {/* BACKUPS & SYNC */}
        <div>
          <span className="font-mono text-[10px] uppercase tracking-wider text-[#8d909b] px-1 font-bold">
            BACKUPS &amp; SYNC
          </span>
          <nav className="mt-1 flex flex-col gap-0.5">
            <button
              type="button"
              onClick={() => onSelectItem('restic')}
              className={`w-full flex items-center justify-between px-2 py-1 rounded font-mono text-xs cursor-pointer transition-colors ${
                activeItem === 'restic'
                  ? 'bg-[#26293c] text-[#b0c9ff] font-bold border-l-2 border-[#b0c9ff]'
                  : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
              }`}
            >
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[14px] text-[#8cd6cb]">cloud_done</span>
                <span>Restic Snapshots</span>
              </span>
              <span className="font-mono text-[10px] text-[#8cd6cb]">GOLD</span>
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
