import React from 'react';
import type { NodeItem } from '../types/index.ts';

interface CockpitSubSidebarProps {
  node: NodeItem;
  activeItem: string;
  onSelectItem: (item: string) => void;
}

export const CockpitSubSidebar: React.FC<CockpitSubSidebarProps> = ({
  node,
  activeItem,
  onSelectItem,
}) => {
  return (
    <aside className="col-span-12 lg:col-span-3 xl:col-span-2 flex flex-col gap-2">
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
              <span className="font-mono text-[10px] text-[#8d909b]">dmesg</span>
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
              className="w-full flex items-center justify-between px-2 py-1 rounded text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa] font-mono text-xs cursor-pointer"
            >
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[14px]">inventory_2</span>
                <span>ZFS Pools (tank0)</span>
              </span>
              <span className="font-mono text-[10px] text-[#8cd6cb]">ONLINE</span>
            </button>

            <button
              type="button"
              onClick={() => onSelectItem('shares')}
              className="w-full flex items-center justify-between px-2 py-1 rounded text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa] font-mono text-xs cursor-pointer"
            >
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[14px]">share</span>
                <span>NFS &amp; SMB Shares</span>
              </span>
              <span className="font-mono text-[10px] text-[#8d909b]">6 export</span>
            </button>

            <button
              type="button"
              onClick={() => onSelectItem('smart')}
              className="w-full flex items-center justify-between px-2 py-1 rounded text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa] font-mono text-xs cursor-pointer"
            >
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[14px]">disc_full</span>
                <span>Disks &amp; SMART</span>
              </span>
              <span className="font-mono text-[10px] text-[#8cd6cb]">8/8 PASSED</span>
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
              className="w-full flex items-center justify-between px-2 py-1 rounded text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa] font-mono text-xs cursor-pointer"
            >
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[14px]">cable</span>
                <span>Interfaces (bond0)</span>
              </span>
              <span className="font-mono text-[10px] text-[#b0c9ff]">10GbE</span>
            </button>

            <button
              type="button"
              onClick={() => onSelectItem('firewall')}
              className="w-full flex items-center justify-between px-2 py-1 rounded text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa] font-mono text-xs cursor-pointer"
            >
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[14px]">security</span>
                <span>Firewall (nftables)</span>
              </span>
              <span className="font-mono text-[10px] text-[#8d909b]">ENFORCED</span>
            </button>

            <button
              type="button"
              onClick={() => onSelectItem('tailscale')}
              className="w-full flex items-center justify-between px-2 py-1 rounded bg-[#313447]/40 text-[#dfe1fa] font-mono text-xs cursor-pointer"
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
              className="w-full flex items-center justify-between px-2 py-1 rounded text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa] font-mono text-xs cursor-pointer"
            >
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[14px] text-[#d9b9ff]">token</span>
                <span>Podman Pods</span>
              </span>
              <span className="font-mono text-[10px] text-[#d9b9ff] font-bold">3 pods</span>
            </button>

            <button
              type="button"
              onClick={() => onSelectItem('containers')}
              className="w-full flex items-center justify-between px-2 py-1 rounded text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa] font-mono text-xs cursor-pointer"
            >
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[14px] text-[#b0c9ff]">deployed_code</span>
                <span>Containers</span>
              </span>
              <span className="font-mono text-[10px] text-[#8cd6cb]">11 Up / 1 Stp</span>
            </button>

            <button
              type="button"
              onClick={() => onSelectItem('vms')}
              className="w-full flex items-center justify-between px-2 py-1 rounded text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa] font-mono text-xs cursor-pointer"
            >
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[14px] text-[#b0c9ff]">qr_code_2</span>
                <span>Virtual Machines</span>
              </span>
              <span className="font-mono text-[10px] text-[#8d909b]">2 KVM</span>
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
              className="w-full flex items-center justify-between px-2 py-1 rounded text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa] font-mono text-xs cursor-pointer"
            >
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[14px] text-[#8cd6cb]">cloud_done</span>
                <span>Restic Snapshots</span>
              </span>
              <span className="font-mono text-[10px] text-[#8cd6cb]">GOLD</span>
            </button>

            <button
              type="button"
              onClick={() => onSelectItem('policy')}
              className="w-full flex items-center justify-between px-2 py-1 rounded text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa] font-mono text-xs cursor-pointer"
            >
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[14px]">policy</span>
                <span>Datacenter Policy</span>
              </span>
              <span className="font-mono text-[10px] text-[#8d909b]">Immutable</span>
            </button>
          </nav>
        </div>
      </div>

      {/* Quick Node Spec Snapshot Card */}
      <div className="bg-[#171a2d] rounded p-2.5 flex flex-col gap-1.5 border border-[#26293c]">
        <span className="font-mono text-[10px] uppercase tracking-wider text-[#8d909b] font-bold">
          CHASSIS &amp; MOTHERBOARD
        </span>
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
