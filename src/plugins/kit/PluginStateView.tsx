import React from 'react';

export interface PluginStateViewProps {
  state: 'loading' | 'offline' | 'empty' | 'error' | 'ready';
  title?: string;
  message?: string;
  error?: string;
  onRetry?: () => void;
  children?: React.ReactNode;
  className?: string;
}

export const PluginStateView: React.FC<PluginStateViewProps> = ({
  state,
  title,
  message,
  error,
  onRetry,
  children,
  className = '',
}) => {
  if (state === 'ready') {
    return <>{children}</>;
  }

  if (state === 'loading') {
    return (
      <div
        className={`bg-[#171a2d] border border-[#26293c] rounded p-8 flex flex-col items-center justify-center gap-3 font-mono text-center ${className}`.trim()}
      >
        <span className="material-symbols-outlined text-2xl text-[#b0c9ff] animate-spin">
          progress_activity
        </span>
        <div className="flex flex-col gap-1">
          <span className="text-xs font-bold text-[#dfe1fa]">
            {title || 'Loading Plugin Data...'}
          </span>
          <span className="text-[11px] text-[#8d909b]">
            {message || 'Communicating with host via daemon connection runner'}
          </span>
        </div>
      </div>
    );
  }

  if (state === 'offline') {
    return (
      <div
        className={`bg-[#171a2d] border border-[#ffb4ab]/30 rounded p-6 flex flex-col items-center justify-center gap-3 font-mono text-center ${className}`.trim()}
      >
        <div className="w-10 h-10 rounded-full bg-[#ffb4ab]/10 border border-[#ffb4ab]/30 flex items-center justify-center text-[#ffb4ab]">
          <span className="material-symbols-outlined text-[20px]">cloud_off</span>
        </div>
        <div className="flex flex-col gap-1 max-w-md">
          <span className="text-xs font-bold text-[#ffb4ab]">
            {title || 'Host or Daemon Offline'}
          </span>
          <span className="text-[11px] text-[#c3c6d2]">
            {message || 'Target node is currently unreachable or daemon probe failed.'}
          </span>
          {error && (
            <span className="text-[10px] text-[#ffb4ab]/80 bg-[#0a0d1f] p-2 rounded border border-[#ffb4ab]/20 mt-1 break-all">
              {error}
            </span>
          )}
        </div>
        {onRetry && (
          <button
            type="button"
            onClick={onRetry}
            className="flex items-center gap-1.5 px-3 py-1 rounded bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] text-xs font-semibold transition-colors border border-[#434750]/40 cursor-pointer"
          >
            <span className="material-symbols-outlined text-[14px]">refresh</span>
            <span>Retry Connection</span>
          </button>
        )}
      </div>
    );
  }

  if (state === 'error') {
    return (
      <div
        className={`bg-[#171a2d] border border-[#ffb4ab]/30 rounded p-6 flex flex-col items-center justify-center gap-3 font-mono text-center ${className}`.trim()}
      >
        <div className="w-10 h-10 rounded-full bg-[#ffb4ab]/10 border border-[#ffb4ab]/30 flex items-center justify-center text-[#ffb4ab]">
          <span className="material-symbols-outlined text-[20px]">error</span>
        </div>
        <div className="flex flex-col gap-1 max-w-md">
          <span className="text-xs font-bold text-[#ffb4ab]">
            {title || 'Plugin Probe or Execution Error'}
          </span>
          <span className="text-[11px] text-[#c3c6d2]">
            {message || 'The plugin encountered an error during inspection or command execution.'}
          </span>
          {error && (
            <span className="text-[10px] text-[#ffb4ab]/80 bg-[#0a0d1f] p-2 rounded border border-[#ffb4ab]/20 mt-1 break-all">
              {error}
            </span>
          )}
        </div>
        {onRetry && (
          <button
            type="button"
            onClick={onRetry}
            className="flex items-center gap-1.5 px-3 py-1 rounded bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] text-xs font-semibold transition-colors border border-[#434750]/40 cursor-pointer"
          >
            <span className="material-symbols-outlined text-[14px]">refresh</span>
            <span>Retry</span>
          </button>
        )}
      </div>
    );
  }

  // state === 'empty'
  return (
    <div
      className={`bg-[#171a2d] border border-[#26293c] rounded p-8 flex flex-col items-center justify-center gap-3 font-mono text-center ${className}`.trim()}
    >
      <div className="w-10 h-10 rounded-full bg-[#26293c]/50 flex items-center justify-center text-[#8d909b]">
        <span className="material-symbols-outlined text-[20px]">inbox</span>
      </div>
      <div className="flex flex-col gap-1 max-w-md">
        <span className="text-xs font-bold text-[#dfe1fa]">
          {title || 'No Resources Detected'}
        </span>
        <span className="text-[11px] text-[#8d909b]">
          {message || 'No items or configuration found for this plugin on the target node.'}
        </span>
      </div>
      {onRetry && (
        <button
          type="button"
          onClick={onRetry}
          className="flex items-center gap-1.5 px-3 py-1 rounded bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] text-xs font-semibold transition-colors border border-[#434750]/40 cursor-pointer"
        >
          <span className="material-symbols-outlined text-[14px]">refresh</span>
          <span>Refresh</span>
        </button>
      )}
    </div>
  );
};
