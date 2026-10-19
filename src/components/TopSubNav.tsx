import React from 'react';
import { TOP_SUB_NAV_TABS } from '../utils/navigationViewModel.ts';

interface TopSubNavProps {
  activeTopTab: string;
  onSelectTopTab: (tabId: string) => void;
  clusterCpu?: string;
  clusterRam?: string;
}

export const TopSubNav: React.FC<TopSubNavProps> = ({
  activeTopTab,
  onSelectTopTab,
  clusterCpu = '14.8%',
  clusterRam = '42.1 / 128 GB',
}) => {
  const tabs = TOP_SUB_NAV_TABS;

  return (
    <div className="fixed top-10 lg:left-72 left-0 right-0 h-8 bg-[#1b1e31] z-30 flex items-center justify-between px-3 shadow-[0_1px_4px_rgba(0,0,0,0.2)] border-b border-[#26293c]">
      <nav className="flex items-center h-full overflow-x-auto">
        {tabs.map(tab => {
          const isActive = activeTopTab === tab.id;
          return (
            <button
              key={tab.id}
              type="button"
              onClick={() => onSelectTopTab(tab.id)}
              className={`px-3 h-full flex items-center font-mono text-xs transition-colors cursor-pointer ${
                isActive
                  ? 'text-[#b0c9ff] font-bold bg-[#26293c] border-b-2 border-[#b0c9ff]'
                  : 'text-[#c3c6d2] hover:text-[#dfe1fa] hover:bg-[#26293c]/50'
              }`}
            >
              {tab.label}
            </button>
          );
        })}
      </nav>

      {/* Cluster aggregate resources */}
      <div className="flex items-center gap-3 font-mono text-xs text-[#c3c6d2]">
        <span className="flex items-center gap-1">
          <span className="material-symbols-outlined text-[14px] text-[#b0c9ff]">memory</span>
          <span>CPU: {clusterCpu}</span>
        </span>
        <span className="flex items-center gap-1">
          <span className="material-symbols-outlined text-[14px] text-[#d9b9ff]">pie_chart</span>
          <span>RAM: {clusterRam}</span>
        </span>
      </div>
    </div>
  );
};
