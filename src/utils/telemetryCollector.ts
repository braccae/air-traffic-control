import type { NodeItem } from '../types/index.ts';

export interface GlancesMetricPayload {
  cpu: { total: number };
  mem: { percent: number; used: number; total: number };
  load: { min1: number; min5: number; min15: number };
  source: 'http_rest' | 'ssh_localhost_curl' | 'cli_stdout_json' | 'pcp_fallback';
}

export interface FetchTelemetryOptions {
  timeoutMs?: number;
  execRemoteCommand?: (node: NodeItem, cmd: string, timeoutMs?: number) => Promise<string>;
  httpFetch?: typeof fetch;
}

/**
 * Parses raw JSON object or JSON string from Glances REST API (/api/3/all or /api/3/quicklook).
 */
export function parseGlancesRestData(data: any): GlancesMetricPayload {
  const parsed = typeof data === 'string' ? JSON.parse(data) : data || {};
  const cpuTotal = Number(parsed.cpu?.total ?? (typeof parsed.cpu === 'number' ? parsed.cpu : 0));
  const memPercent = Number(parsed.mem?.percent ?? 0);
  const memUsed = Number(parsed.mem?.used ?? 0);
  const memTotal = Number(parsed.mem?.total ?? 0);
  const min1 = Number(parsed.load?.min1 ?? 0);
  const min5 = Number(parsed.load?.min5 ?? 0);
  const min15 = Number(parsed.load?.min15 ?? 0);

  return {
    cpu: { total: cpuTotal },
    mem: { percent: memPercent, used: memUsed, total: memTotal },
    load: { min1, min5, min15 },
    source: 'http_rest',
  };
}

/**
 * Parses stdout from `glances --stdout-json now,cpu,mem,load`.
 */
export function parseGlancesCliJson(stdout: string): GlancesMetricPayload {
  const start = stdout.indexOf('{');
  const end = stdout.lastIndexOf('}');
  if (start === -1 || end === -1 || end < start) {
    throw new Error('Invalid JSON in Glances CLI output: no JSON object found');
  }
  const jsonStr = stdout.slice(start, end + 1);
  const parsed = JSON.parse(jsonStr);

  const cpuTotal = Number(parsed.cpu?.total ?? 0);
  const memPercent = Number(parsed.mem?.percent ?? 0);
  const memUsed = Number(parsed.mem?.used ?? 0);
  const memTotal = Number(parsed.mem?.total ?? 0);
  const min1 = Number(parsed.load?.min1 ?? 0);
  const min5 = Number(parsed.load?.min5 ?? 0);
  const min15 = Number(parsed.load?.min15 ?? 0);

  return {
    cpu: { total: cpuTotal },
    mem: { percent: memPercent, used: memUsed, total: memTotal },
    load: { min1, min5, min15 },
    source: 'cli_stdout_json',
  };
}

/**
 * Parses combined output from sysfs/proc commands (/proc/loadavg, /proc/meminfo, /proc/stat)
 * or PCP metrics fallback without synthetic noise.
 */
export function parseSysfsTelemetry(rawOutput: string): GlancesMetricPayload {
  // 1. Load averages: typically first line or regex match
  const loadMatch = rawOutput.match(/(?:^|\n)\s*(\d+\.\d+)\s+(\d+\.\d+)\s+(\d+\.\d+)/);
  const min1 = loadMatch ? parseFloat(loadMatch[1]) : 0;
  const min5 = loadMatch ? parseFloat(loadMatch[2]) : 0;
  const min15 = loadMatch ? parseFloat(loadMatch[3]) : 0;

  // 2. Memory info
  const memTotalMatch = rawOutput.match(/MemTotal:\s*(\d+)\s*kB/i);
  const memFreeMatch = rawOutput.match(/MemFree:\s*(\d+)\s*kB/i);
  const memAvailMatch = rawOutput.match(/MemAvailable:\s*(\d+)\s*kB/i);
  const buffersMatch = rawOutput.match(/Buffers:\s*(\d+)\s*kB/i);
  const cachedMatch = rawOutput.match(/Cached:\s*(\d+)\s*kB/i);

  const memTotalKb = memTotalMatch ? parseInt(memTotalMatch[1], 10) : 0;
  let memAvailKb = memAvailMatch ? parseInt(memAvailMatch[1], 10) : 0;
  if (!memAvailMatch && memTotalKb > 0) {
    const freeKb = memFreeMatch ? parseInt(memFreeMatch[1], 10) : 0;
    const bufKb = buffersMatch ? parseInt(buffersMatch[1], 10) : 0;
    const cacheKb = cachedMatch ? parseInt(cachedMatch[1], 10) : 0;
    memAvailKb = freeKb + bufKb + cacheKb;
  }
  const memUsedKb = Math.max(0, memTotalKb - memAvailKb);
  const memTotalBytes = memTotalKb * 1024;
  const memUsedBytes = memUsedKb * 1024;
  const memPercent = memTotalKb > 0 ? Number(((memUsedKb / memTotalKb) * 100).toFixed(1)) : 0;

  // 3. CPU info from /proc/stat
  let cpuTotal = 0;
  const statMatch = rawOutput.match(/(?:^|\n)cpu\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)(?:\s+(\d+))?/);
  if (statMatch) {
    const user = parseInt(statMatch[1], 10) || 0;
    const nice = parseInt(statMatch[2], 10) || 0;
    const system = parseInt(statMatch[3], 10) || 0;
    const idle = parseInt(statMatch[4], 10) || 0;
    const iowait = parseInt(statMatch[5], 10) || 0;
    const irq = parseInt(statMatch[6], 10) || 0;
    const softirq = parseInt(statMatch[7], 10) || 0;
    const steal = parseInt(statMatch[8] || '0', 10) || 0;

    const totalTicks = user + nice + system + idle + iowait + irq + softirq + steal;
    const idleTicks = idle + iowait;
    const busyTicks = totalTicks - idleTicks;
    if (totalTicks > 0) {
      cpuTotal = Number(((busyTicks / totalTicks) * 100).toFixed(1));
    }
  }

  return {
    cpu: { total: cpuTotal },
    mem: { percent: memPercent, used: memUsedBytes, total: memTotalBytes },
    load: { min1, min5, min15 },
    source: 'pcp_fallback',
  };
}

/**
 * Multi-Tier Glances Telemetry Engine:
 * Tier 1: Query HTTP REST API: http://${route}:61208/api/3/all (strict timeout)
 * Tier 2: SSH localhost curl: curl -s --connect-timeout 2 http://127.0.0.1:61208/api/3/all
 * Tier 3: CLI tool: glances --stdout-json now,cpu,mem,load
 * Tier 4: Graceful PCP / native sysfs fallback: cat /proc/loadavg; cat /proc/meminfo; cat /proc/stat
 */
export async function fetchNodeTelemetryMultiTier(
  node: NodeItem,
  options?: FetchTelemetryOptions
): Promise<GlancesMetricPayload> {
  const timeoutMs = options?.timeoutMs ?? 2000;
  const execRemote =
    options?.execRemoteCommand ??
    (async (n: NodeItem, cmd: string, timeout = 6000) => {
      const { runNodeCommand } = await import('./liveCluster.ts');
      return runNodeCommand(n, cmd, timeout);
    });
  const httpFetch = options?.httpFetch ?? fetch;

  const errors: string[] = [];

  // Tier 1: Query HTTP REST API first
  const route = node.tailscaleIp || node.ip || node.tailscaleHost || node.fallbackIp || '127.0.0.1';
  const apiPort = node.apiPort || 61208;
  const restUrl = `http://${route}:${apiPort}/api/3/all`;

  try {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeoutMs);
    try {
      const res = await httpFetch(restUrl, { signal: controller.signal });
      if (res.ok) {
        const json = await res.json();
        return parseGlancesRestData(json);
      }
      errors.push(`Tier 1 HTTP status ${res.status}`);
    } finally {
      clearTimeout(timer);
    }
  } catch (err: any) {
    errors.push(`Tier 1 HTTP failed: ${err?.message || String(err)}`);
  }

  // Tier 2: SSH localhost curl
  try {
    const curlCmd = `curl -s --connect-timeout 2 http://127.0.0.1:${apiPort}/api/3/all`;
    const stdout = await execRemote(node, curlCmd, timeoutMs + 1000);
    if (stdout && stdout.trim().startsWith('{')) {
      const payload = parseGlancesRestData(stdout);
      return {
        ...payload,
        source: 'ssh_localhost_curl',
      };
    }
    errors.push('Tier 2 SSH curl returned non-JSON output');
  } catch (err: any) {
    errors.push(`Tier 2 SSH localhost curl failed: ${err?.message || String(err)}`);
  }

  // Tier 3: CLI tool glances --stdout-json
  try {
    const cliCmd = 'glances --stdout-json now,cpu,mem,load 2>/dev/null';
    const stdout = await execRemote(node, cliCmd, timeoutMs + 1500);
    if (stdout && stdout.includes('{')) {
      return parseGlancesCliJson(stdout);
    }
    errors.push('Tier 3 Glances CLI returned non-JSON output');
  } catch (err: any) {
    errors.push(`Tier 3 Glances CLI failed: ${err?.message || String(err)}`);
  }

  // Tier 4: Fallback to native Linux sysfs/proc commands or PCP
  try {
    const sysfsCmd = 'cat /proc/loadavg 2>/dev/null; cat /proc/meminfo 2>/dev/null; cat /proc/stat 2>/dev/null';
    const stdout = await execRemote(node, sysfsCmd, timeoutMs + 1000);
    if (stdout && (stdout.includes('MemTotal') || stdout.includes('cpu '))) {
      return parseSysfsTelemetry(stdout);
    }
    errors.push('Tier 4 sysfs commands returned insufficient data');
  } catch (err: any) {
    errors.push(`Tier 4 sysfs fallback failed: ${err?.message || String(err)}`);
  }

  // Strictly adhere to Zero-Mock Policy: NEVER return synthetic fake metrics
  throw new Error(
    `Failed to collect telemetry for node ${node.name || node.id}: all probe tiers exhausted (${errors.join('; ')})`
  );
}
