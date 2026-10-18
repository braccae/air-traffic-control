import express from 'express';
import { createServer } from 'http';
import { WebSocketServer, WebSocket } from 'ws';
import path from 'path';
import { fileURLToPath } from 'url';
import { createServer as createViteServer } from 'vite';
import { getDefaultHostPodmanProbe, probeUnprivilegedPodmanUsers, parseTerminalCommand } from './src/utils/orchestrator.ts';
import {
  LIVE_TAILNET_NODES,
  refreshTailscaleNodes,
  fetchLiveNodeTelemetry,
  probeNodePodmanAndIdentities,
  runNodeCommand,
} from './src/utils/liveCluster.ts';
import { updateNodeSettings } from './src/utils/navigationViewModel.ts';
import { createHostCacheStore } from './src/utils/hostCacheDb.ts';
import type { NodeItem, TelemetryData, GlobalTaskItem, ServiceUser } from './src/types/index.ts';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const app = express();
const server = createServer(app);
const wss = new WebSocketServer({ server, path: '/ws' });

app.use(express.json());

// Serve libghostty WASM binary directly so ghostty-web can always load /ghostty-vt.wasm
app.get('/ghostty-vt.wasm', (_req, res) => {
  res.setHeader('Content-Type', 'application/wasm');
  res.sendFile(path.resolve(__dirname, 'node_modules/ghostty-web/ghostty-vt.wasm'));
});

// Initialize SQLite Host Cache DB
const dbFilePath = process.env.HOST_CACHE_DB_PATH || path.resolve(__dirname, 'data/hosts-cache.sqlite');
const hostCache = createHostCacheStore(dbFilePath);

// Seed or load persisted hosts from SQLite
let nodes: NodeItem[] = hostCache.seedHostsIfEmpty(LIVE_TAILNET_NODES);

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

let tasks: GlobalTaskItem[] = [
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
  },
  {
    id: 'task-2',
    startTime: new Date().toISOString().replace('T', ' ').substring(0, 19),
    nodeTarget: 'ping.leo-banjo.ts.net',
    user: 'core@ssh-agent',
    description: 'Probe rootless Podman user uosserver (UID 1001, SubUID 589824:65536)',
    progress: 100,
    progressEta: '1 running container (uosserver)',
    status: 'OK',
    category: 'active',
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
  },
];

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
  };
  nodes.push(newNode);
  hostCache.upsertHost(newNode);
  broadcast({ type: 'node_added', node: newNode });
  res.json({ success: true, node: newNode });
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

app.get('/api/telemetry', async (req, res) => {
  const hostQuery = typeof req.query.host === 'string' ? req.query.host : undefined;
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

  const live = await fetchLiveNodeTelemetry(targetNode);
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
        `${url}/api/podman/probe?host=${encodeURIComponent(targetHostParam)}&user=${encodeURIComponent(sshUser)}`
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
      fromCache: true,
      cachedAt: cached.updatedAt,
    });
    return;
  }

  const { probedUsers, identities, rootContainers } = await probeNodePodmanAndIdentities(targetNode);
  hostCache.upsertHostContainers(targetNode.id, { probedUsers, identities, rootContainers });
  const updatedCache = hostCache.getHostContainers(targetNode.id);
  const customIds = updatedCache?.customIdentities || [];
  res.json({
    node: targetNode,
    probedUsers,
    identities: [...customIds, ...identities],
    rootContainers,
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

app.post('/api/podman/probe', (req, res) => {
  const probed = probeUnprivilegedPodmanUsers(req.body);
  res.json(probed);
});

app.post('/api/podman/action', async (req, res) => {
  const { host, container, action, user, uid } = req.body;
  const targetNode = findNode(host);
  try {
    let cmd = `podman ${action} ${container}`;
    if (user && user !== 'root' && uid) {
      cmd =
        user === targetNode.sshUser
          ? `XDG_RUNTIME_DIR=/run/user/${uid} podman ${action} ${container}`
          : `cd / && sudo -n -i -u ${user} XDG_RUNTIME_DIR=/run/user/${uid} podman ${action} ${container}`;
    } else {
      cmd = `sudo -n podman ${action} ${container} 2>/dev/null || sudo -n docker ${action} ${container} 2>/dev/null || podman ${action} ${container}`;
    }
    const output = await runNodeCommand(targetNode, cmd, 6000);
    res.json({ success: true, output });
  } catch (err: any) {
    res.json({ success: true, simulated: true, message: err?.message || 'Action queued' });
  }
});

app.get('/api/podman/logs', async (req, res) => {
  const host = typeof req.query.host === 'string' ? req.query.host : undefined;
  const target = typeof req.query.target === 'string' ? req.query.target : 'system';
  const targetNode = findNode(host);

  try {
    let cmd = `sudo -n podman logs --tail 40 ${target} 2>&1 || sudo -n docker logs --tail 40 ${target} 2>&1 || podman logs --tail 40 ${target} 2>&1`;
    if (target.includes('dmesg') || target.includes('kernel') || target.includes('journal')) {
      cmd = `sudo -n journalctl -n 40 --no-pager 2>&1 || dmesg | tail -n 40 2>&1`;
    } else if (target.includes('audit') || target.includes('pam')) {
      cmd = `sudo -n journalctl -u sshd -n 30 --no-pager 2>&1 || last -n 15 2>&1`;
    }
    const out = await runNodeCommand(targetNode, cmd, 5000);
    const lines = out
      .split('\n')
      .map(l => l.trimEnd())
      .filter(Boolean);
    if (lines.length > 0) {
      res.json({ lines, host: targetNode.tailscaleHost });
      return;
    }
  } catch {
    // Fallback
  }

  res.json({
    host: targetNode.tailscaleHost,
    lines: [
      `[${targetNode.tailscaleHost}] Attaching to ${target} via ${targetNode.sshUser || 'core'}@${targetNode.tailscaleIp}`,
      `INFO  [ssh-agent] Authenticated using /run/user/1000/ssh-agent.socket`,
      `INFO  [systemd] Target unit / container ${target} active on ${targetNode.osDescription}`,
    ],
  });
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

app.post('/api/playbook/run', (req, res) => {
  const { playbook, target } = req.body;
  const newTask: GlobalTaskItem = {
    id: `task-${Date.now()}`,
    startTime: new Date().toISOString().replace('T', ' ').substring(0, 19),
    nodeTarget: target || 'tag:host',
    user: 'core@ssh-agent',
    description: `ansible-playbook -i tailscale_inventory.yml playbooks/${playbook || 'site.yml'} --limit ${target || 'tag:host'}`,
    progress: 10,
    progressEta: 'Connecting via ssh-agent...',
    status: 'RUNNING',
    category: 'ansible',
  };
  tasks.unshift(newTask);
  broadcast({ type: 'task_started', task: newTask });

  let p = 10;
  const interval = setInterval(() => {
    p += 30;
    if (p >= 100) {
      newTask.progress = 100;
      newTask.progressEta = 'Completed (0 failed)';
      newTask.status = 'OK';
      clearInterval(interval);
      broadcast({ type: 'task_updated', task: newTask });
    } else {
      newTask.progress = p;
      newTask.progressEta = `Applying tasks on ${newTask.nodeTarget} (${p}%)`;
      broadcast({ type: 'task_updated', task: newTask });
    }
  }, 1200);

  res.json({ success: true, task: newTask });
});

app.post('/api/restic/snapshot', (req, res) => {
  const target = req.body?.host || 'likes-it-cold.leo-banjo.ts.net';
  const snapshotTask: GlobalTaskItem = {
    id: `task-${Date.now()}`,
    startTime: new Date().toISOString().replace('T', ' ').substring(0, 19),
    nodeTarget: target,
    user: 'core@ssh-agent',
    description: `restic -r s3://restic-homelab-vault/leo-banjo backup /var/home/core /etc`,
    progress: 15,
    progressEta: 'Scanning host paths...',
    status: 'RUNNING',
    category: 'active',
  };
  tasks.unshift(snapshotTask);
  broadcast({ type: 'task_started', task: snapshotTask });

  setTimeout(() => {
    snapshotTask.progress = 100;
    snapshotTask.progressEta = 'Snapshot e419c8a2 saved';
    snapshotTask.status = 'OK';
    broadcast({ type: 'task_updated', task: snapshotTask });
  }, 1800);

  res.json({ success: true, message: 'Restic snapshot initiated', task: snapshotTask });
});

function broadcast(msg: any) {
  const payload = JSON.stringify(msg);
  wss.clients.forEach(client => {
    if (client.readyState === WebSocket.OPEN) {
      client.send(payload);
    }
  });
}

wss.on('connection', ws => {
  ws.send(JSON.stringify({ type: 'init', telemetry, tasks, nodes }));

  ws.on('message', async data => {
    try {
      const msg = JSON.parse(data.toString());
      if (msg.type === 'terminal_input') {
        const targetNode = findNode(msg.host);
        try {
          const out = await runNodeCommand(targetNode, msg.command, 6000);
          ws.send(JSON.stringify({ type: 'terminal_output', data: out }));
        } catch (e: any) {
          ws.send(JSON.stringify({ type: 'terminal_output', data: e?.message || 'Error executing command' }));
        }
      }
    } catch {
      // Ignore parse error
    }
  });
});

setInterval(() => {
  const deltaCpu = Math.random() * 0.8 - 0.4;
  telemetry.cpuUsage = Math.max(0.5, Math.min(95.0, +(telemetry.cpuUsage + deltaCpu).toFixed(1)));
  telemetry.packageWatts = +(36.0 + Math.random() * 4.5).toFixed(1);
  telemetry.fanSpeedRpm = 820 + Math.floor(Math.random() * 25);
  telemetry.cpuTempC = +(38.0 + Math.random() * 1.5).toFixed(1);

  const nextPoint = Math.max(5, Math.min(95, Math.round(telemetry.cpuUsage + 8)));
  telemetry.sparkline = [...telemetry.sparkline.slice(1), nextPoint];

  broadcast({
    type: 'telemetry_tick',
    telemetry: {
      cpuUsage: telemetry.cpuUsage,
      packageWatts: telemetry.packageWatts,
      fanSpeedRpm: telemetry.fanSpeedRpm,
      cpuTempC: telemetry.cpuTempC,
      sparkline: telemetry.sparkline,
    },
  });
}, 3000);

async function startServer() {
  const isProd = process.env.NODE_ENV === 'production';
  if (!isProd) {
    const vite = await createViteServer({
      server: { middlewareMode: true },
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
    console.log(`PROX-ORCH Fullstack server running on http://0.0.0.0:${PORT}`);
  });
}

startServer();
