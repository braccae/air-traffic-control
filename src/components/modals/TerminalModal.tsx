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
  const [history, setHistory] = useState<Array<{ command: string; output: string }>>([
    {
      command: '',
      output: [
        'PROX-ORCH v8.2-ansible | Cockpit Bridge & SSH PTY Gateway',
        `Connected to: ${node.name} (${node.ip} / ${node.tailscaleIp})`,
        `Kernel: Linux 6.8.4-pve | SSH Session established via ed25519`,
        'Type "help" for a list of diagnostics or "podman ps", "glances", "tailscale status", "restic snapshots".',
      ].join('\n'),
    },
  ]);
  const [currentInput, setCurrentInput] = useState('');
  const bottomRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (isOpen) {
      setTimeout(() => {
        inputRef.current?.focus();
        bottomRef.current?.scrollIntoView({ behavior: 'smooth' });
      }, 100);
    }
  }, [isOpen, history]);

  if (!isOpen) return null;

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const cmd = currentInput.trim();
    if (cmd.toLowerCase() === 'clear') {
      setHistory([]);
      setCurrentInput('');
      return;
    }

    const { output } = parseTerminalCommand(cmd, node);
    setHistory(prev => [...prev, { command: cmd, output }]);
    setCurrentInput('');
  };

  const runQuickCommand = (cmd: string) => {
    const { output } = parseTerminalCommand(cmd, node);
    setHistory(prev => [...prev, { command: cmd, output }]);
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
              root@{node.tailscaleHost}:~ (SSH / cockpit-bridge ws-tunnel)
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
          {['podman ps', 'podman pod ps', 'podman probe', 'glances', 'tailscale status', 'restic snapshots', 'uptime', 'uname -a'].map(
            cmd => (
              <button
                key={cmd}
                type="button"
                onClick={() => runQuickCommand(cmd)}
                className="px-2 py-0.5 bg-[#26293c] hover:bg-[#35384c] text-[#8cd6cb] font-mono text-[10px] rounded cursor-pointer transition-colors border border-[#434750]/30 whitespace-nowrap"
              >
                {cmd}
              </button>
            )
          )}
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
                  <span className="text-[#8cd6cb] font-bold">[root@{node.shortName} ~]#</span>
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
            <span className="text-[#8cd6cb] font-bold">[root@{node.shortName} ~]#</span>
            <input
              ref={inputRef}
              type="text"
              value={currentInput}
              onChange={e => setCurrentInput(e.target.value)}
              className="flex-1 bg-transparent border-none outline-none font-mono text-xs text-[#dfe1fa] focus:ring-0"
              autoFocus
              placeholder="type command..."
            />
          </form>
          <div ref={bottomRef} />
        </div>

        {/* Terminal status bar */}
        <div className="h-6 bg-[#171a2d] px-3 flex items-center justify-between font-mono text-[10px] text-[#8d909b] border-t border-[#26293c]">
          <span>Protocol: SSH-2.0-OpenSSH_9.5p1 / PTY allocated (80x24)</span>
          <span className="text-[#8cd6cb]">Encrypted TLS 1.3 / Tailscale WireGuard</span>
        </div>
      </div>
    </div>
  );
};
