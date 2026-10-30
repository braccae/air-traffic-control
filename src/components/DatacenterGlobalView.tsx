import React from 'react';
import type { NodeItem, GlobalTaskItem, QueuedAnsibleTask } from '../types/index.ts';
import { DatacenterSummaryView } from './DatacenterSummaryView.tsx';
import type { DatacenterSection, DatacenterSettingPageId } from '../utils/navigationViewModel.ts';
import { VaultSettingsPage } from './VaultSettingsPage.tsx';

export interface DatacenterGlobalViewProps {
  datacenterName?: string;
  nodes: NodeItem[];
  tasks: GlobalTaskItem[];
  /** Live Ansible mutation queue, fed from the coordinator websocket. */
  queue?: QueuedAnsibleTask[];
  isQueueRunning?: boolean;
  activeSection?: DatacenterSection;
  onSelectNode: (node: NodeItem) => void;
  onOpenPlaybookModal: (playbookName?: string) => void;
  /** Navigates to a datacenter settings submenu page. */
  onOpenSetting: (page: DatacenterSettingPageId) => void;
  onToggleTasksDrawer?: () => void;
  onSelectSection?: (section: DatacenterSection) => void;
}

/**
 * Datacenter Overview.
 *
 * This page is a summary only. Anything with a dedicated submenu entry
 * (playbooks, schedules, quick commands, ansible.cfg, mesh topology, vault)
 * lives in the settings submenu, so it is not repeated here.
 */
export const DatacenterGlobalView: React.FC<DatacenterGlobalViewProps> = ({
  datacenterName = 'leo-banjo.ts.net',
  nodes,
  tasks,
  queue = [],
  isQueueRunning = false,
  activeSection,
  onSelectNode,
  onOpenPlaybookModal,
  onOpenSetting,
  onToggleTasksDrawer,
  onSelectSection,
}) => {
  // Full-page vault editor: the sidebar's "Vars & Vault Secrets" section.
  if (activeSection === 'vars_vault') {
    return (
      <VaultSettingsPage onBack={onSelectSection ? () => onSelectSection('overview') : undefined} />
    );
  }

  return (
    <div className="flex flex-col gap-4 w-full">
      <DatacenterSummaryView
        datacenterName={datacenterName}
        nodes={nodes}
        tasks={tasks}
        queue={queue}
        isQueueRunning={isQueueRunning}
        onSelectNode={onSelectNode}
        onOpenPlaybookModal={onOpenPlaybookModal}
        onOpenSetting={onOpenSetting}
        onOpenConsole={onToggleTasksDrawer}
      />
    </div>
  );
};
