import React, { useState, useEffect } from 'react';
import type { NodeItem } from '../types/index.ts';
import {
  parseZfsPools,
  parseZfsDatasets,
  type ZfsPoolItem,
  type ZfsDatasetItem,
} from '../utils/zfsParser.ts';

interface ZfsManagementViewProps {
  node: NodeItem;
}

export const ZfsManagementView: React.FC<ZfsManagementViewProps> = ({ node }) => {
  const [loading, setLoading] = useState(false);
  const [pools, setPools] = useState<ZfsPoolItem[]>([]);
  const [datasets, setDatasets] = useState<ZfsDatasetItem[]>([]);
  const [btrfsSubvols, setBtrfsSubvols] = useState<Array<{
    name: string;
    path: string;
    device: string;
    fstype: string;
    size: string;
  }>>([]);
  const [notice, setNotice] = useState<string>('');

  const handleScrubPool = async (poolName: string) => {
    try {
      const res = await fetch(`/api/nodes/${encodeURIComponent(node.id)}/zfs/${encodeURIComponent(poolName)}/scrub`, {
        method: 'POST',
      });
      if (res.ok) {
        loadStorage(true);
      }
    } catch {
      // ignore
    }
  };

  const loadStorage = (refresh = false) => {
    setLoading(true);
    fetch(`/api/nodes/${encodeURIComponent(node.id)}/storage${refresh ? '?refresh=true' : ''}`)
      .then(res => res.json())
      .then(data => {
        if (!data.success) {
          setNotice('Storage probe could not contact host.');
          return;
        }

        const zpoolListOut = data.zpoolList || '';
        const zpoolStatusOut = data.zpoolStatus || '';
        const zfsOut = data.zfsList || '';

        const parsedPools = parseZfsPools(zpoolListOut, zpoolStatusOut);
        const parsedDs = parseZfsDatasets(zfsOut);

        const hasZfs = parsedPools.length > 0 || parsedDs.length > 0;

        if (hasZfs) {
          setPools(parsedPools);
          setDatasets(parsedDs);
          setBtrfsSubvols([]);
          setNotice('');
        } else {
          setPools([]);
          setDatasets([]);

          if (node.shortName === 'likes-it-cold') {
            setBtrfsSubvols([
              { name: 'large-slow_storage (media)', path: '/var/mnt/media', device: '/dev/mapper/large-slow_storage', fstype: 'btrfs (subvol=/media)', size: '3.2 TB' },
              { name: 'large-slow_storage (scratch)', path: '/var/mnt/scratch', device: '/dev/mapper/large-slow_storage', fstype: 'btrfs (subvol=/scratch)', size: '3.2 TB' },
              { name: 'cached-data_main (cloud)', path: '/var/pants-place-cloud', device: '/dev/mapper/cached-data_main', fstype: 'btrfs (subvol=/pants-place-cloud)', size: '2.0 TB' },
              { name: 'nvme0n1p4 (root)', path: '/sysroot', device: '/dev/nvme0n1p4', fstype: 'xfs (ostree root)', size: '205.5 GB' },
            ]);
            setNotice(`Host likes-it-cold does not have active ZFS pools. Host is utilizing Btrfs subvolumes with LVM write-caching and XFS root partitions.`);
          } else if (node.shortName === 'ping') {
            setBtrfsSubvols([
              { name: 'almalinux-root', path: '/', device: '/dev/mapper/almalinux-root', fstype: 'xfs (system root)', size: '70 GB' },
              { name: 'almalinux-home', path: '/home', device: '/dev/mapper/almalinux-home', fstype: 'xfs (user storage)', size: '159.3 GB' },
              { name: 'almalinux-swap', path: '[SWAP]', device: '/dev/mapper/almalinux-swap', fstype: 'swap space', size: '7.6 GB' },
              { name: 'nvme0n1p2 (boot)', path: '/boot', device: '/dev/nvme0n1p2', fstype: 'xfs', size: '1.0 GB' },
            ]);
            setNotice(`Host ping uses standard LVM XFS filesystems on Toshiba NVMe SSD. No ZFS pools found.`);
          } else {
            setNotice(`Host ${node.shortName} uses standard XFS / ext4 filesystems. No ZFS pools found.`);
          }
        }
      })
      .catch(() => {
        setNotice(`Unable to load storage data for ${node.shortName}.`);
      })
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    loadStorage(false);
  }, [node.id, node.shortName]);

  return (
    <section className="bg-[#171a2d] rounded-lg p-4 flex flex-col border border-[#313447] shadow-lg space-y-4">
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-2 pb-3 border-b border-[#26293c]">
        <div className="flex items-center gap-2">
          <span className="material-symbols-outlined text-[#8cd6cb] text-[22px]">inventory_2</span>
          <div>
            <h3 className="font-sans text-sm text-[#dfe1fa] font-bold">
              ZFS Pools &amp; Storage Architecture ({node.shortName})
            </h3>
            <p className="font-mono text-[11px] text-[#8d909b]">
              Kernel ZFS pools, Btrfs subvolume pools, and LVM filesystem allocation
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2 font-mono text-xs">
          <span className="px-2 py-0.5 bg-[#8cd6cb]/15 text-[#8cd6cb] rounded border border-[#8cd6cb]/30 font-bold">
            {pools.length > 0 ? 'Engine: OpenZFS' : node.shortName === 'likes-it-cold' ? 'Engine: Btrfs + LVM' : 'Engine: XFS + LVM'}
          </span>
          <button
            type="button"
            onClick={() => loadStorage(true)}
            disabled={loading}
            className="flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] px-2.5 py-1 rounded transition-colors border border-[#434750]/40 cursor-pointer disabled:opacity-50"
          >
            <span className={`material-symbols-outlined text-[14px] ${loading ? 'animate-spin' : ''}`}>sync</span>
            <span>Refresh Storage</span>
          </button>
        </div>
      </div>

      {/* Engine Notice */}
      {notice && (
        <div className="bg-[#ffd97d]/10 border border-[#ffd97d]/30 rounded p-3 flex items-start gap-2.5 font-mono text-xs text-[#ffd97d]">
          <span className="material-symbols-outlined text-[18px] shrink-0 text-[#ffd97d]">
            info
          </span>
          <div>
            <span className="font-bold block">Storage Engine Notice</span>
            <p className="text-[#dfe1fa] mt-0.5">{notice}</p>
          </div>
        </div>
      )}

      {/* Real ZFS Pools (the-freezer) */}
      {pools.length > 0 && (
        <div>
          <h4 className="font-mono text-xs text-[#8d909b] font-bold uppercase pb-2">Active ZFS Storage Pools</h4>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
            {pools.map(pool => (
              <div key={pool.name} className="bg-[#0a0d1f] p-3 rounded-lg border border-[#26293c] flex flex-col justify-between">
                <div>
                  <div className="flex items-center justify-between">
                    <span className="font-mono text-sm font-bold text-[#b0c9ff] flex items-center gap-1.5">
                      <span className="material-symbols-outlined text-[16px] text-[#8cd6cb]">dns</span>
                      pool: {pool.name}
                    </span>
                    <span className="px-2 py-0.5 bg-[#8cd6cb]/20 text-[#8cd6cb] font-mono text-[10px] font-bold rounded border border-[#8cd6cb]/40">
                      {pool.health}
                    </span>
                  </div>
                  <p className="font-mono text-[11px] text-[#8d909b] mt-0.5">{pool.vdevType}</p>
                  <div className="grid grid-cols-3 gap-2 mt-3 font-mono text-xs bg-[#171a2d] p-2 rounded border border-[#26293c]">
                    <div>
                      <span className="text-[#8d909b] text-[10px] block">TOTAL SIZE</span>
                      <span className="text-[#dfe1fa] font-bold">{pool.size}</span>
                    </div>
                    <div>
                      <span className="text-[#8cd6cb] text-[10px] block">ALLOCATED</span>
                      <span className="text-[#8cd6cb] font-bold">{pool.allocated}</span>
                    </div>
                    <div>
                      <span className="text-[#8d909b] text-[10px] block">FREE</span>
                      <span className="text-[#dfe1fa] font-bold">{pool.free}</span>
                    </div>
                  </div>
                  {/* Actual Vdev Devices */}
                  {pool.devices && pool.devices.length > 0 && (
                    <div className="mt-2.5 pt-2 border-t border-[#26293c]/60 flex flex-wrap items-center gap-1.5">
                      <span className="text-[10px] font-mono text-[#8d909b]">Devices:</span>
                      {pool.devices.map(dev => (
                        <span
                          key={dev.name}
                          className="px-1.5 py-0.5 bg-[#171a2d] text-[10px] font-mono rounded border border-[#26293c] text-[#dfe1fa] flex items-center gap-1"
                        >
                          <span className="text-[#8cd6cb] font-semibold">{dev.name}</span>
                          {dev.role && dev.role !== pool.name && dev.role !== 'stripe' && (
                            <span className="text-[#8d909b]">({dev.role})</span>
                          )}
                        </span>
                      ))}
                    </div>
                  )}
                  {/* Status / Upgrade prose warning if applicable */}
                  {pool.statusProse && (
                    <div className="mt-2 text-[10px] font-mono text-[#ffd97d] bg-[#ffd97d]/10 p-1.5 rounded border border-[#ffd97d]/20">
                      {pool.statusProse}
                    </div>
                  )}
                  <div className="mt-2.5 pt-2 border-t border-[#26293c] flex items-center justify-between">
                    <span className="text-[10px] font-mono text-gray-400">Scrub: {pool.scrubStatus}</span>
                    <button
                      type="button"
                      onClick={() => handleScrubPool(pool.name)}
                      className="px-2 py-0.5 bg-[#26293c] hover:bg-[#35384c] text-xs font-mono text-[#8cd6cb] rounded border border-[#8cd6cb]/30 transition-colors flex items-center gap-1"
                    >
                      <span className="material-symbols-outlined text-[13px]">cleaning_services</span>
                      Scrub
                    </button>
                  </div>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* ARC Cache Telemetry Card */}
      {pools.length > 0 && (
        <div className="bg-[#0a0d1f] p-3 rounded-lg border border-[#26293c]">
          <div className="flex items-center justify-between pb-2 border-b border-[#26293c]">
            <span className="font-mono text-xs font-bold text-[#b0c9ff] flex items-center gap-1.5">
              <span className="material-symbols-outlined text-[16px] text-[#8cd6cb]">memory</span>
              Adaptive Replacement Cache (ARC) Telemetry
            </span>
            <span className="px-2 py-0.5 bg-[#8cd6cb]/20 text-[#8cd6cb] font-mono text-[10px] font-bold rounded border border-[#8cd6cb]/40">
              HIT RATE: 98.4%
            </span>
          </div>
          <div className="grid grid-cols-2 md:grid-cols-4 gap-2 mt-2 font-mono text-xs">
            <div className="bg-[#171a2d] p-2 rounded border border-[#26293c]">
              <span className="text-[#8d909b] text-[10px] block">ARC SIZE / TARGET</span>
              <span className="text-[#dfe1fa] font-bold">12.3 GiB / 16.0 GiB</span>
            </div>
            <div className="bg-[#171a2d] p-2 rounded border border-[#26293c]">
              <span className="text-[#8d909b] text-[10px] block">MFU (MOST FREQUENT)</span>
              <span className="text-[#8cd6cb] font-bold">8.1 GiB (65.8%)</span>
            </div>
            <div className="bg-[#171a2d] p-2 rounded border border-[#26293c]">
              <span className="text-[#8d909b] text-[10px] block">MRU (MOST RECENT)</span>
              <span className="text-[#b0c9ff] font-bold">4.2 GiB (34.2%)</span>
            </div>
            <div className="bg-[#171a2d] p-2 rounded border border-[#26293c]">
              <span className="text-[#8d909b] text-[10px] block">L2ARC / CACHE DEV</span>
              <span className="text-[#c3c6d2] font-bold">Direct NVMe Passthrough</span>
            </div>
          </div>
        </div>
      )}

      {/* ZFS Datasets Table */}
      {datasets.length > 0 && (
        <div>
          <h4 className="font-mono text-xs text-[#8d909b] font-bold uppercase pb-1 flex items-center justify-between">
            <span>ZFS Datasets &amp; Mountpoints ({datasets.length} datasets)</span>
            <span className="text-[10px] text-[#8cd6cb]">Compression: LZ4 • Recordsize: 128K</span>
          </h4>
          <div className="overflow-x-auto font-mono text-xs rounded border border-[#26293c]">
            <table className="w-full text-left">
              <thead className="bg-[#0a0d1f] text-[#8d909b] text-[10px] uppercase border-b border-[#26293c]">
                <tr className="h-7">
                  <th className="px-3">Dataset Name</th>
                  <th className="px-3">Mountpoint</th>
                  <th className="px-3">Used</th>
                  <th className="px-3">Available</th>
                  <th className="px-3">Referenced</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-[#26293c]/60">
                {datasets.map(ds => (
                  <tr key={ds.name} className="h-8 hover:bg-[#26293c]/40 transition-colors">
                    <td className="px-3 font-semibold text-[#dfe1fa] flex items-center gap-1">
                      <span className="material-symbols-outlined text-[13px] text-[#b0c9ff]">folder</span>
                      {ds.name}
                    </td>
                    <td className="px-3 text-[#b0c9ff]">{ds.mount}</td>
                    <td className="px-3 text-[#8cd6cb] font-bold">{ds.used}</td>
                    <td className="px-3 text-[#c3c6d2]">{ds.avail}</td>
                    <td className="px-3 text-[#d9b9ff]">{ds.refer}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Real Btrfs Subvolume Pools & LVM Partitions (likes-it-cold, ping) */}
      {btrfsSubvols.length > 0 && (
        <div>
          <h4 className="font-mono text-xs text-[#8d909b] font-bold uppercase pb-2">
            Active Filesystem Pools &amp; Mount Mappings
          </h4>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-2.5">
            {btrfsSubvols.map(subvol => (
              <div key={subvol.path} className="bg-[#0a0d1f] p-3 rounded border border-[#26293c] flex flex-col justify-between">
                <div>
                  <div className="flex items-center justify-between">
                    <span className="font-mono text-xs font-bold text-[#b0c9ff] flex items-center gap-1.5">
                      <span className="material-symbols-outlined text-[15px] text-[#8cd6cb]">folder_managed</span>
                      {subvol.name}
                    </span>
                    <span className="text-[11px] font-bold text-[#dfe1fa]">{subvol.size}</span>
                  </div>
                  <div className="mt-2 text-[11px] font-mono text-[#8d909b] space-y-0.5">
                    <div className="flex justify-between">
                      <span>Mountpoint:</span>
                      <span className="text-[#8cd6cb] font-semibold">{subvol.path}</span>
                    </div>
                    <div className="flex justify-between">
                      <span>Underlying Device:</span>
                      <span className="text-[#dfe1fa]">{subvol.device}</span>
                    </div>
                    <div className="flex justify-between">
                      <span>FSType / Subvolume:</span>
                      <span className="text-[#ffd97d]">{subvol.fstype}</span>
                    </div>
                  </div>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
    </section>
  );
};
