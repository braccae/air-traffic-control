import React, { useState, useEffect } from 'react';
import type { NodeItem } from '../types/index.ts';
import { ConnectNetworkModal } from './modals/ConnectNetworkModal.tsx';

interface NetworkInterfacesViewProps {
  node: NodeItem;
  onRefresh?: () => void;
}

export const NetworkInterfacesView: React.FC<NetworkInterfacesViewProps> = ({ node }) => {
  const [interfaces, setInterfaces] = useState<Array<{
    name: string;
    type: string;
    ip: string;
    mac: string;
    speed: string;
    rx: string;
    tx: string;
    status: string;
    mtu: number;
  }>>([]);
  const [loading, setLoading] = useState(false);
  const [toggling, setToggling] = useState<string | null>(null);

  // Connect network modal state
  const [showConnectModal, setShowConnectModal] = useState(false);
  const [isModalMinimized, setIsModalMinimized] = useState(false);

  const fetchInterfaces = (refresh = false) => {
    setLoading(true);
    fetch(`/api/nodes/${encodeURIComponent(node.id)}/interfaces${refresh ? '?refresh=true' : ''}`)
      .then(res => res.json())
      .then(data => {
        if (data.success && Array.isArray(data.interfaces)) {
          setInterfaces(data.interfaces);
        }
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    fetchInterfaces(false);
  }, [node.id, node.shortName]);

  const toggleInterface = async (name: string) => {
    const iface = interfaces.find(i => i.name === name);
    const nextState = iface?.status.includes('UP') ? 'down' : 'up';
    setToggling(name);
    try {
      const res = await fetch(`/api/nodes/${encodeURIComponent(node.id)}/interfaces/${encodeURIComponent(name)}/toggle`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ state: nextState }),
      });
      if (res.ok) {
        fetchInterfaces(true);
      }
    } catch {
      // Revert if error
    } finally {
      setToggling(null);
    }
  };

  return (
    <section className="bg-[#171a2d] rounded-lg p-4 flex flex-col border border-[#313447] shadow-lg">
      <div className="flex flex-wrap items-center justify-between gap-2 pb-3 border-b border-[#26293c]">
        <div className="flex items-center gap-2">
          <span className="material-symbols-outlined text-[#8cd6cb] text-[22px]">cable</span>
          <div>
            <h3 className="font-sans text-sm text-[#dfe1fa] font-bold">
              Network Interfaces &amp; Link Aggregation ({node.shortName})
            </h3>
            <p className="font-mono text-[11px] text-[#8d909b]">
              Kernel routing table, Tailscale WireGuard virtual adapters &amp; hardware NICs
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2 font-mono text-xs">
          <button
            type="button"
            onClick={() => fetchInterfaces(true)}
            disabled={loading}
            className="flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] px-2.5 py-1 rounded transition-colors border border-[#434750]/40 cursor-pointer disabled:opacity-50"
          >
            <span className={`material-symbols-outlined text-[14px] ${loading ? 'animate-spin' : ''}`}>sync</span>
            <span>Refresh Interfaces</span>
          </button>
          <button
            type="button"
            onClick={() => {
              setIsModalMinimized(false);
              setShowConnectModal(true);
            }}
            className="flex items-center gap-1 bg-[#8cd6cb] hover:bg-[#a0ebe0] text-[#002e68] font-bold px-2.5 py-1 rounded transition-colors cursor-pointer"
          >
            <span className="material-symbols-outlined text-[14px]">add</span>
            <span>+ Connect Network</span>
          </button>
        </div>
      </div>

      <div className="overflow-x-auto mt-3 font-mono text-xs">
        <table className="w-full text-left">
          <thead className="bg-[#0a0d1f] text-[#8d909b] text-[10px] uppercase">
            <tr className="h-7">
              <th className="px-3">Interface</th>
              <th className="px-3">Type</th>
              <th className="px-3">IPv4 / Subnet</th>
              <th className="px-3">Hardware MAC</th>
              <th className="px-3">Speed / Duplex</th>
              <th className="px-3">MTU</th>
              <th className="px-3">RX / TX</th>
              <th className="px-3">Status</th>
              <th className="px-3 text-right">Action</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[#26293c]/60">
            {interfaces.map(iface => {
              const isUp = iface.status.includes('UP');
              return (
                <tr key={iface.name} className="h-10 hover:bg-[#26293c]/40 transition-colors">
                  <td className="px-3 font-bold text-[#dfe1fa] flex items-center gap-2 pt-2.5">
                    <span
                      className={`w-2 h-2 rounded-full ${
                        isUp ? 'bg-[#8cd6cb] shadow-[0_0_6px_rgba(140,214,203,0.8)]' : 'bg-[#ffb4ab]'
                      }`}
                    ></span>
                    <span>{iface.name}</span>
                  </td>
                  <td className="px-3 text-[#b0c9ff]">{iface.type}</td>
                  <td className="px-3 font-semibold text-[#dfe1fa]">{iface.ip}</td>
                  <td className="px-3 text-[#8d909b]">{iface.mac}</td>
                  <td className="px-3 text-[#d9b9ff]">{iface.speed}</td>
                  <td className="px-3 text-[#c3c6d2]">{iface.mtu || 1500}</td>
                  <td className="px-3 text-[#c3c6d2]">
                    ↓ {iface.rx} | ↑ {iface.tx}
                  </td>
                  <td className="px-3">
                    <span
                      className={`px-1.5 py-0.5 rounded text-[10px] font-bold border ${
                        isUp
                          ? 'bg-[#8cd6cb]/20 text-[#8cd6cb] border-[#8cd6cb]/40'
                          : 'bg-[#ffb4ab]/20 text-[#ffb4ab] border-[#ffb4ab]/40'
                      }`}
                    >
                      {iface.status}
                    </span>
                  </td>
                  <td className="px-3 text-right">
                    <button
                      type="button"
                      disabled={toggling === iface.name}
                      onClick={() => toggleInterface(iface.name)}
                      className={`px-2 py-0.5 rounded font-mono text-[10px] transition-colors cursor-pointer border ${
                        isUp
                          ? 'bg-[#26293c] hover:bg-[#35384c] text-[#ffb4ab] border-[#ffb4ab]/30'
                          : 'bg-[#b0c9ff] hover:bg-[#d8e2ff] text-[#002e68] font-bold border-transparent'
                      }`}
                    >
                      {toggling === iface.name ? 'Applying...' : isUp ? 'Set Down' : 'Bring Up'}
                    </button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      {/* Connect Network Modal */}
      <ConnectNetworkModal
        node={node}
        isOpen={showConnectModal}
        isMinimized={isModalMinimized}
        onMinimize={() => setIsModalMinimized(prev => !prev)}
        onClose={() => {
          setShowConnectModal(false);
          setIsModalMinimized(false);
        }}
        onConnected={() => fetchInterfaces(true)}
        existingInterfaces={interfaces.map(i => i.name)}
      />

      {/* Minimized Modal Floating Bar */}
      {isModalMinimized && showConnectModal && (
        <div className="fixed bottom-4 right-4 z-50 bg-[#171a2d] border border-[#8cd6cb] rounded-lg px-3 py-2 flex items-center gap-3 shadow-2xl font-mono text-xs">
          <div className="flex items-center gap-1.5">
            <span className="material-symbols-outlined text-[#8cd6cb] text-[18px]">settings_ethernet</span>
            <span className="text-[#dfe1fa] font-bold">Connect Network</span>
          </div>
          <div className="flex items-center gap-1">
            <button
              type="button"
              onClick={() => setIsModalMinimized(false)}
              className="text-[#8cd6cb] hover:bg-[#8cd6cb]/20 px-2 py-0.5 rounded cursor-pointer transition-colors"
            >
              Restore
            </button>
            <button
              type="button"
              onClick={() => {
                setShowConnectModal(false);
                setIsModalMinimized(false);
              }}
              className="text-[#8d909b] hover:text-[#dfe1fa] p-1 cursor-pointer transition-colors"
            >
              <span className="material-symbols-outlined text-[14px]">close</span>
            </button>
          </div>
        </div>
      )}
    </section>
  );
};
