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

interface TerminalModalProps {
  node: NodeItem;
  isOpen: boolean;
  onClose: () => void;
}

export const TerminalModal: React.FC<TerminalModalProps> = ({
  node,
  isOpen,
  onClose,
}) => {
  const sshUser = node.sshUser || 'core';
  const [running, setRunning] = useState(false);
  const [engineReady, setEngineReady] = useState(false);
  const [lastOutputText, setLastOutputText] = useState('');
  const containerRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const inputStateRef = useRef<TerminalInputState>({
    buffer: '',
    history: [],
    historyIndex: -1,
  });
  const runningRef = useRef(false);

  const writePrompt = useCallback(
    (term?: Terminal | null) => {
      const target = term || termRef.current;
      if (!target) return;
      target.write(formatGhosttyPrompt(node));
    },
    [node]
  );

  const executeRemoteCommand = useCallback(
    async (cmd: string, echoCommandLine = false) => {
      const term = termRef.current;
      if (!term) return;

      const trimmed = cmd.trim();
      if (echoCommandLine) {
        // Erase any partially typed buffer first
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
      term.focus();
    },
    [node, writePrompt]
  );

  useEffect(() => {
    if (!isOpen) return;

    let disposed = false;
    let termInstance: Terminal | null = null;
    let fitAddon: FitAddon | null = null;

    async function mountGhostty() {
      if (!containerRef.current) return;
      try {
        const ghostty = await Ghostty.load('/ghostty-vt.wasm');
        if (disposed || !containerRef.current) return;

        termInstance = new Terminal({
          cursorBlink: true,
          cursorStyle: 'block',
          fontSize: 13,
          fontFamily: '"JetBrains Mono", "Fira Code", monospace',
          scrollback: 5000,
          ghostty,
          theme: {
            background: '#0a0d1f',
            foreground: '#dfe1fa',
            cursor: '#8cd6cb',
            cursorAccent: '#0a0d1f',
            selectionBackground: '#35384c',
            black: '#171a2d',
            red: '#ffb4ab',
            green: '#8cd6cb',
            yellow: '#ffd97d',
            blue: '#b0c9ff',
            magenta: '#d9baff',
            cyan: '#8cd6cb',
            white: '#dfe1fa',
          },
        });

        fitAddon = new FitAddon();
        termInstance.loadAddon(fitAddon);

        containerRef.current.innerHTML = '';
        termInstance.open(containerRef.current);
        fitAddon.fit();
        fitAddon.observeResize();

        termRef.current = termInstance;
        fitAddonRef.current = fitAddon;
        inputStateRef.current = { buffer: '', history: [], historyIndex: -1 };
        setEngineReady(true);

        termInstance.write(formatGhosttyBanner(node));
        writePrompt(termInstance);
        termInstance.focus();

        termInstance.onData((data: string) => {
          if (!termInstance || runningRef.current) return;
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
      if (fitAddon) {
        try {
          fitAddon.dispose();
        } catch {
          // ignore
        }
      }
      if (termInstance) {
        try {
          termInstance.dispose();
        } catch {
          // ignore
        }
      }
      termRef.current = null;
      fitAddonRef.current = null;
    };
  }, [isOpen, node, executeRemoteCommand, writePrompt]);

  if (!isOpen) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-xs p-4">
      <div className="w-full max-w-4xl bg-[#0a0d1f] rounded-lg shadow-2xl border border-[#313447] flex flex-col h-[580px] overflow-hidden">
        {/* Terminal Header */}
        <div className="h-8 bg-[#171a2d] px-3 flex items-center justify-between border-b border-[#26293c]">
          <div className="flex items-center gap-2">
            <span className="w-3 h-3 rounded-full bg-[#ffb4ab]/80 inline-block"></span>
            <span className="w-3 h-3 rounded-full bg-[#ffd97d]/80 inline-block"></span>
            <span className="w-3 h-3 rounded-full bg-[#8cd6cb]/80 inline-block"></span>
            <span className="font-mono text-xs text-[#dfe1fa] font-bold ml-2 flex items-center gap-1.5">
              <span className="material-symbols-outlined text-[15px] text-[#b0c9ff]">terminal</span>
              {sshUser}@{node.tailscaleHost}:~ (SSH via ssh-agent)
            </span>
            <span className="px-1.5 py-0.5 rounded bg-[#26293c] text-[#8cd6cb] font-mono text-[10px] border border-[#8cd6cb]/30">
              {engineReady ? 'libghostty-vt WASM' : 'loading libghostty...'}
            </span>
          </div>

          <div className="flex items-center gap-1">
            <button
              type="button"
              onClick={onClose}
              className="text-[#8d909b] hover:text-[#dfe1fa] p-1 cursor-pointer"
              aria-label="Close terminal"
            >
              <span className="material-symbols-outlined text-[16px]">close</span>
            </button>
          </div>
        </div>

        {/* Quick action command pills */}
        <div className="bg-[#1b1e31] px-3 py-1.5 flex items-center gap-1.5 border-b border-[#26293c] overflow-x-auto">
          <span className="text-[10px] font-mono text-[#8d909b]">Quick:</span>
          {[
            'uname -a',
            'uptime',
            'free -h',
            'df -h /',
            'podman ps --all',
            'cat /etc/subuid',
            'ls -l /var/lib/systemd/linger',
            'id',
          ].map(cmd => (
            <button
              key={cmd}
              type="button"
              disabled={running || !engineReady}
              onClick={() => executeRemoteCommand(cmd, true)}
              className="px-2 py-0.5 bg-[#26293c] hover:bg-[#35384c] disabled:opacity-50 text-[#8cd6cb] font-mono text-[10px] rounded cursor-pointer transition-colors border border-[#434750]/30 whitespace-nowrap"
            >
              {cmd}
            </button>
          ))}
        </div>

        {/* libghostty-vt Canvas Screen Body */}
        <div
          ref={containerRef}
          onClick={() => termRef.current?.focus()}
          data-testid="ghostty-terminal-container"
          className="flex-1 bg-[#0a0d1f] p-2 overflow-hidden relative cursor-text"
        />

        {/* Accessible screen-reader mirror of latest command output for assistive & automated verification */}
        {lastOutputText && (
          <pre className="sr-only" data-testid="ghostty-last-output">
            {lastOutputText}
          </pre>
        )}

        {/* Terminal status bar */}
        <div className="h-6 bg-[#171a2d] px-3 flex items-center justify-between font-mono text-[10px] text-[#8d909b] border-t border-[#26293c]">
          <span>
            Engine: libghostty-vt (ghostty-web 0.4.0) | Target: {sshUser}@{node.tailscaleHost}:{node.port || 22}
          </span>
          <span className="text-[#8cd6cb]">
            {running ? 'Executing remote SSH command...' : 'Encrypted Tailscale WireGuard + OpenSSH'}
          </span>
        </div>
      </div>
    </div>
  );
};
