import React, { useState, useEffect } from 'react';

interface GroupVarsModalProps {
  isOpen: boolean;
  onClose: () => void;
}

const DEFAULT_GROUP_VARS: Record<string, string> = {
  'group_vars/all.yml': `---
# Ansible Global Group Vars (leo-banjo.ts.net Tailnet)
datacenter_name: leo-banjo.ts.net
tailnet_domain: leo-banjo.ts.net
default_ssh_user: core
ssh_agent_socket: /run/user/1000/ssh-agent.socket
restic_repository: "s3://restic-homelab-vault/leo-banjo"
restic_retention_days: 7
restic_retention_weeks: 4
restic_retention_months: 12

podman_rootless_default: true
security:
  selinux_mode: enforcing
  tailscale_ssh_enabled: true
`,
  'group_vars/tag-host.yml': `---
# Ansible Group Vars for tag:host nodes
ansible_user: core
ansible_port: 22
host_members:
  - likes-it-cold.leo-banjo.ts.net
  - ping.leo-banjo.ts.net
  - fred.leo-banjo.ts.net
  - the-freezer.leo-banjo.ts.net
  - chuck.leo-banjo.ts.net
  - larry.leo-banjo.ts.net
  - orangepizero3.leo-banjo.ts.net
  - dev-pc.leo-banjo.ts.net
`,
  'group_vars/likes-it-cold.yml': `---
# Host Vars: likes-it-cold.leo-banjo.ts.net
ansible_host: likes-it-cold.leo-banjo.ts.net
ansible_user: core
os_distribution: "AlmaLinux 10.2 (Lavender Lion)"
`,
  'group_vars/ping.yml': `---
# Host Vars: ping.leo-banjo.ts.net
ansible_host: ping.leo-banjo.ts.net
ansible_user: core
os_distribution: "AlmaLinux 10.2 (Lavender Lion)"
`,
};

export const GroupVarsModal: React.FC<GroupVarsModalProps> = ({
  isOpen,
  onClose,
}) => {
  const [filesMap, setFilesMap] = useState<Record<string, string>>(DEFAULT_GROUP_VARS);
  const [selectedFile, setSelectedFile] = useState('group_vars/all.yml');
  const [saveMessage, setSaveMessage] = useState<string | null>(null);

  useEffect(() => {
    if (isOpen) {
      fetch('/api/group-vars')
        .then(res => res.json())
        .then(data => {
          if (data && typeof data === 'object') {
            setFilesMap(prev => ({ ...prev, ...data }));
          }
        })
        .catch(() => {});
    }
  }, [isOpen]);

  if (!isOpen) return null;

  const currentContent = filesMap[selectedFile] ?? '';

  const handleContentChange = (val: string) => {
    setFilesMap(prev => ({ ...prev, [selectedFile]: val }));
    setSaveMessage(null);
  };

  const handleSave = async () => {
    try {
      await fetch('/api/group-vars', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ file: selectedFile, content: currentContent }),
      });
      setSaveMessage(`Saved ${selectedFile}`);
      setTimeout(() => {
        setSaveMessage(null);
        onClose();
      }, 600);
    } catch {
      onClose();
    }
  };

  const fileKeys = Object.keys(filesMap);

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
        <div className="bg-[#1b1e31] px-3 py-1 flex items-center gap-1.5 border-b border-[#26293c] font-mono text-xs overflow-x-auto">
          {fileKeys.map(f => (
            <button
              key={f}
              type="button"
              onClick={() => {
                setSelectedFile(f);
                setSaveMessage(null);
              }}
              className={`px-2 py-0.5 rounded cursor-pointer whitespace-nowrap ${
                selectedFile === f
                  ? 'bg-[#26293c] text-[#b0c9ff] font-bold border border-[#b0c9ff]/40'
                  : 'text-[#8d909b] hover:text-[#dfe1fa]'
              }`}
            >
              {f}
            </button>
          ))}
          <span className="ml-auto text-[10px] text-[#8cd6cb] font-mono shrink-0">
            Vault Cipher: AES256
          </span>
        </div>

        <div className="flex-1 p-3 bg-[#0a0d1f] font-mono text-xs">
          <textarea
            value={currentContent}
            onChange={e => handleContentChange(e.target.value)}
            className="w-full h-full bg-[#171a2d] p-3 text-[#dfe1fa] rounded border border-[#26293c] focus:border-[#b0c9ff] outline-none font-mono resize-none"
            spellCheck={false}
          />
        </div>

        <div className="h-9 bg-[#171a2d] px-3 flex items-center justify-between font-mono text-xs border-t border-[#26293c]">
          <span className="text-[#8cd6cb]">
            {saveMessage || 'Encrypted with Ansible Vault master passphrase'}
          </span>
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
              onClick={handleSave}
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
