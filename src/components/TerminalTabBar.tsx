/**
 * @deprecated TerminalTabBar has been deprecated.
 * Terminal windows now render as independent floating windows with dedicated single-session title bars.
 * Minimized terminal sessions are docked and managed through the global Console dock bar at the bottom.
 */
import React from 'react';
import type { TerminalSession } from '../utils/terminalSessionManager.ts';
import type { NodeItem } from '../types/index.ts';

export interface TerminalTabBarProps {
  sessions?: TerminalSession[];
  activeSessionId?: string | null;
  nodes?: NodeItem[];
  onSelectTab?: (id: string) => void;
  onCloseTab?: (id: string) => void;
  onNewTab?: (nodeId?: string) => void;
  onMinimize?: () => void;
  onToggleMaximize?: () => void;
  onCloseWindow?: () => void;
  isMaximized?: boolean;
  onRunQuickCommand?: (cmd: string) => void;
  dragProps?: Record<string, unknown>;
}

export const TerminalTabBar: React.FC<TerminalTabBarProps> = () => null;
