import React from 'react';
import type { ThemeMode } from '../utils/themeManager.ts';
import { getThemeIcon, getThemeLabel } from '../utils/themeManager.ts';

interface HeaderProps {
  onOpenAddNode: () => void;
  onOpenPlaybook: () => void;
  onOpenPlaybookEditor?: () => void;
  onOpenTerminal: () => void;
  onOpenSearch: () => void;
  onOpenGroupVars: () => void;
  onToggleSidebar?: () => void;
  isSidebarOpen?: boolean;
  onlineCount?: number;
  totalCount?: number;
  theme?: ThemeMode;
  onToggleTheme?: () => void;
}

export const Header: React.FC<HeaderProps> = ({
  onOpenAddNode,
  onOpenPlaybook,
  onOpenPlaybookEditor,
  onOpenTerminal,
  onOpenSearch,
  onOpenGroupVars,
  onToggleSidebar,
  isSidebarOpen,
  onlineCount,
  totalCount,
  theme = 'dark',
  onToggleTheme,
}) => {
  return (
    <header className="fixed top-0 left-0 right-0 z-50 h-10 bg-[#0a0d1f] flex items-center justify-between px-3 shadow-[0_1px_8px_rgba(0,0,0,0.5)] border-b border-[#26293c]">
      {/* Brand & Datacenter */}
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
        <div className="flex items-center gap-1.5 bg-[#313447] px-2 py-0.5 rounded border border-[#434750]/50">
          <span className="material-symbols-outlined text-[#b0c9ff] text-[18px]">dns</span>
          <span className="font-mono text-xs tracking-wider text-[#b0c9ff] font-bold">PROX-ORCH</span>
          <span className="font-mono text-[10px] px-1 bg-[#171a2d] text-[#8cd6cb] rounded">v8.2-ansible</span>
        </div>
        <div className="h-4 w-[1px] bg-[#434750]"></div>
        <div className="flex items-center gap-1 text-[#c3c6d2] font-mono text-xs">
          <span className="material-symbols-outlined text-[15px] text-[#8cd6cb]">share_location</span>
          <span>Datacenter:</span>
          <span className="text-[#dfe1fa] font-semibold">homelab-dc1</span>
          <span className="w-2 h-2 rounded-full bg-[#8cd6cb] ml-1 shadow-[0_0_8px_rgba(140,214,203,0.8)]"></span>
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

          {onOpenPlaybookEditor && (
            <button
              type="button"
              onClick={onOpenPlaybookEditor}
              className="flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] px-2.5 py-1 rounded font-mono text-xs border border-[#434750]/50 transition-colors cursor-pointer"
              title="Add, edit, or remove Ansible playbooks"
            >
              <span className="material-symbols-outlined text-[14px] text-[#b0c9ff]">menu_book</span>
              <span>Playbooks</span>
            </button>
          )}

          <button
            type="button"
            onClick={onOpenGroupVars}
            className="flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] px-2.5 py-1 rounded font-mono text-xs border border-[#434750]/50 transition-colors cursor-pointer"
          >
            <span className="material-symbols-outlined text-[14px] text-[#d9b9ff]">lock_reset</span>
            <span>New Group Var</span>
          </button>

          <button
            type="button"
            onClick={onOpenTerminal}
            className="flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] px-2.5 py-1 rounded font-mono text-xs border border-[#434750]/50 transition-colors cursor-pointer"
          >
            <span className="material-symbols-outlined text-[14px] text-[#b0c9ff]">terminal</span>
            <span>Terminal / SSH</span>
          </button>

          {onToggleTheme && (
            <button
              type="button"
              onClick={onToggleTheme}
              className="flex items-center gap-1.5 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] px-2.5 py-1 rounded font-mono text-xs border border-[#434750]/50 transition-colors cursor-pointer"
              title={`Toggle Theme (Current: ${theme === 'latte' ? 'Catppuccin Latte' : 'Dark'})`}
              aria-label={`Toggle theme (currently ${theme === 'latte' ? 'Latte' : 'Dark'})`}
            >
              <span className="material-symbols-outlined text-[14px] text-[#f9c068]">
                {getThemeIcon(theme)}
              </span>
              <span>{getThemeLabel(theme)}</span>
            </button>
          )}
        </div>

        <div className="h-4 w-[1px] bg-[#434750] mx-1"></div>

        {/* Nodes Health & User Profile */}
        <div className="flex items-center gap-2">
          <div className="flex items-center gap-1 bg-[#171a2d] px-2 py-0.5 rounded border border-[#26293c]">
            <span className="w-2 h-2 rounded-full bg-[#8cd6cb] animate-pulse shadow-[0_0_6px_rgba(140,214,203,0.8)]"></span>
            <span className="font-mono text-xs text-[#c3c6d2]">
              {onlineCount ?? 8}/{totalCount ?? 8} Up
            </span>
          </div>

          <div className="flex items-center gap-1.5 text-[#dfe1fa] pl-1">
            <div className="w-7 h-7 rounded-full bg-[#b0c9ff] text-[#002e68] flex items-center justify-center font-bold text-xs shadow-sm">
              <span className="material-symbols-outlined text-[18px]">person</span>
            </div>
            <div className="flex flex-col text-left">
              <span className="font-mono text-xs text-[#dfe1fa] leading-tight font-bold">root@pam</span>
              <span className="font-mono text-[10px] text-[#8cd6cb] leading-none">PAM AUTH</span>
            </div>
          </div>
        </div>
      </div>
    </header>
  );
};
