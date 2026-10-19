import type { DragPosition } from '../hooks/useDraggable.ts';
import type { WindowSize } from '../hooks/useResizable.ts';

export interface TerminalSession {
  id: string;
  title: string;
  nodeId: string;
  nodeName: string;
  hostIp: string;
  isMinimized: boolean;
  isMaximized: boolean;
  position: DragPosition;
  size: WindowSize;
  createdAt: number;
}

export function createTerminalSession(
  nodeId: string,
  nodeName: string,
  hostIp: string,
  title?: string,
  existingCount = 0
): TerminalSession {
  const id = `term-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`;
  const defaultTitle = title || `${nodeName} (${hostIp})`;
  // Stagger window positions slightly if multiple sessions
  const offsetX = 80 + (existingCount % 5) * 30;
  const offsetY = 60 + (existingCount % 5) * 30;

  return {
    id,
    title: defaultTitle,
    nodeId,
    nodeName,
    hostIp,
    isMinimized: false,
    isMaximized: false,
    position: { x: offsetX, y: offsetY },
    size: { width: 860, height: 560 },
    createdAt: Date.now(),
  };
}

export function closeTerminalSession(
  sessions: TerminalSession[],
  sessionIdToClose: string,
  activeSessionId: string | null
): { sessions: TerminalSession[]; nextActiveId: string | null } {
  const remaining = sessions.filter((s) => s.id !== sessionIdToClose);
  let nextActiveId = activeSessionId;

  if (activeSessionId === sessionIdToClose) {
    if (remaining.length > 0) {
      // Find the most recently created or visible remaining session
      const visible = remaining.filter((s) => !s.isMinimized);
      nextActiveId = (visible.length > 0 ? visible[visible.length - 1] : remaining[remaining.length - 1]).id;
    } else {
      nextActiveId = null;
    }
  }

  return { sessions: remaining, nextActiveId };
}

export function setSessionMinimized(
  sessions: TerminalSession[],
  sessionId: string,
  isMinimized: boolean
): TerminalSession[] {
  return sessions.map((s) => (s.id === sessionId ? { ...s, isMinimized } : s));
}

export function toggleSessionMaximized(
  sessions: TerminalSession[],
  sessionId: string
): TerminalSession[] {
  return sessions.map((s) => (s.id === sessionId ? { ...s, isMaximized: !s.isMaximized } : s));
}

export function updateSessionPosition(
  sessions: TerminalSession[],
  sessionId: string,
  position: DragPosition
): TerminalSession[] {
  return sessions.map((s) => (s.id === sessionId ? { ...s, position } : s));
}

export function updateSessionSize(
  sessions: TerminalSession[],
  sessionId: string,
  size: WindowSize
): TerminalSession[] {
  return sessions.map((s) => (s.id === sessionId ? { ...s, size } : s));
}
