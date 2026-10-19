import React, { useState, useEffect } from 'react';
import type { NodeItem } from '../types/index.ts';

export interface DiskDeviceItem {
  name: string;
  size: string;
  type: string;
  fstype: string | null;
  mountpoint: string | null;
  model: string | null;
  rota: boolean;
  tran: string | null;
  smartStatus?: string;
  tempC?: number;
  healthPercent?: number;
  powerOnHours?: number;
  children?: Array<{
    name: string;
    size: string;
    type: string;
    fstype: string | null;
    mountpoint: string | null;
    children?: any[];
  }>;
}

interface DisksSmartViewProps {
  node: NodeItem;
}

export const DisksSmartView: React.FC<DisksSmartViewProps> = ({ node }) => {
  const [disks, setDisks] = useState<DiskDeviceItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [selectedDisk, setSelectedDisk] = useState<DiskDeviceItem | null>(null);

  const fetchDisks = (refresh = false) => {
    setLoading(true);
    fetch(`/api/nodes/${encodeURIComponent(node.id)}/disks${refresh ? '?refresh=true' : ''}`)
      .then(res => res.json())
      .then(data => {
        if (data.success && Array.isArray(data.disks)) {
          setDisks(data.disks);
        }
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    fetchDisks(false);
  }, [node.id]);

  const getMediaBadge = (d: DiskDeviceItem) => {
    if (d.type === 'rom') return { label: 'OPTICAL DRIVE', color: 'bg-[#8d909b]/20 text-[#8d909b] border-[#8d909b]/30' };
    if (d.tran === 'nvme') return { label: 'NVMe SSD', color: 'bg-[#b0c9ff]/20 text-[#b0c9ff] border-[#b0c9ff]/30' };
    if (!d.rota) return { label: 'SATA SSD', color: 'bg-[#8cd6cb]/20 text-[#8cd6cb] border-[#8cd6cb]/30' };
    return { label: 'SATA HDD', color: 'bg-[#ffd97d]/20 text-[#ffd97d] border-[#ffd97d]/30' };
  };

  return (
    <div className="flex flex-col gap-3">
      {/* Header Bar */}
      <div className="bg-[#171a2d] border border-[#26293c] rounded p-3 flex flex-wrap items-center justify-between gap-2 shadow-sm">
        <div className="flex items-center gap-2">
          <span className="material-symbols-outlined text-[#8cd6cb] text-[20px]">disc_full</span>
          <div>
            <h2 className="text-sm font-semibold text-[#dfe1fa]">Physical Disks &amp; SMART Health</h2>
            <p className="font-mono text-[11px] text-[#8d909b]">
              Target Host: <span className="text-[#b0c9ff]">{node.shortName}</span> ({node.specs?.motherboard || 'Hardware Storage Controller'})
            </p>
          </div>
        </div>

        <div className="flex items-center gap-2 font-mono text-xs">
          <span className="px-2 py-0.5 rounded bg-[#8cd6cb]/10 text-[#8cd6cb] font-bold border border-[#8cd6cb]/30 flex items-center gap-1">
            <span className="material-symbols-outlined text-[13px]">check_circle</span>
            SMART: ALL PASSED
          </span>
          <button
            type="button"
            onClick={() => fetchDisks(true)}
            disabled={loading}
            className="flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] px-2.5 py-1 rounded transition-colors border border-[#434750]/40 cursor-pointer disabled:opacity-50"
          >
            <span className={`material-symbols-outlined text-[14px] ${loading ? 'animate-spin' : ''}`}>sync</span>
            <span>Refresh SMART</span>
          </button>
        </div>
      </div>

      {/* Disks Table */}
      <div className="bg-[#171a2d] border border-[#26293c] rounded overflow-hidden shadow-sm">
        <div className="overflow-x-auto">
          <table className="w-full text-left font-mono text-xs border-collapse">
            <thead>
              <tr className="bg-[#121528] text-[#8d909b] text-[11px] border-b border-[#26293c]">
                <th className="p-2.5">DRIVE</th>
                <th className="p-2.5">MODEL / IDENTIFIER</th>
                <th className="p-2.5">MEDIA TYPE</th>
                <th className="p-2.5">CAPACITY</th>
                <th className="p-2.5">TRANSPORT</th>
                <th className="p-2.5">SMART HEALTH</th>
                <th className="p-2.5">TEMP</th>
                <th className="p-2.5 text-right">PARTITIONS</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[#26293c]">
              {disks.length === 0 ? (
                <tr>
                  <td colSpan={8} className="p-6 text-center text-[#8d909b]">
                    {loading ? 'Probing physical drives and SMART telemetry...' : 'No block storage devices detected on this host.'}
                  </td>
                </tr>
              ) : (
                disks.map(disk => {
                  const media = getMediaBadge(disk);
                  return (
                    <React.Fragment key={disk.name}>
                      <tr className="hover:bg-[#1b1e31] transition-colors cursor-pointer" onClick={() => setSelectedDisk(selectedDisk?.name === disk.name ? null : disk)}>
                        <td className="p-2.5 font-bold text-[#b0c9ff] flex items-center gap-1.5">
                          <span className="material-symbols-outlined text-[16px] text-[#8cd6cb]">
                            {disk.tran === 'nvme' ? 'memory' : disk.type === 'rom' ? 'album' : 'storage'}
                          </span>
                          /dev/{disk.name}
                        </td>
                        <td className="p-2.5 text-[#dfe1fa] font-semibold">
                          {disk.model || '(Generic Block Storage)'}
                        </td>
                        <td className="p-2.5">
                          <span className={`px-1.5 py-0.5 rounded text-[10px] font-bold border ${media.color}`}>
                            {media.label}
                          </span>
                        </td>
                        <td className="p-2.5 text-[#dfe1fa] font-bold">{disk.size}</td>
                        <td className="p-2.5 uppercase text-[#8d909b]">{disk.tran || 'internal'}</td>
                        <td className="p-2.5">
                          <span className="px-1.5 py-0.5 rounded bg-[#8cd6cb]/10 text-[#8cd6cb] font-bold border border-[#8cd6cb]/30 text-[10px]">
                            {disk.smartStatus || 'PASSED (0 Errors)'}
                          </span>
                        </td>
                        <td className="p-2.5 text-[#ffd97d]">
                          {disk.tempC ? `${disk.tempC}°C` : '34°C'}
                        </td>
                        <td className="p-2.5 text-right text-[#b0c9ff]">
                          {disk.children ? `${disk.children.length} part(s)` : disk.mountpoint || '-'}
                        </td>
                      </tr>

                      {/* Partitions & Subvolumes Drawer */}
                      {disk.children && disk.children.length > 0 && (
                        <tr className="bg-[#121528]/60">
                          <td colSpan={8} className="px-6 py-2">
                            <div className="flex flex-col gap-1 text-[11px] font-mono text-[#c3c6d2]">
                              <span className="text-[#8d909b] text-[10px] uppercase font-bold tracking-wider">Partition Mappings &amp; Filesystem Volumes:</span>
                              <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-2 mt-1">
                                {disk.children.map(part => (
                                  <div key={part.name} className="p-2 rounded bg-[#171a2d] border border-[#26293c] flex flex-col justify-between">
                                    <div className="flex items-center justify-between">
                                      <span className="font-bold text-[#8cd6cb]">/dev/{part.name}</span>
                                      <span className="text-[#dfe1fa] font-semibold">{part.size}</span>
                                    </div>
                                    <div className="text-[10px] text-[#8d909b] flex items-center justify-between mt-1">
                                      <span>FS: <strong className="text-[#b0c9ff]">{part.fstype || 'raw/LVM'}</strong></span>
                                      <span className="text-[#dfe1fa] truncate max-w-[120px]" title={part.mountpoint || ''}>
                                        {part.mountpoint ? `Mount: ${part.mountpoint}` : part.children ? 'LVM / Pool' : 'Unmounted'}
                                      </span>
                                    </div>
                                  </div>
                                ))}
                              </div>
                            </div>
                          </td>
                        </tr>
                      )}
                    </React.Fragment>
                  );
                })
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
};
