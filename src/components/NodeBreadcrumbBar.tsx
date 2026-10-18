import React, { useState } from 'react';
import type { NodeItem } from '../types/index.ts';

interface NodeBreadcrumbBarProps {
  node: NodeItem;
  onOpenTerminal: () => void;
  onTriggerRestic: () => void;
  onReboot: (mode: string) => void;
  onPowerOff: () => void;
  onEditNode?: () => void;
}

export const NodeBreadcrumbBar: React.FC<NodeBreadcrumbBarProps> = ({
  node,
  onOpenTerminal,
  onTriggerRestic,
  onReboot,
  onPowerOff,
  onEditNode,
}) => {
  const [rebootDropdownOpen, setRebootDropdownOpen] = useState(false);

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
                className="w-full text-left px-3 py-1.5 hover:bg-[#26293c] text-[#dfe1fa] flex items-center gap-1.5"
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
                className="w-full text-left px-3 py-1.5 hover:bg-[#26293c] text-[#dfe1fa] flex items-center gap-1.5"
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
                className="w-full text-left px-3 py-1.5 hover:bg-[#93000a]/40 text-[#ffb4ab] flex items-center gap-1.5"
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
