import React from 'react';
import type { NodeItem } from '../../types/index.ts';
import type { DragPosition } from '../../hooks/useDraggable.ts';
import type { WindowSize } from '../../hooks/useResizable.ts';
import type { TerminalSession } from '../../utils/terminalSessionManager.ts';
import type { ThemeChoice, ResolvedTheme } from '../../utils/themeManager.ts';
import { TerminalWindow } from '../TerminalWindow.tsx';

export interface TerminalModalProps {
  sessions: TerminalSession[];
  activeSessionId: string | null;
  nodes: NodeItem[];
  isOpen: boolean;
  themeChoice?: ThemeChoice;
  resolvedTheme?: ResolvedTheme;
  onClose: () => void;
  onSelectSession: (id: string) => void;
  onCloseSession: (id: string) => void;
  onNewSession?: (nodeId?: string) => void;
  onMinimizeSession: (id: string) => void;
  onToggleMaximizeSession: (id: string) => void;
  onUpdateSessionPosition: (id: string, pos: DragPosition) => void;
  onUpdateSessionSize: (id: string, size: WindowSize) => void;
}

export const TerminalModal: React.FC<TerminalModalProps> = ({
  sessions,
  activeSessionId,
  nodes,
  isOpen,
  themeChoice,
  resolvedTheme,
  onClose,
  onSelectSession,
  onCloseSession,
  onMinimizeSession,
  onToggleMaximizeSession,
  onUpdateSessionPosition,
  onUpdateSessionSize,
}) => {
  if (!isOpen || sessions.length === 0) return null;

  return (
    <>
      {sessions.map((session) => {
        const node =
          nodes.find((n) => n.id === session.nodeId || n.shortName === session.nodeName) ||
          nodes[0];
        return (
          <TerminalWindow
            key={session.id}
            session={session}
            node={node}
            isActive={session.id === activeSessionId}
            isOpen={isOpen}
            themeChoice={themeChoice}
            resolvedTheme={resolvedTheme}
            onFocus={() => onSelectSession(session.id)}
            onClose={() => {
              onCloseSession(session.id);
              if (sessions.length <= 1) {
                onClose();
              }
            }}
            onMinimize={() => onMinimizeSession(session.id)}
            onToggleMaximize={() => onToggleMaximizeSession(session.id)}
            onUpdatePosition={(pos) => onUpdateSessionPosition(session.id, pos)}
            onUpdateSize={(size) => onUpdateSessionSize(session.id, size)}
          />
        );
      })}
    </>
  );
};

export { TerminalWindow };
