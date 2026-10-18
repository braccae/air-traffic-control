import React from 'react';
import type { NodeItem } from '../types/index.ts';

interface TailscaleMeshCardProps {
  node: NodeItem;
  peers?: NodeItem[];
  onReauth?: () => void;
  onEditNode?: () => void;
}

export const TailscaleMeshCard: React.FC<TailscaleMeshCardProps> = ({
  node,
  peers = [],
  onReauth,
  onEditNode,
}) => {
  const peerList = peers.filter(p => p.id !== node.id).slice(0, 3);

  return (
    <section className="md:col-span-5 bg-[#171a2d] rounded p-3 flex flex-col justify-between border border-[#26293c]">
      <div>
        {/* Title and Connected badge */}
        <div className="flex items-center justify-between pb-1.5 bg-[#0a0d1f]/40 px-2 py-1 rounded border border-[#26293c]/40">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[#8cd6cb] text-[18px]">vpn_key</span>
            <span className="font-sans text-sm text-[#dfe1fa] font-semibold">
              Tailscale Mesh ({node.datacenter || 'leo-banjo.ts.net'})
            </span>
          </div>
          <span
            className={`px-2 py-0.5 font-mono text-[10px] font-bold rounded flex items-center gap-1 border ${
              node.sshConnected
                ? 'bg-[#8cd6cb]/20 text-[#8cd6cb] border-[#8cd6cb]/30'
                : 'bg-[#b0c9ff]/20 text-[#b0c9ff] border-[#b0c9ff]/30'
            }`}
          >
            <span
              className={`w-1.5 h-1.5 rounded-full ${
                node.sshConnected ? 'bg-[#8cd6cb]' : 'bg-[#b0c9ff]'
              }`}
            ></span>
            {node.sshConnected ? 'SSH CONNECTED' : 'TAILSCALE ONLINE'}
          </span>
        </div>

        {/* IP and SSH Agent routing */}
        <div className="grid grid-cols-2 gap-2 mt-2 font-mono text-xs">
          <div className="bg-[#1b1e31] p-2 rounded border border-[#26293c]/50">
            <span className="text-[#8d909b] block text-[10px]">TAILSCALE IP &amp; DNS</span>
            <span className="text-[#dfe1fa] font-bold text-[#b0c9ff]">{node.tailscaleIp}</span>
            <span className="text-[#8d909b] text-[10px] block truncate">{node.tailscaleHost}</span>
          </div>
          <div className="bg-[#1b1e31] p-2 rounded border border-[#26293c]/50">
            <span className="text-[#8d909b] block text-[10px]">SSH SESSION &amp; LAN IP</span>
            <span className="text-[#8cd6cb] font-bold">
              {node.sshUser || 'core'}@{node.shortName}:{node.port || 22}
            </span>
            <span className="text-[#d9b9ff] text-[10px] block truncate">
              LAN: {node.ip} ({node.group})
            </span>
          </div>
        </div>

        {/* Mesh Peer Latencies */}
        <div className="mt-2 bg-[#313447]/30 p-2 rounded border border-[#26293c]/40">
          <div className="flex items-center justify-between font-mono text-[10px] text-[#8d909b] pb-1">
            <span>TAG:HOST MESH PEERS</span>
            <span className="text-[#8cd6cb]">ssh-agent: /run/user/1000/ssh-agent.socket</span>
          </div>
          <div className="grid grid-cols-3 gap-1 font-mono text-xs text-center">
            {peerList.length > 0 ? (
              peerList.map(peer => (
                <div
                  key={peer.id}
                  className="bg-[#171a2d] py-1 px-1 rounded border border-[#26293c]/50 truncate"
                >
                  <span className="text-[#8d909b] block text-[10px] truncate">
                    {peer.shortName}
                  </span>
                  <span className="text-[#8cd6cb] font-bold">{peer.rttMs || '2.0ms'}</span>
                </div>
              ))
            ) : (
              <>
                <div className="bg-[#171a2d] py-1 rounded border border-[#26293c]/50">
                  <span className="text-[#8d909b] block text-[10px]">likes-it-cold</span>
                  <span className="text-[#8cd6cb] font-bold">1.8ms</span>
                </div>
                <div className="bg-[#171a2d] py-1 rounded border border-[#26293c]/50">
                  <span className="text-[#8d909b] block text-[10px]">ping</span>
                  <span className="text-[#8cd6cb] font-bold">2.1ms</span>
                </div>
                <div className="bg-[#171a2d] py-1 rounded border border-[#26293c]/50">
                  <span className="text-[#8d909b] block text-[10px]">fred</span>
                  <span className="text-[#d9b9ff] font-bold">3.4ms</span>
                </div>
              </>
            )}
          </div>
        </div>
      </div>

      {/* Footer key expiry and edit connection link */}
      <div className="mt-2 flex items-center justify-between text-xs font-mono pt-1 text-[#8d909b] border-t border-[#26293c]/50">
        <span>
          Auth: <strong className="text-[#dfe1fa]">{node.authMethod || 'ssh-ed25519'}</strong>
        </span>
        <div className="flex items-center gap-3">
          {onEditNode && (
            <button
              type="button"
              onClick={onEditNode}
              className="text-[#b0c9ff] cursor-pointer hover:underline text-xs"
            >
              Configure SSH
            </button>
          )}
          <button
            type="button"
            onClick={onReauth}
            className="text-[#8cd6cb] cursor-pointer hover:underline text-xs"
          >
            Open SSH Console
          </button>
        </div>
      </div>
    </section>
  );
};
