import React, { useEffect, useState } from 'react';
import type { NodeItem, ServiceUser, UnprivilegedPodmanUser } from '../types/index.ts';
import { correlateIdentitiesWithProbe } from '../utils/podmanViewModel.ts';

interface ServiceIdentitiesTableProps {
  selectedNode?: NodeItem;
  onAddIdentity?: () => void;
  onAudit?: (user: string) => void;
}

const fallbackIdentities: ServiceUser[] = [
  {
    user: 'core',
    uidGid: '1000:1000',
    rolesGroups: 'wheel, sudo, systemd-journal',
    authMechanism: 'ssh-ed25519 (ssh-agent)',
    lingerSubUid: 'Linger: Enabled (100000:65536)',
    icon: 'badge',
  },
];

export const ServiceIdentitiesTable: React.FC<ServiceIdentitiesTableProps> = ({
  selectedNode,
  onAudit,
}) => {
  const [probedUsers, setProbedUsers] = useState<UnprivilegedPodmanUser[]>([]);
  const [hostIdentities, setHostIdentities] = useState<ServiceUser[]>(fallbackIdentities);
  const [probeStatus, setProbeStatus] = useState<string>('Live Probe Active');
  const [lockedUsers, setLockedUsers] = useState<Record<string, boolean>>({});

  // Inline Add Service Identity form
  const [showAddForm, setShowAddForm] = useState(false);
  const [newUser, setNewUser] = useState('');
  const [newUid, setNewUid] = useState('2900');
  const [newGroups, setNewGroups] = useState('podman, service-identity');
  const [newSubUid, setNewSubUid] = useState('655360:65536');

  const hostParam = selectedNode?.id || selectedNode?.tailscaleHost || 'likes-it-cold';

  const runLiveProbe = () => {
    setProbeStatus('Probing SSH...');
    fetch(`/api/nodes/${encodeURIComponent(hostParam)}/containers`)
      .then(res => res.json())
      .then(data => {
        if (Array.isArray(data?.probedUsers)) {
          setProbedUsers(data.probedUsers);
          setProbeStatus(
            `Probed ${selectedNode?.shortName || 'host'} (${data.probedUsers.length} rootless active)`
          );
        }
        if (Array.isArray(data?.identities) && data.identities.length > 0) {
          setHostIdentities(data.identities);
        }
      })
      .catch(() => {
        setProbeStatus('Probe Cached');
      });
  };

  useEffect(() => {
    runLiveProbe();
  }, [hostParam]);

  const handleAddIdentitySubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!newUser.trim()) return;
    const payload = {
      user: newUser.trim(),
      uidGid: `${newUid}:${newUid}`,
      rolesGroups: newGroups,
      authMechanism: `Rootless SubUID ${newSubUid}`,
      lingerSubUid: `Linger: Enabled (${newSubUid})`,
    };
    fetch(`/api/nodes/${encodeURIComponent(hostParam)}/identities`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    })
      .then(res => res.json())
      .then(data => {
        if (data?.identity) {
          setHostIdentities(prev => [data.identity, ...prev]);
        }
      })
      .catch(() => {
        setHostIdentities(prev => [
          { ...payload, icon: 'deployed_code' },
          ...prev,
        ]);
      });

    setNewUser('');
    setShowAddForm(false);
  };

  const correlatedIdentities = correlateIdentitiesWithProbe(hostIdentities, probedUsers);

  return (
    <section className="bg-[#171a2d] rounded p-3 flex flex-col border border-[#26293c]">
      <div className="flex flex-wrap items-center justify-between gap-2 pb-1.5 bg-[#0a0d1f]/40 px-2 py-1 rounded border border-[#26293c]/40">
        <div className="flex items-center gap-2">
          <span className="material-symbols-outlined text-[#d9b9ff] text-[18px]">
            manage_accounts
          </span>
          <span className="font-sans text-sm text-[#dfe1fa] font-semibold">
            Service Users &amp; System Identities ({selectedNode?.shortName || 'host'})
          </span>
          <span className="px-1.5 py-0.5 bg-[#8cd6cb]/15 text-[#8cd6cb] rounded font-mono text-[10px] border border-[#8cd6cb]/30">
            {probeStatus}
          </span>
        </div>
        <div className="flex items-center gap-1.5">
          <button
            type="button"
            onClick={runLiveProbe}
            className="flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#8cd6cb] px-2.5 py-1 rounded font-mono text-xs border border-[#434750]/40 transition-colors cursor-pointer"
          >
            <span className="material-symbols-outlined text-[13px]">radar</span>
            <span>Probe Rootless Podman</span>
          </button>
          <button
            type="button"
            onClick={() => setShowAddForm(prev => !prev)}
            className="flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#d9b9ff] px-2.5 py-1 rounded font-mono text-xs border border-[#434750]/40 transition-colors cursor-pointer"
          >
            <span className="material-symbols-outlined text-[13px]">person_add</span>
            <span>+ Add Service Identity</span>
          </button>
        </div>
      </div>

      {showAddForm && (
        <form
          onSubmit={handleAddIdentitySubmit}
          className="mt-2 p-2.5 bg-[#0a0d1f] rounded border border-[#d9b9ff]/40 grid grid-cols-1 md:grid-cols-5 gap-2 font-mono text-xs items-end"
        >
          <div>
            <label className="block text-[10px] text-[#8d909b] mb-0.5">USERNAME</label>
            <input
              type="text"
              required
              value={newUser}
              onChange={e => setNewUser(e.target.value)}
              placeholder="e.g. svc-metrics"
              className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2 py-1 text-[#dfe1fa]"
            />
          </div>
          <div>
            <label className="block text-[10px] text-[#8d909b] mb-0.5">UID / GID</label>
            <input
              type="text"
              value={newUid}
              onChange={e => setNewUid(e.target.value)}
              className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2 py-1 text-[#dfe1fa]"
            />
          </div>
          <div>
            <label className="block text-[10px] text-[#8d909b] mb-0.5">GROUPS</label>
            <input
              type="text"
              value={newGroups}
              onChange={e => setNewGroups(e.target.value)}
              className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2 py-1 text-[#dfe1fa]"
            />
          </div>
          <div>
            <label className="block text-[10px] text-[#8d909b] mb-0.5">SUBUID RANGE</label>
            <input
              type="text"
              value={newSubUid}
              onChange={e => setNewSubUid(e.target.value)}
              className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2 py-1 text-[#8cd6cb]"
            />
          </div>
          <div className="flex gap-1.5">
            <button
              type="submit"
              className="px-3 py-1 bg-[#d9b9ff] text-[#280056] font-bold rounded cursor-pointer"
            >
              Add User
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

      <div className="overflow-x-auto mt-2 font-mono text-xs">
        <table className="w-full text-left">
          <thead className="bg-[#0a0d1f] text-[#8d909b] text-[10px]">
            <tr className="h-6">
              <th className="px-2">User / Identity</th>
              <th className="px-2">UID:GID</th>
              <th className="px-2">Rootless Podman</th>
              <th className="px-2">Role / Groups</th>
              <th className="px-2">Authentication / SubUID</th>
              <th className="px-2">Linger State</th>
              <th className="px-2 text-right">Actions</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[#26293c]/50">
            {correlatedIdentities.map(id => {
              const isLocked = Boolean(lockedUsers[id.user]);
              return (
                <tr key={id.user} className="h-8 hover:bg-[#26293c]/40 transition-colors">
                  <td className="px-2 text-[#dfe1fa] font-bold flex items-center gap-1.5">
                    <span className="material-symbols-outlined text-[15px] text-[#b0c9ff]">
                      {id.icon || 'deployed_code'}
                    </span>
                    <span>{id.user}</span>
                    {isLocked && (
                      <span className="px-1 py-0.2 bg-[#ffb4ab]/20 text-[#ffb4ab] text-[9px] rounded">
                        LOCKED
                      </span>
                    )}
                  </td>
                  <td className="px-2 text-[#8d909b]">{id.uidGid}</td>
                  <td className="px-2">
                    {id.isRunningPodman ? (
                      <span className="px-1.5 py-0.5 bg-[#8cd6cb]/20 text-[#8cd6cb] rounded text-[10px] border border-[#8cd6cb]/30">
                        Active ({id.containerCount} containers)
                      </span>
                    ) : (
                      <span className="text-[#8d909b] text-[10px]">Idle (0)</span>
                    )}
                  </td>
                  <td className="px-2 text-[#d9b9ff]">{id.rolesGroups}</td>
                  <td className="px-2 text-[#8cd6cb]">{id.authMechanism}</td>
                  <td
                    className={`px-2 ${
                      id.lingerSubUid.includes('Enabled')
                        ? 'text-[#8cd6cb] font-bold'
                        : 'text-[#c3c6d2]'
                    }`}
                  >
                    {id.lingerSubUid}
                  </td>
                  <td className="px-2 text-right space-x-1">
                    <button
                      type="button"
                      onClick={() => onAudit?.(`pam-audit-${id.user}`)}
                      className="px-1.5 py-0.5 bg-[#26293c] hover:bg-[#35384c] text-[#8cd6cb] rounded text-[10px] cursor-pointer"
                    >
                      Audit
                    </button>
                    <button
                      type="button"
                      onClick={() =>
                        setLockedUsers(prev => ({ ...prev, [id.user]: !prev[id.user] }))
                      }
                      className="px-1.5 py-0.5 bg-[#26293c] hover:bg-[#35384c] text-[#8d909b] rounded text-[10px] cursor-pointer"
                    >
                      {isLocked ? 'Unlock' : 'Lock'}
                    </button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </section>
  );
};
