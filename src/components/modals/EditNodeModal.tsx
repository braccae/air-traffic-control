import React, { useState, useEffect } from 'react';
import type { NodeItem, NodeType } from '../../types/index.ts';
import { resolveConnectionRoute } from '../../utils/hardwareProbe.ts';

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
  // Persistent Configurable Options
  const [name, setName] = useState('');
  const [group, setGroup] = useState('tag:host');
  const [nodeType, setNodeType] = useState<NodeType>('linux');

  // Dynamic Connection Options (adapted per nodeType)
  const [sshUser, setSshUser] = useState('core');
  const [port, setPort] = useState(22);
  const [authMethod, setAuthMethod] = useState('ssh-ed25519 (ssh-agent)');

  // PVE / PBS specific
  const [apiPort, setApiPort] = useState(8006);
  const [apiUser, setApiUser] = useState('root@pam');

  // Windows specific
  const [winrmPort, setWinrmPort] = useState(5986);
  const [rdpPort, setRdpPort] = useState(3389);

  // Router / Switch specific
  const [mgmtUser, setMgmtUser] = useState('vyos');

  // VM / LXC specific
  const [hypervisorHost, setHypervisorHost] = useState('');
  const [vmId, setVmId] = useState<number | undefined>(undefined);

  // Fallback Route for Network Failover
  const [fallbackIp, setFallbackIp] = useState('');
  const [fallbackHost, setFallbackHost] = useState('');

  // Read-only Hardware Specs & Telemetry (auto-probed)
  const [specs, setSpecs] = useState<NodeItem['specs']>({
    cpu: '',
    vcpus: 1,
    ramGiB: 1,
    motherboard: '',
    bios: '',
    ipmi: '',
    ecc: '',
    secureBoot: '',
    iommu: '',
  });
  const [osDescription, setOsDescription] = useState('');

  // Probing and saving states
  const [probing, setProbing] = useState(false);
  const [probeStatus, setProbeStatus] = useState<{ success: boolean; message: string } | null>(null);
  const [testSshOnSave, setTestSshOnSave] = useState(true);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (node) {
      setName(node.name || '');
      setGroup(node.group || 'tag:host');
      const nt = (node.nodeType as NodeType) || 'linux';
      setNodeType(nt);
      setSshUser(node.sshUser || (nt === 'windows' ? 'Administrator' : (nt === 'pve' || nt === 'pbs' ? 'root' : nt === 'router_switch' ? 'vyos' : 'core')));
      setPort(node.port || 22);
      setAuthMethod(node.authMethod || 'ssh-ed25519 (ssh-agent)');
      setApiPort(node.apiPort || (nt === 'pbs' ? 8007 : 8006));
      setApiUser(node.apiUser || 'root@pam');
      setWinrmPort(node.winrmPort || 5986);
      setRdpPort(node.rdpPort || 3389);
      setMgmtUser(node.sshUser || 'vyos');
      setHypervisorHost(node.hypervisorHost || '');
      setVmId(node.vmId);
      setFallbackIp(node.fallbackIp || node.ip || '');
      setFallbackHost(node.fallbackHost || '');
      setOsDescription(node.osDescription || '');
      setSpecs(node.specs || {
        cpu: '',
        vcpus: 1,
        ramGiB: 1,
        motherboard: '',
        bios: '',
        ipmi: 'DHCP',
        ecc: 'Non-ECC',
        secureBoot: 'Disabled',
        iommu: 'Disabled',
      });
      setProbeStatus(null);
    }
  }, [node, isOpen]);

  if (!isOpen || !node) return null;

  const handleNodeTypeChange = (newType: NodeType) => {
    setNodeType(newType);
    if (newType === 'pve') {
      if (!apiPort || apiPort === 8007) setApiPort(8006);
      if (!apiUser) setApiUser('root@pam');
      if (sshUser === 'core' || !sshUser) setSshUser('root');
    } else if (newType === 'pbs') {
      if (!apiPort || apiPort === 8006) setApiPort(8007);
      if (!apiUser) setApiUser('root@pam');
      if (sshUser === 'core' || !sshUser) setSshUser('root');
    } else if (newType === 'windows') {
      if (sshUser === 'core' || !sshUser) setSshUser('Administrator');
      if (!winrmPort) setWinrmPort(5986);
      if (!rdpPort) setRdpPort(3389);
    } else if (newType === 'router_switch') {
      if (sshUser === 'core' || !sshUser) setSshUser('vyos');
      setMgmtUser(sshUser || 'vyos');
    }
  };

  const handleProbeHardware = async () => {
    if (!node) return;
    setProbing(true);
    setProbeStatus(null);
    try {
      let resp = await fetch(`/api/nodes/${encodeURIComponent(node.id)}/probe-host`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
      });

      if (!resp.ok && resp.status === 404) {
        resp = await fetch('/api/nodes/probe-host', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            host: node.tailscaleHost || node.tailscaleIp || node.ip,
            port,
            sshUser: nodeType === 'router_switch' ? mgmtUser : sshUser,
            authMethod,
          }),
        });
      }

      const data = await resp.json().catch(() => ({ success: false, error: `Invalid response (HTTP ${resp.status})` }));
      if (!resp.ok || !data.success) {
        const detail = data?.error ? `: ${data.error}` : ` (HTTP ${resp.status} ${resp.statusText || 'Error'})`;
        throw new Error(`Hardware probe failed${detail}`);
      }

      if (data.hardware) {
        setSpecs(prev => ({
          ...prev,
          cpu: data.hardware.cpu || prev.cpu,
          vcpus: data.hardware.vcpus ?? prev.vcpus,
          ramGiB: data.hardware.ramGiB ?? prev.ramGiB,
          motherboard: data.hardware.motherboard || prev.motherboard,
          bios: data.hardware.bios || prev.bios,
          secureBoot: data.hardware.secureBoot || prev.secureBoot,
          iommu: data.hardware.iommu || prev.iommu,
        }));
        if (data.hardware.osDescription) {
          setOsDescription(data.hardware.osDescription);
        }
      }
      setProbeStatus({ success: true, message: 'Hardware probed successfully from remote host' });
    } catch (err: any) {
      setProbeStatus({ success: false, message: err?.message || 'Failed to probe remote hardware' });
    } finally {
      setProbing(false);
    }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    try {
      const activeUser = nodeType === 'router_switch' ? mgmtUser : sshUser;
      await onSaveNode(node.id, {
        name,
        shortName: name.split(' ')[0],
        group,
        nodeType,
        ip: fallbackIp || node.ip,
        port,
        sshUser: activeUser,
        authMethod,
        apiPort: (nodeType === 'pve' || nodeType === 'pbs') ? apiPort : undefined,
        apiUser: (nodeType === 'pve' || nodeType === 'pbs') ? apiUser : undefined,
        winrmPort: nodeType === 'windows' ? winrmPort : undefined,
        rdpPort: nodeType === 'windows' ? rdpPort : undefined,
        hypervisorHost: (nodeType === 'vm' || nodeType === 'lxc') ? hypervisorHost : undefined,
        vmId: (nodeType === 'vm' || nodeType === 'lxc') ? vmId : undefined,
        fallbackIp,
        fallbackHost,
        osDescription,
        specs,
        testSsh: testSshOnSave,
      });
      onClose();
    } finally {
      setSaving(false);
    }
  };

  const routes = resolveConnectionRoute({
    ...node,
    fallbackHost,
    fallbackIp,
  });

  return (
    <div
      style={{ display: isMinimized ? 'none' : 'flex' }}
      className="fixed inset-0 z-50 items-center justify-center bg-black/70 backdrop-blur-xs p-4"
    >
      <div className="w-full max-w-3xl bg-[#0a0d1f] rounded-lg shadow-2xl border border-[#313447] overflow-hidden max-h-[92vh] flex flex-col">
        {/* Header */}
        <div className="h-9 bg-[#171a2d] px-3 flex items-center justify-between border-b border-[#26293c]">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[#b0c9ff] text-[18px]">tune</span>
            <span className="font-mono text-xs text-[#dfe1fa] font-bold">
              Edit Node &amp; Connection Settings: {node.name}
            </span>
            <span className="font-mono text-[10px] px-1.5 py-0.5 rounded bg-[#21263d] text-[#8cd6cb] font-semibold uppercase">
              {nodeType}
            </span>
          </div>
          <div className="flex items-center gap-1">
            {onMinimize && (
              <button
                type="button"
                onClick={onMinimize}
                className="text-[#8d909b] hover:text-[#dfe1fa] p-1 cursor-pointer transition-colors"
                title="Minimize to bottom panel"
                aria-label="Minimize"
              >
                <span className="material-symbols-outlined text-[16px]">remove</span>
              </button>
            )}
            <button
              type="button"
              onClick={onClose}
              className="text-[#8d909b] hover:text-[#dfe1fa] p-1 cursor-pointer transition-colors"
              title="Close"
              aria-label="Close"
            >
              <span className="material-symbols-outlined text-[16px]">close</span>
            </button>
          </div>
        </div>

        {/* Modal Body */}
        <form onSubmit={handleSubmit} className="p-4 space-y-4 font-mono text-xs overflow-y-auto">
          {/* Section 1: Persistent Configurable Options */}
          <div>
            <div className="text-[11px] font-bold uppercase tracking-wider text-[#b0c9ff] mb-2 flex items-center gap-1.5">
              <span className="material-symbols-outlined text-[14px]">settings</span>
              <span>Persistent Node Configuration</span>
            </div>
            <div className="grid grid-cols-1 md:grid-cols-3 gap-2.5 bg-[#131628] p-3 rounded border border-[#21253b]">
              <div>
                <label className="block text-[#8d909b] mb-1 text-[11px]">NODE NAME *</label>
                <input
                  type="text"
                  required
                  value={name}
                  onChange={e => setName(e.target.value)}
                  className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
                />
              </div>
              <div>
                <label className="block text-[#8d909b] mb-1 text-[11px]">GROUP / CLUSTER TAG</label>
                <input
                  type="text"
                  value={group}
                  onChange={e => setGroup(e.target.value)}
                  placeholder="tag:host"
                  className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
                />
              </div>
              <div>
                <label className="block text-[#8d909b] mb-1 text-[11px]">NODE TYPE</label>
                <select
                  value={nodeType}
                  onChange={e => handleNodeTypeChange(e.target.value as NodeType)}
                  className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2 py-1.5 text-[#dfe1fa] outline-none"
                >
                  <option value="linux">linux (SSH / Rootless Podman)</option>
                  <option value="pve">pve (Proxmox VE Hypervisor)</option>
                  <option value="pbs">pbs (Proxmox Backup Server)</option>
                  <option value="vm">vm (Virtual Machine / KVM)</option>
                  <option value="lxc">lxc (LXC Container / Proxmox CT)</option>
                  <option value="windows">windows (WinRM / RDP)</option>
                  <option value="router_switch">router_switch (Network Appliance)</option>
                  <option value="k3s">k3s (Kubernetes Node)</option>
                </select>
              </div>
            </div>
          </div>

          {/* Section 2: Dynamic Connection Options (per nodeType) */}
          <div>
            <div className="text-[11px] font-bold uppercase tracking-wider text-[#b0c9ff] mb-2 flex items-center gap-1.5">
              <span className="material-symbols-outlined text-[14px]">key</span>
              <span>Connection &amp; Authentication Options ({nodeType})</span>
            </div>

            <div className="bg-[#131628] p-3 rounded border border-[#21253b] space-y-3">
              {/* Linux / K3s Connection Options */}
              {(nodeType === 'linux' || nodeType === 'k3s') && (
                <div className="grid grid-cols-1 md:grid-cols-3 gap-2.5">
                  <div>
                    <label className="block text-[#8d909b] mb-1 text-[11px]">SSH USER</label>
                    <input
                      type="text"
                      value={sshUser}
                      onChange={e => setSshUser(e.target.value)}
                      placeholder="core"
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#8cd6cb] font-bold focus:border-[#b0c9ff] outline-none"
                    />
                  </div>
                  <div>
                    <label className="block text-[#8d909b] mb-1 text-[11px]">SSH PORT</label>
                    <input
                      type="number"
                      value={port}
                      onChange={e => setPort(Number(e.target.value))}
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
                    />
                  </div>
                  <div>
                    <label className="block text-[#8d909b] mb-1 text-[11px]">AUTH METHOD</label>
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
              )}

              {/* PVE / PBS Connection Options */}
              {(nodeType === 'pve' || nodeType === 'pbs') && (
                <div className="space-y-2.5">
                  <div className="grid grid-cols-1 md:grid-cols-3 gap-2.5">
                    <div>
                      <label className="block text-[#8d909b] mb-1 text-[11px]">PROXMOX API PORT</label>
                      <input
                        type="number"
                        value={apiPort}
                        onChange={e => setApiPort(Number(e.target.value))}
                        className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
                      />
                    </div>
                    <div>
                      <label className="block text-[#8d909b] mb-1 text-[11px]">PROXMOX API USER / REALM</label>
                      <input
                        type="text"
                        value={apiUser}
                        onChange={e => setApiUser(e.target.value)}
                        placeholder="root@pam"
                        className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#8cd6cb] font-bold focus:border-[#b0c9ff] outline-none"
                      />
                    </div>
                    <div>
                      <label className="block text-[#8d909b] mb-1 text-[11px]">AUTH METHOD</label>
                      <select
                        value={authMethod}
                        onChange={e => setAuthMethod(e.target.value)}
                        className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2 py-1.5 text-[#dfe1fa] outline-none"
                      >
                        <option value="ssh-ed25519 (ssh-agent)">ssh-ed25519 (ssh-agent)</option>
                        <option value="api-token">Proxmox API Token</option>
                        <option value="tailscale-ssh">Tailscale SSH</option>
                      </select>
                    </div>
                  </div>
                  <div className="grid grid-cols-1 md:grid-cols-2 gap-2.5 pt-1 border-t border-[#21253b]">
                    <div>
                      <label className="block text-[#8d909b] mb-1 text-[11px]">SSH FALLBACK PORT</label>
                      <input
                        type="number"
                        value={port}
                        onChange={e => setPort(Number(e.target.value))}
                        className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
                      />
                    </div>
                    <div>
                      <label className="block text-[#8d909b] mb-1 text-[11px]">SSH USER</label>
                      <input
                        type="text"
                        value={sshUser}
                        onChange={e => setSshUser(e.target.value)}
                        placeholder="root"
                        className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
                      />
                    </div>
                  </div>
                </div>
              )}

              {/* Windows Connection Options */}
              {nodeType === 'windows' && (
                <div className="grid grid-cols-1 md:grid-cols-4 gap-2.5">
                  <div>
                    <label className="block text-[#8d909b] mb-1 text-[11px]">WINRM PORT</label>
                    <input
                      type="number"
                      value={winrmPort}
                      onChange={e => setWinrmPort(Number(e.target.value))}
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
                    />
                  </div>
                  <div>
                    <label className="block text-[#8d909b] mb-1 text-[11px]">ADMIN USER</label>
                    <input
                      type="text"
                      value={sshUser}
                      onChange={e => setSshUser(e.target.value)}
                      placeholder="Administrator"
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#8cd6cb] font-bold focus:border-[#b0c9ff] outline-none"
                    />
                  </div>
                  <div>
                    <label className="block text-[#8d909b] mb-1 text-[11px]">RDP PORT</label>
                    <input
                      type="number"
                      value={rdpPort}
                      onChange={e => setRdpPort(Number(e.target.value))}
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
                    />
                  </div>
                  <div>
                    <label className="block text-[#8d909b] mb-1 text-[11px]">AUTH METHOD</label>
                    <select
                      value={authMethod}
                      onChange={e => setAuthMethod(e.target.value)}
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2 py-1.5 text-[#dfe1fa] outline-none"
                    >
                      <option value="ssh-ed25519 (ssh-agent)">ssh-ed25519 (OpenSSH)</option>
                      <option value="ntlm">WinRM NTLM / Password</option>
                      <option value="kerberos">Kerberos Realm</option>
                      <option value="certificate">Client Certificate</option>
                    </select>
                  </div>
                </div>
              )}

              {/* Router / Switch Connection Options */}
              {nodeType === 'router_switch' && (
                <div className="grid grid-cols-1 md:grid-cols-3 gap-2.5">
                  <div>
                    <label className="block text-[#8d909b] mb-1 text-[11px]">MANAGEMENT PORT</label>
                    <input
                      type="number"
                      value={port}
                      onChange={e => setPort(Number(e.target.value))}
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
                    />
                  </div>
                  <div>
                    <label className="block text-[#8d909b] mb-1 text-[11px]">MANAGEMENT USER</label>
                    <input
                      type="text"
                      value={mgmtUser}
                      onChange={e => setMgmtUser(e.target.value)}
                      placeholder="vyos"
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#8cd6cb] font-bold focus:border-[#b0c9ff] outline-none"
                    />
                  </div>
                  <div>
                    <label className="block text-[#8d909b] mb-1 text-[11px]">AUTH METHOD</label>
                    <select
                      value={authMethod}
                      onChange={e => setAuthMethod(e.target.value)}
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2 py-1.5 text-[#dfe1fa] outline-none"
                    >
                      <option value="ssh-ed25519 (ssh-agent)">ssh-ed25519 (ssh-agent)</option>
                      <option value="pam-password">Password Authentication</option>
                      <option value="api-token">REST API Token</option>
                    </select>
                  </div>
                </div>
              )}

              {/* VM / LXC Connection Options */}
              {(nodeType === 'vm' || nodeType === 'lxc') && (
                <div className="grid grid-cols-1 md:grid-cols-4 gap-2.5">
                  <div>
                    <label className="block text-[#8d909b] mb-1 text-[11px]">HYPERVISOR HOST</label>
                    <input
                      type="text"
                      value={hypervisorHost}
                      onChange={e => setHypervisorHost(e.target.value)}
                      placeholder="pve-node-01"
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
                    />
                  </div>
                  <div>
                    <label className="block text-[#8d909b] mb-1 text-[11px]">VM / CT ID</label>
                    <input
                      type="number"
                      value={vmId ?? ''}
                      onChange={e => setVmId(e.target.value ? Number(e.target.value) : undefined)}
                      placeholder="100"
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
                    />
                  </div>
                  <div>
                    <label className="block text-[#8d909b] mb-1 text-[11px]">SSH USER</label>
                    <input
                      type="text"
                      value={sshUser}
                      onChange={e => setSshUser(e.target.value)}
                      placeholder="core"
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#8cd6cb] font-bold focus:border-[#b0c9ff] outline-none"
                    />
                  </div>
                  <div>
                    <label className="block text-[#8d909b] mb-1 text-[11px]">SSH PORT</label>
                    <input
                      type="number"
                      value={port}
                      onChange={e => setPort(Number(e.target.value))}
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
                    />
                  </div>
                </div>
              )}
            </div>
          </div>

          {/* Section 3: Network Routes & Failover */}
          <div>
            <div className="text-[11px] font-bold uppercase tracking-wider text-[#b0c9ff] mb-2 flex items-center justify-between">
              <div className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[14px]">alt_route</span>
                <span>Network Routes &amp; Failover</span>
              </div>
              <div className="flex items-center gap-1 text-[10px] text-[#8cd6cb] bg-[#162725] px-2 py-0.5 rounded border border-[#1f423d]">
                <span className="w-1.5 h-1.5 rounded-full bg-[#8cd6cb] animate-pulse"></span>
                <span>Active Route: {routes.primary}</span>
              </div>
            </div>

            <div className="bg-[#131628] p-3 rounded border border-[#21253b] space-y-3">
              {/* Read-Only Tailscale Routes */}
              <div>
                <label className="block text-[#8d909b] mb-1 text-[10px] uppercase font-semibold">
                  Fetched Tailscale Mesh Routes (Read-Only)
                </label>
                <div className="grid grid-cols-1 md:grid-cols-3 gap-2">
                  <div className="bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 flex flex-col justify-center">
                    <span className="text-[10px] text-[#8d909b]">TAILSCALE TAG</span>
                    <span className="text-[#dfe1fa] font-mono truncate">{node.tailscaleTag || node.group || 'tag:host'}</span>
                  </div>
                  <div className="bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 flex flex-col justify-center">
                    <span className="text-[10px] text-[#8d909b]">TAILSCALE IP</span>
                    <span className="text-[#8cd6cb] font-mono truncate">{node.tailscaleIp || 'Not Assigned'}</span>
                  </div>
                  <div className="bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 flex flex-col justify-center">
                    <span className="text-[10px] text-[#8d909b]">TAILSCALE HOSTNAME</span>
                    <span className="text-[#dfe1fa] font-mono truncate">{node.tailscaleHost || 'Not Assigned'}</span>
                  </div>
                </div>
              </div>

              {/* Configurable Failover Route */}
              <div className="pt-2 border-t border-[#21253b]">
                <div className="flex items-center justify-between mb-1">
                  <label className="block text-[#8d909b] text-[10px] uppercase font-semibold">
                    Configurable Secondary / Failover Route
                  </label>
                  {routes.fallback && (
                    <span className="text-[10px] text-[#e0a82e]">
                      Fallback: {routes.fallback}
                    </span>
                  )}
                </div>
                <div className="grid grid-cols-1 md:grid-cols-2 gap-2.5">
                  <div>
                    <label className="block text-[#8d909b] mb-1 text-[11px]">FALLBACK LAN IP</label>
                    <input
                      type="text"
                      value={fallbackIp}
                      onChange={e => setFallbackIp(e.target.value)}
                      placeholder="192.168.1.100"
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
                    />
                  </div>
                  <div>
                    <label className="block text-[#8d909b] mb-1 text-[11px]">FALLBACK LOCAL HOSTNAME</label>
                    <input
                      type="text"
                      value={fallbackHost}
                      onChange={e => setFallbackHost(e.target.value)}
                      placeholder="hostname.local"
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
                    />
                  </div>
                </div>
                <p className="text-[10px] text-[#6d7182] mt-1.5">
                  Fallback route is automatically used if the Tailscale mesh connection times out or drops packets.
                </p>
              </div>
            </div>
          </div>

          {/* Section 4: Auto-Probed Hardware & Platform Telemetry Card */}
          <div className="bg-[#121526] border border-[#262a40] rounded-lg p-3 space-y-3">
            <div className="flex items-center justify-between flex-wrap gap-2">
              <div className="flex items-center gap-2">
                <span className="material-symbols-outlined text-[#b0c9ff] text-[16px]">memory</span>
                <span className="text-[11px] font-bold uppercase tracking-wider text-[#dfe1fa]">
                  Hardware &amp; Platform Telemetry
                </span>
                <span className="px-1.5 py-0.5 rounded text-[10px] bg-[#1a2d42] text-[#b0c9ff] border border-[#294666] font-semibold">
                  Auto-Probed via Remote Host
                </span>
                <span className="px-1.5 py-0.5 rounded text-[10px] bg-[#1b332b] text-[#8cd6cb] border border-[#295446] font-semibold">
                  Auto-Discovered
                </span>
              </div>

              <button
                type="button"
                onClick={handleProbeHardware}
                disabled={probing}
                className="px-2.5 py-1 rounded bg-[#20253e] hover:bg-[#2c3355] text-[#b0c9ff] border border-[#373e66] flex items-center gap-1.5 cursor-pointer disabled:opacity-50 transition-colors text-[11px]"
              >
                <span className={`material-symbols-outlined text-[14px] ${probing ? 'animate-spin' : ''}`}>
                  sync
                </span>
                <span>{probing ? 'Probing Remote Host...' : 'Re-probe Hardware'}</span>
              </button>
            </div>

            {probeStatus && (
              <div
                className={`p-2 rounded text-[11px] flex items-center gap-2 border ${
                  probeStatus.success
                    ? 'bg-[#12281e] text-[#8cd6cb] border-[#1d4c38]'
                    : 'bg-[#331114] text-[#ffb4ab] border-[#5e2025]'
                }`}
              >
                <span className="material-symbols-outlined text-[15px]">
                  {probeStatus.success ? 'check_circle' : 'error'}
                </span>
                <span>{probeStatus.message}</span>
              </div>
            )}

            {/* Read-only Hardware Grid */}
            <div className="grid grid-cols-2 md:grid-cols-4 gap-2 text-[11px]">
              <div className="bg-[#171a2e] p-2 rounded border border-[#22263d]">
                <span className="block text-[10px] text-[#8d909b]">CPU MODEL</span>
                <span className="text-[#dfe1fa] font-semibold truncate block" title={specs.cpu || 'Not probed'}>
                  {specs.cpu || 'Not probed'}
                </span>
              </div>
              <div className="bg-[#171a2e] p-2 rounded border border-[#22263d]">
                <span className="block text-[10px] text-[#8d909b]">vCPUs / CORES</span>
                <span className="text-[#dfe1fa] font-semibold block">{specs.vcpus || 1} Cores</span>
              </div>
              <div className="bg-[#171a2e] p-2 rounded border border-[#22263d]">
                <span className="block text-[10px] text-[#8d909b]">SYSTEM RAM</span>
                <span className="text-[#dfe1fa] font-semibold block">{specs.ramGiB ? `${specs.ramGiB} GiB` : '—'}</span>
              </div>
              <div className="bg-[#171a2e] p-2 rounded border border-[#22263d]">
                <span className="block text-[10px] text-[#8d909b]">MOTHERBOARD / PLATFORM</span>
                <span className="text-[#dfe1fa] font-semibold truncate block" title={specs.motherboard || '—'}>
                  {specs.motherboard || 'Homelab Host Platform'}
                </span>
              </div>
              <div className="bg-[#171a2e] p-2 rounded border border-[#22263d]">
                <span className="block text-[10px] text-[#8d909b]">BIOS / FIRMWARE</span>
                <span className="text-[#dfe1fa] font-semibold truncate block" title={specs.bios || '—'}>
                  {specs.bios || 'UEFI / SeaBIOS'}
                </span>
              </div>
              <div className="bg-[#171a2e] p-2 rounded border border-[#22263d]">
                <span className="block text-[10px] text-[#8d909b]">SECURE BOOT</span>
                <span className={`font-semibold block ${specs.secureBoot === 'Enabled' ? 'text-[#8cd6cb]' : 'text-[#8d909b]'}`}>
                  {specs.secureBoot || 'Disabled'}
                </span>
              </div>
              <div className="bg-[#171a2e] p-2 rounded border border-[#22263d]">
                <span className="block text-[10px] text-[#8d909b]">IOMMU / VIRTUALIZATION</span>
                <span className={`font-semibold block ${specs.iommu && specs.iommu.startsWith('Enabled') ? 'text-[#8cd6cb]' : 'text-[#8d909b]'}`}>
                  {specs.iommu || 'Disabled'}
                </span>
              </div>
              <div className="bg-[#171a2e] p-2 rounded border border-[#22263d]">
                <span className="block text-[10px] text-[#8d909b]">IPMI / BMC</span>
                <span className="text-[#dfe1fa] font-semibold block">{specs.ipmi || 'DHCP'}</span>
              </div>
              <div className="bg-[#171a2e] p-2 rounded border border-[#22263d]">
                <span className="block text-[10px] text-[#8d909b]">ECC MEMORY</span>
                <span className="text-[#dfe1fa] font-semibold block">{specs.ecc || 'Non-ECC'}</span>
              </div>
              <div className="bg-[#171a2e] p-2 rounded border border-[#22263d] col-span-1 md:col-span-3">
                <span className="block text-[10px] text-[#8d909b]">OPERATING SYSTEM</span>
                <span className="text-[#dfe1fa] font-semibold truncate block" title={osDescription || 'Linux (Generic)'}>
                  {osDescription || 'Linux (Generic)'}
                </span>
              </div>
            </div>

            <p className="text-[10px] text-[#6d7182] italic">
              Hardware telemetry is discovered automatically from host interfaces (/proc, /sys/class/dmi) via SSH daemon.
              Telemetry values cannot be altered manually to maintain telemetry integrity.
            </p>
          </div>

          {/* SSH verification checkbox */}
          <label className="flex items-center gap-2 text-[#c3c6d2] cursor-pointer pt-1">
            <input
              type="checkbox"
              checked={testSshOnSave}
              onChange={e => setTestSshOnSave(e.target.checked)}
              className="rounded border-[#434750] accent-[#b0c9ff]"
            />
            <span>Verify SSH/Remote connection via ssh-agent upon saving</span>
          </label>

          {/* Modal Footer / Actions */}
          <div className="pt-2 flex items-center justify-between border-t border-[#26293c]">
            {onDeleteNode ? (
              <button
                type="button"
                onClick={() => {
                  onDeleteNode(node.id);
                  onClose();
                }}
                className="px-3 py-1.5 bg-[#93000a]/40 hover:bg-[#93000a]/70 text-[#ffb4ab] rounded cursor-pointer transition-colors text-xs flex items-center gap-1"
              >
                <span className="material-symbols-outlined text-[14px]">delete</span>
                <span>Remove Node</span>
              </button>
            ) : (
              <div />
            )}

            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={onClose}
                className="px-3 py-1.5 bg-[#1b1e31] hover:bg-[#26293c] text-[#c3c6d2] rounded cursor-pointer transition-colors text-xs"
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={saving}
                className="px-3.5 py-1.5 bg-[#b0c9ff] hover:bg-[#d8e2ff] text-[#002e68] font-bold rounded cursor-pointer flex items-center gap-1.5 shadow-sm transition-colors text-xs disabled:opacity-50"
              >
                <span className="material-symbols-outlined text-[15px]">save</span>
                <span>{saving ? 'Saving Settings...' : 'Save Settings'}</span>
              </button>
            </div>
          </div>
        </form>
      </div>
    </div>
  );
};
