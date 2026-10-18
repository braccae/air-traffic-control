import express from 'express';
import { createServer } from 'http';
import { WebSocketServer, WebSocket } from 'ws';
import path from 'path';
import { fileURLToPath } from 'url';
import { createServer as createViteServer } from 'vite';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const app = express();
const server = createServer(app);
const wss = new WebSocketServer({ server, path: '/ws' });

app.use(express.json());

// In-memory data store for live datacenter operations
let nodes = [
  {
    id: 'node-150-openmediavault',
    name: '150 (openmediavault)',
    shortName: '150',
    group: 'cold-war',
    datacenter: 'homelab-dc1',
    nodeType: 'linux',
    badge: 'SSH',
    badgeColor: 'text-outline',
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
    badgeColor: 'text-outline',
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
    badgeColor: 'text-outline',
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
    badgeColor: 'text-secondary',
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
    badgeColor: 'text-primary',
    ip: '192.168.1.154',
    port: 22,
    tailscaleIp: '100.97.159.105',
    tailscaleHost: 'k3s-ctrl.ts.net',
    osDescription: 'Alpine Linux 3.19 (K3s v1.29.3+k3s1)',
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
    badgeColor: 'text-tertiary',
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
  {
    id: 'node-202-win-render-srv',
    name: '202 (win-render-srv)',
    shortName: '202',
    group: 'cold-war',
    datacenter: 'homelab-dc1',
    nodeType: 'windows',
    badge: 'WIN',
    badgeColor: 'text-outline',
    ip: '192.168.1.202',
    port: 22,
    tailscaleIp: '100.97.159.112',
    tailscaleHost: 'win-render.ts.net',
    osDescription: 'Windows Server 2022 Datacenter (OpenSSH 9.5)',
    status: 'stopped',
    uptime: '0m',
    hasGlances: true,
    hasCockpit: false,
    hasPodman: false,
    hasRestic: true,
    specs: {
      cpu: 'Intel Xeon W-2295',
      vcpus: 18,
      ramGiB: 128,
      motherboard: 'ASUS WS C422 PRO/SE',
      bios: '3601',
      ipmi: '192.168.1.58',
      ecc: 'Active ECC Registered',
      secureBoot: 'Enabled',
      iommu: 'Enabled',
    },
  },
  {
    id: 'node-sw-core-01',
    name: 'sw-core-01 (10G Switch)',
    shortName: 'sw-core',
    group: 'noodle',
    datacenter: 'homelab-dc1',
    nodeType: 'router_switch',
    badge: 'SW',
    badgeColor: 'text-tertiary',
    ip: '192.168.1.2',
    port: 22,
    tailscaleIp: '100.97.159.201',
    tailscaleHost: 'sw-core-01.ts.net',
    osDescription: 'MikroTik RouterOS 7.14.3 / SwOS',
    status: 'online',
    uptime: '240d 12h',
    hasGlances: false,
    hasCockpit: false,
    hasPodman: false,
    hasRestic: false,
    specs: {
      cpu: 'ARM 32-bit Cortex-A9 800MHz',
      vcpus: 2,
      ramGiB: 1,
      motherboard: 'MikroTik CRS309-1G-8S+IN',
      bios: 'RouterBOOT 7.14',
      ipmi: 'N/A',
      ecc: 'Embedded',
      secureBoot: 'Enforced',
      iommu: 'N/A',
    },
  },
];

let tasks = [
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

// Telemetry state
let telemetry = {
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

// REST API routes
app.get('/api/nodes', (req, res) => {
  res.json(nodes);
});

app.post('/api/nodes', (req, res) => {
  const newNode = {
    id: `node-${Date.now()}`,
    name: req.body.name || 'New Node',
    shortName: req.body.shortName || 'node',
    group: req.body.group || 'cold-war',
    datacenter: req.body.datacenter || 'homelab-dc1',
    nodeType: req.body.nodeType || 'linux',
    ip: req.body.ip || '192.168.1.200',
    port: req.body.port || 22,
    tailscaleIp: req.body.tailscaleIp || '100.97.159.199',
    tailscaleHost: req.body.tailscaleHost || 'node.ts.net',
    osDescription: req.body.osDescription || 'Linux (SSH)',
    status: 'online',
    uptime: '1m',
    hasGlances: true,
    hasCockpit: req.body.nodeType === 'linux',
    hasPodman: req.body.nodeType === 'linux',
    hasRestic: true,
    specs: {
      cpu: req.body.cpu || 'Multi-core x86_64',
      vcpus: req.body.vcpus || 8,
      ramGiB: req.body.ramGiB || 16,
      motherboard: 'Standard Server Platform',
      bios: 'UEFI 2.8',
      ipmi: req.body.ipmi || 'DHCP',
      ecc: 'Supported',
      secureBoot: 'Enabled',
      iommu: 'Enabled',
    },
  };
  nodes.push(newNode as any);
  broadcast({ type: 'node_added', node: newNode });
  res.json({ success: true, node: newNode });
});

app.get('/api/telemetry', (req, res) => {
  res.json(telemetry);
});

app.get('/api/tasks', (req, res) => {
  res.json(tasks);
});

app.post('/api/playbook/run', (req, res) => {
  const { playbook, target } = req.body;
  const newTask = {
    id: `task-${Date.now()}`,
    startTime: new Date().toISOString().replace('T', ' ').substring(0, 19),
    nodeTarget: target || 'all (14 hosts)',
    user: 'root@pam',
    description: `ansible-playbook -i production playbooks/${playbook || 'site.yml'}`,
    progress: 5,
    progressEta: 'Starting execution...',
    status: 'RUNNING',
    category: 'ansible',
  };
  tasks.unshift(newTask as any);
  broadcast({ type: 'task_started', task: newTask });

  // Simulate progress progression over time
  let p = 5;
  const interval = setInterval(() => {
    p += 25;
    if (p >= 100) {
      newTask.progress = 100;
      newTask.progressEta = 'Completed in 00:00:18';
      newTask.status = 'OK';
      clearInterval(interval);
      broadcast({ type: 'task_updated', task: newTask });
    } else {
      newTask.progress = p;
      newTask.progressEta = `Executing role tasks (${p}%)`;
      broadcast({ type: 'task_updated', task: newTask });
    }
  }, 1500);

  res.json({ success: true, task: newTask });
});

app.post('/api/restic/snapshot', (req, res) => {
  const snapshotTask = {
    id: `task-${Date.now()}`,
    startTime: new Date().toISOString().replace('T', ' ').substring(0, 19),
    nodeTarget: '150 (the-freezer.homelab)',
    user: 'root@pam',
    description: 'restic -r s3://restic-homelab-vault/the-freezer backup /tank0/appdata /etc',
    progress: 10,
    progressEta: 'Scanning directories...',
    status: 'RUNNING',
    category: 'active',
  };
  tasks.unshift(snapshotTask as any);
  broadcast({ type: 'task_started', task: snapshotTask });

  setTimeout(() => {
    snapshotTask.progress = 60;
    snapshotTask.progressEta = 'Deduplicating chunks (AES-256)...';
    broadcast({ type: 'task_updated', task: snapshotTask });
  }, 1200);

  setTimeout(() => {
    snapshotTask.progress = 100;
    snapshotTask.progressEta = 'Snapshot a492f1b0 created';
    snapshotTask.status = 'OK';
    broadcast({ type: 'task_updated', task: snapshotTask });
  }, 2500);

  res.json({ success: true, message: 'Restic snapshot initiated' });
});

function broadcast(msg: any) {
  const payload = JSON.stringify(msg);
  wss.clients.forEach(client => {
    if (client.readyState === WebSocket.OPEN) {
      client.send(payload);
    }
  });
}

// WebSocket telemetry ticker and terminal handler
wss.on('connection', ws => {
  ws.send(JSON.stringify({ type: 'init', telemetry, tasks, nodes }));

  ws.on('message', data => {
    try {
      const msg = JSON.parse(data.toString());
      if (msg.type === 'terminal_input') {
        // Echo back or process command
        ws.send(JSON.stringify({
          type: 'terminal_output',
          data: `Executing via SSH/cockpit-bridge: ${msg.command}\r\n`,
        }));
      }
    } catch {
      // Ignore parse error
    }
  });
});

// Periodic simulated live telemetry tick (glances / pcp metrics)
setInterval(() => {
  const deltaCpu = (Math.random() * 0.8 - 0.4);
  telemetry.cpuUsage = Math.max(0.8, Math.min(6.5, +(telemetry.cpuUsage + deltaCpu).toFixed(1)));
  telemetry.packageWatts = +(42.0 + Math.random() * 4.5).toFixed(1);
  telemetry.fanSpeedRpm = 810 + Math.floor(Math.random() * 30);
  telemetry.cpuTempC = +(37.5 + Math.random() * 1.5).toFixed(1);
  
  // Update sparkline
  const nextPoint = Math.floor(10 + Math.random() * 12);
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
}, 2500);

// Set up Vite in development mode or serve static build in production
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
