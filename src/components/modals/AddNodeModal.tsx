import React, { useState } from 'react';
import type { NodeItem, NodeType } from '../../types/index.ts';

interface AddNodeModalProps {
  isOpen: boolean;
  onClose: () => void;
  onAddNode: (node: Partial<NodeItem>) => void;
}

export const AddNodeModal: React.FC<AddNodeModalProps> = ({
  isOpen,
  onClose,
  onAddNode,
}) => {
  const [name, setName] = useState('');
  const [group, setGroup] = useState('cold-war');
  const [nodeType, setNodeType] = useState<NodeType>('linux');
  const [ip, setIp] = useState('');
  const [port, setPort] = useState(22);
  const [tailscaleIp, setTailscaleIp] = useState('');
  const [tailscaleHost, setTailscaleHost] = useState('');
  const [osDescription, setOsDescription] = useState('Debian GNU/Linux 12');
  const [authMethod, setAuthMethod] = useState('ssh-ed25519');
  const [vcpus, setVcpus] = useState(8);
  const [ramGiB, setRamGiB] = useState(16);

  if (!isOpen) return null;

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) return;

    onAddNode({
      name,
      shortName: name.split(' ')[0],
      group,
      datacenter: 'homelab-dc1',
      nodeType,
      ip: ip || '192.168.1.180',
      port,
      tailscaleIp: tailscaleIp || '100.97.159.180',
      tailscaleHost: tailscaleHost || `${name.toLowerCase().replace(/[^a-z0-9]/g, '-')}.ts.net`,
      osDescription,
      status: 'online',
      uptime: '1m',
      hasGlances: nodeType === 'linux',
      hasCockpit: nodeType === 'linux',
      hasPodman: nodeType === 'linux',
      hasRestic: true,
      specs: {
        cpu: 'AMD EPYC / Intel Core vCPU',
        vcpus,
        ramGiB,
        motherboard: 'Virtualization / Server Chassis',
        bios: 'UEFI 2.8',
        ipmi: 'DHCP',
        ecc: 'Active',
        secureBoot: 'Enabled',
        iommu: 'Enabled',
      },
    });

    onClose();
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-xs p-4">
      <div className="w-full max-w-lg bg-[#0a0d1f] rounded-lg shadow-2xl border border-[#313447] overflow-hidden">
        <div className="h-9 bg-[#171a2d] px-3 flex items-center justify-between border-b border-[#26293c]">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[#8cd6cb] text-[18px]">add_circle</span>
            <span className="font-mono text-xs text-[#dfe1fa] font-bold">
              Provision / Add Node to Datacenter
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

        <form onSubmit={handleSubmit} className="p-4 space-y-3 font-mono text-xs">
          {/* Node Type Selector */}
          <div>
            <label className="block text-[#8d909b] mb-1">NODE ARCHITECTURE &amp; TARGET</label>
            <div className="grid grid-cols-3 gap-2">
              <button
                type="button"
                onClick={() => {
                  setNodeType('linux');
                  setOsDescription('Debian GNU/Linux 12 (Cockpit / Podman)');
                }}
                className={`p-2 rounded text-left border cursor-pointer transition-colors ${
                  nodeType === 'linux'
                    ? 'bg-[#26293c] border-[#b0c9ff] text-[#b0c9ff]'
                    : 'bg-[#1b1e31] border-[#26293c] text-[#c3c6d2]'
                }`}
              >
                <div className="font-bold flex items-center gap-1">
                  <span className="material-symbols-outlined text-[14px]">dns</span>
                  Linux
                </div>
                <div className="text-[10px] text-[#8d909b]">Cockpit &amp; Podman</div>
              </button>

              <button
                type="button"
                onClick={() => {
                  setNodeType('windows');
                  setOsDescription('Windows Server 2022 (OpenSSH / WinRM)');
                }}
                className={`p-2 rounded text-left border cursor-pointer transition-colors ${
                  nodeType === 'windows'
                    ? 'bg-[#26293c] border-[#b0c9ff] text-[#b0c9ff]'
                    : 'bg-[#1b1e31] border-[#26293c] text-[#c3c6d2]'
                }`}
              >
                <div className="font-bold flex items-center gap-1">
                  <span className="material-symbols-outlined text-[14px]">desktop_windows</span>
                  Windows
                </div>
                <div className="text-[10px] text-[#8d909b]">OpenSSH &amp; WinRM</div>
              </button>

              <button
                type="button"
                onClick={() => {
                  setNodeType('router_switch');
                  setOsDescription('VyOS / Cisco IOS / RouterOS');
                }}
                className={`p-2 rounded text-left border cursor-pointer transition-colors ${
                  nodeType === 'router_switch'
                    ? 'bg-[#26293c] border-[#b0c9ff] text-[#b0c9ff]'
                    : 'bg-[#1b1e31] border-[#26293c] text-[#c3c6d2]'
                }`}
              >
                <div className="font-bold flex items-center gap-1">
                  <span className="material-symbols-outlined text-[14px]">router</span>
                  Switch / Router
                </div>
                <div className="text-[10px] text-[#8d909b]">SSH CLI Console</div>
              </button>
            </div>
          </div>

          {/* Node Name & Group */}
          <div className="grid grid-cols-2 gap-2">
            <div>
              <label className="block text-[#8d909b] mb-1">NODE NAME / IDENTIFIER</label>
              <input
                type="text"
                required
                value={name}
                onChange={e => setName(e.target.value)}
                placeholder="e.g. 155 (docker-prod)"
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
              />
            </div>
            <div>
              <label className="block text-[#8d909b] mb-1">ANSIBLE INVENTORY GROUP</label>
              <select
                value={group}
                onChange={e => setGroup(e.target.value)}
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
              >
                <option value="cold-war">cold-war (Datacenter Nodes)</option>
                <option value="noodle">noodle (Secondary Cluster)</option>
                <option value="edge">edge (Remote Gateways)</option>
              </select>
            </div>
          </div>

          {/* IP and Tailscale */}
          <div className="grid grid-cols-2 gap-2">
            <div>
              <label className="block text-[#8d909b] mb-1">LAN IP ADDRESS &amp; PORT</label>
              <div className="flex gap-1">
                <input
                  type="text"
                  value={ip}
                  onChange={e => setIp(e.target.value)}
                  placeholder="192.168.1.180"
                  className="flex-1 bg-[#1b1e31] border border-[#26293c] rounded px-2 py-1.5 text-[#dfe1fa] outline-none"
                />
                <input
                  type="number"
                  value={port}
                  onChange={e => setPort(Number(e.target.value))}
                  className="w-16 bg-[#1b1e31] border border-[#26293c] rounded px-2 py-1.5 text-[#dfe1fa] outline-none"
                />
              </div>
            </div>
            <div>
              <label className="block text-[#8d909b] mb-1">TAILSCALE MESH IP</label>
              <input
                type="text"
                value={tailscaleIp}
                onChange={e => setTailscaleIp(e.target.value)}
                placeholder="100.97.159.180"
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] outline-none"
              />
            </div>
          </div>

          {/* Authentication & Specs */}
          <div className="grid grid-cols-2 gap-2">
            <div>
              <label className="block text-[#8d909b] mb-1">AUTHENTICATION MECHANISM</label>
              <select
                value={authMethod}
                onChange={e => setAuthMethod(e.target.value)}
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2 py-1.5 text-[#dfe1fa] outline-none"
              >
                <option value="ssh-ed25519">ssh-ed25519 (Ansible Vault)</option>
                <option value="pam-password">PAM Password (GSSAPI)</option>
                <option value="tailscale-ssh">Tailscale SSH Keyless</option>
              </select>
            </div>
            <div>
              <label className="block text-[#8d909b] mb-1">HARDWARE ALLOCATION</label>
              <div className="flex gap-1 items-center">
                <input
                  type="number"
                  value={vcpus}
                  onChange={e => setVcpus(Number(e.target.value))}
                  className="w-16 bg-[#1b1e31] border border-[#26293c] rounded px-2 py-1.5 text-[#dfe1fa]"
                />
                <span className="text-[#8d909b]">vCPUs</span>
                <input
                  type="number"
                  value={ramGiB}
                  onChange={e => setRamGiB(Number(e.target.value))}
                  className="w-16 bg-[#1b1e31] border border-[#26293c] rounded px-2 py-1.5 text-[#dfe1fa] ml-1"
                />
                <span className="text-[#8d909b]">GiB</span>
              </div>
            </div>
          </div>

          {/* Action Buttons */}
          <div className="pt-2 flex items-center justify-end gap-2 border-t border-[#26293c]">
            <button
              type="button"
              onClick={onClose}
              className="px-3 py-1.5 bg-[#1b1e31] hover:bg-[#26293c] text-[#c3c6d2] rounded cursor-pointer"
            >
              Cancel
            </button>
            <button
              type="submit"
              className="px-3 py-1.5 bg-[#b0c9ff] hover:bg-[#d8e2ff] text-[#002e68] font-bold rounded cursor-pointer flex items-center gap-1 shadow-sm"
            >
              <span className="material-symbols-outlined text-[15px]">add</span>
              Provision &amp; Connect
            </button>
          </div>
        </form>
      </div>
    </div>
  );
};
