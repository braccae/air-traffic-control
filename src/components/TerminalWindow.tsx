import React, { useState, useRef, useEffect, useCallback } from 'react';
import { Ghostty, Terminal, FitAddon } from 'ghostty-web';
import { Terminal as TerminalIcon, TerminalSquare, Minus, Square, Copy, X } from 'lucide-react';
import type { NodeItem } from '../types/index.ts';
import { parseTerminalCommand } from '../utils/orchestrator.ts';
import {
  formatGhosttyBanner,
  formatGhosttyPrompt,
  formatCommandOutputForGhostty,
  applyTerminalInputKey,
  type TerminalInputState,
} from '../utils/ghosttyTerminal.ts';
import { useDraggable, type DragPosition } from '../hooks/useDraggable.ts';
import { useResizable, type WindowSize, type ResizeDirection } from '../hooks/useResizable.ts';
import {
  type TerminalSession,
  formatSshTarget,
} from '../utils/terminalSessionManager.ts';
import {
  loadQuickCommands,
  QUICK_COMMAND_EVENT,
  type QuickCommandItem,
} from '../utils/quickCommandsManager.ts';

let ghosttyLoadPromise: Promise<Ghostty> | null = null;
function loadGhosttyEngine(): Promise<Ghostty> {
  if (!ghosttyLoadPromise) {
    ghosttyLoadPromise = Ghostty.load('/ghostty-vt.wasm');
  }
  return ghosttyLoadPromise;
}

export interface TerminalWindowProps {
  session: TerminalSession;
  node: NodeItem;
  isActive: boolean;
  isOpen: boolean;
  onFocus: () => void;
  onClose: () => void;
  onMinimize: () => void;
  onToggleMaximize: () => void;
  onUpdatePosition: (pos: DragPosition) => void;
  onUpdateSize: (size: WindowSize) => void;
}

export const TerminalWindow: React.FC<TerminalWindowProps> = ({
  session,
  node,
  isActive,
  isOpen,
  onFocus,
  onClose,
  onMinimize,
  onToggleMaximize,
  onUpdatePosition,
  onUpdateSize,
}) => {
  const [running, setRunning] = useState(false);
  const [engineReady, setEngineReady] = useState(false);
  const [lastOutputText, setLastOutputText] = useState('');
  const [connStatus, setConnStatus] = useState<'connected' | 'reconnecting' | 'disconnected'>('connected');
  const [showQuickMenu, setShowQuickMenu] = useState(false);
  const [quickCommands, setQuickCommands] = useState<QuickCommandItem[]>(() => loadQuickCommands());

  useEffect(() => {
    const handleQuickCommandsUpdated = (event: Event) => {
      const customEvt = event as CustomEvent<QuickCommandItem[]>;
      if (customEvt.detail && Array.isArray(customEvt.detail)) {
        setQuickCommands(customEvt.detail);
      } else {
        setQuickCommands(loadQuickCommands());
      }
    };

    window.addEventListener(QUICK_COMMAND_EVENT, handleQuickCommandsUpdated);
    return () => {
      window.removeEventListener(QUICK_COMMAND_EVENT, handleQuickCommandsUpdated);
    };
  }, []);

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

  // Position and Size Hooks bound to this individual session
  const { position, setPosition, dragProps } = useDraggable({
    initialPosition: session.position,
    disabled: session.isMaximized,
    onDragEnd: (pos) => {
      onUpdatePosition(pos);
    },
  });

  const { size, setSize, startResize, resizeProps } = useResizable({
    initialSize: session.size,
    onResizeEnd: (newSize) => {
      onUpdateSize(newSize);
      fitAddonRef.current?.fit();
    },
    onPositionChange: (newPos) => {
      setPosition(newPos);
      onUpdatePosition(newPos);
    },
  });

  // Keep local draggable and resizable state in sync if session prop updates
  useEffect(() => {
    setPosition(session.position);
  }, [session.position.x, session.position.y]);

  useEffect(() => {
    setSize(session.size);
  }, [session.size.width, session.size.height]);

  // When un-minimized, resized, or maximized, refit terminal and focus if active
  useEffect(() => {
    if (isOpen && !session.isMinimized) {
      const timer = setTimeout(() => {
        fitAddonRef.current?.fit();
        if (isActive) {
          termRef.current?.focus();
        }
      }, 50);
      return () => clearTimeout(timer);
    }
  }, [isOpen, session.isMinimized, session.isMaximized, isActive]);

  const writePrompt = useCallback(
    (term?: Terminal | null) => {
      const target = term || termRef.current;
      if (!target || !node) return;
      target.write(formatGhosttyPrompt(node));
    },
    [node]
  );

  const executeRemoteCommand = useCallback(
    async (cmd: string, echoCommandLine = false) => {
      const term = termRef.current;
      if (!term || !node) return;

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
          body: JSON.stringify({ command: trimmed, host: node.id }),
        });
        if (resp.ok) {
          const data = await resp.json();
          output = data.output || '';
        } else {
          output = parseTerminalCommand(trimmed, node).output;
        }
      } catch {
        output = parseTerminalCommand(trimmed, node).output;
      }

      if (output) {
        term.write(formatCommandOutputForGhostty(output));
        setLastOutputText(output);
      }

      runningRef.current = false;
      setRunning(false);
      writePrompt(term);
    },
    [node, writePrompt]
  );

  const handleRunQuickCommand = useCallback(
    (cmd: string) => {
      if (wsRef.current && wsRef.current.readyState === WebSocket.OPEN) {
        wsRef.current.send(
          JSON.stringify({
            type: 'terminal_data',
            sessionId: session.id,
            data: `${cmd}\n`,
          })
        );
        termRef.current?.focus();
      } else {
        executeRemoteCommand(cmd, true);
      }
    },
    [session.id, executeRemoteCommand]
  );

  // Initialize libghostty WASM terminal and persistent WebSocket PTY connection
  useEffect(() => {
    if (!isOpen || !containerRef.current || !node) return;

    let disposed = false;
    let termInstance: Terminal | null = null;
    let fitAddon: FitAddon | null = null;

    async function mountGhostty() {
      try {
        await loadGhosttyEngine();
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

        termInstance.write(formatGhosttyBanner(node));
        writePrompt(termInstance);
        if (isActive) {
          termInstance.focus();
        }

        // Connect to real-time interactive WebSocket PTY stream
        try {
          const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
          const ws = new WebSocket(`${protocol}//${window.location.host}/ws`);
          wsRef.current = ws;

          ws.onopen = () => {
            setConnStatus('connected');
            ws.send(
              JSON.stringify({
                type: 'terminal_start',
                sessionId: session.id,
                nodeId: node.id,
                host: node.id,
              })
            );
          };

          ws.onmessage = (event) => {
            try {
              const msg = JSON.parse(event.data);
              if (msg.type === 'terminal_data' && msg.sessionId === session.id) {
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
          if (wsRef.current && wsRef.current.readyState === WebSocket.OPEN) {
            wsRef.current.send(
              JSON.stringify({
                type: 'terminal_data',
                sessionId: session.id,
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
          if (wsRef.current.readyState === WebSocket.OPEN) {
            wsRef.current.send(
              JSON.stringify({
                type: 'terminal_close',
                sessionId: session.id,
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
  }, [isOpen, session.id, node?.id]);

  const isMax = session.isMaximized;
  const sshTarget = formatSshTarget(node, session.hostIp);

  // Window frame styles
  const windowStyle: React.CSSProperties = isMax
    ? {
        position: 'fixed',
        top: 16,
        left: 16,
        right: 16,
        bottom: 56,
        width: 'auto',
        height: 'auto',
        zIndex: isActive ? 55 : 50,
      }
    : {
        position: 'fixed',
        left: `${position.x}px`,
        top: `${position.y}px`,
        width: `${size.width}px`,
        height: `${size.height}px`,
        zIndex: isActive ? 55 : 50,
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
        display: session.isMinimized ? 'none' : 'flex',
      }}
      onPointerDown={() => onFocus()}
      {...resizeProps}
      className={`bg-[#0d1117] rounded-lg shadow-2xl border ${
        isActive ? 'border-[#58a6ff]/80 shadow-[0_0_20px_rgba(88,166,255,0.15)]' : 'border-[#30363d]'
      } flex flex-col overflow-hidden backdrop-blur-md select-none`}
    >
      {/* 8-Direction Resize Handles (when not maximized) */}
      {!isMax &&
        resizeHandles.map((dir) => (
          <div
            key={dir}
            onPointerDown={(e) => {
              onFocus();
              startResize(dir, e, position);
            }}
            className={`absolute z-30 ${getHandleClass(dir)} hover:bg-[#58a6ff]/30`}
          />
        ))}

      {/* Dedicated Window Title Bar (Single Session - No Tabs) */}
      <div
        {...dragProps}
        onPointerDown={(e) => {
          onFocus();
          dragProps.onPointerDown?.(e);
        }}
        className="flex items-center justify-between bg-[#161b22] border-b border-[#30363d] px-3 py-1.5 cursor-grab active:cursor-grabbing select-none"
      >
        {/* Left: Terminal Icon + Host Name + SSH Target + Status Dot */}
        <div className="flex items-center gap-2 min-w-0">
          <TerminalIcon className={`w-3.5 h-3.5 shrink-0 ${isActive ? 'text-[#58a6ff]' : 'text-gray-400'}`} />
          <span className="font-mono font-bold text-xs text-white truncate max-w-[150px]">
            {session.nodeName || node.name}
          </span>
          <span className="font-mono text-[11px] text-gray-400 hidden sm:inline truncate max-w-[220px]">
            {sshTarget}
          </span>
          <span
            className={`w-2 h-2 rounded-full shrink-0 ${
              connStatus === 'connected'
                ? 'bg-[#3fb950]'
                : connStatus === 'reconnecting'
                ? 'bg-amber-400 animate-pulse'
                : 'bg-red-500'
            }`}
            title={`Status: ${connStatus}`}
          />
        </div>

        {/* Right: Quick Commands + Window Action Buttons */}
        <div className="flex items-center gap-2 shrink-0">
          {/* Quick Commands for Desktop */}
          <div className="hidden md:flex items-center gap-1 px-1.5 py-0.5 bg-[#0d1117] rounded border border-[#30363d]/60">
            <span className="text-[10px] text-gray-500 font-mono flex items-center gap-1">
              <TerminalSquare className="w-3 h-3 text-[#79c0ff]" />
            </span>
            {quickCommands.map((qc) => (
              <button
                key={qc.id}
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  handleRunQuickCommand(qc.cmd);
                }}
                className="px-1.5 py-0.5 text-[10px] font-mono text-gray-300 hover:text-white hover:bg-[#21262d] rounded transition-colors"
                title={qc.description ? `${qc.description} (${qc.cmd})` : `Run '${qc.cmd}'`}
              >
                {qc.label}
              </button>
            ))}
          </div>

          {/* Quick Commands Dropdown for Small Screens */}
          <div className="relative md:hidden">
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                setShowQuickMenu(!showQuickMenu);
              }}
              className="p-1 rounded text-gray-400 hover:text-white hover:bg-[#21262d] transition-colors"
              title="Quick Commands"
            >
              <TerminalSquare className="w-3.5 h-3.5 text-[#79c0ff]" />
            </button>
            {showQuickMenu && (
              <div className="absolute right-0 top-8 z-50 bg-[#161b22] border border-[#30363d] rounded-md shadow-xl py-1 w-48">
                <div className="px-2.5 py-1 text-[10px] font-mono text-gray-400 uppercase tracking-wider border-b border-[#30363d]/50">
                  Quick Commands
                </div>
                {quickCommands.map((qc) => (
                  <button
                    key={qc.id}
                    type="button"
                    onClick={(e) => {
                      e.stopPropagation();
                      handleRunQuickCommand(qc.cmd);
                      setShowQuickMenu(false);
                    }}
                    className="w-full text-left px-2.5 py-1.5 text-xs font-mono text-gray-200 hover:text-white hover:bg-[#1f6feb]/20 flex flex-col"
                    title={qc.cmd}
                  >
                    <span>{qc.label}</span>
                    {qc.description && (
                      <span className="text-[10px] text-gray-400 font-sans truncate">{qc.description}</span>
                    )}
                  </button>
                ))}
              </div>
            )}
          </div>

          {/* Window Control Buttons: Minimize [-], Maximize [□], Close [✕] */}
          <div className="flex items-center gap-1">
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                onMinimize();
              }}
              className="p-1 rounded text-gray-400 hover:text-white hover:bg-[#21262d] transition-colors"
              title="Minimize to Console"
            >
              <Minus className="w-3.5 h-3.5" />
            </button>
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                onToggleMaximize();
              }}
              className="p-1 rounded text-gray-400 hover:text-white hover:bg-[#21262d] transition-colors"
              title={isMax ? 'Restore' : 'Maximize'}
            >
              {isMax ? <Copy className="w-3.5 h-3.5" /> : <Square className="w-3.5 h-3.5" />}
            </button>
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                onClose();
              }}
              className="p-1 rounded text-gray-400 hover:text-red-400 hover:bg-red-500/20 transition-colors"
              title="Close Terminal Session"
            >
              <X className="w-3.5 h-3.5" />
            </button>
          </div>
        </div>
      </div>

      {/* Terminal Canvas Body */}
      <div
        ref={containerRef}
        onClick={() => {
          onFocus();
          termRef.current?.focus();
        }}
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
          <span className="truncate max-w-[200px]">
            {sshTarget}:{node?.port || 22}
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
