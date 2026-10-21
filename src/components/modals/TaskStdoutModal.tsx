import React, { useEffect, useState, useRef } from 'react';
import type { GlobalTaskItem } from '../../types/index.ts';

export interface TaskStdoutModalProps {
  taskId: string | null;
  task?: GlobalTaskItem | null;
  isOpen: boolean;
  isMinimized?: boolean;
  onMinimize?: () => void;
  onClose: () => void;
}

export const TaskStdoutModal: React.FC<TaskStdoutModalProps> = ({
  taskId,
  task: propTask,
  isOpen,
  isMinimized = false,
  onMinimize,
  onClose,
}) => {
  const [fetchedTask, setFetchedTask] = useState<GlobalTaskItem | null>(null);
  const [liveLines, setLiveLines] = useState<string[]>(() =>
    propTask?.logs && Array.isArray(propTask.logs) ? propTask.logs : []
  );
  const [filter, setFilter] = useState('');
  const [autoScroll, setAutoScroll] = useState(true);
  const logContainerRef = useRef<HTMLDivElement>(null);

  const currentTask = propTask || fetchedTask;

  // Fetch initial task state (and logs) when opened
  useEffect(() => {
    if (!isOpen || !taskId) {
      setFetchedTask(null);
      setLiveLines([]);
      return;
    }

    if (propTask?.logs && Array.isArray(propTask.logs)) {
      setLiveLines(propTask.logs);
    }

    fetch(`/api/tasks/${encodeURIComponent(taskId)}`)
      .then(res => (res.ok ? res.json() : null))
      .then((data: GlobalTaskItem | null) => {
        if (data && data.id === taskId) {
          setFetchedTask(data);
          if (Array.isArray(data.logs) && data.logs.length > 0) {
            setLiveLines(prev => (data.logs!.length >= prev.length ? data.logs! : prev));
          }
        }
      })
      .catch(() => {});
  }, [isOpen, taskId]);

  // Keep synced with propTask updates from App's WebSocket stream
  useEffect(() => {
    if (propTask && propTask.id === taskId && Array.isArray(propTask.logs)) {
      setLiveLines(prev => (propTask.logs!.length >= prev.length ? propTask.logs! : prev));
    }
  }, [propTask, taskId]);

  // Subscribe to WebSocket for incremental task_output & task_updated events
  useEffect(() => {
    if (!isOpen || !taskId) return;
    let isMounted = true;
    let ws: WebSocket | null = null;
    let reconnectTimeout: ReturnType<typeof setTimeout> | null = null;

    const connectWS = () => {
      try {
        if (typeof window === 'undefined' || !window.location) return;
        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        ws = new WebSocket(`${protocol}//${window.location.host}/ws`);

        ws.onmessage = event => {
          if (!isMounted) return;
          try {
            const msg = JSON.parse(event.data);
            if (msg.type === 'task_output' && msg.taskId === taskId && typeof msg.line === 'string') {
              setLiveLines(prev => [...prev, msg.line]);
            } else if (msg.type === 'task_updated' && msg.task && msg.task.id === taskId) {
              setFetchedTask(msg.task);
              if (Array.isArray(msg.task.logs) && msg.task.logs.length > 0) {
                setLiveLines(prev => (msg.task.logs.length >= prev.length ? msg.task.logs : prev));
              }
            } else if (msg.type === 'playbook_log' && msg.taskId === taskId && typeof msg.output === 'string') {
              const split = msg.output
                .split('\n')
                .filter((l: string) => l.trim() !== '');
              if (split.length > 0) {
                setLiveLines(prev => (split.length >= prev.length ? split : prev));
              }
            }
          } catch {
            // ignore non-json frames
          }
        };

        ws.onclose = () => {
          if (isMounted) {
            reconnectTimeout = setTimeout(connectWS, 3000);
          }
        };
      } catch {
        if (isMounted) {
          reconnectTimeout = setTimeout(connectWS, 3000);
        }
      }
    };

    connectWS();
    return () => {
      isMounted = false;
      if (ws) ws.close();
      if (reconnectTimeout) clearTimeout(reconnectTimeout);
    };
  }, [isOpen, taskId]);

  useEffect(() => {
    if (autoScroll && logContainerRef.current) {
      logContainerRef.current.scrollTop = logContainerRef.current.scrollHeight;
    }
  }, [liveLines, autoScroll]);

  if (!isOpen || !taskId) return null;

  const status = currentTask?.status || 'RUNNING';
  const filteredLines = liveLines.filter(
    l => !filter.trim() || l.toLowerCase().includes(filter.toLowerCase())
  );

  return (
    <div
      data-testid="task-stdout-modal"
      style={{ display: isMinimized ? 'none' : 'flex' }}
      className="fixed inset-0 z-50 items-center justify-center bg-black/70 backdrop-blur-xs p-4"
    >
      <div className="w-full max-w-4xl bg-[#0a0d1f] rounded-lg shadow-2xl border border-[#313447] flex flex-col h-[560px] overflow-hidden">
        {/* Header */}
        <div className="h-9 bg-[#171a2d] px-3 flex items-center justify-between border-b border-[#26293c]">
          <div className="flex items-center gap-2 min-w-0">
            <span className="material-symbols-outlined text-[#b0c9ff] text-[18px]">terminal</span>
            <span className="font-mono text-xs text-[#dfe1fa] font-bold truncate">
              Task Stdout: {currentTask?.description || taskId}
            </span>
            {status === 'RUNNING' ? (
              <span
                data-testid="task-stdout-status-running"
                className="px-1.5 py-0.5 bg-[#b0c9ff]/20 text-[#b0c9ff] font-bold font-mono rounded text-[10px] border border-[#b0c9ff]/30 flex items-center gap-1 shrink-0"
              >
                <span className="w-1.5 h-1.5 rounded-full bg-[#b0c9ff] animate-ping" />
                LIVE STREAMING
              </span>
            ) : status === 'FAILED' ? (
              <span
                data-testid="task-stdout-status-failed"
                className="px-1.5 py-0.5 bg-[#ffb4ab]/20 text-[#ffb4ab] font-bold font-mono rounded text-[10px] border border-[#ffb4ab]/30 shrink-0"
              >
                FAILED
              </span>
            ) : (
              <span
                data-testid="task-stdout-status-ok"
                className="px-1.5 py-0.5 bg-[#8cd6cb]/20 text-[#8cd6cb] font-bold font-mono rounded text-[10px] border border-[#8cd6cb]/30 shrink-0"
              >
                COMPLETED
              </span>
            )}
          </div>
          <div className="flex items-center gap-1 shrink-0">
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
              data-testid="task-stdout-close"
              onClick={onClose}
              className="text-[#8d909b] hover:text-[#dfe1fa] p-1 cursor-pointer transition-colors"
              title="Close view (task continues running in background)"
            >
              <span className="material-symbols-outlined text-[16px]">close</span>
            </button>
          </div>
        </div>

        {/* Metadata & Filter Bar */}
        <div className="bg-[#121528] px-3 py-1.5 flex flex-wrap items-center justify-between gap-2 border-b border-[#26293c] font-mono text-xs">
          <div className="flex items-center gap-3 text-[11px] text-[#8d909b]">
            <span>
              Target: <span className="text-[#d9b9ff] font-bold">{currentTask?.nodeTarget || 'cluster'}</span>
            </span>
            <span>
              User: <span className="text-[#dfe1fa]">{currentTask?.user || 'core@ansible'}</span>
            </span>
            {currentTask?.progressEta && (
              <span>
                Status: <span className="text-[#8cd6cb]">{currentTask.progressEta}</span>
              </span>
            )}
          </div>

          <div className="flex items-center gap-2">
            <div className="flex items-center gap-1.5 bg-[#1b1e31] px-2 py-0.5 rounded border border-[#26293c]">
              <span className="material-symbols-outlined text-[14px] text-[#8d909b]">filter_alt</span>
              <input
                type="text"
                placeholder="Filter stdout..."
                value={filter}
                onChange={e => setFilter(e.target.value)}
                className="bg-transparent border-none outline-none text-[#dfe1fa] text-xs w-36"
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
              <span className="text-[#8d909b]">
                {filteredLines.length}/{liveLines.length} lines
              </span>
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
        </div>

        {/* Streaming Output Body */}
        <div
          ref={logContainerRef}
          data-testid="task-stdout-lines"
          className="flex-1 bg-[#0a0d1f] p-3 font-mono text-xs overflow-y-auto space-y-1 text-[#c3c6d2]"
        >
          {liveLines.length === 0 ? (
            <div className="text-[#8d909b] italic">
              {status === 'RUNNING'
                ? 'Waiting for subprocess stdout stream...'
                : 'No stdout lines captured for this task.'}
            </div>
          ) : filteredLines.length === 0 ? (
            <div className="text-[#8d909b] italic">No stdout lines match filter "{filter}"</div>
          ) : (
            filteredLines.map((line, idx) => (
              <div key={idx} className="flex gap-2 hover:bg-[#121528] px-1 rounded">
                <span className="text-[#8d909b] select-none text-[10px] shrink-0">
                  {String(idx + 1).padStart(3, '0')}
                </span>
                <span
                  className={`break-all whitespace-pre-wrap ${
                    line.startsWith('PLAY') || line.startsWith('TASK') || line.includes('[ansible]')
                      ? 'text-[#b0c9ff] font-bold'
                      : line.includes('failed') || line.includes('fatal') || line.includes('ERROR') || line.includes('ERR')
                      ? 'text-[#ffb4ab]'
                      : line.includes('changed') || line.includes('WARN')
                      ? 'text-[#ffd580]'
                      : 'text-[#dfe1fa]'
                  }`}
                >
                  {line}
                </span>
              </div>
            ))
          )}
        </div>

        {/* Footer */}
        <div className="h-8 bg-[#171a2d] px-3 flex items-center justify-between font-mono text-xs text-[#8d909b] border-t border-[#26293c]">
          <span>
            {status === 'RUNNING'
              ? 'Subprocess active — closing this window will not interrupt or terminate the task.'
              : `Subprocess finished (${status}) — captured output persisted in SQLite.`}
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
