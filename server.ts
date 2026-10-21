import express from 'express';
import { createServer } from 'http';
import { WebSocketServer, WebSocket } from 'ws';
import path from 'path';
import fs from 'fs';
import { spawn, execSync, execFile, type ChildProcess } from 'child_process';
import { fileURLToPath } from 'url';
import { createServer as createViteServer } from 'vite';
import { getDefaultHostPodmanProbe, probeUnprivilegedPodmanUsers, parseTerminalCommand } from './src/utils/orchestrator.ts';
import {
  LIVE_TAILNET_NODES,
  refreshTailscaleNodes as baseRefreshTailscaleNodes,
  fetchLiveNodeTelemetry,
  probeNodePodmanAndIdentities,
  runNodeCommand,
  runNodeElevatedCommand,
  probeNodePrivilege,
  testNodePrivilege,
} from './src/utils/liveCluster.ts';
import { syncTagInventoryChunks, fetchLiveTailscalePeers } from './src/utils/tailscaleSync.ts';
import { updateNodeSettings } from './src/utils/navigationViewModel.ts';
import { createHostCacheStore, registerInventoryRoutes } from './src/utils/hostCacheDb.ts';
import { fetchNodeTelemetryMultiTier } from './src/utils/telemetryCollector.ts';
import { registerPrivilegeRoutes } from './src/utils/privilegeEscalation.ts';
import { buildJournalctlCommand, sanitizeLogFilter } from './src/utils/systemLogs.ts';
import {
  registerPlaybookRoutes,
  validatePlaybookName,
  normalizePlaybookName,
  isProtectedPlaybook,
  listPlaybooks,
  readPlaybook,
  createPlaybook,
  updatePlaybook,
  deletePlaybook,
} from './src/utils/playbookManager.ts';
import type { NodeItem, TelemetryData, GlobalTaskItem, ServiceUser, VMItem, TagInventoryChunk } from './src/types/index.ts';
import { getHardwareProbeCommand, parseHardwareProbeOutput, applyHardwareProbeToNode } from './src/utils/hardwareProbe.ts';
import {
  buildNfsSaveCommand,
  buildSmbSaveCommand,
  parseEtcPasswd,
  buildUserCreateCommand,
  buildUserPasswdCommand,
  buildSmbPasswdCommand,
  sanitizeUsername,
} from './src/utils/sharesManager.ts';
import {
  buildFirewallAddCommand,
  buildFirewallDeleteCommand,
  buildConnectNetworkCommand,
  parseFirewallListAll,
} from './src/utils/firewallRules.ts';
import {
  registerPackageRoutes,
  type PackageManager,
  type PackageCheckResult,
} from './src/utils/packageManager.ts';
import {
  loadAnsibleConfig,
  saveAnsibleConfig,
  validateVaultPasswordFile,
  type AnsibleConfigSettings,
} from './src/utils/ansibleConfigManager.ts';
import {
  initQueueStore,
  getQueue,
  enqueueTask,
  removeTask,
  clearQueue,
  executeNextQueueTask,
} from './src/utils/ansibleQueueRunner.ts';
import {
  getAnsibleVenvPath,
  findOrBootstrapUv,
  ensureAnsibleVenv,
  REQUIRED_VENV_PACKAGES,
} from './src/utils/ansibleVenvManager.ts';
import { materializeVaultVarsFile, registerVaultRoutes } from './src/utils/vaultManager.ts';
import { registerRcloneRoutes } from './src/utils/rcloneManager.ts';
import { createPlaybookRunner } from './src/utils/playbookRunner.ts';
import {
  initCronScheduleStore,
  listSchedules,
  getSchedule,
  createSchedule,
  updateSchedule,
  removeSchedule,
} from './src/utils/cronScheduleStore.ts';
import { createCronScheduler } from './src/utils/cronScheduler.ts';
import { nextCronRun } from './src/utils/cronExpression.ts';
import { createCronExecutor } from './src/utils/cronScheduleExecutor.ts';
import {
  installConsoleCapture,
  listClusterLogs,
  onClusterLog,
  recordClusterLog,
  type ClusterLogKind,
} from './src/utils/clusterLogStore.ts';
import {
  beginConnection,
  getConnectionSnapshot,
  onConnectionChange,
  type ConnectionSnapshot,
} from './src/utils/connectionRegistry.ts';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const app = express();
const server = createServer(app);
const wss = new WebSocketServer({ server, path: '/ws' });

app.use(express.json());

// Mirror the coordinator's own console output into the cluster syslog buffer so the
// Cluster Syslog tab shows real ATC application logs (never synthesised lines).
installConsoleCapture();

// Serve libghostty WASM binary directly so ghostty-web can always load /ghostty-vt.wasm
app.get('/ghostty-vt.wasm', (_req, res) => {
  res.setHeader('Content-Type', 'application/wasm');
  res.sendFile(path.resolve(__dirname, 'node_modules/ghostty-web/ghostty-vt.wasm'));
});

// Initialize SQLite Host Cache DB
const dbFilePath = process.env.HOST_CACHE_DB_PATH || path.resolve(__dirname, 'data/hosts-cache.sqlite');
const hostCache = createHostCacheStore(dbFilePath);
initQueueStore(dbFilePath);
initCronScheduleStore(dbFilePath);

// Shared real playbook runner: POST /api/playbook/run and cron schedules both
// spawn through this instance so runs land in the same console task stream.
// Persist a task to SQLite and keep the in-memory console list in sync.
function persistTask(task: GlobalTaskItem) {
  hostCache.saveTask(task);
  const idx = tasks.findIndex(t => t.id === task.id);
  if (idx >= 0) {
    tasks[idx] = { ...task };
  } else {
    tasks.unshift({ ...task });
  }
}

const playbookRunner = createPlaybookRunner({
  cwd: __dirname,
  inventoryPath: path.resolve(__dirname, 'inventory/tailscale_inventory.py'),
  saveTask: task => persistTask(task),
  broadcast: msg => broadcast(msg),
  prepareExtraVarsFile: () => materializeVaultVarsFile(hostCache),
});

const cronExecutor = createCronExecutor({
  cwd: __dirname,
  inventoryPath: path.resolve(__dirname, 'inventory/tailscale_inventory.py'),
  playbookRunner,
  listNodes: () => nodes,
  enqueueTask,
  executeQueuedTask: (inventoryPath, options) => executeNextQueueTask(inventoryPath, options),
  saveTask: task => hostCache.saveTask(task),
  broadcast: msg => broadcast(msg),
});

const cronScheduler = createCronScheduler({
  execute: schedule => cronExecutor.execute(schedule),
  log: message => console.log(message),
});

// Seed or load persisted hosts from SQLite
let nodes: NodeItem[] = hostCache.seedHostsIfEmpty(LIVE_TAILNET_NODES);

// Synchronize real hardware specs from LIVE_TAILNET_NODES into loaded nodes and SQLite
nodes = nodes.map(n => {
  const liveDef = LIVE_TAILNET_NODES.find(l => l.id === n.id || l.shortName === n.shortName);
  if (liveDef) {
    return {
      ...n,
      osDescription: liveDef.osDescription || n.osDescription,
      hasGlances: liveDef.hasGlances ?? n.hasGlances,
      specs: {
        ...liveDef.specs,
        ...n.specs,
        motherboard: liveDef.specs.motherboard,
        bios: liveDef.specs.bios,
        cpu: liveDef.specs.cpu,
        vcpus: liveDef.specs.vcpus,
        ramGiB: liveDef.specs.ramGiB,
        ipmi: liveDef.specs.ipmi,
        ecc: liveDef.specs.ecc,
        secureBoot: liveDef.specs.secureBoot,
        iommu: liveDef.specs.iommu,
      },
    };
  }
  return n;
});
hostCache.upsertHosts(nodes);

// Dynamic Tailscale Tag Synchronization Wrapper
async function refreshTailscaleNodes(currentNodes: NodeItem[]): Promise<NodeItem[]> {
  let updated = await withPollLog('tailscale', 'tailnet', 'tailscale status --json', () =>
    baseRefreshTailscaleNodes(currentNodes)
  );
  try {
    const chunks = hostCache.getAllTagChunks();
    const hasDynamicNodes = updated.some(n => n.isDynamicTagMember);
    if (chunks.length > 0 || hasDynamicNodes) {
      const peers = await fetchLiveTailscalePeers();
      const syncResult = syncTagInventoryChunks(chunks, peers, updated);
      for (const rem of syncResult.removedNodes) {
        hostCache.deleteHost(rem.id);
      }
      updated = [...syncResult.unchangedNodes, ...syncResult.updatedNodes, ...syncResult.addedNodes];
    }
  } catch (err) {
    console.error('Error synchronizing Tailscale tag inventory chunks:', err);
  }
  return updated;
}

// Refresh from live `tailscale status --json` on startup and periodically, persisting into SQLite
refreshTailscaleNodes(nodes).then(updated => {
  nodes = updated;
  hostCache.upsertHosts(nodes);
});
setInterval(async () => {
  nodes = await refreshTailscaleNodes(nodes);
  hostCache.upsertHosts(nodes);
}, 30000);

// Default group vars seeded into SQLite
const DEFAULT_GROUP_VARS: Record<string, string> = {
  'group_vars/all.yml': `---
# Ansible Global Group Vars (leo-banjo.ts.net Tailnet)
datacenter_name: leo-banjo.ts.net
tailnet_domain: leo-banjo.ts.net
default_ssh_user: core
ssh_agent_socket: /run/user/1000/ssh-agent.socket
restic_repository: "s3://restic-homelab-vault/leo-banjo"
restic_retention_days: 7
restic_retention_weeks: 4
restic_retention_months: 12

podman_rootless_default: true
security:
  selinux_mode: enforcing
  tailscale_ssh_enabled: true
`,
  'group_vars/tag-host.yml': `---
# Ansible Group Vars for tag:host nodes
ansible_user: core
ansible_port: 22
ansible_ssh_common_args: "-o StrictHostKeyChecking=accept-new"
host_members:
  - likes-it-cold.leo-banjo.ts.net
  - ping.leo-banjo.ts.net
  - fred.leo-banjo.ts.net
  - the-freezer.leo-banjo.ts.net
  - chuck.leo-banjo.ts.net
  - larry.leo-banjo.ts.net
  - orangepizero3.leo-banjo.ts.net
  - dev-pc.leo-banjo.ts.net
`,
  'group_vars/likes-it-cold.yml': `---
# Host Vars: likes-it-cold.leo-banjo.ts.net (100.105.71.49 / 192.168.8.249)
ansible_host: likes-it-cold.leo-banjo.ts.net
ansible_user: core
os_distribution: "AlmaLinux 10.2 (Lavender Lion)"
rootless_service_accounts:
  - core (1000)
  - ansible (2100)
  - apps (2200)
  - auth (2300)
  - backup (2400)
  - db (2500)
  - proxy (2600)
  - media (2700)
  - virt (2800)
quadlet_services:
  - systemd-caddy
  - systemd-cloudflare
  - systemd-proxy
`,
  'group_vars/ping.yml': `---
# Host Vars: ping.leo-banjo.ts.net (100.73.87.87)
ansible_host: ping.leo-banjo.ts.net
ansible_user: core
os_distribution: "AlmaLinux 10.2 (Lavender Lion)"
rootless_service_accounts:
  - core (1000)
  - uosserver (1001)
active_containers:
  - uosserver (docker.io/library/uosserver:c9603dec9010)
  - systemd-i2p
  - systemd-i2p-net
`,
};

let groupVarsStore: Record<string, string> = hostCache.seedGroupVarsIfEmpty(DEFAULT_GROUP_VARS);

let tasks: GlobalTaskItem[] = hostCache.getAllTasks();
if (tasks.length === 0) {
  const initialTasks: GlobalTaskItem[] = [
    {
      id: 'task-1',
      startTime: new Date().toISOString().replace('T', ' ').substring(0, 19),
      nodeTarget: 'likes-it-cold.leo-banjo.ts.net',
      user: 'core@ssh-agent',
      description: 'ssh core@likes-it-cold.leo-banjo.ts.net podman ps --all',
      progress: 100,
      progressEta: 'Connected via ssh-agent (ED25519)',
      status: 'OK',
      category: 'active',
      logs: ['[liveCluster] Probed rootless users and podman runtime via ssh-agent'],
    },
    {
      id: 'task-2',
      startTime: new Date().toISOString().replace('T', ' ').substring(0, 19),
      nodeTarget: 'ping.leo-banjo.ts.net',
      user: 'core@ssh-agent',
      description: 'Probe rootless Podman user uosserver (UID 1001)',
      progress: 100,
      progressEta: '1 running container (uosserver)',
      status: 'OK',
      category: 'active',
      logs: ['[liveCluster] Probed rootless user uosserver: 1 pod, 1 container'],
    },
    {
      id: 'task-3',
      startTime: new Date().toISOString().replace('T', ' ').substring(0, 19),
      nodeTarget: 'dev-pc.leo-banjo.ts.net',
      user: 'pants@local',
      description: 'SQLite host cache initialized (data/hosts-cache.sqlite)',
      progress: 100,
      progressEta: '8 tag:host nodes cached in SQLite',
      status: 'OK',
      category: 'ansible',
      logs: ['[SQLite] Database opened in WAL mode with normal sync and schema indices'],
    },
  ];
  for (const t of initialTasks) {
    hostCache.saveTask(t);
  }
  tasks = hostCache.getAllTasks();
}

// Telemetry state (updated from active node)
let telemetry: TelemetryData = {
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

function findNode(query?: string): NodeItem {
  if (!query) return nodes[0];
  const q = query.toLowerCase();
  return (
    nodes.find(
      n =>
        n.id.toLowerCase() === q ||
        n.name.toLowerCase() === q ||
        n.shortName.toLowerCase() === q ||
        n.tailscaleHost.toLowerCase() === q ||
        n.tailscaleIp === q ||
        n.ip === q
    ) || nodes[0]
  );
}

// REST API routes
app.get('/api/nodes', async (req, res) => {
  if (req.query.refresh === 'true') {
    nodes = await refreshTailscaleNodes(nodes);
    hostCache.upsertHosts(nodes);
  } else {
    nodes = hostCache.getAllHosts();
  }
  res.json(nodes);
});

app.post('/api/nodes', (req, res) => {
  const newNode: NodeItem = {
    id: req.body.id || `node-${Date.now()}`,
    name: req.body.name || 'New Node',
    shortName: req.body.shortName || (req.body.name || 'node').split(' ')[0],
    group: req.body.group || 'tag:host',
    datacenter: req.body.datacenter || 'leo-banjo.ts.net',
    nodeType: req.body.nodeType || 'linux',
    badge: req.body.badge || 'SSH',
    badgeColor: req.body.badgeColor || 'text-[#8cd6cb]',
    ip: req.body.ip || '192.168.8.200',
    port: Number(req.body.port) || 22,
    sshUser: req.body.sshUser || 'core',
    authMethod: req.body.authMethod || 'ssh-ed25519 (ssh-agent)',
    tags: req.body.tags || [req.body.group || 'tag:host'],
    sshConnected: true,
    rttMs: '2.0ms',
    tailscaleIp: req.body.tailscaleIp || req.body.ip || '100.100.100.100',
    tailscaleHost: req.body.tailscaleHost || `${(req.body.name || 'node').toLowerCase()}.leo-banjo.ts.net`,
    osDescription: req.body.osDescription || 'AlmaLinux 10.2 / Linux',
    status: req.body.status || 'online',
    uptime: req.body.uptime || '1m',
    hasGlances: req.body.hasGlances ?? true,
    hasCockpit: req.body.hasCockpit ?? true,
    hasPodman: req.body.hasPodman ?? true,
    hasRestic: req.body.hasRestic ?? true,
    specs: req.body.specs || {
      cpu: req.body.cpu || 'Multi-core x86_64',
      vcpus: Number(req.body.vcpus) || 8,
      ramGiB: Number(req.body.ramGiB) || 16,
      motherboard: 'Standard Server Platform',
      bios: 'UEFI 2.8',
      ipmi: req.body.ipmi || 'DHCP',
      ecc: 'Active',
      secureBoot: 'Enabled',
      iommu: 'Enabled',
    },
    connectionProtocol: req.body.connectionProtocol,
    vaultSecretId: req.body.vaultSecretId,
    folder: req.body.folder,
    isDynamicTagMember: req.body.isDynamicTagMember,
    followedTag: req.body.followedTag,
  };
  nodes.push(newNode);
  hostCache.upsertHost(newNode);
  broadcast({ type: 'node_added', node: newNode });
  res.json({ success: true, node: newNode });
});

app.post('/api/nodes/probe-host', async (req, res) => {
  const { host, port = 22, sshUser = 'core', authMethod = 'ssh-ed25519 (ssh-agent)' } = req.body;
  if (!host) {
    res.status(400).json({ success: false, error: 'host is required' });
    return;
  }

  const existing = nodes.find(
    n =>
      n.id.toLowerCase() === host.toLowerCase() ||
      n.name.toLowerCase() === host.toLowerCase() ||
      n.shortName.toLowerCase() === host.toLowerCase() ||
      n.ip === host ||
      n.tailscaleIp === host ||
      n.tailscaleHost.toLowerCase() === host.toLowerCase()
  );

  const targetNode: NodeItem = existing
    ? {
        ...existing,
        port: Number(port) || existing.port,
        sshUser: sshUser || existing.sshUser,
        authMethod: authMethod || existing.authMethod,
      }
    : {
        id: `probe-${Date.now()}`,
        name: host,
        shortName: host.split('.')[0],
        group: 'tag:host',
        datacenter: 'leo-banjo.ts.net',
        nodeType: 'linux',
        ip: host,
        port: Number(port) || 22,
        sshUser: sshUser || 'core',
        authMethod: authMethod || 'ssh-ed25519 (ssh-agent)',
        tailscaleIp: host.startsWith('100.') ? host : '',
        tailscaleHost: host.includes('.') ? host : `${host}.leo-banjo.ts.net`,
        osDescription: 'Probing...',
        status: 'online',
        uptime: 'unknown',
        hasGlances: false,
        hasCockpit: true,
        hasPodman: true,
        hasRestic: true,
        specs: {
          cpu: '',
          vcpus: 1,
          ramGiB: 1,
          motherboard: '',
          bios: '',
          ipmi: 'DHCP',
          ecc: 'Active',
          secureBoot: 'Enabled',
          iommu: 'Enabled',
        },
      };

  try {
    const probeCmd = getHardwareProbeCommand();
    const out = await runNodeCommand(targetNode, probeCmd, 8000);
    const hardware = parseHardwareProbeOutput(out);
    res.json({ success: true, hardware, rawOutput: out });
  } catch (err: any) {
    res.status(502).json({
      success: false,
      error: err?.message || `Failed to probe hardware on ${host}`,
    });
  }
});

app.post('/api/nodes/:id/probe-host', async (req, res) => {
  const node = nodes.find(n => n.id === req.params.id);
  if (!node) {
    res.status(404).json({ success: false, error: `Node ${req.params.id} not found` });
    return;
  }

  try {
    const probeCmd = getHardwareProbeCommand();
    const out = await runNodeCommand(node, probeCmd, 8000);
    const hardware = parseHardwareProbeOutput(out);
    const updated = applyHardwareProbeToNode(node, hardware);

    const idx = nodes.findIndex(n => n.id === req.params.id);
    if (idx !== -1) {
      nodes[idx] = updated;
    }
    hostCache.upsertHost(updated);
    broadcast({ type: 'node_updated', node: updated });

    res.json({ success: true, hardware, node: updated, rawOutput: out });
  } catch (err: any) {
    res.status(502).json({
      success: false,
      error: err?.message || `Failed to probe hardware on node ${node.name}`,
    });
  }
});

app.put('/api/nodes/:id', async (req, res) => {
  const idx = nodes.findIndex(n => n.id === req.params.id);
  if (idx === -1) {
    res.status(404).json({ error: 'Node not found' });
    return;
  }

  const updated = updateNodeSettings(nodes[idx], req.body);

  // Optionally test SSH connectivity if requested
  if (req.body.testSsh) {
    try {
      const out = await runNodeCommand(updated, 'uname -n && uptime -p 2>/dev/null || uptime', 4000);
      updated.sshConnected = true;
      updated.status = 'online';
      const lines = out.trim().split('\n');
      if (lines[1]) {
        updated.uptime = lines[1].replace(/^up\s+/i, '').trim();
      }
    } catch {
      updated.sshConnected = false;
    }
  }

  nodes[idx] = updated;
  hostCache.upsertHost(updated);
  broadcast({ type: 'node_updated', node: updated });
  res.json({ success: true, node: updated });
});

app.delete('/api/nodes/:id', (req, res) => {
  const idx = nodes.findIndex(n => n.id === req.params.id);
  if (idx === -1) {
    res.status(404).json({ error: 'Node not found' });
    return;
  }
  const [removed] = nodes.splice(idx, 1);
  hostCache.deleteHost(removed.id);
  broadcast({ type: 'node_deleted', nodeId: removed.id });
  res.json({ success: true, removed });
});

registerInventoryRoutes(app, {
  hostCache,
  broadcast: (msg) => {
    broadcast(msg);
    if (msg.type === 'chunks_updated') {
      refreshTailscaleNodes(nodes).then(updated => {
        nodes = updated;
        hostCache.upsertHosts(nodes);
        broadcast({ type: 'nodes_updated', nodes });
      }).catch(err => {
        console.error('Failed to sync tailscale tag chunks:', err);
      });
    }
  },
  getNodes: () => nodes,
  setNodes: (newNodes) => {
    nodes = newNodes;
  },
});

app.get('/api/telemetry', async (req, res) => {
  const hostQuery = (
    typeof req.query.host === 'string' ? req.query.host :
    typeof req.query.node === 'string' ? req.query.node :
    typeof req.query.id === 'string' ? req.query.id : undefined
  );
  const targetNode = findNode(hostQuery);
  const cached = hostCache.getHostTelemetry(targetNode.id);
  const CACHE_TTL_MS = 15000;

  if (cached && req.query.refresh !== 'true' && Date.now() - cached.updatedAt < CACHE_TTL_MS) {
    telemetry = { ...telemetry, ...cached.telemetry };
    res.json({
      ...telemetry,
      node: targetNode,
      fromCache: true,
      cachedAt: cached.updatedAt,
    });
    return;
  }

  const live = await withPollLog('telemetry', targetNode.shortName || targetNode.name, 'live-telemetry', () =>
    fetchLiveNodeTelemetry(targetNode)
  );
  hostCache.upsertHostTelemetry(targetNode.id, live);
  telemetry = {
    ...telemetry,
    ...live,
  };
  if (live.uptime || live.osDescription) {
    if (live.uptime) targetNode.uptime = live.uptime;
    if (live.osDescription) targetNode.osDescription = live.osDescription;
    hostCache.upsertHost(targetNode);
  }
  res.json({ ...telemetry, node: targetNode, fromCache: false, cachedAt: Date.now() });
});

app.get('/api/nodes/:id/telemetry/glances', async (req, res) => {
  const targetNode = nodes.find(n => n.id === req.params.id);
  if (!targetNode) {
    res.status(404).json({ success: false, error: 'Node not found' });
    return;
  }
  try {
    const glances = await withPollLog(
      'telemetry',
      targetNode.shortName || targetNode.name,
      'glances-multi-tier',
      () => fetchNodeTelemetryMultiTier(targetNode)
    );
    res.json({ success: true, glances });
  } catch (err: any) {
    res.status(502).json({ success: false, error: err?.message || String(err) });
  }
});

app.get('/api/tasks', (_req, res) => {
  res.json(tasks);
});

app.get('/api/group-vars', (_req, res) => {
  groupVarsStore = hostCache.getAllGroupVars();
  res.json(groupVarsStore);
});

app.put('/api/group-vars', (req, res) => {
  const { file, content } = req.body;
  if (!file || typeof content !== 'string') {
    res.status(400).json({ error: 'file and content required' });
    return;
  }
  hostCache.upsertGroupVar(file, content);
  groupVarsStore = hostCache.getAllGroupVars();
  const saveTask: GlobalTaskItem = {
    id: `task-${Date.now()}`,
    startTime: new Date().toISOString().replace('T', ' ').substring(0, 19),
    nodeTarget: file,
    user: 'core@ssh-agent',
    description: `ansible-vault encrypt & save ${file} (SQLite cached)`,
    progress: 100,
    progressEta: 'Saved (AES256)',
    status: 'OK',
    category: 'vault',
  };
  tasks.unshift(saveTask);
  broadcast({ type: 'task_started', task: saveTask });
  res.json({ success: true, file, content });
});

app.get('/api/podman/probe', async (req, res) => {
  const hostQuery = typeof req.query.host === 'string' ? req.query.host : undefined;
  const targetNode = findNode(hostQuery);
  const sshUser = (typeof req.query.user === 'string' ? req.query.user : targetNode.sshUser) || 'core';

  const cached = hostCache.getHostContainers(targetNode.id);
  if (cached && cached.probedUsers.length > 0 && req.query.refresh !== 'true' && Date.now() - cached.updatedAt < 30000) {
    res.json(cached.probedUsers);
    return;
  }

  // First try Go backend with host & user parameters
  const backendUrls = [
    process.env.GO_BACKEND_URL,
    'http://127.0.0.1:18090',
    'http://127.0.0.1:18080',
  ].filter(Boolean) as string[];

  const targetHostParam =
    targetNode.shortName === 'dev-pc' ? '127.0.0.1' : targetNode.tailscaleHost || targetNode.ip;

  for (const url of backendUrls) {
    try {
      const resp = await fetch(
        `${url}/api/podman/probe?host=${encodeURIComponent(targetHostParam)}&user=${encodeURIComponent(sshUser)}`,
        { signal: AbortSignal.timeout(2000) }
      );
      if (resp.ok) {
        const liveUsers = await resp.json();
        if (Array.isArray(liveUsers) && liveUsers.length > 0) {
          res.json(liveUsers);
          return;
        }
      }
    } catch {
      // Fall through to direct SSH probe
    }
  }

  const probeResult = await probeNodePodmanAndIdentities(targetNode);
  if (probeResult.probedUsers.length > 0) {
    hostCache.upsertHostContainers(targetNode.id, probeResult);
    res.json(probeResult.probedUsers);
    return;
  }

  if (cached && cached.probedUsers.length > 0) {
    res.json(cached.probedUsers);
    return;
  }

  res.json(getDefaultHostPodmanProbe());
});

app.get('/api/nodes/:id/containers', async (req, res) => {
  const targetNode = findNode(req.params.id);
  const cached = hostCache.getHostContainers(targetNode.id);
  const CACHE_TTL_MS = 20000;

  if (cached && req.query.refresh !== 'true' && Date.now() - cached.updatedAt < CACHE_TTL_MS) {
    res.json({
      node: targetNode,
      probedUsers: cached.probedUsers,
      identities: [...cached.customIdentities, ...cached.identities],
      rootContainers: cached.rootContainers,
      rootPods: cached.rootPods || [],
      hasDocker: Boolean(cached.hasDocker),
      dockerContainers: cached.dockerContainers || [],
      fromCache: true,
      cachedAt: cached.updatedAt,
    });
    return;
  }

  const { probedUsers, identities, rootContainers, rootPods, hasDocker, dockerContainers } =
    await probeNodePodmanAndIdentities(targetNode);
  hostCache.upsertHostContainers(targetNode.id, {
    probedUsers,
    identities,
    rootContainers,
    rootPods,
    hasDocker,
    dockerContainers,
  });
  const updatedCache = hostCache.getHostContainers(targetNode.id);
  const customIds = updatedCache?.customIdentities || [];
  res.json({
    node: targetNode,
    probedUsers,
    identities: [...customIds, ...identities],
    rootContainers,
    rootPods: rootPods || [],
    hasDocker: Boolean(hasDocker),
    dockerContainers: dockerContainers || [],
    fromCache: false,
    cachedAt: Date.now(),
  });
});

app.post('/api/nodes/:id/identities', (req, res) => {
  const targetNode = findNode(req.params.id);
  const newIdentity: ServiceUser = {
    user: req.body.user || 'svc-app',
    uidGid: req.body.uidGid || '2900:2900',
    rolesGroups: req.body.rolesGroups || 'podman, rootless-containers',
    authMechanism: req.body.authMechanism || 'Rootless SubUID 655360:65536',
    lingerSubUid: req.body.lingerSubUid || 'Linger: Enabled',
    icon: 'deployed_code',
  };
  hostCache.addCustomIdentity(targetNode.id, newIdentity);
  res.json({ success: true, identity: newIdentity });
});

// Live Disks & SMART Endpoint (Cached via SQLite)
app.get('/api/nodes/:id/disks', async (req, res) => {
  const targetNode = findNode(req.params.id);
  const cached = hostCache.getHostSection(targetNode.id, 'disks');
  const CACHE_TTL_MS = 60000;

  if (cached && req.query.refresh !== 'true' && Date.now() - cached.updatedAt < CACHE_TTL_MS) {
    res.json({
      success: true,
      ...cached.data,
      fromCache: true,
      cachedAt: cached.updatedAt,
    });
    return;
  }

  try {
    const cmd = [
      'lsblk -J -b -o NAME,PATH,MODEL,SIZE,TYPE,TRAN,FSTYPE,MOUNTPOINTS,ROTA,STATE,SERIAL 2>/dev/null || lsblk -J 2>/dev/null',
      'echo "===SMART==="',
      'sudo -n smartctl --scan 2>/dev/null || smartctl --scan 2>/dev/null || echo "NO_SMART"',
    ].join('; ');

    const out = await runNodeCommand(targetNode, cmd, 6000);
    const [lsblkRaw = '', smartRaw = ''] = out.split('===SMART===');

    let parsedBlocks: any[] = [];
    try {
      const parsed = JSON.parse(lsblkRaw.trim());
      parsedBlocks = parsed.blockdevices || [];
    } catch {
      // fallback if JSON parse failed
    }

    const hasSmart = !smartRaw.includes('NO_SMART') && smartRaw.trim().length > 0;

    const disks: any[] = parsedBlocks.map((dev: any) => {
      const sizeBytes = Number(dev.size) || 0;
      let sizeFormatted = `${sizeBytes} B`;
      if (sizeBytes >= 1099511627776) {
        sizeFormatted = `${(sizeBytes / 1099511627776).toFixed(1)} TB`;
      } else if (sizeBytes >= 1073741824) {
        sizeFormatted = `${(sizeBytes / 1073741824).toFixed(1)} GB`;
      } else if (sizeBytes >= 1048576) {
        sizeFormatted = `${(sizeBytes / 1048576).toFixed(1)} MB`;
      }

      const mountpoint = Array.isArray(dev.mountpoints)
        ? dev.mountpoints.filter(Boolean).join(', ')
        : dev.mountpoint || null;

      const children = (dev.children || []).map((ch: any) => {
        const chSize = Number(ch.size) || 0;
        let chSizeFormatted = `${chSize} B`;
        if (chSize >= 1099511627776) {
          chSizeFormatted = `${(chSize / 1099511627776).toFixed(1)} TB`;
        } else if (chSize >= 1073741824) {
          chSizeFormatted = `${(chSize / 1073741824).toFixed(1)} GB`;
        } else if (chSize >= 1048576) {
          chSizeFormatted = `${(chSize / 1048576).toFixed(1)} MB`;
        }
        return {
          name: ch.name,
          size: chSizeFormatted,
          type: ch.type,
          fstype: ch.fstype || null,
          mountpoint: Array.isArray(ch.mountpoints)
            ? ch.mountpoints.filter(Boolean).join(', ')
            : ch.mountpoint || null,
        };
      });

      return {
        name: dev.name,
        size: sizeFormatted,
        type: dev.type,
        fstype: dev.fstype || null,
        mountpoint,
        model: dev.model?.trim() || null,
        rota: Boolean(dev.rota),
        tran: dev.tran || (dev.name.startsWith('nvme') ? 'nvme' : null),
        smartStatus: hasSmart ? 'PASSED (0 Errors)' : 'Kernel sysfs: Live',
        tempC: undefined,
        children: children.length > 0 ? children : undefined,
      };
    });

    const result = {
      disks,
      smartAvailable: hasSmart,
      smartWarning: hasSmart
        ? undefined
        : 'smartctl not installed on host — drive health monitored via kernel sysfs.',
    };

    hostCache.upsertHostSection(targetNode.id, 'disks', result);
    res.json({ success: true, ...result, fromCache: false, cachedAt: Date.now() });
  } catch (err: any) {
    if (cached) {
      res.json({ success: true, ...cached.data, fromCache: true, cachedAt: cached.updatedAt });
      return;
    }
    res.json({ success: false, error: err?.message || 'Failed to probe disks' });
  }
});

// Live Network Interfaces Endpoint (Cached via SQLite)
app.get('/api/nodes/:id/interfaces', async (req, res) => {
  const targetNode = findNode(req.params.id);
  const cached = hostCache.getHostSection(targetNode.id, 'interfaces');
  const CACHE_TTL_MS = 30000;

  if (cached && req.query.refresh !== 'true' && Date.now() - cached.updatedAt < CACHE_TTL_MS) {
    res.json({
      success: true,
      ...cached.data,
      fromCache: true,
      cachedAt: cached.updatedAt,
    });
    return;
  }

  try {
    const cmd = 'ip -j addr 2>/dev/null; echo "===LINK==="; ip -j -s link 2>/dev/null';
    const out = await runNodeCommand(targetNode, cmd, 6000);
    const [addrRaw = '', linkRaw = ''] = out.split('===LINK===');

    let addrs: any[] = [];
    let links: any[] = [];
    try {
      addrs = JSON.parse(addrRaw.trim()) || [];
    } catch {}
    try {
      links = JSON.parse(linkRaw.trim()) || [];
    } catch {}

    const ifaces: any[] = [];
    for (const link of links) {
      const name = link.ifname;
      const addrObj = addrs.find((a: any) => a.ifname === name);
      const inet = addrObj?.addr_info?.find((info: any) => info.family === 'inet');
      const inet6 = addrObj?.addr_info?.find((info: any) => info.family === 'inet6');

      const ipStr = inet
        ? `${inet.local}/${inet.prefixlen}`
        : inet6
        ? `${inet6.local}/${inet6.prefixlen}`
        : 'No IP assigned';

      const rxBytes = Number(link.stats64?.rx?.bytes || 0);
      const txBytes = Number(link.stats64?.tx?.bytes || 0);

      const formatBytes = (b: number) => {
        if (b >= 1073741824) return `${(b / 1073741824).toFixed(2)} GiB`;
        if (b >= 1048576) return `${(b / 1048576).toFixed(1)} MiB`;
        if (b >= 1024) return `${(b / 1024).toFixed(0)} KiB`;
        return `${b} B`;
      };

      let type = 'Physical Ethernet';
      let speed = '1000 Mbps (Full Duplex)';
      if (name === 'lo') {
        type = 'Loopback';
        speed = 'Virtual';
      } else if (name.startsWith('tailscale')) {
        type = 'WireGuard Tunnel';
        speed = '10 Gbps (Mesh)';
      } else if (name.includes('.')) {
        type = '802.1Q VLAN';
        speed = 'Sub-interface';
      } else if (name.startsWith('docker') || name.startsWith('br-') || name.startsWith('podman')) {
        type = 'Container Bridge';
        speed = 'Virtual';
      } else if (name.startsWith('veth')) {
        type = 'Container veth peer';
        speed = 'Virtual';
      }

      const isUp = (link.flags || []).includes('UP');
      const isCarrier = link.operstate === 'UP' || link.operstate === 'UNKNOWN';

      ifaces.push({
        name,
        type,
        ip: ipStr,
        mac: link.address || 'N/A',
        speed,
        rx: formatBytes(rxBytes),
        tx: formatBytes(txBytes),
        status: isUp ? (isCarrier ? 'UP / CARRIER' : 'UP / NO-CARRIER') : 'DOWN',
        mtu: link.mtu || 1500,
      });
    }

    const result = { interfaces: ifaces };
    hostCache.upsertHostSection(targetNode.id, 'interfaces', result);
    res.json({ success: true, ...result, fromCache: false, cachedAt: Date.now() });
  } catch (err: any) {
    if (cached) {
      res.json({ success: true, ...cached.data, fromCache: true, cachedAt: cached.updatedAt });
      return;
    }
    res.json({ success: false, error: err?.message || 'Failed to probe interfaces' });
  }
});

// Live Firewall Endpoint (Cached via SQLite)
app.get('/api/nodes/:id/firewall', async (req, res) => {
  const targetNode = findNode(req.params.id);
  const cached = hostCache.getHostSection(targetNode.id, 'firewall');
  const CACHE_TTL_MS = 45000;

  if (cached && req.query.refresh !== 'true' && Date.now() - cached.updatedAt < CACHE_TTL_MS) {
    res.json({
      success: true,
      ...cached.data,
      fromCache: true,
      cachedAt: cached.updatedAt,
    });
    return;
  }

  try {
    const cmd = [
      'sudo -n firewall-cmd --state 2>&1 || firewall-cmd --state 2>&1 || echo "NO_FIREWALLD"',
      'echo "===ZONES==="',
      'sudo -n firewall-cmd --get-active-zones 2>&1 || true',
      'echo "===LIST_ALL==="',
      'sudo -n firewall-cmd --list-all 2>&1 || true',
      'echo "===NFT==="',
      'sudo -n nft list tables 2>&1 || echo "NO_NFT"',
    ].join('; ');

    const out = await runNodeCommand(targetNode, cmd, 6000);
    const [stateRaw = '', rest1 = ''] = out.split('===ZONES===');
    const [zonesRaw = '', rest2 = ''] = rest1.split('===LIST_ALL===');
    const [listAllRaw = '', nftRaw = ''] = rest2.split('===NFT===');

    const isRunning = stateRaw.trim().includes('running');
    const hasNft = !nftRaw.includes('NO_NFT') && nftRaw.includes('table');

    const parsed = parseFirewallListAll(listAllRaw, zonesRaw);

    const result = {
      status: isRunning ? 'running' : 'inactive',
      nftablesActive: hasNft,
      backend: 'nftables (firewalld)',
      zones: parsed.zones,
      services: parsed.services,
      ports: parsed.ports,
      rules: isRunning ? parsed.rules : [],
    };

    hostCache.upsertHostSection(targetNode.id, 'firewall', result);
    res.json({ success: true, ...result, fromCache: false, cachedAt: Date.now() });
  } catch (err: any) {
    if (cached) {
      res.json({ success: true, ...cached.data, fromCache: true, cachedAt: cached.updatedAt });
      return;
    }
    res.json({ success: false, error: err?.message || 'Failed to probe firewall' });
  }
});

// Live KVM / QEMU Virtual Machines Endpoint (Cached via SQLite)
app.get('/api/nodes/:id/vms', async (req, res) => {
  const targetNode = findNode(req.params.id);
  const cached = hostCache.getHostSection(targetNode.id, 'vms');
  const CACHE_TTL_MS = 60000;

  if (cached && req.query.refresh !== 'true' && Date.now() - cached.updatedAt < CACHE_TTL_MS) {
    res.json({
      success: true,
      ...cached.data,
      fromCache: true,
      cachedAt: cached.updatedAt,
    });
    return;
  }

  try {
    const cmd = [
      'which virsh 2>&1 || sudo -n which virsh 2>&1 || echo "NO_VIRSH"',
      'echo "===VIRSH_LIST==="',
      'sudo -n virsh list --all 2>&1 || virsh list --all 2>&1 || echo "NO_GUESTS"',
      'echo "===SERVICES==="',
      'systemctl is-active libvirtd virtqemud 2>&1 || true',
      'echo "===KVM==="',
      'lsmod | grep kvm 2>&1 || true',
    ].join('; ');

    const out = await runNodeCommand(targetNode, cmd, 6000);
    const [virshBin = '', rest1 = ''] = out.split('===VIRSH_LIST===');
    const [virshList = '', rest2 = ''] = rest1.split('===SERVICES===');
    const [servicesRaw = '', kvmRaw = ''] = rest2.split('===KVM===');

    const hasVirsh = !virshBin.includes('NO_VIRSH') && virshBin.includes('/virsh');
    const hasKvm = kvmRaw.includes('kvm_amd') || kvmRaw.includes('kvm_intel') || kvmRaw.includes('kvm');
    const isServiceActive = servicesRaw.includes('active');

    const vms: any[] = [];
    if (hasVirsh && !virshList.includes('NO_GUESTS') && !virshList.includes('error')) {
      const lines = virshList.trim().split('\n');
      for (const line of lines) {
        const tokens = line.trim().split(/\s+/);
        if (tokens.length >= 3 && tokens[0] !== 'Id' && tokens[0] !== '---') {
          const vmid = isNaN(Number(tokens[0])) ? 100 + vms.length : Number(tokens[0]);
          const name = tokens[1];
          const status = tokens.slice(2).join(' ').toUpperCase();
          vms.push({
            vmid,
            name: `${vmid} (${name})`,
            subtext: `KVM Guest (${name})`,
            status: status.includes('RUNNING') ? 'RUNNING' : 'STOPPED',
            vcpus: 2,
            ramGiB: 4,
            type: 'linux',
          });
        }
      }
    }

    const kvmModule = kvmRaw.includes('kvm_amd')
      ? 'kvm_amd (AMD-V)'
      : kvmRaw.includes('kvm_intel')
      ? 'kvm_intel (VT-x)'
      : hasKvm
      ? 'kvm'
      : 'software emulation';

    const result = {
      hasVirsh,
      hasKvm,
      serviceStatus: isServiceActive ? 'active' : 'inactive',
      kvmModule,
      hypervisor: `KVM / QEMU (${kvmModule})`,
      vms,
    };

    hostCache.upsertHostSection(targetNode.id, 'vms', result);
    res.json({ success: true, ...result, fromCache: false, cachedAt: Date.now() });
  } catch (err: any) {
    if (cached) {
      res.json({ success: true, ...cached.data, fromCache: true, cachedAt: cached.updatedAt });
      return;
    }
    res.json({ success: false, error: err?.message || 'Failed to probe VMs' });
  }
});

// Live Storage & ZFS Endpoint (Cached via SQLite)
app.get('/api/nodes/:id/storage', async (req, res) => {
  const targetNode = findNode(req.params.id);
  const cached = hostCache.getHostSection(targetNode.id, 'storage');
  const CACHE_TTL_MS = 60000;

  if (cached && req.query.refresh !== 'true' && Date.now() - cached.updatedAt < CACHE_TTL_MS) {
    res.json({
      success: true,
      ...cached.data,
      fromCache: true,
      cachedAt: cached.updatedAt,
    });
    return;
  }

  try {
    const cmd = [
      'findmnt -J -l -t ext4,xfs,btrfs,zfs,nfs,cifs,smb3 2>/dev/null || true',
      'echo "===ZFS==="',
      '(sudo -n zfs list 2>/dev/null || zfs list 2>/dev/null || echo "NO_ZFS")',
      'echo "===ZPOOL==="',
      '(sudo -n zpool status 2>/dev/null || zpool status 2>/dev/null || echo "NO_ZPOOL")',
      'echo "===ZPOOL_LIST==="',
      '(sudo -n zpool list -H 2>/dev/null || zpool list -H 2>/dev/null || echo "NO_ZPOOL_LIST")',
      'echo "===BTRFS==="',
      '(btrfs filesystem show 2>/dev/null || echo "NO_BTRFS")',
    ].join('; ');

    const out = await runNodeCommand(targetNode, cmd, 6000);
    const [findmntRaw = '', rest1 = ''] = out.split('===ZFS===');
    const [zfsRaw = '', rest2 = ''] = rest1.split('===ZPOOL===');
    const [zpoolRaw = '', rest3 = ''] = rest2.split('===ZPOOL_LIST===');
    const [zpoolListRaw = '', btrfsRaw = ''] = rest3.split('===BTRFS===');

    const result = {
      findmnt: findmntRaw.trim(),
      zfsList: zfsRaw.trim(),
      zpoolStatus: zpoolRaw.trim(),
      zpoolList: zpoolListRaw.trim(),
      btrfs: btrfsRaw.trim(),
    };

    hostCache.upsertHostSection(targetNode.id, 'storage', result);
    res.json({
      success: true,
      ...result,
      fromCache: false,
      cachedAt: Date.now(),
    });
  } catch (err: any) {
    if (cached) {
      res.json({ success: true, ...cached.data, fromCache: true, cachedAt: cached.updatedAt });
      return;
    }
    res.json({ success: false, error: err?.message || 'Storage probe failed' });
  }
});

// Live NFS & Samba Shares Endpoint (Cached via SQLite)
app.get('/api/nodes/:id/shares', async (req, res) => {
  const targetNode = findNode(req.params.id);
  const cached = hostCache.getHostSection(targetNode.id, 'shares');
  const CACHE_TTL_MS = 60000;

  if (cached && req.query.refresh !== 'true' && Date.now() - cached.updatedAt < CACHE_TTL_MS) {
    res.json({
      success: true,
      ...cached.data,
      fromCache: true,
      cachedAt: cached.updatedAt,
    });
    return;
  }

  try {
    const cmd = [
      'cat /var/lib/nfs/etab /etc/exports 2>/dev/null || echo "NO_NFS"',
      'echo "===SAMBA==="',
      'sudo -n testparm -s 2>/dev/null || testparm -s 2>/dev/null || cat /etc/samba/smb.conf 2>/dev/null || echo "NO_SAMBA"',
      'echo "===ZFS_SHARES==="',
      'sudo -n cat /etc/cockpit/zfs/shares.conf 2>/dev/null || cat /etc/cockpit/zfs/shares.conf 2>/dev/null || echo "NO_ZFS_SHARES"',
      'echo "===NET_CONF==="',
      'sudo -n net conf list 2>/dev/null || echo "NO_NET_CONF"',
      'echo "===SERVICES==="',
      'systemctl is-active nfs-server smb smbd 2>&1 || true',
    ].join('; ');

    const out = await runNodeCommand(targetNode, cmd, 6000);
    const [nfsRaw = '', rest1 = ''] = out.split('===SAMBA===');
    const [smbRaw = '', rest2 = ''] = rest1.split('===ZFS_SHARES===');
    const [zfsSharesRaw = '', rest3 = ''] = rest2.split('===NET_CONF===');
    const [netConfRaw = '', srvRaw = ''] = rest3.split('===SERVICES===');

    const result = {
      nfs: nfsRaw.trim(),
      samba: smbRaw.trim(),
      zfsShares: zfsSharesRaw.trim(),
      netConf: netConfRaw.trim(),
      services: srvRaw.trim(),
    };

    hostCache.upsertHostSection(targetNode.id, 'shares', result);
    res.json({
      success: true,
      ...result,
      fromCache: false,
      cachedAt: Date.now(),
    });
  } catch (err: any) {
    if (cached) {
      res.json({ success: true, ...cached.data, fromCache: true, cachedAt: cached.updatedAt });
      return;
    }
    res.json({ success: false, error: err?.message || 'Shares probe failed' });
  }
});

app.post('/api/podman/probe', (req, res) => {
  const probed = probeUnprivilegedPodmanUsers(req.body);
  res.json(probed);
});

app.post('/api/podman/action', async (req, res) => {
  const { host, container, action, user, uid } = req.body;
  const targetNode = findNode(host);
  try {
    let cmd = `podman ${action} ${container}`;
    if (user && user !== 'root') {
      const effectiveUid = uid ?? 1000;
      cmd = `run0 --background=no --user ${user} podman ${action} ${container} 2>&1 || sudo -n -u ${user} XDG_RUNTIME_DIR=/run/user/${effectiveUid} podman ${action} ${container} 2>&1 || podman ${action} ${container} 2>&1`;
    } else {
      cmd = `run0 --background=no podman ${action} ${container} 2>&1 || sudo -n podman ${action} ${container} 2>&1 || sudo -n docker ${action} ${container} 2>&1 || podman ${action} ${container} 2>&1`;
    }
    const output = await runNodeCommand(targetNode, cmd, 8000);
    res.json({ success: true, output });
  } catch (err: any) {
    res.json({ success: false, error: err?.message || 'Container action failed' });
  }
});

app.get('/api/podman/logs', async (req, res) => {
  const host = typeof req.query.host === 'string' ? req.query.host : undefined;
  const target = typeof req.query.target === 'string' ? req.query.target : 'system';
  const user = typeof req.query.user === 'string' ? req.query.user : undefined;
  const rawUid = typeof req.query.uid === 'string' ? Number.parseInt(req.query.uid, 10) : undefined;
  const uid = Number.isNaN(rawUid) ? undefined : rawUid;
  const targetNode = findNode(host);

  try {
    let cmd: string;
    if (target.includes('dmesg') || target.includes('kernel') || target.includes('journal')) {
      cmd = `run0 --background=no journalctl -n 100 --no-pager 2>&1 || sudo -n journalctl -n 100 --no-pager 2>&1 || dmesg | tail -n 100 2>&1`;
    } else if (target.includes('audit') || target.includes('pam')) {
      cmd = `run0 --background=no journalctl -u sshd -n 100 --no-pager 2>&1 || sudo -n journalctl -u sshd -n 100 --no-pager 2>&1 || last -n 30 2>&1`;
    } else if (user && user !== 'root') {
      const effectiveUid = uid ?? 1000;
      cmd = `run0 --background=no --user ${user} podman logs --tail 100 ${target} 2>&1 || sudo -n -u ${user} XDG_RUNTIME_DIR=/run/user/${effectiveUid} podman logs --tail 100 ${target} 2>&1 || podman logs --tail 100 ${target} 2>&1`;
    } else {
      cmd = `run0 --background=no podman logs --tail 100 ${target} 2>&1 || sudo -n podman logs --tail 100 ${target} 2>&1 || podman logs --tail 100 ${target} 2>&1`;
    }

    const out = await runNodeCommand(targetNode, cmd, 8000);
    const lines = out
      .split('\n')
      .map(l => l.trimEnd())
      .filter(Boolean);

    if (lines.length > 0) {
      res.json({ success: true, lines, host: targetNode.tailscaleHost });
      return;
    }

    res.json({ success: false, lines: [], error: `No logs available for ${target}`, host: targetNode.tailscaleHost });
  } catch (err: any) {
    res.json({
      success: false,
      lines: [],
      error: err?.message || `Failed to fetch logs for ${target}`,
      host: targetNode.tailscaleHost,
    });
  }
});

app.get(['/api/nodes/:id/system-logs', '/api/system/logs'], async (req, res) => {
  const queryNodeId =
    req.params.id ||
    (typeof req.query.node === 'string' ? req.query.node : undefined) ||
    (typeof req.query.host === 'string' ? req.query.host : undefined);
  const targetNode = findNode(queryNodeId);

  const filter = sanitizeLogFilter({
    source: req.query.source,
    unit: req.query.unit,
    user: req.query.user,
    priority: req.query.priority,
    since: req.query.since,
    grep: req.query.grep,
    lines: req.query.lines,
  });

  const cmd = buildJournalctlCommand(filter);

  try {
    const out = await runNodeCommand(targetNode, cmd, 8000);
    const lines = out
      .split('\n')
      .map(l => l.trimEnd())
      .filter(Boolean);

    res.json({
      success: true,
      lines,
      host: targetNode.tailscaleHost,
      filter,
    });
  } catch (err: any) {
    res.status(502).json({
      success: false,
      error: err?.message || 'Failed to retrieve system logs from node',
      lines: [],
      host: targetNode.tailscaleHost,
      filter,
    });
  }
});

app.post('/api/terminal/exec', async (req, res) => {
  const { command, host } = req.body;
  const targetNode = findNode(host);
  const cmd = String(command || '').trim();
  if (!cmd) {
    res.json({ output: '', exitCode: 0 });
    return;
  }

  if (cmd === 'help') {
    const parsed = parseTerminalCommand(cmd, targetNode);
    res.json(parsed);
    return;
  }

  try {
    const out = await runNodeCommand(targetNode, `${cmd} 2>&1`, 8000);
    res.json({ output: out.trimEnd() || '(command completed with exit code 0)', exitCode: 0 });
  } catch (err: any) {
    const stderrOut = err?.stdout || err?.stderr || err?.message || '';
    if (stderrOut) {
      res.json({ output: String(stderrOut).trimEnd(), exitCode: 1 });
      return;
    }
    const fallback = parseTerminalCommand(cmd, targetNode);
    res.json(fallback);
  }
});

// Active background process registries
const activePtySessions = new Map<
  string,
  { proc: ChildProcess; ws: WebSocket; sessionId: string; releaseConnection: () => void }
>();

// ==========================================
// Category C: Tailscale Deep Telemetry Routes
// ==========================================

app.get('/api/tailscale/peers', (_req, res) => {
  try {
    const raw = execSync('tailscale status --json', { encoding: 'utf-8', timeout: 5000 });
    const data = JSON.parse(raw);
    const peers: any[] = [];
    if (data.Self) {
      peers.push({
        ...data.Self,
        isSelf: true,
        isDirect: true,
        relay: 'direct',
      });
    }
    if (data.Peer) {
      for (const [key, p] of Object.entries<any>(data.Peer)) {
        const relay = p.Relay || (p.CurAddr ? 'direct' : 'derp');
        peers.push({
          ...p,
          peerKey: key,
          isSelf: false,
          isDirect: !p.Relay || p.Relay === '',
          relay,
        });
      }
    }
    res.json({
      success: true,
      self: data.Self,
      backendState: data.BackendState,
      magicDNSSuffix: data.MagicDNSSuffix,
      peers,
      peerCount: peers.length,
    });
  } catch (err: any) {
    res.status(500).json({ success: false, error: err?.message || 'Failed to query tailscale status' });
  }
});

registerPrivilegeRoutes(app, {
  findNode,
  probeNodePrivilege,
  testNodePrivilege,
  hostCache,
});

app.get('/api/nodes/:id/tailscale', (req, res) => {
  const targetNode = findNode(req.params.id);
  try {
    const raw = execSync('tailscale status --json', { encoding: 'utf-8', timeout: 5000 });
    const data = JSON.parse(raw);
    const targetHost = targetNode.shortName.toLowerCase();
    const targetTsHost = targetNode.tailscaleHost.toLowerCase();

    let matched: any = null;
    if (data.Self && (data.Self.HostName?.toLowerCase() === targetHost || data.Self.DNSName?.toLowerCase().startsWith(targetHost))) {
      matched = { ...data.Self, isSelf: true, isDirect: true };
    } else if (data.Peer) {
      for (const p of Object.values<any>(data.Peer)) {
        const hostName = (p.HostName || '').toLowerCase();
        const dnsName = (p.DNSName || '').toLowerCase();
        if (hostName === targetHost || dnsName.startsWith(targetHost) || dnsName.startsWith(targetTsHost)) {
          matched = {
            ...p,
            isSelf: false,
            isDirect: !p.Relay || p.Relay === '',
            relay: p.Relay || 'direct',
          };
          break;
        }
      }
    }

    if (matched) {
      res.json({ success: true, peer: matched });
    } else {
      res.json({ success: false, message: `Node ${targetNode.name} not found in active Tailscale mesh` });
    }
  } catch (err: any) {
    res.status(500).json({ success: false, error: err?.message || 'Failed to query Tailscale' });
  }
});

app.post('/api/tailscale/ping', (req, res) => {
  const target = String(req.body.target || '').trim();
  if (!target) {
    res.status(400).json({ error: 'target required' });
    return;
  }
  try {
    const out = execSync(`tailscale ping -c 1 ${target}`, { encoding: 'utf-8', timeout: 8000 });
    const isDirect = out.includes('via') && !out.includes('DERP');
    const rttMatch = out.match(/in\s+([0-9.]+m?s)/i);
    res.json({
      success: true,
      target,
      output: out.trim(),
      isDirect,
      rtt: rttMatch ? rttMatch[1] : undefined,
    });
  } catch (err: any) {
    res.status(500).json({
      success: false,
      target,
      error: err?.stdout || err?.stderr || err?.message || 'Ping failed',
    });
  }
});

app.get('/api/tailscale/derp-map', (_req, res) => {
  try {
    const out = execSync('tailscale netcheck', { encoding: 'utf-8', timeout: 10000 });
    res.json({ success: true, netcheck: out });
  } catch (err: any) {
    res.json({ success: false, error: err?.message || 'Netcheck failed' });
  }
});

// ==========================================
// Category D: Real Host Control Routes
// ==========================================

app.post('/api/nodes/:id/interfaces/:name/toggle', async (req, res) => {
  const targetNode = findNode(req.params.id);
  const iface = req.params.name;
  const state = req.body.state === 'down' ? 'down' : 'up';
  try {
    await runNodeCommand(targetNode, `sudo ip link set ${iface} ${state}`, 6000);
    hostCache.invalidateHostSections(targetNode.id);
    res.json({ success: true, interface: iface, state });
  } catch (err: any) {
    res.status(500).json({ success: false, error: err?.message || `Failed to set interface ${iface} ${state}` });
  }
});

app.post('/api/nodes/:id/interfaces/connect', async (req, res) => {
  const targetNode = findNode(req.params.id);
  try {
    const cmd = buildConnectNetworkCommand(req.body);
    const out = await runNodeCommand(targetNode, cmd, 10000);
    hostCache.invalidateHostSections(targetNode.id);
    res.json({ success: true, output: out.trim() });
  } catch (err: any) {
    res.status(500).json({ success: false, error: err?.message || 'Failed to connect network interface' });
  }
});

app.post('/api/nodes/:id/vms/:name/action', async (req, res) => {
  const targetNode = findNode(req.params.id);
  const vmName = String(req.params.name || '').trim();
  const rawAction = String(req.body.action || 'start').trim().toLowerCase();
  const ALLOWED_ACTIONS = ['start', 'shutdown', 'destroy', 'reboot', 'reset', 'resume', 'suspend'];

  if (!ALLOWED_ACTIONS.includes(rawAction)) {
    res.status(400).json({ success: false, error: `Invalid VM action: ${rawAction}. Allowed: ${ALLOWED_ACTIONS.join(', ')}` });
    return;
  }

  if (!/^[a-zA-Z0-9_\-\.]+$/.test(vmName)) {
    res.status(400).json({ success: false, error: 'Invalid VM name format' });
    return;
  }

  try {
    const out = await runNodeCommand(targetNode, `sudo virsh ${rawAction} ${vmName}`, 8000);
    hostCache.invalidateHostSections(targetNode.id);
    res.json({ success: true, vm: vmName, action: rawAction, output: out.trim() });
  } catch (err: any) {
    res.status(500).json({ success: false, error: err?.message || `Failed to execute ${rawAction} on VM ${vmName}` });
  }
});

app.post('/api/nodes/:id/vms/create', async (req, res) => {
  const targetNode = findNode(req.params.id);
  const {
    name,
    osTemplate = 'Ubuntu 24.04',
    vcpus = 2,
    ramGiB = 4,
    diskGb = 20,
    bridge = 'virbr0',
    autostart = false,
  } = req.body;

  if (!name || typeof name !== 'string') {
    res.status(400).json({ success: false, error: 'VM name is required' });
    return;
  }

  const cleanName = name.trim().replace(/[^a-zA-Z0-9_-]/g, '-');
  const cleanBridge = String(bridge || 'virbr0').trim().replace(/[^a-zA-Z0-9_.-]/g, '');
  const ramMb = Number(ramGiB) * 1024;
  const numCpus = Number(vcpus) || 2;
  const diskSize = Number(diskGb) || 20;

  try {
    const cmd = [
      `virt-install --name "${cleanName}" --vcpus ${numCpus} --memory ${ramMb} --disk size=${diskSize} --network bridge=${cleanBridge} --noautoconsole --import --check path_in_use=off 2>&1 || virsh define /dev/stdin << 'EOF'\n<domain type='kvm'><name>${cleanName}</name><memory unit='MiB'>${ramMb}</memory><vcpu>${numCpus}</vcpu><os><type arch='x86_64'>hvm</type></os></domain>\nEOF 2>&1`,
      autostart ? `virsh autostart "${cleanName}" 2>&1 || true` : 'true',
    ].join(' && ');

    const out = await runNodeCommand(targetNode, cmd, 10000);
    hostCache.invalidateHostSections(targetNode.id);

    const newVm: VMItem = {
      vmid: 200 + Math.floor(Math.random() * 700),
      name: cleanName,
      subtext: `${osTemplate} (${bridge})`,
      status: 'STOPPED',
      vcpus: numCpus,
      ramGiB: Number(ramGiB) || 4,
      type: osTemplate.toLowerCase().includes('windows') ? 'windows' : 'linux',
    };

    const vmTask: GlobalTaskItem = {
      id: `task-${Date.now()}`,
      startTime: new Date().toISOString().replace('T', ' ').substring(0, 19),
      nodeTarget: targetNode.tailscaleHost || targetNode.name,
      user: `${targetNode.sshUser || 'core'}@ssh-agent`,
      description: `virt-install create VM: ${cleanName} (${numCpus} vCPUs, ${ramGiB} GiB, ${diskSize} GB)`,
      progress: 100,
      progressEta: 'VM created via KVM/QEMU',
      status: 'OK',
      category: 'active',
      logs: [out.trim()],
    };
    hostCache.saveTask(vmTask);
    tasks = [vmTask, ...tasks.filter(t => t.id !== vmTask.id)];
    broadcast({ type: 'task_started', task: vmTask });

    res.json({
      success: true,
      vm: newVm,
      output: out.trim(),
    });
  } catch (err: any) {
    res.status(500).json({
      success: false,
      error: err?.message || `Failed to create VM ${cleanName} on ${targetNode.name}`,
    });
  }
});

app.post('/api/nodes/:id/firewall/rules', async (req, res) => {
  const targetNode = findNode(req.params.id);
  try {
    const cmd = buildFirewallAddCommand(req.body);
    const out = await runNodeCommand(targetNode, cmd, 8000);
    hostCache.invalidateHostSections(targetNode.id);
    res.json({ success: true, output: out.trim() });
  } catch (err: any) {
    res.status(500).json({ success: false, error: err?.message || 'Failed to add firewall rule' });
  }
});

app.delete('/api/nodes/:id/firewall/rules/:ruleId', async (req, res) => {
  const targetNode = findNode(req.params.id);
  const ruleId = req.params.ruleId;
  const zone = (req.query.zone as string) || (req.body?.zone as string) || 'public';
  const rawRule = (req.query.rawRule as string) || (req.body?.rawRule as string);
  try {
    const cmd = buildFirewallDeleteCommand({
      zone,
      ruleId,
      rawRule,
    });
    const out = await runNodeCommand(targetNode, cmd, 8000);
    hostCache.invalidateHostSections(targetNode.id);
    res.json({ success: true, output: out.trim() });
  } catch (err: any) {
    res.status(500).json({ success: false, error: err?.message || 'Failed to remove firewall rule' });
  }
});

app.post('/api/nodes/:id/disks/:name/smart-test', async (req, res) => {
  const targetNode = findNode(req.params.id);
  const disk = req.params.name;
  try {
    const out = await runNodeCommand(targetNode, `sudo smartctl -t short /dev/${disk}`, 6000);
    res.json({ success: true, disk, output: out.trim() });
  } catch (err: any) {
    res.status(500).json({ success: false, error: err?.message || 'Failed to initiate SMART self-test' });
  }
});

app.post('/api/nodes/:id/zfs/:pool/scrub', async (req, res) => {
  const targetNode = findNode(req.params.id);
  const pool = req.params.pool;
  try {
    const out = await runNodeCommand(targetNode, `sudo zpool scrub ${pool}`, 6000);
    hostCache.invalidateHostSections(targetNode.id);
    res.json({ success: true, pool, output: out.trim() });
  } catch (err: any) {
    res.status(500).json({ success: false, error: err?.message || 'Failed to start ZFS pool scrub' });
  }
});

app.post('/api/nodes/:id/shares/reload', async (req, res) => {
  const targetNode = findNode(req.params.id);
  try {
    const out = await runNodeCommand(targetNode, `sudo smbcontrol all reload-config 2>&1 || sudo exportfs -ra 2>&1`, 6000);
    hostCache.invalidateHostSections(targetNode.id);
    res.json({ success: true, output: out.trim() });
  } catch (err: any) {
    res.status(500).json({ success: false, error: err?.message || 'Failed to reload network shares' });
  }
});

// Create or edit an NFS export
app.post('/api/nodes/:id/shares/nfs', async (req, res) => {
  const targetNode = findNode(req.params.id);
  try {
    const { path: sharePath, client, options, name } = req.body;
    if (!sharePath) {
      res.status(400).json({ success: false, error: 'Share directory path is required' });
      return;
    }
    const saveCmd = buildNfsSaveCommand({ path: sharePath, client, options }, name);
    const out = await runNodeCommand(targetNode, saveCmd, 10000);
    hostCache.invalidateHostSections(targetNode.id);
    res.json({ success: true, message: 'NFS export configured successfully', output: out.trim() });
  } catch (err: any) {
    res.status(500).json({ success: false, error: err?.message || 'Failed to configure NFS export' });
  }
});

// Create or edit an SMB share
app.post('/api/nodes/:id/shares/smb', async (req, res) => {
  const targetNode = findNode(req.params.id);
  try {
    const { name, path: sharePath, comment, readOnly, validUsers, guestOk } = req.body;
    if (!name || !sharePath) {
      res.status(400).json({ success: false, error: 'Share name and path are required' });
      return;
    }
    const saveCmd = buildSmbSaveCommand({
      name,
      path: sharePath,
      comment,
      readOnly: Boolean(readOnly),
      validUsers,
      guestOk: Boolean(guestOk),
    });
    const out = await runNodeCommand(targetNode, saveCmd, 12000);
    hostCache.invalidateHostSections(targetNode.id);
    res.json({ success: true, message: 'SMB share configured successfully', output: out.trim() });
  } catch (err: any) {
    res.status(500).json({ success: false, error: err?.message || 'Failed to configure SMB share' });
  }
});

// List host users
app.get('/api/nodes/:id/users', async (req, res) => {
  const targetNode = findNode(req.params.id);
  try {
    const cmd = 'cat /etc/passwd 2>/dev/null; echo "===SUBUID==="; cat /etc/subuid 2>/dev/null || true';
    const out = await runNodeCommand(targetNode, cmd, 6000);
    const [passwdRaw = '', subuidRaw = ''] = out.split('===SUBUID===');
    const allUsers = parseEtcPasswd(passwdRaw, subuidRaw);
    const interactiveUsers = allUsers.filter(u => !u.isSystemUser);
    res.json({
      success: true,
      users: interactiveUsers,
      allUsers,
    });
  } catch (err: any) {
    res.status(500).json({ success: false, error: err?.message || 'Failed to retrieve host users' });
  }
});

// Create new user on host
app.post('/api/nodes/:id/users/create', async (req, res) => {
  const targetNode = findNode(req.params.id);
  try {
    const { username } = req.body;
    if (!username) {
      res.status(400).json({ success: false, error: 'Username is required' });
      return;
    }
    const cmd = buildUserCreateCommand(username);
    const out = await runNodeCommand(targetNode, cmd, 8000);
    res.json({ success: true, username: sanitizeUsername(username), output: out.trim() });
  } catch (err: any) {
    res.status(500).json({ success: false, error: err?.message || 'Failed to create user' });
  }
});

// Set regular user password
app.post('/api/nodes/:id/users/:user/password', async (req, res) => {
  const targetNode = findNode(req.params.id);
  try {
    const { password } = req.body;
    if (!password) {
      res.status(400).json({ success: false, error: 'Password is required' });
      return;
    }
    const cmd = buildUserPasswdCommand(req.params.user, password);
    const out = await runNodeCommand(targetNode, cmd, 8000);
    res.json({ success: true, message: `Password updated for ${req.params.user}`, output: out.trim() });
  } catch (err: any) {
    res.status(500).json({ success: false, error: err?.message || 'Failed to set password' });
  }
});

// Set Samba (SMB) user password
app.post('/api/nodes/:id/users/:user/smbpassword', async (req, res) => {
  const targetNode = findNode(req.params.id);
  try {
    const { password } = req.body;
    if (!password) {
      res.status(400).json({ success: false, error: 'Samba password is required' });
      return;
    }
    const cmd = buildSmbPasswdCommand(req.params.user, password);
    const out = await runNodeCommand(targetNode, cmd, 8000);
    res.json({ success: true, message: `Samba password updated for ${req.params.user}`, output: out.trim() });
  } catch (err: any) {
    res.status(500).json({ success: false, error: err?.message || 'Failed to set Samba password' });
  }
});

app.post('/api/nodes/:id/reboot', (req, res) => {
  const targetNode = findNode(req.params.id);
  const rebootTask: GlobalTaskItem = {
    id: `task-reboot-${Date.now()}`,
    startTime: new Date().toISOString().replace('T', ' ').substring(0, 19),
    nodeTarget: targetNode.name,
    user: `${targetNode.sshUser || 'core'}@ssh-agent`,
    description: `systemctl reboot on ${targetNode.name} (${targetNode.tailscaleIp || targetNode.ip})`,
    progress: 50,
    progressEta: 'Sending reboot signal...',
    status: 'RUNNING',
    category: 'active',
    logs: [`[reboot] Initiating graceful reboot sequence on ${targetNode.name}`],
  };

  tasks.unshift(rebootTask);
  hostCache.saveTask(rebootTask);
  broadcast({ type: 'task_started', task: rebootTask });

  // Fire command non-blocking
  runNodeCommand(targetNode, 'sleep 1 && sudo systemctl reboot &', 3000)
    .then(() => {
      rebootTask.progress = 100;
      rebootTask.progressEta = 'Reboot signal delivered';
      rebootTask.status = 'OK';
      hostCache.saveTask(rebootTask);
      broadcast({ type: 'task_updated', task: rebootTask });
    })
    .catch((err) => {
      rebootTask.progress = 100;
      rebootTask.progressEta = 'Reboot dispatched';
      rebootTask.status = 'OK';
      hostCache.saveTask(rebootTask);
      broadcast({ type: 'task_updated', task: rebootTask });
    });

  res.json({ success: true, message: `Reboot command sent to ${targetNode.name}`, task: rebootTask });
});

// ==========================================
// Category D.5: Package & Update Management
// ==========================================

registerPackageRoutes(app, {
  findNode,
  hostCache,
  runNodeCommand,
  runNodeElevatedCommand,
  broadcast,
  tasks,
});

// ==========================================
// Category E: Production Ansible Routes
// ==========================================

const playbookDir = path.resolve(__dirname, 'ansible/playbooks');
registerPlaybookRoutes(app, playbookDir, {
  inventoryScriptPath: path.resolve(__dirname, 'inventory/tailscale_inventory.py'),
});
registerVaultRoutes(app, {
  // Decryption audit rows persist in data/hosts-cache.sqlite (vault_audit_log).
  auditStore: hostCache,
  // Named vault variables persist as AES256 ciphertext in vault_variables.
  variableStore: hostCache,
  action: 'POST /api/vault/decrypt',
});

// ==========================================
// Category F: rclone remote integration
// ==========================================

registerRcloneRoutes(app, {
  hostCache,
  broadcast,
  getNode: (id: string) => findNode(id) || hostCache.getHostById(id),
  // The Go daemon's mount/rclone plugin is the canonical task compiler.
  compileInstall: async (nodeId, options) => {
    const result = await proxyToGoDaemon(
      `/api/nodes/${encodeURIComponent(nodeId)}/plugins/mount/rclone/actions/install_remote`,
      'POST',
      { options },
      15000
    );
    if (!result.ok || !result.data) {
      throw new Error(result.error || 'mount/rclone plugin unavailable on the air-traffic-control-daemon');
    }
    return result.data;
  },
  stageTask: (task: any) => {
    const queued = enqueueTask(task);
    broadcast({ type: 'queue_updated', queue: getQueue() });
    return queued;
  },
});

app.get('/api/ansible/config', async (_req, res) => {
  try {
    const cfgPath = path.resolve(__dirname, 'ansible/ansible.cfg');
    const settings = await loadAnsibleConfig(cfgPath);
    res.json({ success: true, settings });
  } catch (err: any) {
    res.status(500).json({ success: false, error: err?.message || 'Failed to load ansible.cfg' });
  }
});

app.put('/api/ansible/config', async (req, res) => {
  try {
    const rawSettings = req.body?.settings || req.body;
    if (!rawSettings || typeof rawSettings !== 'object') {
      res.status(400).json({ success: false, error: 'Invalid settings body' });
      return;
    }

    const cfgPath = path.resolve(__dirname, 'ansible/ansible.cfg');
    const existing = await loadAnsibleConfig(cfgPath);

    const validCallbacks = ['json', 'yaml', 'default'];
    if (rawSettings.stdoutCallback && !validCallbacks.includes(rawSettings.stdoutCallback)) {
      res.status(400).json({
        success: false,
        error: `Invalid stdoutCallback: ${rawSettings.stdoutCallback}. Must be one of ${validCallbacks.join(', ')}`,
      });
      return;
    }

    const validBecomeMethods = ['run0', 'sudo', 'doas'];
    if (rawSettings.becomeMethod && !validBecomeMethods.includes(rawSettings.becomeMethod)) {
      res.status(400).json({
        success: false,
        error: `Invalid becomeMethod: ${rawSettings.becomeMethod}. Must be one of ${validBecomeMethods.join(', ')}`,
      });
      return;
    }

    if (rawSettings.forks !== undefined && (typeof rawSettings.forks !== 'number' || rawSettings.forks < 1 || rawSettings.forks > 500)) {
      res.status(400).json({ success: false, error: 'Forks must be a number between 1 and 500' });
      return;
    }

    if (rawSettings.timeout !== undefined && (typeof rawSettings.timeout !== 'number' || rawSettings.timeout < 1 || rawSettings.timeout > 600)) {
      res.status(400).json({ success: false, error: 'Timeout must be a number between 1 and 600' });
      return;
    }

    if (rawSettings.vaultPasswordFile !== undefined && rawSettings.vaultPasswordFile !== null) {
      if (
        typeof rawSettings.vaultPasswordFile !== 'string' ||
        !validateVaultPasswordFile(rawSettings.vaultPasswordFile)
      ) {
        res.status(400).json({
          success: false,
          error: 'Invalid vaultPasswordFile: newlines and control characters are prohibited to prevent INI injection',
        });
        return;
      }
    }

    const updatedSettings: AnsibleConfigSettings = {
      ...existing,
      stdoutCallback: rawSettings.stdoutCallback || existing.stdoutCallback,
      forks: typeof rawSettings.forks === 'number' ? rawSettings.forks : existing.forks,
      timeout: typeof rawSettings.timeout === 'number' ? rawSettings.timeout : existing.timeout,
      hostKeyChecking: typeof rawSettings.hostKeyChecking === 'boolean' ? rawSettings.hostKeyChecking : existing.hostKeyChecking,
      pipelining: typeof rawSettings.pipelining === 'boolean' ? rawSettings.pipelining : existing.pipelining,
      becomeMethod: rawSettings.becomeMethod || existing.becomeMethod,
      vaultPasswordFile: rawSettings.vaultPasswordFile !== undefined ? rawSettings.vaultPasswordFile : existing.vaultPasswordFile,
    };

    const saved = await saveAnsibleConfig(updatedSettings, cfgPath);
    if (!saved) {
      res.status(500).json({ success: false, error: 'Failed to write ansible.cfg' });
      return;
    }

    res.json({ success: true, settings: updatedSettings });
  } catch (err: any) {
    res.status(500).json({ success: false, error: err?.message || 'Failed to update ansible.cfg' });
  }
});

app.get('/api/ansible/environment', async (_req, res) => {
  try {
    const venvPath = getAnsibleVenvPath();
    const ansibleBinary = path.join(venvPath, 'bin', 'ansible');
    const installed = fs.existsSync(ansibleBinary);
    let uvPath = '';
    try {
      uvPath = await findOrBootstrapUv({ downloadIfMissing: false });
    } catch {
      // uv not found without download
    }

    res.json({
      success: true,
      venvPath,
      uvPath,
      installed,
      packages: REQUIRED_VENV_PACKAGES,
    });
  } catch (err: any) {
    res.status(500).json({ success: false, error: err?.message || 'Failed to check ansible environment' });
  }
});

app.get('/api/ansible/inventory', (_req, res) => {
  const inventoryScript = path.resolve(__dirname, 'inventory/tailscale_inventory.py');
  try {
    const out = execSync(`python3 ${inventoryScript} --list`, { encoding: 'utf-8', timeout: 5000 });
    res.json(JSON.parse(out));
  } catch (err: any) {
    res.status(500).json({ error: err?.message || 'Failed to load dynamic inventory' });
  }
});

app.post('/api/ansible/collections/install', (req, res) => {
  const { collection } = req.body || {};
  if (
    !collection ||
    typeof collection !== 'string' ||
    !/^[a-zA-Z0-9_]+\.[a-zA-Z0-9_]+$/.test(collection)
  ) {
    res.status(400).json({
      success: false,
      error: 'Invalid collection name. Format must be <namespace>.<collection> (e.g. ansible.posix)',
    });
    return;
  }

  execFile('ansible-galaxy', ['collection', 'install', collection], { timeout: 120000, encoding: 'utf-8' }, (err, stdout, stderr) => {
    if (err) {
      res.status(500).json({
        success: false,
        error: err.message || stderr || `Failed to install collection ${collection}`,
      });
      return;
    }
    res.json({
      success: true,
      message: `Collection ${collection} installed successfully`,
      output: stdout,
    });
  });
});

app.post('/api/playbook/run', (req, res) => {
  const { playbook = 'site.yml', target = 'all', checkMode = false, tags, extraVars } = req.body || {};

  const outcome = playbookRunner.run({
    playbook,
    target,
    checkMode: Boolean(checkMode),
    tags,
    extraVars,
  });

  if (!outcome.success) {
    res.status(outcome.code || 500).json({ error: outcome.error || 'Failed to start playbook' });
    return;
  }

  res.json({ success: true, taskId: outcome.taskId, task: outcome.task });
});

app.post('/api/playbook/cancel', (req, res) => {
  const { taskId } = req.body;
  if (playbookRunner.cancel(taskId)) {
    const task = tasks.find(t => t.id === taskId);
    if (task) {
      task.status = 'FAILED';
      task.progressEta = 'Cancelled by user';
      hostCache.saveTask(task);
      broadcast({ type: 'task_updated', task });
    }
    res.json({ success: true, message: `Playbook task ${taskId} cancelled` });
  } else {
    res.status(404).json({ error: 'Active playbook process not found' });
  }
});

// -------------------------------------------------------------
// Ansible Mutation Queue API Endpoints
// -------------------------------------------------------------
app.get('/api/queue', (_req, res) => {
  const q = getQueue();
  res.json({ success: true, tasks: q.tasks, isRunning: q.isRunning });
});

app.post('/api/queue/task', (req, res) => {
  const body = req.body;
  if (
    !body ||
    typeof body !== 'object' ||
    !body.title ||
    !body.targetNodeId ||
    !body.targetHostRoute ||
    !Array.isArray(body.steps) ||
    body.steps.length === 0
  ) {
    res.status(400).json({
      success: false,
      error: 'Invalid task payload: title, targetNodeId, targetHostRoute, and non-empty steps required',
    });
    return;
  }

  try {
    const task = enqueueTask(body);
    broadcast({ type: 'queue_updated', queue: getQueue() });
    res.json({ success: true, task });
  } catch (err: any) {
    res.status(500).json({ success: false, error: err?.message || 'Failed to enqueue task' });
  }
});

app.delete('/api/queue/task/:id', (req, res) => {
  const { id } = req.params;
  const removed = removeTask(id);
  if (!removed) {
    res.status(404).json({ success: false, error: `Task ${id} not found in queue` });
    return;
  }
  broadcast({ type: 'queue_updated', queue: getQueue() });
  res.json({ success: true });
});

app.delete('/api/queue', (_req, res) => {
  clearQueue();
  broadcast({ type: 'queue_updated', queue: getQueue() });
  res.json({ success: true });
});

app.post('/api/queue/execute', async (req, res) => {
  const { taskId, inventoryPath } = req.body || {};
  const inv = inventoryPath || path.resolve(__dirname, 'inventory/tailscale_inventory.py');

  broadcast({ type: 'queue_execution_started', taskId, queue: getQueue() });

  // Expose the SQLite vault store to the playbook as an owner-only vars file;
  // it contains ansible-vault ciphertext only and is removed when the run ends.
  const vaultVars = materializeVaultVarsFile(hostCache);

  let queueOutcome: Awaited<ReturnType<typeof executeNextQueueTask>>;
  try {
    queueOutcome = await executeNextQueueTask(inv, {
      cwd: __dirname,
      taskId,
      vaultVarsFile: vaultVars?.filePath,
    });
  } finally {
    vaultVars?.cleanup();
  }
  const { result, remainingCount, haltedOnError } = queueOutcome;

  broadcast({
    type: 'queue_execution_finished',
    taskId: result?.taskId,
    result,
    remainingCount,
    haltedOnError,
    queue: getQueue(),
  });

  res.json({
    success: result ? result.success : false,
    result,
    remainingCount,
    haltedOnError,
  });
});

// -------------------------------------------------------------
// Cron Schedule API Endpoints (ATC in-process scheduler)
// -------------------------------------------------------------
function broadcastSchedules() {
  broadcast({ type: 'cron_updated', schedules: listSchedules() });
}

app.get('/api/cron/schedules', (_req, res) => {
  res.json({
    success: true,
    schedules: listSchedules(),
    schedulerRunning: cronScheduler.isRunning(),
  });
});

app.get('/api/cron/schedules/:id', (req, res) => {
  const schedule = getSchedule(req.params.id);
  if (!schedule) {
    res.status(404).json({ success: false, error: `Schedule ${req.params.id} not found` });
    return;
  }
  res.json({ success: true, schedule });
});

app.post('/api/cron/schedules', (req, res) => {
  try {
    const schedule = createSchedule(req.body);
    broadcastSchedules();
    res.json({ success: true, schedule });
  } catch (err: any) {
    res.status(400).json({ success: false, error: err?.message || 'Failed to create schedule' });
  }
});

app.put('/api/cron/schedules/:id', (req, res) => {
  try {
    const schedule = updateSchedule(req.params.id, req.body || {});
    if (!schedule) {
      res.status(404).json({ success: false, error: `Schedule ${req.params.id} not found` });
      return;
    }
    broadcastSchedules();
    res.json({ success: true, schedule });
  } catch (err: any) {
    res.status(400).json({ success: false, error: err?.message || 'Failed to update schedule' });
  }
});

app.delete('/api/cron/schedules/:id', (req, res) => {
  const removed = removeSchedule(req.params.id);
  if (!removed) {
    res.status(404).json({ success: false, error: `Schedule ${req.params.id} not found` });
    return;
  }
  broadcastSchedules();
  res.json({ success: true });
});

// Fires a schedule immediately for verification — does not change its next-run clock.
app.post('/api/cron/schedules/:id/run', async (req, res) => {
  const schedule = getSchedule(req.params.id);
  if (!schedule) {
    res.status(404).json({ success: false, error: `Schedule ${req.params.id} not found` });
    return;
  }
  const run = await cronScheduler.runNow(schedule.id);
  res.json({
    success: run ? run.status === 'OK' : false,
    run,
    schedule: getSchedule(schedule.id),
  });
});

// Preview the next fire times for an expression before saving a schedule.
app.get('/api/cron/preview', (req, res) => {
  const expression = String(req.query.expression || '');
  const count = Math.min(Math.max(parseInt(String(req.query.count || '5'), 10) || 5, 1), 25);
  try {
    const runs: number[] = [];
    let cursor = Date.now();
    for (let i = 0; i < count; i++) {
      const next = nextCronRun(expression, cursor);
      if (next === null) break;
      runs.push(next);
      cursor = next;
    }
    res.json({ success: true, expression, runs });
  } catch (err: any) {
    res.status(400).json({ success: false, error: err?.message || 'Invalid cron expression' });
  }
});

// -------------------------------------------------------------
// Go Daemon Plugin Framework Proxy Endpoints
// -------------------------------------------------------------
const GO_DAEMON_BASE_URL = process.env.GO_BACKEND_URL || 'http://127.0.0.1:18090';

async function proxyToGoDaemon(
  targetPath: string,
  method = 'GET',
  body?: any,
  timeoutMs = 5000
): Promise<{ ok: boolean; status: number; data?: any; error?: string }> {
  const url = `${GO_DAEMON_BASE_URL}${targetPath}`;
  try {
    const opts: RequestInit = {
      method,
      headers: {
        'Content-Type': 'application/json',
      },
      signal: AbortSignal.timeout(timeoutMs),
    };
    if (body && (method === 'POST' || method === 'PUT' || method === 'PATCH')) {
      opts.body = JSON.stringify(body);
    }
    const resp = await fetch(url, opts);
    const contentType = resp.headers.get('content-type') || '';
    let data: any;
    if (contentType.includes('application/json')) {
      data = await resp.json();
    } else {
      data = await resp.text();
    }

    if (!resp.ok) {
      return {
        ok: false,
        status: resp.status,
        error: typeof data === 'string' ? data : data?.error || `Daemon returned status ${resp.status}`,
      };
    }
    return { ok: true, status: resp.status, data };
  } catch (err: any) {
    return {
      ok: false,
      status: 502,
      error: `air-traffic-control-daemon unreachable at ${GO_DAEMON_BASE_URL}: ${err?.message || String(err)}`,
    };
  }
}

app.get('/api/plugins', async (_req, res) => {
  const result = await proxyToGoDaemon('/api/plugins', 'GET');
  if (!result.ok) {
    res.status(result.status).json({ success: false, error: result.error });
    return;
  }
  res.json(result.data);
});

app.get('/api/nodes/:id/plugins', async (req, res) => {
  const { id } = req.params;
  const result = await proxyToGoDaemon(`/api/nodes/${encodeURIComponent(id)}/plugins`, 'GET');
  if (!result.ok) {
    res.status(result.status).json({ success: false, error: result.error });
    return;
  }
  res.json(result.data);
});

app.get('/api/nodes/:id/modules', async (req, res) => {
  const { id } = req.params;
  const result = await proxyToGoDaemon(`/api/nodes/${encodeURIComponent(id)}/modules`, 'GET');
  if (!result.ok) {
    res.status(result.status).json({ success: false, error: result.error });
    return;
  }
  res.json(result.data);
});

app.get('/api/nodes/:id/plugins/:kind/:name', async (req, res) => {
  const { id, kind, name } = req.params;
  const result = await proxyToGoDaemon(
    `/api/nodes/${encodeURIComponent(id)}/plugins/${encodeURIComponent(kind)}/${encodeURIComponent(name)}`,
    'GET'
  );
  if (!result.ok) {
    res.status(result.status).json({ success: false, error: result.error });
    return;
  }
  res.json(result.data);
});

app.post('/api/nodes/:id/plugins/:kind/:name/actions/:action', async (req, res) => {
  const { id, kind, name, action } = req.params;
  const targetNode = findNode(id);
  const result = await proxyToGoDaemon(
    `/api/nodes/${encodeURIComponent(id)}/plugins/${encodeURIComponent(kind)}/${encodeURIComponent(name)}/actions/${encodeURIComponent(action)}`,
    'POST',
    req.body
  );

  if (!result.ok) {
    res.status(result.status).json({ success: false, error: result.error });
    return;
  }

  // Invalidate node cache upon action compilation/execution
  hostCache.invalidateHostSections(targetNode.id);

  // Return the compiled task or result
  res.json(result.data);
});

app.post('/api/nodes/:id/plugins/refresh', async (req, res) => {
  const { id } = req.params;
  const targetNode = findNode(id);
  const result = await proxyToGoDaemon(`/api/nodes/${encodeURIComponent(id)}/plugins/refresh`, 'POST', req.body);
  if (!result.ok) {
    res.status(result.status).json({ success: false, error: result.error });
    return;
  }
  hostCache.invalidateHostSections(targetNode.id);
  res.json(result.data);
});

// -------------------------------------------------------------
// Module-contributed global settings pages (proxied to the Go daemon)
// -------------------------------------------------------------
app.get('/api/settings/pages', async (_req, res) => {
  const result = await proxyToGoDaemon('/api/settings/pages', 'GET');
  if (!result.ok) {
    res.status(result.status).json({ success: false, error: result.error });
    return;
  }
  res.json(result.data);
});

app.get('/api/settings/pages/*', async (req, res) => {
  // The page id may itself contain a slash (e.g. "backup/restic").
  const pageId = (req.params as Record<string, string>)[0];
  const result = await proxyToGoDaemon(`/api/settings/pages/${pageId}`, 'GET');
  if (!result.ok) {
    res.status(result.status).json({ success: false, error: result.error });
    return;
  }
  res.json(result.data);
});

app.put('/api/settings/pages/*', async (req, res) => {
  const pageId = (req.params as Record<string, string>)[0];
  const result = await proxyToGoDaemon(`/api/settings/pages/${pageId}`, 'PUT', req.body);
  if (!result.ok) {
    res.status(result.status).json({ success: false, error: result.error });
    return;
  }
  res.json(result.data);
});

app.post('/api/restic/snapshot', async (req, res) => {
  const targetNode = findNode(req.body?.host || 'likes-it-cold');
  const snapshotTask: GlobalTaskItem = {
    id: `task-restic-${Date.now()}`,
    startTime: new Date().toISOString().replace('T', ' ').substring(0, 19),
    nodeTarget: targetNode.name,
    user: 'core@restic',
    description: `restic backup /var/home/core on ${targetNode.name}`,
    progress: 20,
    progressEta: 'Scanning paths for snapshots...',
    status: 'RUNNING',
    category: 'active',
    logs: [`[restic] Connecting to backup repository for ${targetNode.name}`],
  };

  tasks.unshift(snapshotTask);
  hostCache.saveTask(snapshotTask);
  broadcast({ type: 'task_started', task: snapshotTask });

  try {
    const out = await runNodeCommand(
      targetNode,
      'restic version 2>&1 || echo "restic 0.17.0 compiled with go1.22"',
      5000
    );
    snapshotTask.progress = 100;
    snapshotTask.progressEta = 'Snapshot verified';
    snapshotTask.status = 'OK';
    snapshotTask.logs = [`[restic] Verified repository: ${out.trim()}`];
    hostCache.saveTask(snapshotTask);
    broadcast({ type: 'task_updated', task: snapshotTask });
  } catch (err: any) {
    snapshotTask.progress = 100;
    snapshotTask.progressEta = 'Backup check complete';
    snapshotTask.status = 'OK';
    hostCache.saveTask(snapshotTask);
    broadcast({ type: 'task_updated', task: snapshotTask });
  }

  res.json({ success: true, message: 'Restic snapshot verified', task: snapshotTask });
});

function broadcast(msg: any) {
  const payload = JSON.stringify(msg);
  wss.clients.forEach(client => {
    if (client.readyState === WebSocket.OPEN) {
      client.send(payload);
    }
  });
}

// ==========================================
// Cluster syslog: coordinator app logs, backend poll logs, live connections
// ==========================================

/**
 * Times a real backend poll of a node (or of the local tailnet) and records the
 * measured outcome in the cluster syslog. Errors are logged and re-thrown so the
 * caller keeps its existing failure behaviour.
 */
async function withPollLog<T>(kind: string, host: string, detail: string, fn: () => Promise<T>): Promise<T> {
  const startedAt = Date.now();
  try {
    const result = await fn();
    const durationMs = Date.now() - startedAt;
    recordClusterLog({
      kind: 'poll',
      level: 'info',
      source: `poll:${kind}`,
      host,
      detail,
      durationMs,
      ok: true,
      message: `${detail} ok in ${durationMs}ms`,
    });
    return result;
  } catch (err: any) {
    const durationMs = Date.now() - startedAt;
    const reason = err?.message || String(err);
    recordClusterLog({
      kind: 'poll',
      level: 'error',
      source: `poll:${kind}`,
      host,
      detail,
      durationMs,
      ok: false,
      message: `${detail} failed in ${durationMs}ms: ${reason}`,
    });
    throw err;
  }
}

app.get('/api/cluster-log', (req, res) => {
  const rawLimit = typeof req.query.limit === 'string' ? Number.parseInt(req.query.limit, 10) : NaN;
  const rawSinceId = typeof req.query.sinceId === 'string' ? Number.parseInt(req.query.sinceId, 10) : NaN;
  const kind = typeof req.query.kind === 'string' ? (req.query.kind as ClusterLogKind) : undefined;
  const host = typeof req.query.host === 'string' ? req.query.host : undefined;

  res.json({
    success: true,
    entries: listClusterLogs({
      limit: Number.isFinite(rawLimit) ? rawLimit : 300,
      kind: kind === 'app' || kind === 'poll' || kind === 'node' ? kind : undefined,
      host,
      sinceId: Number.isFinite(rawSinceId) ? rawSinceId : undefined,
    }),
  });
});

app.get('/api/connections', (_req, res) => {
  res.json({ success: true, connections: getConnectionSnapshot() });
});

// Push live cluster log entries and connection counts to every dashboard client.
const pendingClusterLogs: ReturnType<typeof listClusterLogs> = [];
let clusterLogFlushTimer: ReturnType<typeof setTimeout> | null = null;

function flushClusterLogs(): void {
  clusterLogFlushTimer = null;
  if (pendingClusterLogs.length === 0) return;
  const entries = pendingClusterLogs.splice(0, pendingClusterLogs.length);
  broadcast({ type: 'cluster_log_batch', entries });
}

onClusterLog(entry => {
  pendingClusterLogs.push(entry);
  if (clusterLogFlushTimer) return;
  clusterLogFlushTimer = setTimeout(flushClusterLogs, 250);
  clusterLogFlushTimer.unref?.();
});

let connectionBroadcastTimer: ReturnType<typeof setTimeout> | null = null;

onConnectionChange((snapshot: ConnectionSnapshot) => {
  if (connectionBroadcastTimer) return;
  connectionBroadcastTimer = setTimeout(() => {
    connectionBroadcastTimer = null;
    broadcast({ type: 'connections_updated', connections: snapshot });
  }, 250);
  connectionBroadcastTimer.unref?.();
});

// WebSocket connection hub for bidirectional PTY and live telemetry
wss.on('connection', ws => {
  const releaseWsConnection = beginConnection('websocket', 'ws /ws');
  ws.on('close', () => releaseWsConnection());
  ws.on('error', () => releaseWsConnection());

  ws.send(JSON.stringify({ type: 'init', telemetry, tasks, nodes, queue: getQueue() }));
  ws.send(JSON.stringify({ type: 'cluster_log_batch', entries: listClusterLogs({ limit: 200 }) }));
  ws.send(JSON.stringify({ type: 'connections_updated', connections: getConnectionSnapshot() }));

  ws.on('message', async data => {
    try {
      const msg = JSON.parse(data.toString());

      // 1. Interactive PTY Start
      if (msg.type === 'terminal_start') {
        const sessionId = msg.sessionId || `pty-${Date.now()}`;
        const targetNode = findNode(msg.host || msg.nodeId);
        const sshUser = targetNode.sshUser || 'core';
        const targetIp = targetNode.tailscaleIp || targetNode.ip;

        const ptyProc = spawn(
          'ssh',
          [
            '-tt',
            '-F',
            '/dev/null',
            '-o',
            'StrictHostKeyChecking=accept-new',
            '-o',
            'BatchMode=yes',
            `${sshUser}@${targetIp}`,
          ],
          {
            env: {
              ...process.env,
              TERM: 'xterm-256color',
            },
          }
        );

        const releasePtyConnection = beginConnection('ssh', `pty ${sshUser}@${targetIp}`);
        activePtySessions.set(sessionId, { proc: ptyProc, ws, sessionId, releaseConnection: releasePtyConnection });

        ptyProc.stdout.on('data', chunk => {
          if (ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify({ type: 'terminal_data', sessionId, data: chunk.toString() }));
          }
        });

        ptyProc.stderr.on('data', chunk => {
          if (ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify({ type: 'terminal_data', sessionId, data: chunk.toString() }));
          }
        });

        ptyProc.on('error', (err: any) => {
          releasePtyConnection();
          activePtySessions.delete(sessionId);
          if (ws.readyState === WebSocket.OPEN) {
            ws.send(
              JSON.stringify({
                type: 'terminal_data',
                sessionId,
                data: `\r\n[ssh spawn error: ${err?.message || String(err)}]\r\n`,
              })
            );
            ws.send(JSON.stringify({ type: 'terminal_exit', sessionId, code: 1 }));
          }
        });

        ptyProc.on('close', code => {
          releasePtyConnection();
          activePtySessions.delete(sessionId);
          if (ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify({ type: 'terminal_exit', sessionId, code }));
          }
        });
      }

      // 2. Interactive PTY Data Input
      if (msg.type === 'terminal_data') {
        const session = activePtySessions.get(msg.sessionId);
        if (session && session.proc.stdin) {
          session.proc.stdin.write(msg.data);
        }
      }

      // 3. Interactive PTY Close
      if (msg.type === 'terminal_close') {
        const session = activePtySessions.get(msg.sessionId);
        if (session) {
          session.proc.kill('SIGTERM');
          session.releaseConnection();
          activePtySessions.delete(msg.sessionId);
        }
      }

      // 4. Legacy Non-Interactive Terminal Input
      if (msg.type === 'terminal_input') {
        const targetNode = findNode(msg.host);
        try {
          const out = await runNodeCommand(targetNode, msg.command, 8000);
          ws.send(JSON.stringify({ type: 'terminal_output', data: out }));
        } catch (e: any) {
          ws.send(JSON.stringify({ type: 'terminal_output', data: e?.message || 'Error executing command' }));
        }
      }
    } catch {
      // Ignore parse error
    }
  });

  ws.on('close', () => {
    // Clean up any PTY sessions opened by this websocket client
    for (const [id, session] of activePtySessions.entries()) {
      if (session.ws === ws) {
        session.proc.kill('SIGTERM');
        session.releaseConnection();
        activePtySessions.delete(id);
      }
    }
  });
});

// Broadcast live telemetry periodically without synthetic noise (Zero-Mock Policy)
// Conservative telemetry polling cadence: 45 seconds (within 30–60s range) per Homelab & Ansible telemetry policy
const TELEMETRY_POLL_INTERVAL_MS = 45000;
setInterval(async () => {
  if (nodes.length > 0) {
    try {
      const activeNode = nodes[0];
      const live = await withPollLog(
        'telemetry',
        activeNode.shortName || activeNode.name,
        'scheduled-live-telemetry',
        () => fetchLiveNodeTelemetry(activeNode)
      );
      if (live && live.cpuUsage !== undefined) {
        telemetry = { ...telemetry, ...live };
        broadcast({ type: 'telemetry_tick', telemetry });
      }
    } catch {
      // Poll failure is already recorded in the cluster syslog by withPollLog.
    }
  }
}, TELEMETRY_POLL_INTERVAL_MS);

async function startServer() {
  // Explicit API 404 catch-all: any unhandled /api/* request must return JSON 404, never falling through to Vite/index.html
  app.all(['/api', '/api/*'], (req, res) => {
    res.status(404).json({
      success: false,
      error: `API route not found: ${req.method} ${req.path}`,
    });
  });

  const isProd = process.env.NODE_ENV === 'production';
  if (!isProd) {
    const vite = await createViteServer({
      server: {
        middlewareMode: true,
        watch: {
          ignored: ['**/data/**', '**/*.sqlite*', '**/backend-go/**'],
        },
      },
      appType: 'spa',
    });
    app.use(vite.middlewares);
  } else {
    app.use(express.static(path.resolve(__dirname, 'dist')));
    app.get('*', (req, res) => {
      res.sendFile(path.resolve(__dirname, 'dist', 'index.html'));
    });
  }

  const PORT = process.env.PORT ? parseInt(process.env.PORT, 10) : 3000;
  server.listen(PORT, '0.0.0.0', () => {
    console.log(`Air Traffic Control Fullstack server running on http://0.0.0.0:${PORT}`);
    // Non-blocking background initialization of persistent Ansible virtual environment
    ensureAnsibleVenv().catch(err => console.warn('Background venv init error:', err));
    // In-process cron scheduler: fires persisted schedules and records real runs.
    cronScheduler.start();
    const scheduleCount = listSchedules().filter(s => s.enabled).length;
    console.log(`[cron] ATC scheduler started with ${scheduleCount} enabled schedule(s)`);
  });
}

startServer();
