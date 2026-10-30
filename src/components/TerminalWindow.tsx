import React, { useState, useRef, useEffect, useCallback } from 'react';
import { Ghostty, Terminal, FitAddon } from 'ghostty-web';
import { Terminal as TerminalIcon, TerminalSquare, Minus, Square, Copy, X, Wifi, WifiOff, RotateCcw } from 'lucide-react';
import type { NodeItem } from '../types/index.ts';
import {
  formatGhosttyBanner,
  formatGhosttyPrompt,
  formatCommandOutputForGhostty,
  applyTerminalInputKey,
  type TerminalInputState,
  getGhosttyTheme,
} from '../utils/ghosttyTerminal.ts';
import { useDraggable, type DragPosition } from '../hooks/useDraggable.ts';
import { useResizable, type WindowSize, type ResizeDirection } from '../hooks/useResizable.ts';
import {
  type TerminalSession,
  formatSshTarget,
} from '../utils/terminalSessionManager.ts';
import type { ThemeChoice, ResolvedTheme } from '../utils/themeManager.ts';
import { resolveTheme } from '../utils/themeManager.ts';
import {
  loadQuickCommands,
  QUICK_COMMAND_EVENT,
  type QuickCommandItem,
} from '../utils/quickCommandsManager.ts';

// WebSocket connection configuration
const WS_RECONNECT_BASE_DELAY = 1000; // 1 second
const WS_RECONNECT_MAX_DELAY = 30000; // 30 seconds
const WS_RECONNECT_MAX_ATTEMPTS = 10;
const WS_RECONNECT_JITTER = 0.3; // 30% jitter

function getWebSocketUrl(): string {
  // Allow override via Vite env var (e.g., VITE_WS_URL=ws://127.0.0.1:18090/ws)
  if (import.meta.env.VITE_WS_URL) {
    return import.meta.env.VITE_WS_URL;
  }
  // Default: same-origin /ws path (works with Vite proxy in dev, Go server in prod)
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${protocol}//${window.location.host}/ws`;
}

let ghosttyLoadPromise: Promise<Ghostty> | null = null;
function loadGhosttyEngine(): Promise<Ghostty> {
  if (!ghosttyLoadPromise) {
    ghosttyLoadPromise = Ghostty.load('/ghostty-vt.wasm');
    // A failed WASM fetch must not poison the module for the rest of the page
    // lifetime: drop the rejected promise so the next mount retries the load.
    ghosttyLoadPromise.catch(() => {
      ghosttyLoadPromise = null;
    });
  }
  return ghosttyLoadPromise;
}

export interface TerminalWindowProps {
  session: TerminalSession;
  node: NodeItem;
  isActive: boolean;
  isOpen: boolean;
  themeChoice?: ThemeChoice;
  resolvedTheme?: ResolvedTheme;
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
  themeChoice,
  resolvedTheme,
  onFocus,
  onClose,
  onMinimize,
  onToggleMaximize,
  onUpdatePosition,
  onUpdateSize,
}) => {
  const [running, setRunning] = useState(false);
  const [engineReady, setEngineReady] = useState(false);
  const [engineError, setEngineError] = useState<string | null>(null);
  const [lastOutputText, setLastOutputText] = useState('');
  // 'connecting' until the backend actually acks the socket open — never
  // default to 'connected' before a real connection exists.
  const [connStatus, setConnStatus] = useState<'connecting' | 'connected' | 'reconnecting' | 'disconnected'>('connecting');
  const [reconnectAttempt, setReconnectAttempt] = useState(0);
  // Authoritative counter lives in a ref: the WS close handler is captured by
  // the mount effect and would otherwise read a forever-stale `0` from state,
  // so WS_RECONNECT_MAX_ATTEMPTS could never trip.
  const reconnectAttemptRef = useRef(0);
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Bumping this nonce re-runs the mount effect (used by the engine-error
  // Retry button).
  const [mountNonce, setMountNonce] = useState(0);
  const [showQuickMenu, setShowQuickMenu] = useState(false);
  const [quickCommands, setQuickCommands] = useState<QuickCommandItem[]>(() => loadQuickCommands());

  const currentThemeMode: ResolvedTheme = resolvedTheme || resolveTheme(themeChoice);
  const ghosttyTheme = getGhosttyTheme(currentThemeMode);

  // Track intentional close to avoid reconnecting after unmount
  const intentionalCloseRef = useRef(false);
  // Track if component is mounted
  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

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
      if (wsRef.current && wsRef.current.readyState === WebSocket.OPEN && fitAddonRef.current) {
        wsRef.current.send(JSON.stringify({
          type: 'terminal_resize',
          sessionId: session.id,
          cols: termRef.current?.cols ?? 80,
          rows: termRef.current?.rows ?? 24,
        }));
      }
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
          const data = await resp.json().catch(() => null);
          throw new Error(data?.error || `HTTP ${resp.status}`);
        }
      } catch (err: any) {
        // Zero-mock invariant: never synthesize shell output. Report the real
        // transport failure so a dead backend is visible, not disguised.
        output = `command failed: ${err?.message || err}`;
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

    // Fresh mount (including Retry after an engine failure): allow the WS to
    // connect again — a previous cleanup intentionally closed it.
    intentionalCloseRef.current = false;

    let disposed = false;
    let termInstance: Terminal | null = null;
    let fitAddon: FitAddon | null = null;

    async function mountGhostty() {
      try {
        // ghostty-web@0.4.0 has no implicit global engine: the loaded Ghostty
        // instance MUST be handed to the Terminal constructor, otherwise it
        // throws "ghostty-web not initialized" and no canvas is ever created.
        const ghostty = await loadGhosttyEngine();
        if (disposed || !containerRef.current) return;

        containerRef.current.innerHTML = '';

        termInstance = new Terminal({
          ghostty,
          cursorBlink: true,
          fontFamily: '"Fira Code", monospace',
          fontSize: 13,
          theme: ghosttyTheme,
        });

        fitAddon = new FitAddon();
        termInstance.loadAddon(fitAddon);
        termInstance.open(containerRef.current);
        if (!containerRef.current.querySelector('canvas')) {
          throw new Error('ghostty engine opened but produced no canvas');
        }
        fitAddon.fit();
        fitAddon.observeResize();

        // Start from a blank screen. ghostty-web can keep the previous
        // instance's rendered buffer in the canvas/WASM state when a new
        // terminal is opened, which made a freshly connected host show the
        // previous session's output until it was overwritten.
        try {
          termInstance.reset();
        } catch {
          // reset() is best-effort; the ANSI clear below still applies.
        }
        termInstance.write('\x1b[2J\x1b[3J\x1b[H');

        termRef.current = termInstance;
        fitAddonRef.current = fitAddon;
        inputStateRef.current = { buffer: '', history: [], historyIndex: -1 };
        setEngineError(null);
        setEngineReady(true);

        termInstance.write(formatGhosttyBanner(node));
        writePrompt(termInstance);
        if (isActive) {
          termInstance.focus();
        }

        // Connect to real-time interactive WebSocket PTY stream with reconnection logic.
        // The attempt counter lives in a ref because this closure is captured
        // once at mount — reading `reconnectAttempt` state here would always be
        // the initial 0, so backoff never escalated and the cap never tripped.
        const scheduleReconnect = () => {
          const attempt = reconnectAttemptRef.current + 1;
          reconnectAttemptRef.current = attempt;
          setReconnectAttempt(attempt);
          if (attempt > WS_RECONNECT_MAX_ATTEMPTS) {
            setConnStatus('disconnected');
            return;
          }
          const baseDelay = Math.min(
            WS_RECONNECT_BASE_DELAY * Math.pow(2, attempt - 1),
            WS_RECONNECT_MAX_DELAY
          );
          // Add jitter to prevent thundering herd
          const jitter = baseDelay * WS_RECONNECT_JITTER * Math.random();
          reconnectTimerRef.current = setTimeout(() => {
            reconnectTimerRef.current = null;
            if (mountedRef.current && !intentionalCloseRef.current && !disposed) {
              connectWebSocket();
            }
          }, baseDelay + jitter);
        };

        const connectWebSocket = () => {
          if (disposed || !mountedRef.current || intentionalCloseRef.current) return;

          try {
            const wsUrl = getWebSocketUrl();
            const ws = new WebSocket(wsUrl);
            wsRef.current = ws;

            ws.onopen = () => {
              if (disposed || !mountedRef.current) return;
              setConnStatus('connected');
              reconnectAttemptRef.current = 0;
              setReconnectAttempt(0);
              ws.send(
                JSON.stringify({
                  type: 'terminal_start',
                  sessionId: session.id,
                  nodeId: node.id,
                  host: node.id,
                  cols: termInstance?.cols ?? 80,
                  rows: termInstance?.rows ?? 24,
                })
              );
            };

            ws.onmessage = (event) => {
              let msg: any;
              try {
                msg = JSON.parse(event.data);
              } catch (err) {
                console.warn('TerminalWindow: failed to parse WS message', err, event.data);
                return;
              }
              if (!msg || typeof msg.type !== 'string') return;
              // Handle both terminal_data and terminal_output (backend may send either)
              if ((msg.type === 'terminal_data' || msg.type === 'terminal_output') && msg.sessionId === session.id) {
                if (msg.data) {
                  termInstance?.write(msg.data);
                }
                // Defer lastOutputText update to avoid per-message re-render lag
                if (msg.data) {
                  setLastOutputText((prev) => (prev + msg.data).slice(-4000));
                }
                return;
              }
              if (msg.type === 'terminal_exit' && msg.sessionId === session.id) {
                termInstance?.write(`\\r\\n[session exited (code ${msg.code ?? 0})]\\r\\n`);
                setConnStatus('disconnected');
                return;
              }
            };

            ws.onerror = () => {
              if (disposed || !mountedRef.current) return;
              setConnStatus('reconnecting');
            };

            ws.onclose = () => {
              if (disposed || !mountedRef.current || intentionalCloseRef.current) return;
              setConnStatus('disconnected');
              scheduleReconnect();
            };
          } catch {
            if (mountedRef.current && !intentionalCloseRef.current && !disposed) {
              setConnStatus('reconnecting');
              scheduleReconnect();
            }
          }
        };

        connectWebSocket();

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
        if (!disposed) {
          setEngineError(err instanceof Error ? err.message : String(err));
          setEngineReady(false);
        }
      }
    }

    mountGhostty();

    return () => {
      intentionalCloseRef.current = true;
      disposed = true;
      setEngineReady(false);
      if (reconnectTimerRef.current) {
        clearTimeout(reconnectTimerRef.current);
        reconnectTimerRef.current = null;
      }
      reconnectAttemptRef.current = 0;
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
  }, [isOpen, session.id, node?.id, currentThemeMode, mountNonce]);

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
      className={`bg-[#0a0d1f] rounded-lg shadow-2xl border ${
        isActive ? 'border-[#b0c9ff]/70 shadow-[0_4px_24px_rgba(0,0,0,0.6)]' : 'border-[#313447]'
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
            className={`absolute z-30 ${getHandleClass(dir)} hover:bg-[#b0c9ff]/20`}
          />
        ))}

      {/* Dedicated Window Title Bar (Single Session - No Tabs) */}
      <div
        {...dragProps}
        onPointerDown={(e) => {
          onFocus();
          dragProps.onPointerDown?.(e);
        }}
        className="h-10 bg-[#171a2d] border-b border-[#26293c] px-3.5 flex items-center justify-between cursor-grab active:cursor-grabbing select-none shrink-0"
      >
        {/* Left: Terminal Icon + Host Name + SSH Target + Status Dot */}
        <div className="flex items-center gap-2 min-w-0">
          <TerminalIcon className={`w-3.5 h-3.5 shrink-0 ${isActive ? 'text-[#8cd6cb]' : 'text-[#8d909b]'}`} />
          <span className="font-mono font-bold text-xs text-[#dfe1fa] truncate max-w-[150px]">
            {session.nodeName || node.name}
          </span>
          <span className="font-mono text-[11px] text-[#8d909b] hidden sm:inline truncate max-w-[220px]">
            {sshTarget}
          </span>
          <span
            data-testid="terminal-conn-status"
            data-status={connStatus}
            className={`w-2 h-2 rounded-full shrink-0 ${connStatus === 'connected' ? 'bg-[#3fb950]' : connStatus === 'connecting' ? 'bg-[#8d909b] animate-pulse' : connStatus === 'reconnecting' ? 'bg-amber-400 animate-pulse' : 'bg-red-500'}`}
            title={
              connStatus === 'reconnecting'
                ? `Status: ${connStatus} (attempt ${reconnectAttempt}/${WS_RECONNECT_MAX_ATTEMPTS})`
                : `Status: ${connStatus}`
            }
          />
        </div>

        {/* Right: Quick Commands + Window Action Buttons */}
        <div className="flex items-center gap-2 shrink-0">
          {/* Quick Commands for Desktop */}
          <div className="hidden md:flex items-center gap-1 px-1.5 py-0.5 bg-[#121528] rounded border border-[#26293c]">
            <span className="text-[10px] text-[#8d909b] font-mono flex items-center gap-1">
              <TerminalSquare className="w-3 h-3 text-[#8cd6cb]" />
            </span>
            {quickCommands.map((qc) => (
              <button
                key={qc.id}
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  handleRunQuickCommand(qc.cmd);
                }}
                className="px-1.5 py-0.5 text-[10px] font-mono text-[#c3c6d2] hover:text-[#dfe1fa] hover:bg-[#26293c] rounded transition-colors cursor-pointer"
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
              className="p-1 rounded text-[#8d909b] hover:text-[#dfe1fa] hover:bg-[#26293c] transition-colors cursor-pointer"
              title="Quick Commands"
            >
              <TerminalSquare className="w-3.5 h-3.5 text-[#8cd6cb]" />
            </button>
            {showQuickMenu && (
              <div className="absolute right-0 top-8 z-50 bg-[#171a2d] border border-[#26293c] rounded-md shadow-xl py-1 w-48">
                <div className="px-2.5 py-1 text-[10px] font-mono text-[#8d909b] uppercase tracking-wider border-b border-[#26293c]">
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
                    className="w-full text-left px-2.5 py-1.5 text-xs font-mono text-[#c3c6d2] hover:text-[#dfe1fa] hover:bg-[#26293c] flex flex-col cursor-pointer"
                    title={qc.cmd}
                  >
                    <span>{qc.label}</span>
                    {qc.description && (
                      <span className="text-[10px] text-[#8d909b] font-sans truncate">{qc.description}</span>
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
              data-testid="terminal-minimize"
              onClick={(e) => {
                e.stopPropagation();
                onMinimize();
              }}
              className="p-1 rounded text-[#8d909b] hover:text-[#dfe1fa] hover:bg-[#26293c] transition-colors cursor-pointer"
              title="Minimize to Console"
            >
              <Minus className="w-3.5 h-3.5" />
            </button>
            <button
              type="button"
              data-testid="terminal-maximize"
              onClick={(e) => {
                e.stopPropagation();
                onToggleMaximize();
              }}
              className="p-1 rounded text-[#8d909b] hover:text-[#dfe1fa] hover:bg-[#26293c] transition-colors cursor-pointer"
              title={isMax ? 'Restore' : 'Maximize'}
            >
              {isMax ? <Copy className="w-3.5 h-3.5" /> : <Square className="w-3.5 h-3.5" />}
            </button>
            <button
              type="button"
              data-testid="terminal-close"
              onClick={(e) => {
                e.stopPropagation();
                onClose();
              }}
              className="p-1 rounded text-[#8d909b] hover:text-[#ffb4ab] hover:bg-[#ffb4ab]/10 transition-colors cursor-pointer"
              title="Close Terminal Session"
            >
              <X className="w-3.5 h-3.5" />
            </button>
          </div>
        </div>
      </div>

      {/* Terminal Canvas Body */}
      <div className="flex-1 relative overflow-hidden">
        <div
          ref={containerRef}
          onClick={() => {
            onFocus();
            termRef.current?.focus();
          }}
          data-testid="ghostty-terminal-container"
          className="h-full w-full bg-[#0a0d1f] overflow-hidden relative cursor-text select-text"
        />
        {engineError && (
          <div
            data-testid="ghostty-engine-error"
            role="alert"
            className="absolute inset-0 z-20 bg-[#0a0d1f] flex flex-col items-center justify-center gap-3 p-6 text-center"
          >
            <div className="font-mono text-xs font-bold text-[#ffb4ab]">
              Terminal engine failed to start
            </div>
            <div className="font-mono text-[11px] text-[#c3c6d2] max-w-md break-words">
              {engineError}
            </div>
            <button
              type="button"
              data-testid="ghostty-engine-retry"
              onClick={() => {
                setEngineError(null);
                setMountNonce((n) => n + 1);
              }}
              className="px-3 py-1.5 rounded font-mono text-xs font-bold text-white bg-[#1f6feb] hover:bg-[#388bfd] cursor-pointer"
            >
              Retry
            </button>
          </div>
        )}
      </div>

      {/* Accessible screen-reader mirror */}
      {lastOutputText && (
        <pre className="sr-only" data-testid="ghostty-last-output">
          {lastOutputText}
        </pre>
      )}

      {/* Terminal Footer Status Bar */}
      <div className="h-7 bg-[#171a2d] px-3.5 flex items-center justify-between font-mono text-[10px] text-[#8d909b] border-t border-[#26293c] shrink-0">
        <div className="flex items-center gap-2">
          <span className="flex items-center gap-1">
            {connStatus === 'reconnecting' ? (
              <RotateCcw className="w-3.5 h-3.5 animate-spin text-amber-400" />
            ) : connStatus === 'connected' ? (
              <Wifi className="w-3.5 h-3.5 text-[#3fb950]" />
            ) : connStatus === 'connecting' ? (
              <Wifi className="w-3.5 h-3.5 text-[#8d909b] animate-pulse" />
            ) : (
              <WifiOff className="w-3.5 h-3.5 text-red-500" />
            )}
            <span className="capitalize">
              {connStatus}
              {connStatus === 'reconnecting' && ` (attempt ${reconnectAttempt}/${WS_RECONNECT_MAX_ATTEMPTS})`}
            </span>
          </span>
          <span className="text-[#313447]">|</span>
          <span className="truncate max-w-[200px] text-[#c3c6d2]">
            {sshTarget}:{node?.port || 22}
          </span>
          <span className="text-[#313447]">|</span>
          <span className="text-[#8cd6cb]">libghostty-vt WASM</span>
        </div>

        <div className="flex items-center gap-2">
          <span className="text-[#8d909b]">
            {isMax ? 'Maximized' : `${size.width}x${size.height}`}
          </span>
          <span className="text-[#3fb950]">
            {running ? 'Executing remote SSH...' : 'WireGuard encrypted'}
          </span>
        </div>
      </div>
    </div>
  );
};
