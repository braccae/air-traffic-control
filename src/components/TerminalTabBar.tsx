import React from 'react';
import { Terminal, Plus, X, Minus, Square, Copy, TerminalSquare } from 'lucide-react';
import type { TerminalSession } from '../utils/terminalSessionManager.ts';
import type { NodeItem } from '../types/index.ts';

interface TerminalTabBarProps {
  sessions: TerminalSession[];
  activeSessionId: string | null;
  nodes: NodeItem[];
  onSelectTab: (id: string) => void;
  onCloseTab: (id: string) => void;
  onNewTab: (nodeId?: string) => void;
  onMinimize: () => void;
  onToggleMaximize: () => void;
  onCloseWindow: () => void;
  isMaximized: boolean;
  onRunQuickCommand?: (cmd: string) => void;
  dragProps?: Record<string, unknown>;
}

export const TerminalTabBar: React.FC<TerminalTabBarProps> = ({
  sessions,
  activeSessionId,
  nodes,
  onSelectTab,
  onCloseTab,
  onNewTab,
  onMinimize,
  onToggleMaximize,
  onCloseWindow,
  isMaximized,
  onRunQuickCommand,
  dragProps,
}) => {
  const [showNodeSelector, setShowNodeSelector] = React.useState(false);

  const quickCommands = [
    { label: 'htop', cmd: 'htop' },
    { label: 'podman ps', cmd: 'podman ps' },
    { label: 'journalctl', cmd: 'journalctl -f -n 50' },
    { label: 'ip a', cmd: 'ip -br a' },
    { label: 'uptime', cmd: 'uptime' },
  ];

  return (
    <div
      {...dragProps}
      className="flex items-center justify-between bg-[#161b22] border-b border-[#30363d] px-2 py-1.5 cursor-grab active:cursor-grabbing select-none"
    >
      {/* Left: Tab list */}
      <div className="flex items-center gap-1 overflow-x-auto max-w-[65%]">
        {sessions.map((session) => {
          const isActive = session.id === activeSessionId;
          return (
            <div
              key={session.id}
              onClick={() => onSelectTab(session.id)}
              className={`flex items-center gap-1.5 px-3 py-1 rounded text-xs font-mono transition-colors cursor-pointer border ${
                isActive
                  ? 'bg-[#0d1117] border-[#30363d] text-white shadow-sm'
                  : 'bg-[#161b22] border-transparent text-gray-400 hover:text-gray-200 hover:bg-[#21262d]'
              }`}
            >
              <Terminal className={`w-3.5 h-3.5 ${isActive ? 'text-[#58a6ff]' : 'text-gray-500'}`} />
              <span className="font-semibold truncate max-w-[140px]">{session.nodeName}</span>
              <span className="text-[10px] text-gray-500 hidden sm:inline">{session.hostIp}</span>
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  onCloseTab(session.id);
                }}
                className="p-0.5 rounded hover:bg-red-500/20 text-gray-400 hover:text-red-400 ml-1 transition-colors"
                title="Close Tab"
              >
                <X className="w-3 h-3" />
              </button>
            </div>
          );
        })}

        {/* New Tab Button */}
        <div className="relative">
          <button
            type="button"
            onClick={() => setShowNodeSelector(!showNodeSelector)}
            className="flex items-center justify-center p-1.5 rounded hover:bg-[#21262d] text-gray-400 hover:text-white transition-colors border border-transparent hover:border-[#30363d]"
            title="Open New Tab (Select Host)"
          >
            <Plus className="w-3.5 h-3.5" />
          </button>

          {showNodeSelector && (
            <div className="absolute top-8 left-0 z-50 bg-[#161b22] border border-[#30363d] rounded-md shadow-xl py-1 w-52 max-h-60 overflow-y-auto">
              <div className="px-2.5 py-1 text-[11px] font-mono text-gray-400 uppercase tracking-wider border-b border-[#30363d]/50">
                Open session on host:
              </div>
              {nodes.map((n) => (
                <button
                  key={n.id}
                  type="button"
                  onClick={() => {
                    onNewTab(n.id);
                    setShowNodeSelector(false);
                  }}
                  className="w-full text-left px-2.5 py-1.5 hover:bg-[#1f6feb]/20 text-xs font-mono text-gray-200 hover:text-white flex items-center justify-between"
                >
                  <span className="truncate">{n.name}</span>
                  <span className="text-[10px] text-gray-500">{n.tailscaleIp || n.ip}</span>
                </button>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* Middle: Quick Commands */}
      {onRunQuickCommand && (
        <div className="hidden lg:flex items-center gap-1 px-2 py-0.5 bg-[#0d1117] rounded border border-[#30363d]/60">
          <span className="text-[10px] text-gray-500 font-mono flex items-center gap-1">
            <TerminalSquare className="w-3 h-3 text-[#79c0ff]" /> Quick:
          </span>
          {quickCommands.map((qc) => (
            <button
              key={qc.label}
              type="button"
              onClick={() => onRunQuickCommand(qc.cmd)}
              className="px-1.5 py-0.5 text-[10px] font-mono text-gray-300 hover:text-white hover:bg-[#21262d] rounded transition-colors"
            >
              {qc.label}
            </button>
          ))}
        </div>
      )}

      {/* Right: Window Controls */}
      <div className="flex items-center gap-1 ml-2">
        <button
          type="button"
          onClick={onMinimize}
          className="p-1 rounded text-gray-400 hover:text-white hover:bg-[#21262d] transition-colors"
          title="Minimize to Dock"
        >
          <Minus className="w-3.5 h-3.5" />
        </button>
        <button
          type="button"
          onClick={onToggleMaximize}
          className="p-1 rounded text-gray-400 hover:text-white hover:bg-[#21262d] transition-colors"
          title={isMaximized ? 'Restore' : 'Maximize'}
        >
          <Square className="w-3.5 h-3.5" />
        </button>
        <button
          type="button"
          onClick={onCloseWindow}
          className="p-1 rounded text-gray-400 hover:text-red-400 hover:bg-red-500/20 transition-colors"
          title="Close Terminal"
        >
          <X className="w-3.5 h-3.5" />
        </button>
      </div>
    </div>
  );
};
