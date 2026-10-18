import React from 'react';

interface OperationalTabsProps {
  activeTab: string;
  onSelectTab: (tabId: string) => void;
}

export const OperationalTabs: React.FC<OperationalTabsProps> = ({
  activeTab,
  onSelectTab,
}) => {
  const tabs = [
    { id: 'overview', label: 'Overview', icon: 'space_dashboard' },
    { id: 'services', label: 'Services & Daemons', icon: 'mobile_arrow_right' },
    { id: 'containers', label: 'Podman Containers & Pods (12)', icon: 'deployed_code' },
    { id: 'vms', label: 'Virtual Machines (2 KVM)', icon: 'laptop_mac' },
    { id: 'storage', label: 'Storage & ZFS Pools', icon: 'hard_drive' },
    { id: 'networking', label: 'Networking & Tailscale', icon: 'hub' },
    { id: 'restic', label: 'Restic Backups', icon: 'cloud_sync' },
    { id: 'accounts', label: 'Accounts & PAM', icon: 'manage_accounts' },
    { id: 'updates', label: 'Updates', icon: 'system_update_alt', badge: '3' },
    { id: 'logs', label: 'Kernel Logs', icon: 'notes' },
  ];

  return (
    <div className="flex items-center overflow-x-auto px-3 bg-[#171a2d] border-b border-[#26293c]">
      {tabs.map(tab => {
        const isActive = activeTab === tab.id;
        return (
          <button
            key={tab.id}
            type="button"
            onClick={() => onSelectTab(tab.id)}
            className={`flex items-center gap-1.5 px-3 py-1.5 font-mono text-xs whitespace-nowrap transition-colors cursor-pointer ${
              isActive
                ? 'text-[#b0c9ff] font-bold bg-[#26293c] rounded-t border-t-2 border-[#b0c9ff]'
                : 'text-[#c3c6d2] hover:text-[#dfe1fa] hover:bg-[#1b1e31]'
            }`}
          >
            <span className="material-symbols-outlined text-[15px]">{tab.icon}</span>
            <span>{tab.label}</span>
            {tab.badge && (
              <span className="px-1 py-0.2 bg-[#573583]/50 text-[#d9b9ff] font-mono text-[10px] rounded ml-0.5">
                {tab.badge}
              </span>
            )}
          </button>
        );
      })}
    </div>
  );
};
