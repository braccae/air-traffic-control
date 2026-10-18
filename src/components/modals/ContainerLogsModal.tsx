import React, { useEffect, useState } from 'react';
import type { NodeItem } from '../../types/index.ts';

interface ContainerLogsModalProps {
  targetName: string | null;
  node?: NodeItem;
  isOpen: boolean;
  onClose: () => void;
}

export const ContainerLogsModal: React.FC<ContainerLogsModalProps> = ({
  targetName,
  node,
  isOpen,
  onClose,
}) => {
  const [lines, setLines] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);

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

  if (!isOpen || !targetName) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-xs p-4">
      <div className="w-full max-w-3xl bg-[#0a0d1f] rounded-lg shadow-2xl border border-[#313447] flex flex-col h-[480px] overflow-hidden">
        <div className="h-9 bg-[#171a2d] px-3 flex items-center justify-between border-b border-[#26293c]">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[#8cd6cb] text-[18px]">article</span>
            <span className="font-mono text-xs text-[#dfe1fa] font-bold">
              Live Logs ({node?.shortName || 'host'}): {targetName}
            </span>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="text-[#8d909b] hover:text-[#dfe1fa] p-1 cursor-pointer"
          >
            <span className="material-symbols-outlined text-[16px]">close</span>
          </button>
        </div>

        <div className="flex-1 bg-[#0a0d1f] p-3 font-mono text-xs overflow-y-auto space-y-1 text-[#c3c6d2]">
          {loading ? (
            <div className="text-[#8cd6cb] animate-pulse">
              Fetching live logs from {node?.tailscaleHost || 'host'} via SSH...
            </div>
          ) : (
            lines.map((line, idx) => (
              <div key={idx} className="flex gap-2">
                <span className="text-[#8d909b] select-none text-[10px]">
                  {String(idx + 1).padStart(2, '0')}
                </span>
                <span className="text-[#dfe1fa] break-all">{line}</span>
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
