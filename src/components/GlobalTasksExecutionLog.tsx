import React, { useState } from 'react';
import type { GlobalTaskItem } from '../types/index.ts';

interface GlobalTasksExecutionLogProps {
  tasks: GlobalTaskItem[];
  onOpenTaskDetails?: (taskId: string) => void;
}

export const GlobalTasksExecutionLog: React.FC<GlobalTasksExecutionLogProps> = ({
  tasks,
  onOpenTaskDetails,
}) => {
  const [activeTab, setActiveTab] = useState<'active' | 'ansible' | 'syslog' | 'vault'>('active');
  const [isExpanded, setIsExpanded] = useState(false);
  const [isCollapsed, setIsCollapsed] = useState(false);

  const activeCount = tasks.filter(t => t.status === 'RUNNING').length || 4;

  const ansibleLogs = [
    'PLAY [Setup Homelab Cluster Services & Podman Engine] ****************************************',
    'TASK [Gathering Facts] **********************************************************************',
    'ok: [node-150-openmediavault]',
    'ok: [node-153-llama-inference]',
    'ok: [node-160-ingress-traefik]',
    'TASK [podman : Verify rootless linger configuration] ****************************************',
    'changed: [node-150-openmediavault] => {"changed": true, "msg": "loginctl enable-linger podman"}',
    'TASK [restic : Ensure immutable gold retention policy] **************************************',
    'ok: [node-150-openmediavault] => {"mode": "immutable", "retention": "7d/4w/12m"}',
    'TASK [tailscale : Verify mesh routing & exit advertisement] *********************************',
    'ok: [node-150-openmediavault] => {"advertised": "192.168.1.0/24", "status": "online"}',
    'PLAY RECAP **********************************************************************************',
    'node-150-openmediavault   : ok=14   changed=2    unreachable=0    failed=0',
  ];

  const syslogLogs = [
    'May 14 10:28:44 the-freezer systemd[1]: Started Cockpit Web Service Session ws-tunnel.',
    'May 14 10:28:40 the-freezer kernel: [59218.412] nvidia-gpu: PCIe BAR0 remap OK, CUDA 12.4 init.',
    'May 14 10:28:35 the-freezer restic[19482]: snapshot f7a91b2e saved to s3://restic-homelab-vault',
    'May 14 10:28:30 the-freezer podman[8412]: pod media-stack health check passed for 3 containers',
    'May 14 10:28:22 the-freezer tailscaled[1104]: mesh: peer cold-war latency 1.2ms (direct)',
  ];

  const vaultLogs = [
    '2025-05-14 10:24:50 [AUDIT] root@pam authenticated with GSSAPI ticket',
    '2025-05-14 10:24:50 [VAULT] Decrypted group_vars/all.yml using AES-256 cipher (Ansible Key ID: 0x8F)',
    '2025-05-14 10:20:12 [AUDIT] Token generated for cockpit-bridge session (ttl: 3600s)',
  ];

  return (
    <section
      className={`fixed bottom-0 left-0 right-0 bg-[#0a0d1f] z-50 flex flex-col shadow-[0_-2px_10px_rgba(0,0,0,0.6)] border-t border-[#26293c] transition-all duration-300 ${
        isCollapsed ? 'h-7' : isExpanded ? 'h-96' : 'h-44'
      }`}
    >
      {/* Top drawer control bar */}
      <div className="h-7 bg-[#171a2d] px-3 flex items-center justify-between border-b border-[#26293c]">
        <div className="flex items-center gap-2 h-full overflow-x-auto">
          <span className="font-mono text-xs text-[#dfe1fa] font-bold uppercase tracking-wider flex items-center gap-1 shrink-0">
            <span className="material-symbols-outlined text-[14px] text-[#b0c9ff]">terminal</span>
            <span className="hidden md:inline">Global Tasks &amp; Execution Log</span>
            <span className="md:hidden">Tasks</span>
          </span>

          <div className="flex items-center h-full">
            <button
              type="button"
              onClick={() => {
                setActiveTab('active');
                setIsCollapsed(false);
              }}
              className={`px-2 h-full font-bold font-mono text-xs transition-colors cursor-pointer ${
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
                setActiveTab('ansible');
                setIsCollapsed(false);
              }}
              className={`px-2 h-full font-mono text-xs transition-colors cursor-pointer ${
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
              className={`px-2 h-full font-mono text-xs transition-colors cursor-pointer ${
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
              className={`px-2 h-full font-mono text-xs transition-colors cursor-pointer ${
                activeTab === 'vault'
                  ? 'text-[#b0c9ff] bg-[#313447]'
                  : 'text-[#c3c6d2] hover:text-[#dfe1fa]'
              }`}
            >
              Vault Audit
            </button>
          </div>
        </div>

        <div className="flex items-center gap-2 text-[#c3c6d2]">
          <span className="font-mono text-xs text-[#8cd6cb] flex items-center gap-1">
            <span className="w-1.5 h-1.5 rounded-full bg-[#8cd6cb] animate-ping"></span>
            Autorefresh 3s
          </span>

          {/* Expand / Minimize buttons */}
          <button
            type="button"
            onClick={() => setIsExpanded(!isExpanded)}
            title={isExpanded ? 'Restore size' : 'Expand log view'}
            className="material-symbols-outlined text-[16px] text-[#8d909b] hover:text-[#dfe1fa] p-0.5 cursor-pointer"
          >
            {isExpanded ? 'close_fullscreen' : 'open_in_full'}
          </button>
          <button
            type="button"
            onClick={() => setIsCollapsed(!isCollapsed)}
            title={isCollapsed ? 'Open log panel' : 'Collapse log panel'}
            className="material-symbols-outlined text-[16px] text-[#8d909b] hover:text-[#dfe1fa] p-0.5 cursor-pointer"
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
                {tasks.map(task => (
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
                      ) : (
                        <span className="px-1.5 py-0.5 bg-[#8cd6cb]/20 text-[#8cd6cb] font-bold rounded text-[10px] border border-[#8cd6cb]/30">
                          OK
                        </span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}

          {activeTab === 'ansible' && (
            <div className="p-3 font-mono text-xs space-y-1 text-[#dfe1fa] bg-[#0a0d1f]">
              {ansibleLogs.map((line, idx) => (
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
              ))}
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
