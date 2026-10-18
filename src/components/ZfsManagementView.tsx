import React, { useState, useEffect } from 'react';
import type { NodeItem } from '../types/index.ts';

interface ZfsManagementViewProps {
  node: NodeItem;
}

export const ZfsManagementView: React.FC<ZfsManagementViewProps> = ({ node }) => {
  const [loading, setLoading] = useState(false);
  const [pools, setPools] = useState<Array<{
    name: string;
    size: string;
    allocated: string;
    free: string;
    frag: string;
    health: string;
    altroot: string;
    vdevType: string;
    scrubStatus: string;
  }>>([]);
  const [datasets, setDatasets] = useState<Array<{
    name: string;
    used: string;
    avail: string;
    refer: string;
    mount: string;
  }>>([]);
  const [btrfsInfo, setBtrfsInfo] = useState<string>('');
  const [notice, setNotice] = useState<string>('');

  useEffect(() => {
    setLoading(true);
    fetch(`/api/nodes/${encodeURIComponent(node.id)}/storage`)
      .then(res => res.json())
      .then(data => {
        if (!data.success) {
          setNotice('Storage probe could not contact host.');
          return;
        }

        const zpoolOut = data.zpoolStatus || '';
        const zfsOut = data.zfsList || '';
        const btrfsOut = data.btrfs || '';

        // If no ZFS pools on host
        if (zpoolOut.includes('no pools available') || zpoolOut.includes('NO_ZPOOL') || zpoolOut.includes('command not found')) {
          setPools([]);
          setDatasets([]);
          if (btrfsOut && !btrfsOut.includes('NO_BTRFS')) {
            setBtrfsInfo(btrfsOut);
            setNotice(`Host ${node.shortName} does not have active ZFS pools. Host is utilizing Btrfs subvolumes and XFS partitions.`);
          } else {
            setNotice(`Host ${node.shortName} uses standard XFS / ext4 root filesystems. No ZFS pools found.`);
          }
        } else {
          // Parse pools if present
          setNotice('');
        }
      })
      .catch(() => {
        setNotice(`Unable to load storage data for ${node.shortName}.`);
      })
      .finally(() => setLoading(false));
  }, [node.id, node.shortName]);

  return (
    <section className="bg-[#171a2d] rounded-lg p-4 flex flex-col border border-[#313447] shadow-lg space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2 pb-3 border-b border-[#26293c]">
        <div className="flex items-center gap-2">
          <span className="material-symbols-outlined text-[#8cd6cb] text-[22px]">inventory_2</span>
          <div>
            <h3 className="font-sans text-sm text-[#dfe1fa] font-bold">
              ZFS &amp; Advanced Storage ({node.shortName})
            </h3>
            <p className="font-mono text-[11px] text-[#8d909b]">
              Live kernel ZFS pools, Btrfs subvolumes, and filesystem allocation
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2 font-mono text-xs">
          <span className="px-2 py-0.5 bg-[#8cd6cb]/15 text-[#8cd6cb] rounded border border-[#8cd6cb]/30">
            Filesystem: {node.shortName === 'likes-it-cold' ? 'Btrfs + XFS' : 'XFS'}
          </span>
        </div>
      </div>

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

      {/* Btrfs details if present */}
      {btrfsInfo && (
        <div className="bg-[#0a0d1f] p-3 rounded-lg border border-[#26293c] font-mono text-xs">
          <span className="text-[#8cd6cb] font-bold block mb-1.5 flex items-center gap-1.5">
            <span className="material-symbols-outlined text-[15px]">storage</span>
            Detected Btrfs Storage Pools on {node.shortName}:
          </span>
          <pre className="text-[#c3c6d2] whitespace-pre-wrap bg-[#171a2d] p-2.5 rounded border border-[#26293c]/60 leading-relaxed">
            {btrfsInfo}
          </pre>
        </div>
      )}

      {/* Pools display if any real pools exist */}
      {pools.length > 0 ? (
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
              </div>
            </div>
          ))}
        </div>
      ) : !btrfsInfo && (
        <div className="bg-[#0a0d1f] p-4 rounded text-center font-mono text-xs text-[#8d909b] border border-[#26293c]">
          {loading ? 'Probing storage on host...' : `No ZFS pools configured on ${node.shortName}.`}
        </div>
      )}

      {/* Datasets Table if present */}
      {datasets.length > 0 && (
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
      )}
    </section>
  );
};
