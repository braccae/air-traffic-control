import React, { useState, useEffect } from 'react';
import type { NodeItem, NodeType } from '../../types/index.ts';
import { buildProvisionedNode } from '../../utils/navigationViewModel.ts';
import type { ProbedHardware } from '../../utils/hardwareProbe.ts';

interface AddNodeModalProps {
  isOpen: boolean;
  isMinimized?: boolean;
  onMinimize?: () => void;
  onClose: () => void;
  onAddNode: (node: Partial<NodeItem>) => void;
}

interface TailscalePeerOption {
  hostName: string;
  dnsName?: string;
  ip: string;
  os?: string;
  online?: boolean;
}

export const AddNodeModal: React.FC<AddNodeModalProps> = ({
  isOpen,
  isMinimized = false,
  onMinimize,
  onClose,
  onAddNode,
}) => {
  const [name, setName] = useState('');
  const [group, setGroup] = useState('tag:host');
  const [nodeType, setNodeType] = useState<NodeType>('linux');
  const [ip, setIp] = useState('');
  const [port, setPort] = useState(22);
  const [sshUser, setSshUser] = useState('core');
  const [tailscaleIp, setTailscaleIp] = useState('');
  const [tailscaleHost, setTailscaleHost] = useState('');
  const [authMethod, setAuthMethod] = useState('ssh-ed25519 (ssh-agent)');

  // Unmanaged Tailscale peers from mesh
  const [tsPeers, setTsPeers] = useState<TailscalePeerOption[]>([]);
  const [selectedPeer, setSelectedPeer] = useState<string>('');

  // Probing state
  const [probing, setProbing] = useState(false);
  const [probeError, setProbeError] = useState<string | null>(null);
  const [probedData, setProbedData] = useState<ProbedHardware | null>(null);

  // Fetch Tailscale peers on mount/open
  useEffect(() => {
    if (!isOpen) return;
    setProbeError(null);
    fetch('/api/tailscale/peers')
      .then(res => res.json())
      .then(data => {
        if (data.success && Array.isArray(data.peers)) {
          const peersList: TailscalePeerOption[] = data.peers.map((p: any) => ({
            hostName: p.HostName || p.DNSName?.split('.')[0] || 'unknown',
            dnsName: p.DNSName ? p.DNSName.replace(/\.$/, '') : undefined,
            ip: p.TailscaleIPs?.[0] || '',
            os: p.OS || '',
            online: p.Online ?? true,
          }));
          setTsPeers(peersList);
        }
      })
      .catch(() => {});
  }, [isOpen]);

  if (!isOpen) return null;

  const handleSelectPeer = (peerHostName: string) => {
    setSelectedPeer(peerHostName);
    const peer = tsPeers.find(p => p.hostName === peerHostName);
    if (peer) {
      setName(peer.hostName);
      if (peer.ip) {
        setTailscaleIp(peer.ip);
        if (!ip) setIp(peer.ip);
      }
      if (peer.dnsName) {
        setTailscaleHost(peer.dnsName);
      }
      if (peer.os?.toLowerCase().includes('windows')) {
        setNodeType('windows');
      } else {
        setNodeType('linux');
      }
    }
  };

  const handleProbeAndAdd = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) return;

    setProbing(true);
    setProbeError(null);

    const targetAddress = tailscaleIp || ip || name.trim();
    let hardware: ProbedHardware | null = probedData;

    try {
      const resp = await fetch('/api/nodes/probe-host', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          host: targetAddress,
          port,
          sshUser,
          authMethod,
        }),
      });

      const data = await resp.json();
      if (data.success && data.hardware) {
        hardware = data.hardware;
        setProbedData(data.hardware);
      } else {
        setProbeError(data.error || 'Host unreachable or SSH probe failed');
      }
    } catch (err: any) {
      setProbeError(err?.message || 'Connection to probe endpoint failed');
    } finally {
      setProbing(false);
    }

    // Build provisioned node with auto-probed specs if available, otherwise sensible defaults
    const nodeSpecs = hardware
      ? {
          cpu: hardware.cpu,
          vcpus: hardware.vcpus,
          ramGiB: hardware.ramGiB,
          motherboard: hardware.motherboard,
          bios: hardware.bios,
          ipmi: 'DHCP',
          ecc: 'Active',
          secureBoot: hardware.secureBoot || 'Enabled',
          iommu: hardware.iommu || 'Enabled',
        }
      : {
          cpu: 'Auto-detected Host CPU',
          vcpus: 4,
          ramGiB: 8,
          motherboard: 'Host Chassis',
          bios: 'UEFI 2.8',
          ipmi: 'DHCP',
          ecc: 'Active',
          secureBoot: 'Enabled',
          iommu: 'Enabled',
        };

    const osDesc = hardware?.osDescription || (nodeType === 'windows' ? 'Windows Server' : 'Linux (Probing)');

    onAddNode(
      buildProvisionedNode({
        name: name.trim(),
        group,
        datacenter: 'leo-banjo.ts.net',
        nodeType,
        ip: ip || targetAddress,
        port,
        sshUser,
        authMethod,
        tailscaleIp: tailscaleIp || (targetAddress.startsWith('100.') ? targetAddress : ''),
        tailscaleHost: tailscaleHost || `${name.trim().toLowerCase()}.leo-banjo.ts.net`,
        osDescription: osDesc,
        specs: nodeSpecs,
      })
    );

    onClose();
  };

  return (
    <div
      style={{ display: isMinimized ? 'none' : 'flex' }}
      className="fixed inset-0 z-50 items-center justify-center bg-black/70 backdrop-blur-xs p-4"
    >
      <div className="w-full max-w-lg bg-[#0a0d1f] rounded-lg shadow-2xl border border-[#313447] overflow-hidden">
        <div className="h-9 bg-[#171a2d] px-3 flex items-center justify-between border-b border-[#26293c]">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[#8cd6cb] text-[18px]">add_circle</span>
            <span className="font-mono text-xs text-[#dfe1fa] font-bold">
              Provision / Add Node to Datacenter
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

        <form onSubmit={handleProbeAndAdd} className="p-4 space-y-3 font-mono text-xs">
          {/* Node Type Selector */}
          <div>
            <label className="block text-[#8d909b] mb-1">NODE ARCHITECTURE &amp; TARGET</label>
            <div className="grid grid-cols-3 gap-2">
              <button
                type="button"
                onClick={() => setNodeType('linux')}
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
                onClick={() => setNodeType('windows')}
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
                onClick={() => setNodeType('router_switch')}
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

          {/* Tailscale Mesh Peer Quick Picker */}
          {tsPeers.length > 0 && (
            <div>
              <label className="block text-[#8d909b] mb-1">SELECT FROM TAILSCALE MESH (OPTIONAL)</label>
              <select
                value={selectedPeer}
                onChange={e => handleSelectPeer(e.target.value)}
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
              >
                <option value="">-- Or enter host details manually below --</option>
                {tsPeers.map(p => (
                  <option key={p.hostName} value={p.hostName}>
                    {p.hostName} ({p.ip || 'no-ip'}) {p.os ? `[${p.os}]` : ''}
                  </option>
                ))}
              </select>
            </div>
          )}

          {/* Node Name & Group */}
          <div className="grid grid-cols-3 gap-2">
            <div>
              <label className="block text-[#8d909b] mb-1">HOST NAME / ADDRESS</label>
              <input
                type="text"
                required
                value={name}
                onChange={e => setName(e.target.value)}
                placeholder="e.g. likes-it-cold"
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
              />
            </div>
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
              <label className="block text-[#8d909b] mb-1">GROUP / TAG</label>
              <select
                value={group}
                onChange={e => setGroup(e.target.value)}
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
              >
                <option value="tag:host">tag:host (Container Hosts)</option>
                <option value="tag:pve">tag:pve (Proxmox Hypervisors)</option>
                <option value="tag:service">tag:service (Mesh Services)</option>
                <option value="tag:admin">tag:admin (Workstations)</option>
              </select>
            </div>
          </div>

          {/* Network: IP and SSH Port */}
          <div className="grid grid-cols-2 gap-2">
            <div>
              <label className="block text-[#8d909b] mb-1">LAN IP ADDRESS &amp; PORT</label>
              <div className="flex gap-1">
                <input
                  type="text"
                  value={ip}
                  onChange={e => setIp(e.target.value)}
                  placeholder="192.168.8.180"
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

          {/* Authentication Mechanism */}
          <div>
            <label className="block text-[#8d909b] mb-1">AUTHENTICATION METHOD</label>
            <select
              value={authMethod}
              onChange={e => setAuthMethod(e.target.value)}
              className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] outline-none"
            >
              <option value="ssh-ed25519 (ssh-agent)">ssh-agent forwarding (ED25519 socket)</option>
              <option value="tailscale-ssh">Tailscale SSH (Keyless / PAM)</option>
              <option value="pam-password">Password / GSSAPI</option>
            </select>
          </div>

          {/* Auto-Probe Info Note */}
          <div className="p-2.5 rounded bg-[#121528] border border-[#26293c] text-[11px] text-[#8d909b] flex items-start gap-2">
            <span className="material-symbols-outlined text-[#8cd6cb] text-[16px] shrink-0 mt-0.5">
              auto_awesome
            </span>
            <div>
              <span className="text-[#dfe1fa] font-semibold">Automatic Hardware Discovery:</span>
              <p className="mt-0.5 text-[10px]">
                CPU model, vCPUs, RAM, OS distribution, motherboard, and BIOS are automatically queried from the host via native kernel interfaces upon connection.
              </p>
            </div>
          </div>

          {/* Probing Feedback / Error Warning */}
          {probing && (
            <div className="flex items-center gap-2 text-[#8cd6cb] bg-[#121528] p-2 rounded border border-[#8cd6cb]/30">
              <span className="material-symbols-outlined text-[16px] animate-spin">sync</span>
              <span>Probing host hardware via SSH...</span>
            </div>
          )}

          {probeError && (
            <div className="flex items-center gap-2 text-[#ffb4ab] bg-[#2d161a] p-2 rounded border border-[#ffb4ab]/30 text-[11px]">
              <span className="material-symbols-outlined text-[16px] shrink-0">warning</span>
              <div className="flex-1">
                <span>{probeError}</span>
              </div>
            </div>
          )}

          {/* Action Buttons */}
          <div className="pt-2 flex items-center justify-end gap-2 border-t border-[#26293c]">
            <button
              type="button"
              onClick={onClose}
              disabled={probing}
              className="px-3 py-1.5 bg-[#1b1e31] hover:bg-[#26293c] text-[#c3c6d2] rounded cursor-pointer disabled:opacity-50"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={probing || !name.trim()}
              className="px-3 py-1.5 bg-[#b0c9ff] hover:bg-[#d8e2ff] text-[#002e68] font-bold rounded cursor-pointer flex items-center gap-1 shadow-sm disabled:opacity-50"
            >
              <span className="material-symbols-outlined text-[15px]">
                {probing ? 'sync' : 'auto_detect_voice'}
              </span>
              <span>{probing ? 'Probing...' : 'Probe & Add'}</span>
            </button>
          </div>
        </form>
      </div>
    </div>
  );
};
