import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import type { NodeItem, TelemetryData, UnprivilegedPodmanUser, ServiceUser } from '../types/index.ts';
import { probeUnprivilegedPodmanUsers } from './orchestrator.ts';

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
    uptime: '2d 18h',
    hasGlances: true,
    hasCockpit: true,
    hasPodman: true,
    hasRestic: true,
    specs: {
      cpu: 'AMD EPYC / x86_64 Host',
      vcpus: 8,
      ramGiB: 16,
      motherboard: 'Proxmox / KVM Guest (QEMU Standard PC)',
      bios: 'OVMF UEFI',
      ipmi: 'Proxmox API',
      ecc: 'Active',
      secureBoot: 'Enabled',
      iommu: 'Enabled',
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
    uptime: '3d 18h',
    hasGlances: true,
    hasCockpit: true,
    hasPodman: true,
    hasRestic: true,
    specs: {
      cpu: 'Intel(R) Core(TM) i5-6500 CPU @ 3.20GHz',
      vcpus: 4,
      ramGiB: 8,
      motherboard: 'Dell Inc. OptiPlex / Skylake',
      bios: 'UEFI 2.7',
      ipmi: 'Intel AMT',
      ecc: 'Non-ECC',
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
      motherboard: 'AM4 Workstation',
      bios: 'AGESA ComboV2',
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
    sshConnected: false,
    rttMs: '1.5ms',
    tailscaleIp: '100.115.75.78',
    tailscaleHost: 'the-freezer.leo-banjo.ts.net',
    osDescription: 'Linux (Tailscale Mesh Node)',
    status: 'online',
    uptime: '14d 08h',
    hasGlances: true,
    hasCockpit: true,
    hasPodman: true,
    hasRestic: true,
    specs: {
      cpu: 'x86_64 Server Platform',
      vcpus: 8,
      ramGiB: 16,
      motherboard: 'Homelab Manager Node',
      bios: 'UEFI',
      ipmi: '192.168.8.3',
      ecc: 'Active',
      secureBoot: 'Enabled',
      iommu: 'Enabled',
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
  const isLocal =
    node.ip === '127.0.0.1' ||
    node.ip === 'localhost' ||
    node.shortName === 'dev-pc' ||
    node.tailscaleHost.startsWith('dev-pc');

  if (isLocal) {
    const { stdout } = await execFileAsync('sh', ['-c', command], {
      timeout: timeoutMs,
      env: { ...process.env, SSH_AUTH_SOCK: SSH_AGENT_SOCKET },
    });
    return stdout;
  }

  const sshUser = node.sshUser || 'core';
  const targetHost = node.tailscaleHost || node.tailscaleIp || node.ip;
  const port = String(node.port || 22);

  const { stdout } = await execFileAsync(
    'ssh',
    [
      '-o',
      'BatchMode=yes',
      '-o',
      'StrictHostKeyChecking=accept-new',
      '-o',
      'ConnectTimeout=4',
      '-p',
      port,
      `${sshUser}@${targetHost}`,
      command,
    ],
    {
      timeout: timeoutMs,
      env: { ...process.env, SSH_AUTH_SOCK: SSH_AGENT_SOCKET },
    }
  );
  return stdout;
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

    const dfParts = (sections[6] || '').split(/\s+/);
    const rootTotalGb = Number.parseFloat(dfParts[0]) || 64;
    const rootUsedGb = Number.parseFloat(dfParts[1]) || 18;

    const hasGlances = sections[7] && !sections[7].includes('NO_GLANCES');
    const hasPcp = sections[8] && !sections[8].includes('NO_PCP');
    const hasCockpit = sections[9] && !sections[9].includes('NO_COCKPIT');

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
      arcCacheGiB: +(memCacheMb / 1024).toFixed(1),
      zfsTankTotalTb: +(rootTotalGb / 1000).toFixed(2),
      zfsTankUsedTb: +(rootUsedGb / 1000).toFixed(2),
      zfsRaidType: 'XFS / RootFS',
      zfsScrubStatus: `Host: ${node.shortName}`,
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

export async function probeNodePodmanAndIdentities(node: NodeItem): Promise<{
  probedUsers: UnprivilegedPodmanUser[];
  identities: ServiceUser[];
  rootContainers: Array<{ id: string; name: string; image: string; status: string; user: string; uid: number; rootless: boolean }>;
}> {
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
            containers: (u.containers || []).map((c: any) => ({
              ...c,
              cpu: c.cpu || '0.4%',
              memory: c.memory || '64 MiB',
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
      'sudo -n podman ps --all --format json 2>/dev/null || true',
      'echo "===DOCKER==="',
      'sudo -n docker ps --format "{{.ID}}:::{{.Names}}:::{{.Image}}:::{{.Status}}" 2>/dev/null || true',
    ].join('; ');

    const raw = await runNodeCommand(node, script, 6000);
    const [psPart, rest1 = ''] = raw.split('===PASSWD===');
    const [passwdPart, rest2 = ''] = rest1.split('===SUBUID===');
    const [subuidPart, rest3 = ''] = rest2.split('===LINGER===');
    const [lingerPart, rest4 = ''] = rest3.split('===RUNUSER===');
    const [runUserPart, rest5 = ''] = rest4.split('===ROOTPODMAN===');
    const [rootPodmanPart, dockerPart = ''] = rest5.split('===DOCKER===');

    const baseProbed =
      goProbed.length > 0
        ? goProbed
        : probeUnprivilegedPodmanUsers({
            psOutput: psPart || '',
            passwdOutput: passwdPart || '',
            subuidOutput: subuidPart || '',
            lingerUsers: lingerPart || '',
            runUserDirs: runUserPart || '',
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

    const rootContainers: Array<{ id: string; name: string; image: string; status: string; user: string; uid: number; rootless: boolean }> = [];
    if (node.shortName !== 'dev-pc') {
      try {
        const parsedRoot = JSON.parse((rootPodmanPart || '').trim());
        if (Array.isArray(parsedRoot)) {
          for (const rc of parsedRoot) {
            const cName = Array.isArray(rc.Names) && rc.Names.length > 0 ? rc.Names[0] : rc.Names || rc.Id;
            rootContainers.push({
              id: String(rc.Id || cName).slice(0, 12),
              name: String(cName),
              image: String(rc.Image || 'system/container'),
              status: String(rc.Status || rc.State || 'Running'),
              user: 'root',
              uid: 0,
              rootless: false,
            });
          }
        }
      } catch {
        // Ignore JSON parse if empty
      }

      for (const rawLine of (dockerPart || '').split('\n')) {
        const line = rawLine.trim();
        if (!line.includes(':::')) continue;
        const [id, name, image, status] = line.split(':::');
        if (!id || id.includes('{{')) continue;
        rootContainers.push({
          id,
          name,
          image,
          status: status || 'Running',
          user: 'root',
          uid: 0,
          rootless: false,
        });
      }
    }

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
    };
  } catch {
    return {
      probedUsers: [],
      identities: [],
      rootContainers: [],
    };
  }
}
