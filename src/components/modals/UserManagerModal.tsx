import React, { useState, useEffect } from 'react';
import type { NodeItem } from '../../types/index.ts';
import type { HostUserItem } from '../../utils/sharesManager.ts';

interface UserManagerModalProps {
  node: NodeItem;
  isOpen: boolean;
  isMinimized?: boolean;
  onMinimize?: () => void;
  onClose: () => void;
}

export const UserManagerModal: React.FC<UserManagerModalProps> = ({
  node,
  isOpen,
  isMinimized = false,
  onMinimize,
  onClose,
}) => {
  const [users, setUsers] = useState<HostUserItem[]>([]);
  const [allUsers, setAllUsers] = useState<HostUserItem[]>([]);
  const [showSystemUsers, setShowSystemUsers] = useState(false);
  const [searchQuery, setSearchQuery] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [successMsg, setSuccessMsg] = useState<string | null>(null);

  // Create User State
  const [showCreateForm, setShowCreateForm] = useState(false);
  const [newUsername, setNewUsername] = useState('');
  const [createLoading, setCreateLoading] = useState(false);

  // Set Password State
  const [targetPasswordUser, setTargetPasswordUser] = useState<string | null>(null);
  const [passwordType, setPasswordType] = useState<'system' | 'samba'>('system');
  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [passwordLoading, setPasswordLoading] = useState(false);

  const fetchUsers = async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await fetch(`/api/nodes/${encodeURIComponent(node.id)}/users`);
      const data = await res.json();
      if (!res.ok || !data.success) {
        throw new Error(data.error || 'Failed to fetch host users');
      }
      setUsers(data.users || []);
      setAllUsers(data.allUsers || []);
    } catch (err: any) {
      setError(err?.message || 'Could not load users from target host');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (isOpen) {
      setError(null);
      setSuccessMsg(null);
      setShowCreateForm(false);
      setTargetPasswordUser(null);
      fetchUsers();
    }
  }, [isOpen, node.id]);

  if (!isOpen) return null;

  const handleCreateUser = async (e: React.FormEvent) => {
    e.preventDefault();
    const username = newUsername.trim();
    if (!username) return;

    setCreateLoading(true);
    setError(null);
    setSuccessMsg(null);

    try {
      const res = await fetch(`/api/nodes/${encodeURIComponent(node.id)}/users/create`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username }),
      });
      const data = await res.json();
      if (!res.ok || !data.success) {
        throw new Error(data.error || 'Failed to create user');
      }
      setSuccessMsg(`User "${username}" created with home directory and /bin/bash.`);
      setNewUsername('');
      setShowCreateForm(false);
      await fetchUsers();
    } catch (err: any) {
      setError(err?.message || 'Error creating user');
    } finally {
      setCreateLoading(false);
    }
  };

  const handleSetPassword = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!targetPasswordUser) return;
    if (password !== confirmPassword) {
      setError('Passwords do not match.');
      return;
    }
    if (!password) {
      setError('Password cannot be empty.');
      return;
    }

    setPasswordLoading(true);
    setError(null);
    setSuccessMsg(null);

    try {
      const endpoint =
        passwordType === 'system'
          ? `/api/nodes/${encodeURIComponent(node.id)}/users/${encodeURIComponent(targetPasswordUser)}/password`
          : `/api/nodes/${encodeURIComponent(node.id)}/users/${encodeURIComponent(targetPasswordUser)}/smbpassword`;

      const res = await fetch(endpoint, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ password }),
      });
      const data = await res.json();
      if (!res.ok || !data.success) {
        throw new Error(data.error || `Failed to set ${passwordType} password`);
      }
      setSuccessMsg(
        passwordType === 'system'
          ? `System password updated successfully for ${targetPasswordUser}.`
          : `Samba (SMB) password set successfully for ${targetPasswordUser}.`
      );
      setTargetPasswordUser(null);
      setPassword('');
      setConfirmPassword('');
    } catch (err: any) {
      setError(err?.message || 'Error updating password');
    } finally {
      setPasswordLoading(false);
    }
  };

  const displayedUsers = (showSystemUsers ? allUsers : users).filter(u => {
    if (!searchQuery) return true;
    const q = searchQuery.toLowerCase();
    return (
      u.username.toLowerCase().includes(q) ||
      u.home.toLowerCase().includes(q) ||
      u.uid.toString().includes(q)
    );
  });

  return (
    <div
      style={{ display: isMinimized ? 'none' : 'flex' }}
      className="fixed inset-0 z-50 items-center justify-center bg-black/75 backdrop-blur-xs p-4"
    >
      <div className="w-full max-w-2xl bg-[#0a0d1f] rounded-lg shadow-2xl border border-[#313447] overflow-hidden flex flex-col max-h-[90vh]">
        {/* Header */}
        <div className="h-9 bg-[#171a2d] px-3 flex items-center justify-between border-b border-[#26293c] shrink-0">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[#ffd97d] text-[18px]">
              manage_accounts
            </span>
            <span className="font-mono text-xs text-[#dfe1fa] font-bold">
              Host Users &amp; Passwords ({node.shortName})
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

        {/* Toolbar */}
        <div className="p-3 bg-[#111425] border-b border-[#26293c] flex flex-wrap items-center justify-between gap-2 shrink-0 font-mono text-xs">
          <div className="flex items-center gap-2">
            <input
              type="text"
              value={searchQuery}
              onChange={e => setSearchQuery(e.target.value)}
              placeholder="Search users / UID..."
              className="bg-[#171a2d] border border-[#313447] rounded px-2.5 py-1 text-[#dfe1fa] text-xs focus:outline-hidden focus:border-[#b0c9ff] w-48"
            />
            <label className="flex items-center gap-1.5 text-[#8d909b] hover:text-[#dfe1fa] cursor-pointer text-[11px] ml-2 select-none">
              <input
                type="checkbox"
                checked={showSystemUsers}
                onChange={e => setShowSystemUsers(e.target.checked)}
                className="rounded border-[#434750] text-[#b0c9ff] focus:ring-0"
              />
              <span>Show System Accounts (UID &lt; 1000)</span>
            </label>
          </div>

          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => fetchUsers()}
              disabled={loading}
              className="p-1 text-[#8d909b] hover:text-[#dfe1fa] cursor-pointer"
              title="Refresh Users"
            >
              <span className={`material-symbols-outlined text-[16px] ${loading ? 'animate-spin' : ''}`}>
                sync
              </span>
            </button>
            <button
              type="button"
              onClick={() => setShowCreateForm(!showCreateForm)}
              className="flex items-center gap-1 bg-[#b0c9ff] hover:bg-[#82aaff] text-[#0a0d1f] font-bold px-2.5 py-1 rounded cursor-pointer transition-colors"
            >
              <span className="material-symbols-outlined text-[14px]">person_add</span>
              <span>Create User</span>
            </button>
          </div>
        </div>

        {/* Create User Dropdown Form */}
        {showCreateForm && (
          <form
            onSubmit={handleCreateUser}
            className="p-3 bg-[#171a2d] border-b border-[#26293c] flex items-center gap-2 font-mono text-xs shrink-0"
          >
            <span className="text-[#ffd97d] font-bold flex items-center gap-1 shrink-0">
              <span className="material-symbols-outlined text-[15px]">add</span>
              New User:
            </span>
            <input
              type="text"
              required
              value={newUsername}
              onChange={e => setNewUsername(e.target.value)}
              placeholder="username (e.g. storage_user)"
              className="flex-1 bg-[#0a0d1f] border border-[#313447] rounded px-2.5 py-1 text-[#dfe1fa] focus:outline-hidden focus:border-[#b0c9ff]"
            />
            <button
              type="submit"
              disabled={createLoading}
              className="bg-[#ffd97d] hover:bg-[#f6c860] text-[#0a0d1f] font-bold px-3 py-1 rounded cursor-pointer disabled:opacity-50"
            >
              {createLoading ? 'Creating...' : 'Create'}
            </button>
            <button
              type="button"
              onClick={() => setShowCreateForm(false)}
              className="text-[#8d909b] hover:text-[#dfe1fa] px-2 py-1"
            >
              Cancel
            </button>
          </form>
        )}

        {/* Set Password Modal / Sub-form */}
        {targetPasswordUser && (
          <form
            onSubmit={handleSetPassword}
            className="p-3 bg-[#1c2035] border-b border-[#313447] space-y-2.5 font-mono text-xs shrink-0"
          >
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-1.5 font-bold text-[#b0c9ff]">
                <span className="material-symbols-outlined text-[16px]">lock_reset</span>
                <span>
                  Set {passwordType === 'system' ? 'System Login' : 'Samba (SMB)'} Password for: &quot;{targetPasswordUser}&quot;
                </span>
              </div>
              <button
                type="button"
                onClick={() => setTargetPasswordUser(null)}
                className="text-[#8d909b] hover:text-[#dfe1fa] text-[16px]"
              >
                ✕
              </button>
            </div>

            <div className="grid grid-cols-2 gap-2">
              <div>
                <label className="block text-[#8d909b] text-[10px] uppercase mb-0.5">NEW PASSWORD</label>
                <input
                  type="password"
                  required
                  value={password}
                  onChange={e => setPassword(e.target.value)}
                  placeholder="Enter password..."
                  className="w-full bg-[#0a0d1f] border border-[#313447] rounded px-2 py-1 text-[#dfe1fa] focus:outline-hidden focus:border-[#b0c9ff]"
                />
              </div>
              <div>
                <label className="block text-[#8d909b] text-[10px] uppercase mb-0.5">CONFIRM PASSWORD</label>
                <input
                  type="password"
                  required
                  value={confirmPassword}
                  onChange={e => setConfirmPassword(e.target.value)}
                  placeholder="Confirm password..."
                  className="w-full bg-[#0a0d1f] border border-[#313447] rounded px-2 py-1 text-[#dfe1fa] focus:outline-hidden focus:border-[#b0c9ff]"
                />
              </div>
            </div>

            <div className="flex items-center justify-end gap-2 pt-1">
              <button
                type="button"
                onClick={() => setTargetPasswordUser(null)}
                className="px-2.5 py-0.5 bg-[#26293c] text-[#dfe1fa] rounded border border-[#434750]/40 text-xs"
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={passwordLoading}
                className="px-3 py-0.5 bg-[#b0c9ff] hover:bg-[#82aaff] text-[#0a0d1f] font-bold rounded text-xs cursor-pointer flex items-center gap-1 disabled:opacity-50"
              >
                {passwordLoading && <span className="material-symbols-outlined text-[12px] animate-spin">refresh</span>}
                <span>Set Password</span>
              </button>
            </div>
          </form>
        )}

        {/* Feedback Alerts */}
        {error && (
          <div className="m-3 mb-0 bg-[#ffb4ab]/10 border border-[#ffb4ab]/30 rounded p-2.5 flex items-start gap-2 text-[#ffb4ab] font-mono text-xs">
            <span className="material-symbols-outlined text-[16px] shrink-0">error</span>
            <p className="break-all">{error}</p>
          </div>
        )}

        {successMsg && (
          <div className="m-3 mb-0 bg-[#8cd6cb]/10 border border-[#8cd6cb]/30 rounded p-2.5 flex items-start gap-2 text-[#8cd6cb] font-mono text-xs">
            <span className="material-symbols-outlined text-[16px] shrink-0">check_circle</span>
            <p>{successMsg}</p>
          </div>
        )}

        {/* Users List Table */}
        <div className="p-3 overflow-y-auto flex-1 font-mono text-xs">
          {displayedUsers.length > 0 ? (
            <table className="w-full text-left">
              <thead className="bg-[#171a2d] text-[#8d909b] text-[10px] uppercase">
                <tr className="h-7">
                  <th className="px-2.5">User</th>
                  <th className="px-2">UID:GID</th>
                  <th className="px-2">Home Dir</th>
                  <th className="px-2">Shell</th>
                  <th className="px-2 text-right">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-[#26293c]/60">
                {displayedUsers.map(u => (
                  <tr key={u.username} className="h-9 hover:bg-[#26293c]/40 transition-colors">
                    <td className="px-2.5 font-bold text-[#b0c9ff] flex items-center gap-1.5 pt-2">
                      <span className="material-symbols-outlined text-[15px] text-[#8d909b]">
                        {u.isSystemUser ? 'account_circle' : 'person'}
                      </span>
                      <span>{u.username}</span>
                      {u.isSystemUser && (
                        <span className="px-1 text-[9px] bg-[#434750]/30 text-[#8d909b] rounded">sys</span>
                      )}
                    </td>
                    <td className="px-2 text-[#dfe1fa]">
                      {u.uid}:{u.gid}
                    </td>
                    <td className="px-2 text-[#8d909b] truncate max-w-[130px]" title={u.home}>
                      {u.home}
                    </td>
                    <td className="px-2 text-[#8d909b] truncate max-w-[100px]" title={u.shell}>
                      {u.shell}
                    </td>
                    <td className="px-2 text-right">
                      <div className="flex items-center justify-end gap-1.5">
                        <button
                          type="button"
                          onClick={() => {
                            setTargetPasswordUser(u.username);
                            setPasswordType('system');
                            setPassword('');
                            setConfirmPassword('');
                          }}
                          className="px-2 py-0.5 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] rounded border border-[#434750]/40 text-[10px] cursor-pointer"
                          title="Set regular Linux passwd"
                        >
                          Passwd
                        </button>
                        <button
                          type="button"
                          onClick={() => {
                            setTargetPasswordUser(u.username);
                            setPasswordType('samba');
                            setPassword('');
                            setConfirmPassword('');
                          }}
                          className="px-2 py-0.5 bg-[#ffd97d]/15 hover:bg-[#ffd97d]/25 text-[#ffd97d] rounded border border-[#ffd97d]/40 text-[10px] cursor-pointer"
                          title="Set Samba smbpasswd"
                        >
                          SMB Passwd
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <div className="bg-[#171a2d] p-6 rounded text-center text-[#8d909b]">
              {loading ? 'Reading user database from host...' : 'No users match criteria.'}
            </div>
          )}
        </div>

        {/* Footer */}
        <div className="h-10 bg-[#171a2d] px-3 flex items-center justify-between border-t border-[#26293c] shrink-0 font-mono text-xs">
          <span className="text-[#8d909b] text-[11px]">
            {displayedUsers.length} user(s) displayed
          </span>
          <button
            type="button"
            onClick={onClose}
            className="px-3 py-1 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] rounded border border-[#434750]/40 cursor-pointer"
          >
            Close
          </button>
        </div>
      </div>
    </div>
  );
};
