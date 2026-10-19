import React, { useState } from 'react';
import type { QueuedAnsibleTask, AnsibleTaskStep } from '../types/index.ts';

export interface TaskQueueTabProps {
  queue: QueuedAnsibleTask[];
  isRunning: boolean;
  onRunNextTask: () => void;
  onExecuteAll: () => void;
  onDeleteTask: (id: string) => void;
  onClearQueue: () => void;
  defaultExpandedIds?: string[];
}

/**
 * Masks secrets or passwords in parameter keys and values.
 */
export function maskSensitiveValue(key: string, val: any, maskedSecrets?: Record<string, string>): string {
  const lowerKey = key.toLowerCase();
  const isSensitiveKey =
    lowerKey.includes('password') ||
    lowerKey.includes('secret') ||
    lowerKey.includes('token') ||
    lowerKey.includes('credential') ||
    lowerKey.includes('privkey') ||
    (maskedSecrets && key in maskedSecrets);

  if (isSensitiveKey) {
    return '••••••••';
  }

  if (typeof val === 'string') {
    if (maskedSecrets) {
      for (const secret of Object.values(maskedSecrets)) {
        if (secret && val.includes(secret)) {
          return '••••••••';
        }
      }
    }
    return val;
  }

  if (typeof val === 'object' && val !== null) {
    const sanitized: any = Array.isArray(val) ? [] : {};
    for (const [k, v] of Object.entries(val)) {
      sanitized[k] = maskSensitiveValue(k, v, maskedSecrets);
    }
    return JSON.stringify(sanitized);
  }

  return String(val);
}

export const TaskQueueTab: React.FC<TaskQueueTabProps> = ({
  queue,
  isRunning,
  onRunNextTask,
  onExecuteAll,
  onDeleteTask,
  onClearQueue,
  defaultExpandedIds = [],
}) => {
  const [expandedIds, setExpandedIds] = useState<Set<string>>(new Set(defaultExpandedIds));

  const stagedCount = queue.filter(t => t.status === 'staged').length;

  const toggleExpand = (taskId: string) => {
    setExpandedIds(prev => {
      const next = new Set(prev);
      if (next.has(taskId)) {
        next.delete(taskId);
      } else {
        next.add(taskId);
      }
      return next;
    });
  };

  return (
    <div className="flex flex-col h-full bg-[#0a0d1f] text-[#dfe1fa] font-mono text-xs">
      {/* Header Toolbar */}
      <div className="h-8 bg-[#171a2d] px-3 flex items-center justify-between border-b border-[#26293c] shrink-0 gap-2">
        <div className="flex items-center gap-2">
          <span className="font-bold text-xs text-[#b0c9ff] flex items-center gap-1.5">
            <span className="material-symbols-outlined text-[15px] text-[#8cd6cb]">queue</span>
            <span>{`Staged Tasks (${stagedCount})`}</span>
          </span>

          <span className="text-[11px] text-[#8d909b]">
            {`Total: ${queue.length}`}
          </span>

          {isRunning && (
            <span className="px-1.5 py-0.5 bg-[#b0c9ff]/20 text-[#b0c9ff] border border-[#b0c9ff]/30 rounded text-[10px] flex items-center gap-1 animate-pulse">
              <span className="w-1.5 h-1.5 rounded-full bg-[#b0c9ff] animate-ping" />
              RUNNING
            </span>
          )}
        </div>

        <div className="flex items-center gap-2">
          {/* Run Next Task Button */}
          <button
            type="button"
            onClick={onRunNextTask}
            disabled={isRunning || stagedCount === 0}
            className={`px-2.5 py-1 rounded text-[11px] font-bold flex items-center gap-1 transition-colors cursor-pointer border ${
              isRunning || stagedCount === 0
                ? 'bg-[#1e2238] text-[#8d909b] border-[#26293c] cursor-not-allowed opacity-50'
                : 'bg-[#26293c] hover:bg-[#35384c] text-[#8cd6cb] border-[#8cd6cb]/30 shadow-xs'
            }`}
            title="Execute the next staged Ansible task"
          >
            <span className="material-symbols-outlined text-[14px]">play_arrow</span>
            <span>Run Next Task</span>
          </button>

          {/* Execute All Queue Button */}
          <button
            type="button"
            onClick={onExecuteAll}
            disabled={isRunning || stagedCount === 0}
            className={`px-2.5 py-1 rounded text-[11px] font-bold flex items-center gap-1 transition-colors cursor-pointer border ${
              isRunning || stagedCount === 0
                ? 'bg-[#1e2238] text-[#8d909b] border-[#26293c] cursor-not-allowed opacity-50'
                : 'bg-[#1f6feb]/20 hover:bg-[#1f6feb]/35 text-[#58a6ff] border-[#58a6ff]/40 shadow-xs'
            }`}
            title="Execute all staged tasks in queue one-by-one"
          >
            <span className="material-symbols-outlined text-[14px]">fast_forward</span>
            <span>Execute Queue (One-by-One)</span>
          </button>

          {/* Clear Queue Button */}
          <button
            type="button"
            onClick={onClearQueue}
            disabled={isRunning || queue.length === 0}
            className={`px-2 py-1 rounded text-[11px] flex items-center gap-1 transition-colors cursor-pointer border ${
              isRunning || queue.length === 0
                ? 'bg-[#1e2238] text-[#8d909b] border-[#26293c] cursor-not-allowed opacity-50'
                : 'bg-[#171a2d] hover:bg-rose-500/20 text-[#8d909b] hover:text-rose-400 border-[#26293c] hover:border-rose-500/30'
            }`}
            title="Clear all tasks from the queue"
          >
            <span className="material-symbols-outlined text-[14px]">delete_sweep</span>
            <span>Clear Queue</span>
          </button>
        </div>
      </div>

      {/* Task List / Option A Accordion */}
      <div className="flex-1 overflow-y-auto p-2 space-y-1.5">
        {queue.length === 0 ? (
          <div className="p-6 text-center text-[#8d909b] flex flex-col items-center justify-center gap-2">
            <span className="material-symbols-outlined text-3xl text-[#434750]">playlist_add_check</span>
            <p className="max-w-md text-xs leading-relaxed">
              No tasks in the Ansible mutation queue. Perform actions like restarting services, editing firewall rules, or updating containers to stage mutations.
            </p>
          </div>
        ) : (
          queue.map(task => {
            const isExpanded = expandedIds.has(task.id);

            return (
              <div
                key={task.id}
                className="border border-[#26293c] rounded bg-[#101426] overflow-hidden transition-all duration-150"
              >
                {/* Collapsed Header Row */}
                <div
                  onClick={() => toggleExpand(task.id)}
                  className="px-3 py-2 flex items-center justify-between gap-2 cursor-pointer hover:bg-[#171a2d] transition-colors select-none"
                >
                  <div className="flex items-center gap-2 min-w-0">
                    {/* Accordion Chevron */}
                    <span className="material-symbols-outlined text-[16px] text-[#8d909b] shrink-0">
                      {isExpanded ? 'expand_less' : 'expand_more'}
                    </span>

                    {/* Status Pill */}
                    {task.status === 'staged' ? (
                      <span className="px-1.5 py-0.5 rounded text-[10px] font-bold bg-amber-500/20 text-amber-300 border border-amber-500/30 shrink-0">
                        STAGED
                      </span>
                    ) : task.status === 'running' ? (
                      <span className="px-1.5 py-0.5 rounded text-[10px] font-bold bg-blue-500/20 text-blue-300 border border-blue-500/30 animate-pulse shrink-0">
                        RUNNING
                      </span>
                    ) : task.status === 'completed' ? (
                      <span className="px-1.5 py-0.5 rounded text-[10px] font-bold bg-emerald-500/20 text-emerald-300 border border-emerald-500/30 shrink-0">
                        COMPLETED
                      </span>
                    ) : task.status === 'failed' ? (
                      <span className="px-1.5 py-0.5 rounded text-[10px] font-bold bg-rose-500/20 text-rose-300 border border-rose-500/30 shrink-0">
                        FAILED
                      </span>
                    ) : (
                      <span className="px-1.5 py-0.5 rounded text-[10px] font-bold bg-gray-500/20 text-gray-400 border border-gray-500/30 shrink-0">
                        CANCELLED
                      </span>
                    )}

                    {/* Target Node Badge */}
                    <span className="px-1.5 py-0.5 rounded text-[10px] bg-[#182030] text-[#b0c9ff] border border-[#b0c9ff]/20 font-bold shrink-0 flex items-center gap-1">
                      <span className="material-symbols-outlined text-[11px]">dns</span>
                      <span>{task.targetNodeName || task.targetNodeId}</span>
                    </span>

                    {/* Title */}
                    <span className="font-semibold text-xs text-[#dfe1fa] truncate">
                      {task.title}
                    </span>

                    {/* Primary Module Pill */}
                    <span className="px-1.5 py-0.5 rounded text-[10px] bg-[#1e2338] text-[#8cd6cb] border border-[#8cd6cb]/30 shrink-0">
                      {task.primaryModule}
                    </span>
                  </div>

                  <div className="flex items-center gap-2 shrink-0">
                    {/* Timestamp */}
                    <span className="text-[10px] text-[#8d909b] hidden sm:inline">
                      {new Date(task.createdAt).toLocaleTimeString()}
                    </span>

                    {/* Delete Task Button */}
                    <button
                      type="button"
                      onClick={(e) => {
                        e.stopPropagation();
                        onDeleteTask(task.id);
                      }}
                      disabled={isRunning && task.status === 'running'}
                      className="p-1 rounded text-[#8d909b] hover:text-rose-400 hover:bg-rose-500/20 transition-colors cursor-pointer"
                      title="Remove task from queue"
                    >
                      <span className="material-symbols-outlined text-[15px]">close</span>
                    </button>
                  </div>
                </div>

                {/* Option A Inline Accordion Expanded Card */}
                {isExpanded && (
                  <div className="px-4 py-3 border-t border-[#26293c] bg-[#0c1022] space-y-3">
                    {/* Checkpoint Failure Banner */}
                    {(task.status === 'failed' || task.error) && (
                      <div className="p-2.5 rounded bg-rose-950/40 border border-rose-500/40 text-rose-200 text-xs flex items-start gap-2">
                        <span className="material-symbols-outlined text-rose-400 text-[18px] shrink-0 mt-0.5">
                          error
                        </span>
                        <div className="flex-1 min-w-0">
                          <div className="font-bold text-rose-300">Execution Checkpoint Failed</div>
                          <div className="text-[11px] text-rose-200/90 font-mono mt-1 break-all whitespace-pre-wrap">
                            {task.error || 'Task execution halted with an unknown error.'}
                          </div>
                        </div>
                      </div>
                    )}

                    {/* Target Route & Connection Details */}
                    <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-4 gap-2 text-[11px]">
                      <div className="bg-[#14182e] p-2 rounded border border-[#26293c]">
                        <span className="text-[#8d909b] block text-[10px] uppercase">ansible_host</span>
                        <span className="text-[#b0c9ff] font-bold">{task.targetHostRoute}</span>
                      </div>

                      <div className="bg-[#14182e] p-2 rounded border border-[#26293c]">
                        <span className="text-[#8d909b] block text-[10px] uppercase">ansible_user</span>
                        <span className="text-[#8cd6cb] font-bold">{task.targetSshUser}</span>
                      </div>

                      <div className="bg-[#14182e] p-2 rounded border border-[#26293c]">
                        <span className="text-[#8d909b] block text-[10px] uppercase">ansible_port</span>
                        <span className="text-[#dfe1fa] font-bold">{task.targetSshPort}</span>
                      </div>

                      <div className="bg-[#14182e] p-2 rounded border border-[#26293c]">
                        <span className="text-[#8d909b] block text-[10px] uppercase">Privilege Elevation</span>
                        <span className="text-[#ffd580] font-bold">
                          {task.become ? `become: ${task.becomeMethod}` : 'none (unprivileged)'}
                        </span>
                      </div>
                    </div>

                    {/* Step-by-Step Breakdown Table */}
                    <div>
                      <div className="text-[11px] font-bold text-[#8d909b] uppercase tracking-wider mb-1.5 flex items-center gap-1">
                        <span className="material-symbols-outlined text-[13px] text-[#b0c9ff]">format_list_bulleted</span>
                        <span>Steps Breakdown ({task.steps.length})</span>
                      </div>

                      <div className="border border-[#26293c] rounded overflow-hidden">
                        <table className="w-full text-left text-[11px]">
                          <thead className="bg-[#171a2d] text-[#8d909b] text-[10px]">
                            <tr>
                              <th className="px-2.5 py-1">#</th>
                              <th className="px-2.5 py-1">Step Name</th>
                              <th className="px-2.5 py-1">Module</th>
                              <th className="px-2.5 py-1">Parameters</th>
                            </tr>
                          </thead>
                          <tbody className="divide-y divide-[#26293c]/50 bg-[#101426]">
                            {task.steps.map((step: AnsibleTaskStep, idx: number) => (
                              <tr key={idx} className="hover:bg-[#14182e]">
                                <td className="px-2.5 py-1.5 text-[#8d909b]">{idx + 1}</td>
                                <td className="px-2.5 py-1.5 text-[#dfe1fa] font-semibold">{step.name}</td>
                                <td className="px-2.5 py-1.5 text-[#8cd6cb]">
                                  <span className="px-1.5 py-0.5 rounded bg-[#1e2338] text-[10px] border border-[#8cd6cb]/30">
                                    {step.module}
                                  </span>
                                </td>
                                <td className="px-2.5 py-1.5 font-mono text-[10px]">
                                  <div className="space-y-0.5">
                                    {Object.entries(step.parameters || {}).map(([paramKey, paramVal]) => (
                                      <div key={paramKey} className="flex items-start gap-1.5">
                                        <span className="text-[#b0c9ff]">{paramKey}:</span>
                                        <span className="text-[#ffd580]">
                                          {maskSensitiveValue(paramKey, paramVal, task.secretsMasked)}
                                        </span>
                                      </div>
                                    ))}
                                  </div>
                                </td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                    </div>
                  </div>
                )}
              </div>
            );
          })
        )}
      </div>
    </div>
  );
};
