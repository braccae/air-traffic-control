import React, { useState, useEffect } from 'react';
import type { NodeItem, PrivilegeMethod, PrivilegeStatus } from '../types/index.ts';
import { formatPrivilegeBadge } from '../utils/privilegeEscalation.ts';

interface NodeBreadcrumbBarProps {
  node: NodeItem;
  onOpenTerminal: () => void;
  onTriggerRestic: () => void;
  onReboot: (mode: string) => void;
  onPowerOff: () => void;
  onEditNode?: () => void;
  onPrivilegeChange?: (status: PrivilegeStatus) => void;
}

export const NodeBreadcrumbBar: React.FC<NodeBreadcrumbBarProps> = ({
  node,
  onOpenTerminal,
  onTriggerRestic,
  onReboot,
  onPowerOff,
  onEditNode,
  onPrivilegeChange,
}) => {
  const [rebootDropdownOpen, setRebootDropdownOpen] = useState(false);
  const [privilegeDropdownOpen, setPrivilegeDropdownOpen] = useState(false);
  const [testingElevation, setTestingElevation] = useState(false);
  const [elevationFeedback, setElevationFeedback] = useState<string | null>(null);

  const [privilege, setPrivilege] = useState<PrivilegeStatus>(() => ({
    method: node.privilegeMethod || (node.sshUser === 'root' ? 'none' : 'run0'),
    active: node.privilegeActive ?? (node.sshUser === 'root'),
    user: node.sshUser || 'core',
    canElevate: true,
  }));

  // Fetch live privilege detection when node changes
  useEffect(() => {
    let cancelled = false;
    setPrivilege({
      method: node.privilegeMethod || (node.sshUser === 'root' ? 'none' : 'run0'),
      active: node.privilegeActive ?? (node.sshUser === 'root'),
      user: node.sshUser || 'core',
      canElevate: true,
    });
    setElevationFeedback(null);

    fetch(`/api/nodes/${node.id}/privilege`)
      .then((res) => res.json())
      .then((data: PrivilegeStatus) => {
        if (!cancelled && data && data.method) {
          setPrivilege(data);
          onPrivilegeChange?.(data);
        }
      })
      .catch(() => {
        // Fall back gracefully to node defaults
      });

    return () => {
      cancelled = true;
    };
  }, [node.id, node.privilegeMethod, node.privilegeActive, node.sshUser, onPrivilegeChange]);

  const handleTestElevation = async (method?: PrivilegeMethod) => {
    setTestingElevation(true);
    setElevationFeedback(null);
    try {
      const res = await fetch(`/api/nodes/${node.id}/privilege/test`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ method }),
      });
      const data = await res.json();
      if (data.success) {
        const updated: PrivilegeStatus = {
          method: data.method || method || privilege.method,
          active: true,
          user: node.sshUser || 'core',
          canElevate: true,
        };
        setPrivilege(updated);
        onPrivilegeChange?.(updated);
        setElevationFeedback(`Elevation active via ${data.method} (UID 0 verified)`);
      } else {
        setElevationFeedback(data.error || 'Elevation test failed (requires authorization)');
      }
    } catch (err: any) {
      setElevationFeedback(err?.message || 'Failed to reach node');
    } finally {
      setTestingElevation(false);
    }
  };

  const handleProbePrivilege = async () => {
    setTestingElevation(true);
    setElevationFeedback(null);
    try {
      const res = await fetch(`/api/nodes/${node.id}/privilege`);
      const data = await res.json();
      if (data && data.method) {
        setPrivilege(data);
        onPrivilegeChange?.(data);
        setElevationFeedback(`Detected: ${data.method} (${data.active ? 'Active' : 'Inactive'})`);
      }
    } catch (err: any) {
      setElevationFeedback(err?.message || 'Probe failed');
    } finally {
      setTestingElevation(false);
    }
  };

  const badgeInfo = formatPrivilegeBadge(privilege);

  return (
    <div className="flex flex-wrap items-center justify-between gap-2 px-3 py-1.5 bg-[#1b1e31] border-b border-[#26293c]">
      {/* Node Path and Badges */}
      <div className="flex items-center gap-2 min-w-0">
        <span className="material-symbols-outlined text-[#b0c9ff] text-[18px]">dns</span>
        <div className="flex items-center gap-1 font-mono text-xs truncate">
          <span className="text-[#8d909b]">{node.datacenter || 'leo-banjo.ts.net'}</span>
          <span className="text-[#434750]">/</span>
          <span className="text-[#d9b9ff] font-medium">{node.group}</span>
          <span className="text-[#434750]">/</span>
          <span className="text-[#dfe1fa] font-bold text-[#b0c9ff]">{node.name}</span>
          <span className="px-1.5 py-0.5 bg-[#313447] text-[#8cd6cb] font-mono text-[10px] rounded ml-1 border border-[#434750]/40">
            {node.osDescription}
          </span>
        </div>

        {/* Tailscale Badge with pulse */}
        <div className="flex items-center gap-1 px-2 py-0.5 bg-[#313447] rounded text-[#8cd6cb] font-mono text-[11px] border border-[#434750]/50">
          <span className="w-1.5 h-1.5 rounded-full bg-[#8cd6cb] animate-pulse"></span>
          <span>
            {node.sshUser || 'core'}@{node.tailscaleIp}
          </span>
        </div>

        {/* Privilege Elevation Pill */}
        <div className="relative inline-block text-left">
          <button
            type="button"
            id="privilege-pill-btn"
            onClick={() => setPrivilegeDropdownOpen(!privilegeDropdownOpen)}
            className={`flex items-center gap-1 px-2 py-0.5 rounded font-mono text-[11px] border transition-colors cursor-pointer ${badgeInfo.badgeColor} hover:brightness-125`}
            title={badgeInfo.description}
          >
            <span className="material-symbols-outlined text-[13px]">{badgeInfo.icon}</span>
            <span className="font-medium">{badgeInfo.label}</span>
            <span className="material-symbols-outlined text-[13px] opacity-70">arrow_drop_down</span>
          </button>

          {privilegeDropdownOpen && (
            <div className="absolute left-0 mt-1 w-64 bg-[#0a0d1f] rounded shadow-xl border border-[#313447] z-50 p-2 font-mono text-xs text-[#dfe1fa]">
              <div className="flex items-center justify-between pb-1.5 mb-1.5 border-b border-[#26293c]">
                <span className="font-bold text-[#b0c9ff] flex items-center gap-1 text-[11px]">
                  <span className="material-symbols-outlined text-[14px]">shield</span>
                  PRIVILEGE ELEVATION
                </span>
                <span className="text-[10px] text-[#8d909b]">{privilege.user}</span>
              </div>

              <div className="text-[11px] text-[#8d909b] mb-2 leading-relaxed">
                {privilege.method === 'run0' ? (
                  <span>
                    Modern systemd transient polkit session (<code className="text-[#8cd6cb]">run0</code> preferred).
                  </span>
                ) : privilege.method === 'sudo' ? (
                  <span>
                    Standard Unix setuid elevation (<code className="text-[#b0c9ff]">sudo -n</code>).
                  </span>
                ) : (
                  <span>Unprivileged host session without automatic elevation.</span>
                )}
              </div>

              {elevationFeedback && (
                <div
                  className={`p-1.5 mb-2 rounded text-[10px] leading-tight ${
                    elevationFeedback.includes('active') || elevationFeedback.includes('verified')
                      ? 'bg-emerald-500/10 text-emerald-300 border border-emerald-500/30'
                      : 'bg-amber-500/10 text-amber-300 border border-amber-500/30'
                  }`}
                >
                  {elevationFeedback}
                </div>
              )}

              <div className="flex flex-col gap-1">
                <button
                  type="button"
                  id="test-run0-btn"
                  disabled={testingElevation}
                  onClick={() => handleTestElevation('run0')}
                  className="w-full text-left px-2 py-1.5 hover:bg-[#26293c] rounded text-[#8cd6cb] flex items-center justify-between transition-colors disabled:opacity-50 cursor-pointer"
                >
                  <span className="flex items-center gap-1.5">
                    <span className="material-symbols-outlined text-[14px]">bolt</span>
                    <span>Test run0 (systemd)</span>
                  </span>
                  <span className="text-[10px] text-[#8d909b]">run0 id -u</span>
                </button>

                <button
                  type="button"
                  id="test-sudo-btn"
                  disabled={testingElevation}
                  onClick={() => handleTestElevation('sudo')}
                  className="w-full text-left px-2 py-1.5 hover:bg-[#26293c] rounded text-[#b0c9ff] flex items-center justify-between transition-colors disabled:opacity-50 cursor-pointer"
                >
                  <span className="flex items-center gap-1.5">
                    <span className="material-symbols-outlined text-[14px]">lock_open</span>
                    <span>Test sudo (-n)</span>
                  </span>
                  <span className="text-[10px] text-[#8d909b]">sudo -n id -u</span>
                </button>

                <div className="h-[1px] bg-[#26293c] my-0.5"></div>

                <button
                  type="button"
                  id="reprobe-privilege-btn"
                  disabled={testingElevation}
                  onClick={handleProbePrivilege}
                  className="w-full text-left px-2 py-1 hover:bg-[#26293c] rounded text-[#dfe1fa] flex items-center gap-1.5 transition-colors disabled:opacity-50 cursor-pointer text-[11px]"
                >
                  <span className="material-symbols-outlined text-[13px] text-[#8d909b]">sync</span>
                  <span>{testingElevation ? 'Probing...' : 'Re-probe Host Capabilities'}</span>
                </button>
              </div>
            </div>
          )}
        </div>
      </div>

      {/* Quick Operational Controls */}
      <div className="flex items-center gap-1.5">
        {/* Edit Node Settings button */}
        {onEditNode && (
          <button
            type="button"
            onClick={onEditNode}
            className="flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#d9b9ff] px-2.5 py-1 rounded font-mono text-xs border border-[#434750]/50 transition-colors cursor-pointer"
          >
            <span className="material-symbols-outlined text-[14px]">tune</span>
            <span>Edit Settings</span>
          </button>
        )}

        {/* Reboot with dropdown */}
        <div className="relative inline-block text-left">
          <button
            type="button"
            id="reboot-btn"
            onClick={() => setRebootDropdownOpen(!rebootDropdownOpen)}
            className="flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] px-2.5 py-1 rounded font-mono text-xs border border-[#434750]/50 transition-colors cursor-pointer"
          >
            <span className="material-symbols-outlined text-[14px] text-[#8cd6cb]">restart_alt</span>
            <span>Reboot</span>
            <span className="material-symbols-outlined text-[14px]">arrow_drop_down</span>
          </button>

          {rebootDropdownOpen && (
            <div className="absolute right-0 mt-1 w-44 bg-[#0a0d1f] rounded shadow-lg border border-[#313447] z-50 py-1 font-mono text-xs">
              <button
                type="button"
                onClick={() => {
                  setRebootDropdownOpen(false);
                  onReboot('graceful');
                }}
                className="w-full text-left px-3 py-1.5 hover:bg-[#26293c] text-[#dfe1fa] flex items-center gap-1.5 cursor-pointer"
              >
                <span className="material-symbols-outlined text-[14px] text-[#8cd6cb]">check_circle</span>
                <span>Graceful ACPI</span>
              </button>
              <button
                type="button"
                onClick={() => {
                  setRebootDropdownOpen(false);
                  onReboot('soft');
                }}
                className="w-full text-left px-3 py-1.5 hover:bg-[#26293c] text-[#dfe1fa] flex items-center gap-1.5 cursor-pointer"
              >
                <span className="material-symbols-outlined text-[14px] text-[#b0c9ff]">refresh</span>
                <span>Soft Reboot (systemctl)</span>
              </button>
              <div className="h-[1px] bg-[#26293c] my-1"></div>
              <button
                type="button"
                onClick={() => {
                  setRebootDropdownOpen(false);
                  onReboot('hard');
                }}
                className="w-full text-left px-3 py-1.5 hover:bg-[#93000a]/40 text-[#ffb4ab] flex items-center gap-1.5 cursor-pointer"
              >
                <span className="material-symbols-outlined text-[14px]">warning</span>
                <span>Hard Reset (IPMI)</span>
              </button>
            </div>
          )}
        </div>

        {/* Power Off button */}
        <button
          type="button"
          onClick={onPowerOff}
          className="flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#ffb4ab] px-2.5 py-1 rounded font-mono text-xs border border-[#434750]/50 transition-colors cursor-pointer"
        >
          <span className="material-symbols-outlined text-[14px] text-[#ffb4ab]">power_settings_new</span>
          <span>Power Off</span>
        </button>

        {/* Terminal / SSH button */}
        <button
          type="button"
          onClick={onOpenTerminal}
          className="flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#b0c9ff] px-2.5 py-1 rounded font-mono text-xs border border-[#434750]/50 transition-colors cursor-pointer"
        >
          <span className="material-symbols-outlined text-[14px]">terminal</span>
          <span>Terminal / SSH</span>
        </button>

        {/* Restic Snapshot Now button */}
        <button
          type="button"
          onClick={onTriggerRestic}
          className="flex items-center gap-1 bg-[#b0c9ff] text-[#002e68] font-bold hover:bg-[#d8e2ff] px-2.5 py-1 rounded font-mono text-xs transition-colors shadow-sm cursor-pointer"
        >
          <span className="material-symbols-outlined text-[14px]">camera</span>
          <span>Restic Snapshot Now</span>
        </button>
      </div>
    </div>
  );
};
