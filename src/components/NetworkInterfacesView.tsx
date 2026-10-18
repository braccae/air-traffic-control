import React, { useState } from 'react';
import type { NodeItem } from '../types/index.ts';

interface NetworkInterfacesViewProps {
  node: NodeItem;
  onRefresh?: () => void;
}

export const NetworkInterfacesView: React.FC<NetworkInterfacesViewProps> = ({ node }) => {
  const [interfaces, setInterfaces] = useState([
    {
      name: 'tailscale0',
      type: 'WireGuard Tunnel',
      ip: `${node.tailscaleIp}/32`,
      mac: 'N/A (point-to-point)',
      speed: '10 Gbps (Mesh)',
      rx: '1.42 GiB',
      tx: '890 MiB',
      status: 'UP / CARRIER',
      mtu: 1280,
    },
    {
      name: 'bond0 (eth0 + eth1)',
      type: 'LACP 802.3ad Bond',
      ip: `${node.ip}/24`,
      mac: 'bc:24:11:89:a4:20',
      speed: '2x 10 Gbps (Full Duplex)',
      rx: '14.8 GiB',
      tx: '11.2 GiB',
      status: 'UP / CARRIER',
      mtu: 9000,
    },
    {
      name: 'podman0',
      type: 'Container Bridge',
      ip: '10.88.0.1/16',
      mac: '52:54:00:12:34:56',
      speed: 'Virtual',
      rx: '6.1 GiB',
      tx: '5.9 GiB',
      status: 'UP / ACTIVE',
      mtu: 1500,
    },
    {
      name: 'virbr0',
      type: 'KVM/QEMU Bridge',
      ip: '192.168.122.1/24',
      mac: '52:54:00:8c:1d:99',
      speed: 'Virtual',
      rx: '820 MiB',
      tx: '650 MiB',
      status: 'UP / ACTIVE',
      mtu: 1500,
    },
  ]);

  const [toggling, setToggling] = useState<string | null>(null);

  const toggleInterface = (name: string) => {
    setToggling(name);
    setTimeout(() => {
      setInterfaces(prev =>
        prev.map(i =>
          i.name === name
            ? {
                ...i,
                status: i.status.includes('UP') ? 'DOWN' : 'UP / CARRIER',
              }
            : i
        )
      );
      setToggling(null);
    }, 400);
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
          <span className="px-2 py-0.5 bg-[#8cd6cb]/15 text-[#8cd6cb] rounded border border-[#8cd6cb]/30">
            LACP active-backup
          </span>
          <span className="px-2 py-0.5 bg-[#b0c9ff]/15 text-[#b0c9ff] rounded border border-[#b0c9ff]/30">
            Jumbo Frames (MTU 9000)
          </span>
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
    </section>
  );
};
