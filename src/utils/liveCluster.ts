import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import type {
  NodeItem,
  TelemetryData,
  UnprivilegedPodmanUser,
  ServiceUser,
  ContainerItem,
  PodItem,
  PrivilegeMethod,
  PrivilegeStatus,
} from '../types/index.ts';
import { probeUnprivilegedPodmanUsers } from './orchestrator.ts';
import { parseDockerPsOutput, separatePodsAndStandaloneContainers } from './podmanViewModel.ts';
import {
  wrapElevatedCommand,
  buildPrivilegeProbeCommand,
  parsePrivilegeProbeOutput,
} from './privilegeEscalation.ts';

const execFileAsync = promisify(execFile);

const SSH_AGENT_SOCKET = process.env.SSH_AUTH_SOCK || '/run/user/1000/ssh-agent.socket';

export const LIVE_TAILNET_NODES: NodeItem[] = [
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
      motherboard: 'QEMU Standard PC (i440FX + PIIX)',
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

export async function runNodeCommand(node: NodeItem, command: string, timeoutMs = 6000): Promise<string> {
  // If node prefers run0 or doas and command attempts sudo, transparently wrap with the preferred method
  let effectiveCommand = command;
  if (node.privilegeMethod === 'run0' && (command.startsWith('sudo ') || command.startsWith('sudo -n '))) {
    effectiveCommand = wrapElevatedCommand(command, 'run0', node.sshUser || 'core');
  } else if (node.privilegeMethod === 'doas' && (command.startsWith('sudo ') || command.startsWith('sudo -n '))) {
    effectiveCommand = wrapElevatedCommand(command, 'doas', node.sshUser || 'core');
  }

  const isLocal =
    node.ip === '127.0.0.1' ||
    node.ip === 'localhost' ||
    node.shortName === 'dev-pc' ||
    node.tailscaleHost.startsWith('dev-pc');

  if (isLocal) {
    const { stdout } = await execFileAsync('sh', ['-c', effectiveCommand], {
      timeout: timeoutMs,
      env: { ...process.env, SSH_AUTH_SOCK: SSH_AGENT_SOCKET },
    });
    return stdout;
  }

  const sshUser = node.sshUser || 'core';
  const targetHost = node.tailscaleIp || node.ip || node.tailscaleHost;
  const port = String(node.port || 22);

  const { stdout } = await execFileAsync(
    'ssh',
    [
      '-F',
      '/dev/null',
      '-o',
      'BatchMode=yes',
      '-o',
      'StrictHostKeyChecking=accept-new',
      '-o',
      'ConnectTimeout=4',
      '-p',
      port,
      `${sshUser}@${targetHost}`,
      effectiveCommand,
    ],
    {
      timeout: timeoutMs,
      env: { ...process.env, SSH_AUTH_SOCK: SSH_AGENT_SOCKET },
    }
  );
  return stdout;
}

export async function runNodeElevatedCommand(
  node: NodeItem,
  command: string,
  timeoutMs = 6000
): Promise<string> {
  const method = node.privilegeMethod || 'run0';
  const effectiveCmd = wrapElevatedCommand(command, method, node.sshUser || 'core');
  return runNodeCommand(node, effectiveCmd, timeoutMs);
}

export async function probeNodePrivilege(node: NodeItem): Promise<PrivilegeStatus> {
  const user = node.sshUser || 'core';
  if (user === 'root') {
    node.privilegeMethod = 'none';
    node.privilegeActive = true;
    return {
      method: 'none',
      active: true,
      user: 'root',
      canElevate: true,
    };
  }

  const probeCmd = buildPrivilegeProbeCommand();
  try {
    const stdout = await runNodeCommand(node, probeCmd, 8000);
    const status = parsePrivilegeProbeOutput(stdout, user);
    node.privilegeMethod = status.method;
    node.privilegeActive = status.active;
    return status;
  } catch (err: any) {
    return {
      method: 'none',
      active: false,
      user,
      canElevate: false,
      error: err?.message || String(err),
    };
  }
}

export async function testNodePrivilege(
  node: NodeItem,
  requestedMethod?: PrivilegeMethod
): Promise<{ success: boolean; method: PrivilegeMethod; output: string; error?: string }> {
  const method = requestedMethod || node.privilegeMethod || 'run0';
  const testCmd =
    method === 'run0'
      ? 'run0 --background=no id -u'
      : method === 'sudo'
        ? 'sudo -n id -u'
        : method === 'doas'
          ? 'doas -n id -u'
          : 'id -u';

  try {
    const stdout = await runNodeCommand(node, testCmd, 6000);
    const trimmed = stdout.trim();
    const success = trimmed === '0';
    if (success) {
      node.privilegeMethod = method;
      node.privilegeActive = true;
    }
    return {
      success,
      method,
      output: trimmed,
    };
  } catch (err: any) {
    return {
      success: false,
      method,
      output: '',
      error: err?.message || 'Elevation test failed',
    };
  }
}

export async function refreshTailscaleNodes(existingNodes: NodeItem[]): Promise<NodeItem[]> {
  try {
    const { stdout } = await execFileAsync('tailscale', ['status', '--json'], { timeout: 3000 });
    const data = JSON.parse(stdout);
    const peersByHost = new Map<string, any>();

    if (data.Self) {
      const selfDns = (data.Self.DNSName || '').replace(/\.$/, '');
      peersByHost.set(selfDns, { ...data.Self, isSelf: true });
      peersByHost.set(data.Self.HostName, { ...data.Self, isSelf: true });
    }
    if (data.Peer) {
      for (const peer of Object.values<any>(data.Peer)) {
        const dns = (peer.DNSName || '').replace(/\.$/, '');
        const short = dns.split('.')[0] || peer.HostName;
        peersByHost.set(dns, peer);
        peersByHost.set(short, peer);
      }
    }

    return existingNodes.map(node => {
      const peer = peersByHost.get(node.tailscaleHost) || peersByHost.get(node.shortName) || peersByHost.get(node.name);
      if (!peer) return node;
      const tsIp = Array.isArray(peer.TailscaleIPs) && peer.TailscaleIPs.length > 0 ? peer.TailscaleIPs[0] : node.tailscaleIp;
      const dns = (peer.DNSName || '').replace(/\.$/, '') || node.tailscaleHost;
      let lanIp = node.ip;
      if (Array.isArray(peer.Addrs) && peer.Addrs.length > 0) {
        const addrIp = String(peer.Addrs[0]).split(':')[0];
        if (addrIp && !addrIp.startsWith('127.')) {
          lanIp = addrIp;
        }
      }
      return {
        ...node,
        ip: lanIp,
        tailscaleIp: tsIp,
        tailscaleHost: dns,
        status: peer.Online ? 'online' : 'stopped',
        tags: Array.isArray(peer.Tags) ? peer.Tags : node.tags,
      };
    });
  } catch {
    return existingNodes;
  }
}

export async function fetchLiveNodeTelemetry(node: NodeItem): Promise<Partial<TelemetryData> & { uptime?: string; osDescription?: string }> {
  try {
    const script = [
      'cat /proc/loadavg',
      'free -m | awk \'/^Mem:/ {print $2, $3, $6}\'',
      'nproc',
      'lscpu | awk -F: \'/Model name/ {gsub(/^[ \\t]+/, "", $2); print $2; exit}\'',
      'uptime -p 2>/dev/null || uptime',
      '. /etc/os-release 2>/dev/null && echo "$PRETTY_NAME"',
      'df -BG / | awk \'NR==2 {gsub(/G/, "", $2); gsub(/G/, "", $3); print $2, $3}\'',
      'which glances 2>/dev/null || echo "NO_GLANCES"',
      'which pcp 2>/dev/null || echo "NO_PCP"',
      'which cockpit-bridge 2>/dev/null || echo "NO_COCKPIT"',
      'cat /proc/spl/kstat/zfs/arcstats 2>/dev/null | awk \'/^size/ {print $3}\' || echo "NO_ARC"',
      'zpool list -H -o name,size,alloc,free,cap,health 2>/dev/null | head -n 1 || echo "NO_POOL"',
    ].join(' && echo "---SPLIT---" && ');

    const out = await runNodeCommand(node, script, 5000);
    const sections = out.split('---SPLIT---').map(s => s.trim());

    const loadParts = (sections[0] || '').split(/\s+/);
    const load1 = Number.parseFloat(loadParts[0]) || 0.2;
    const load5 = Number.parseFloat(loadParts[1]) || 0.3;
    const load15 = Number.parseFloat(loadParts[2]) || 0.25;

    const memParts = (sections[1] || '').split(/\s+/);
    const memTotalMb = Number.parseFloat(memParts[0]) || node.specs.ramGiB * 1024;
    const memUsedMb = Number.parseFloat(memParts[1]) || memTotalMb * 0.35;
    const memCacheMb = Number.parseFloat(memParts[2]) || memTotalMb * 0.15;

    const vCpus = Number.parseInt(sections[2] || '', 10) || node.specs.vcpus;
    const cpuModel = sections[3] || node.specs.cpu;
    const uptimeRaw = (sections[4] || '').replace(/^up\s+/i, '').trim() || node.uptime;
    const osPretty = sections[5] || node.osDescription;

    const hasGlances = sections[7] && !sections[7].includes('NO_GLANCES');
    const hasPcp = sections[8] && !sections[8].includes('NO_PCP');
    const hasCockpit = sections[9] && !sections[9].includes('NO_COCKPIT');

    // Real ZFS ARC vs Linux Page Cache
    const arcBytes = Number.parseInt(sections[10] || '', 10);
    const hasArc = !Number.isNaN(arcBytes) && arcBytes > 0;
    const arcCacheGiB = hasArc ? +(arcBytes / (1024 * 1024 * 1024)).toFixed(1) : 0;
    const buffCacheGiB = +(memCacheMb / 1024).toFixed(1);

    // Real ZFS Pool vs Btrfs / XFS
    const poolLine = sections[11] || '';
    const poolParts = poolLine.split(/\s+/);
    let zfsTankTotalTb = 0;
    let zfsTankUsedTb = 0;
    let zfsRaidType = node.shortName === 'likes-it-cold' ? 'Btrfs / LVM Cache' : 'XFS / LVM Root';
    let zfsScrubStatus = node.shortName === 'likes-it-cold' ? 'Btrfs: 3 Active Subvolumes' : `Host: ${node.shortName}`;

    if (poolParts.length >= 6 && poolParts[0] !== 'NO_POOL' && !poolParts[0].includes('no pools')) {
      const pName = poolParts[0];
      const pSize = Number.parseFloat(poolParts[1].replace(/[TG]/i, '')) || 7.27;
      const pAlloc = Number.parseFloat(poolParts[2].replace(/[TG]/i, '')) || 4.22;
      const pHealth = poolParts[5];
      zfsTankTotalTb = pSize;
      zfsTankUsedTb = pAlloc;
      zfsRaidType = `ZFS Pool (${pName})`;
      zfsScrubStatus = `ZFS: ${pHealth} (${poolParts[4]} allocated)`;
    }

    let metricsSource: 'glances' | 'pcp' | 'cockpit-bridge' | 'kernel' = 'kernel';
    let metricsWarning: string | undefined;

    if (hasGlances) {
      metricsSource = 'glances';
    } else if (hasPcp) {
      metricsSource = 'pcp';
      metricsWarning = 'Glances not installed on host. Falling back to PCP (Performance Co-Pilot) daemon.';
    } else if (hasCockpit) {
      metricsSource = 'cockpit-bridge';
      metricsWarning = 'Glances not installed on host. Sourcing system metrics via Cockpit Bridge.';
    } else {
      metricsSource = 'kernel';
      metricsWarning = 'Glances not installed on host. Sourcing metrics directly from /proc & sysfs.';
    }

    const cpuUsage = Math.max(0.5, Math.min(99.0, +((load1 / Math.max(1, vCpus)) * 100).toFixed(1)));

    return {
      cpuUsage,
      cpuModel,
      vCpus,
      memoryTotalGiB: +(memTotalMb / 1024).toFixed(1),
      memoryUsedGiB: +(memUsedMb / 1024).toFixed(1),
      arcCacheGiB,
      buffCacheGiB,
      zfsTankTotalTb,
      zfsTankUsedTb,
      zfsRaidType,
      zfsScrubStatus,
      loadAvg1m: load1,
      loadAvg5m: load5,
      loadAvg15m: load15,
      uptime: uptimeRaw,
      osDescription: osPretty,
      metricsSource,
      metricsWarning,
    };
  } catch {
    return {
      cpuModel: node.specs.cpu,
      vCpus: node.specs.vcpus,
      memoryTotalGiB: node.specs.ramGiB,
      memoryUsedGiB: +(node.specs.ramGiB * 0.4).toFixed(1),
      zfsScrubStatus: `Host: ${node.shortName} (Cached)`,
      metricsSource: 'cockpit-bridge',
      metricsWarning: 'Glances not reachable. Using cached Cockpit system telemetry.',
    };
  }
}

export interface ProbePodmanResult {
  probedUsers: UnprivilegedPodmanUser[];
  identities: ServiceUser[];
  rootContainers: ContainerItem[];
  rootPods: PodItem[];
  hasDocker: boolean;
  dockerContainers: ContainerItem[];
}

export async function probeNodePodmanAndIdentities(node: NodeItem): Promise<ProbePodmanResult> {
  try {
    const sshUser = node.sshUser || 'core';
    const targetHostParam =
      node.shortName === 'dev-pc' ? '127.0.0.1' : node.tailscaleHost || node.ip;

    let goProbed: UnprivilegedPodmanUser[] = [];
    try {
      const resp = await fetch(
        `http://127.0.0.1:18090/api/podman/probe?host=${encodeURIComponent(targetHostParam)}&user=${encodeURIComponent(sshUser)}`,
        { signal: AbortSignal.timeout(2000) }
      );
      if (resp.ok) {
        const data = await resp.json();
        if (Array.isArray(data)) {
          goProbed = data.map((u: any) => ({
            ...u,
            containers: (u.containers || [])
              .filter((c: any) => {
                const n = (c.name || '').toLowerCase();
                const img = (c.image || '').toLowerCase();
                return !n.includes('pause') && !n.endsWith('-infra') && !img.includes('pause');
              })
              .map((c: any) => ({
                ...c,
                cpu: c.cpu || '',
                memory: c.memory || '',
              })),
          }));
        }
      }
    } catch {
      // Fallback to direct script
    }

    const script = [
      'ps -eo uid,user:32,args --no-headers',
      'echo "===PASSWD==="',
      'cat /etc/passwd',
      'echo "===SUBUID==="',
      'cat /etc/subuid 2>/dev/null || true',
      'echo "===LINGER==="',
      'ls -1 /var/lib/systemd/linger 2>/dev/null || true',
      'echo "===RUNUSER==="',
      'ls -1d /run/user/*/libpod /run/user/*/podman /run/user/*/containers 2>/dev/null || true',
      'echo "===ROOTPODMAN==="',
      '(run0 --background=no podman ps --all --format json 2>/dev/null || sudo -n podman ps --all --format json 2>/dev/null || podman ps --all --format json 2>/dev/null) || true',
      'echo "===ROOTPODS==="',
      '(run0 --background=no podman pod ps --format json 2>/dev/null || sudo -n podman pod ps --format json 2>/dev/null || podman pod ps --format json 2>/dev/null) || true',
      'echo "===ROOTSTATS==="',
      '(run0 --background=no podman stats --no-stream --format json 2>/dev/null || sudo -n podman stats --no-stream --format json 2>/dev/null || podman stats --no-stream --format json 2>/dev/null) || true',
      'echo "===USERPROBES==="',
      'USERS=$( ( [ -d /run/user ] && ls -1 /run/user 2>/dev/null | grep -E "^[0-9]+$" | while read u; do id -nu "$u" 2>/dev/null; done ; ls -1 /var/lib/systemd/linger 2>/dev/null ; awk -F: "$1 != \\"root\\" && $2 != \\"\\" {print $1}" /etc/subuid 2>/dev/null ) | sort -u )',
      'for u in $USERS; do uid=$(id -u "$u" 2>/dev/null || true); [ -n "$uid" ] && [ "$uid" -gt 0 ] 2>/dev/null || continue; echo "---USER_PS:$u:$uid---"; (run0 --background=no --user "$u" podman ps --all --format json 2>/dev/null || sudo -n -u "$u" XDG_RUNTIME_DIR=/run/user/$uid podman ps --all --format json 2>/dev/null || XDG_RUNTIME_DIR=/run/user/$uid podman ps --all --format json 2>/dev/null) || true; echo "---USER_PODS:$u:$uid---"; (run0 --background=no --user "$u" podman pod ps --format json 2>/dev/null || sudo -n -u "$u" XDG_RUNTIME_DIR=/run/user/$uid podman pod ps --format json 2>/dev/null || XDG_RUNTIME_DIR=/run/user/$uid podman pod ps --format json 2>/dev/null) || true; echo "---USER_STATS:$u:$uid---"; (run0 --background=no --user "$u" podman stats --no-stream --format json 2>/dev/null || sudo -n -u "$u" XDG_RUNTIME_DIR=/run/user/$uid podman stats --no-stream --format json 2>/dev/null || XDG_RUNTIME_DIR=/run/user/$uid podman stats --no-stream --format json 2>/dev/null) || true; done',
      'echo "===DOCKER_CLI==="',
      'which docker 2>/dev/null || echo "NO_DOCKER"',
      'echo "===DOCKER==="',
      '(docker ps --format "{{.ID}}:::{{.Names}}:::{{.Image}}:::{{.Status}}" 2>/dev/null || sudo -n docker ps --format "{{.ID}}:::{{.Names}}:::{{.Image}}:::{{.Status}}" 2>/dev/null) || echo "NO_DOCKER"',
    ].join('; ');

    const raw = await runNodeCommand(node, script, 8000);
    const [psPart, rest1 = ''] = raw.split('===PASSWD===');
    const [passwdPart, rest2 = ''] = rest1.split('===SUBUID===');
    const [subuidPart, rest3 = ''] = rest2.split('===LINGER===');
    const [lingerPart, rest4 = ''] = rest3.split('===RUNUSER===');
    const [runUserPart, rest5 = ''] = rest4.split('===ROOTPODMAN===');
    const [rootPodmanPart, rest6 = ''] = rest5.split('===ROOTPODS===');
    const [rootPodsPart, rest7 = ''] = rest6.split('===ROOTSTATS===');
    const [rootStatsPart, rest8 = ''] = rest7.split('===USERPROBES===');
    const [userProbesPart, rest9 = ''] = rest8.split('===DOCKER_CLI===');
    const [dockerCliPart, dockerPart = ''] = rest9.split('===DOCKER===');

    // Parse stats JSON helper
    const parseStatsJson = (statsJsonStr: string) => {
      const map = new Map<string, { cpu: string; memory: string }>();
      try {
        const parsed = JSON.parse(statsJsonStr.trim());
        if (Array.isArray(parsed)) {
          for (const s of parsed) {
            const sid = String(s.ID || s.Id || s.Container || '').trim();
            const sname = String(s.Name || '').trim();
            const cpu = String(s.CPUPerc || s.CPU || s.cpu || '').trim();
            const memRaw = String(s.MemUsage || s.Memory || s.memory || '').trim();
            const mem = memRaw.includes('/') ? memRaw.split('/')[0].trim() : memRaw;
            const entry = { cpu, memory: mem };
            if (sid) {
              map.set(sid, entry);
              if (sid.length > 12) map.set(sid.slice(0, 12), entry);
            }
            if (sname) {
              map.set(sname, entry);
            }
          }
        }
      } catch {}
      return map;
    };

    const rootStatsMap = parseStatsJson(rootStatsPart || '');

    // Parse user probes from userProbesPart
    const discoveredUserContainers: Record<string, ContainerItem[]> = {};
    const discoveredUserPods: Record<string, PodItem[]> = {};

    const userProbeBlocks = (userProbesPart || '').split('---USER_PS:');
    for (const block of userProbeBlocks) {
      if (!block.trim()) continue;
      const firstLineEnd = block.indexOf('---');
      if (firstLineEnd === -1) continue;
      const userMeta = block.slice(0, firstLineEnd).trim();
      const [uName, uUidStr] = userMeta.split(':');
      if (!uName) continue;
      const uUid = Number.parseInt(uUidStr, 10) || 1000;

      const restOfBlock = block.slice(firstLineEnd + 3);
      const [userPsJson, restUser1 = ''] = restOfBlock.split(`---USER_PODS:${uName}:${uUid}---`);
      const [userPodsJson, userStatsJson = ''] = restUser1.split(`---USER_STATS:${uName}:${uUid}---`);

      const userStatsMap = parseStatsJson(userStatsJson);
      const userContainersList: ContainerItem[] = [];

      try {
        const parsedCs = JSON.parse((userPsJson || '').trim());
        if (Array.isArray(parsedCs)) {
          for (const rc of parsedCs) {
            const cName = Array.isArray(rc.Names) && rc.Names.length > 0 ? rc.Names[0] : rc.Names || rc.Id;
            const cNameLower = String(cName).toLowerCase();
            const imgLower = String(rc.Image || '').toLowerCase();
            if (
              rc.IsInfra ||
              cNameLower.includes('pause') ||
              cNameLower.endsWith('-infra') ||
              imgLower.includes('pause') ||
              imgLower.includes('podman-pause')
            ) {
              continue;
            }
            const fullId = String(rc.Id || cName);
            const shortId = fullId.slice(0, 12);
            const nameStr = String(cName);
            const stat = userStatsMap.get(fullId) || userStatsMap.get(shortId) || userStatsMap.get(nameStr);

            userContainersList.push({
              id: shortId,
              name: nameStr,
              image: String(rc.Image || `${nameStr}:latest`),
              status: String(rc.Status || rc.State || 'Running'),
              cpu: stat?.cpu || '',
              memory: stat?.memory || '',
              user: uName,
              uid: uUid,
              rootless: true,
              podId: rc.Pod || rc.PodId || undefined,
              podName: rc.PodName || undefined,
            });
          }
        }
      } catch {}

      const userPodsList: PodItem[] = [];
      try {
        const parsedPs = JSON.parse((userPodsJson || '').trim());
        if (Array.isArray(parsedPs)) {
          for (const rp of parsedPs) {
            const pName = String(rp.Name || rp.Id || 'user-pod');
            const pId = String(rp.Id || pName);
            const memberContainers = userContainersList.filter(
              c => c.podId === pId || c.podName === pName || c.podId === rp.Name
            );
            userPodsList.push({
              id: pId,
              name: pName.startsWith('pod:') ? pName : `pod: ${pName}`,
              containersRunningCount: memberContainers.filter(c =>
                c.status.toLowerCase().includes('running') || c.status.toLowerCase().startsWith('up ')
              ).length,
              ports: rp.PortMappings || rp.Ports ? String(rp.PortMappings || rp.Ports) : `Host: ${node.tailscaleHost || 'local'}`,
              totalCpu: '',
              totalRam: '',
              user: uName,
              uid: uUid,
              rootless: true,
              containers: memberContainers,
            });
          }
        }
      } catch {}

      if (userContainersList.length > 0) {
        discoveredUserContainers[uName] = userContainersList;
      }
      if (userPodsList.length > 0) {
        discoveredUserPods[uName] = userPodsList;
      }
    }

    const baseProbed =
      goProbed.length > 0
        ? goProbed
        : probeUnprivilegedPodmanUsers({
            psOutput: psPart || '',
            passwdOutput: passwdPart || '',
            subuidOutput: subuidPart || '',
            lingerUsers: lingerPart || '',
            runUserDirs: runUserPart || '',
            userContainers: Object.keys(discoveredUserContainers).length > 0 ? discoveredUserContainers : undefined,
            userPods: Object.keys(discoveredUserPods).length > 0 ? discoveredUserPods : undefined,
          });

    const passwdLines = (passwdPart || '').split('\n').filter(Boolean);
    const passwdMap = new Map<string, { uid: number; gid: number; home: string; shell: string }>();
    for (const line of passwdLines) {
      const parts = line.split(':');
      if (parts.length >= 7) {
        const uid = Number.parseInt(parts[2], 10);
        const gid = Number.parseInt(parts[3], 10);
        if (!Number.isNaN(uid) && uid >= 900 && uid < 65534) {
          passwdMap.set(parts[0], { uid, gid, home: parts[5], shell: parts[6] });
        }
      }
    }

    const lingerSet = new Set(
      (lingerPart || '')
        .split('\n')
        .map(s => s.trim())
        .filter(Boolean)
    );
    const subuidRangeMap = new Map<string, string>();
    for (const line of (subuidPart || '').split('\n')) {
      const p = line.trim().split(':');
      if (p.length >= 3) subuidRangeMap.set(p[0], `${p[1]}:${p[2]}`);
    }

    const parsedRootPods: PodItem[] = [];
    try {
      const parsedPods = JSON.parse((rootPodsPart || '').trim());
      if (Array.isArray(parsedPods)) {
        for (const rp of parsedPods) {
          const pName = String(rp.Name || rp.Id || 'system-pod');
          const pId = String(rp.Id || pName);
          parsedRootPods.push({
            id: pId,
            name: pName.startsWith('pod:') ? pName : `pod: ${pName}`,
            containersRunningCount: 0,
            ports: rp.PortMappings || rp.Ports ? String(rp.PortMappings || rp.Ports) : `Host: ${node.tailscaleHost || 'local'}`,
            totalCpu: '',
            totalRam: '',
            user: 'root',
            uid: 0,
            rootless: false,
            containers: [],
          });
        }
      }
    } catch {}

    const rawRootContainers: ContainerItem[] = [];
    try {
      const parsedRoot = JSON.parse((rootPodmanPart || '').trim());
      if (Array.isArray(parsedRoot)) {
        for (const rc of parsedRoot) {
          const cName = Array.isArray(rc.Names) && rc.Names.length > 0 ? rc.Names[0] : rc.Names || rc.Id;
          const cNameLower = String(cName).toLowerCase();
          const imgLower = String(rc.Image || '').toLowerCase();
          if (
            rc.IsInfra ||
            cNameLower.includes('pause') ||
            cNameLower.endsWith('-infra') ||
            imgLower.includes('pause') ||
            imgLower.includes('podman-pause')
          ) {
            continue;
          }
          const fullId = String(rc.Id || cName);
          const shortId = fullId.slice(0, 12);
          const nameStr = String(cName);
          const podId = rc.Pod || rc.PodId || '';
          const podName = rc.PodName || '';
          const stat = rootStatsMap.get(fullId) || rootStatsMap.get(shortId) || rootStatsMap.get(nameStr);

          rawRootContainers.push({
            id: shortId,
            name: nameStr,
            image: String(rc.Image || `${nameStr}:latest`),
            status: String(rc.Status || rc.State || 'Running'),
            cpu: stat?.cpu || '',
            memory: stat?.memory || '',
            user: 'root',
            uid: 0,
            rootless: false,
            podId: podId || undefined,
            podName: podName || undefined,
          });
        }
      }
    } catch {
      // Ignore JSON parse if empty
    }

    const { pods: rootPods, standaloneContainers: rootContainers } =
      separatePodsAndStandaloneContainers(rawRootContainers, parsedRootPods);

    const { hasDocker, dockerContainers } = parseDockerPsOutput(dockerPart || '', dockerCliPart || '');

    const identities: ServiceUser[] = [];
    for (const [user, pw] of passwdMap.entries()) {
      const subRange = subuidRangeMap.get(user);
      const hasLinger = lingerSet.has(user);
      identities.push({
        user,
        uidGid: `${pw.uid}:${pw.gid}`,
        rolesGroups: user === 'core' || user === 'pants' ? 'wheel, sudo, systemd-journal' : 'podman, service-identity',
        authMechanism: subRange ? `Rootless SubUID ${subRange}` : pw.shell,
        lingerSubUid: hasLinger ? `Linger: Enabled (${subRange || 'uid'})` : subRange ? `SubUID: ${subRange}` : 'Linger: No',
        icon: user === 'core' || user === 'pants' ? 'badge' : 'deployed_code',
      });
    }

    return {
      probedUsers: baseProbed,
      identities,
      rootContainers,
      rootPods,
      hasDocker,
      dockerContainers,
    };
  } catch {
    return {
      probedUsers: [],
      identities: [],
      rootContainers: [],
      rootPods: [],
      hasDocker: false,
      dockerContainers: [],
    };
  }
}
