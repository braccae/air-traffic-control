import React, { useState, useEffect } from 'react';
import type { NodeItem } from '../types/index.ts';
import {
  parseZfsPools,
  parseZfsDatasets,
  parseBtrfsFilesystems,
  parseStorageMounts,
  detectStorageEngine,
  type ZfsPoolItem,
  type ZfsDatasetItem,
  type BtrfsFilesystem,
  type StorageMountItem,
  type StorageEngineType,
} from '../utils/zfsParser.ts';
import { safeFetchJson } from '../utils/apiDefensive.ts';

interface ZfsManagementViewProps {
  node: NodeItem;
  onEngineDetected?: (engine: StorageEngineType) => void;
  targetType?: 'zfs' | 'btrfs' | 'lvm' | 'all';
}

export const ZfsManagementView: React.FC<ZfsManagementViewProps> = ({
  node,
  onEngineDetected,
  targetType = 'all',
}) => {
  const [loading, setLoading] = useState(false);
  const [pools, setPools] = useState<ZfsPoolItem[]>([]);
  const [datasets, setDatasets] = useState<ZfsDatasetItem[]>([]);
  const [btrfsFilesystems, setBtrfsFilesystems] = useState<BtrfsFilesystem[]>([]);
  const [mounts, setMounts] = useState<StorageMountItem[]>([]);
  const [engine, setEngine] = useState<StorageEngineType>('none');
  const [notice, setNotice] = useState<string>('');

  const handleScrubPool = async (poolName: string) => {
    try {
      const result = await safeFetchJson<{ success?: boolean; error?: string }>(
        `/api/nodes/${encodeURIComponent(node.id)}/zfs/${encodeURIComponent(poolName)}/scrub`,
        { method: 'POST' }
      );
      if (result.success) {
        setNotice(`Scrub initiated successfully on pool ${poolName}.`);
        loadStorage(true);
      } else {
        setNotice(`Failed to start scrub on pool ${poolName}: ${result.error || 'HTTP ' + result.status}`);
      }
    } catch (err: any) {
      setNotice(`Failed to start scrub on pool ${poolName}: ${err?.message || 'Network error'}`);
    }
  };

  const loadStorage = async (refresh = false) => {
    setLoading(true);
    try {
      const result = await safeFetchJson<{
        success: boolean;
        zpoolList?: string;
        zpoolStatus?: string;
        zfsList?: string;
        btrfs?: string;
        findmnt?: string;
      }>(`/api/nodes/${encodeURIComponent(node.id)}/storage${refresh ? '?refresh=true' : ''}`);

      if (!result.success || !result.data) {
        setNotice(result.error || 'Storage probe could not contact host.');
        return;
      }

      const data = result.data;
      const zpoolListOut = data.zpoolList || '';
      const zpoolStatusOut = data.zpoolStatus || '';
      const zfsOut = data.zfsList || '';
      const btrfsOut = data.btrfs || '';
      const findmntOut = data.findmnt || '';

      const parsedPools = parseZfsPools(zpoolListOut, zpoolStatusOut);
      const parsedDs = parseZfsDatasets(zfsOut);
      const parsedBtrfs = parseBtrfsFilesystems(btrfsOut, findmntOut);
      const parsedMounts = parseStorageMounts(findmntOut);
      const detectedEngine = detectStorageEngine(zpoolListOut, btrfsOut, findmntOut);

      const effectiveEngine: StorageEngineType =
        targetType === 'zfs'
          ? 'zfs'
          : targetType === 'btrfs'
          ? 'btrfs'
          : targetType === 'lvm'
          ? 'lvm_ext'
          : detectedEngine;

      setEngine(effectiveEngine);
      onEngineDetected?.(effectiveEngine);

      if (targetType === 'zfs') {
        setPools(parsedPools);
        setDatasets(parsedDs);
        setBtrfsFilesystems([]);
        setMounts([]);
        if (parsedPools.length === 0) {
          setNotice(`No active OpenZFS pools found on ${node.shortName}.`);
        } else {
          setNotice('');
        }
      } else if (targetType === 'btrfs') {
        setPools([]);
        setDatasets([]);
        setBtrfsFilesystems(parsedBtrfs);
        setMounts(parsedMounts.filter(m => m.fstype === 'btrfs' || m.source.toLowerCase().includes('btrfs')));
        setNotice('');
      } else if (targetType === 'lvm') {
        setPools([]);
        setDatasets([]);
        setBtrfsFilesystems([]);
        setMounts(parsedMounts.filter(m => m.fstype !== 'btrfs' && m.fstype !== 'zfs'));
        setNotice('');
      } else {
        if (detectedEngine === 'zfs') {
          setPools(parsedPools);
          setDatasets(parsedDs);
          setBtrfsFilesystems([]);
          setMounts([]);
          setNotice('');
        } else {
          setPools([]);
          setDatasets([]);
          setBtrfsFilesystems(parsedBtrfs);
          setMounts(parsedMounts);
          setNotice('No active OpenZFS pools found on host. Displaying native Btrfs / LVM filesystems.');
        }
      }
    } catch {
      setNotice(`Unable to load storage data for ${node.shortName}.`);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadStorage(false);
  }, [node.id, node.shortName, targetType]);

  const displayIcon =
    targetType === 'btrfs'
      ? 'folder_managed'
      : targetType === 'lvm'
      ? 'storage'
      : targetType === 'zfs'
      ? 'inventory_2'
      : engine === 'zfs'
      ? 'inventory_2'
      : engine === 'btrfs'
      ? 'folder_managed'
      : 'storage';

  const displayTitle =
    targetType === 'btrfs'
      ? `Btrfs Storage Architecture (${node.shortName})`
      : targetType === 'lvm'
      ? `LVM Storage Architecture (${node.shortName})`
      : targetType === 'zfs'
      ? `ZFS Pools & Storage Architecture (${node.shortName})`
      : engine === 'zfs'
      ? `ZFS Pools & Storage Architecture (${node.shortName})`
      : engine === 'btrfs'
      ? `Btrfs Storage Architecture (${node.shortName})`
      : `Native Storage & Filesystems (${node.shortName})`;

  const displaySubtitle =
    targetType === 'btrfs'
      ? 'Btrfs multi-device storage pools, subvolumes, and active mounts'
      : targetType === 'lvm'
      ? 'Logical Volume Manager volume groups, thin pools, and active mounts'
      : targetType === 'zfs'
      ? 'Kernel OpenZFS pools, dataset hierarchies, and scrub telemetry'
      : engine === 'zfs'
      ? 'Kernel OpenZFS pools, dataset hierarchies, and scrub telemetry'
      : engine === 'btrfs'
      ? 'Btrfs multi-device storage pools, subvolumes, and active mounts'
      : 'Native Linux filesystems, LVM volumes, and mountpoint allocation';

  const displayBadge =
    targetType === 'btrfs'
      ? 'Engine: Btrfs'
      : targetType === 'lvm'
      ? 'Engine: LVM / XFS / ext4'
      : targetType === 'zfs'
      ? 'Engine: OpenZFS'
      : engine === 'zfs'
      ? 'Engine: OpenZFS'
      : engine === 'btrfs'
      ? 'Engine: Btrfs'
      : engine === 'lvm_ext'
      ? 'Engine: LVM / XFS'
      : 'Engine: Standard Storage';

  return (
    <section className="bg-[#171a2d] rounded-lg p-4 flex flex-col border border-[#313447] shadow-lg space-y-4">
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-2 pb-3 border-b border-[#26293c]">
        <div className="flex items-center gap-2">
          <span className="material-symbols-outlined text-[#8cd6cb] text-[22px]">
            {displayIcon}
          </span>
          <div>
            <h3 className="font-sans text-sm text-[#dfe1fa] font-bold">
              {displayTitle}
            </h3>
            <p className="font-mono text-[11px] text-[#8d909b]">
              {displaySubtitle}
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2 font-mono text-xs">
          <span className="px-2 py-0.5 bg-[#8cd6cb]/15 text-[#8cd6cb] rounded border border-[#8cd6cb]/30 font-bold">
            {displayBadge}
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

      {/* Real ZFS Pools (when active) */}
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

      {/* ZFS Datasets Table */}
      {datasets.length > 0 && (
        <div>
          <h4 className="font-mono text-xs text-[#8d909b] font-bold uppercase pb-1 flex items-center justify-between">
            <span>ZFS Datasets &amp; Mountpoints ({datasets.length} datasets)</span>
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

      {/* Native Storage & Filesystem Allocation (Btrfs, LVM, or when no ZFS pools exist) */}
      {(targetType === 'btrfs' || targetType === 'lvm' || (targetType === 'all' && pools.length === 0)) && (
        <div className="space-y-4">
          <div className="border-b border-[#26293c] pb-2">
            <h4 className="font-mono text-xs text-[#8d909b] font-bold uppercase">
              {targetType === 'btrfs'
                ? 'Btrfs Storage Pools & Subvolumes'
                : targetType === 'lvm'
                ? 'LVM Volume Mounts & Filesystem Allocation'
                : 'Native Storage & Filesystem Allocation'}
            </h4>
            <p className="font-mono text-[11px] text-[#8d909b] mt-0.5">
              {targetType === 'btrfs'
                ? 'Host Btrfs multi-device storage pools, member drives, and subvolume mounts'
                : targetType === 'lvm'
                ? 'Host block storage devices, logical volume partitions, and active filesystem mounts'
                : 'Host block storage devices, Btrfs subvolumes, and Linux filesystem mountpoints'}
            </p>
          </div>

          {/* Btrfs Storage Pools Cards (only for btrfs or all) */}
          {(targetType === 'btrfs' || targetType === 'all') && btrfsFilesystems.length > 0 && (
            <div>
              <h5 className="font-mono text-xs text-[#b0c9ff] font-semibold pb-2 flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[15px] text-[#8cd6cb]">folder_managed</span>
                <span>Btrfs Storage Pools ({btrfsFilesystems.length})</span>
              </h5>
              <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                {btrfsFilesystems.map(fs => (
                  <div key={fs.uuid} className="bg-[#0a0d1f] p-3 rounded-lg border border-[#26293c] flex flex-col justify-between">
                    <div>
                      <div className="flex items-center justify-between">
                        <span className="font-mono text-sm font-bold text-[#b0c9ff] flex items-center gap-1.5">
                          <span className="material-symbols-outlined text-[16px] text-[#8cd6cb]">dns</span>
                          {fs.label !== 'none' ? `pool: ${fs.label}` : `uuid: ${fs.uuid.slice(0, 8)}...`}
                        </span>
                        <span className="px-2 py-0.5 bg-[#8cd6cb]/20 text-[#8cd6cb] font-mono text-[10px] font-bold rounded border border-[#8cd6cb]/40">
                          BTRFS
                        </span>
                      </div>
                      <p className="font-mono text-[11px] text-[#8d909b] mt-0.5 truncate">
                        UUID: {fs.uuid}
                      </p>
                      <div className="grid grid-cols-2 gap-2 mt-3 font-mono text-xs bg-[#171a2d] p-2 rounded border border-[#26293c]">
                        <div>
                          <span className="text-[#8d909b] text-[10px] block">DEVICES</span>
                          <span className="text-[#dfe1fa] font-bold">{fs.totalDevices} device{fs.totalDevices > 1 ? 's' : ''}</span>
                        </div>
                        <div>
                          <span className="text-[#8cd6cb] text-[10px] block">FS BYTES USED</span>
                          <span className="text-[#8cd6cb] font-bold">{fs.fsBytesUsed || 'N/A'}</span>
                        </div>
                      </div>
                      {/* Member devices */}
                      {fs.devices && fs.devices.length > 0 && (
                        <div className="mt-2.5 pt-2 border-t border-[#26293c]/60 flex flex-wrap items-center gap-1.5">
                          <span className="text-[10px] font-mono text-[#8d909b]">Devices:</span>
                          {fs.devices.map(dev => (
                            <span
                              key={dev.path}
                              className="px-1.5 py-0.5 bg-[#171a2d] text-[10px] font-mono rounded border border-[#26293c] text-[#dfe1fa] flex items-center gap-1"
                            >
                              <span className="text-[#8cd6cb] font-semibold">{dev.path}</span>
                              {dev.size && <span className="text-[#8d909b]">({dev.size})</span>}
                            </span>
                          ))}
                        </div>
                      )}
                      {/* Associated Mountpoints */}
                      {fs.mountpoints && fs.mountpoints.length > 0 && (
                        <div className="mt-2.5 pt-2 border-t border-[#26293c]/60 flex flex-wrap items-center gap-1.5">
                          <span className="text-[10px] font-mono text-[#8d909b]">Mounts:</span>
                          {fs.mountpoints.map(mp => (
                            <span
                              key={mp}
                              className="px-1.5 py-0.5 bg-[#26293c]/50 text-[10px] font-mono rounded text-[#b0c9ff]"
                            >
                              {mp}
                            </span>
                          ))}
                        </div>
                      )}
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* Mounted Filesystems Table */}
          {mounts.length > 0 && (
            <div>
              <h5 className="font-mono text-xs text-[#b0c9ff] font-semibold pb-1 flex items-center justify-between">
                <span>Mounted Filesystem Allocation ({mounts.length} mounts)</span>
              </h5>
              <div className="overflow-x-auto font-mono text-xs rounded border border-[#26293c]">
                <table className="w-full text-left">
                  <thead className="bg-[#0a0d1f] text-[#8d909b] text-[10px] uppercase border-b border-[#26293c]">
                    <tr className="h-7">
                      <th className="px-3">Mountpoint (Target)</th>
                      <th className="px-3">Source Device / Subvolume</th>
                      <th className="px-3">FSType</th>
                      <th className="px-3">Size</th>
                      <th className="px-3">Used</th>
                      <th className="px-3">Available</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-[#26293c]/60">
                    {mounts.map(m => (
                      <tr key={`${m.target}-${m.source}`} className="h-8 hover:bg-[#26293c]/40 transition-colors">
                        <td className="px-3 font-semibold text-[#dfe1fa] flex items-center gap-1">
                          <span className="material-symbols-outlined text-[13px] text-[#b0c9ff]">folder</span>
                          {m.target}
                        </td>
                        <td className="px-3 text-[#b0c9ff] truncate max-w-xs" title={m.source}>{m.source}</td>
                        <td className="px-3">
                          <span className="px-1.5 py-0.5 rounded text-[10px] font-bold bg-[#26293c] text-[#ffd97d]">
                            {m.fstype}
                          </span>
                        </td>
                        <td className="px-3 text-[#dfe1fa]">{m.size || '-'}</td>
                        <td className="px-3 text-[#8cd6cb] font-bold">{m.used || '-'}</td>
                        <td className="px-3 text-[#c3c6d2]">{m.avail || '-'}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}

          {!loading && btrfsFilesystems.length === 0 && mounts.length === 0 && (
            <div className="bg-[#0a0d1f] p-6 rounded border border-[#26293c] text-center font-mono text-xs text-[#8d909b]">
              No active filesystems or storage pools detected on this host.
            </div>
          )}
        </div>
      )}
    </section>
  );
};
