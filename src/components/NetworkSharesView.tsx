import React, { useState, useEffect } from 'react';
import type { NodeItem } from '../types/index.ts';
import { ShareEditorModal } from './modals/ShareEditorModal.tsx';
import { UserManagerModal } from './modals/UserManagerModal.tsx';

interface NetworkSharesViewProps {
  node: NodeItem;
}

export const NetworkSharesView: React.FC<NetworkSharesViewProps> = ({ node }) => {
  const [loading, setLoading] = useState(false);
  const [shares, setShares] = useState<Array<{
    id: string;
    name: string;
    protocol: string;
    path: string;
    permissions: string;
    clientsCount: number;
    status: string;
  }>>([]);
  const [sambaConfig, setSambaConfig] = useState<string>('');
  const [notice, setNotice] = useState<string>('');
  const [showShareModal, setShowShareModal] = useState(false);
  const [editingShare, setEditingShare] = useState<{
    id?: string;
    name?: string;
    protocol?: string;
    path?: string;
    permissions?: string;
  } | null>(null);
  const [showUserModal, setShowUserModal] = useState(false);

  const fetchShares = (refresh = false) => {
    setLoading(true);
    fetch(`/api/nodes/${encodeURIComponent(node.id)}/shares${refresh ? '?refresh=true' : ''}`)
      .then(res => res.json())
      .then(data => {
        if (!data.success) {
          setNotice(`Could not fetch share configuration from ${node.shortName}.`);
          return;
        }

        const nfsRaw = data.nfs || '';
        const sambaRaw = data.samba || '';
        const zfsSharesRaw = data.zfsShares || '';
        const netConfRaw = data.netConf || '';
        const servicesRaw = data.services || '';

        const detectedShares: Array<{
          id: string;
          name: string;
          protocol: string;
          path: string;
          permissions: string;
          clientsCount: number;
          status: string;
        }> = [];
        const seenNames = new Set<string>();

        const parseSambaSection = (sec: string, source: string) => {
          const headerMatch = sec.match(/^\[([^\]]+)\]/);
          if (!headerMatch) return;
          const name = headerMatch[1].trim();
          if (name === 'global' || name === 'printers' || name === 'print$' || name === 'homes') return;
          if (seenNames.has(name.toLowerCase())) return;
          seenNames.add(name.toLowerCase());

          const pathMatch = sec.match(/path\s*=\s*(.+)/i);
          const path = pathMatch ? pathMatch[1].trim() : '(dynamic/homes)';
          const isReadonly = /read only\s*=\s*yes/i.test(sec);

          detectedShares.push({
            id: `smb-${name}`,
            name,
            protocol: source,
            path,
            permissions: isReadonly ? 'Read-only' : 'Read/Write',
            clientsCount: 0,
            status: servicesRaw.includes('active') ? 'Active (smbd)' : 'Exported',
          });
        };

        if (sambaRaw && !sambaRaw.includes('NO_SAMBA')) {
          setSambaConfig(sambaRaw);
          const sections = sambaRaw.split(/(?=\[[^\]]+\])/);
          for (const sec of sections) parseSambaSection(sec, 'Samba / SMB');
        }

        if (zfsSharesRaw && !zfsSharesRaw.includes('NO_ZFS_SHARES')) {
          if (!sambaConfig) setSambaConfig(zfsSharesRaw);
          const sections = zfsSharesRaw.split(/(?=\[[^\]]+\])/);
          for (const sec of sections) parseSambaSection(sec, 'Cockpit ZFS (Samba)');
        }

        if (netConfRaw && !netConfRaw.includes('NO_NET_CONF')) {
          if (!sambaConfig) setSambaConfig(netConfRaw);
          const sections = netConfRaw.split(/(?=\[[^\]]+\])/);
          for (const sec of sections) parseSambaSection(sec, 'Samba Registry');
        }

        if (nfsRaw && !nfsRaw.includes('NO_NFS')) {
          const lines = nfsRaw.split('\n');
          for (const line of lines) {
            const trimmed = line.trim();
            if (!trimmed || trimmed.startsWith('#')) continue;
            const parts = trimmed.split(/\s+/);
            if (parts.length >= 2) {
              const exportPath = parts[0];
              const client = parts.slice(1).join(' ');
              const name = exportPath.split('/').pop() || exportPath;
              if (!seenNames.has(name.toLowerCase())) {
                seenNames.add(name.toLowerCase());
                detectedShares.push({
                  id: `nfs-${name}`,
                  name,
                  protocol: 'NFS Export',
                  path: exportPath,
                  permissions: client.includes('ro') ? 'Read-only' : 'Read/Write',
                  clientsCount: 1,
                  status: servicesRaw.includes('active') ? 'Active (nfs-server)' : 'Exported',
                });
              }
            }
          }
        }

        if (detectedShares.length > 0) {
          setShares(detectedShares);
          setNotice('');
        } else {
          setShares([]);
          setNotice(`Host ${node.shortName} has no exported NFS or user-defined Samba shares configured.`);
        }
      })
      .catch(() => {
        setNotice(`Failed to connect to ${node.shortName} for shares probe.`);
      })
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    fetchShares(false);
  }, [node.id, node.shortName]);

  const [reloading, setReloading] = useState(false);
  const handleReloadShares = async () => {
    setReloading(true);
    try {
      const res = await fetch(`/api/nodes/${encodeURIComponent(node.id)}/shares/reload`, {
        method: 'POST',
      });
      if (res.ok) {
        fetchShares(true);
      }
    } catch {
      // error
    } finally {
      setReloading(false);
    }
  };

  return (
    <section className="bg-[#171a2d] rounded-lg p-4 flex flex-col border border-[#313447] shadow-lg">
      <div className="flex flex-wrap items-center justify-between gap-2 pb-3 border-b border-[#26293c]">
        <div className="flex items-center gap-2">
          <span className="material-symbols-outlined text-[#ffd97d] text-[22px]">share</span>
          <div>
            <h3 className="font-sans text-sm text-[#dfe1fa] font-bold">
              Network File Shares: NFS &amp; Samba ({node.shortName})
            </h3>
            <p className="font-mono text-[11px] text-[#8d909b]">
              Real-time exported shares, Cockpit file-sharing mounts, and smbd services
            </p>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2 font-mono text-xs">
          <button
            type="button"
            onClick={() => {
              setEditingShare(null);
              setShowShareModal(true);
            }}
            className="flex items-center gap-1 bg-[#b0c9ff] hover:bg-[#82aaff] text-[#0a0d1f] font-bold px-2.5 py-1 rounded transition-colors cursor-pointer shadow-xs"
          >
            <span className="material-symbols-outlined text-[15px]">add</span>
            <span>Add Share</span>
          </button>
          <button
            type="button"
            onClick={() => setShowUserModal(true)}
            className="flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#ffd97d] px-2.5 py-1 rounded transition-colors border border-[#ffd97d]/40 cursor-pointer"
          >
            <span className="material-symbols-outlined text-[15px]">manage_accounts</span>
            <span>Manage Users &amp; Passwords</span>
          </button>
          <button
            type="button"
            onClick={handleReloadShares}
            disabled={reloading || loading}
            className="flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] px-2.5 py-1 rounded transition-colors border border-[#434750]/40 cursor-pointer disabled:opacity-50"
          >
            <span className={`material-symbols-outlined text-[14px] ${reloading ? 'animate-spin' : ''}`}>refresh</span>
            <span>Reload Daemon</span>
          </button>
          <button
            type="button"
            onClick={() => fetchShares(true)}
            disabled={loading}
            className="flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] px-2.5 py-1 rounded transition-colors border border-[#434750]/40 cursor-pointer disabled:opacity-50"
          >
            <span className={`material-symbols-outlined text-[14px] ${loading ? 'animate-spin' : ''}`}>sync</span>
            <span>Refresh Shares</span>
          </button>
        </div>
      </div>

      {notice && (
        <div className="mt-3 bg-[#ffd97d]/10 border border-[#ffd97d]/30 rounded p-3 flex items-start gap-2.5 font-mono text-xs text-[#ffd97d]">
          <span className="material-symbols-outlined text-[18px] shrink-0 text-[#ffd97d]">
            info
          </span>
          <div>
            <span className="font-bold block">Share Configuration Status</span>
            <p className="text-[#dfe1fa] mt-0.5">{notice}</p>
          </div>
        </div>
      )}

      {shares.length > 0 ? (
        <div className="overflow-x-auto mt-3 font-mono text-xs">
          <table className="w-full text-left">
            <thead className="bg-[#0a0d1f] text-[#8d909b] text-[10px] uppercase">
              <tr className="h-7">
                <th className="px-3">Share Name</th>
                <th className="px-3">Protocol</th>
                <th className="px-3">Exported Path</th>
                <th className="px-3">Permissions</th>
                <th className="px-3">Service Status</th>
                <th className="px-3 text-right">Actions</th>
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
                  <td className="px-3">
                    <span className="px-1.5 py-0.2 rounded text-[10px] font-bold bg-[#8cd6cb]/20 text-[#8cd6cb] border border-[#8cd6cb]/30">
                      {share.status}
                    </span>
                  </td>
                  <td className="px-3 text-right">
                    <button
                      type="button"
                      onClick={() => {
                        setEditingShare(share);
                        setShowShareModal(true);
                      }}
                      className="p-1 hover:bg-[#35384c] text-[#b0c9ff] rounded transition-colors cursor-pointer"
                      title="Edit share configuration"
                    >
                      <span className="material-symbols-outlined text-[15px]">edit</span>
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <div className="bg-[#0a0d1f] p-4 rounded text-center font-mono text-xs text-[#8d909b] border border-[#26293c] mt-3">
          {loading ? 'Querying Samba & NFS exports on host...' : `No active network shares on ${node.shortName}.`}
        </div>
      )}

      {sambaConfig && (
        <div className="mt-4 bg-[#0a0d1f] p-3 rounded-lg border border-[#26293c] font-mono text-xs">
          <span className="text-[#ffd97d] font-bold block mb-1.5 flex items-center gap-1.5">
            <span className="material-symbols-outlined text-[15px]">settings</span>
            Samba /etc/samba/smb.conf Configuration ({node.shortName}):
          </span>
          <pre className="text-[#c3c6d2] whitespace-pre-wrap bg-[#171a2d] p-2.5 rounded border border-[#26293c]/60 leading-relaxed max-h-48 overflow-y-auto">
            {sambaConfig}
          </pre>
        </div>
      )}

      <ShareEditorModal
        node={node}
        isOpen={showShareModal}
        onClose={() => {
          setShowShareModal(false);
          setEditingShare(null);
        }}
        onSaved={() => fetchShares(true)}
        initialShare={editingShare}
      />

      <UserManagerModal
        node={node}
        isOpen={showUserModal}
        onClose={() => setShowUserModal(false)}
      />
    </section>
  );
};
