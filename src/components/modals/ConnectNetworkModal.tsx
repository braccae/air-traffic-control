import React, { useState, useEffect } from 'react';
import type { NodeItem } from '../../types/index.ts';
import { validateInterfaceName, validateCidrOrIp } from '../../utils/firewallRules.ts';
import { getDriver, networkDriver, stageTaskInQueue } from '../../utils/drivers/index.ts';

interface ConnectNetworkModalProps {
  node?: NodeItem;
  nodes?: NodeItem[];
  isOpen: boolean;
  isMinimized?: boolean;
  onMinimize?: () => void;
  onClose: () => void;
  onConnected?: () => void;
  existingInterfaces?: string[];
}

export const ConnectNetworkModal: React.FC<ConnectNetworkModalProps> = ({
  node,
  nodes = [],
  isOpen,
  isMinimized = false,
  onMinimize,
  onClose,
  onConnected,
  existingInterfaces = [],
}) => {
  const [targetNodeId, setTargetNodeId] = useState<string>(node?.id || '');
  const [networkType, setNetworkType] = useState<'wireguard' | 'bridge' | 'vlan' | 'static' | 'dhcp'>('wireguard');
  const [interfaceName, setInterfaceName] = useState('wg1');
  const [parentInterface, setParentInterface] = useState('');
  const [vlanId, setVlanId] = useState(10);
  const [cidr, setCidr] = useState('10.0.0.1/24');
  const [gateway, setGateway] = useState('');
  const [dnsServers, setDnsServers] = useState('1.1.1.1, 8.8.8.8');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (node?.id) {
      setTargetNodeId(node.id);
    } else if (nodes.length > 0 && !targetNodeId) {
      setTargetNodeId(nodes[0].id);
    }
  }, [node?.id, nodes]);

  // Adjust defaults when type changes
  useEffect(() => {
    switch (networkType) {
      case 'wireguard':
        setInterfaceName('wg1');
        setCidr('10.0.0.1/24');
        break;
      case 'bridge':
        setInterfaceName('br0');
        setCidr('192.168.10.1/24');
        break;
      case 'vlan':
        setInterfaceName('vlan10');
        setCidr('10.10.10.50/24');
        break;
      case 'static':
        if (existingInterfaces.length > 0 && !interfaceName.startsWith('eth')) {
          setInterfaceName(existingInterfaces[0]);
        }
        setCidr('192.168.1.50/24');
        break;
      case 'dhcp':
        if (existingInterfaces.length > 0 && !interfaceName.startsWith('eth')) {
          setInterfaceName(existingInterfaces[0]);
        }
        setCidr('');
        break;
    }
  }, [networkType]);

  useEffect(() => {
    if (isOpen) {
      setError(null);
    }
  }, [isOpen]);

  if (!isOpen) return null;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);

    const resolvedNodeId = targetNodeId || node?.id || nodes[0]?.id;
    if (!resolvedNodeId) {
      setError('A target host node must be selected');
      return;
    }

    const ifaceVal = validateInterfaceName(interfaceName);
    if (!ifaceVal.valid) {
      setError(ifaceVal.error || 'Invalid interface name');
      return;
    }

    if (networkType === 'vlan' && !parentInterface.trim()) {
      setError('Parent interface is required for 802.1Q VLAN');
      return;
    }

    if (networkType === 'static' && !cidr.trim()) {
      setError('IP Address / CIDR is required for static network configuration');
      return;
    }

    if (cidr.trim()) {
      const cidrVal = validateCidrOrIp(cidr);
      if (!cidrVal.valid) {
        setError(cidrVal.error || 'Invalid IP address / CIDR');
        return;
      }
    }

    if (gateway.trim()) {
      const gwVal = validateCidrOrIp(gateway);
      if (!gwVal.valid) {
        setError(gwVal.error || 'Invalid gateway IP address');
        return;
      }
    }

    const dnsList = dnsServers
      .split(/[,;\s]+/)
      .map(s => s.trim())
      .filter(Boolean);

    setLoading(true);

    try {
      const resp = await fetch(`/api/nodes/${encodeURIComponent(resolvedNodeId)}/interfaces/connect`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          type: networkType,
          name: interfaceName.trim(),
          parent: parentInterface.trim() || undefined,
          vlanId: networkType === 'vlan' ? Number(vlanId) : undefined,
          cidr: cidr.trim() || undefined,
          gateway: gateway.trim() || undefined,
          dns: dnsList.length > 0 ? dnsList : undefined,
        }),
      });

      const data = await resp.json();
      if (!resp.ok || !data.success) {
        throw new Error(data.error || 'Failed to connect network interface');
      }

      if (onConnected) {
        onConnected();
      }
      onClose();
    } catch (err: any) {
      setError(err?.message || 'Error occurred while provisioning network interface');
    } finally {
      setLoading(false);
    }
  };

  const [staging, setStaging] = useState(false);

  const handleStageNetwork = async () => {
    setError(null);

    const resolvedNode = nodes.find(n => n.id === targetNodeId) || node || nodes[0];
    if (!resolvedNode) {
      setError('A target host node must be selected');
      return;
    }

    const ifaceVal = validateInterfaceName(interfaceName);
    if (!ifaceVal.valid) {
      setError(ifaceVal.error || 'Invalid interface name');
      return;
    }

    if (networkType === 'vlan' && !parentInterface.trim()) {
      setError('Parent interface is required for 802.1Q VLAN');
      return;
    }

    if (networkType === 'static' && !cidr.trim()) {
      setError('IP Address / CIDR is required for static network configuration');
      return;
    }

    if (cidr.trim()) {
      const cidrVal = validateCidrOrIp(cidr);
      if (!cidrVal.valid) {
        setError(cidrVal.error || 'Invalid IP address / CIDR');
        return;
      }
    }

    if (gateway.trim()) {
      const gwVal = validateCidrOrIp(gateway);
      if (!gwVal.valid) {
        setError(gwVal.error || 'Invalid gateway IP address');
        return;
      }
    }

    const dnsList = dnsServers
      .split(/[,;\s]+/)
      .map(s => s.trim())
      .filter(Boolean);

    setStaging(true);
    try {
      const driver = getDriver('network', resolvedNode) || networkDriver;
      const task = driver.compileMutation(resolvedNode, 'connect_interface', {
        type: networkType,
        name: interfaceName.trim(),
        parent: parentInterface.trim() || undefined,
        vlanId: networkType === 'vlan' ? Number(vlanId) : undefined,
        cidr: cidr.trim() || undefined,
        gateway: gateway.trim() || undefined,
        dns: dnsList.length > 0 ? dnsList : undefined,
      });

      const res = await stageTaskInQueue(task);
      if (res.success) {
        if (onConnected) onConnected();
        onClose();
      } else {
        setError(res.error || 'Failed to stage network task');
      }
    } catch (err: any) {
      setError(err?.message || 'Failed to stage network task');
    } finally {
      setStaging(false);
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
            <span className="material-symbols-outlined text-[#8cd6cb] text-[18px]">
              settings_ethernet
            </span>
            <span className="font-mono text-xs text-[#dfe1fa] font-bold">
              Connect Network / Add Interface
            </span>
          </div>
          <div className="flex items-center gap-1">
            {onMinimize && (
              <button
                type="button"
                onClick={onMinimize}
                className="text-[#8d909b] hover:text-[#dfe1fa] p-1 cursor-pointer transition-colors"
                title="Minimize [-]"
              >
                <span className="material-symbols-outlined text-[16px]">remove</span>
              </button>
            )}
            <button
              type="button"
              onClick={onClose}
              className="text-[#8d909b] hover:text-[#dfe1fa] p-1 cursor-pointer transition-colors"
              title="Close [✕]"
            >
              <span className="material-symbols-outlined text-[16px]">close</span>
            </button>
          </div>
        </div>

        {/* Modal Body Form */}
        <form onSubmit={handleSubmit} className="p-4 space-y-3 font-mono text-xs">
          {error && (
            <div className="p-2 bg-[#93000a]/20 border border-[#ffb4ab]/50 rounded text-[#ffb4ab] text-[11px] flex items-center gap-1.5">
              <span className="material-symbols-outlined text-[16px]">error</span>
              <span>{error}</span>
            </div>
          )}

          {/* Target Host if nodes provided */}
          {nodes.length > 1 && (
            <div>
              <label className="block text-[#8d909b] mb-1">TARGET HOST</label>
              <select
                value={targetNodeId}
                onChange={e => setTargetNodeId(e.target.value)}
                className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#8cd6cb] focus:outline-none"
              >
                {nodes.map(n => (
                  <option key={n.id} value={n.id}>
                    {n.name} ({n.ip})
                  </option>
                ))}
              </select>
            </div>
          )}

          {/* Interface Type */}
          <div>
            <label className="block text-[#8d909b] mb-1">NETWORK / INTERFACE TYPE</label>
            <div className="grid grid-cols-2 md:grid-cols-3 gap-1.5">
              {[
                { id: 'wireguard', label: 'WireGuard Tunnel', icon: 'vpn_lock' },
                { id: 'bridge', label: 'Linux Bridge (br)', icon: 'hub' },
                { id: 'vlan', label: '802.1Q VLAN', icon: 'alt_route' },
                { id: 'static', label: 'Static IP (Link)', icon: 'pin' },
                { id: 'dhcp', label: 'DHCP Client', icon: 'dynamic_form' },
              ].map(t => (
                <button
                  key={t.id}
                  type="button"
                  onClick={() => setNetworkType(t.id as any)}
                  className={`flex items-center gap-1.5 px-2.5 py-1.5 rounded border text-left cursor-pointer transition-colors ${
                    networkType === t.id
                      ? 'bg-[#8cd6cb]/15 border-[#8cd6cb] text-[#8cd6cb] font-bold'
                      : 'bg-[#171a2d] border-[#26293c] text-[#c3c6d2] hover:bg-[#26293c]'
                  }`}
                >
                  <span className="material-symbols-outlined text-[16px]">{t.icon}</span>
                  <span className="truncate">{t.label}</span>
                </button>
              ))}
            </div>
          </div>

          {/* Interface Name & Parent */}
          <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
            <div>
              <label className="block text-[#8d909b] mb-1">INTERFACE NAME</label>
              <input
                type="text"
                required
                value={interfaceName}
                onChange={e => setInterfaceName(e.target.value)}
                placeholder="e.g. wg1, br0, vlan10"
                className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#8cd6cb] focus:outline-none"
              />
            </div>

            {(networkType === 'bridge' || networkType === 'vlan') && (
              <div>
                <label className="block text-[#8d909b] mb-1">
                  PARENT INTERFACE {networkType === 'vlan' ? '(Required)' : '(Optional)'}
                </label>
                {existingInterfaces.length > 0 ? (
                  <select
                    value={parentInterface}
                    onChange={e => setParentInterface(e.target.value)}
                    className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#8cd6cb] focus:outline-none"
                  >
                    <option value="">-- Select Parent Interface --</option>
                    {existingInterfaces.map(iface => (
                      <option key={iface} value={iface}>
                        {iface}
                      </option>
                    ))}
                  </select>
                ) : (
                  <input
                    type="text"
                    value={parentInterface}
                    onChange={e => setParentInterface(e.target.value)}
                    placeholder="e.g. eth0 or enp3s0"
                    className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#8cd6cb] focus:outline-none"
                  />
                )}
              </div>
            )}

            {networkType === 'vlan' && (
              <div>
                <label className="block text-[#8d909b] mb-1">VLAN ID (1-4094)</label>
                <input
                  type="number"
                  min="1"
                  max="4094"
                  value={vlanId}
                  onChange={e => setVlanId(Number(e.target.value))}
                  className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#8cd6cb] focus:outline-none"
                />
              </div>
            )}
          </div>

          {/* IP / CIDR Configuration (unless pure DHCP) */}
          {networkType !== 'dhcp' && (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
              <div>
                <label className="block text-[#8d909b] mb-1">IP ADDRESS / CIDR</label>
                <input
                  type="text"
                  required={networkType === 'static'}
                  value={cidr}
                  onChange={e => setCidr(e.target.value)}
                  placeholder="e.g. 192.168.10.50/24"
                  className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#8cd6cb] focus:outline-none"
                />
              </div>

              <div>
                <label className="block text-[#8d909b] mb-1">GATEWAY IP (OPTIONAL)</label>
                <input
                  type="text"
                  value={gateway}
                  onChange={e => setGateway(e.target.value)}
                  placeholder="e.g. 192.168.10.1"
                  className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#8cd6cb] focus:outline-none"
                />
              </div>
            </div>
          )}

          {/* DNS Servers */}
          {(networkType === 'static' || networkType === 'wireguard') && (
            <div>
              <label className="block text-[#8d909b] mb-1">DNS SERVERS (OPTIONAL)</label>
              <input
                type="text"
                value={dnsServers}
                onChange={e => setDnsServers(e.target.value)}
                placeholder="e.g. 1.1.1.1, 8.8.8.8"
                className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#8cd6cb] focus:outline-none"
              />
            </div>
          )}

          {/* Action Buttons */}
          <div className="pt-3 border-t border-[#26293c] flex items-center justify-end gap-2">
            <button
              type="button"
              onClick={onClose}
              disabled={loading || staging}
              className="px-3 py-1.5 bg-[#171a2d] hover:bg-[#26293c] text-[#c3c6d2] rounded font-mono text-xs transition-colors cursor-pointer disabled:opacity-50"
            >
              Cancel
            </button>
            <button
              type="button"
              disabled={loading || staging}
              onClick={handleStageNetwork}
              className="px-3 py-1.5 bg-[#171a2d] hover:bg-[#26293c] text-[#ffd97d] border border-[#ffd97d]/40 rounded font-mono text-xs cursor-pointer font-bold flex items-center gap-1.5 transition-colors disabled:opacity-50"
              title="Stage network interface configuration into Ansible Mutation Queue without immediate execution"
            >
              <span className="material-symbols-outlined text-[15px]">queue</span>
              <span>{staging ? 'Staging...' : 'Stage in Queue'}</span>
            </button>
            <button
              type="submit"
              disabled={loading || staging}
              className="px-4 py-1.5 bg-[#8cd6cb] hover:bg-[#a0ebe0] text-[#002e68] font-bold rounded font-mono text-xs transition-colors flex items-center gap-1.5 cursor-pointer disabled:opacity-50"
            >
              {loading && <span className="material-symbols-outlined text-[16px] animate-spin">sync</span>}
              <span>{loading ? 'Connecting...' : 'Connect Interface'}</span>
            </button>
          </div>
        </form>
      </div>
    </div>
  );
};
