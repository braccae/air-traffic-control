import React from 'react';
import type { ThemeChoice, ResolvedTheme } from '../utils/themeManager.ts';

interface HeaderProps {
  onOpenAddNode: () => void;
  onOpenPlaybook: () => void;
  onOpenPlaybookEditor?: () => void;
  onOpenSearch: () => void;
  onOpenGroupVars: () => void;
  onToggleSidebar?: () => void;
  isSidebarOpen?: boolean;
  onlineCount?: number;
  totalCount?: number;
  theme?: ThemeChoice;
  resolvedTheme?: ResolvedTheme;
  onToggleTheme?: () => void;
  onSelectTheme?: (theme: ThemeChoice) => void;
  onOpenClientSettings?: () => void;
}

export const Header: React.FC<HeaderProps> = ({
  onOpenAddNode,
  onOpenPlaybook,
  onOpenSearch,
  onOpenGroupVars,
  onToggleSidebar,
  isSidebarOpen,
  onOpenClientSettings,
}) => {

  return (
    <header className="fixed top-0 left-0 right-0 z-50 h-10 bg-[#0a0d1f] flex items-center justify-between px-3 shadow-[0_1px_8px_rgba(0,0,0,0.5)] border-b border-[#26293c]">
      {/* Brand */}
      <div className="flex items-center gap-2">
        {onToggleSidebar && (
          <button
            type="button"
            onClick={onToggleSidebar}
            aria-label="Toggle server sidebar"
            className="lg:hidden text-[#8d909b] hover:text-[#dfe1fa] p-1 cursor-pointer mr-0.5"
          >
            <span className="material-symbols-outlined text-[20px]">
              {isSidebarOpen ? 'close' : 'menu'}
            </span>
          </button>
        )}
        <div className="flex items-center gap-1.5">
          <span className="material-symbols-outlined text-[#b0c9ff] text-[20px]">radar</span>
          <span className="font-mono text-sm tracking-wider text-[#dfe1fa] font-bold">AIR TRAFFIC CONTROL</span>
        </div>
      </div>

      {/* Global Search Bar (Ctrl+K) */}
      <div className="flex-1 max-w-md mx-4">
        <button
          type="button"
          onClick={onOpenSearch}
          className="w-full flex items-center bg-[#171a2d] hover:bg-[#1b1e31] px-3 py-1 rounded text-[#c3c6d2] gap-2 border border-[#313447] text-left transition-colors cursor-pointer group"
          aria-label="Search hosts, playbooks, vars"
        >
          <span className="material-symbols-outlined text-[16px] text-[#8d909b] group-hover:text-[#b0c9ff]">search</span>
          <span className="font-mono text-xs flex-1 truncate text-[#8d909b]">Search hosts, playbooks, vars... (Ctrl+K)</span>
          <kbd className="font-mono text-[10px] bg-[#313447] text-[#c3c6d2] px-1.5 py-0.5 rounded border border-[#434750]">^K</kbd>
        </button>
      </div>

      {/* Action Buttons & Status Profile */}
      <div className="flex items-center gap-2">
        <div className="flex items-center gap-1.5">
          <button
            type="button"
            onClick={onOpenAddNode}
            className="flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] px-2.5 py-1 rounded font-mono text-xs border border-[#434750]/50 transition-colors cursor-pointer"
          >
            <span className="material-symbols-outlined text-[14px] text-[#8cd6cb]">add_circle</span>
            <span>+ Add Node</span>
          </button>

          <button
            type="button"
            onClick={onOpenPlaybook}
            className="flex items-center gap-1 bg-[#b0c9ff] text-[#002e68] font-bold hover:bg-[#d8e2ff] px-2.5 py-1 rounded font-mono text-xs transition-colors shadow-sm cursor-pointer"
          >
            <span className="material-symbols-outlined text-[14px]">play_arrow</span>
            <span>Run Playbook</span>
          </button>

          <button
            type="button"
            onClick={onOpenGroupVars}
            className="flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] px-2.5 py-1 rounded font-mono text-xs border border-[#434750]/50 transition-colors cursor-pointer"
          >
            <span className="material-symbols-outlined text-[14px] text-[#d9b9ff]">lock_reset</span>
            <span>New Group Var</span>
          </button>

          {onOpenClientSettings && (
            <button
              type="button"
              onClick={onOpenClientSettings}
              className="flex items-center gap-1.5 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] px-2.5 py-1 rounded font-mono text-xs border border-[#434750]/50 transition-colors cursor-pointer"
              title="Client Settings & Appearance"
              aria-label="Open Client Settings and Appearance"
            >
              <span className="material-symbols-outlined text-[14px] text-[#f9c068]">palette</span>
              <span className="hidden sm:inline">Settings</span>
            </button>
          )}
        </div>

        <div className="h-4 w-[1px] bg-[#434750] mx-1"></div>

        {/* User Profile */}
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={onOpenClientSettings}
            className="flex items-center gap-1.5 text-[#dfe1fa] pl-1 cursor-pointer hover:opacity-90 transition-opacity"
            title="Role-Based Access Control / OIDC (Enabled) - Click for Client Settings"
            aria-label="User profile: root@pam (Role-Based Access Control / OIDC Enabled)"
          >
            <div className="w-7 h-7 rounded-full bg-[#b0c9ff] text-[#002e68] flex items-center justify-center font-bold text-xs shadow-sm">
              <span className="material-symbols-outlined text-[18px]">person</span>
            </div>
            <div className="flex flex-col text-left">
              <span className="font-mono text-xs text-[#dfe1fa] leading-tight font-bold">root@pam</span>
              <span className="font-mono text-[10px] text-[#8cd6cb] leading-none">PAM AUTH</span>
            </div>
          </button>
        </div>
      </div>
    </header>
  );
};
