import React, { useState, useEffect } from 'react';
import type { NodeItem, NodeType } from '../../types/index.ts';

interface EditNodeModalProps {
  node: NodeItem | null;
  isOpen: boolean;
  isMinimized?: boolean;
  onMinimize?: () => void;
  onClose: () => void;
  onSaveNode: (nodeId: string, updates: Partial<NodeItem> & { testSsh?: boolean }) => Promise<void> | void;
  onDeleteNode?: (nodeId: string) => void;
}

export const EditNodeModal: React.FC<EditNodeModalProps> = ({
  node,
  isOpen,
  isMinimized = false,
  onMinimize,
  onClose,
  onSaveNode,
  onDeleteNode,
}) => {
  const [name, setName] = useState('');
  const [group, setGroup] = useState('tag:host');
  const [nodeType, setNodeType] = useState<NodeType>('linux');
  const [ip, setIp] = useState('');
  const [port, setPort] = useState(22);
  const [sshUser, setSshUser] = useState('core');
  const [authMethod, setAuthMethod] = useState('ssh-ed25519 (ssh-agent)');
  const [tailscaleIp, setTailscaleIp] = useState('');
  const [tailscaleHost, setTailscaleHost] = useState('');
  const [osDescription, setOsDescription] = useState('');
  const [cpu, setCpu] = useState('');
  const [vcpus, setVcpus] = useState(4);
  const [ramGiB, setRamGiB] = useState(8);
  const [motherboard, setMotherboard] = useState('');
  const [bios, setBios] = useState('');
  const [ipmi, setIpmi] = useState('');
  const [ecc, setEcc] = useState('');
  const [secureBoot, setSecureBoot] = useState('');
  const [iommu, setIommu] = useState('');
  const [testSshOnSave, setTestSshOnSave] = useState(true);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (node) {
      setName(node.name);
      setGroup(node.group || 'tag:host');
      setNodeType(node.nodeType || 'linux');
      setIp(node.ip || '');
      setPort(node.port || 22);
      setSshUser(node.sshUser || 'core');
      setAuthMethod(node.authMethod || 'ssh-ed25519 (ssh-agent)');
      setTailscaleIp(node.tailscaleIp || '');
      setTailscaleHost(node.tailscaleHost || '');
      setOsDescription(node.osDescription || '');
      setCpu(node.specs?.cpu || '');
      setVcpus(node.specs?.vcpus || 4);
      setRamGiB(node.specs?.ramGiB || 8);
      setMotherboard(node.specs?.motherboard || '');
      setBios(node.specs?.bios || '');
      setIpmi(node.specs?.ipmi || '');
      setEcc(node.specs?.ecc || '');
      setSecureBoot(node.specs?.secureBoot || '');
      setIommu(node.specs?.iommu || '');
    }
  }, [node, isOpen]);

  if (!isOpen || !node) return null;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    try {
      await onSaveNode(node.id, {
        name,
        shortName: name.split(' ')[0],
        group,
        nodeType,
        ip,
        port,
        sshUser,
        authMethod,
        tailscaleIp,
        tailscaleHost,
        osDescription,
        specs: {
          cpu,
          vcpus,
          ramGiB,
          motherboard,
          bios,
          ipmi,
          ecc,
          secureBoot,
          iommu,
        },
        testSsh: testSshOnSave,
      });
      onClose();
    } finally {
      setSaving(false);
    }
  };

  return (
    <div
      style={{ display: isMinimized ? 'none' : 'flex' }}
      className="fixed inset-0 z-50 items-center justify-center bg-black/70 backdrop-blur-xs p-4"
    >
      <div className="w-full max-w-2xl bg-[#0a0d1f] rounded-lg shadow-2xl border border-[#313447] overflow-hidden max-h-[90vh] flex flex-col">
        <div className="h-9 bg-[#171a2d] px-3 flex items-center justify-between border-b border-[#26293c]">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[#b0c9ff] text-[18px]">tune</span>
            <span className="font-mono text-xs text-[#dfe1fa] font-bold">
              Edit Node &amp; SSH Settings: {node.name}
            </span>
          </div>
          <div className="flex items-center gap-1">
            {onMinimize && (
              <button
                type="button"
                onClick={onMinimize}
                className="text-[#8d909b] hover:text-[#dfe1fa] p-1 cursor-pointer transition-colors"
                title="Minimize to bottom panel"
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

        <form onSubmit={handleSubmit} className="p-4 space-y-3 font-mono text-xs overflow-y-auto">
          <div className="grid grid-cols-3 gap-2">
            <div>
              <label className="block text-[#8d909b] mb-1">NODE NAME</label>
              <input
                type="text"
                required
                value={name}
                onChange={e => setName(e.target.value)}
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
              />
            </div>
            <div>
              <label className="block text-[#8d909b] mb-1">GROUP / TAILSCALE TAG</label>
              <input
                type="text"
                value={group}
                onChange={e => setGroup(e.target.value)}
                placeholder="tag:host"
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
              />
            </div>
            <div>
              <label className="block text-[#8d909b] mb-1">NODE TYPE</label>
              <select
                value={nodeType}
                onChange={e => setNodeType(e.target.value as NodeType)}
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2 py-1.5 text-[#dfe1fa] outline-none"
              >
                <option value="linux">linux (SSH / Podman)</option>
                <option value="k3s">k3s (Kubernetes)</option>
                <option value="lxc">lxc (Container)</option>
                <option value="pbs">pbs (Backup Server)</option>
                <option value="router_switch">router_switch</option>
                <option value="windows">windows</option>
              </select>
            </div>
          </div>

          <div className="grid grid-cols-3 gap-2">
            <div>
              <label className="block text-[#8d909b] mb-1">SSH USER</label>
              <input
                type="text"
                value={sshUser}
                onChange={e => setSshUser(e.target.value)}
                placeholder="core"
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#8cd6cb] font-bold focus:border-[#b0c9ff] outline-none"
              />
            </div>
            <div>
              <label className="block text-[#8d909b] mb-1">SSH PORT</label>
              <input
                type="number"
                value={port}
                onChange={e => setPort(Number(e.target.value))}
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
              />
            </div>
            <div>
              <label className="block text-[#8d909b] mb-1">AUTH METHOD</label>
              <select
                value={authMethod}
                onChange={e => setAuthMethod(e.target.value)}
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2 py-1.5 text-[#dfe1fa] outline-none"
              >
                <option value="ssh-ed25519 (ssh-agent)">ssh-ed25519 (ssh-agent)</option>
                <option value="tailscale-ssh">Tailscale SSH</option>
                <option value="pam-password">PAM Password</option>
              </select>
            </div>
          </div>

          <div className="grid grid-cols-3 gap-2">
            <div>
              <label className="block text-[#8d909b] mb-1">TAILSCALE DNS HOSTNAME</label>
              <input
                type="text"
                value={tailscaleHost}
                onChange={e => setTailscaleHost(e.target.value)}
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
              />
            </div>
            <div>
              <label className="block text-[#8d909b] mb-1">TAILSCALE IP</label>
              <input
                type="text"
                value={tailscaleIp}
                onChange={e => setTailscaleIp(e.target.value)}
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
              />
            </div>
            <div>
              <label className="block text-[#8d909b] mb-1">LAN IP</label>
              <input
                type="text"
                value={ip}
                onChange={e => setIp(e.target.value)}
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
              />
            </div>
          </div>

          <div className="grid grid-cols-2 gap-2">
            <div>
              <label className="block text-[#8d909b] mb-1">OS DESCRIPTION</label>
              <input
                type="text"
                value={osDescription}
                onChange={e => setOsDescription(e.target.value)}
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
              />
            </div>
            <div>
              <label className="block text-[#8d909b] mb-1">CPU MODEL</label>
              <input
                type="text"
                value={cpu}
                onChange={e => setCpu(e.target.value)}
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
              />
            </div>
          </div>

          <div className="grid grid-cols-4 gap-2">
            <div>
              <label className="block text-[#8d909b] mb-1">vCPUs</label>
              <input
                type="number"
                value={vcpus}
                onChange={e => setVcpus(Number(e.target.value))}
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2 py-1.5 text-[#dfe1fa]"
              />
            </div>
            <div>
              <label className="block text-[#8d909b] mb-1">RAM (GiB)</label>
              <input
                type="number"
                value={ramGiB}
                onChange={e => setRamGiB(Number(e.target.value))}
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2 py-1.5 text-[#dfe1fa]"
              />
            </div>
            <div>
              <label className="block text-[#8d909b] mb-1">MOTHERBOARD / PLATFORM</label>
              <input
                type="text"
                value={motherboard}
                onChange={e => setMotherboard(e.target.value)}
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2 py-1.5 text-[#dfe1fa]"
              />
            </div>
            <div>
              <label className="block text-[#8d909b] mb-1">IPMI / BMC</label>
              <input
                type="text"
                value={ipmi}
                onChange={e => setIpmi(e.target.value)}
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2 py-1.5 text-[#dfe1fa]"
              />
            </div>
          </div>

          <div className="grid grid-cols-4 gap-2">
            <div>
              <label className="block text-[#8d909b] mb-1">BIOS</label>
              <input
                type="text"
                value={bios}
                onChange={e => setBios(e.target.value)}
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2 py-1.5 text-[#dfe1fa]"
              />
            </div>
            <div>
              <label className="block text-[#8d909b] mb-1">ECC MEMORY</label>
              <input
                type="text"
                value={ecc}
                onChange={e => setEcc(e.target.value)}
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2 py-1.5 text-[#dfe1fa]"
              />
            </div>
            <div>
              <label className="block text-[#8d909b] mb-1">SECURE BOOT</label>
              <input
                type="text"
                value={secureBoot}
                onChange={e => setSecureBoot(e.target.value)}
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2 py-1.5 text-[#dfe1fa]"
              />
            </div>
            <div>
              <label className="block text-[#8d909b] mb-1">IOMMU</label>
              <input
                type="text"
                value={iommu}
                onChange={e => setIommu(e.target.value)}
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2 py-1.5 text-[#dfe1fa]"
              />
            </div>
          </div>

          <label className="flex items-center gap-2 text-[#c3c6d2] cursor-pointer pt-1">
            <input
              type="checkbox"
              checked={testSshOnSave}
              onChange={e => setTestSshOnSave(e.target.checked)}
              className="rounded border-[#434750]"
            />
            <span>Verify SSH connection via local ssh-agent on save</span>
          </label>

          <div className="pt-2 flex items-center justify-between border-t border-[#26293c]">
            {onDeleteNode ? (
              <button
                type="button"
                onClick={() => {
                  onDeleteNode(node.id);
                  onClose();
                }}
                className="px-3 py-1.5 bg-[#93000a]/40 hover:bg-[#93000a]/70 text-[#ffb4ab] rounded cursor-pointer"
              >
                Remove Node
              </button>
            ) : (
              <div />
            )}

            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={onClose}
                className="px-3 py-1.5 bg-[#1b1e31] hover:bg-[#26293c] text-[#c3c6d2] rounded cursor-pointer"
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={saving}
                className="px-3 py-1.5 bg-[#b0c9ff] hover:bg-[#d8e2ff] text-[#002e68] font-bold rounded cursor-pointer flex items-center gap-1 shadow-sm"
              >
                <span className="material-symbols-outlined text-[15px]">save</span>
                <span>{saving ? 'Saving & Probing...' : 'Save Settings'}</span>
              </button>
            </div>
          </div>
        </form>
      </div>
    </div>
  );
};
