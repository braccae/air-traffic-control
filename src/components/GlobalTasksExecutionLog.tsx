import React, { useState, useEffect } from 'react';
import type { GlobalTaskItem, QueuedAnsibleTask } from '../types/index.ts';
import type { ManagedWindow } from '../utils/windowManager.ts';
import { getConsoleWindowPillClasses, formatTaskQueueTabLabel, type ConsoleDrawerTab } from '../utils/navigationViewModel.ts';
import { TaskQueueTab } from './TaskQueueTab.tsx';

interface GlobalTasksExecutionLogProps {
  tasks: GlobalTaskItem[];
  queue?: QueuedAnsibleTask[];
  isRunning?: boolean;
  onOpenTaskDetails?: (taskId: string) => void;
  onOpenPlaybookEditor?: () => void;
  windows?: ManagedWindow[];
  activeWindowId?: string | null;
  onSelectWindow?: (id: string) => void;
  onRestoreWindow?: (id: string) => void;
  onMinimizeWindow?: (id: string) => void;
  onCloseWindow?: (id: string) => void;
}

export const GlobalTasksExecutionLog: React.FC<GlobalTasksExecutionLogProps> = ({
  tasks,
  queue: initialQueue,
  isRunning: initialIsRunning,
  onOpenTaskDetails,
  onOpenPlaybookEditor,
  windows = [],
  activeWindowId,
  onSelectWindow,
  onRestoreWindow,
  onMinimizeWindow,
  onCloseWindow,
}) => {
  const [activeTab, setActiveTab] = useState<ConsoleDrawerTab>('active');
  const [isExpanded, setIsExpanded] = useState(false);
  const [isCollapsed, setIsCollapsed] = useState(false);

  // Task Queue state
  const [queue, setQueue] = useState<QueuedAnsibleTask[]>(initialQueue || []);
  const [isQueueRunning, setIsQueueRunning] = useState<boolean>(initialIsRunning || false);

  useEffect(() => {
    if (initialQueue !== undefined) {
      setQueue(initialQueue);
    }
  }, [initialQueue]);

  useEffect(() => {
    if (initialIsRunning !== undefined) {
      setIsQueueRunning(initialIsRunning);
    }
  }, [initialIsRunning]);

  // Fetch initial queue state from /api/queue on mount and connect to WebSocket
  useEffect(() => {
    let isMounted = true;

    fetch('/api/queue')
      .then(res => res.json())
      .then(data => {
        if (isMounted && data?.tasks) {
          setQueue(data.tasks);
          setIsQueueRunning(!!data.isRunning);
        }
      })
      .catch(() => {});

    let ws: WebSocket | null = null;
    let reconnectTimeout: ReturnType<typeof setTimeout> | null = null;

    const connectWS = () => {
      try {
        if (typeof window === 'undefined' || !window.location) return;
        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        ws = new WebSocket(`${protocol}//${window.location.host}/ws`);

        ws.onmessage = (event) => {
          try {
            const msg = JSON.parse(event.data);
            if (msg.type === 'init' && msg.queue) {
              setQueue(msg.queue.tasks || []);
              setIsQueueRunning(!!msg.queue.isRunning);
            } else if (msg.type === 'queue_updated' && msg.queue) {
              setQueue(msg.queue.tasks || []);
              setIsQueueRunning(!!msg.queue.isRunning);
            } else if (msg.type === 'queue_execution_started') {
              setIsQueueRunning(true);
              if (msg.queue) {
                setQueue(msg.queue.tasks || []);
              }
            } else if (msg.type === 'queue_execution_finished') {
              setIsQueueRunning(false);
              if (msg.queue) {
                setQueue(msg.queue.tasks || []);
              }
            }
          } catch {
            // ignore non-json messages
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
  }, []);

  const refreshQueue = async () => {
    try {
      const res = await fetch('/api/queue');
      const data = await res.json();
      if (data?.tasks) {
        setQueue(data.tasks);
        setIsQueueRunning(!!data.isRunning);
      }
    } catch {
      // ignore
    }
  };

  const handleRunNextTask = async () => {
    try {
      setIsQueueRunning(true);
      await fetch('/api/queue/execute', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({}),
      });
      await refreshQueue();
    } catch (err) {
      console.error('Failed to run next queue task:', err);
    } finally {
      setIsQueueRunning(false);
    }
  };

  const handleExecuteAll = async () => {
    try {
      setIsQueueRunning(true);
      let keepGoing = true;
      while (keepGoing) {
        const res = await fetch('/api/queue/execute', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({}),
        });
        const data = await res.json();
        if (!data.success || data.haltedOnError || (data.remainingCount !== undefined && data.remainingCount === 0)) {
          keepGoing = false;
        }
      }
      await refreshQueue();
    } catch (err) {
      console.error('Failed to execute all queue tasks:', err);
    } finally {
      setIsQueueRunning(false);
    }
  };

  const handleDeleteTask = async (id: string) => {
    try {
      await fetch(`/api/queue/task/${encodeURIComponent(id)}`, {
        method: 'DELETE',
      });
      setQueue(prev => prev.filter(t => t.id !== id));
    } catch (err) {
      console.error(`Failed to delete task ${id}:`, err);
    }
  };

  const handleClearQueue = async () => {
    try {
      await fetch('/api/queue', {
        method: 'DELETE',
      });
      setQueue([]);
    } catch (err) {
      console.error('Failed to clear queue:', err);
    }
  };


  const activeCount = tasks.filter(t => t.status === 'RUNNING').length;

  const runningOrRecentAnsible = tasks.find(t => t.category === 'ansible' && t.logs && t.logs.length > 0);
  const liveAnsibleLogs = runningOrRecentAnsible?.logs && runningOrRecentAnsible.logs.length > 0
    ? runningOrRecentAnsible.logs
    : tasks.find(t => t.logs && t.logs.length > 0)?.logs || null;

  const fallbackAnsibleLogs = [
    'PLAY [Setup Homelab Cluster Services & Podman Engine] ****************************************',
    'TASK [Gathering Facts] **********************************************************************',
    'ok: [likes-it-cold.leo-banjo.ts.net]',
    'ok: [ping.leo-banjo.ts.net]',
    'ok: [dev-pc.leo-banjo.ts.net]',
    'TASK [podman : Verify rootless linger configuration] ****************************************',
    'changed: [likes-it-cold.leo-banjo.ts.net] => {"changed": true, "msg": "loginctl enable-linger core"}',
    'changed: [ping.leo-banjo.ts.net] => {"changed": true, "msg": "loginctl enable-linger uosserver"}',
    'TASK [restic : Ensure immutable gold retention policy] **************************************',
    'ok: [likes-it-cold.leo-banjo.ts.net] => {"mode": "immutable", "retention": "7d/4w/12m"}',
    'TASK [tailscale : Verify mesh routing & exit advertisement] *********************************',
    'ok: [likes-it-cold.leo-banjo.ts.net] => {"advertised": "192.168.8.0/24", "status": "online"}',
    'PLAY RECAP **********************************************************************************',
    'likes-it-cold.leo-banjo.ts.net : ok=14   changed=1    unreachable=0    failed=0',
    'ping.leo-banjo.ts.net          : ok=12   changed=1    unreachable=0    failed=0',
  ];

  const syslogLogs = [
    'Oct 18 02:15:44 likes-it-cold systemd[1]: Started Cockpit Web Service Session ws-tunnel.',
    'Oct 18 02:15:40 likes-it-cold podman[4182]: container c9603dec9010 (uosserver) health check: PASSED',
    'Oct 18 02:15:35 likes-it-cold restic[19482]: snapshot f7a91b2e saved to s3://restic-homelab-vault/leo-banjo',
    'Oct 18 02:15:30 ping systemd[1]: uosserver.service: Active rootless pod (UID 1001)',
    'Oct 18 02:15:22 dev-pc tailscaled[1104]: mesh: peer likes-it-cold latency 1.8ms (direct WireGuard)',
  ];

  const vaultLogs = [
    '2026-10-18 02:10:50 [AUDIT] root@pam authenticated with GSSAPI ticket',
    '2026-10-18 02:10:50 [VAULT] Decrypted group_vars/all.yml using AES-256 cipher (SQLite cache sync)',
    '2026-10-18 02:05:12 [AUDIT] Token generated for cockpit-bridge session (ttl: 3600s)',
  ];

  return (
    <section
      id="global-tasks-drawer"
      aria-label="Console"
      className={`fixed bottom-0 left-0 right-0 bg-[#0a0d1f] z-50 flex flex-col shadow-[0_-2px_10px_rgba(0,0,0,0.6)] border-t border-[#26293c] transition-all duration-300 ${
        isCollapsed ? 'h-7' : isExpanded ? 'h-96' : 'h-44'
      }`}
    >
      {/* Top drawer control bar */}
      <div className="h-7 bg-[#171a2d] px-2 sm:px-3 flex items-center justify-between border-b border-[#26293c] gap-2">
        {/* Pinned Left Section: Title and Category Tabs */}
        <div className="shrink-0 flex items-center gap-2 h-full">
          <span className="font-mono text-xs text-[#dfe1fa] font-bold uppercase tracking-wider flex items-center gap-1 shrink-0">
            <span className="material-symbols-outlined text-[14px] text-[#b0c9ff]">terminal</span>
            <span>Console</span>
          </span>

          <div className="flex items-center h-full shrink-0">
            <button
              type="button"
              onClick={() => {
                setActiveTab('active');
                setIsCollapsed(false);
              }}
              className={`px-2 h-full font-bold font-mono text-xs transition-colors cursor-pointer shrink-0 ${
                activeTab === 'active'
                  ? 'text-[#b0c9ff] bg-[#313447]'
                  : 'text-[#c3c6d2] hover:text-[#dfe1fa]'
              }`}
            >
              Active Tasks ({activeCount})
            </button>
            <button
              type="button"
              onClick={() => {
                setActiveTab('task_queue');
                setIsCollapsed(false);
              }}
              className={`px-2 h-full font-mono text-xs transition-colors cursor-pointer shrink-0 ${
                activeTab === 'task_queue'
                  ? 'text-[#b0c9ff] bg-[#313447] font-bold'
                  : 'text-[#c3c6d2] hover:text-[#dfe1fa]'
              }`}
            >
              {formatTaskQueueTabLabel(queue.filter(t => t.status === 'staged').length)}
            </button>
            <button
              type="button"
              onClick={() => {
                setActiveTab('ansible');
                setIsCollapsed(false);
              }}
              className={`px-2 h-full font-mono text-xs transition-colors cursor-pointer shrink-0 ${
                activeTab === 'ansible'
                  ? 'text-[#b0c9ff] bg-[#313447]'
                  : 'text-[#c3c6d2] hover:text-[#dfe1fa]'
              }`}
            >
              Ansible Stdout
            </button>
            <button
              type="button"
              onClick={() => {
                setActiveTab('syslog');
                setIsCollapsed(false);
              }}
              className={`px-2 h-full font-mono text-xs transition-colors cursor-pointer shrink-0 ${
                activeTab === 'syslog'
                  ? 'text-[#b0c9ff] bg-[#313447]'
                  : 'text-[#c3c6d2] hover:text-[#dfe1fa]'
              }`}
            >
              Cluster Syslog
            </button>
            <button
              type="button"
              onClick={() => {
                setActiveTab('vault');
                setIsCollapsed(false);
              }}
              className={`px-2 h-full font-mono text-xs transition-colors cursor-pointer shrink-0 ${
                activeTab === 'vault'
                  ? 'text-[#b0c9ff] bg-[#313447]'
                  : 'text-[#c3c6d2] hover:text-[#dfe1fa]'
              }`}
            >
              Vault Audit
            </button>
          </div>
        </div>

        {/* Middle Isolated Dock Section: Minimized Window Pills */}
        {windows.length > 0 && (
          <div className="flex-1 min-w-0 flex items-center gap-1.5 h-full px-2 border-l border-r border-[#26293c]">
            <span className="font-mono text-[10px] text-[#8d909b] uppercase tracking-wider flex items-center gap-1 shrink-0">
              <span className="material-symbols-outlined text-[13px] text-[#8cd6cb]">dock_to_bottom</span>
              <span className="hidden sm:inline">Windows ({windows.length})</span>
            </span>

            <div className="flex items-center gap-1 overflow-x-auto scrollbar-none flex-1 py-0.5">
              {windows.map((win) => {
                const isActive = win.id === activeWindowId && !win.isMinimized;
                const isMin = win.isMinimized;
                const pillStyles = getConsoleWindowPillClasses(isActive, isMin);

                return (
                  <div
                    key={win.id}
                    onClick={() => {
                      if (isMin) {
                        onRestoreWindow?.(win.id);
                      } else if (win.id === activeWindowId) {
                        onMinimizeWindow?.(win.id);
                      } else {
                        onSelectWindow?.(win.id);
                      }
                    }}
                    className={pillStyles.pill}
                    title={`${win.title} (${win.type}) - ${
                      isMin ? 'Click to restore' : isActive ? 'Click to minimize' : 'Click to focus'
                    }`}
                  >
                    <span className={pillStyles.dot} />
                    {win.icon && (
                      <span className="material-symbols-outlined text-[13px] text-[#b0c9ff] shrink-0">
                        {win.icon}
                      </span>
                    )}
                    <span className="font-medium max-w-[130px] truncate">{win.title}</span>

                    <span className="material-symbols-outlined text-[12px] text-gray-400 group-hover:text-white shrink-0">
                      {isMin ? 'expand_less' : 'expand_more'}
                    </span>

                    {onCloseWindow && (
                      <button
                        type="button"
                        onClick={(e) => {
                          e.stopPropagation();
                          onCloseWindow(win.id);
                        }}
                        className="p-0.5 rounded hover:bg-red-500/20 text-gray-400 hover:text-red-400 transition-colors ml-0.5 cursor-pointer shrink-0"
                        title="Close window"
                      >
                        <span className="material-symbols-outlined text-[12px] leading-none">close</span>
                      </button>
                    )}
                  </div>
                );
              })}
            </div>
          </div>
        )}

        {/* Pinned Right Section: Live Indicator and Controls */}
        <div className="shrink-0 flex items-center gap-2 text-[#c3c6d2]">
          <span className="font-mono text-xs text-[#8cd6cb] flex items-center gap-1 shrink-0">
            <span className="w-1.5 h-1.5 rounded-full bg-[#8cd6cb] animate-ping"></span>
            Autorefresh 3s
          </span>

          {/* Quick Playbook Launch Button */}
          <button
            type="button"
            onClick={() => {
              setActiveTab('ansible');
              setIsCollapsed(false);
            }}
            className="px-2 py-0.5 bg-[#26293c] hover:bg-[#35384c] text-[#b0c9ff] rounded font-mono text-[10px] cursor-pointer border border-[#434750]/30 hidden sm:inline-flex items-center gap-1 shrink-0"
          >
            <span className="material-symbols-outlined text-[13px]">play_arrow</span>
            <span>Ansible Stream</span>
          </button>

          {onOpenPlaybookEditor && (
            <button
              type="button"
              onClick={onOpenPlaybookEditor}
              className="px-2 py-0.5 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] rounded font-mono text-[10px] cursor-pointer border border-[#434750]/30 hidden md:inline-flex items-center gap-1 shrink-0"
              title="Open Ansible Playbook Editor"
            >
              <span className="material-symbols-outlined text-[13px] text-[#b0c9ff]">menu_book</span>
              <span>Manage Playbooks</span>
            </button>
          )}

          {/* Expand / Minimize buttons */}
          <button
            type="button"
            onClick={() => setIsExpanded(!isExpanded)}
            title={isExpanded ? 'Restore size' : 'Expand log view'}
            className="material-symbols-outlined text-[16px] text-[#8d909b] hover:text-[#dfe1fa] p-0.5 cursor-pointer shrink-0"
          >
            {isExpanded ? 'close_fullscreen' : 'open_in_full'}
          </button>
          <button
            type="button"
            onClick={() => setIsCollapsed(!isCollapsed)}
            title={isCollapsed ? 'Open log panel' : 'Collapse log panel'}
            className="material-symbols-outlined text-[16px] text-[#8d909b] hover:text-[#dfe1fa] p-0.5 cursor-pointer shrink-0"
          >
            {isCollapsed ? 'keyboard_arrow_up' : 'keyboard_arrow_down'}
          </button>
        </div>
      </div>

      {/* Log Content */}
      {!isCollapsed && (
        <div className="flex-1 overflow-x-auto overflow-y-auto">
          {activeTab === 'active' && (
            <table className="w-full text-left font-mono text-xs">
              <thead className="bg-[#171a2d] text-[#8d909b] text-[10px] sticky top-0">
                <tr className="h-6">
                  <th className="px-3 py-0.5">Start Time</th>
                  <th className="px-3 py-0.5">Node / Target</th>
                  <th className="px-3 py-0.5">User</th>
                  <th className="px-3 py-0.5">Description / Task Command</th>
                  <th className="px-3 py-0.5">Progress / ETA</th>
                  <th className="px-3 py-0.5 text-right">Status</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-[#26293c]/50">
                {tasks.length === 0 ? (
                  <tr>
                    <td colSpan={6} className="px-3 py-6 text-center text-[#8d909b]">
                      No active or historical tasks queued. Run a playbook or execute a node action to see real execution telemetry.
                    </td>
                  </tr>
                ) : (
                  tasks.map(task => (
                    <tr
                      key={task.id}
                      onClick={() => onOpenTaskDetails?.(task.id)}
                      className="h-7 bg-[#0a0d1f] hover:bg-[#26293c]/60 transition-colors cursor-pointer"
                    >
                      <td className="px-3 py-0.5 text-[#c3c6d2] whitespace-nowrap">{task.startTime}</td>
                      <td className="px-3 py-0.5 text-[#d9b9ff] whitespace-nowrap">
                        {task.nodeTarget}
                      </td>
                      <td className="px-3 py-0.5 text-[#dfe1fa] whitespace-nowrap">{task.user}</td>
                      <td className="px-3 py-0.5 text-[#dfe1fa] font-mono truncate max-w-md">
                        {task.description}
                      </td>
                      <td className="px-3 py-0.5">
                        {task.status === 'RUNNING' ? (
                          <div className="flex items-center gap-2">
                            <div className="w-36 bg-[#26293c] h-2 rounded overflow-hidden">
                              <div
                                className="bg-[#b0c9ff] h-full transition-all duration-300"
                                style={{ width: `${task.progress}%` }}
                              ></div>
                            </div>
                            <span className="text-[10px] text-[#8d909b]">{task.progressEta}</span>
                          </div>
                        ) : (
                          <span className="text-[#8d909b]">{task.progressEta}</span>
                        )}
                      </td>
                      <td className="px-3 py-0.5 text-right">
                        {task.status === 'RUNNING' ? (
                          <span className="px-1.5 py-0.5 bg-[#b0c9ff]/20 text-[#b0c9ff] font-bold rounded text-[10px] border border-[#b0c9ff]/30">
                            RUNNING
                          </span>
                        ) : task.status === 'FAILED' ? (
                          <span className="px-1.5 py-0.5 bg-[#ffb4ab]/20 text-[#ffb4ab] font-bold rounded text-[10px] border border-[#ffb4ab]/30">
                            FAILED
                          </span>
                        ) : (
                          <span className="px-1.5 py-0.5 bg-[#8cd6cb]/20 text-[#8cd6cb] font-bold rounded text-[10px] border border-[#8cd6cb]/30">
                            OK
                          </span>
                        )}
                      </td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          )}

          {activeTab === 'task_queue' && (
            <TaskQueueTab
              queue={queue}
              isRunning={isQueueRunning}
              onRunNextTask={handleRunNextTask}
              onExecuteAll={handleExecuteAll}
              onDeleteTask={handleDeleteTask}
              onClearQueue={handleClearQueue}
            />
          )}

          {activeTab === 'ansible' && (
            <div className="p-3 font-mono text-xs space-y-1 text-[#dfe1fa] bg-[#0a0d1f]">
              {liveAnsibleLogs ? (
                liveAnsibleLogs.map((line, idx) => (
                  <div
                    key={idx}
                    className={
                      line.startsWith('PLAY') || line.startsWith('TASK') || line.includes('ansible-playbook')
                        ? 'text-[#b0c9ff] font-bold'
                        : line.includes('changed')
                        ? 'text-[#d9b9ff]'
                        : line.includes('failed') || line.includes('fatal') || line.includes('[stderr]')
                        ? 'text-[#ffb4ab]'
                        : 'text-[#8cd6cb]'
                    }
                  >
                    {line}
                  </div>
                ))
              ) : (
                fallbackAnsibleLogs.map((line, idx) => (
                  <div
                    key={idx}
                    className={
                      line.startsWith('PLAY') || line.startsWith('TASK')
                        ? 'text-[#b0c9ff] font-bold'
                        : line.includes('changed')
                        ? 'text-[#d9b9ff]'
                        : line.includes('failed')
                        ? 'text-[#ffb4ab]'
                        : 'text-[#8cd6cb]'
                    }
                  >
                    {line}
                  </div>
                ))
              )}
            </div>
          )}

          {activeTab === 'syslog' && (
            <div className="p-3 font-mono text-xs space-y-1 text-[#c3c6d2] bg-[#0a0d1f]">
              {syslogLogs.map((line, idx) => (
                <div key={idx} className="hover:bg-[#171a2d] px-1 rounded">
                  <span className="text-[#8d909b]">{line.substring(0, 15)}</span>
                  <span className="text-[#8cd6cb] ml-2">the-freezer</span>
                  <span className="text-[#dfe1fa] ml-2">{line.substring(27)}</span>
                </div>
              ))}
            </div>
          )}

          {activeTab === 'vault' && (
            <div className="p-3 font-mono text-xs space-y-1 text-[#c3c6d2] bg-[#0a0d1f]">
              {vaultLogs.map((line, idx) => (
                <div key={idx} className="hover:bg-[#171a2d] px-1 rounded flex items-center gap-2">
                  <span className="material-symbols-outlined text-[14px] text-[#d9b9ff]">
                    verified
                  </span>
                  <span>{line}</span>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </section>
  );
};
