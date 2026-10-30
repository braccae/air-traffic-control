import React, { useState, useEffect, useCallback } from 'react';
import type { NodeItem, TelemetryData, GlobalTaskItem, QueuedAnsibleTask, TagInventoryChunk, CustomFolder } from './types/index.ts';
import { Header } from './components/Header.tsx';
import { ServerViewSidebar } from './components/ServerViewSidebar.tsx';
import { findLocalNode } from './utils/nodeTarget.ts';
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
import { SystemServicesView } from './components/SystemServicesView.tsx';
import { HostModulesView } from './components/HostModulesView.tsx';
import { getPluginView } from './plugins/views/index.ts';
import { ToastNotification, type ToastItem } from './components/ToastNotification.tsx';

// Modals
import { TerminalModal } from './components/modals/TerminalModal.tsx';
import type { ManagedWindow } from './utils/windowManager.ts';
import { TailscaleExplorerView } from './components/TailscaleExplorerView.tsx';
import { AddNodeModal } from './components/modals/AddNodeModal.tsx';
import { CreateVmModal } from './components/modals/CreateVmModal.tsx';
import { EditNodeModal } from './components/modals/EditNodeModal.tsx';
import { RunPlaybookModal } from './components/modals/RunPlaybookModal.tsx';
import { ContainerLogsModal } from './components/modals/ContainerLogsModal.tsx';
import { RebootConfirmModal } from './components/modals/RebootConfirmModal.tsx';
import { SearchModal } from './components/modals/SearchModal.tsx';
import { GroupVarsModal } from './components/modals/GroupVarsModal.tsx';
import { ClientSettingsModal } from './components/modals/ClientSettingsModal.tsx';
import { DatacenterGlobalView } from './components/DatacenterGlobalView.tsx';
import { DatacenterSettingsView } from './components/DatacenterSettingsView.tsx';
import {
  updateNodeSettings,
  getSubSidebarWrapperClasses,
  getWorkspaceWrapperClasses,
  DEFAULT_DATACENTER_ROUTE,
  formatDatacenterRoute,
  parseDatacenterRoute,
  type SidebarViewMode,
  type DatacenterSection,
  type DatacenterRoute,
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

const initialNodes: NodeItem[] = []; // Removed hardcoded nodes - now loaded dynamically from API

const initialTelemetry: TelemetryData = {
  cpuUsage: null,
  cpuModel: null,
  vCpus: null,
  memoryUsedGiB: null,
  memoryTotalGiB: null,
  arcCacheGiB: null,
  zfsTankUsedTb: null,
  zfsTankTotalTb: null,
  zfsRaidType: null,
  zfsScrubStatus: null,
  loadAvg1m: null,
  loadAvg5m: null,
  loadAvg15m: null,
  ioDelayPercent: null,
  cpuTempC: null,
  fanSpeedRpm: null,
  packageWatts: null,
  sparkline: [],
};

const initialTasks: GlobalTaskItem[] = [];

export default function App() {
  const [nodesList, setNodesList] = useState<NodeItem[]>([]);
  const [selectedNode, setSelectedNode] = useState<NodeItem | null>(null);
  const [selectedHostGroup, setSelectedHostGroup] = useState('all');
  const [activeTopTab, setActiveTopTab] = useState('summary');
  const [activeOpTab, setActiveOpTab] = useState('overview');
  const [activeCockpitItem, setActiveCockpitItem] = useState('overview');
  // Deep-linkable datacenter routes (#/datacenter/<section> and
  // #/datacenter/settings/<category>/<setting>) — the hash is the source of
  // truth on load and on back/forward navigation.
  const [selectedViewMode, setSelectedViewMode] = useState<SidebarViewMode>(() =>
    parseDatacenterRoute(typeof window !== 'undefined' ? window.location.hash : '') ? 'datacenter' : 'nodes'
  );
  const [datacenterRoute, setDatacenterRoute] = useState<DatacenterRoute>(
    () => parseDatacenterRoute(typeof window !== 'undefined' ? window.location.hash : '') ?? DEFAULT_DATACENTER_ROUTE
  );
  const datacenterSection: DatacenterSection =
    datacenterRoute.kind === 'section' ? datacenterRoute.section : 'overview';
  const activeDatacenterSetting = datacenterRoute.kind === 'setting' ? datacenterRoute.setting : null;

  const writeDatacenterHash = (route: DatacenterRoute | null) => {
    if (typeof window === 'undefined') return;
    const nextHash = route ? formatDatacenterRoute(route) : '';
    const current = window.location.hash;
    if (route) {
      if (current !== nextHash) window.history.pushState(null, '', nextHash);
    } else if (current) {
      window.history.pushState(null, '', window.location.pathname + window.location.search);
    }
  };

  const navigateDatacenterRoute = (route: DatacenterRoute) => {
    setDatacenterRoute(route);
    setSelectedViewMode('datacenter');
    writeDatacenterHash(route);
  };

  const handleSelectViewMode = (mode: SidebarViewMode) => {
    setSelectedViewMode(mode);
    if (mode === 'datacenter') {
      setDatacenterRoute(DEFAULT_DATACENTER_ROUTE);
      writeDatacenterHash(DEFAULT_DATACENTER_ROUTE);
    } else {
      writeDatacenterHash(null);
    }
  };

  useEffect(() => {
    const syncRouteFromHash = () => {
      const route = parseDatacenterRoute(window.location.hash);
      if (route) {
        setDatacenterRoute(route);
        setSelectedViewMode('datacenter');
      } else {
        setSelectedViewMode('nodes');
      }
    };
    window.addEventListener('hashchange', syncRouteFromHash);
    window.addEventListener('popstate', syncRouteFromHash);
    return () => {
      window.removeEventListener('hashchange', syncRouteFromHash);
      window.removeEventListener('popstate', syncRouteFromHash);
    };
  }, []);

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

  // The playbook editor now lives in the datacenter settings submenu; the old
  // popup modal duplicated it and has been retired.
  const handleOpenPlaybookEditor = () => {
    navigateDatacenterRoute({ kind: 'setting', setting: 'playbook-editor' });
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
    if (!target) return;
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
    if (!target) return;
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
        if (Array.isArray(data)) {
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
    if (!selectedNode) return;
    fetch(`/api/telemetry?host=${encodeURIComponent(selectedNode.id)}`)
      .then(res => res.json())
      .then(data => {
        if (data && typeof data.cpuUsage === 'number') {
          setTelemetry(prev => ({ ...prev, ...data }));
        }
        if (data?.node) {
          setSelectedNode(prev => {
            if (!prev || prev.id !== data.node.id) return prev || data.node;
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
  }, [selectedNode?.id]);

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
              setTasks(prev => [msg.task, ...prev.filter(t => t.id !== msg.task.id)]);
              addToast({ type: 'info', title: 'Task Started', message: msg.task.description });
            } else if (msg.type === 'task_updated' && msg.task) {
              setTasks(prev => {
                const exists = prev.some(t => t.id === msg.task.id);
                return exists
                  ? prev.map(t => (t.id === msg.task.id ? msg.task : t))
                  : [msg.task, ...prev];
              });
              if (msg.task.status === 'OK') {
                addToast({ type: 'success', title: 'Task Completed', message: msg.task.description });
              } else if (msg.task.status === 'FAILED') {
                addToast({ type: 'error', title: 'Task Failed', message: msg.task.description });
              }
            } else if (msg.type === 'task_output' && msg.taskId && typeof msg.line === 'string') {
              setTasks(prev =>
                prev.map(t =>
                  t.id === msg.taskId
                    ? { ...t, logs: [...(Array.isArray(t.logs) ? t.logs : []), msg.line] }
                    : t
                )
              );
            } else if ((msg.type === 'init' || msg.type === 'initial_state') && Array.isArray(msg.tasks)) {
              setTasks(msg.tasks);
              if (msg.queue) {
                if (Array.isArray(msg.queue.tasks)) setQueue(msg.queue.tasks);
                setIsQueueRunning(Boolean(msg.queue.isRunning));
              }
            } else if (msg.type === 'node_added') {
              setNodesList(prev =>
                prev.some(n => n.id === msg.node.id) ? prev : [...prev, msg.node]
              );
              addToast({ type: 'success', title: 'Node Added', message: `Registered node ${msg.node.name}` });
            } else if (msg.type === 'node_updated' && msg.node) {
              setNodesList(prev => prev.map(n => (n.id === msg.node.id ? msg.node : n)));
              setSelectedNode(prev => (!prev || prev.id !== msg.node.id ? msg.node : prev));
              addToast({ type: 'info', title: 'Node Updated', message: `Settings saved for ${msg.node.name}` });
            } else if (msg.type === 'node_deleted' && msg.nodeId) {
              setNodesList(prev => prev.filter(n => n.id !== msg.nodeId));
              addToast({ type: 'warning', title: 'Node Removed', message: `Deleted node ${msg.nodeId}` });
            } else if (msg.type === 'chunks_updated') {
              setChunks(Array.isArray(msg.chunks) ? msg.chunks : []);
            } else if (msg.type === 'folders_updated') {
              setFolders(Array.isArray(msg.folders) ? msg.folders : []);
            } else if (msg.type === 'nodes_updated' && Array.isArray(msg.nodes)) {
              setNodesList(msg.nodes);
              setSelectedNode(prev => {
                if (!prev) return null;
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
    // Persist first: never show the modal as saved when the coordinator
    // rejected the change (e.g. 422 for an unknown vault variable).
    const resp = await fetch(`/api/nodes/${encodeURIComponent(nodeId)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(updates),
    });

    if (!resp.ok) {
      const data = await resp.json().catch(() => ({}));
      const message = data?.error || `Failed to save node settings (HTTP ${resp.status})`;
      addToast({ type: 'error', title: 'Save failed', message });
      throw new Error(message);
    }

    const data = await resp.json().catch(() => ({}));
    const savedNode = data?.node;
    if (savedNode) {
      setNodesList(prev => prev.map(n => (n.id === nodeId ? savedNode : n)));
      setSelectedNode(prev => (prev && prev.id === nodeId ? savedNode : prev));
    } else {
      setNodesList(prev =>
        prev.map(n => (n.id === nodeId ? updateNodeSettings(n, updates) : n))
      );
      setSelectedNode(prev => {
        if (!prev || prev.id !== nodeId) return prev;
        return updateNodeSettings(prev, updates);
      });
    }
  };

  const handleDeleteNode = (nodeId: string) => {
    setNodesList(prev => {
      const remaining = prev.filter(n => n.id !== nodeId);
      if (selectedNode?.id === nodeId && remaining.length > 0) {
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
      const data = await res.json().catch(() => ({}));
      if (res.ok) {
        if (data?.folder) {
          setFolders(prev => [...prev.filter(f => f.id !== data.folder.id), data.folder]);
        }
        addToast({
          type: 'success',
          title: 'Folder Created',
          message: `Created custom folder "${name}"`,
        });
      } else {
        addToast({
          type: 'error',
          title: 'Folder Creation Failed',
          message: data?.error || 'Failed to create folder',
        });
      }
    } catch (err: any) {
      console.error('Failed to create folder:', err);
      addToast({
        type: 'error',
        title: 'Folder Creation Error',
        message: err?.message || 'Connection error creating folder',
      });
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

  const handleMoveNodeToFolder = async (nodeId: string, folderId: string | null) => {
    const previous = nodesList.find(n => n.id === nodeId)?.folder;
    try {
      const res = await fetch(`/api/nodes/${encodeURIComponent(nodeId)}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ folder: folderId }),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(body?.error || `HTTP ${res.status}`);
      }
      const data = await res.json().catch(() => ({}));
      if (data?.node) {
        setNodesList(prev => prev.map(n => (n.id === nodeId ? data.node : n)));
      } else {
        setNodesList(prev => prev.map(n => (n.id === nodeId ? { ...n, folder: folderId || undefined } : n)));
      }
    } catch (err: any) {
      // Revert the optimistic move so the tree keeps showing the host where it
      // actually lives, and tell the operator instead of failing silently.
      setNodesList(prev => prev.map(n => (n.id === nodeId ? { ...n, folder: previous } : n)));
      addToast({
        type: 'error',
        title: 'Could not move host',
        message: err?.message || 'The node update request failed.',
      });
    }
  };

  const handleDeleteChunk = async (id: string) => {
    try {
      const res = await fetch(`/api/inventory/chunks/${encodeURIComponent(id)}`, {
        method: 'DELETE',
      });
      if (res.ok) {
        setChunks(prev => prev.filter(c => c.id !== id));
        addToast({
          type: 'info',
          title: 'Tag Unfollowed',
          message: 'Stopped following tag',
        });
      }
    } catch (err) {
      console.error('Failed to delete chunk:', err);
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
    fetch('/api/nodes?refresh=true')
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
    if (!selectedNode) return;
    fetch('/api/restic/snapshot', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ host: selectedNode.tailscaleHost }),
    }).catch(() => {});
  };

  const handleRestartPod = (podName: string) => {
    if (!selectedNode) return;
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
    if (!selectedNode) return;
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
        selectedNodeId={selectedNode?.id ?? ''}
        onSelectNode={handleSelectNode}
        onEditNode={node => setEditNodeTarget(node)}
        onOpenTerminal={node => selectedNode && handleOpenTerminal(node.id)}
        onOpenLogs={node => selectedNode && handleOpenLogs(node.tailscaleHost || node.ip, node.sshUser)}
        onDeleteNode={handleDeleteNode}
        selectedHostGroup={selectedHostGroup}
        onSelectHostGroup={setSelectedHostGroup}
        isOpenMobile={mobileSidebarOpen}
        onCloseMobile={() => setMobileSidebarOpen(false)}
        viewMode={selectedViewMode}
        onSelectViewMode={handleSelectViewMode}
        selectedDatacenterSection={datacenterSection}
        onSelectDatacenterSection={section => navigateDatacenterRoute({ kind: 'section', section })}
        selectedDatacenterSetting={activeDatacenterSetting}
        onSelectDatacenterSetting={setting => navigateDatacenterRoute({ kind: 'setting', setting })}
        chunks={chunks}
        folders={folders}
        onCreateFolder={handleCreateFolder}
        onDeleteFolder={handleDeleteFolder}
        onDeleteChunk={handleDeleteChunk}
        onMoveNodeToFolder={handleMoveNodeToFolder}
      />

      {/* Main Content Workspace */}
      <div className="lg:pl-72 pl-0">
        <main className={`relative pt-12 pb-[var(--console-height,176px)] w-full px-3 min-h-screen ${
          highContrast ? 'bg-[#080a16]' : 'bg-[#0f1224]'
        }`}>
          {selectedViewMode === 'datacenter' ? (
            activeDatacenterSetting ? (
              <DatacenterSettingsView
                page={activeDatacenterSetting}
                nodes={nodesList}
                onSelectNode={handleSelectNode}
              />
            ) : (
            <DatacenterGlobalView
              datacenterName={
                selectedNode?.datacenter ||
                nodesList.find(n => n.tailscaleHost)?.tailscaleHost?.split('.').slice(1).join('.') ||
                'unknown'
              }
              nodes={nodesList}
              tasks={tasks}
              activeSection={datacenterSection}
              onSelectNode={handleSelectNode}
              onOpenPlaybookModal={playbookName => {
                setSelectedPlaybookForEdit(playbookName);
                setPlaybookOpen(true);
              }}
              onOpenSetting={setting => navigateDatacenterRoute({ kind: 'setting', setting })}
              onToggleTasksDrawer={() => {
                const el = document.getElementById('global-tasks-drawer');
                el?.scrollIntoView({ behavior: 'smooth' });
              }}
            />
            )
          ) : (
            <div className="flex flex-col w-full">
            {/* Secondary Context Bar & System Breadcrumb */}
            <div className={`flex flex-col shadow-md rounded-t border ${
              highContrast ? 'bg-[#121528] border-[#383c54]' : 'bg-[#171a2d] border-[#26293c]'
            }`}>
              <NodeBreadcrumbBar
                node={selectedNode}
                onOpenTerminal={() => selectedNode && handleOpenTerminal(selectedNode.id)}
                onTriggerRestic={() => selectedNode && handleTriggerRestic()}
                onReboot={() => setRebootModalOpen(true)}
                onPowerOff={() => setRebootModalOpen(true)}
                onEditNode={() => selectedNode && setEditNodeTarget(selectedNode)}
              />
            </div>

            {/* Cockpit Nested Split Layout: Sub-Sidebar + Main Control Workspace */}
            <div className="grid grid-cols-12 gap-2 mt-2 w-full">
              {/* Cockpit Sub-Sidebar with Resizable Width */}
              <div className={getSubSidebarWrapperClasses(subSidebarWidth)}>
                <CockpitSubSidebar
                  node={selectedNode}
                  activeItem={activeCockpitItem}
                  storageEngine={selectedNode?.id ? nodeStorageEngines[selectedNode.id] : undefined}
                  onSelectItem={item => {
                    if (!selectedNode) return;
                    if (item === 'terminal') {
                      handleOpenTerminal(selectedNode.id);
                      setActiveCockpitItem('terminal');
                      return;
                    }
                    setActiveCockpitItem(item);
                    setActiveOpTab(item);
                  }}
                  onEditNode={() => setEditNodeTarget(selectedNode)}
                />
              </div>

              {/* Main Operational Workspace Grid */}
              <div className={getWorkspaceWrapperClasses(subSidebarWidth)}>
                {/* Specific dedicated Views when selected from Menu */}
                {(activeOpTab === 'services' || activeCockpitItem === 'services') && selectedNode && (
                  <SystemServicesView node={selectedNode} />
                )}

                {activeCockpitItem === 'terminal' && selectedNode && (
                  <div className="bg-[#171a2d] border border-[#26293c] rounded p-6 flex flex-col items-center justify-center text-center gap-3 font-mono">
                    <span className="material-symbols-outlined text-[36px] text-[#b0c9ff]">terminal</span>
                    <h3 className="text-sm font-bold text-[#dfe1fa]">Interactive Ghostty Terminal Active</h3>
                    <p className="text-xs text-[#8d909b] max-w-md">
                      A live WASM PTY session to <strong className="text-[#8cd6cb]">{selectedNode.name}</strong> ({selectedNode.sshUser || 'core'}@{selectedNode.tailscaleHost || selectedNode.ip}) is open in the terminal drawer below.
                    </p>
                    <button
                      type="button"
                      onClick={() => handleOpenTerminal(selectedNode.id)}
                      className="px-3 py-1.5 rounded bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] text-xs font-semibold border border-[#434750]/40 cursor-pointer transition-colors flex items-center gap-2"
                    >
                      <span className="material-symbols-outlined text-[14px] text-[#8cd6cb]">open_in_new</span>
                      <span>Focus Terminal Session</span>
                    </button>
                  </div>
                )}

                {(activeOpTab === 'logs' || activeCockpitItem === 'logs') && selectedNode && (
                  <SystemLogsView node={selectedNode} />
                )}

                {(activeOpTab === 'updates' || activeCockpitItem === 'updates') && selectedNode && (
                  <PackageUpdatesView
                    node={selectedNode}
                    onOpenTask={taskId => {
                      setLogsTarget(taskId);
                    }}
                  />
                )}

                {activeOpTab === 'interfaces' && selectedNode && (
                  <NetworkInterfacesView node={selectedNode} />
                )}

                {activeOpTab === 'firewall' && selectedNode && (
                  <FirewallView node={selectedNode} />
                )}

                {(activeOpTab === 'zfs' || activeCockpitItem === 'zfs' || activeOpTab === 'storage' || activeCockpitItem === 'storage') && selectedNode && (
                  <ZfsManagementView
                    node={selectedNode}
                    onEngineDetected={(engine: StorageEngineType) => {
                      if (selectedNode) {
                        setNodeStorageEngines(prev => ({ ...prev, [selectedNode.id]: engine }));
                      }
                    }}
                  />
                )}

                {activeOpTab === 'shares' && selectedNode && (
                  <NetworkSharesView node={selectedNode} />
                )}

                {activeOpTab === 'smart' && selectedNode && (
                  <DisksSmartView node={selectedNode} />
                )}

                {activeOpTab === 'tailscale' && (
                  <TailscaleExplorerView
                    onOpenTerminalForHost={ip => {
                      const match = nodesList.find(n => n.tailscaleIp === ip || n.ip === ip);
                      handleOpenTerminal(match?.id);
                    }}
                    nodes={nodesList}
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

                {activeOpTab === 'vms' && selectedNode && (
                  <VirtualMachinesKVM
                    node={selectedNode}
                    onOpenConsole={() => setTerminalOpen(true)}
                    onNewVM={() => setCreateVmOpen(true)}
                  />
                )}

                {activeOpTab === 'restic' && selectedNode && (
                  <ResticBackupPolicy
                    nodeName={selectedNode.name}
                    hasRestic={selectedNode.hasRestic}
                    onIntegrityCheck={() =>
                      handleRunPlaybook('security-hardening.yml', selectedNode.tailscaleHost || selectedNode.ip || '')
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

                {/* Host Modules & Plugin Matrix View */}
                {(activeOpTab === 'modules' || activeCockpitItem === 'modules') && (
                  <HostModulesView
                    node={selectedNode ?? findLocalNode(nodesList)}
                    onOpenQueueTask={taskId => {
                      setLogsTarget(taskId);
                    }}
                  />
                )}

                {/* Dynamic Custom or Generic Plugin View Routing */}
                {Boolean(
                  (activeCockpitItem && activeCockpitItem.includes('/')) ||
                  (activeOpTab && activeOpTab.includes('/'))
                ) && selectedNode && (() => {
                  const currentPluginTab = (activeCockpitItem?.includes('/') ? activeCockpitItem : activeOpTab) || '';
                  const PluginViewComponent = getPluginView(currentPluginTab);
                  return (
                    <PluginViewComponent
                      node={selectedNode}
                      pluginId={currentPluginTab}
                      telemetry={telemetry}
                      onOpenLogs={handleOpenLogs}
                      onOpenExec={() => setTerminalOpen(true)}
                      onOpenTerminal={handleOpenTerminal}
                    />
                  );
                })()}

                {/* Default Overview / Reorderable Interactive Workspace */}
                {activeOpTab === 'overview' && (
                  <div className="flex flex-col gap-2">
                    {panelOrder.map((panelId, idx) => {
                      if (panelId === 'telemetry_mesh' && selectedNode) {
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

                      if (panelId === 'vms_restic' && selectedNode) {
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
        themeChoice={themeChoice}
        resolvedTheme={resolvedTheme}
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
        nodes={nodesList}
      />

      <CreateVmModal
        node={selectedNode ?? undefined}
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
            message: `VM ${newVm.name} successfully provisioned on ${selectedNode?.name ?? 'unknown node'}`,
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
        onOpenEditor={() => {
          setPlaybookOpen(false);
          handleOpenPlaybookEditor();
        }}
        nodes={nodesList}
      />

      <ContainerLogsModal
        targetName={logsTarget}
        node={selectedNode ?? undefined}
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

      {selectedNode && (
        <RebootConfirmModal
          node={selectedNode}
          isOpen={rebootModalOpen}
          onClose={() => setRebootModalOpen(false)}
          onConfirmReboot={handleRebootConfirm}
        />
      )}

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
        subSidebarWidth={subSidebarWidth}
        onSelectSubSidebarWidth={setSubSidebarWidth}
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
