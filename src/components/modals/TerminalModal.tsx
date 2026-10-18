import React, { useState, useRef, useEffect } from 'react';
import type { NodeItem } from '../../types/index.ts';
import { parseTerminalCommand } from '../../utils/orchestrator.ts';

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
  const [history, setHistory] = useState<Array<{ command: string; output: string }>>([]);
  const [currentInput, setCurrentInput] = useState('');
  const [running, setRunning] = useState(false);
  const bottomRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    setHistory([
      {
        command: '',
        output: [
          'PROX-ORCH v8.2-ansible | Live SSH-Agent & Cockpit Gateway',
          `Connected to: ${sshUser}@${node.tailscaleHost} (${node.ip} / ${node.tailscaleIp})`,
          `OS: ${node.osDescription} | SSH Auth: ${node.authMethod || 'ssh-ed25519 (/run/user/1000/ssh-agent.socket)'}`,
          'Type any shell command (e.g. "uname -a", "uptime", "free -h", "podman ps --all", "id").',
        ].join('\n'),
      },
    ]);
  }, [node.id, node.tailscaleHost, sshUser]);

  useEffect(() => {
    if (isOpen) {
      setTimeout(() => {
        inputRef.current?.focus();
        bottomRef.current?.scrollIntoView({ behavior: 'smooth' });
      }, 100);
    }
  }, [isOpen, history]);

  if (!isOpen) return null;

  const executeRemoteCommand = async (cmd: string) => {
    if (!cmd) return;
    if (cmd.toLowerCase() === 'clear') {
      setHistory([]);
      return;
    }

    setRunning(true);
    try {
      const resp = await fetch('/api/terminal/exec', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ command: cmd, host: node.id }),
      });
      if (resp.ok) {
        const data = await resp.json();
        setHistory(prev => [...prev, { command: cmd, output: data.output || '' }]);
        setRunning(false);
        return;
      }
    } catch {
      // Fallback
    }
    const { output } = parseTerminalCommand(cmd, node);
    setHistory(prev => [...prev, { command: cmd, output }]);
    setRunning(false);
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const cmd = currentInput.trim();
    setCurrentInput('');
    executeRemoteCommand(cmd);
  };

  const runQuickCommand = (cmd: string) => {
    executeRemoteCommand(cmd);
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-xs p-4">
      <div className="w-full max-w-4xl bg-[#0a0d1f] rounded-lg shadow-2xl border border-[#313447] flex flex-col h-[560px] overflow-hidden">
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
              onClick={() => runQuickCommand(cmd)}
              className="px-2 py-0.5 bg-[#26293c] hover:bg-[#35384c] text-[#8cd6cb] font-mono text-[10px] rounded cursor-pointer transition-colors border border-[#434750]/30 whitespace-nowrap"
            >
              {cmd}
            </button>
          ))}
        </div>

        {/* Terminal Screen Body */}
        <div
          onClick={() => inputRef.current?.focus()}
          className="flex-1 bg-[#0a0d1f] p-3 font-mono text-xs overflow-y-auto text-[#dfe1fa] space-y-2 cursor-text"
        >
          {history.map((item, idx) => (
            <div key={idx} className="space-y-0.5">
              {item.command && (
                <div className="flex items-center gap-1.5 text-[#b0c9ff]">
                  <span className="text-[#8cd6cb] font-bold">
                    [{sshUser}@{node.shortName} ~]$
                  </span>
                  <span>{item.command}</span>
                </div>
              )}
              {item.output && (
                <pre className="text-[#c3c6d2] whitespace-pre-wrap font-mono text-xs leading-relaxed pl-1 border-l-2 border-[#26293c]">
                  {item.output}
                </pre>
              )}
            </div>
          ))}

          {/* Prompt line */}
          <form onSubmit={handleSubmit} className="flex items-center gap-1.5 text-[#dfe1fa] pt-1">
            <span className="text-[#8cd6cb] font-bold">
              [{sshUser}@{node.shortName} ~]$
            </span>
            <input
              ref={inputRef}
              type="text"
              disabled={running}
              value={currentInput}
              onChange={e => setCurrentInput(e.target.value)}
              className="flex-1 bg-transparent border-none outline-none font-mono text-xs text-[#dfe1fa] focus:ring-0"
              autoFocus
              placeholder={running ? 'executing over SSH...' : 'type command...'}
            />
          </form>
          <div ref={bottomRef} />
        </div>

        {/* Terminal status bar */}
        <div className="h-6 bg-[#171a2d] px-3 flex items-center justify-between font-mono text-[10px] text-[#8d909b] border-t border-[#26293c]">
          <span>
            Agent: /run/user/1000/ssh-agent.socket | Target: {sshUser}@{node.tailscaleHost}:{node.port || 22}
          </span>
          <span className="text-[#8cd6cb]">Encrypted Tailscale WireGuard + OpenSSH</span>
        </div>
      </div>
    </div>
  );
};
