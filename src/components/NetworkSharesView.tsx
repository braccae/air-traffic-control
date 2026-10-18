import React, { useState } from 'react';
import type { NodeItem } from '../types/index.ts';

interface NetworkSharesViewProps {
  node: NodeItem;
}

export const NetworkSharesView: React.FC<NetworkSharesViewProps> = ({ node }) => {
  const [shares, setShares] = useState([
    {
      id: 'sh-1',
      name: 'media_vault',
      protocol: 'SMB / CIFS (Samba 4.19)',
      path: '/mnt/vault/media',
      permissions: 'read-only (guests), read-write (core, media)',
      clientsCount: 4,
      status: 'EXPORTED / ACTIVE',
    },
    {
      id: 'sh-2',
      name: 'backups_nfs',
      protocol: 'NFSv4 (Kernel Server)',
      path: '/mnt/vault/backups',
      permissions: 'rw,sync,no_subtree_check (100.64.0.0/10)',
      clientsCount: 2,
      status: 'EXPORTED / ACTIVE',
    },
    {
      id: 'sh-3',
      name: 'app_configs',
      protocol: 'SMB / CIFS',
      path: '/var/home/core/configs',
      permissions: 'rw (core:1000)',
      clientsCount: 1,
      status: 'EXPORTED / ACTIVE',
    },
    {
      id: 'sh-4',
      name: 'iso_store',
      protocol: 'NFSv4 / WebDAV',
      path: '/var/lib/libvirt/images',
      permissions: 'ro,all_squash (homelab-dc1)',
      clientsCount: 3,
      status: 'EXPORTED / ACTIVE',
    },
  ]);

  const [showAddForm, setShowAddForm] = useState(false);
  const [newName, setNewName] = useState('');
  const [newProto, setNewProto] = useState('NFSv4');
  const [newPath, setNewPath] = useState('/mnt/vault/');

  const handleAddShare = (e: React.FormEvent) => {
    e.preventDefault();
    if (!newName.trim()) return;
    const item = {
      id: `sh-${Date.now()}`,
      name: newName.trim(),
      protocol: newProto,
      path: newPath.trim(),
      permissions: 'rw (authenticated mesh nodes)',
      clientsCount: 0,
      status: 'EXPORTED / ACTIVE',
    };
    setShares(prev => [item, ...prev]);
    setNewName('');
    setShowAddForm(false);
  };

  return (
    <section className="bg-[#171a2d] rounded-lg p-4 flex flex-col border border-[#313447] shadow-lg">
      <div className="flex flex-wrap items-center justify-between gap-2 pb-3 border-b border-[#26293c]">
        <div className="flex items-center gap-2">
          <span className="material-symbols-outlined text-[#ffd97d] text-[22px]">share</span>
          <div>
            <h3 className="font-sans text-sm text-[#dfe1fa] font-bold">
              Network File Shares: NFSv4 &amp; Samba / SMB ({node.shortName})
            </h3>
            <p className="font-mono text-[11px] text-[#8d909b]">
              Exported file systems for homelab mesh nodes, Proxmox storage backends &amp; clients
            </p>
          </div>
        </div>
        <button
          type="button"
          onClick={() => setShowAddForm(prev => !prev)}
          className="flex items-center gap-1 bg-[#b0c9ff] hover:bg-[#d8e2ff] text-[#002e68] font-bold font-mono text-xs px-2.5 py-1 rounded transition-colors cursor-pointer"
        >
          <span className="material-symbols-outlined text-[14px]">add</span>
          <span>+ New Export</span>
        </button>
      </div>

      {showAddForm && (
        <form
          onSubmit={handleAddShare}
          className="mt-3 p-3 bg-[#0a0d1f] rounded border border-[#b0c9ff]/40 grid grid-cols-1 md:grid-cols-4 gap-2 font-mono text-xs items-end"
        >
          <div>
            <label className="block text-[10px] text-[#8d909b] mb-0.5">SHARE NAME</label>
            <input
              type="text"
              required
              value={newName}
              onChange={e => setNewName(e.target.value)}
              placeholder="e.g. data_vault"
              className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2 py-1 text-[#dfe1fa]"
            />
          </div>
          <div>
            <label className="block text-[10px] text-[#8d909b] mb-0.5">PROTOCOL</label>
            <select
              value={newProto}
              onChange={e => setNewProto(e.target.value)}
              className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2 py-1 text-[#dfe1fa]"
            >
              <option value="NFSv4">NFSv4</option>
              <option value="SMB / CIFS">SMB / CIFS</option>
              <option value="NFS + SMB">NFS + SMB (Dual Export)</option>
            </select>
          </div>
          <div>
            <label className="block text-[10px] text-[#8d909b] mb-0.5">HOST PATH</label>
            <input
              type="text"
              required
              value={newPath}
              onChange={e => setNewPath(e.target.value)}
              className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2 py-1 text-[#dfe1fa]"
            />
          </div>
          <div className="flex gap-1.5">
            <button
              type="submit"
              className="px-3 py-1 bg-[#8cd6cb] text-[#002e68] font-bold rounded cursor-pointer"
            >
              Export Share
            </button>
            <button
              type="button"
              onClick={() => setShowAddForm(false)}
              className="px-2 py-1 bg-[#26293c] text-[#c3c6d2] rounded cursor-pointer"
            >
              Cancel
            </button>
          </div>
        </form>
      )}

      <div className="overflow-x-auto mt-3 font-mono text-xs">
        <table className="w-full text-left">
          <thead className="bg-[#0a0d1f] text-[#8d909b] text-[10px] uppercase">
            <tr className="h-7">
              <th className="px-3">Share Name</th>
              <th className="px-3">Protocol</th>
              <th className="px-3">Exported Path</th>
              <th className="px-3">Security &amp; Permissions</th>
              <th className="px-3">Active Clients</th>
              <th className="px-3">Status</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[#26293c]/60">
            {shares.map(share => (
              <tr key={share.id} className="h-9 hover:bg-[#26293c]/40 transition-colors">
                <td className="px-3 font-bold text-[#b0c9ff] flex items-center gap-1.5 pt-2">
                  <span className="material-symbols-outlined text-[15px] text-[#ffd97d]">folder_shared</span>
                  <span>{share.name}</span>
                </td>
                <td className="px-3 text-[#d9b9ff]">{share.protocol}</td>
                <td className="px-3 text-[#dfe1fa]">{share.path}</td>
                <td className="px-3 text-[#8d909b]">{share.permissions}</td>
                <td className="px-3 text-[#8cd6cb]">{share.clientsCount} connected</td>
                <td className="px-3">
                  <span className="px-1.5 py-0.2 rounded text-[10px] font-bold bg-[#8cd6cb]/20 text-[#8cd6cb] border border-[#8cd6cb]/30">
                    {share.status}
                  </span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
};
