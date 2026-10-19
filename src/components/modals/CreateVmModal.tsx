import React, { useState, useEffect } from 'react';
import type { NodeItem, VMItem } from '../../types/index.ts';

interface CreateVmModalProps {
  node?: NodeItem;
  nodes?: NodeItem[];
  isOpen: boolean;
  isMinimized?: boolean;
  onMinimize?: () => void;
  onClose: () => void;
  onCreated?: (newVm: VMItem) => void;
}

export const CreateVmModal: React.FC<CreateVmModalProps> = ({
  node,
  nodes = [],
  isOpen,
  isMinimized = false,
  onMinimize,
  onClose,
  onCreated,
}) => {
  const [targetNodeId, setTargetNodeId] = useState<string>(node?.id || '');
  const [name, setName] = useState('vm-ubuntu-svc-01');
  const [osTemplate, setOsTemplate] = useState('Ubuntu 24.04');
  const [vcpus, setVcpus] = useState(2);
  const [ramGiB, setRamGiB] = useState(4);
  const [diskGb, setDiskGb] = useState(20);
  const [bridge, setBridge] = useState('virbr0');
  const [autostart, setAutostart] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (node?.id) {
      setTargetNodeId(node.id);
    } else if (nodes.length > 0 && !targetNodeId) {
      setTargetNodeId(nodes[0].id);
    }
  }, [node?.id, nodes]);

  useEffect(() => {
    if (isOpen) {
      setError(null);
    }
  }, [isOpen]);

  if (!isOpen) return null;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) {
      setError('VM name is required');
      return;
    }
    const resolvedNodeId = targetNodeId || node?.id || nodes[0]?.id;
    if (!resolvedNodeId) {
      setError('A target host node must be selected');
      return;
    }

    setLoading(true);
    setError(null);

    try {
      const resp = await fetch(`/api/nodes/${encodeURIComponent(resolvedNodeId)}/vms/create`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: name.trim(),
          osTemplate,
          vcpus: Number(vcpus),
          ramGiB: Number(ramGiB),
          diskGb: Number(diskGb),
          bridge,
          autostart,
        }),
      });

      const data = await resp.json();
      if (!resp.ok || !data.success) {
        throw new Error(data.error || 'Failed to create virtual machine');
      }

      if (data.vm && onCreated) {
        onCreated(data.vm);
      }
      onClose();
    } catch (err: any) {
      setError(err?.message || 'Error occurred while creating virtual machine');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div
      style={{ display: isMinimized ? 'none' : 'flex' }}
      className="fixed inset-0 z-50 items-center justify-center bg-black/70 backdrop-blur-xs p-4"
    >
      <div className="w-full max-w-lg bg-[#0a0d1f] rounded-lg shadow-2xl border border-[#313447] overflow-hidden">
        {/* Header with Title and Control Buttons */}
        <div className="h-9 bg-[#171a2d] px-3 flex items-center justify-between border-b border-[#26293c]">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[#b0c9ff] text-[18px]">
              desktop_windows
            </span>
            <span className="font-mono text-xs text-[#dfe1fa] font-bold">
              Create Virtual Machine (KVM/QEMU)
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

        {/* Modal Body Form */}
        <form onSubmit={handleSubmit} className="p-4 space-y-3 font-mono text-xs">
          {/* Target Host */}
          <div>
            <label className="block text-[#8d909b] mb-1">TARGET HOST HYPERVISOR</label>
            <select
              value={targetNodeId}
              onChange={e => setTargetNodeId(e.target.value)}
              className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
            >
              {nodes.map(n => (
                <option key={n.id} value={n.id}>
                  {n.name} ({n.tailscaleIp || n.ip})
                </option>
              ))}
            </select>
          </div>

          {/* VM Name & OS Template */}
          <div className="grid grid-cols-2 gap-2">
            <div>
              <label className="block text-[#8d909b] mb-1">VM NAME / IDENTIFIER</label>
              <input
                type="text"
                required
                value={name}
                onChange={e => setName(e.target.value)}
                placeholder="vm-ubuntu-svc-01"
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
              />
            </div>
            <div>
              <label className="block text-[#8d909b] mb-1">OS / TEMPLATE</label>
              <select
                value={osTemplate}
                onChange={e => setOsTemplate(e.target.value)}
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
              >
                <option value="Ubuntu 24.04">Ubuntu 24.04 LTS (Noble Numbat)</option>
                <option value="Debian 12">Debian 12 (Bookworm)</option>
                <option value="AlmaLinux 9">AlmaLinux 9 / RHEL Enterprise</option>
                <option value="Alpine 3.20">Alpine Linux 3.20 (Minimal)</option>
                <option value="Windows Server">Windows Server 2022 (UEFI/VirtIO)</option>
              </select>
            </div>
          </div>

          {/* vCPUs & RAM */}
          <div className="grid grid-cols-2 gap-2">
            <div>
              <label className="block text-[#8d909b] mb-1">vCPUs ALLOCATION (1-32)</label>
              <div className="flex items-center gap-1">
                <input
                  type="number"
                  min={1}
                  max={32}
                  value={vcpus}
                  onChange={e => setVcpus(Math.max(1, Math.min(32, Number(e.target.value))))}
                  className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2 py-1.5 text-[#dfe1fa] outline-none"
                />
                <span className="text-[#8d909b] shrink-0">Cores</span>
              </div>
            </div>
            <div>
              <label className="block text-[#8d909b] mb-1">MEMORY ALLOCATION (GiB)</label>
              <div className="flex items-center gap-1">
                <input
                  type="number"
                  min={1}
                  max={128}
                  value={ramGiB}
                  onChange={e => setRamGiB(Math.max(1, Math.min(128, Number(e.target.value))))}
                  className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2 py-1.5 text-[#dfe1fa] outline-none"
                />
                <span className="text-[#8d909b] shrink-0">GiB</span>
              </div>
            </div>
          </div>

          {/* Disk Size & Network Bridge */}
          <div className="grid grid-cols-2 gap-2">
            <div>
              <label className="block text-[#8d909b] mb-1">DISK STORAGE (GB)</label>
              <div className="flex items-center gap-1">
                <input
                  type="number"
                  min={5}
                  max={1000}
                  value={diskGb}
                  onChange={e => setDiskGb(Math.max(5, Number(e.target.value)))}
                  className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2 py-1.5 text-[#dfe1fa] outline-none"
                />
                <span className="text-[#8d909b] shrink-0">GB</span>
              </div>
            </div>
            <div>
              <label className="block text-[#8d909b] mb-1">NETWORK BRIDGE</label>
              <select
                value={bridge}
                onChange={e => setBridge(e.target.value)}
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
              >
                <option value="virbr0">virbr0 (NAT Default)</option>
                <option value="br0">br0 (Bridged Physical LAN)</option>
                <option value="tailscale0">tailscale0 (Tailnet Interface)</option>
              </select>
            </div>
          </div>

          {/* Autostart Checkbox */}
          <div className="pt-1">
            <label className="flex items-center gap-2 cursor-pointer text-[#dfe1fa]">
              <input
                type="checkbox"
                checked={autostart}
                onChange={e => setAutostart(e.target.checked)}
                className="rounded border-[#26293c] bg-[#1b1e31] text-[#b0c9ff] focus:ring-0"
              />
              <span>Autostart guest VM when host powers on</span>
            </label>
          </div>

          {/* Error Warning */}
          {error && (
            <div className="p-2 rounded bg-[#2d161a] border border-[#ffb4ab]/30 text-[#ffb4ab] text-[11px] flex items-center gap-2">
              <span className="material-symbols-outlined text-[16px] shrink-0">warning</span>
              <span>{error}</span>
            </div>
          )}

          {/* Action Buttons */}
          <div className="pt-2 flex items-center justify-end gap-2 border-t border-[#26293c]">
            <button
              type="button"
              onClick={onClose}
              disabled={loading}
              className="px-3 py-1.5 bg-[#1b1e31] hover:bg-[#26293c] text-[#c3c6d2] rounded cursor-pointer disabled:opacity-50"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={loading || !name.trim()}
              className="px-3 py-1.5 bg-[#b0c9ff] hover:bg-[#d8e2ff] text-[#002e68] font-bold rounded cursor-pointer flex items-center gap-1 shadow-sm disabled:opacity-50"
            >
              <span className="material-symbols-outlined text-[15px]">
                {loading ? 'sync' : 'add'}
              </span>
              <span>{loading ? 'Creating VM...' : 'Create Virtual Machine'}</span>
            </button>
          </div>
        </form>
      </div>
    </div>
  );
};
