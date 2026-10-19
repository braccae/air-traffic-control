import React, { useState, useEffect } from 'react';
import type { NodeItem } from '../../types/index.ts';
import { getDriver, storageDriver, stageTaskInQueue } from '../../utils/drivers/index.ts';

interface ShareEditorModalProps {
  node: NodeItem;
  isOpen: boolean;
  isMinimized?: boolean;
  onMinimize?: () => void;
  onClose: () => void;
  onSaved?: () => void;
  initialShare?: {
    id?: string;
    name?: string;
    protocol?: string;
    path?: string;
    permissions?: string;
  } | null;
}

export const ShareEditorModal: React.FC<ShareEditorModalProps> = ({
  node,
  isOpen,
  isMinimized = false,
  onMinimize,
  onClose,
  onSaved,
  initialShare = null,
}) => {
  const isEditing = Boolean(initialShare?.name);
  const initialProtocol = initialShare?.protocol?.toLowerCase().includes('nfs') ? 'nfs' : 'smb';

  const [shareType, setShareType] = useState<'smb' | 'nfs'>(initialProtocol);

  // Common & SMB fields
  const [name, setName] = useState('');
  const [path, setPath] = useState('');
  const [comment, setComment] = useState('');
  const [readOnly, setReadOnly] = useState(false);
  const [guestOk, setGuestOk] = useState(false);
  const [validUsers, setValidUsers] = useState('');

  // NFS fields
  const [nfsClient, setNfsClient] = useState('*');
  const [nfsOptions, setNfsOptions] = useState('rw,sync,no_subtree_check,no_root_squash');

  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [successMsg, setSuccessMsg] = useState<string | null>(null);

  useEffect(() => {
    if (isOpen) {
      setError(null);
      setSuccessMsg(null);
      if (initialShare) {
        const isNfs = initialShare.protocol?.toLowerCase().includes('nfs');
        setShareType(isNfs ? 'nfs' : 'smb');
        setName(initialShare.name || '');
        setPath(initialShare.path || '');
        const isRo = initialShare.permissions?.toLowerCase().includes('read-only');
        setReadOnly(Boolean(isRo));
        setComment('');
        setGuestOk(false);
        setValidUsers('');
        setNfsClient('*');
        setNfsOptions(isRo ? 'ro,sync,no_subtree_check' : 'rw,sync,no_subtree_check,no_root_squash');
      } else {
        setShareType('smb');
        setName('');
        setPath('');
        setComment('');
        setReadOnly(false);
        setGuestOk(false);
        setValidUsers('');
        setNfsClient('*');
        setNfsOptions('rw,sync,no_subtree_check,no_root_squash');
      }
    }
  }, [isOpen, initialShare]);

  if (!isOpen) return null;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    setSuccessMsg(null);

    const trimmedPath = path.trim();
    if (!trimmedPath) {
      setError('Directory path is required.');
      return;
    }
    if (!trimmedPath.startsWith('/')) {
      setError('Directory path must be absolute (starting with "/").');
      return;
    }

    if (shareType === 'smb' && !name.trim()) {
      setError('SMB Share Name is required.');
      return;
    }

    setLoading(true);

    try {
      if (shareType === 'smb') {
        const res = await fetch(`/api/nodes/${encodeURIComponent(node.id)}/shares/smb`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            name: name.trim(),
            path: trimmedPath,
            comment: comment.trim() || undefined,
            readOnly,
            validUsers: validUsers.trim() || undefined,
            guestOk,
          }),
        });
        const data = await res.json();
        if (!res.ok || !data.success) {
          throw new Error(data.error || 'Failed to configure SMB share');
        }
        setSuccessMsg(`Samba share [${name.trim()}] configured and reloaded on ${node.shortName}.`);
      } else {
        const res = await fetch(`/api/nodes/${encodeURIComponent(node.id)}/shares/nfs`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            name: name.trim() || undefined,
            path: trimmedPath,
            client: nfsClient.trim() || '*',
            options: nfsOptions.trim() || undefined,
          }),
        });
        const data = await res.json();
        if (!res.ok || !data.success) {
          throw new Error(data.error || 'Failed to configure NFS export');
        }
        setSuccessMsg(`NFS export for "${trimmedPath}" exported and refreshed on ${node.shortName}.`);
      }

      setTimeout(() => {
        if (onSaved) onSaved();
        onClose();
      }, 700);
    } catch (err: any) {
      setError(err?.message || 'Failed to save network share');
    } finally {
      setLoading(false);
    }
  };

  const [staging, setStaging] = useState(false);

  const handleStageShare = async () => {
    setError(null);
    setSuccessMsg(null);

    const trimmedPath = path.trim();
    if (!trimmedPath) {
      setError('Directory path is required.');
      return;
    }
    if (!trimmedPath.startsWith('/')) {
      setError('Directory path must be absolute (starting with "/").');
      return;
    }
    if (shareType === 'smb' && !name.trim()) {
      setError('SMB Share Name is required.');
      return;
    }

    setStaging(true);
    try {
      const driver = getDriver('storage', node) || storageDriver;
      const task =
        shareType === 'smb'
          ? driver.compileMutation(node, 'smb', {
              name: name.trim(),
              path: trimmedPath,
              comment: comment.trim() || undefined,
              readOnly,
              validUsers: validUsers.trim() || undefined,
              guestOk,
            })
          : driver.compileMutation(node, 'nfs', {
              name: name.trim() || undefined,
              path: trimmedPath,
              client: nfsClient.trim() || '*',
              options: nfsOptions.trim() || undefined,
            });

      const res = await stageTaskInQueue(task);
      if (res.success) {
        if (onSaved) onSaved();
        onClose();
      } else {
        setError(res.error || 'Failed to stage share in queue');
      }
    } catch (err: any) {
      setError(err?.message || 'Failed to stage share in queue');
    } finally {
      setStaging(false);
    }
  };

  return (
    <div
      style={{ display: isMinimized ? 'none' : 'flex' }}
      className="fixed inset-0 z-50 items-center justify-center bg-black/75 backdrop-blur-xs p-4"
    >
      <div className="w-full max-w-lg bg-[#0a0d1f] rounded-lg shadow-2xl border border-[#313447] overflow-hidden flex flex-col max-h-[90vh]">
        {/* Header */}
        <div className="h-9 bg-[#171a2d] px-3 flex items-center justify-between border-b border-[#26293c] shrink-0">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[#ffd97d] text-[18px]">
              folder_shared
            </span>
            <span className="font-mono text-xs text-[#dfe1fa] font-bold">
              {isEditing ? `Edit Network Share: ${initialShare?.name}` : `Create Network Share on ${node.shortName}`}
            </span>
          </div>
          <div className="flex items-center gap-1">
            {onMinimize && (
              <button
                type="button"
                onClick={onMinimize}
                className="text-[#8d909b] hover:text-[#dfe1fa] p-1 cursor-pointer transition-colors"
                title="Minimize"
              >
                <span className="material-symbols-outlined text-[16px]">remove</span>
              </button>
            )}
            <button
              type="button"
              onClick={onClose}
              className="text-[#8d909b] hover:text-[#dfe1fa] p-1 cursor-pointer transition-colors"
              title="Close"
            >
              <span className="material-symbols-outlined text-[16px]">close</span>
            </button>
          </div>
        </div>

        {/* Modal Form */}
        <form onSubmit={handleSubmit} className="p-4 space-y-4 font-mono text-xs overflow-y-auto">
          {/* Protocol Selector Tabs */}
          <div>
            <label className="block text-[#8d909b] mb-1.5 font-bold uppercase tracking-wide text-[10px]">
              Share Protocol
            </label>
            <div className="grid grid-cols-2 gap-2 bg-[#171a2d] p-1 rounded border border-[#26293c]">
              <button
                type="button"
                onClick={() => setShareType('smb')}
                className={`py-1.5 px-3 rounded font-bold cursor-pointer transition-all flex items-center justify-center gap-1.5 ${
                  shareType === 'smb'
                    ? 'bg-[#b0c9ff] text-[#0a0d1f] shadow'
                    : 'text-[#8d909b] hover:text-[#dfe1fa]'
                }`}
              >
                <span className="material-symbols-outlined text-[15px]">folder_special</span>
                <span>Samba (SMB / CIFS)</span>
              </button>
              <button
                type="button"
                onClick={() => setShareType('nfs')}
                className={`py-1.5 px-3 rounded font-bold cursor-pointer transition-all flex items-center justify-center gap-1.5 ${
                  shareType === 'nfs'
                    ? 'bg-[#ffd97d] text-[#0a0d1f] shadow'
                    : 'text-[#8d909b] hover:text-[#dfe1fa]'
                }`}
              >
                <span className="material-symbols-outlined text-[15px]">lan</span>
                <span>NFS Export</span>
              </button>
            </div>
          </div>

          {/* Directory Path (common to both) */}
          <div>
            <label className="block text-[#8d909b] mb-1">
              EXPORTED DIRECTORY PATH <span className="text-[#ffb4ab]">*</span>
            </label>
            <input
              type="text"
              required
              value={path}
              onChange={e => setPath(e.target.value)}
              placeholder="/tank/data or /mnt/storage"
              className="w-full bg-[#171a2d] border border-[#313447] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:outline-hidden focus:border-[#b0c9ff]"
            />
            <span className="text-[10px] text-[#8d909b] mt-0.5 block">
              Absolute path to the directory on {node.shortName}
            </span>
          </div>

          {/* Protocol-specific fields */}
          {shareType === 'smb' ? (
            <>
              {/* Share Name */}
              <div>
                <label className="block text-[#8d909b] mb-1">
                  SAMBA SHARE NAME <span className="text-[#ffb4ab]">*</span>
                </label>
                <input
                  type="text"
                  required
                  value={name}
                  onChange={e => setName(e.target.value)}
                  placeholder="e.g. Media, Backups, Public"
                  className="w-full bg-[#171a2d] border border-[#313447] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:outline-hidden focus:border-[#b0c9ff]"
                />
                <span className="text-[10px] text-[#8d909b] mt-0.5 block">
                  Windows UNC name (e.g. \\{node.shortName}\{name || 'ShareName'})
                </span>
              </div>

              {/* Comment / Description */}
              <div>
                <label className="block text-[#8d909b] mb-1">COMMENT / DESCRIPTION</label>
                <input
                  type="text"
                  value={comment}
                  onChange={e => setComment(e.target.value)}
                  placeholder="e.g. Media repository for Cockpit cluster"
                  className="w-full bg-[#171a2d] border border-[#313447] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:outline-hidden focus:border-[#b0c9ff]"
                />
              </div>

              {/* Valid Users */}
              <div>
                <label className="block text-[#8d909b] mb-1">VALID USERS (OPTIONAL)</label>
                <input
                  type="text"
                  value={validUsers}
                  onChange={e => setValidUsers(e.target.value)}
                  placeholder="e.g. core apps @wheel (blank for all registered users)"
                  className="w-full bg-[#171a2d] border border-[#313447] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:outline-hidden focus:border-[#b0c9ff]"
                />
              </div>

              {/* Toggles: Read-only & Guest access */}
              <div className="grid grid-cols-2 gap-3 pt-1">
                <label className="flex items-center gap-2 bg-[#171a2d] p-2.5 rounded border border-[#26293c] cursor-pointer hover:bg-[#20243a]">
                  <input
                    type="checkbox"
                    checked={readOnly}
                    onChange={e => setReadOnly(e.target.checked)}
                    className="rounded border-[#434750] text-[#b0c9ff] focus:ring-0"
                  />
                  <div>
                    <span className="font-bold text-[#dfe1fa] block">Read-Only</span>
                    <span className="text-[10px] text-[#8d909b]">Deny write access</span>
                  </div>
                </label>

                <label className="flex items-center gap-2 bg-[#171a2d] p-2.5 rounded border border-[#26293c] cursor-pointer hover:bg-[#20243a]">
                  <input
                    type="checkbox"
                    checked={guestOk}
                    onChange={e => setGuestOk(e.target.checked)}
                    className="rounded border-[#434750] text-[#b0c9ff] focus:ring-0"
                  />
                  <div>
                    <span className="font-bold text-[#dfe1fa] block">Guest Access</span>
                    <span className="text-[10px] text-[#8d909b]">Allow anonymous login</span>
                  </div>
                </label>
              </div>
            </>
          ) : (
            <>
              {/* Export Identifier Name (for config file reference) */}
              <div>
                <label className="block text-[#8d909b] mb-1">EXPORT NAME IDENTIFIER</label>
                <input
                  type="text"
                  value={name}
                  onChange={e => setName(e.target.value)}
                  placeholder="e.g. tank_media or nfs_backup"
                  className="w-full bg-[#171a2d] border border-[#313447] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:outline-hidden focus:border-[#b0c9ff]"
                />
                <span className="text-[10px] text-[#8d909b] mt-0.5 block">
                  Used for config filename `/etc/exports.d/cockpit-&lt;name&gt;.exports`
                </span>
              </div>

              {/* Allowed Clients / CIDR */}
              <div>
                <label className="block text-[#8d909b] mb-1">
                  ALLOWED CLIENTS / NETWORK CIDR <span className="text-[#ffb4ab]">*</span>
                </label>
                <input
                  type="text"
                  required
                  value={nfsClient}
                  onChange={e => setNfsClient(e.target.value)}
                  placeholder="* or 100.64.0.0/10 or 192.168.1.0/24"
                  className="w-full bg-[#171a2d] border border-[#313447] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:outline-hidden focus:border-[#b0c9ff]"
                />
                <span className="text-[10px] text-[#8d909b] mt-0.5 block">
                  Use `*` for all clients, or specify Tailscale/LAN CIDR subnet.
                </span>
              </div>

              {/* Permissions & Options */}
              <div>
                <label className="block text-[#8d909b] mb-1">EXPORT PERMISSIONS &amp; FLAGS</label>
                <input
                  type="text"
                  value={nfsOptions}
                  onChange={e => setNfsOptions(e.target.value)}
                  placeholder="rw,sync,no_subtree_check,no_root_squash"
                  className="w-full bg-[#171a2d] border border-[#313447] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:outline-hidden focus:border-[#b0c9ff]"
                />
                <div className="flex gap-1.5 mt-1.5">
                  <button
                    type="button"
                    onClick={() => setNfsOptions('rw,sync,no_subtree_check,no_root_squash')}
                    className="px-2 py-0.5 bg-[#171a2d] hover:bg-[#26293c] text-[#8cd6cb] rounded border border-[#8cd6cb]/30 text-[10px] cursor-pointer"
                  >
                    Preset: Read/Write (Root)
                  </button>
                  <button
                    type="button"
                    onClick={() => setNfsOptions('ro,sync,no_subtree_check')}
                    className="px-2 py-0.5 bg-[#171a2d] hover:bg-[#26293c] text-[#b0c9ff] rounded border border-[#b0c9ff]/30 text-[10px] cursor-pointer"
                  >
                    Preset: Read-Only
                  </button>
                  <button
                    type="button"
                    onClick={() => setNfsOptions('rw,sync,all_squash,anonuid=1000,anongid=1000')}
                    className="px-2 py-0.5 bg-[#171a2d] hover:bg-[#26293c] text-[#ffd97d] rounded border border-[#ffd97d]/30 text-[10px] cursor-pointer"
                  >
                    Preset: All Squash
                  </button>
                </div>
              </div>
            </>
          )}

          {/* Feedback Alerts */}
          {error && (
            <div className="bg-[#ffb4ab]/10 border border-[#ffb4ab]/30 rounded p-2.5 flex items-start gap-2 text-[#ffb4ab]">
              <span className="material-symbols-outlined text-[16px] shrink-0">error</span>
              <p className="text-xs break-all">{error}</p>
            </div>
          )}

          {successMsg && (
            <div className="bg-[#8cd6cb]/10 border border-[#8cd6cb]/30 rounded p-2.5 flex items-start gap-2 text-[#8cd6cb]">
              <span className="material-symbols-outlined text-[16px] shrink-0">check_circle</span>
              <p className="text-xs">{successMsg}</p>
            </div>
          )}

          {/* Footer Controls */}
          <div className="pt-2 border-t border-[#26293c] flex items-center justify-end gap-2">
            <button
              type="button"
              onClick={onClose}
              disabled={loading || staging}
              className="px-3 py-1.5 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] rounded border border-[#434750]/40 cursor-pointer disabled:opacity-50"
            >
              Cancel
            </button>
            <button
              type="button"
              disabled={loading || staging}
              onClick={handleStageShare}
              className="px-3 py-1.5 bg-[#171a2d] hover:bg-[#26293c] text-[#ffd97d] border border-[#ffd97d]/40 rounded cursor-pointer font-bold flex items-center gap-1.5 transition-colors disabled:opacity-50"
              title="Stage share mutation into Ansible Mutation Queue without immediate execution"
            >
              <span className="material-symbols-outlined text-[15px]">queue</span>
              <span>{staging ? 'Staging...' : 'Stage in Queue'}</span>
            </button>
            <button
              type="submit"
              disabled={loading || staging}
              className="px-4 py-1.5 bg-[#b0c9ff] hover:bg-[#82aaff] text-[#0a0d1f] font-bold rounded cursor-pointer flex items-center gap-1.5 disabled:opacity-50 transition-colors"
            >
              {loading && <span className="material-symbols-outlined text-[14px] animate-spin">refresh</span>}
              <span>{isEditing ? 'Save Changes' : 'Create Share'}</span>
            </button>
          </div>
        </form>
      </div>
    </div>
  );
};
