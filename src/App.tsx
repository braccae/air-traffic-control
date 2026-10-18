import React, { useState, useEffect } from 'react';
import type { NodeItem, TelemetryData, GlobalTaskItem } from './types/index.ts';
import { Header } from './components/Header.tsx';
import { ServerViewSidebar } from './components/ServerViewSidebar.tsx';
import { TopSubNav } from './components/TopSubNav.tsx';
import { NodeBreadcrumbBar } from './components/NodeBreadcrumbBar.tsx';
import { OperationalTabs } from './components/OperationalTabs.tsx';
import { CockpitSubSidebar } from './components/CockpitSubSidebar.tsx';
import { SystemHealthTelemetry } from './components/SystemHealthTelemetry.tsx';
import { TailscaleMeshCard } from './components/TailscaleMeshCard.tsx';
import { PodmanManager } from './components/PodmanManager.tsx';
import { VirtualMachinesKVM } from './components/VirtualMachinesKVM.tsx';
import { ResticBackupPolicy } from './components/ResticBackupPolicy.tsx';
import { ServiceIdentitiesTable } from './components/ServiceIdentitiesTable.tsx';
import { GlobalTasksExecutionLog } from './components/GlobalTasksExecutionLog.tsx';

// Modals
import { TerminalModal } from './components/modals/TerminalModal.tsx';
import { AddNodeModal } from './components/modals/AddNodeModal.tsx';
import { RunPlaybookModal } from './components/modals/RunPlaybookModal.tsx';
import { ContainerLogsModal } from './components/modals/ContainerLogsModal.tsx';
import { RebootConfirmModal } from './components/modals/RebootConfirmModal.tsx';
import { SearchModal } from './components/modals/SearchModal.tsx';
import { GroupVarsModal } from './components/modals/GroupVarsModal.tsx';

const initialNodes: NodeItem[] = [
  {
    id: 'node-150-openmediavault',
    name: '150 (openmediavault)',
    shortName: '150',
    group: 'cold-war',
    datacenter: 'homelab-dc1',
    nodeType: 'linux',
    badge: 'SSH',
    badgeColor: 'text-[#8d909b]',
    ip: '192.168.1.150',
    port: 22,
    tailscaleIp: '100.97.159.101',
    tailscaleHost: 'the-freezer.ts.net',
    osDescription: 'AlmaLinux 10.2 / Debian 12',
    status: 'online',
    uptime: '16h 44m',
    hasGlances: true,
    hasCockpit: true,
    hasPodman: true,
    hasRestic: true,
    specs: {
      cpu: 'Intel Core i9-13900K',
      vcpus: 16,
      ramGiB: 64,
      motherboard: 'ASUS Pro WS W680-ACE',
      bios: '3401 (03/24/2024)',
      ipmi: '192.168.1.50',
      ecc: 'Active',
      secureBoot: 'Disabled',
      iommu: 'Enabled',
    },
  },
  {
    id: 'node-151-proxmox-backup-server',
    name: '151 (proxmox-backup)',
    shortName: '151',
    group: 'cold-war',
    datacenter: 'homelab-dc1',
    nodeType: 'pbs',
    badge: 'PBS',
    badgeColor: 'text-[#8d909b]',
    ip: '192.168.1.151',
    port: 22,
    tailscaleIp: '100.97.159.102',
    tailscaleHost: 'pbs-backup.ts.net',
    osDescription: 'Proxmox Backup Server 3.1-4',
    status: 'online',
    uptime: '84d 10h',
    hasGlances: true,
    hasCockpit: true,
    hasPodman: false,
    hasRestic: true,
    specs: {
      cpu: 'AMD EPYC 7302P',
      vcpus: 16,
      ramGiB: 128,
      motherboard: 'Supermicro H11SSL-i',
      bios: '2.5',
      ipmi: '192.168.1.51',
      ecc: 'Active Registered',
      secureBoot: 'Enabled',
      iommu: 'Enabled',
    },
  },
  {
    id: 'node-152-apt-cacher-ng',
    name: '152 (apt-cacher-ng)',
    shortName: '152',
    group: 'cold-war',
    datacenter: 'homelab-dc1',
    nodeType: 'lxc',
    badge: 'LXC',
    badgeColor: 'text-[#8d909b]',
    ip: '192.168.1.152',
    port: 22,
    tailscaleIp: '100.97.159.103',
    tailscaleHost: 'cacher-lxc.ts.net',
    osDescription: 'Debian Bookworm 12 (LXC)',
    status: 'online',
    uptime: '12d 6h',
    hasGlances: true,
    hasCockpit: true,
    hasPodman: true,
    hasRestic: true,
    specs: {
      cpu: 'Shared Core Allocation',
      vcpus: 4,
      ramGiB: 4,
      motherboard: 'Containerized cgroup v2',
      bios: 'LXC Runtime',
      ipmi: 'N/A',
      ecc: 'Active',
      secureBoot: 'Host Dependent',
      iommu: 'N/A',
    },
  },
  {
    id: 'node-153-llama-inference-box',
    name: '153 (llama-inference)',
    shortName: '153',
    group: 'cold-war',
    datacenter: 'homelab-dc1',
    nodeType: 'linux',
    badge: 'GPU',
    badgeColor: 'text-[#d9b9ff]',
    ip: '192.168.1.153',
    port: 22,
    tailscaleIp: '100.97.159.104',
    tailscaleHost: 'inference-ai.ts.net',
    osDescription: 'Ubuntu 24.04 LTS (NVIDIA CUDA 12.4)',
    status: 'online',
    uptime: '3d 21h',
    hasGlances: true,
    hasCockpit: true,
    hasPodman: true,
    hasRestic: true,
    specs: {
      cpu: 'AMD Ryzen 9 7950X',
      vcpus: 32,
      ramGiB: 64,
      motherboard: 'ASRock Rack B650D4U',
      bios: '8.02',
      ipmi: '192.168.1.53',
      ecc: 'Active ECC UDIMM',
      secureBoot: 'Disabled',
      iommu: 'Enabled',
    },
  },
  {
    id: 'node-154-k3s-master-01',
    name: '154 (k3s-master-01)',
    shortName: '154',
    group: 'cold-war',
    datacenter: 'homelab-dc1',
    nodeType: 'k3s',
    badge: 'K3S',
    badgeColor: 'text-[#b0c9ff]',
    ip: '192.168.1.154',
    port: 22,
    tailscaleIp: '100.97.159.105',
    tailscaleHost: 'k3s-ctrl.ts.net',
    osDescription: 'Alpine Linux 3.19 (K3s v1.29.3)',
    status: 'online',
    uptime: '45d 02h',
    hasGlances: true,
    hasCockpit: true,
    hasPodman: true,
    hasRestic: true,
    specs: {
      cpu: 'Intel Xeon E-2388G',
      vcpus: 8,
      ramGiB: 32,
      motherboard: 'Supermicro X12STL-F',
      bios: '1.4b',
      ipmi: '192.168.1.54',
      ecc: 'Active ECC',
      secureBoot: 'Enabled',
      iommu: 'Enabled',
    },
  },
  {
    id: 'node-160-ingress-traefik',
    name: '160 (ingress-traefik)',
    shortName: '160',
    group: 'noodle',
    datacenter: 'homelab-dc1',
    nodeType: 'linux',
    ip: '192.168.1.160',
    port: 22,
    tailscaleIp: '100.97.159.106',
    tailscaleHost: 'traefik-gw.ts.net',
    osDescription: 'Debian GNU/Linux 12 (bookworm)',
    status: 'online',
    uptime: '110d 14h',
    hasGlances: true,
    hasCockpit: true,
    hasPodman: true,
    hasRestic: true,
    specs: {
      cpu: 'Intel Core i5-12500',
      vcpus: 6,
      ramGiB: 16,
      motherboard: 'ASUS Prime B660M',
      bios: '2603',
      ipmi: '192.168.1.56',
      ecc: 'Non-ECC',
      secureBoot: 'Enabled',
      iommu: 'Disabled',
    },
  },
  {
    id: 'node-161-postgres-ha-primary',
    name: '161 (postgres-ha)',
    shortName: '161',
    group: 'noodle',
    datacenter: 'homelab-dc1',
    nodeType: 'linux',
    ip: '192.168.1.161',
    port: 22,
    tailscaleIp: '100.97.159.107',
    tailscaleHost: 'pg-primary.ts.net',
    osDescription: 'Rocky Linux 9.3 (Blue Onyx)',
    status: 'online',
    uptime: '62d 18h',
    hasGlances: true,
    hasCockpit: true,
    hasPodman: true,
    hasRestic: true,
    specs: {
      cpu: 'AMD EPYC 7282',
      vcpus: 16,
      ramGiB: 64,
      motherboard: 'Supermicro H11SSL-NC',
      bios: '2.5',
      ipmi: '192.168.1.57',
      ecc: 'Active ECC Reg',
      secureBoot: 'Enabled',
      iommu: 'Enabled',
    },
  },
  {
    id: 'node-pong-edge',
    name: 'pong (Edge node)',
    shortName: 'pong',
    group: 'noodle',
    datacenter: 'homelab-dc1',
    nodeType: 'router_switch',
    badge: 'GW',
    badgeColor: 'text-[#8cd6cb]',
    ip: '192.168.1.254',
    port: 22,
    tailscaleIp: '100.97.159.120',
    tailscaleHost: 'pong-edge.ts.net',
    osDescription: 'VyOS 1.5-rolling / Ubiquiti EdgeOS',
    status: 'online',
    uptime: '180d 05h',
    hasGlances: false,
    hasCockpit: false,
    hasPodman: false,
    hasRestic: false,
    specs: {
      cpu: 'Octeon III Dual-Core 1.0 GHz',
      vcpus: 2,
      ramGiB: 2,
      motherboard: 'Ubiquiti Networks ER-X-SFP',
      bios: 'U-Boot 2014.10',
      ipmi: 'N/A (Serial Console)',
      ecc: 'Embedded',
      secureBoot: 'Hardware Encrypted',
      iommu: 'N/A',
    },
  },
];

const initialTelemetry: TelemetryData = {
  cpuUsage: 1.4,
  cpuModel: 'Intel Core i9-13900K',
  vCpus: 16,
  memoryUsedGiB: 42.4,
  memoryTotalGiB: 62.8,
  arcCacheGiB: 12.3,
  zfsTankUsedTb: 14.2,
  zfsTankTotalTb: 48.0,
  zfsRaidType: 'RAIDZ2',
  zfsScrubStatus: 'Scrub 2d ago',
  loadAvg1m: 0.42,
  loadAvg5m: 0.58,
  loadAvg15m: 0.61,
  ioDelayPercent: 0.02,
  cpuTempC: 38.0,
  fanSpeedRpm: 820,
  packageWatts: 44.2,
  sparkline: [20, 19, 22, 18, 14, 16, 12, 15, 10, 14, 11],
};

const initialTasks: GlobalTaskItem[] = [
  {
    id: 'task-1',
    startTime: '2025-05-14 10:28:12',
    nodeTarget: 'all (14 hosts)',
    user: 'root@pam',
    description: 'ansible-playbook -i production site.yml',
    progress: 65,
    progressEta: 'Running (00:01:45)',
    status: 'RUNNING',
    category: 'active',
  },
  {
    id: 'task-2',
    startTime: '2025-05-14 10:27:44',
    nodeTarget: '150 (openmediavault)',
    user: 'root@pam',
    description: 'cockpit-session ws-tunnel /dev/pts/3',
    progress: 100,
    progressEta: 'Connected (00:04:12)',
    status: 'OK',
    category: 'active',
  },
  {
    id: 'task-3',
    startTime: '2025-05-14 10:26:01',
    nodeTarget: 'cold-war/152',
    user: 'system',
    description: 'apt update && apt dist-upgrade -y',
    progress: 100,
    progressEta: '100% completed',
    status: 'OK',
    category: 'active',
  },
  {
    id: 'task-4',
    startTime: '2025-05-14 10:24:50',
    nodeTarget: 'inventory/vault',
    user: 'root@pam',
    description: 'ansible-vault decrypt group_vars/all.yml',
    progress: 100,
    progressEta: 'Cipher: AES256',
    status: 'OK',
    category: 'vault',
  },
];

export default function App() {
  const [nodesList, setNodesList] = useState<NodeItem[]>(initialNodes);
  const [selectedNode, setSelectedNode] = useState<NodeItem>(initialNodes[0]);
  const [selectedHostGroup, setSelectedHostGroup] = useState('all');
  const [activeTopTab, setActiveTopTab] = useState('summary');
  const [activeOpTab, setActiveOpTab] = useState('overview');
  const [activeCockpitItem, setActiveCockpitItem] = useState('overview');

  const [telemetry, setTelemetry] = useState<TelemetryData>(initialTelemetry);
  const [tasks, setTasks] = useState<GlobalTaskItem[]>(initialTasks);

  // Mobile sidebar state
  const [mobileSidebarOpen, setMobileSidebarOpen] = useState(false);

  // Modals state
  const [terminalOpen, setTerminalOpen] = useState(false);
  const [addNodeOpen, setAddNodeOpen] = useState(false);
  const [playbookOpen, setPlaybookOpen] = useState(false);
  const [logsTarget, setLogsTarget] = useState<string | null>(null);
  const [rebootModalOpen, setRebootModalOpen] = useState(false);
  const [searchOpen, setSearchOpen] = useState(false);
  const [groupVarsOpen, setGroupVarsOpen] = useState(false);

  // Fetch initial nodes and tasks from API
  useEffect(() => {
    fetch('/api/nodes')
      .then(res => res.json())
      .then(data => {
        if (Array.isArray(data) && data.length > 0) {
          setNodesList(data);
        }
      })
      .catch(() => {
        // Use seed
      });

    fetch('/api/tasks')
      .then(res => res.json())
      .then(data => {
        if (Array.isArray(data) && data.length > 0) {
          setTasks(data);
        }
      })
      .catch(() => {});
  }, []);

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
            } else if (msg.type === 'task_updated') {
              setTasks(prev => prev.map(t => (t.id === msg.task.id ? msg.task : t)));
            } else if (msg.type === 'node_added') {
              setNodesList(prev => [...prev, msg.node]);
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
    setMobileSidebarOpen(false);
  };

  const handleAddNode = (newNode: Partial<NodeItem>) => {
    const fullNode: NodeItem = {
      id: `node-${Date.now()}`,
      name: newNode.name || 'New Node',
      shortName: newNode.shortName || 'node',
      group: newNode.group || 'cold-war',
      datacenter: 'homelab-dc1',
      nodeType: newNode.nodeType || 'linux',
      ip: newNode.ip || '192.168.1.180',
      port: newNode.port || 22,
      tailscaleIp: newNode.tailscaleIp || '100.97.159.180',
      tailscaleHost: newNode.tailscaleHost || 'node.ts.net',
      osDescription: newNode.osDescription || 'Linux (SSH)',
      status: 'online',
      uptime: '1m',
      hasGlances: true,
      hasCockpit: true,
      hasPodman: true,
      hasRestic: true,
      specs: newNode.specs || {
        cpu: 'Intel Core / AMD EPYC',
        vcpus: 8,
        ramGiB: 16,
        motherboard: 'Enterprise Platform',
        bios: '2.8',
        ipmi: 'DHCP',
        ecc: 'Active',
        secureBoot: 'Enabled',
        iommu: 'Enabled',
      },
    };

    setNodesList(prev => [...prev, fullNode]);
    setSelectedNode(fullNode);

    // Call backend endpoint
    fetch('/api/nodes', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(fullNode),
    }).catch(() => {});
  };

  const handleRunPlaybook = (playbook: string, target: string) => {
    fetch('/api/playbook/run', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ playbook, target }),
    }).catch(() => {
      // Local fallback task addition
      const localTask: GlobalTaskItem = {
        id: `task-${Date.now()}`,
        startTime: new Date().toISOString().replace('T', ' ').substring(0, 19),
        nodeTarget: target,
        user: 'root@pam',
        description: `ansible-playbook -i production playbooks/${playbook}`,
        progress: 10,
        progressEta: 'Executing role tasks (10%)',
        status: 'RUNNING',
        category: 'ansible',
      };
      setTasks(prev => [localTask, ...prev]);
    });
  };

  const handleTriggerRestic = () => {
    fetch('/api/restic/snapshot', { method: 'POST' }).catch(() => {
      const resticTask: GlobalTaskItem = {
        id: `task-${Date.now()}`,
        startTime: new Date().toISOString().replace('T', ' ').substring(0, 19),
        nodeTarget: selectedNode.name,
        user: 'root@pam',
        description: 'restic -r s3://restic-homelab-vault/the-freezer backup /tank0/appdata',
        progress: 30,
        progressEta: 'Deduplicating chunks (AES-256)...',
        status: 'RUNNING',
        category: 'active',
      };
      setTasks(prev => [resticTask, ...prev]);
    });
  };

  const handleRestartPod = (podName: string) => {
    const restartTask: GlobalTaskItem = {
      id: `task-${Date.now()}`,
      startTime: new Date().toISOString().replace('T', ' ').substring(0, 19),
      nodeTarget: selectedNode.name,
      user: 'root@pam',
      description: `podman pod restart ${podName}`,
      progress: 100,
      progressEta: 'Restarted in 00:00:03',
      status: 'OK',
      category: 'active',
    };
    setTasks(prev => [restartTask, ...prev]);
  };

  const handleRebootConfirm = (mode: string) => {
    const rebootTask: GlobalTaskItem = {
      id: `task-${Date.now()}`,
      startTime: new Date().toISOString().replace('T', ' ').substring(0, 19),
      nodeTarget: selectedNode.name,
      user: 'root@pam',
      description: `reboot sequence initiated (mode: ${mode})`,
      progress: 50,
      progressEta: 'Broadcasting ACPI reboot signal',
      status: 'RUNNING',
      category: 'active',
    };
    setTasks(prev => [rebootTask, ...prev]);
  };

  return (
    <div className="bg-[#0f1224] text-[#dfe1fa] min-h-screen selection:bg-[#395d9f] selection:text-white font-sans">
      {/* Top Header */}
      <Header
        onOpenAddNode={() => setAddNodeOpen(true)}
        onOpenPlaybook={() => setPlaybookOpen(true)}
        onOpenTerminal={() => setTerminalOpen(true)}
        onOpenSearch={() => setSearchOpen(true)}
        onOpenGroupVars={() => setGroupVarsOpen(true)}
        onToggleSidebar={() => setMobileSidebarOpen(!mobileSidebarOpen)}
        isSidebarOpen={mobileSidebarOpen}
      />

      {/* Left Server View Sidebar */}
      <ServerViewSidebar
        nodes={nodesList}
        selectedNodeId={selectedNode.id}
        onSelectNode={handleSelectNode}
        selectedHostGroup={selectedHostGroup}
        onSelectHostGroup={setSelectedHostGroup}
        isOpenMobile={mobileSidebarOpen}
        onCloseMobile={() => setMobileSidebarOpen(false)}
      />

      {/* Top Sub-nav Bar */}
      <div className="lg:pl-72 pl-0">
        <TopSubNav
          activeTopTab={activeTopTab}
          onSelectTopTab={tabId => {
            setActiveTopTab(tabId);
            if (tabId === 'xterm_console') setTerminalOpen(true);
            if (tabId === 'playbooks') setPlaybookOpen(true);
            if (tabId === 'group_vars') setGroupVarsOpen(true);
          }}
          clusterCpu={`${telemetry.cpuUsage * 2 + 12}%`}
          clusterRam="42.1 / 128 GB"
        />

        {/* Main Content Workspace */}
        <main className="relative pt-18 pb-48 w-full px-3 min-h-screen bg-[#0f1224]">
          <div className="flex flex-col w-full">
            {/* Secondary Context Bar & System Breadcrumb */}
            <div className="flex flex-col bg-[#171a2d] shadow-sm rounded-t border border-[#26293c]">
              <NodeBreadcrumbBar
                node={selectedNode}
                onOpenTerminal={() => setTerminalOpen(true)}
                onTriggerRestic={handleTriggerRestic}
                onReboot={() => setRebootModalOpen(true)}
                onPowerOff={() => setRebootModalOpen(true)}
              />

              <OperationalTabs
                activeTab={activeOpTab}
                onSelectTab={tabId => {
                  setActiveOpTab(tabId);
                  if (tabId === 'logs') setLogsTarget('kernel-dmesg');
                }}
              />
            </div>

            {/* Cockpit Nested Split Layout: Sub-Sidebar + Main Control Workspace */}
            <div className="grid grid-cols-12 gap-2 mt-2 w-full">
              {/* Cockpit Sub-Sidebar */}
              <CockpitSubSidebar
                node={selectedNode}
                activeItem={activeCockpitItem}
                onSelectItem={item => {
                  setActiveCockpitItem(item);
                  if (item === 'logs') setLogsTarget('dmesg / journald');
                }}
              />

              {/* Main Operational Workspace Grid */}
              <div className="col-span-12 lg:col-span-9 xl:col-span-10 flex flex-col gap-2">
                {/* TOP ROW: Panel 1 (Telemetry) & Panel 2 (Tailscale Mesh) */}
                <div className="grid grid-cols-1 md:grid-cols-12 gap-2">
                  <SystemHealthTelemetry telemetry={telemetry} />
                  <TailscaleMeshCard
                    node={selectedNode}
                    onReauth={() => setTerminalOpen(true)}
                  />
                </div>

                {/* MIDDLE ROW: Panel 3 (Podman Containers & Pods) */}
                <PodmanManager
                  onOpenLogs={name => setLogsTarget(name)}
                  onOpenExec={name => {
                    setLogsTarget(null);
                    setTerminalOpen(true);
                  }}
                  onRestartPod={handleRestartPod}
                  onCreatePod={() => setAddNodeOpen(true)}
                  onRunContainer={() => setAddNodeOpen(true)}
                />

                {/* BOTTOM ROW 1: Panel 4 (KVM VMs) & Panel 5 (Restic Backups) */}
                <div className="grid grid-cols-1 md:grid-cols-12 gap-2">
                  <VirtualMachinesKVM
                    onOpenConsole={() => setTerminalOpen(true)}
                    onNewVM={() => setAddNodeOpen(true)}
                  />
                  <ResticBackupPolicy
                    onIntegrityCheck={() => handleRunPlaybook('security-hardening.yml', selectedNode.name)}
                    onBrowseFiles={() => setLogsTarget('restic-repo-browser')}
                    onSnapshot={handleTriggerRestic}
                  />
                </div>

                {/* BOTTOM ROW 2: Panel 6 (Service Users & System Identities) */}
                <ServiceIdentitiesTable
                  onAddIdentity={() => setAddNodeOpen(true)}
                  onAudit={() => setLogsTarget('pam-audit-log')}
                />
              </div>
            </div>
          </div>
        </main>
      </div>

      {/* Fixed Bottom Global Tasks & Execution Log */}
      <GlobalTasksExecutionLog tasks={tasks} />

      {/* Interactive Modals */}
      <TerminalModal
        node={selectedNode}
        isOpen={terminalOpen}
        onClose={() => setTerminalOpen(false)}
      />

      <AddNodeModal
        isOpen={addNodeOpen}
        onClose={() => setAddNodeOpen(false)}
        onAddNode={handleAddNode}
      />

      <RunPlaybookModal
        isOpen={playbookOpen}
        onClose={() => setPlaybookOpen(false)}
        onRunPlaybook={handleRunPlaybook}
      />

      <ContainerLogsModal
        targetName={logsTarget}
        isOpen={Boolean(logsTarget)}
        onClose={() => setLogsTarget(null)}
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
        onOpenTerminal={() => setTerminalOpen(true)}
      />

      <GroupVarsModal
        isOpen={groupVarsOpen}
        onClose={() => setGroupVarsOpen(false)}
      />
    </div>
  );
}
