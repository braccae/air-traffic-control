import React from 'react';
import type { NodeItem } from '../types/index.ts';

interface TailscaleMeshCardProps {
  node: NodeItem;
  onReauth?: () => void;
}

export const TailscaleMeshCard: React.FC<TailscaleMeshCardProps> = ({
  node,
  onReauth,
}) => {
  return (
    <section className="md:col-span-5 bg-[#171a2d] rounded p-3 flex flex-col justify-between border border-[#26293c]">
      <div>
        {/* Title and Connected badge */}
        <div className="flex items-center justify-between pb-1.5 bg-[#0a0d1f]/40 px-2 py-1 rounded border border-[#26293c]/40">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[#8cd6cb] text-[18px]">vpn_key</span>
            <span className="font-sans text-sm text-[#dfe1fa] font-semibold">
              Tailscale Mesh Node
            </span>
          </div>
          <span className="px-2 py-0.5 bg-[#8cd6cb]/20 text-[#8cd6cb] font-mono text-[10px] font-bold rounded flex items-center gap-1 border border-[#8cd6cb]/30">
            <span className="w-1.5 h-1.5 rounded-full bg-[#8cd6cb]"></span>
            CONNECTED
          </span>
        </div>

        {/* IP and Exit node routing */}
        <div className="grid grid-cols-2 gap-2 mt-2 font-mono text-xs">
          <div className="bg-[#1b1e31] p-2 rounded border border-[#26293c]/50">
            <span className="text-[#8d909b] block text-[10px]">TAILSCALE IP</span>
            <span className="text-[#dfe1fa] font-bold text-[#b0c9ff]">{node.tailscaleIp}</span>
            <span className="text-[#8d909b] text-[10px] block truncate">{node.tailscaleHost}</span>
          </div>
          <div className="bg-[#1b1e31] p-2 rounded border border-[#26293c]/50">
            <span className="text-[#8d909b] block text-[10px]">ROUTING &amp; EXIT</span>
            <span className="text-[#8cd6cb] font-bold">Exit Node Enabled</span>
            <span className="text-[#d9b9ff] text-[10px] block">192.168.1.0/24 adv</span>
          </div>
        </div>

        {/* Mesh Peer Latencies */}
        <div className="mt-2 bg-[#313447]/30 p-2 rounded border border-[#26293c]/40">
          <div className="flex items-center justify-between font-mono text-[10px] text-[#8d909b] pb-1">
            <span>MESH PEER LATENCY</span>
            <span className="text-[#8cd6cb]">Tailscale SSH Active</span>
          </div>
          <div className="grid grid-cols-3 gap-1 font-mono text-xs text-center">
            <div className="bg-[#171a2d] py-1 rounded border border-[#26293c]/50">
              <span className="text-[#8d909b] block text-[10px]">cold-war</span>
              <span className="text-[#8cd6cb] font-bold">1.2ms</span>
            </div>
            <div className="bg-[#171a2d] py-1 rounded border border-[#26293c]/50">
              <span className="text-[#8d909b] block text-[10px]">noodle</span>
              <span className="text-[#8cd6cb] font-bold">0.8ms</span>
            </div>
            <div className="bg-[#171a2d] py-1 rounded border border-[#26293c]/50">
              <span className="text-[#8d909b] block text-[10px]">pong-edge</span>
              <span className="text-[#d9b9ff] font-bold">14.1ms</span>
            </div>
          </div>
        </div>
      </div>

      {/* Footer key expiry and reauth link */}
      <div className="mt-2 flex items-center justify-between text-xs font-mono pt-1 text-[#8d909b] border-t border-[#26293c]/50">
        <span>
          Key expiry in <strong className="text-[#dfe1fa]">84 days</strong>
        </span>
        <button
          type="button"
          onClick={onReauth}
          className="text-[#8cd6cb] cursor-pointer hover:underline text-xs"
        >
          Re-authenticate node
        </button>
      </div>
    </section>
  );
};
