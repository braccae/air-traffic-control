import React from 'react';
import type { NodeItem } from '../types/index.ts';

interface TailscaleMeshCardProps {
  node: NodeItem | null;
  peers?: NodeItem[];
  onReauth?: () => void;
  onEditNode?: () => void;
  onOpenExplorer?: () => void;
}

export const TailscaleMeshCard: React.FC<TailscaleMeshCardProps> = ({
  node,
  peers = [],
  onReauth,
  onEditNode,
  onOpenExplorer,
}) => {
  if (!node) return null;
  // Show up to 6 direct cluster peers with real latency and status
  const peerList = peers.filter((p) => p.id !== node.id);

  return (
    <section className="md:col-span-5 bg-[#171a2d] rounded p-3 flex flex-col justify-between border border-[#26293c]">
      <div>
        {/* Title and Connected badge */}
        <div className="flex items-center justify-between pb-1.5 bg-[#0a0d1f]/40 px-2 py-1 rounded border border-[#26293c]/40">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[#8cd6cb] text-[18px]">vpn_key</span>
            <span className="font-sans text-sm text-[#dfe1fa] font-semibold">
              Tailscale Mesh ({node.datacenter || node.tailscaleHost?.split('.').slice(1).join('.') || 'unknown'})
            </span>
          </div>
          <div className="flex items-center gap-2">
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
              />
              {node.sshConnected ? 'SSH CONNECTED' : 'TAILSCALE ONLINE'}
            </span>
          </div>
        </div>

        {/* IP and SSH Agent routing */}
        <div className="grid grid-cols-2 gap-2 mt-2 font-mono text-xs">
          <div className="bg-[#1b1e31] p-2 rounded border border-[#26293c]/50">
            <span className="text-[#8d909b] block text-[10px]">TAILSCALE IP &amp; DNS</span>
            <span className="text-[#dfe1fa] font-bold text-[#b0c9ff]">{node.tailscaleIp || node.ip}</span>
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
            <span>CLUSTER PEERS ({peerList.length})</span>
            <span className="text-[#8cd6cb] truncate max-w-[220px]">
              {node.authMethod || 'ssh-agent: dynamic socket'}
            </span>
          </div>
          <div className="grid grid-cols-3 sm:grid-cols-4 gap-1.5 font-mono text-xs text-center">
            {peerList.length > 0 ? (
              peerList.slice(0, 8).map((peer) => (
                <div
                  key={peer.id}
                  className="bg-[#171a2d] py-1 px-1.5 rounded border border-[#26293c]/50 truncate"
                  title={`${peer.name} (${peer.tailscaleIp || peer.ip}) - Status: ${peer.status}`}
                >
                  <div className="flex items-center justify-center gap-1">
                    <span
                      className={`w-1.5 h-1.5 rounded-full ${
                        peer.status === 'online' ? 'bg-[#3fb950]' : 'bg-gray-500'
                      }`}
                    />
                    <span className="text-[#8d909b] text-[10px] truncate">{peer.shortName}</span>
                  </div>
                  <span className="text-[#8cd6cb] font-bold text-[11px]">
                    {peer.rttMs || (peer.status === 'online' ? '<1ms' : 'offline')}
                  </span>
                </div>
              ))
            ) : (
              <div className="col-span-3 py-2 text-center text-xs text-gray-500 font-mono">
                No active peers discovered
              </div>
            )}
          </div>
        </div>
      </div>

      {/* Footer key expiry and edit connection link */}
      <div className="mt-2 flex items-center justify-between text-xs font-mono pt-1 text-[#8d909b] border-t border-[#26293c]/50">
        <span className="truncate max-w-[200px]">
          Auth: <strong className="text-[#dfe1fa]">{node.authMethod || 'ssh-ed25519'}</strong>
        </span>
        <div className="flex items-center gap-3">
          {onOpenExplorer && (
            <button
              type="button"
              onClick={onOpenExplorer}
              className="text-[#58a6ff] hover:underline cursor-pointer flex items-center gap-1 text-[11px]"
            >
              Mesh Explorer
            </button>
          )}
          {onEditNode && (
            <button
              type="button"
              onClick={onEditNode}
              className="text-[#b0c9ff] hover:underline cursor-pointer flex items-center gap-1"
            >
              <span className="material-symbols-outlined text-[14px]">tune</span>
              Edit Node
            </button>
          )}
        </div>
      </div>
    </section>
  );
};
