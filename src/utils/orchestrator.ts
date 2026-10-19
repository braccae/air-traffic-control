import type { ContainerItem, NodeItem, TelemetryData, UnprivilegedPodmanUser } from '../types/index.ts';

export interface PodmanProbeInput {
  psOutput: string;
  passwdOutput?: string;
  subuidOutput?: string;
  lingerUsers?: string[] | string;
  runUserDirs?: string[] | string;
  userContainers?: Record<string, ContainerItem[]>;
}

const PODMAN_PROCESS_SIGNATURES = [
  'podman',
  'conmon',
  'catatonit',
  'pasta',
  'slirp4netns',
  'aardvark-dns',
  'rootlessport',
  'fuse-overlayfs',
  'crun',
];

function isPodmanProcessCmd(cmdline: string): boolean {
  const lower = cmdline.toLowerCase();
  return PODMAN_PROCESS_SIGNATURES.some(sig => lower.includes(sig));
}

function extractContainersFromConmonProcesses(
  processes: string[],
  user: string,
  uid: number
): ContainerItem[] {
  const containers: ContainerItem[] = [];
  const seen = new Set<string>();
  let hasCatatonit = false;

  for (const proc of processes) {
    const lower = proc.toLowerCase();
    if (lower.includes('catatonit')) {
      hasCatatonit = true;
    }
    if (!lower.includes('conmon')) continue;
    const tokens = proc.trim().split(/\s+/);
    let cid = '';
    let cname = '';
    for (let i = 0; i < tokens.length; i++) {
      const tok = tokens[i];
      if ((tok === '-c' || tok === '--cid') && i + 1 < tokens.length) {
        cid = tokens[i + 1];
        i++;
      } else if (tok.startsWith('--cid=')) {
        cid = tok.slice('--cid='.length);
      } else if (tok.startsWith('-c') && tok.length > 2 && !tok.startsWith('--')) {
        cid = tok.slice(2);
      } else if ((tok === '-n' || tok === '--name') && i + 1 < tokens.length) {
        cname = tokens[i + 1];
        i++;
      } else if (tok.startsWith('--name=')) {
        cname = tok.slice('--name='.length);
      } else if (tok.startsWith('-n') && tok.length > 2 && !tok.startsWith('--')) {
        cname = tok.slice(2);
      }
    }
    if (!cid && !cname) continue;
    if (!cid) cid = cname;
    if (!cname) cname = cid;
    const cidLower = cid.toLowerCase();
    const cnameLower = cname.toLowerCase();
    if (cidLower.includes('pause') || cnameLower.includes('pause') || cnameLower.endsWith('-infra')) {
      continue;
    }
    if (seen.has(cid)) continue;
    seen.add(cid);
    containers.push({
      id: cid,
      name: cname,
      image: 'rootless/container',
      status: 'Running',
      cpu: '0.0%',
      memory: '0 MiB',
      user,
      uid,
      rootless: uid !== 0,
    });
  }

  // Do not synthesize podman-pause containers for idle namespace processes
  return containers;
}

/**
 * Probes system process table, /etc/passwd, /etc/subuid, /var/lib/systemd/linger, and /run/user
 * to discover unprivileged (non-root) users running Podman containers.
 */
export function probeUnprivilegedPodmanUsers(input: PodmanProbeInput): UnprivilegedPodmanUser[] {
  const passwdByUser = new Map<string, { user: string; uid: number; gid: number; homeDir: string }>();
  const passwdByUid = new Map<number, { user: string; uid: number; gid: number; homeDir: string }>();

  for (const rawLine of (input.passwdOutput ?? '').split('\n')) {
    const line = rawLine.trim();
    if (!line || line.startsWith('#')) continue;
    const parts = line.split(':');
    if (parts.length < 6) continue;
    const user = parts[0];
    const uid = Number.parseInt(parts[2], 10);
    const gid = Number.parseInt(parts[3], 10);
    if (Number.isNaN(uid) || uid === 0 || user === 'root') continue;
    const entry = { user, uid, gid: Number.isNaN(gid) ? uid : gid, homeDir: parts[5] };
    passwdByUser.set(user, entry);
    passwdByUid.set(uid, entry);
  }

  const subuidByUser = new Map<string, string>();
  const candidates = new Map<string, UnprivilegedPodmanUser>();

  for (const rawLine of (input.subuidOutput ?? '').split('\n')) {
    const line = rawLine.trim();
    if (!line || line.startsWith('#')) continue;
    const parts = line.split(':');
    if (parts.length < 3) continue;
    const user = parts[0];
    if (user === 'root') continue;
    const range = `${parts[1]}:${parts[2]}`;
    subuidByUser.set(user, range);
    const pw = passwdByUser.get(user);
    if (pw) {
      candidates.set(user, {
        user: pw.user,
        uid: pw.uid,
        gid: pw.gid,
        homeDir: pw.homeDir,
        subUidRange: range,
        linger: false,
        activeProcesses: [],
        containers: [],
      });
    }
  }

  const lingerSet = new Set<string>();
  const lingerList = Array.isArray(input.lingerUsers)
    ? input.lingerUsers
    : (input.lingerUsers ?? '').split('\n');
  for (const rawUser of lingerList) {
    const user = rawUser.trim();
    if (!user || user === 'root') continue;
    lingerSet.add(user);
    const existing = candidates.get(user);
    if (existing) {
      existing.linger = true;
    } else {
      const pw = passwdByUser.get(user);
      if (pw) {
        candidates.set(user, {
          user: pw.user,
          uid: pw.uid,
          gid: pw.gid,
          homeDir: pw.homeDir,
          subUidRange: subuidByUser.get(user),
          linger: true,
          activeProcesses: [],
          containers: [],
        });
      }
    }
  }

  const runDirs = Array.isArray(input.runUserDirs)
    ? input.runUserDirs
    : (input.runUserDirs ?? '').split('\n');
  for (const rawDir of runDirs) {
    const dir = rawDir.trim();
    if (!dir.startsWith('/run/user/')) continue;
    const rest = dir.slice('/run/user/'.length);
    const uidStr = rest.split('/')[0];
    const uid = Number.parseInt(uidStr, 10);
    if (Number.isNaN(uid) || uid === 0) continue;
    const pw = passwdByUid.get(uid);
    if (pw && !candidates.has(pw.user)) {
      candidates.set(pw.user, {
        user: pw.user,
        uid: pw.uid,
        gid: pw.gid,
        homeDir: pw.homeDir,
        subUidRange: subuidByUser.get(pw.user),
        linger: lingerSet.has(pw.user),
        activeProcesses: [],
        containers: [],
      });
    }
  }

  for (const rawLine of input.psOutput.split('\n')) {
    const line = rawLine.trim();
    if (!line) continue;
    const fields = line.split(/\s+/);
    if (fields.length < 3) continue;
    const uid = Number.parseInt(fields[0], 10);
    if (Number.isNaN(uid) || uid === 0) continue;
    const pw = passwdByUid.get(uid);
    const user = pw ? pw.user : fields[1];
    if (user === 'root') continue;
    const args = fields.slice(2).join(' ');
    if (!isPodmanProcessCmd(args)) continue;

    let cand = candidates.get(user);
    if (!cand) {
      cand = {
        user,
        uid,
        gid: pw?.gid,
        homeDir: pw?.homeDir,
        subUidRange: subuidByUser.get(user),
        linger: lingerSet.has(user),
        activeProcesses: [],
        containers: [],
      };
      candidates.set(user, cand);
    }
    cand.activeProcesses.push(args);
  }

  const sortedUsers = Array.from(candidates.keys()).sort();
  const activeUsers: UnprivilegedPodmanUser[] = [];

  for (const username of sortedUsers) {
    const cand = candidates.get(username)!;
    if (cand.uid === 0 || cand.user === 'root') continue;
    cand.linger = lingerSet.has(username);
    if (!cand.subUidRange && subuidByUser.has(username)) {
      cand.subUidRange = subuidByUser.get(username);
    }

    const mappedContainers = input.userContainers?.[username];
    let containers: ContainerItem[] = [];
    if (mappedContainers && mappedContainers.length > 0) {
      containers = mappedContainers.map(c => ({
        ...c,
        user: cand.user,
        uid: cand.uid,
        rootless: true,
      }));
    } else if (!input.userContainers || !(username in input.userContainers)) {
      containers = extractContainersFromConmonProcesses(cand.activeProcesses, cand.user, cand.uid);
    }

    if (containers.length > 0) {
      cand.containers = containers;
      activeUsers.push(cand);
    }
  }

  return activeUsers;
}

/**
 * Returns default host probe results discovering unprivileged users running Podman containers
 */
export function getDefaultHostPodmanProbe(): UnprivilegedPodmanUser[] {
  return probeUnprivilegedPodmanUsers({
    psOutput: [
      '0 root /usr/lib/systemd/systemd --system',
      '1000 deployer /usr/bin/conmon --api-version 1 -c d9841af024ea -n infra-con-media',
      '1000 deployer /usr/bin/conmon --api-version 1 -c 3f0a9918bc22 -n jellyfin-media-server',
      '1000 deployer /usr/bin/conmon --api-version 1 -c c810d48a1239 -n caddy-gateway',
      '1001 podman /usr/bin/conmon --api-version 1 -c 7e4b9019a101 -n ollama-vllm-engine',
      '1001 podman /usr/bin/conmon --api-version 1 -c e29d48bc991a -n open-webui',
      '984 restic-svc /usr/bin/conmon --api-version 1 -c 8811aabbccdd -n restic-exporter',
    ].join('\n'),
    passwdOutput: [
      'root:x:0:0:root:/root:/bin/bash',
      'deployer:x:1000:1000:Deployer:/home/deployer:/bin/bash',
      'podman:x:1001:1001:Podman:/home/podman:/bin/bash',
      'restic-svc:x:984:984:Restic Backup:/var/lib/restic:/sbin/nologin',
    ].join('\n'),
    subuidOutput: [
      'deployer:100000:65536',
      'podman:165536:65536',
      'restic-svc:231072:65536',
    ].join('\n'),
    lingerUsers: ['deployer', 'podman'],
    userContainers: {
      deployer: [
        {
          id: 'd9841af024ea',
          podId: 'media-stack',
          name: 'infra-con-media',
          image: 'k8s.gcr.io/pause:3.9',
          status: 'Running',
          cpu: '0.01%',
          memory: '4.2 MiB',
        },
        {
          id: '3f0a9918bc22',
          podId: 'media-stack',
          name: 'jellyfin-media-server',
          image: 'jellyfin/jellyfin:10.8.13',
          status: 'Running (VAAPI)',
          cpu: '2.14%',
          memory: '1.68 GiB',
        },
        {
          id: 'c810d48a1239',
          podId: 'media-stack',
          name: 'caddy-gateway',
          image: 'caddy:2.7.6-alpine',
          status: 'Running',
          cpu: '0.25%',
          memory: '138 MiB',
        },
      ],
      podman: [
        {
          id: '7e4b9019a101',
          podId: 'ai-inference',
          name: 'ollama-vllm-engine',
          image: 'ollama/ollama:latest',
          status: 'Running (CUDA 12.4)',
          cpu: '3.8%',
          memory: '6.4 GiB VRAM',
          gpuAcc: 'RTX 4090 [0000:01:00.0]',
        },
        {
          id: 'e29d48bc991a',
          podId: 'ai-inference',
          name: 'open-webui',
          image: 'ghcr.io/open-webui/open-webui:main',
          status: 'Running',
          cpu: '0.9%',
          memory: '512 MiB System',
          gpuAcc: 'Shared IPC',
        },
      ],
      'restic-svc': [
        {
          id: '8811aabbccdd',
          name: 'restic-exporter',
          image: 'ngosang/restic-exporter:latest',
          status: 'Running',
          cpu: '0.1%',
          memory: '12 MiB',
        },
      ],
    },
  });
}

/**
 * Calculates system health state based on CPU, load, and IO delay
 */
export function calculateSystemHealth(telemetry: Partial<TelemetryData>): 'HEALTHY' | 'WARNING' | 'CRITICAL' {
  const cpu = telemetry.cpuUsage ?? 0;
  const ioDelay = telemetry.ioDelayPercent ?? 0;
  const temp = telemetry.cpuTempC ?? 35;

  if (cpu > 90 || temp > 85 || ioDelay > 15) {
    return 'CRITICAL';
  }
  if (cpu > 70 || temp > 75 || ioDelay > 5) {
    return 'WARNING';
  }
  return 'HEALTHY';
}

/**
 * Filters containers based on running / stopped status
 */
export function filterContainersByStatus(
  containers: ContainerItem[],
  filter: 'all' | 'running' | 'stopped'
): ContainerItem[] {
  if (filter === 'all') return containers;
  if (filter === 'running') {
    return containers.filter(c => c.status.toLowerCase().includes('running'));
  }
  return containers.filter(c => !c.status.toLowerCase().includes('running'));
}

/**
 * Formats byte numbers to human-readable strings
 */
export function formatBytes(bytes: number, decimals = 1): string {
  if (bytes === 0) return '0 B';
  const k = 1024;
  const dm = decimals < 0 ? 0 : decimals;
  const sizes = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(dm))} ${sizes[i]}`;
}

/**
 * Evaluates SSH/Cockpit terminal commands with realistic execution results
 */
export function parseTerminalCommand(cmd: string, node: NodeItem): { output: string; exitCode: number } {
  const trimmed = cmd.trim();
  if (!trimmed) {
    return { output: '', exitCode: 0 };
  }

  const parts = trimmed.split(/\s+/);
  const bin = parts[0].toLowerCase();

  switch (bin) {
    case 'help':
      return {
        output: [
          'PROX-ORCH Integrated Terminal Helper',
          'Supported commands:',
          '  cockpit-bridge   - Query or run Cockpit system bridge session',
          '  podman ps        - List active rootless/system containers',
          '  podman pod ps    - List Podman pods on target',
          '  podman probe     - Probe unprivileged users running rootless Podman containers',
          '  glances          - Display real-time telemetry snapshot',
          '  restic snapshots - List encrypted repository snapshots',
          '  tailscale status - Show Tailscale mesh nodes and IP routing',
          '  uptime           - Show host uptime and load average',
          '  uname -a         - Print kernel and architecture details',
          '  clear            - Clear terminal window',
        ].join('\n'),
        exitCode: 0,
      };

    case 'uname':
      return {
        output: `Linux ${node.name.replace(/[^a-zA-Z0-9-]/g, '')} 6.8.4-pve #1 SMP PREEMPT_DYNAMIC x86_64 GNU/Linux`,
        exitCode: 0,
      };

    case 'uptime':
      return {
        output: ` 10:28:44 up ${node.uptime}, 3 users, load average: 0.42, 0.58, 0.61`,
        exitCode: 0,
      };

    case 'tailscale':
      if (parts[1] === 'status') {
        return {
          output: [
            `${node.tailscaleIp}  ${node.tailscaleHost}  linux  -`,
            `100.97.159.102  cold-war.ts.net         linux  active; direct 192.168.1.150:41641; 1.2ms`,
            `100.97.159.103  noodle.ts.net           linux  active; direct 192.168.1.160:41641; 0.8ms`,
            `100.97.159.120  pong-edge.ts.net        linux  active; relay "ord", 14.1ms`,
            `Tailscale SSH: active (enforcing strict PAM MFA)`,
          ].join('\n'),
          exitCode: 0,
        };
      }
      return { output: 'Usage: tailscale status [--json]', exitCode: 1 };

    case 'podman':
      if (parts[1] === 'ps') {
        return {
          output: [
            'CONTAINER ID  USER (UID)       IMAGE                              STATUS        PORTS                   NAMES',
            'd9841af024ea  deployer (1000)  k8s.gcr.io/pause:3.9               Up (healthy)  8096:8096, 443:443      infra-con-media',
            '3f0a9918bc22  deployer (1000)  jellyfin/jellyfin:10.8.13          Up 2 days     8096->8096/tcp          jellyfin-media-server',
            'c810d48a1239  deployer (1000)  caddy:2.7.6-alpine                 Up 2 days     80->80/tcp, 443->443    caddy-gateway',
            '7e4b9019a101  podman (1001)    ollama/ollama:latest               Up (CUDA)     11434->11434/tcp        ollama-vllm-engine',
            'e29d48bc991a  podman (1001)    ghcr.io/open-webui/open-webui:main Up 12 hours   3000->8080/tcp          open-webui',
          ].join('\n'),
          exitCode: 0,
        };
      }
      if (parts[1] === 'pod' && parts[2] === 'ps') {
        return {
          output: [
            'POD ID        NAME          USER (UID)       STATUS   CREATED       # OF CONTAINERS  INFRA ID',
            'a98c091f09e1  media-stack   deployer (1000)  Running  2 days ago    3                d9841af024ea',
            'bb09817adfe2  ai-inference  podman (1001)    Running  12 hours ago  2                44b910fae120',
          ].join('\n'),
          exitCode: 0,
        };
      }
      if (parts[1] === 'probe') {
        return {
          output: [
            'ROOTLESS PODMAN USER PROBE',
            'USER         UID   SUBUID RANGE    LINGER   CONTAINERS  ACTIVE PODS',
            'deployer     1000  100000:65536    yes      3 running   media-stack',
            'podman       1001  165536:65536    yes      2 running   ai-inference',
            'restic-svc   984   231072:65536    no       1 running   standalone (restic-exporter)',
          ].join('\n'),
          exitCode: 0,
        };
      }
      return { output: 'Usage: podman [ps|pod ps|probe|logs|restart]', exitCode: 1 };

    case 'cockpit-bridge':
      return {
        output: [
          'cockpit-bridge: version 312.1',
          'Channel [0] opened: {"command":"init","version":1}',
          'Authentication: PAM root@pam authorized',
          'Socket /run/cockpit/socket active, D-Bus session bus mapped',
          'Ready for multiplexed WebSocket frames.',
        ].join('\n'),
        exitCode: 0,
      };

    case 'restic':
      if (parts[1] === 'snapshots') {
        return {
          output: [
            'repository s3://restic-homelab-vault/the-freezer opened (version 2, compression: auto)',
            'ID        Time                 Host                  Tags        Paths',
            '--------------------------------------------------------------------------------',
            'f7a91b2e  2025-05-14 10:04:12  the-freezer.homelab   gold,daily  /etc, /tank0/appdata',
            '09a12c4d  2025-05-13 10:00:00  the-freezer.homelab   gold,daily  /etc, /tank0/appdata',
            '88db91e0  2025-05-12 10:00:00  the-freezer.homelab   gold,daily  /etc, /tank0/appdata',
            '--------------------------------------------------------------------------------',
            '3 snapshots (100% deduplicated, 0 modified chunks)',
          ].join('\n'),
          exitCode: 0,
        };
      }
      return { output: 'Usage: restic [snapshots|backup|check]', exitCode: 1 };

    case 'glances':
      return {
        output: [
          'GLANCES v3.4.0.5 - Telemetry Probe',
          `CPU: ${node.specs.cpu} (16 vCPUs) - 1.4% usage, 38°C`,
          'MEM: 42.4 / 62.8 GiB (67.5% used, ZFS ARC: 12.3 GiB)',
          'SWAP: 0.0 / 8.0 GiB (0% used)',
          'ZFS POOL tank0: 14.2 / 48.0 TB (29.5% used, RAIDZ2, Scrub healthy)',
          'NETWORK: bond0 Rx: 12.4 MB/s, Tx: 8.9 MB/s (10GbE SFP+)',
          'PROCESSES: 182 total, 1 running, 181 sleeping',
        ].join('\n'),
        exitCode: 0,
      };

    default:
      return {
        output: `bash: ${bin}: command not found. Type 'help' for available diagnostic commands.`,
        exitCode: 127,
      };
  }
}
