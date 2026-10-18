import React from 'react';

interface ContainerLogsModalProps {
  targetName: string | null;
  isOpen: boolean;
  onClose: () => void;
}

export const ContainerLogsModal: React.FC<ContainerLogsModalProps> = ({
  targetName,
  isOpen,
  onClose,
}) => {
  if (!isOpen || !targetName) return null;

  const mockLogs = [
    `[podman-engine] Attaching to container/pod: ${targetName}`,
    'INFO  [server::init] Loading configuration from /etc/services/config.yaml',
    'INFO  [server::network] Listening on 0.0.0.0:8096 (HTTP/2 enabled)',
    'INFO  [engine::storage] ZFS subvolume mounted rw,noatime at /data',
    'DEBUG [transcoder::vaapi] Initialized Intel QSV / VAAPI hardware acceleration device /dev/dri/renderD128',
    'INFO  [auth::pam] Rootless podman namespace user mapped: subuid 100000:165535',
    'INFO  [stream::health] Health check passed (response time: 1.4ms)',
    'INFO  [metrics::exporter] Exporting Prometheus metrics on port :9150',
    'INFO  [ready] Service is ready for client connections.',
  ];

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-xs p-4">
      <div className="w-full max-w-3xl bg-[#0a0d1f] rounded-lg shadow-2xl border border-[#313447] flex flex-col h-[480px] overflow-hidden">
        <div className="h-9 bg-[#171a2d] px-3 flex items-center justify-between border-b border-[#26293c]">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[#8cd6cb] text-[18px]">article</span>
            <span className="font-mono text-xs text-[#dfe1fa] font-bold">
              Podman Logs: {targetName}
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
          {mockLogs.map((line, idx) => (
            <div key={idx} className="flex gap-2">
              <span className="text-[#8d909b] select-none text-[10px]">
                {String(idx + 1).padStart(2, '0')}
              </span>
              <span
                className={
                  line.includes('DEBUG')
                    ? 'text-[#d9b9ff]'
                    : line.includes('ready')
                    ? 'text-[#8cd6cb] font-bold'
                    : 'text-[#dfe1fa]'
                }
              >
                {line}
              </span>
            </div>
          ))}
        </div>

        <div className="h-8 bg-[#171a2d] px-3 flex items-center justify-between font-mono text-xs text-[#8d909b] border-t border-[#26293c]">
          <span>Streaming live stdout/stderr via cockpit-bridge socket</span>
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
