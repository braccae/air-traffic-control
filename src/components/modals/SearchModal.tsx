import React, { useState, useEffect, useRef } from 'react';
import type { NodeItem } from '../../types/index.ts';
import { filterNodesBySearchQuery } from '../../utils/navigationViewModel.ts';

interface SearchModalProps {
  isOpen: boolean;
  onClose: () => void;
  nodes: NodeItem[];
  onSelectNode: (node: NodeItem) => void;
  onOpenPlaybook: () => void;
  onOpenPlaybookEditor?: () => void;
  onOpenTerminal: () => void;
}

export const SearchModal: React.FC<SearchModalProps> = ({
  isOpen,
  onClose,
  nodes,
  onSelectNode,
  onOpenPlaybook,
  onOpenPlaybookEditor,
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

  const filteredNodes = filterNodesBySearchQuery(nodes, query);

  const playbooks = [
    { title: 'site.yml', subtext: 'Full cluster orchestration and service deployment', action: onOpenPlaybook },
    { title: 'security-hardening.yml', subtext: 'NFTables firewall, SSH strict MFA, rootless podman policies', action: onOpenPlaybook },
    { title: 'tailscale-status.yml', subtext: 'Verify mesh routing & exit advertisement across peers', action: onOpenPlaybook },
    { title: 'podman-update.yml', subtext: 'Rolling rootless container pull and systemd daemon reload', action: onOpenPlaybook },
  ].filter(p => !query.trim() || p.title.toLowerCase().includes(query.toLowerCase()) || p.subtext.toLowerCase().includes(query.toLowerCase()));

  const actions = [
    { title: 'Open Floating Ghostty Terminal', action: onOpenTerminal, icon: 'terminal', desc: 'PTY WebSocket session' },
    { title: 'Run Playbook Runner', action: onOpenPlaybook, icon: 'play_arrow', desc: 'Ansible execution with dry-run' },
    ...(onOpenPlaybookEditor
      ? [{ title: 'Manage & Edit Ansible Playbooks', action: onOpenPlaybookEditor, icon: 'menu_book', desc: 'Add, edit, or delete cluster playbooks' }]
      : []),
    { title: 'Inspect Tailscale Peer Mesh', action: onOpenTerminal, icon: 'hub', desc: '48 nodes telemetry' },
  ].filter(a => !query.trim() || a.title.toLowerCase().includes(query.toLowerCase()) || a.desc.toLowerCase().includes(query.toLowerCase()));

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
            placeholder="Search hosts, playbooks, vars, containers, IP..."
            className="flex-1 bg-transparent border-none outline-none font-mono text-xs text-[#dfe1fa] focus:ring-0"
          />
          <kbd className="font-mono text-[10px] bg-[#26293c] text-[#8d909b] px-1.5 py-0.5 rounded border border-[#434750]/50">
            ESC
          </kbd>
        </div>

        {/* Results */}
        <div className="max-h-80 overflow-y-auto p-2 font-mono text-xs space-y-2">
          {/* Nodes */}
          <div>
            <div className="text-[10px] text-[#8d909b] uppercase px-2 py-1 font-bold">
              Hosts &amp; Nodes ({filteredNodes.length})
            </div>
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
                    <span className="w-2 h-2 rounded-full bg-[#8cd6cb] animate-pulse"></span>
                    <span className="font-bold text-[#dfe1fa] group-hover:text-[#b0c9ff]">{node.name}</span>
                    <span className="text-[#8d909b] text-[10px]">{node.osDescription}</span>
                  </div>
                  <span className="text-[#8cd6cb] text-[10px]">{node.tailscaleIp}</span>
                </button>
              ))
            ) : (
              <div className="text-[#8d909b] px-2 py-1 text-[11px]">No matching nodes found.</div>
            )}
          </div>

          {/* Playbooks */}
          {playbooks.length > 0 && (
            <div>
              <div className="text-[10px] text-[#8d909b] uppercase px-2 py-1 font-bold">
                Ansible Playbooks ({playbooks.length})
              </div>
              {playbooks.map(p => (
                <button
                  key={p.title}
                  type="button"
                  onClick={() => {
                    p.action();
                    onClose();
                  }}
                  className="w-full text-left p-2 rounded hover:bg-[#1b1e31] flex items-center justify-between transition-colors cursor-pointer group"
                >
                  <div className="flex items-center gap-2">
                    <span className="material-symbols-outlined text-[16px] text-[#b0c9ff]">play_arrow</span>
                    <div>
                      <span className="font-bold text-[#dfe1fa] group-hover:text-[#b0c9ff]">{p.title}</span>
                      <p className="text-[10px] text-[#8d909b]">{p.subtext}</p>
                    </div>
                  </div>
                </button>
              ))}
            </div>
          )}

          {/* Quick Actions */}
          {actions.length > 0 && (
            <div>
              <div className="text-[10px] text-[#8d909b] uppercase px-2 py-1 font-bold">
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
                  className="w-full text-left p-2 rounded hover:bg-[#1b1e31] flex items-center justify-between text-[#c3c6d2] hover:text-[#dfe1fa] cursor-pointer"
                >
                  <div className="flex items-center gap-2">
                    <span className="material-symbols-outlined text-[16px] text-[#8cd6cb]">{act.icon}</span>
                    <span>{act.title}</span>
                  </div>
                  <span className="text-[10px] text-[#8d909b]">{act.desc}</span>
                </button>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
};
