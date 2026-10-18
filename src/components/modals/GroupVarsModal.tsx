import React, { useState } from 'react';

interface GroupVarsModalProps {
  isOpen: boolean;
  onClose: () => void;
}

export const GroupVarsModal: React.FC<GroupVarsModalProps> = ({
  isOpen,
  onClose,
}) => {
  const [selectedFile, setSelectedFile] = useState('group_vars/all.yml');
  const [content, setContent] = useState(`---
# Ansible Global Group Vars (Homelab Cluster)
datacenter_name: homelab-dc1
domain_suffix: homelab.internal
tailscale_authkey: "!vault | $ANSIBLE_VAULT;1.1;AES256..."
restic_repository: "s3://restic-homelab-vault"
restic_retention_days: 7
restic_retention_weeks: 4
restic_retention_months: 12

podman_storage_driver: zfs
podman_rootless_users:
  - deployer
  - podman

security:
  nftables_enforce: true
  sshd_strict_mfa: true
  ssh_allowed_groups:
    - wheel
    - sudo
`);

  if (!isOpen) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-xs p-4">
      <div className="w-full max-w-2xl bg-[#0a0d1f] rounded-lg shadow-2xl border border-[#313447] flex flex-col h-[520px] overflow-hidden">
        <div className="h-9 bg-[#171a2d] px-3 flex items-center justify-between border-b border-[#26293c]">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[#d9b9ff] text-[18px]">lock_reset</span>
            <span className="font-mono text-xs text-[#dfe1fa] font-bold">
              Ansible Group Vars &amp; Vault Editor
            </span>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="text-[#8d909b] hover:text-[#dfe1fa] p-1 cursor-pointer"
          >
            <span className="material-symbols-outlined text-[16px]">close</span>
          </button>
        </div>

        {/* Tab files */}
        <div className="bg-[#1b1e31] px-3 py-1 flex items-center gap-2 border-b border-[#26293c] font-mono text-xs">
          {['group_vars/all.yml', 'group_vars/cold-war.yml', 'group_vars/noodle.yml'].map(f => (
            <button
              key={f}
              type="button"
              onClick={() => setSelectedFile(f)}
              className={`px-2 py-0.5 rounded cursor-pointer ${
                selectedFile === f
                  ? 'bg-[#26293c] text-[#b0c9ff] font-bold border border-[#b0c9ff]/40'
                  : 'text-[#8d909b] hover:text-[#dfe1fa]'
              }`}
            >
              {f}
            </button>
          ))}
          <span className="ml-auto text-[10px] text-[#8cd6cb] font-mono">Vault Cipher: AES256</span>
        </div>

        <div className="flex-1 p-3 bg-[#0a0d1f] font-mono text-xs">
          <textarea
            value={content}
            onChange={e => setContent(e.target.value)}
            className="w-full h-full bg-[#171a2d] p-3 text-[#dfe1fa] rounded border border-[#26293c] focus:border-[#b0c9ff] outline-none font-mono resize-none"
            spellCheck={false}
          />
        </div>

        <div className="h-9 bg-[#171a2d] px-3 flex items-center justify-between font-mono text-xs border-t border-[#26293c]">
          <span className="text-[#8d909b]">Encrypted with Ansible Vault master passphrase</span>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={onClose}
              className="px-2.5 py-1 bg-[#1b1e31] text-[#c3c6d2] rounded hover:bg-[#26293c] cursor-pointer"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={onClose}
              className="px-3 py-1 bg-[#b0c9ff] text-[#002e68] font-bold rounded hover:bg-[#d8e2ff] cursor-pointer"
            >
              Encrypt &amp; Save
            </button>
          </div>
        </div>
      </div>
    </div>
  );
};
