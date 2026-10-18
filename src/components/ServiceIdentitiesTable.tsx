import React from 'react';
import type { ServiceUser } from '../types/index.ts';

interface ServiceIdentitiesTableProps {
  onAddIdentity?: () => void;
  onAudit?: (user: string) => void;
}

export const ServiceIdentitiesTable: React.FC<ServiceIdentitiesTableProps> = ({
  onAddIdentity,
  onAudit,
}) => {
  const identities: ServiceUser[] = [
    {
      user: 'deployer',
      uidGid: '1000:1000',
      rolesGroups: 'wheel, sudo, docker',
      authMechanism: 'ssh-ed25519 (Ansible Vault managed)',
      lingerSubUid: 'Linger: Yes',
      icon: 'badge',
    },
    {
      user: 'restic-svc',
      uidGid: '984:984',
      rolesGroups: 'backup-operators',
      authMechanism: '/sbin/nologin (systemd-timer)',
      lingerSubUid: 'Linger: No',
      icon: 'smart_toy',
    },
    {
      user: 'podman',
      uidGid: '1001:1001',
      rolesGroups: 'podman, systemd-journal',
      authMechanism: 'Rootless SubUID 100000-165535',
      lingerSubUid: 'Linger: Enabled',
      icon: 'deployed_code',
    },
    {
      user: 'cockpit-wsinstance',
      uidGid: '977:977',
      rolesGroups: 'cockpit-ws, ssl-cert',
      authMechanism: 'Unix Socket Web PKI',
      lingerSubUid: 'PAM GSSAPI',
      icon: 'terminal',
    },
  ];

  return (
    <section className="bg-[#171a2d] rounded p-3 flex flex-col border border-[#26293c]">
      <div className="flex items-center justify-between pb-1.5 bg-[#0a0d1f]/40 px-2 py-1 rounded border border-[#26293c]/40">
        <div className="flex items-center gap-2">
          <span className="material-symbols-outlined text-[#d9b9ff] text-[18px]">
            manage_accounts
          </span>
          <span className="font-sans text-sm text-[#dfe1fa] font-semibold">
            Service Users &amp; System Identities
          </span>
        </div>
        <button
          type="button"
          onClick={onAddIdentity}
          className="flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#d9b9ff] px-2.5 py-1 rounded font-mono text-xs border border-[#434750]/40 transition-colors cursor-pointer"
        >
          <span className="material-symbols-outlined text-[13px]">person_add</span>
          <span>+ Add Service Identity</span>
        </button>
      </div>

      <div className="overflow-x-auto mt-2 font-mono text-xs">
        <table className="w-full text-left">
          <thead className="bg-[#0a0d1f] text-[#8d909b] text-[10px]">
            <tr className="h-6">
              <th className="px-2">User / Identity</th>
              <th className="px-2">UID:GID</th>
              <th className="px-2">Role / Groups</th>
              <th className="px-2">Authentication Mechanism</th>
              <th className="px-2">Linger / SubUID</th>
              <th className="px-2 text-right">Actions</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[#26293c]/50">
            {identities.map(id => (
              <tr key={id.user} className="h-8 hover:bg-[#26293c]/40 transition-colors">
                <td className="px-2 text-[#dfe1fa] font-bold flex items-center gap-1.5">
                  <span
                    className={`material-symbols-outlined text-[15px] ${
                      id.user === 'deployer'
                        ? 'text-[#b0c9ff]'
                        : id.user === 'restic-svc'
                        ? 'text-[#8cd6cb]'
                        : id.user === 'podman'
                        ? 'text-[#d9b9ff]'
                        : 'text-[#b0c9ff]'
                    }`}
                  >
                    {id.icon}
                  </span>
                  <span>{id.user}</span>
                </td>
                <td className="px-2 text-[#8d909b]">{id.uidGid}</td>
                <td className="px-2 text-[#d9b9ff]">{id.rolesGroups}</td>
                <td
                  className={`px-2 ${
                    id.user === 'deployer' ? 'text-[#8cd6cb]' : 'text-[#8d909b]'
                  }`}
                >
                  {id.authMechanism}
                </td>
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
                  {id.user === 'deployer' ? (
                    <>
                      <button
                        type="button"
                        onClick={() => onAudit?.(id.user)}
                        className="px-1.5 py-0.5 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] rounded text-[10px] cursor-pointer"
                      >
                        Keys
                      </button>
                      <button
                        type="button"
                        className="px-1.5 py-0.5 bg-[#26293c] hover:bg-[#35384c] text-[#8d909b] rounded text-[10px] cursor-pointer"
                      >
                        Lock
                      </button>
                    </>
                  ) : id.user === 'restic-svc' ? (
                    <button
                      type="button"
                      onClick={() => onAudit?.(id.user)}
                      className="px-1.5 py-0.5 bg-[#26293c] hover:bg-[#35384c] text-[#8cd6cb] rounded text-[10px] cursor-pointer"
                    >
                      Audit
                    </button>
                  ) : id.user === 'podman' ? (
                    <button
                      type="button"
                      onClick={() => onAudit?.(id.user)}
                      className="px-1.5 py-0.5 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] rounded text-[10px] cursor-pointer"
                    >
                      Config
                    </button>
                  ) : (
                    <button
                      type="button"
                      onClick={() => onAudit?.(id.user)}
                      className="px-1.5 py-0.5 bg-[#26293c] hover:bg-[#35384c] text-[#8d909b] rounded text-[10px] cursor-pointer"
                    >
                      Session
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
};
