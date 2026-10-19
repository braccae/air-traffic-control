import React, { useState, useEffect, useCallback } from 'react';
import type { NodeItem, TelemetryData, GlobalTaskItem, QueuedAnsibleTask, TagInventoryChunk, CustomFolder } from './types/index.ts';
import { Header } from './components/Header.tsx';
import { ServerViewSidebar } from './components/ServerViewSidebar.tsx';
import { NodeBreadcrumbBar } from './components/NodeBreadcrumbBar.tsx';
import { CockpitSubSidebar } from './components/CockpitSubSidebar.tsx';
import { SystemHealthTelemetry } from './components/SystemHealthTelemetry.tsx';
import { TailscaleMeshCard } from './components/TailscaleMeshCard.tsx';
import { PodmanManager } from './components/PodmanManager.tsx';
import { VirtualMachinesKVM } from './components/VirtualMachinesKVM.tsx';
import { ResticBackupPolicy } from './components/ResticBackupPolicy.tsx';
import { ServiceIdentitiesTable } from './components/ServiceIdentitiesTable.tsx';
import { GlobalTasksExecutionLog } from './components/GlobalTasksExecutionLog.tsx';
import { NetworkInterfacesView } from './components/NetworkInterfacesView.tsx';
import { FirewallView } from './components/FirewallView.tsx';
import { ZfsManagementView } from './components/ZfsManagementView.tsx';
import type { StorageEngineType } from './utils/zfsParser.ts';
import { NetworkSharesView } from './components/NetworkSharesView.tsx';
import { DisksSmartView } from './components/DisksSmartView.tsx';
import { SystemLogsView } from './components/SystemLogsView.tsx';
import { PackageUpdatesView } from './components/PackageUpdatesView.tsx';
import { ToastNotification, type ToastItem } from './components/ToastNotification.tsx';

// Modals
import { TerminalModal } from './components/modals/TerminalModal.tsx';
import type { ManagedWindow } from './utils/windowManager.ts';
import { TailscaleExplorerView } from './components/TailscaleExplorerView.tsx';
import { AddNodeModal } from './components/modals/AddNodeModal.tsx';
import { CreateVmModal } from './components/modals/CreateVmModal.tsx';
import { EditNodeModal } from './components/modals/EditNodeModal.tsx';
import { RunPlaybookModal } from './components/modals/RunPlaybookModal.tsx';
import { PlaybookEditorModal } from './components/modals/PlaybookEditorModal.tsx';
import { ContainerLogsModal } from './components/modals/ContainerLogsModal.tsx';
import { RebootConfirmModal } from './components/modals/RebootConfirmModal.tsx';
import { SearchModal } from './components/modals/SearchModal.tsx';
import { GroupVarsModal } from './components/modals/GroupVarsModal.tsx';
import { ClientSettingsModal } from './components/modals/ClientSettingsModal.tsx';
import { DatacenterGlobalView } from './components/DatacenterGlobalView.tsx';
import {
  updateNodeSettings,
  getSubSidebarWrapperClasses,
  getWorkspaceWrapperClasses,
  type SidebarViewMode,
  type DatacenterSection,
  type ConsoleDrawerTab,
} from './utils/navigationViewModel.ts';
import {
  createTerminalSession,
  closeTerminalSession,
  setSessionMinimized,
  toggleSessionMaximized,
  updateSessionPosition,
  updateSessionSize,
  type TerminalSession,
} from './utils/terminalSessionManager.ts';
import {
  type ThemeChoice,
  type ResolvedTheme,
  type AccentColor,
  getStoredTheme,
  setStoredTheme,
  resolveTheme,
  getSystemTheme,
  watchSystemTheme,
  toggleTheme,
  applyThemeToDocument,
  getStoredAccentColor,
  setStoredAccentColor,
  applyAccentColorToDocument,
} from './utils/themeManager.ts';
import type { DragPosition } from './hooks/useDraggable.ts';
import type { WindowSize } from './hooks/useResizable.ts';

const initialNodes: NodeItem[] = [
  {
    id: 'node-likes-it-cold',
    name: 'likes-it-cold',
    shortName: 'likes-it-cold',
    group: 'tag:host',
    datacenter: 'leo-banjo.ts.net',
    nodeType: 'linux',
    badge: 'SSH',
    badgeColor: 'text-[#8cd6cb]',
    ip: '192.168.8.249',
    port: 22,
    sshUser: 'core',
    authMethod: 'ssh-ed25519 (ssh-agent)',
    tags: ['tag:host'],
    sshConnected: true,
    rttMs: '1.8ms',
    tailscaleIp: '100.105.71.49',
    tailscaleHost: 'likes-it-cold.leo-banjo.ts.net',
    osDescription: 'AlmaLinux 10.2 (Lavender Lion)',
    status: 'online',
    uptime: '3d 14h',
    hasGlances: false,
    hasCockpit: true,
    hasPodman: true,
    hasRestic: true,
    specs: {
      cpu: 'AMD Ryzen 5 PRO 2400G with Radeon Vega Graphics',
      vcpus: 8,
      ramGiB: 32,
      motherboard: 'Lenovo 3141 (ThinkCentre M725s)',
      bios: 'Lenovo M25KT2FA (2019-04-23)',
      ipmi: 'N/A (Desktop SFF)',
      ecc: 'Non-ECC DDR4',
      secureBoot: 'Enabled',
      iommu: 'Enabled (AMD-Vi)',
    },
  },
  {
    id: 'node-ping',
    name: 'ping',
    shortName: 'ping',
    group: 'tag:host',
    datacenter: 'leo-banjo.ts.net',
    nodeType: 'linux',
    badge: 'SSH',
    badgeColor: 'text-[#8cd6cb]',
    ip: '100.73.87.87',
    port: 22,
    sshUser: 'core',
    authMethod: 'ssh-ed25519 (ssh-agent)',
    tags: ['tag:host'],
    sshConnected: true,
    rttMs: '2.1ms',
    tailscaleIp: '100.73.87.87',
    tailscaleHost: 'ping.leo-banjo.ts.net',
    osDescription: 'AlmaLinux 10.2 (Lavender Lion)',
    status: 'online',
    uptime: '4d 14h',
    hasGlances: false,
    hasCockpit: true,
    hasPodman: true,
    hasRestic: true,
    specs: {
      cpu: 'Intel(R) Core(TM) i5-6500 CPU @ 3.20GHz',
      vcpus: 4,
      ramGiB: 8,
      motherboard: 'Dell 0Y7WYT (OptiPlex 7040)',
      bios: 'Dell 1.24.0 (2022-07-14)',
      ipmi: 'Intel AMT',
      ecc: 'Non-ECC DDR4',
      secureBoot: 'Enabled',
      iommu: 'Enabled (VT-d)',
    },
  },
  {
    id: 'node-dev-pc',
    name: 'dev-pc (local)',
    shortName: 'dev-pc',
    group: 'tag:host',
    datacenter: 'leo-banjo.ts.net',
    nodeType: 'linux',
    badge: 'LOCAL',
    badgeColor: 'text-[#b0c9ff]',
    ip: '127.0.0.1',
    port: 22,
    sshUser: 'pants',
    authMethod: 'local-socket / ssh-agent',
    tags: ['tag:admin', 'tag:host'],
    sshConnected: true,
    rttMs: '0.1ms',
    tailscaleIp: '100.97.159.101',
    tailscaleHost: 'dev-pc.leo-banjo.ts.net',
    osDescription: 'CachyOS Linux (6.19.11-1-cachyos)',
    status: 'online',
    uptime: '1d 22h',
    hasGlances: true,
    hasCockpit: true,
    hasPodman: true,
    hasRestic: true,
    specs: {
      cpu: 'AMD Ryzen 5 5600X 6-Core Processor',
      vcpus: 12,
      ramGiB: 32,
      motherboard: 'ASUS ROG STRIX B450-F GAMING',
      bios: 'ASUS 4801',
      ipmi: 'Local Bus',
      ecc: 'Non-ECC',
      secureBoot: 'Disabled',
      iommu: 'Enabled (AMD-Vi)',
    },
  },
  {
    id: 'node-fred',
    name: 'fred',
    shortName: 'fred',
    group: 'tag:host',
    datacenter: 'leo-banjo.ts.net',
    nodeType: 'linux',
    badge: 'TS-SSH',
    badgeColor: 'text-[#8cd6cb]',
    ip: '100.100.173.57',
    port: 22,
    sshUser: 'root',
    authMethod: 'tailscale-ssh',
    tags: ['tag:host'],
    sshConnected: true,
    rttMs: '3.4ms',
    tailscaleIp: '100.100.173.57',
    tailscaleHost: 'fred.leo-banjo.ts.net',
    osDescription: 'Debian GNU/Linux 13 (trixie)',
    status: 'online',
    uptime: '37d 04h',
    hasGlances: true,
    hasCockpit: true,
    hasPodman: true,
    hasRestic: true,
    specs: {
      cpu: 'Common KVM processor',
      vcpus: 4,
      ramGiB: 4,
      motherboard: 'QEMU Standard PC',
      bios: 'SeaBIOS',
      ipmi: 'Proxmox VE',
      ecc: 'Host Managed',
      secureBoot: 'Disabled',
      iommu: 'N/A',
    },
  },
  {
    id: 'node-the-freezer',
    name: 'the-freezer',
    shortName: 'the-freezer',
    group: 'tag:host',
    datacenter: 'leo-banjo.ts.net',
    nodeType: 'linux',
    badge: 'MGR',
    badgeColor: 'text-[#d9b9ff]',
    ip: '192.168.8.3',
    port: 22,
    sshUser: 'core',
    authMethod: 'ssh-ed25519 (ssh-agent)',
    tags: ['tag:host', 'tag:manager'],
    sshConnected: true,
    rttMs: '1.5ms',
    tailscaleIp: '100.115.75.78',
    tailscaleHost: 'the-freezer.leo-banjo.ts.net',
    osDescription: 'AlmaLinux 10.2 (Lavender Lion)',
    status: 'online',
    uptime: '14d 08h',
    hasGlances: false,
    hasCockpit: true,
    hasPodman: true,
    hasRestic: true,
    specs: {
      cpu: 'AMD Ryzen 7 5700X 8-Core Processor',
      vcpus: 16,
      ramGiB: 64,
      motherboard: 'ASUS PRIME B550-PLUS',
      bios: 'ASUS UEFI 3607 (2024-03-22)',
      ipmi: 'N/A (Tower ATX)',
      ecc: 'Non-ECC DDR4',
      secureBoot: 'Enabled',
      iommu: 'Enabled (AMD-Vi)',
    },
  },
  {
    id: 'node-chuck',
    name: 'chuck',
    shortName: 'chuck',
    group: 'tag:host',
    datacenter: 'leo-banjo.ts.net',
    nodeType: 'linux',
    badge: 'HOST',
    badgeColor: 'text-[#8d909b]',
    ip: '100.114.97.98',
    port: 22,
    sshUser: 'core',
    authMethod: 'ssh-ed25519 (ssh-agent)',
    tags: ['tag:host'],
    sshConnected: false,
    rttMs: '2.8ms',
    tailscaleIp: '100.114.97.98',
    tailscaleHost: 'chuck.leo-banjo.ts.net',
    osDescription: 'Linux (tag:host)',
    status: 'online',
    uptime: '9d 12h',
    hasGlances: true,
    hasCockpit: true,
    hasPodman: true,
    hasRestic: true,
    specs: {
      cpu: 'x86_64 Host Node',
      vcpus: 4,
      ramGiB: 8,
      motherboard: 'Homelab Host',
      bios: 'UEFI',
      ipmi: 'N/A',
      ecc: 'Standard',
      secureBoot: 'Enabled',
      iommu: 'Enabled',
    },
  },
  {
    id: 'node-larry',
    name: 'larry',
    shortName: 'larry',
    group: 'tag:host',
    datacenter: 'leo-banjo.ts.net',
    nodeType: 'linux',
    badge: 'HOST',
    badgeColor: 'text-[#8d909b]',
    ip: '192.168.8.144',
    port: 22,
    sshUser: 'core',
    authMethod: 'ssh-ed25519 (ssh-agent)',
    tags: ['tag:host'],
    sshConnected: false,
    rttMs: '1.9ms',
    tailscaleIp: '100.107.167.19',
    tailscaleHost: 'larry.leo-banjo.ts.net',
    osDescription: 'Linux (tag:host)',
    status: 'online',
    uptime: '11d 03h',
    hasGlances: true,
    hasCockpit: true,
    hasPodman: true,
    hasRestic: true,
    specs: {
      cpu: 'x86_64 Host Node',
      vcpus: 4,
      ramGiB: 8,
      motherboard: 'Homelab Host',
      bios: 'UEFI',
      ipmi: '192.168.8.144',
      ecc: 'Standard',
      secureBoot: 'Enabled',
      iommu: 'Enabled',
    },
  },
  {
    id: 'node-orangepizero3',
    name: 'orangepizero3',
    shortName: 'orangepizero3',
    group: 'tag:host',
    datacenter: 'leo-banjo.ts.net',
    nodeType: 'linux',
    badge: 'ARM64',
    badgeColor: 'text-[#ffd97d]',
    ip: '100.65.100.14',
    port: 22,
    sshUser: 'core',
    authMethod: 'ssh-ed25519 (ssh-agent)',
    tags: ['tag:host'],
    sshConnected: false,
    rttMs: '4.2ms',
    tailscaleIp: '100.65.100.14',
    tailscaleHost: 'orangepizero3.leo-banjo.ts.net',
    osDescription: 'Debian / Armbian (Allwinner H618 ARM64)',
    status: 'online',
    uptime: '21d 06h',
    hasGlances: true,
    hasCockpit: false,
    hasPodman: true,
    hasRestic: false,
    specs: {
      cpu: 'Allwinner H618 Quad-Core Cortex-A53',
      vcpus: 4,
      ramGiB: 4,
      motherboard: 'Orange Pi Zero 3 SBC',
      bios: 'U-Boot ARM64',
      ipmi: 'GPIO / Serial',
      ecc: 'Non-ECC LPDDR4',
      secureBoot: 'Disabled',
      iommu: 'N/A',
    },
  },
];

const initialTelemetry: TelemetryData = {
  cpuUsage: 4.2,
  cpuModel: 'AMD EPYC / x86_64 Host',
  vCpus: 8,
  memoryUsedGiB: 6.4,
  memoryTotalGiB: 16.0,
  arcCacheGiB: 2.1,
  zfsTankUsedTb: 0.18,
  zfsTankTotalTb: 1.0,
  zfsRaidType: 'XFS / RootFS',
  zfsScrubStatus: 'Host: likes-it-cold',
  loadAvg1m: 0.34,
  loadAvg5m: 0.28,
  loadAvg15m: 0.22,
  ioDelayPercent: 0.02,
  cpuTempC: 39.0,
  fanSpeedRpm: 840,
  packageWatts: 38.5,
  sparkline: [14, 16, 15, 18, 12, 14, 11, 13, 10, 12, 11],
};

const initialTasks: GlobalTaskItem[] = [
  {
    id: 'task-1',
    startTime: '2026-10-18 01:20:00',
    nodeTarget: 'likes-it-cold.leo-banjo.ts.net',
    user: 'core@ssh-agent',
    description: 'ssh core@likes-it-cold.leo-banjo.ts.net podman ps --all',
    progress: 100,
    progressEta: 'Connected via ssh-agent (ED25519)',
    status: 'OK',
    category: 'active',
  },
  {
    id: 'task-2',
    startTime: '2026-10-18 01:20:05',
    nodeTarget: 'ping.leo-banjo.ts.net',
    user: 'core@ssh-agent',
    description: 'Probe rootless Podman user uosserver (UID 1001, SubUID 589824:65536)',
    progress: 100,
    progressEta: '1 running container (uosserver)',
    status: 'OK',
    category: 'active',
  },
];

export default function App() {
  const [nodesList, setNodesList] = useState<NodeItem[]>(initialNodes);
  const [selectedNode, setSelectedNode] = useState<NodeItem>(initialNodes[0]);
  const [selectedHostGroup, setSelectedHostGroup] = useState('all');
  const [activeTopTab, setActiveTopTab] = useState('summary');
  const [activeOpTab, setActiveOpTab] = useState('overview');
  const [activeCockpitItem, setActiveCockpitItem] = useState('overview');
  const [selectedViewMode, setSelectedViewMode] = useState<SidebarViewMode>('nodes');
  const [datacenterSection, setDatacenterSection] = useState<DatacenterSection>('overview');

  const [telemetry, setTelemetry] = useState<TelemetryData>(initialTelemetry);
  const [tasks, setTasks] = useState<GlobalTaskItem[]>(initialTasks);
  const [queue, setQueue] = useState<QueuedAnsibleTask[]>([]);
  const [isQueueRunning, setIsQueueRunning] = useState<boolean>(false);
  const [activeConsoleTab, setActiveConsoleTab] = useState<ConsoleDrawerTab>('active');
  const [nodeStorageEngines, setNodeStorageEngines] = useState<Record<string, StorageEngineType>>({});
  const [chunks, setChunks] = useState<TagInventoryChunk[]>([]);
  const [folders, setFolders] = useState<CustomFolder[]>([]);

  // Theme state: system | macchiato | latte with OS auto-preference
  const [themeChoice, setThemeChoice] = useState<ThemeChoice>(() => getStoredTheme());
  const [systemIsDark, setSystemIsDark] = useState<boolean>(() => getSystemTheme());
  const [accentColor, setAccentColor] = useState<AccentColor>(() => getStoredAccentColor());
  const [clientSettingsOpen, setClientSettingsOpen] = useState(false);
  const [reducedMotion, setReducedMotion] = useState(false);

  useEffect(() => {
    return watchSystemTheme((isDark) => {
      setSystemIsDark(isDark);
    });
  }, []);

  const resolvedTheme = resolveTheme(themeChoice, systemIsDark);

  useEffect(() => {
    applyThemeToDocument(themeChoice, undefined, systemIsDark);
    setStoredTheme(themeChoice);
  }, [themeChoice, systemIsDark]);

  useEffect(() => {
    applyAccentColorToDocument(accentColor);
    setStoredAccentColor(accentColor);
  }, [accentColor]);

  const handleToggleTheme = useCallback(() => {
    setThemeChoice(prev => toggleTheme(prev));
  }, []);

  const handleSelectTheme = useCallback((choice: ThemeChoice) => {
    setThemeChoice(choice);
  }, []);

  // Mobile sidebar state
  const [mobileSidebarOpen, setMobileSidebarOpen] = useState(false);

  // Modals state
  const [terminalOpen, setTerminalOpen] = useState(false);
  const [addNodeOpen, setAddNodeOpen] = useState(false);
  const [createVmOpen, setCreateVmOpen] = useState(false);
  const [editNodeTarget, setEditNodeTarget] = useState<NodeItem | null>(null);
  const [playbookOpen, setPlaybookOpen] = useState(false);
  const [playbookEditorOpen, setPlaybookEditorOpen] = useState(false);
  const [selectedPlaybookForEdit, setSelectedPlaybookForEdit] = useState<string | undefined>(undefined);
  const [logsTarget, setLogsTarget] = useState<string | null>(null);
  const [logsUser, setLogsUser] = useState<string | undefined>(undefined);
  const [logsUid, setLogsUid] = useState<number | undefined>(undefined);

  const handleOpenLogs = (target: string, user?: string, uid?: number) => {
    setLogsTarget(target);
    setLogsUser(user);
    setLogsUid(uid);
  };

  const [rebootModalOpen, setRebootModalOpen] = useState(false);
  const [searchOpen, setSearchOpen] = useState(false);
  const [groupVarsOpen, setGroupVarsOpen] = useState(false);

  const handleOpenPlaybookEditor = (playbookName?: string) => {
    setSelectedPlaybookForEdit(playbookName);
    setPlaybookEditorOpen(true);
  };

  // Floating & Draggable Terminal Sessions State
  const [sessions, setSessions] = useState<TerminalSession[]>(() => {
    if (typeof window !== 'undefined') {
      try {
        const saved = localStorage.getItem('air_traffic_control_term_sessions') || localStorage.getItem('prox_orch_term_sessions');
        if (saved) return JSON.parse(saved);
      } catch {}
    }
    return [];
  });
  const [activeSessionId, setActiveSessionId] = useState<string | null>(() => {
    if (typeof window !== 'undefined') {
      return localStorage.getItem('air_traffic_control_term_active_id') || localStorage.getItem('prox_orch_term_active_id');
    }
    return null;
  });

  const [toasts, setToasts] = useState<ToastItem[]>([]);

  const addToast = useCallback((toast: Omit<ToastItem, 'id'>) => {
    const id = `toast-${Date.now()}-${Math.random().toString(36).substring(2, 6)}`;
    setToasts(prev => [...prev.slice(-3), { ...toast, id }]);
  }, []);

  const handleDismissToast = useCallback((id: string) => {
    setToasts(prev => prev.filter(t => t.id !== id));
  }, []);

  useEffect(() => {
    const handleCustomToast = (e: Event) => {
      const detail = (e as CustomEvent).detail;
      if (detail && typeof detail === 'object') {
        addToast(detail);
      }
    };
    window.addEventListener('air_traffic_control_toast', handleCustomToast);
    window.addEventListener('prox_orch_toast', handleCustomToast);
    return () => {
      window.removeEventListener('air_traffic_control_toast', handleCustomToast);
      window.removeEventListener('prox_orch_toast', handleCustomToast);
    };
  }, [addToast]);
  useEffect(() => {
    if (typeof window !== 'undefined') {
      try {
        localStorage.setItem('air_traffic_control_term_sessions', JSON.stringify(sessions));
        if (activeSessionId) {
          localStorage.setItem('air_traffic_control_term_active_id', activeSessionId);
        } else {
          localStorage.removeItem('air_traffic_control_term_active_id');
        }
      } catch {}
    }
  }, [sessions, activeSessionId]);

  const handleOpenTerminal = (targetNodeId?: string) => {
    const target = nodesList.find(n => n.id === targetNodeId) || selectedNode;
    const existing = sessions.find(s => s.nodeId === target.id);
    if (existing) {
      if (existing.isMinimized) {
        setSessions(prev => setSessionMinimized(prev, existing.id, false));
      }
      setActiveSessionId(existing.id);
    } else {
      const newSession = createTerminalSession(
        target.id,
        target.name,
        target.tailscaleIp || target.ip,
        undefined,
        sessions.length
      );
      setSessions(prev => [...prev, newSession]);
      setActiveSessionId(newSession.id);
    }
    setTerminalOpen(true);
  };

  const handleCloseTerminalSession = (id: string) => {
    const { sessions: next, nextActiveId } = closeTerminalSession(sessions, id, activeSessionId);
    setSessions(next);
    setActiveSessionId(nextActiveId);
    if (!nextActiveId) setTerminalOpen(false);
  };

  const handleNewTerminalSession = (nodeId?: string) => {
    const target = nodesList.find(n => n.id === nodeId) || selectedNode;
    const newSession = createTerminalSession(
      target.id,
      target.name,
      target.tailscaleIp || target.ip,
      undefined,
      sessions.length
    );
    setSessions(prev => [...prev, newSession]);
    setActiveSessionId(newSession.id);
    setTerminalOpen(true);
  };

  const handleMinimizeTerminalSession = (id: string) => {
    setSessions(prev => setSessionMinimized(prev, id, true));
  };

  const handleToggleMaximizeTerminalSession = (id: string) => {
    setSessions(prev => toggleSessionMaximized(prev, id));
  };

  const handleUpdateSessionPosition = (id: string, pos: DragPosition) => {
    setSessions(prev => updateSessionPosition(prev, id, pos));
  };

  const handleUpdateSessionSize = (id: string, size: WindowSize) => {
    setSessions(prev => updateSessionSize(prev, id, size));
  };

  // Unified Window Manager State for Minimizable Modals
  const [minimizedModals, setMinimizedModals] = useState<Record<string, boolean>>({});

  const handleSelectWindow = (id: string) => {
    if (id.startsWith('term-') || sessions.some(s => s.id === id)) {
      setActiveSessionId(id);
      setTerminalOpen(true);
      setSessions(prev => setSessionMinimized(prev, id, false));
    } else {
      setMinimizedModals(prev => ({ ...prev, [id]: false }));
    }
  };

  const handleRestoreWindow = (id: string) => {
    if (id.startsWith('term-') || sessions.some(s => s.id === id)) {
      setSessions(prev => setSessionMinimized(prev, id, false));
      setActiveSessionId(id);
      setTerminalOpen(true);
    } else {
      setMinimizedModals(prev => ({ ...prev, [id]: false }));
    }
  };

  const handleMinimizeWindow = (id: string) => {
    if (id.startsWith('term-') || sessions.some(s => s.id === id)) {
      handleMinimizeTerminalSession(id);
    } else {
      setMinimizedModals(prev => ({ ...prev, [id]: true }));
    }
  };

  const handleCloseWindow = (id: string) => {
    if (id.startsWith('term-') || sessions.some(s => s.id === id)) {
      handleCloseTerminalSession(id);
    } else if (id === 'modal-add-node') {
      setAddNodeOpen(false);
      setMinimizedModals(prev => ({ ...prev, [id]: false }));
    } else if (id === 'modal-create-vm') {
      setCreateVmOpen(false);
      setMinimizedModals(prev => ({ ...prev, [id]: false }));
    } else if (id === 'modal-edit-node') {
      setEditNodeTarget(null);
      setMinimizedModals(prev => ({ ...prev, [id]: false }));
    } else if (id === 'modal-run-playbook') {
      setPlaybookOpen(false);
      setMinimizedModals(prev => ({ ...prev, [id]: false }));
    } else if (id === 'modal-playbook-editor') {
      setPlaybookEditorOpen(false);
      setMinimizedModals(prev => ({ ...prev, [id]: false }));
    } else if (id === 'modal-group-vars') {
      setGroupVarsOpen(false);
      setMinimizedModals(prev => ({ ...prev, [id]: false }));
    } else if (id === 'modal-container-logs') {
      setLogsTarget(null);
      setLogsUser(undefined);
      setLogsUid(undefined);
      setMinimizedModals(prev => ({ ...prev, [id]: false }));
    }
  };

  const managedWindows: ManagedWindow[] = [
    ...sessions.map((s) => ({
      id: s.id,
      title: s.nodeName || s.title,
      icon: 'terminal',
      type: 'terminal' as const,
      isMinimized: s.isMinimized,
      isOpen: terminalOpen,
      meta: { sessionId: s.id },
    })),
    ...(addNodeOpen
      ? [
          {
            id: 'modal-add-node',
            title: 'Add Node',
            icon: 'add_circle',
            type: 'modal' as const,
            isMinimized: Boolean(minimizedModals['modal-add-node']),
            isOpen: addNodeOpen,
          },
        ]
      : []),
    ...(createVmOpen
      ? [
          {
            id: 'modal-create-vm',
            title: 'Create VM',
            icon: 'desktop_windows',
            type: 'modal' as const,
            isMinimized: Boolean(minimizedModals['modal-create-vm']),
            isOpen: createVmOpen,
          },
        ]
      : []),
    ...(editNodeTarget
      ? [
          {
            id: 'modal-edit-node',
            title: `Edit: ${editNodeTarget.name}`,
            icon: 'settings',
            type: 'modal' as const,
            isMinimized: Boolean(minimizedModals['modal-edit-node']),
            isOpen: true,
          },
        ]
      : []),
    ...(playbookOpen
      ? [
          {
            id: 'modal-run-playbook',
            title: 'Ansible: Run Playbook',
            icon: 'play_arrow',
            type: 'modal' as const,
            isMinimized: Boolean(minimizedModals['modal-run-playbook']),
            isOpen: playbookOpen,
          },
        ]
      : []),
    ...(playbookEditorOpen
      ? [
          {
            id: 'modal-playbook-editor',
            title: selectedPlaybookForEdit ? `Editor: ${selectedPlaybookForEdit}` : 'Playbook Editor',
            icon: 'menu_book',
            type: 'modal' as const,
            isMinimized: Boolean(minimizedModals['modal-playbook-editor']),
            isOpen: playbookEditorOpen,
          },
        ]
      : []),
    ...(groupVarsOpen
      ? [
          {
            id: 'modal-group-vars',
            title: 'Ansible Group Vars',
            icon: 'lock_reset',
            type: 'modal' as const,
            isMinimized: Boolean(minimizedModals['modal-group-vars']),
            isOpen: groupVarsOpen,
          },
        ]
      : []),
    ...(logsTarget
      ? [
          {
            id: 'modal-container-logs',
            title: `Logs: ${logsTarget}`,
            icon: 'receipt_long',
            type: 'modal' as const,
            isMinimized: Boolean(minimizedModals['modal-container-logs']),
            isOpen: true,
          },
        ]
      : []),
  ];

  const activeWindowId =
    terminalOpen && activeSessionId && !sessions.find((s) => s.id === activeSessionId)?.isMinimized
      ? activeSessionId
      : addNodeOpen && !minimizedModals['modal-add-node']
      ? 'modal-add-node'
      : createVmOpen && !minimizedModals['modal-create-vm']
      ? 'modal-create-vm'
      : editNodeTarget && !minimizedModals['modal-edit-node']
      ? 'modal-edit-node'
      : playbookOpen && !minimizedModals['modal-run-playbook']
      ? 'modal-run-playbook'
      : playbookEditorOpen && !minimizedModals['modal-playbook-editor']
      ? 'modal-playbook-editor'
      : groupVarsOpen && !minimizedModals['modal-group-vars']
      ? 'modal-group-vars'
      : logsTarget && !minimizedModals['modal-container-logs']
      ? 'modal-container-logs'
      : null;

  // Global Keyboard Shortcuts (Task 25 & Task 95)
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      // Ctrl+Shift+T or Ctrl+` to toggle terminal
      if ((e.ctrlKey && e.shiftKey && e.key.toLowerCase() === 't') || (e.ctrlKey && e.key === '`')) {
        e.preventDefault();
        if (terminalOpen && activeSessionId) {
          const s = sessions.find(s => s.id === activeSessionId);
          if (s && !s.isMinimized) {
            handleMinimizeTerminalSession(activeSessionId);
          } else {
            setTerminalOpen(false);
          }
        } else {
          handleOpenTerminal();
        }
      }
      // Cmd/Ctrl+K for search modal
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setSearchOpen(prev => !prev);
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [terminalOpen, activeSessionId, sessions, selectedNode, nodesList]);

  // Fetch initial nodes and tasks from API
  useEffect(() => {
    fetch('/api/nodes')
      .then(res => res.json())
      .then(data => {
        if (Array.isArray(data) && data.length > 0) {
          setNodesList(data);
          setSelectedNode(data[0]);
        }
      })
      .catch(() => {});

    fetch('/api/tasks')
      .then(res => res.json())
      .then(data => {
        if (Array.isArray(data) && data.length > 0) {
          setTasks(data);
        }
      })
      .catch(() => {});

    fetch('/api/queue')
      .then(res => res.json())
      .then(data => {
        if (data?.tasks && Array.isArray(data.tasks)) {
          setQueue(data.tasks);
          setIsQueueRunning(Boolean(data.isRunning));
        }
      })
      .catch(() => {});

    fetch('/api/inventory/chunks')
      .then(res => res.json())
      .then(data => {
        if (data?.chunks && Array.isArray(data.chunks)) {
          setChunks(data.chunks);
        } else if (Array.isArray(data)) {
          setChunks(data);
        }
      })
      .catch(() => {});

    fetch('/api/inventory/folders')
      .then(res => res.json())
      .then(data => {
        if (data?.folders && Array.isArray(data.folders)) {
          setFolders(data.folders);
        } else if (Array.isArray(data)) {
          setFolders(data);
        }
      })
      .catch(() => {});
  }, []);

  // Fetch live telemetry when selectedNode changes
  useEffect(() => {
    fetch(`/api/telemetry?host=${encodeURIComponent(selectedNode.id)}`)
      .then(res => res.json())
      .then(data => {
        if (data && typeof data.cpuUsage === 'number') {
          setTelemetry(prev => ({ ...prev, ...data }));
        }
        if (data?.node) {
          setSelectedNode(prev => {
            if (prev.id !== data.node.id) return prev;
            if (
              prev.uptime === data.node.uptime &&
              prev.status === data.node.status &&
              prev.sshConnected === data.node.sshConnected &&
              prev.specs?.cpu === data.node.specs?.cpu
            ) {
              return prev;
            }
            return { ...prev, ...data.node };
          });
          setNodesList(prev =>
            prev.map(n => {
              if (n.id !== data.node.id) return n;
              if (
                n.uptime === data.node.uptime &&
                n.status === data.node.status &&
                n.sshConnected === data.node.sshConnected &&
                n.specs?.cpu === data.node.specs?.cpu
              ) {
                return n;
              }
              return { ...n, ...data.node };
            })
          );
        }
      })
      .catch(() => {});
  }, [selectedNode.id]);

  // WebSocket Connection for real-time telemetry ticks and task progress
  useEffect(() => {
    let ws: WebSocket | null = null;
    let reconnectTimeout: any = null;

    const connectWS = () => {
      try {
        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        ws = new WebSocket(`${protocol}//${window.location.host}/ws`);

        ws.onmessage = event => {
          try {
            const msg = JSON.parse(event.data);
            if (msg.type === 'telemetry_tick' && msg.telemetry) {
              setTelemetry(prev => ({
                ...prev,
                cpuUsage: msg.telemetry.cpuUsage ?? prev.cpuUsage,
                packageWatts: msg.telemetry.packageWatts ?? prev.packageWatts,
                fanSpeedRpm: msg.telemetry.fanSpeedRpm ?? prev.fanSpeedRpm,
                cpuTempC: msg.telemetry.cpuTempC ?? prev.cpuTempC,
                sparkline: msg.telemetry.sparkline ?? prev.sparkline,
              }));
            } else if (msg.type === 'task_started') {
              setTasks(prev => [msg.task, ...prev]);
              addToast({ type: 'info', title: 'Task Started', message: msg.task.description });
            } else if (msg.type === 'task_updated') {
              setTasks(prev => prev.map(t => (t.id === msg.task.id ? msg.task : t)));
              if (msg.task.status === 'OK') {
                addToast({ type: 'success', title: 'Task Completed', message: msg.task.description });
              } else if (msg.task.status === 'FAILED') {
                addToast({ type: 'error', title: 'Task Failed', message: msg.task.description });
              }
            } else if (msg.type === 'node_added') {
              setNodesList(prev =>
                prev.some(n => n.id === msg.node.id) ? prev : [...prev, msg.node]
              );
              addToast({ type: 'success', title: 'Node Added', message: `Registered node ${msg.node.name}` });
            } else if (msg.type === 'node_updated' && msg.node) {
              setNodesList(prev => prev.map(n => (n.id === msg.node.id ? msg.node : n)));
              setSelectedNode(prev => (prev.id === msg.node.id ? msg.node : prev));
              addToast({ type: 'info', title: 'Node Updated', message: `Settings saved for ${msg.node.name}` });
            } else if (msg.type === 'node_deleted' && msg.nodeId) {
              setNodesList(prev => prev.filter(n => n.id !== msg.nodeId));
              addToast({ type: 'warning', title: 'Node Removed', message: `Deleted node ${msg.nodeId}` });
            } else if (msg.type === 'chunks_updated' && msg.chunks) {
              setChunks(Array.isArray(msg.chunks) ? msg.chunks : []);
            } else if (msg.type === 'folders_updated' && msg.folders) {
              setFolders(Array.isArray(msg.folders) ? msg.folders : []);
            } else if (msg.type === 'nodes_updated' && Array.isArray(msg.nodes)) {
              setNodesList(msg.nodes);
              setSelectedNode(prev => {
                const match = msg.nodes.find((n: NodeItem) => n.id === prev.id);
                return match || (msg.nodes.length > 0 ? msg.nodes[0] : prev);
              });
            } else if (msg.type === 'init' && msg.queue) {
              if (Array.isArray(msg.queue.tasks)) setQueue(msg.queue.tasks);
              setIsQueueRunning(Boolean(msg.queue.isRunning));
            } else if (msg.type === 'queue_updated' && msg.queue) {
              if (Array.isArray(msg.queue.tasks)) setQueue(msg.queue.tasks);
              setIsQueueRunning(Boolean(msg.queue.isRunning));
            } else if (msg.type === 'queue_execution_started') {
              setIsQueueRunning(true);
              if (msg.queue?.tasks && Array.isArray(msg.queue.tasks)) {
                setQueue(msg.queue.tasks);
              }
              addToast({
                type: 'info',
                title: 'Queue Execution Started',
                message: msg.taskId ? `Executing task ${msg.taskId}` : 'Executing next queued Ansible mutation task',
              });
            } else if (msg.type === 'queue_execution_finished') {
              setIsQueueRunning(false);
              if (msg.queue?.tasks && Array.isArray(msg.queue.tasks)) {
                setQueue(msg.queue.tasks);
              }
              if (msg.haltedOnError) {
                addToast({
                  type: 'error',
                  title: 'Queue Execution Halted',
                  message: msg.result?.error || 'Queue checkpoint failure: execution stopped to prevent damage',
                });
              } else if (msg.result?.success) {
                addToast({
                  type: 'success',
                  title: 'Queue Task Completed',
                  message: msg.result.taskId ? `Finished task ${msg.result.taskId}` : 'Task executed successfully',
                });
              }
            }
          } catch {
            // ignore JSON parse
          }
        };

        ws.onclose = () => {
          reconnectTimeout = setTimeout(connectWS, 3000);
        };
      } catch {
        reconnectTimeout = setTimeout(connectWS, 3000);
      }
    };

    connectWS();
    return () => {
      if (ws) ws.close();
      if (reconnectTimeout) clearTimeout(reconnectTimeout);
    };
  }, []);

  // Keyboard shortcut Ctrl+K / Cmd+K
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setSearchOpen(prev => !prev);
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, []);

  // Handlers
  const handleSelectNode = (node: NodeItem) => {
    setSelectedNode(node);
    setSelectedViewMode('nodes');
    setMobileSidebarOpen(false);
  };

  const handleAddNode = (newNode: Partial<NodeItem>) => {
    fetch('/api/nodes', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(newNode),
    })
      .then(res => res.json())
      .then(data => {
        if (data?.node) {
          setNodesList(prev =>
            prev.some(n => n.id === data.node.id) ? prev : [...prev, data.node]
          );
          setSelectedNode(data.node);
        }
      })
      .catch(() => {});
  };

  const handleSaveNodeSettings = async (
    nodeId: string,
    updates: Partial<NodeItem> & { testSsh?: boolean }
  ) => {
    setNodesList(prev =>
      prev.map(n => (n.id === nodeId ? updateNodeSettings(n, updates) : n))
    );
    setSelectedNode(prev =>
      prev.id === nodeId ? updateNodeSettings(prev, updates) : prev
    );

    try {
      const resp = await fetch(`/api/nodes/${encodeURIComponent(nodeId)}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(updates),
      });
      if (resp.ok) {
        const data = await resp.json();
        if (data?.node) {
          setNodesList(prev => prev.map(n => (n.id === nodeId ? data.node : n)));
          setSelectedNode(prev => (prev.id === nodeId ? data.node : prev));
        }
      }
    } catch {
      // Local state already updated
    }
  };

  const handleDeleteNode = (nodeId: string) => {
    setNodesList(prev => {
      const remaining = prev.filter(n => n.id !== nodeId);
      if (selectedNode.id === nodeId && remaining.length > 0) {
        setSelectedNode(remaining[0]);
      }
      return remaining;
    });
    fetch(`/api/nodes/${encodeURIComponent(nodeId)}`, { method: 'DELETE' }).catch(() => {});
  };

  const handleCreateFolder = async (name: string) => {
    try {
      const res = await fetch('/api/inventory/folders', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name }),
      });
      if (res.ok) {
        const data = await res.json();
        if (data?.folder) {
          setFolders(prev => [...prev.filter(f => f.id !== data.folder.id), data.folder]);
        }
        addToast({
          type: 'success',
          title: 'Folder Created',
          message: `Created custom folder "${name}"`,
        });
      }
    } catch (err) {
      console.error('Failed to create folder:', err);
    }
  };

  const handleDeleteFolder = async (id: string) => {
    try {
      const res = await fetch(`/api/inventory/folders/${encodeURIComponent(id)}`, {
        method: 'DELETE',
      });
      if (res.ok) {
        setFolders(prev => prev.filter(f => f.id !== id));
        addToast({
          type: 'info',
          title: 'Folder Deleted',
          message: 'Deleted custom folder',
        });
      }
    } catch (err) {
      console.error('Failed to delete folder:', err);
    }
  };

  const handleFollowTag = useCallback((chunk: { tag: string; authMethod: string; sshUser?: string; vaultSecretId?: string }) => {
    fetch('/api/inventory/chunks')
      .then(res => res.json())
      .then(data => {
        if (data?.chunks && Array.isArray(data.chunks)) {
          setChunks(data.chunks);
        }
      })
      .catch(() => {});
    fetch('/api/nodes')
      .then(res => res.json())
      .then(data => {
        if (Array.isArray(data)) {
          setNodesList(data);
        }
      })
      .catch(() => {});
    addToast({
      type: 'success',
      title: 'Tag Followed',
      message: `Now dynamically following ${chunk.tag}`,
    });
  }, [addToast]);

  const handleRunPlaybook = (
    playbook: string,
    target: string,
    checkMode = false,
    tags?: string,
    extraVars?: string
  ) => {
    fetch('/api/playbook/run', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ playbook, target, checkMode, tags, extraVars }),
    }).catch(() => {});
  };

  const handleTriggerRestic = () => {
    fetch('/api/restic/snapshot', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ host: selectedNode.tailscaleHost }),
    }).catch(() => {});
  };

  const handleRestartPod = (podName: string) => {
    const restartTask: GlobalTaskItem = {
      id: `task-${Date.now()}`,
      startTime: new Date().toISOString().replace('T', ' ').substring(0, 19),
      nodeTarget: selectedNode.tailscaleHost,
      user: `${selectedNode.sshUser || 'core'}@ssh-agent`,
      description: `podman action: ${podName}`,
      progress: 100,
      progressEta: 'Completed via SSH',
      status: 'OK',
      category: 'active',
    };
    setTasks(prev => [restartTask, ...prev]);
  };

  const handleRebootConfirm = (mode: string) => {
    fetch(`/api/nodes/${encodeURIComponent(selectedNode.id)}/reboot`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ mode }),
    })
      .then(res => res.json())
      .then(data => {
        if (data?.task) {
          setTasks(prev => [data.task, ...prev.filter(t => t.id !== data.task.id)]);
        }
      })
      .catch(() => {
        const rebootTask: GlobalTaskItem = {
          id: `task-${Date.now()}`,
          startTime: new Date().toISOString().replace('T', ' ').substring(0, 19),
          nodeTarget: selectedNode.tailscaleHost,
          user: `${selectedNode.sshUser || 'core'}@ssh-agent`,
          description: `reboot sequence initiated (mode: ${mode})`,
          progress: 50,
          progressEta: 'Broadcasting ACPI reboot signal',
          status: 'RUNNING',
          category: 'active',
        };
        setTasks(prev => [rebootTask, ...prev]);
      });
  };

  // Drag-and-drop panel order state
  const [panelOrder, setPanelOrder] = useState<string[]>([
    'telemetry_mesh',
    'containers',
    'vms_restic',
    'identities',
  ]);
  const [draggedPanel, setDraggedPanel] = useState<string | null>(null);

  // Section density / contrast toggle
  const [highContrast, setHighContrast] = useState(true);

  // Cockpit sub-sidebar width resizer state
  const [subSidebarWidth, setSubSidebarWidth] = useState<'normal' | 'wide' | 'compact'>('normal');

  const handleDragStart = (id: string) => {
    setDraggedPanel(id);
  };

  const handleDragOver = (e: React.DragEvent) => {
    e.preventDefault();
  };

  const handleDrop = (targetId: string) => {
    if (!draggedPanel || draggedPanel === targetId) return;
    setPanelOrder(prev => {
      const next = [...prev];
      const fromIdx = next.indexOf(draggedPanel);
      const toIdx = next.indexOf(targetId);
      if (fromIdx !== -1 && toIdx !== -1) {
        next.splice(fromIdx, 1);
        next.splice(toIdx, 0, draggedPanel);
      }
      return next;
    });
    setDraggedPanel(null);
  };

  const movePanel = (id: string, direction: 'up' | 'down') => {
    setPanelOrder(prev => {
      const idx = prev.indexOf(id);
      if (idx === -1) return prev;
      const targetIdx = direction === 'up' ? idx - 1 : idx + 1;
      if (targetIdx < 0 || targetIdx >= prev.length) return prev;
      const next = [...prev];
      const [item] = next.splice(idx, 1);
      next.splice(targetIdx, 0, item);
      return next;
    });
  };

  return (
    <div className={`min-h-screen selection:bg-[#395d9f] selection:text-white font-sans ${
      highContrast ? 'bg-[#080a16] text-[#f0f2ff]' : 'bg-[#0f1224] text-[#dfe1fa]'
    }`}>
      {/* Top Header */}
      <Header
        onOpenAddNode={() => setAddNodeOpen(true)}
        onOpenPlaybook={() => setPlaybookOpen(true)}
        onOpenPlaybookEditor={() => handleOpenPlaybookEditor()}
        onOpenSearch={() => setSearchOpen(true)}
        onOpenGroupVars={() => setGroupVarsOpen(true)}
        onToggleSidebar={() => setMobileSidebarOpen(!mobileSidebarOpen)}
        isSidebarOpen={mobileSidebarOpen}
        onlineCount={nodesList.filter(n => n.status === 'online').length}
        totalCount={nodesList.length}
        theme={themeChoice}
        resolvedTheme={resolvedTheme}
        onToggleTheme={handleToggleTheme}
        onSelectTheme={handleSelectTheme}
        onOpenClientSettings={() => setClientSettingsOpen(true)}
      />

      {/* Left Server View Sidebar */}
      <ServerViewSidebar
        nodes={nodesList}
        selectedNodeId={selectedNode.id}
        onSelectNode={handleSelectNode}
        onEditNode={node => setEditNodeTarget(node)}
        selectedHostGroup={selectedHostGroup}
        onSelectHostGroup={setSelectedHostGroup}
        isOpenMobile={mobileSidebarOpen}
        onCloseMobile={() => setMobileSidebarOpen(false)}
        viewMode={selectedViewMode}
        onSelectViewMode={setSelectedViewMode}
        selectedDatacenterSection={datacenterSection}
        onSelectDatacenterSection={setDatacenterSection}
        chunks={chunks}
        folders={folders}
        onCreateFolder={handleCreateFolder}
        onDeleteFolder={handleDeleteFolder}
      />

      {/* Main Content Workspace */}
      <div className="lg:pl-72 pl-0">
        <main className={`relative pt-12 pb-48 w-full px-3 min-h-screen ${
          highContrast ? 'bg-[#080a16]' : 'bg-[#0f1224]'
        }`}>
          {selectedViewMode === 'datacenter' ? (
            <DatacenterGlobalView
              datacenterName={selectedNode?.datacenter || 'leo-banjo.ts.net'}
              nodes={nodesList}
              tasks={tasks}
              activeSection={datacenterSection}
              onSelectNode={handleSelectNode}
              onOpenPlaybookModal={playbookName => {
                setSelectedPlaybookForEdit(playbookName);
                setPlaybookOpen(true);
              }}
              onOpenPlaybookEditor={playbookName => handleOpenPlaybookEditor(playbookName)}
              onOpenGroupVars={() => setGroupVarsOpen(true)}
              onToggleTasksDrawer={() => {
                const el = document.getElementById('global-tasks-drawer');
                el?.scrollIntoView({ behavior: 'smooth' });
              }}
              onRunPlaybookQuick={(playbook, target) => handleRunPlaybook(playbook, target || 'tag:host')}
            />
          ) : (
            <div className="flex flex-col w-full">
            {/* Secondary Context Bar & System Breadcrumb */}
            <div className={`flex flex-col shadow-md rounded-t border ${
              highContrast ? 'bg-[#121528] border-[#383c54]' : 'bg-[#171a2d] border-[#26293c]'
            }`}>
              <NodeBreadcrumbBar
                node={selectedNode}
                onOpenTerminal={() => handleOpenTerminal()}
                onTriggerRestic={handleTriggerRestic}
                onReboot={() => setRebootModalOpen(true)}
                onPowerOff={() => setRebootModalOpen(true)}
                onEditNode={() => setEditNodeTarget(selectedNode)}
              />

              <div className="flex items-center justify-between border-t border-[#26293c] px-3 py-1.5 bg-[#121528] text-xs font-mono">
                <div className="flex items-center gap-2 text-[#dfe1fa]">
                  <span className="material-symbols-outlined text-[#8cd6cb] text-[16px]">tune</span>
                  <span className="font-bold text-[#b0c9ff]">ACTIVE COCKPIT SECTION:</span>
                  <span className="px-2 py-0.5 rounded bg-[#26293c] text-[#8cd6cb] font-bold border border-[#8cd6cb]/30 uppercase tracking-wider text-[11px]">
                    {activeCockpitItem}
                  </span>
                </div>

                {/* UX Controls: High Contrast & Sidebar Resizing */}
                <div className="flex items-center gap-1.5 font-mono text-[10px] text-[#8d909b] shrink-0">
                  <button
                    type="button"
                    onClick={() => setHighContrast(!highContrast)}
                    className="px-2 py-0.5 rounded bg-[#1f2238] hover:bg-[#2c304d] text-[#b0c9ff] cursor-pointer border border-[#3b405e]"
                    title="Toggle high contrast UI"
                  >
                    Contrast: {highContrast ? 'HIGH' : 'STANDARD'}
                  </button>
                  <button
                    type="button"
                    onClick={() => {
                      setSubSidebarWidth(w => (w === 'normal' ? 'wide' : w === 'wide' ? 'compact' : 'normal'));
                    }}
                    className="px-2 py-0.5 rounded bg-[#1f2238] hover:bg-[#2c304d] text-[#8cd6cb] cursor-pointer border border-[#3b405e]"
                    title="Resize Sub-sidebar panel"
                  >
                    Sidebar: {subSidebarWidth.toUpperCase()}
                  </button>
                </div>
              </div>
            </div>

            {/* Cockpit Nested Split Layout: Sub-Sidebar + Main Control Workspace */}
            <div className="grid grid-cols-12 gap-2 mt-2 w-full items-start">
              {/* Cockpit Sub-Sidebar with Resizable Width */}
              <div className={getSubSidebarWrapperClasses(subSidebarWidth)}>
                <CockpitSubSidebar
                  node={selectedNode}
                  activeItem={activeCockpitItem}
                  storageEngine={nodeStorageEngines[selectedNode.id]}
                  onSelectItem={item => {
                    setActiveCockpitItem(item);
                    setActiveOpTab(item);
                  }}
                  onEditNode={() => setEditNodeTarget(selectedNode)}
                />
              </div>

              {/* Main Operational Workspace Grid */}
              <div className={getWorkspaceWrapperClasses(subSidebarWidth)}>
                {/* Specific dedicated Views when selected from Menu */}
                {(activeOpTab === 'logs' || activeCockpitItem === 'logs') && (
                  <SystemLogsView node={selectedNode} />
                )}

                {(activeOpTab === 'updates' || activeCockpitItem === 'updates') && (
                  <PackageUpdatesView
                    node={selectedNode}
                    onOpenTask={taskId => {
                      setLogsTarget(taskId);
                    }}
                  />
                )}

                {activeOpTab === 'interfaces' && (
                  <NetworkInterfacesView node={selectedNode} />
                )}

                {activeOpTab === 'firewall' && (
                  <FirewallView node={selectedNode} />
                )}

                {(activeOpTab === 'zfs' || activeCockpitItem === 'zfs' || activeOpTab === 'storage' || activeCockpitItem === 'storage') && (
                  <ZfsManagementView
                    node={selectedNode}
                    onEngineDetected={engine => {
                      setNodeStorageEngines(prev => ({ ...prev, [selectedNode.id]: engine }));
                    }}
                  />
                )}

                {activeOpTab === 'shares' && (
                  <NetworkSharesView node={selectedNode} />
                )}

                {activeOpTab === 'smart' && (
                  <DisksSmartView node={selectedNode} />
                )}

                {activeOpTab === 'tailscale' && (
                  <TailscaleExplorerView
                    onOpenTerminalForHost={ip => {
                      const match = nodesList.find(n => n.tailscaleIp === ip || n.ip === ip);
                      handleOpenTerminal(match?.id);
                    }}
                  />
                )}

                {activeOpTab === 'metrics' && (
                  <SystemHealthTelemetry telemetry={telemetry} />
                )}

                {(activeOpTab === 'containers' || activeOpTab === 'pods') && (
                  <PodmanManager
                    selectedNode={selectedNode}
                    onOpenLogs={handleOpenLogs}
                    onOpenExec={() => setTerminalOpen(true)}
                    onRestartPod={podName => handleRunPlaybook('site.yml', podName)}
                  />
                )}

                {activeOpTab === 'vms' && (
                  <VirtualMachinesKVM
                    node={selectedNode}
                    onOpenConsole={() => setTerminalOpen(true)}
                    onNewVM={() => setCreateVmOpen(true)}
                  />
                )}

                {activeOpTab === 'restic' && (
                  <ResticBackupPolicy
                    nodeName={selectedNode.name}
                    hasRestic={selectedNode.hasRestic}
                    onIntegrityCheck={() =>
                      handleRunPlaybook('security-hardening.yml', selectedNode.tailscaleHost)
                    }
                    onBrowseFiles={() => setLogsTarget('restic-repo-browser')}
                    onSnapshot={handleTriggerRestic}
                  />
                )}

                {activeOpTab === 'identities' && (
                  <ServiceIdentitiesTable
                    selectedNode={selectedNode}
                    onAudit={target => setLogsTarget(target || 'pam-audit-log')}
                  />
                )}

                {/* Default Overview / Reorderable Interactive Workspace */}
                {activeOpTab === 'overview' && (
                  <div className="flex flex-col gap-2">
                    {panelOrder.map((panelId, idx) => {
                      if (panelId === 'telemetry_mesh') {
                        return (
                          <div
                            key="telemetry_mesh"
                            draggable
                            onDragStart={() => handleDragStart('telemetry_mesh')}
                            onDragOver={handleDragOver}
                            onDrop={() => handleDrop('telemetry_mesh')}
                            className={`rounded-lg border transition-all ${
                              draggedPanel === 'telemetry_mesh' ? 'opacity-40 border-dashed border-[#b0c9ff]' : ''
                            } ${highContrast ? 'border-[#313447]' : 'border-[#26293c]'}`}
                          >
                            <div className="bg-[#121528] px-2 py-1 flex items-center justify-between border-b border-[#26293c] text-[10px] font-mono text-[#8d909b]">
                              <span className="flex items-center gap-1 cursor-grab active:cursor-grabbing font-bold text-[#b0c9ff]">
                                <span className="material-symbols-outlined text-[13px]">drag_indicator</span>
                                TELEMETRY &amp; TAILSCALE MESH (Drag to reorder)
                              </span>
                              <div className="flex items-center gap-1">
                                {idx > 0 && (
                                  <button
                                    type="button"
                                    onClick={() => movePanel('telemetry_mesh', 'up')}
                                    className="p-0.5 hover:text-[#dfe1fa] cursor-pointer"
                                    title="Move section up"
                                  >
                                    ▲
                                  </button>
                                )}
                                {idx < panelOrder.length - 1 && (
                                  <button
                                    type="button"
                                    onClick={() => movePanel('telemetry_mesh', 'down')}
                                    className="p-0.5 hover:text-[#dfe1fa] cursor-pointer"
                                    title="Move section down"
                                  >
                                    ▼
                                  </button>
                                )}
                              </div>
                            </div>
                            <div className="grid grid-cols-1 md:grid-cols-12 gap-2 p-1">
                              <SystemHealthTelemetry telemetry={telemetry} />
                              <TailscaleMeshCard
                                node={selectedNode}
                                peers={nodesList}
                                onReauth={() => setTerminalOpen(true)}
                                onEditNode={() => setEditNodeTarget(selectedNode)}
                              />
                            </div>
                          </div>
                        );
                      }

                      if (panelId === 'containers') {
                        return (
                          <div
                            key="containers"
                            draggable
                            onDragStart={() => handleDragStart('containers')}
                            onDragOver={handleDragOver}
                            onDrop={() => handleDrop('containers')}
                            className={`rounded-lg border transition-all ${
                              draggedPanel === 'containers' ? 'opacity-40 border-dashed border-[#b0c9ff]' : ''
                            } ${highContrast ? 'border-[#313447]' : 'border-[#26293c]'}`}
                          >
                            <div className="bg-[#121528] px-2 py-1 flex items-center justify-between border-b border-[#26293c] text-[10px] font-mono text-[#8d909b]">
                              <span className="flex items-center gap-1 cursor-grab active:cursor-grabbing font-bold text-[#d9b9ff]">
                                <span className="material-symbols-outlined text-[13px]">drag_indicator</span>
                                PODMAN CONTAINERS &amp; PODS (Drag to reorder)
                              </span>
                              <div className="flex items-center gap-1">
                                {idx > 0 && (
                                  <button
                                    type="button"
                                    onClick={() => movePanel('containers', 'up')}
                                    className="p-0.5 hover:text-[#dfe1fa] cursor-pointer"
                                    title="Move section up"
                                  >
                                    ▲
                                  </button>
                                )}
                                {idx < panelOrder.length - 1 && (
                                  <button
                                    type="button"
                                    onClick={() => movePanel('containers', 'down')}
                                    className="p-0.5 hover:text-[#dfe1fa] cursor-pointer"
                                    title="Move section down"
                                  >
                                    ▼
                                  </button>
                                )}
                              </div>
                            </div>
                            <div className="p-1">
                              <PodmanManager
                                selectedNode={selectedNode}
                                onOpenLogs={handleOpenLogs}
                                onOpenExec={() => {
                                  setLogsTarget(null);
                                  setLogsUser(undefined);
                                  setLogsUid(undefined);
                                  setTerminalOpen(true);
                                }}
                                onRestartPod={handleRestartPod}
                              />
                            </div>
                          </div>
                        );
                      }

                      if (panelId === 'vms_restic') {
                        return (
                          <div
                            key="vms_restic"
                            draggable
                            onDragStart={() => handleDragStart('vms_restic')}
                            onDragOver={handleDragOver}
                            onDrop={() => handleDrop('vms_restic')}
                            className={`rounded-lg border transition-all ${
                              draggedPanel === 'vms_restic' ? 'opacity-40 border-dashed border-[#b0c9ff]' : ''
                            } ${highContrast ? 'border-[#313447]' : 'border-[#26293c]'}`}
                          >
                            <div className="bg-[#121528] px-2 py-1 flex items-center justify-between border-b border-[#26293c] text-[10px] font-mono text-[#8d909b]">
                              <span className="flex items-center gap-1 cursor-grab active:cursor-grabbing font-bold text-[#8cd6cb]">
                                <span className="material-symbols-outlined text-[13px]">drag_indicator</span>
                                VIRTUAL MACHINES &amp; RESTIC BACKUP POLICY (Drag to reorder)
                              </span>
                              <div className="flex items-center gap-1">
                                {idx > 0 && (
                                  <button
                                    type="button"
                                    onClick={() => movePanel('vms_restic', 'up')}
                                    className="p-0.5 hover:text-[#dfe1fa] cursor-pointer"
                                    title="Move section up"
                                  >
                                    ▲
                                  </button>
                                )}
                                {idx < panelOrder.length - 1 && (
                                  <button
                                    type="button"
                                    onClick={() => movePanel('vms_restic', 'down')}
                                    className="p-0.5 hover:text-[#dfe1fa] cursor-pointer"
                                    title="Move section down"
                                  >
                                    ▼
                                  </button>
                                )}
                              </div>
                            </div>
                            <div className="grid grid-cols-1 md:grid-cols-12 gap-2 p-1">
                              <VirtualMachinesKVM
                                node={selectedNode}
                                onOpenConsole={() => setTerminalOpen(true)}
                                onNewVM={() => setCreateVmOpen(true)}
                              />
                              <ResticBackupPolicy
                                nodeName={selectedNode.name}
                                hasRestic={selectedNode.hasRestic}
                                onIntegrityCheck={() =>
                                  handleRunPlaybook('security-hardening.yml', selectedNode.tailscaleHost)
                                }
                                onBrowseFiles={() => setLogsTarget('restic-repo-browser')}
                                onSnapshot={handleTriggerRestic}
                              />
                            </div>
                          </div>
                        );
                      }

                      if (panelId === 'identities') {
                        return (
                          <div
                            key="identities"
                            draggable
                            onDragStart={() => handleDragStart('identities')}
                            onDragOver={handleDragOver}
                            onDrop={() => handleDrop('identities')}
                            className={`rounded-lg border transition-all ${
                              draggedPanel === 'identities' ? 'opacity-40 border-dashed border-[#b0c9ff]' : ''
                            } ${highContrast ? 'border-[#313447]' : 'border-[#26293c]'}`}
                          >
                            <div className="bg-[#121528] px-2 py-1 flex items-center justify-between border-b border-[#26293c] text-[10px] font-mono text-[#8d909b]">
                              <span className="flex items-center gap-1 cursor-grab active:cursor-grabbing font-bold text-[#ffd97d]">
                                <span className="material-symbols-outlined text-[13px]">drag_indicator</span>
                                SERVICE USERS &amp; SYSTEM IDENTITIES (Drag to reorder)
                              </span>
                              <div className="flex items-center gap-1">
                                {idx > 0 && (
                                  <button
                                    type="button"
                                    onClick={() => movePanel('identities', 'up')}
                                    className="p-0.5 hover:text-[#dfe1fa] cursor-pointer"
                                    title="Move section up"
                                  >
                                    ▲
                                  </button>
                                )}
                                {idx < panelOrder.length - 1 && (
                                  <button
                                    type="button"
                                    onClick={() => movePanel('identities', 'down')}
                                    className="p-0.5 hover:text-[#dfe1fa] cursor-pointer"
                                    title="Move section down"
                                  >
                                    ▼
                                  </button>
                                )}
                              </div>
                            </div>
                            <div className="p-1">
                              <ServiceIdentitiesTable
                                selectedNode={selectedNode}
                                onAudit={target => setLogsTarget(target || 'pam-audit-log')}
                              />
                            </div>
                          </div>
                        );
                      }

                      return null;
                    })}
                  </div>
                )}
              </div>
            </div>
          </div>
        )}
      </main>
      </div>

      {/* Fixed Bottom Console with Unified Window Dock */}
      <GlobalTasksExecutionLog
        tasks={tasks}
        queue={queue}
        isRunning={isQueueRunning}
        activeDrawerTab={activeConsoleTab}
        onDrawerTabChange={setActiveConsoleTab}
        onOpenPlaybookEditor={() => handleOpenPlaybookEditor()}
        windows={managedWindows}
        activeWindowId={activeWindowId}
        onSelectWindow={handleSelectWindow}
        onRestoreWindow={handleRestoreWindow}
        onMinimizeWindow={handleMinimizeWindow}
        onCloseWindow={handleCloseWindow}
      />

      {/* Interactive Modals */}
      <TerminalModal
        sessions={sessions}
        activeSessionId={activeSessionId}
        nodes={nodesList}
        isOpen={terminalOpen && !!activeSessionId}
        onClose={() => setTerminalOpen(false)}
        onSelectSession={setActiveSessionId}
        onCloseSession={handleCloseTerminalSession}
        onNewSession={handleNewTerminalSession}
        onMinimizeSession={handleMinimizeTerminalSession}
        onToggleMaximizeSession={handleToggleMaximizeTerminalSession}
        onUpdateSessionPosition={handleUpdateSessionPosition}
        onUpdateSessionSize={handleUpdateSessionSize}
      />

      <AddNodeModal
        isOpen={addNodeOpen}
        isMinimized={Boolean(minimizedModals['modal-add-node'])}
        onMinimize={() => setMinimizedModals(prev => ({ ...prev, 'modal-add-node': true }))}
        onClose={() => {
          setAddNodeOpen(false);
          setMinimizedModals(prev => ({ ...prev, 'modal-add-node': false }));
        }}
        onAddNode={handleAddNode}
        onFollowTag={handleFollowTag}
      />

      <CreateVmModal
        node={selectedNode}
        nodes={nodesList}
        isOpen={createVmOpen}
        isMinimized={Boolean(minimizedModals['modal-create-vm'])}
        onMinimize={() => setMinimizedModals(prev => ({ ...prev, 'modal-create-vm': true }))}
        onClose={() => {
          setCreateVmOpen(false);
          setMinimizedModals(prev => ({ ...prev, 'modal-create-vm': false }));
        }}
        onCreated={(newVm) => {
          addToast({
            type: 'success',
            title: 'Virtual Machine Created',
            message: `VM ${newVm.name} successfully provisioned on ${selectedNode.name}`,
          });
        }}
      />

      <EditNodeModal
        node={editNodeTarget}
        isOpen={Boolean(editNodeTarget)}
        isMinimized={Boolean(minimizedModals['modal-edit-node'])}
        onMinimize={() => setMinimizedModals(prev => ({ ...prev, 'modal-edit-node': true }))}
        onClose={() => {
          setEditNodeTarget(null);
          setMinimizedModals(prev => ({ ...prev, 'modal-edit-node': false }));
        }}
        onSaveNode={handleSaveNodeSettings}
        onDeleteNode={handleDeleteNode}
      />

      <RunPlaybookModal
        isOpen={playbookOpen}
        initialPlaybook={selectedPlaybookForEdit}
        isMinimized={Boolean(minimizedModals['modal-run-playbook'])}
        onMinimize={() => setMinimizedModals(prev => ({ ...prev, 'modal-run-playbook': true }))}
        onClose={() => {
          setPlaybookOpen(false);
          setMinimizedModals(prev => ({ ...prev, 'modal-run-playbook': false }));
        }}
        onRunPlaybook={handleRunPlaybook}
        onOpenEditor={(playbook) => {
          setPlaybookOpen(false);
          handleOpenPlaybookEditor(playbook);
        }}
      />

      <PlaybookEditorModal
        isOpen={playbookEditorOpen}
        initialPlaybook={selectedPlaybookForEdit}
        isMinimized={Boolean(minimizedModals['modal-playbook-editor'])}
        onMinimize={() => setMinimizedModals(prev => ({ ...prev, 'modal-playbook-editor': true }))}
        onClose={() => {
          setPlaybookEditorOpen(false);
          setMinimizedModals(prev => ({ ...prev, 'modal-playbook-editor': false }));
        }}
        onRunPlaybook={(playbook) => {
          setPlaybookEditorOpen(false);
          setSelectedPlaybookForEdit(playbook);
          setPlaybookOpen(true);
        }}
      />

      <ContainerLogsModal
        targetName={logsTarget}
        node={selectedNode}
        user={logsUser}
        uid={logsUid}
        isOpen={Boolean(logsTarget)}
        isMinimized={Boolean(minimizedModals['modal-container-logs'])}
        onMinimize={() => setMinimizedModals(prev => ({ ...prev, 'modal-container-logs': true }))}
        onClose={() => {
          setLogsTarget(null);
          setLogsUser(undefined);
          setLogsUid(undefined);
          setMinimizedModals(prev => ({ ...prev, 'modal-container-logs': false }));
        }}
      />

      <RebootConfirmModal
        node={selectedNode}
        isOpen={rebootModalOpen}
        onClose={() => setRebootModalOpen(false)}
        onConfirmReboot={handleRebootConfirm}
      />

      <SearchModal
        isOpen={searchOpen}
        onClose={() => setSearchOpen(false)}
        nodes={nodesList}
        onSelectNode={handleSelectNode}
        onOpenPlaybook={() => setPlaybookOpen(true)}
        onOpenPlaybookEditor={() => handleOpenPlaybookEditor()}
        onOpenTerminal={() => setTerminalOpen(true)}
      />

      <GroupVarsModal
        isOpen={groupVarsOpen}
        isMinimized={Boolean(minimizedModals['modal-group-vars'])}
        onMinimize={() => setMinimizedModals(prev => ({ ...prev, 'modal-group-vars': true }))}
        onClose={() => {
          setGroupVarsOpen(false);
          setMinimizedModals(prev => ({ ...prev, 'modal-group-vars': false }));
        }}
      />

      <ClientSettingsModal
        isOpen={clientSettingsOpen}
        onClose={() => setClientSettingsOpen(false)}
        themeChoice={themeChoice}
        onSelectTheme={handleSelectTheme}
        resolvedTheme={resolvedTheme}
        accentColor={accentColor}
        onSelectAccentColor={setAccentColor}
        highContrast={highContrast}
        onToggleHighContrast={() => setHighContrast(prev => !prev)}
        reducedMotion={reducedMotion}
        onToggleReducedMotion={() => setReducedMotion(prev => !prev)}
      />

      <ToastNotification
        toasts={toasts}
        onDismiss={handleDismissToast}
      />
    </div>
  );
}
