import React, { useState, useEffect, useRef } from 'react';
import type { NodeItem } from '../../types/index.ts';

interface SearchModalProps {
  isOpen: boolean;
  onClose: () => void;
  nodes: NodeItem[];
  onSelectNode: (node: NodeItem) => void;
  onOpenPlaybook: () => void;
  onOpenTerminal: () => void;
}

export const SearchModal: React.FC<SearchModalProps> = ({
  isOpen,
  onClose,
  nodes,
  onSelectNode,
  onOpenPlaybook,
  onOpenTerminal,
}) => {
  const [query, setQuery] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (isOpen) {
      setTimeout(() => inputRef.current?.focus(), 50);
    }
  }, [isOpen]);

  if (!isOpen) return null;

  const filteredNodes = nodes.filter(
    n =>
      n.name.toLowerCase().includes(query.toLowerCase()) ||
      n.ip.includes(query) ||
      n.tailscaleIp.includes(query) ||
      n.group.toLowerCase().includes(query.toLowerCase())
  );

  const actions = [
    { title: 'Run Playbook (site.yml)', action: onOpenPlaybook, icon: 'play_arrow' },
    { title: 'Open xterm.js Web Shell', action: onOpenTerminal, icon: 'terminal' },
    { title: 'Inspect Restic Snapshots', action: onOpenTerminal, icon: 'camera' },
  ].filter(a => a.title.toLowerCase().includes(query.toLowerCase()));

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center pt-20 bg-black/70 backdrop-blur-xs p-4">
      <div className="w-full max-w-lg bg-[#0a0d1f] rounded-lg shadow-2xl border border-[#313447] overflow-hidden">
        {/* Search Input bar */}
        <div className="p-3 bg-[#171a2d] flex items-center gap-2 border-b border-[#26293c]">
          <span className="material-symbols-outlined text-[#8d909b] text-[18px]">search</span>
          <input
            ref={inputRef}
            type="text"
            value={query}
            onChange={e => setQuery(e.target.value)}
            placeholder="Search hosts, playbooks, vars, containers..."
            className="flex-1 bg-transparent border-none outline-none font-mono text-xs text-[#dfe1fa] focus:ring-0"
          />
          <kbd className="font-mono text-[10px] bg-[#26293c] text-[#8d909b] px-1.5 py-0.5 rounded border border-[#434750]/50">
            ESC
          </kbd>
        </div>

        {/* Results */}
        <div className="max-h-72 overflow-y-auto p-2 font-mono text-xs space-y-1">
          <div className="text-[10px] text-[#8d909b] uppercase px-2 py-1 font-bold">Hosts &amp; Nodes</div>
          {filteredNodes.length > 0 ? (
            filteredNodes.map(node => (
              <button
                key={node.id}
                type="button"
                onClick={() => {
                  onSelectNode(node);
                  onClose();
                }}
                className="w-full text-left p-2 rounded hover:bg-[#1b1e31] flex items-center justify-between transition-colors cursor-pointer group"
              >
                <div className="flex items-center gap-2">
                  <span className="w-2 h-2 rounded-full bg-[#8cd6cb]"></span>
                  <span className="font-bold text-[#dfe1fa] group-hover:text-[#b0c9ff]">{node.name}</span>
                  <span className="text-[#8d909b] text-[10px]">{node.osDescription}</span>
                </div>
                <span className="text-[#8cd6cb] text-[10px]">{node.tailscaleIp}</span>
              </button>
            ))
          ) : (
            <div className="text-[#8d909b] px-2 py-1 text-[11px]">No matching nodes found.</div>
          )}

          <div className="text-[10px] text-[#8d909b] uppercase px-2 py-1 font-bold mt-2">
            Quick Orchestration Actions
          </div>
          {actions.map((act, i) => (
            <button
              key={i}
              type="button"
              onClick={() => {
                act.action();
                onClose();
              }}
              className="w-full text-left p-2 rounded hover:bg-[#1b1e31] flex items-center gap-2 text-[#c3c6d2] hover:text-[#dfe1fa] cursor-pointer"
            >
              <span className="material-symbols-outlined text-[16px] text-[#b0c9ff]">{act.icon}</span>
              <span>{act.title}</span>
            </button>
          ))}
        </div>
      </div>
    </div>
  );
};
