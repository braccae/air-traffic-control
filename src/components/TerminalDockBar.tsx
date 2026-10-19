import React from 'react';
import { Terminal, X, ChevronUp, ChevronDown, Plus } from 'lucide-react';
import type { TerminalSession } from '../utils/terminalSessionManager.ts';

interface TerminalDockBarProps {
  sessions: TerminalSession[];
  activeSessionId: string | null;
  onSelectSession: (id: string) => void;
  onRestoreSession: (id: string) => void;
  onMinimizeSession: (id: string) => void;
  onCloseSession: (id: string) => void;
  onNewSession: () => void;
}

export const TerminalDockBar: React.FC<TerminalDockBarProps> = ({
  sessions,
  activeSessionId,
  onSelectSession,
  onRestoreSession,
  onMinimizeSession,
  onCloseSession,
  onNewSession,
}) => {
  if (sessions.length === 0) return null;

  return (
    <div className="fixed bottom-3 right-6 z-40 flex items-center gap-2 bg-[#0d1117]/95 border border-[#30363d] backdrop-blur-md px-3 py-1.5 rounded-lg shadow-2xl">
      <div className="flex items-center gap-1.5 text-xs text-gray-400 font-mono pr-2 border-r border-[#30363d]">
        <Terminal className="w-3.5 h-3.5 text-[#58a6ff]" />
        <span className="font-semibold text-gray-300">Terminals ({sessions.length})</span>
      </div>

      <div className="flex items-center gap-1.5 overflow-x-auto max-w-[60vw]">
        {sessions.map((session) => {
          const isActive = session.id === activeSessionId && !session.isMinimized;
          const isMin = session.isMinimized;

          return (
            <div
              key={session.id}
              onClick={() => {
                if (isMin) {
                  onRestoreSession(session.id);
                } else if (session.id === activeSessionId) {
                  onMinimizeSession(session.id);
                } else {
                  onSelectSession(session.id);
                }
              }}
              className={`group flex items-center gap-2 px-2.5 py-1 rounded text-xs font-mono transition-all cursor-pointer border ${
                isActive
                  ? 'bg-[#1f6feb]/20 border-[#58a6ff] text-white shadow-sm'
                  : isMin
                  ? 'bg-[#161b22] border-[#30363d] text-gray-400 hover:text-gray-200 hover:border-gray-500'
                  : 'bg-[#161b22]/70 border-[#30363d]/60 text-gray-300 hover:border-gray-500'
              }`}
              title={`${session.nodeName} (${session.hostIp}) - ${isMin ? 'Click to restore' : 'Click to minimize/focus'}`}
            >
              <span
                className={`w-2 h-2 rounded-full ${
                  isActive ? 'bg-[#3fb950] animate-pulse' : isMin ? 'bg-amber-400' : 'bg-gray-500'
                }`}
              />
              <span className="font-medium max-w-[120px] truncate">{session.nodeName}</span>
              <span className="text-[10px] text-gray-500">{session.hostIp}</span>

              {isMin ? (
                <ChevronUp className="w-3 h-3 text-gray-400 group-hover:text-white" />
              ) : (
                <ChevronDown className="w-3 h-3 text-gray-400 group-hover:text-white" />
              )}

              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  onCloseSession(session.id);
                }}
                className="p-0.5 rounded hover:bg-red-500/20 text-gray-400 hover:text-red-400 transition-colors ml-0.5"
                title="Close session"
              >
                <X className="w-3 h-3" />
              </button>
            </div>
          );
        })}
      </div>

      <button
        type="button"
        onClick={onNewSession}
        className="flex items-center gap-1 px-2 py-1 bg-[#238636]/20 border border-[#238636]/50 hover:bg-[#238636]/40 text-[#3fb950] rounded text-xs font-mono transition-colors"
        title="Open new terminal tab"
      >
        <Plus className="w-3.5 h-3.5" />
        <span className="text-[11px]">New</span>
      </button>
    </div>
  );
};
