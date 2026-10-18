import type { ContainerItem, NodeItem, TelemetryData } from '../types/index.ts';

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
            'CONTAINER ID  IMAGE                              COMMAND               CREATED        STATUS        PORTS                   NAMES',
            'd9841af024ea  k8s.gcr.io/pause:3.9               /pause                2 days ago     Up (healthy)  8096:8096, 443:443      infra-con-media',
            '3f0a9918bc22  jellyfin/jellyfin:10.8.13          /jellyfin/jellyfin    2 days ago     Up 2 days     8096->8096/tcp          jellyfin-media-server',
            'c810d48a1239  caddy:2.7.6-alpine                 caddy run             2 days ago     Up 2 days     80->80/tcp, 443->443    caddy-gateway',
            '7e4b9019a101  ollama/ollama:latest               serve                 12 hours ago   Up (CUDA)     11434->11434/tcp        ollama-vllm-engine',
            'e29d48bc991a  ghcr.io/open-webui/open-webui:main start.sh              12 hours ago   Up 12 hours   3000->8080/tcp          open-webui',
          ].join('\n'),
          exitCode: 0,
        };
      }
      if (parts[1] === 'pod' && parts[2] === 'ps') {
        return {
          output: [
            'POD ID        NAME          STATUS   CREATED       # OF CONTAINERS  INFRA ID',
            'a98c091f09e1  media-stack   Running  2 days ago    3                d9841af024ea',
            'bb09817adfe2  ai-inference  Running  12 hours ago  2                44b910fae120',
          ].join('\n'),
          exitCode: 0,
        };
      }
      return { output: 'Usage: podman [ps|pod ps|logs|restart]', exitCode: 1 };

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
