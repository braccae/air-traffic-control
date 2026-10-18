import React, { useState } from 'react';
import type { NodeItem } from '../types/index.ts';

interface ZfsManagementViewProps {
  node: NodeItem;
}

export const ZfsManagementView: React.FC<ZfsManagementViewProps> = ({ node }) => {
  const [pools, setPools] = useState([
    {
      name: 'tank0',
      size: '1.82 TiB',
      allocated: '320 GiB (17%)',
      free: '1.50 TiB',
      frag: '2%',
      health: 'ONLINE',
      altroot: '-',
      vdevType: 'mirror (2x 2TB NVMe Samsung 980 Pro)',
      scrubStatus: 'Scrub finished with 0 errors on Sun Oct 18 00:00:00 2026',
    },
    {
      name: 'vault',
      size: '14.5 TiB',
      allocated: '6.8 TiB (47%)',
      free: '7.7 TiB',
      frag: '5%',
      health: 'ONLINE',
      altroot: '/mnt/vault',
      vdevType: 'raidz2 (6x 4TB Seagate IronWolf)',
      scrubStatus: 'Scrub repaired 0B in 04:12:18, 0 errors',
    },
  ]);

  const [scrubbing, setScrubbing] = useState<string | null>(null);

  const triggerScrub = (poolName: string) => {
    setScrubbing(poolName);
    setTimeout(() => {
      setPools(prev =>
        prev.map(p =>
          p.name === poolName
            ? { ...p, scrubStatus: 'Scrub in progress: 1.2% scanned, 0 errors' }
            : p
        )
      );
      setScrubbing(null);
    }, 1200);
  };

  const datasets = [
    { name: 'tank0/root', used: '18.4 GiB', avail: '1.50 TiB', refer: '18.4 GiB', mount: '/' },
    { name: 'tank0/podman-storage', used: '84.2 GiB', avail: '1.50 TiB', refer: '84.2 GiB', mount: '/var/lib/containers' },
    { name: 'tank0/home', used: '12.8 GiB', avail: '1.50 TiB', refer: '12.8 GiB', mount: '/var/home' },
    { name: 'vault/media', used: '4.8 TiB', avail: '7.7 TiB', refer: '4.8 TiB', mount: '/mnt/vault/media' },
    { name: 'vault/backups', used: '1.9 TiB', avail: '7.7 TiB', refer: '1.9 TiB', mount: '/mnt/vault/backups' },
  ];

  return (
    <section className="bg-[#171a2d] rounded-lg p-4 flex flex-col border border-[#313447] shadow-lg space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2 pb-3 border-b border-[#26293c]">
        <div className="flex items-center gap-2">
          <span className="material-symbols-outlined text-[#8cd6cb] text-[22px]">inventory_2</span>
          <div>
            <h3 className="font-sans text-sm text-[#dfe1fa] font-bold">
              ZFS Storage Pools &amp; Datasets ({node.shortName})
            </h3>
            <p className="font-mono text-[11px] text-[#8d909b]">
              Native ARC cache, snapshots, redundancy layout and background scrub diagnostics
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2 font-mono text-xs">
          <span className="px-2 py-0.5 bg-[#8cd6cb]/15 text-[#8cd6cb] rounded border border-[#8cd6cb]/30">
            ARC: 2.1 GiB (Hit Rate: 98.4%)
          </span>
          <span className="px-2 py-0.5 bg-[#b0c9ff]/15 text-[#b0c9ff] rounded border border-[#b0c9ff]/30">
            Compression: zstd (1.62x)
          </span>
        </div>
      </div>

      {/* Pools Cards */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
        {pools.map(pool => (
          <div key={pool.name} className="bg-[#0a0d1f] p-3 rounded-lg border border-[#26293c] flex flex-col justify-between">
            <div>
              <div className="flex items-center justify-between">
                <span className="font-mono text-sm font-bold text-[#b0c9ff]">{pool.name}</span>
                <span className="px-2 py-0.5 bg-[#8cd6cb]/20 text-[#8cd6cb] font-mono text-[10px] font-bold rounded border border-[#8cd6cb]/40">
                  {pool.health}
                </span>
              </div>
              <p className="font-mono text-[11px] text-[#8d909b] mt-0.5">{pool.vdevType}</p>

              <div className="grid grid-cols-3 gap-2 mt-3 font-mono text-xs bg-[#171a2d] p-2 rounded">
                <div>
                  <span className="text-[#8d909b] text-[10px] block">TOTAL SIZE</span>
                  <span className="text-[#dfe1fa] font-bold">{pool.size}</span>
                </div>
                <div>
                  <span className="text-[#8d909b] text-[10px] block">ALLOCATED</span>
                  <span className="text-[#8cd6cb] font-bold">{pool.allocated}</span>
                </div>
                <div>
                  <span className="text-[#8d909b] text-[10px] block">FREE</span>
                  <span className="text-[#dfe1fa] font-bold">{pool.free}</span>
                </div>
              </div>

              <div className="mt-2 text-[11px] font-mono text-[#c3c6d2]">
                <span className="text-[#8d909b]">Scrub: </span>
                <span>{pool.scrubStatus}</span>
              </div>
            </div>

            <div className="mt-3 pt-2 border-t border-[#26293c] flex items-center justify-end gap-2">
              <button
                type="button"
                disabled={scrubbing === pool.name}
                onClick={() => triggerScrub(pool.name)}
                className="px-2.5 py-1 bg-[#26293c] hover:bg-[#35384c] text-[#8cd6cb] font-mono text-xs rounded transition-colors cursor-pointer border border-[#434750]/30"
              >
                {scrubbing === pool.name ? 'Starting Scrub...' : 'Run Scrub'}
              </button>
            </div>
          </div>
        ))}
      </div>

      {/* Datasets Table */}
      <div>
        <h4 className="font-mono text-xs text-[#8d909b] font-bold uppercase pb-1">ZFS Datasets &amp; Mountpoints</h4>
        <div className="overflow-x-auto font-mono text-xs">
          <table className="w-full text-left">
            <thead className="bg-[#0a0d1f] text-[#8d909b] text-[10px] uppercase">
              <tr className="h-7">
                <th className="px-3">Dataset</th>
                <th className="px-3">Mountpoint</th>
                <th className="px-3">Used</th>
                <th className="px-3">Available</th>
                <th className="px-3">Referenced</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[#26293c]/60">
              {datasets.map(ds => (
                <tr key={ds.name} className="h-8 hover:bg-[#26293c]/40 transition-colors">
                  <td className="px-3 font-semibold text-[#dfe1fa]">{ds.name}</td>
                  <td className="px-3 text-[#b0c9ff]">{ds.mount}</td>
                  <td className="px-3 text-[#8cd6cb]">{ds.used}</td>
                  <td className="px-3 text-[#c3c6d2]">{ds.avail}</td>
                  <td className="px-3 text-[#d9b9ff]">{ds.refer}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </section>
  );
};
