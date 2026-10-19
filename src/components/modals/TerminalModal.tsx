import React, { useState, useRef, useEffect, useCallback } from 'react';
import { Ghostty, Terminal, FitAddon } from 'ghostty-web';
import type { NodeItem } from '../../types/index.ts';
import { parseTerminalCommand } from '../../utils/orchestrator.ts';
import {
  formatGhosttyBanner,
  formatGhosttyPrompt,
  formatCommandOutputForGhostty,
  applyTerminalInputKey,
  type TerminalInputState,
} from '../../utils/ghosttyTerminal.ts';
import { useDraggable, type DragPosition } from '../../hooks/useDraggable.ts';
import { useResizable, type WindowSize, type ResizeDirection } from '../../hooks/useResizable.ts';
import { TerminalTabBar } from '../TerminalTabBar.tsx';
import type { TerminalSession } from '../../utils/terminalSessionManager.ts';

interface TerminalModalProps {
  sessions: TerminalSession[];
  activeSessionId: string | null;
  nodes: NodeItem[];
  isOpen: boolean;
  onClose: () => void;
  onSelectSession: (id: string) => void;
  onCloseSession: (id: string) => void;
  onNewSession: (nodeId?: string) => void;
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
  onClose,
  onSelectSession,
  onCloseSession,
  onNewSession,
  onMinimizeSession,
  onToggleMaximizeSession,
  onUpdateSessionPosition,
  onUpdateSessionSize,
}) => {
  const activeSession = sessions.find((s) => s.id === activeSessionId) || sessions[0];
  const activeNode =
    nodes.find((n) => n.id === activeSession?.nodeId || n.shortName === activeSession?.nodeName) ||
    nodes[0];

  const [running, setRunning] = useState(false);
  const [engineReady, setEngineReady] = useState(false);
  const [lastOutputText, setLastOutputText] = useState('');
  const [connStatus, setConnStatus] = useState<'connected' | 'reconnecting' | 'disconnected'>('connected');

  const containerRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const inputStateRef = useRef<TerminalInputState>({
    buffer: '',
    history: [],
    historyIndex: -1,
  });
  const runningRef = useRef(false);

  // Load saved position or use session default
  const initialPos = activeSession?.position || { x: 120, y: 80 };
  const initialWinSize = activeSession?.size || { width: 880, height: 560 };

  const { position, setPosition, dragProps } = useDraggable({
    initialPosition: initialPos,
    disabled: activeSession?.isMaximized,
    onDragEnd: (pos) => {
      if (activeSession) {
        onUpdateSessionPosition(activeSession.id, pos);
      }
    },
  });

  const { size, setSize, startResize, resizeProps } = useResizable({
    initialSize: initialWinSize,
    onResizeEnd: (newSize) => {
      if (activeSession) {
        onUpdateSessionSize(activeSession.id, newSize);
      }
      fitAddonRef.current?.fit();
    },
    onPositionChange: (newPos) => {
      setPosition(newPos);
      if (activeSession) {
        onUpdateSessionPosition(activeSession.id, newPos);
      }
    },
  });

  // Sync state if active session changes
  useEffect(() => {
    if (activeSession) {
      setPosition(activeSession.position);
      setSize(activeSession.size);
    }
  }, [activeSessionId]);

  // When un-minimized, re-fit and focus terminal
  useEffect(() => {
    if (isOpen && activeSession && !activeSession.isMinimized) {
      const timer = setTimeout(() => {
        fitAddonRef.current?.fit();
        termRef.current?.focus();
      }, 50);
      return () => clearTimeout(timer);
    }
  }, [isOpen, activeSession?.isMinimized]);

  const writePrompt = useCallback(
    (term?: Terminal | null) => {
      const target = term || termRef.current;
      if (!target || !activeNode) return;
      target.write(formatGhosttyPrompt(activeNode));
    },
    [activeNode]
  );

  const executeRemoteCommand = useCallback(
    async (cmd: string, echoCommandLine = false) => {
      const term = termRef.current;
      if (!term || !activeNode) return;

      const trimmed = cmd.trim();
      if (echoCommandLine) {
        if (inputStateRef.current.buffer.length > 0) {
          term.write('\b \b'.repeat(inputStateRef.current.buffer.length));
          inputStateRef.current.buffer = '';
        }
        term.write(`${trimmed}\r\n`);
      }

      if (!trimmed) {
        writePrompt(term);
        return;
      }

      if (trimmed.toLowerCase() === 'clear') {
        term.clear();
        setLastOutputText('');
        writePrompt(term);
        return;
      }

      runningRef.current = true;
      setRunning(true);

      let output = '';
      try {
        const resp = await fetch('/api/terminal/exec', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ command: trimmed, host: activeNode.id }),
        });
        if (resp.ok) {
          const data = await resp.json();
          output = data.output || '';
        } else {
          output = parseTerminalCommand(trimmed, activeNode).output;
        }
      } catch {
        output = parseTerminalCommand(trimmed, activeNode).output;
      }

      if (output) {
        term.write(formatCommandOutputForGhostty(output));
        setLastOutputText(output);
      }

      runningRef.current = false;
      setRunning(false);
      writePrompt(term);
    },
    [activeNode, writePrompt]
  );

  // Initialize libghostty WASM terminal and persistent WebSocket PTY connection
  useEffect(() => {
    if (!isOpen || !containerRef.current || !activeNode) return;

    let disposed = false;
    let termInstance: Terminal | null = null;
    let fitAddon: FitAddon | null = null;

    async function mountGhostty() {
      try {
        await Ghostty.load('/ghostty-vt.wasm');
        if (disposed || !containerRef.current) return;

        containerRef.current.innerHTML = '';

        termInstance = new Terminal({
          cursorBlink: true,
          fontFamily: '"Fira Code", monospace',
          fontSize: 13,
          theme: {
            background: '#0d1117',
            foreground: '#c9d1d9',
            cursor: '#58a6ff',
          },
        });

        fitAddon = new FitAddon();
        termInstance.loadAddon(fitAddon);
        termInstance.open(containerRef.current);
        fitAddon.fit();
        fitAddon.observeResize();

        termRef.current = termInstance;
        fitAddonRef.current = fitAddon;
        inputStateRef.current = { buffer: '', history: [], historyIndex: -1 };
        setEngineReady(true);

        termInstance.write(formatGhosttyBanner(activeNode));
        writePrompt(termInstance);
        termInstance.focus();

        // Connect to real-time interactive WebSocket PTY stream
        try {
          const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
          const ws = new WebSocket(`${protocol}//${window.location.host}/ws`);
          wsRef.current = ws;

          ws.onopen = () => {
            setConnStatus('connected');
            if (activeSession) {
              ws.send(
                JSON.stringify({
                  type: 'terminal_start',
                  sessionId: activeSession.id,
                  nodeId: activeNode.id,
                  host: activeNode.id,
                })
              );
            }
          };

          ws.onmessage = (event) => {
            try {
              const msg = JSON.parse(event.data);
              if (msg.type === 'terminal_data' && activeSession && msg.sessionId === activeSession.id) {
                termInstance?.write(msg.data);
                setLastOutputText((prev) => (prev + msg.data).slice(-4000));
              }
            } catch {}
          };

          ws.onerror = () => {
            setConnStatus('reconnecting');
          };

          ws.onclose = () => {
            setConnStatus('disconnected');
          };
        } catch {
          setConnStatus('connected');
        }

        termInstance.onData((data: string) => {
          if (!termInstance || runningRef.current) return;

          // If interactive WebSocket PTY is active, send stream directly
          if (wsRef.current && wsRef.current.readyState === WebSocket.OPEN && activeSession) {
            wsRef.current.send(
              JSON.stringify({
                type: 'terminal_data',
                sessionId: activeSession.id,
                data,
              })
            );
            return;
          }

          // Fallback to local buffer + /api/terminal/exec
          const result = applyTerminalInputKey(inputStateRef.current, data);
          inputStateRef.current = result.nextState;

          if (result.clearScreen) {
            termInstance.clear();
            writePrompt(termInstance);
            return;
          }

          if (result.echo) {
            termInstance.write(result.echo);
          }

          if (result.submitCommand !== undefined) {
            executeRemoteCommand(result.submitCommand, false);
          }
        });
      } catch (err) {
        console.error('Failed to initialize libghostty WASM terminal:', err);
      }
    }

    mountGhostty();

    return () => {
      disposed = true;
      setEngineReady(false);
      if (wsRef.current) {
        try {
          if (wsRef.current.readyState === WebSocket.OPEN && activeSession) {
            wsRef.current.send(
              JSON.stringify({
                type: 'terminal_close',
                sessionId: activeSession.id,
              })
            );
          }
          wsRef.current.close();
        } catch {}
        wsRef.current = null;
      }
      if (fitAddon) {
        try {
          fitAddon.dispose();
        } catch {}
      }
      if (termInstance) {
        try {
          termInstance.dispose();
        } catch {}
      }
      termRef.current = null;
      fitAddonRef.current = null;
    };
  }, [isOpen, activeSessionId, activeNode?.id]);

  if (!isOpen || !activeSession) return null;

  const isMax = activeSession.isMaximized;

  // Window frame styles
  const windowStyle: React.CSSProperties = isMax
    ? {
        position: 'fixed',
        top: 16,
        left: 16,
        right: 16,
        bottom: 16,
        width: 'auto',
        height: 'auto',
        zIndex: 50,
      }
    : {
        position: 'fixed',
        left: `${position.x}px`,
        top: `${position.y}px`,
        width: `${size.width}px`,
        height: `${size.height}px`,
        zIndex: 50,
      };

  const resizeHandles: ResizeDirection[] = ['n', 's', 'e', 'w', 'ne', 'nw', 'se', 'sw'];

  const getHandleClass = (dir: ResizeDirection) => {
    switch (dir) {
      case 'n':
        return 'top-0 left-2 right-2 h-1.5 cursor-n-resize';
      case 's':
        return 'bottom-0 left-2 right-2 h-1.5 cursor-s-resize';
      case 'e':
        return 'right-0 top-2 bottom-2 w-1.5 cursor-e-resize';
      case 'w':
        return 'left-0 top-2 bottom-2 w-1.5 cursor-w-resize';
      case 'ne':
        return 'top-0 right-0 w-3 h-3 cursor-ne-resize';
      case 'nw':
        return 'top-0 left-0 w-3 h-3 cursor-nw-resize';
      case 'se':
        return 'bottom-0 right-0 w-3 h-3 cursor-se-resize';
      case 'sw':
        return 'bottom-0 left-0 w-3 h-3 cursor-sw-resize';
    }
  };

  return (
    <div
      style={{
        ...windowStyle,
        display: activeSession.isMinimized ? 'none' : 'flex',
      }}
      {...resizeProps}
      className="bg-[#0d1117] rounded-lg shadow-2xl border border-[#30363d] flex flex-col overflow-hidden backdrop-blur-md select-none"
    >
      {/* 8-Direction Resize Handles (when not maximized) */}
      {!isMax &&
        resizeHandles.map((dir) => (
          <div
            key={dir}
            onPointerDown={(e) => startResize(dir, e, position)}
            className={`absolute z-30 ${getHandleClass(dir)} hover:bg-[#58a6ff]/30`}
          />
        ))}

      {/* Tab Bar and Header with Draggable Handle */}
      <TerminalTabBar
        sessions={sessions}
        activeSessionId={activeSession.id}
        nodes={nodes}
        onSelectTab={onSelectSession}
        onCloseTab={(id) => {
          onCloseSession(id);
          if (sessions.length <= 1) {
            onClose();
          }
        }}
        onNewTab={onNewSession}
        onMinimize={() => onMinimizeSession(activeSession.id)}
        onToggleMaximize={() => onToggleMaximizeSession(activeSession.id)}
        onCloseWindow={onClose}
        isMaximized={isMax}
        onRunQuickCommand={(cmd) => executeRemoteCommand(cmd, true)}
        dragProps={dragProps}
      />

      {/* Terminal Canvas Body */}
      <div
        ref={containerRef}
        onClick={() => termRef.current?.focus()}
        data-testid="ghostty-terminal-container"
        className="flex-1 bg-[#0d1117] p-2 overflow-hidden relative cursor-text select-text"
      />

      {/* Accessible screen-reader mirror */}
      {lastOutputText && (
        <pre className="sr-only" data-testid="ghostty-last-output">
          {lastOutputText}
        </pre>
      )}

      {/* Terminal Footer Status Bar */}
      <div className="h-6 bg-[#161b22] px-3 flex items-center justify-between font-mono text-[10px] text-gray-400 border-t border-[#30363d]">
        <div className="flex items-center gap-2">
          <span className="flex items-center gap-1">
            <span
              className={`w-1.5 h-1.5 rounded-full ${
                connStatus === 'connected' ? 'bg-[#3fb950]' : 'bg-red-500'
              }`}
            />
            <span>{connStatus}</span>
          </span>
          <span className="text-gray-600">|</span>
          <span>
            {activeNode?.sshUser || 'core'}@{activeNode?.tailscaleHost || activeNode?.name}:{activeNode?.port || 22}
          </span>
          <span className="text-gray-600">|</span>
          <span className="text-[#58a6ff]">libghostty-vt WASM</span>
        </div>

        <div className="flex items-center gap-2">
          <span className="text-gray-500">
            {isMax ? 'Maximized' : `${size.width}x${size.height}`}
          </span>
          <span className="text-[#7ee787]">
            {running ? 'Executing remote SSH...' : 'WireGuard encrypted'}
          </span>
        </div>
      </div>
    </div>
  );
};
