import React, { useEffect, useState, useRef } from 'react';
import type { NodeItem } from '../../types/index.ts';

interface ContainerLogsModalProps {
  targetName: string | null;
  node?: NodeItem;
  isOpen: boolean;
  isMinimized?: boolean;
  onMinimize?: () => void;
  onClose: () => void;
}

export const ContainerLogsModal: React.FC<ContainerLogsModalProps> = ({
  targetName,
  node,
  isOpen,
  isMinimized = false,
  onMinimize,
  onClose,
}) => {
  const [lines, setLines] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);
  const [filter, setFilter] = useState('');
  const [autoScroll, setAutoScroll] = useState(true);
  const logContainerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!isOpen || !targetName) return;
    setLoading(true);
    const hostParam = node?.id || node?.tailscaleHost || 'likes-it-cold';
    fetch(
      `/api/podman/logs?host=${encodeURIComponent(hostParam)}&target=${encodeURIComponent(targetName)}`
    )
      .then(res => res.json())
      .then(data => {
        if (Array.isArray(data?.lines) && data.lines.length > 0) {
          setLines(data.lines);
        } else {
          setLines([`[${hostParam}] Attached to ${targetName}`]);
        }
      })
      .catch(() => {
        setLines([`[${hostParam}] Attached to ${targetName}`]);
      })
      .finally(() => setLoading(false));
  }, [isOpen, targetName, node?.id]);

  useEffect(() => {
    if (autoScroll && logContainerRef.current) {
      logContainerRef.current.scrollTop = logContainerRef.current.scrollHeight;
    }
  }, [lines, autoScroll]);

  if (!isOpen || !targetName) return null;

  const filteredLines = lines.filter(l =>
    !filter.trim() || l.toLowerCase().includes(filter.toLowerCase())
  );

  return (
    <div
      style={{ display: isMinimized ? 'none' : 'flex' }}
      className="fixed inset-0 z-50 items-center justify-center bg-black/70 backdrop-blur-xs p-4"
    >
      <div className="w-full max-w-3xl bg-[#0a0d1f] rounded-lg shadow-2xl border border-[#313447] flex flex-col h-[520px] overflow-hidden">
        <div className="h-9 bg-[#171a2d] px-3 flex items-center justify-between border-b border-[#26293c]">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[#8cd6cb] text-[18px]">article</span>
            <span className="font-mono text-xs text-[#dfe1fa] font-bold">
              Live Logs ({node?.shortName || 'host'}): {targetName}
            </span>
          </div>
          <div className="flex items-center gap-1">
            {onMinimize && (
              <button
                type="button"
                onClick={onMinimize}
                className="text-[#8d909b] hover:text-[#dfe1fa] p-1 cursor-pointer transition-colors"
                title="Minimize to bottom panel"
              >
                <span className="material-symbols-outlined text-[16px]">remove</span>
              </button>
            )}
            <button
              type="button"
              onClick={onClose}
              className="text-[#8d909b] hover:text-[#dfe1fa] p-1 cursor-pointer transition-colors"
              title="Close"
            >
              <span className="material-symbols-outlined text-[16px]">close</span>
            </button>
          </div>
        </div>

        {/* Filter & Controls Toolbar */}
        <div className="bg-[#121528] px-3 py-1.5 flex items-center justify-between gap-2 border-b border-[#26293c] font-mono text-xs">
          <div className="flex items-center gap-1.5 flex-1 max-w-sm bg-[#1b1e31] px-2 py-0.5 rounded border border-[#26293c]">
            <span className="material-symbols-outlined text-[14px] text-[#8d909b]">filter_alt</span>
            <input
              type="text"
              placeholder="Filter log lines..."
              value={filter}
              onChange={e => setFilter(e.target.value)}
              className="bg-transparent border-none outline-none text-[#dfe1fa] text-xs w-full"
            />
            {filter && (
              <button
                type="button"
                onClick={() => setFilter('')}
                className="text-[#8d909b] hover:text-[#dfe1fa] text-[10px]"
              >
                ✕
              </button>
            )}
          </div>

          <div className="flex items-center gap-2 text-[11px] text-[#c3c6d2]">
            <span className="text-[#8d909b]">{filteredLines.length}/{lines.length} lines</span>
            <label className="flex items-center gap-1 cursor-pointer select-none">
              <input
                type="checkbox"
                checked={autoScroll}
                onChange={e => setAutoScroll(e.target.checked)}
                className="rounded"
              />
              <span>Autoscroll</span>
            </label>
          </div>
        </div>

        <div
          ref={logContainerRef}
          className="flex-1 bg-[#0a0d1f] p-3 font-mono text-xs overflow-y-auto space-y-1 text-[#c3c6d2]"
        >
          {loading ? (
            <div className="text-[#8cd6cb] animate-pulse">
              Fetching live logs from {node?.tailscaleHost || 'host'} via SSH...
            </div>
          ) : filteredLines.length === 0 ? (
            <div className="text-[#8d909b] italic">No log lines match filter "{filter}"</div>
          ) : (
            filteredLines.map((line, idx) => (
              <div key={idx} className="flex gap-2 hover:bg-[#121528] px-1 rounded">
                <span className="text-[#8d909b] select-none text-[10px]">
                  {String(idx + 1).padStart(2, '0')}
                </span>
                <span className={`break-all ${
                  line.includes('ERROR') || line.includes('ERR') || line.includes('failed')
                    ? 'text-[#ffb4ab]'
                    : line.includes('WARN')
                    ? 'text-[#ffd97d]'
                    : 'text-[#dfe1fa]'
                }`}>
                  {line}
                </span>
              </div>
            ))
          )}
        </div>

        <div className="h-8 bg-[#171a2d] px-3 flex items-center justify-between font-mono text-xs text-[#8d909b] border-t border-[#26293c]">
          <span>
            Streaming live stdout/stderr from {node?.sshUser || 'core'}@
            {node?.tailscaleHost || 'host'}
          </span>
          <button
            type="button"
            onClick={onClose}
            className="px-2.5 py-0.5 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] rounded text-xs cursor-pointer"
          >
            Close
          </button>
        </div>
      </div>
    </div>
  );
};
