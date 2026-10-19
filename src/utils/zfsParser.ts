export interface ZfsPoolDevice {
  name: string;
  state: string;
  role?: string;
  read?: string;
  write?: string;
  cksum?: string;
}

export interface ZfsPoolItem {
  name: string;
  size: string;
  allocated: string;
  free: string;
  frag: string;
  cap?: string;
  dedup?: string;
  health: string;
  altroot: string;
  vdevType: string;
  scrubStatus: string;
  devices?: ZfsPoolDevice[];
  errors?: string;
  statusProse?: string;
  actionProse?: string;
}

export interface ZfsDatasetItem {
  name: string;
  used: string;
  avail: string;
  refer: string;
  mount: string;
}

export interface ZpoolStatusDetail {
  name: string;
  state: string;
  scan: string;
  statusProse?: string;
  actionProse?: string;
  errors?: string;
  vdevType: string;
  devices: ZfsPoolDevice[];
}

const PROSE_KEYWORDS = new Set([
  'status:',
  'action:',
  'scan:',
  'pool:',
  'state:',
  'errors:',
  'config:',
  'see:',
  'the',
  'some',
  'all',
  'one',
  'please',
  'scrub',
  'resilver',
  'read',
  'write',
  'cksum',
]);

function isValidPoolName(name: string): boolean {
  if (!name) return false;
  if (PROSE_KEYWORDS.has(name.toLowerCase())) return false;
  return /^[a-zA-Z][a-zA-Z0-9_\-\.:]*$/.test(name);
}

export function formatScrubStatus(scan: string): string {
  if (!scan || scan === 'none requested') return 'None requested';
  if (/scrub in progress/i.test(scan)) {
    const matchDone = scan.match(/(\d+\.?\d*%\s+done)/i);
    const matchToGo = scan.match(/(\d+:\d+:\d+\s+to\s+go)/i);
    if (matchDone && matchToGo) {
      return `In progress (${matchDone[1]}, ${matchToGo[1]})`;
    }
    if (matchDone) return `In progress (${matchDone[1]})`;
    return 'Scrub in progress';
  }
  if (/scrub repaired/i.test(scan)) {
    const match = scan.match(/scrub repaired (\S+) (?:in [^ ]+ )?with (\d+ errors)/i);
    if (match) {
      return `repaired ${match[1]}, ${match[2]}`;
    }
  }
  return scan;
}

function finalizePool(pool: ZpoolStatusDetail) {
  if (!pool.devices || pool.devices.length === 0) {
    pool.vdevType = 'ZFS Storage Pool';
    return;
  }

  // Group devices by role
  const roles = new Map<string, string[]>();
  for (const dev of pool.devices) {
    const role = dev.role || 'data';
    const list = roles.get(role) || [];
    list.push(dev.name);
    roles.set(role, list);
  }

  const parts: string[] = [];
  for (const [role, devList] of roles.entries()) {
    if (role === 'stripe' || role === 'data') {
      if (devList.length === 1) {
        parts.push(`single disk (${devList[0]})`);
      } else {
        parts.push(`stripe (${devList.join(', ')})`);
      }
    } else {
      parts.push(`${role} (${devList.join(', ')})`);
    }
  }

  pool.vdevType = parts.join(', ');
}

export function parseZpoolList(raw?: string): ZfsPoolItem[] {
  if (!raw || typeof raw !== 'string') return [];
  const trimmed = raw.trim();
  if (
    trimmed === '' ||
    trimmed.includes('NO_ZPOOL_LIST') ||
    trimmed.includes('no pools available') ||
    trimmed.includes('command not found')
  ) {
    return [];
  }

  const lines = trimmed.split('\n');
  const pools: ZfsPoolItem[] = [];

  for (const line of lines) {
    const trimmedLine = line.trim();
    if (!trimmedLine) continue;
    if (trimmedLine.startsWith('NAME')) continue;

    const tokens = trimmedLine.split(/\s+/);
    if (tokens.length < 4) continue;

    const name = tokens[0];
    if (!isValidPoolName(name)) continue;

    let size = '-';
    let allocated = '-';
    let free = '-';
    let frag = '-';
    let cap = '-';
    let dedup = '1.00x';
    let health = 'ONLINE';
    let altroot = '-';

    if (tokens.length >= 11) {
      // NAME SIZE ALLOC FREE CKPOINT EXPANDSZ FRAG CAP DEDUP HEALTH ALTROOT
      size = tokens[1];
      allocated = tokens[2];
      free = tokens[3];
      frag = tokens[6];
      cap = tokens[7];
      dedup = tokens[8];
      health = tokens[9];
      altroot = tokens[10];
    } else if (tokens.length === 10) {
      // NAME SIZE ALLOC FREE EXPANDSZ FRAG CAP DEDUP HEALTH ALTROOT
      size = tokens[1];
      allocated = tokens[2];
      free = tokens[3];
      frag = tokens[5];
      cap = tokens[6];
      dedup = tokens[7];
      health = tokens[8];
      altroot = tokens[9];
    } else if (tokens.length === 9) {
      // NAME SIZE ALLOC FREE FRAG CAP DEDUP HEALTH ALTROOT
      size = tokens[1];
      allocated = tokens[2];
      free = tokens[3];
      frag = tokens[4];
      cap = tokens[5];
      dedup = tokens[6];
      health = tokens[7];
      altroot = tokens[8];
    } else {
      size = tokens[1] || '-';
      allocated = tokens[2] || '-';
      free = tokens[3] || '-';
      const healthToken = tokens.find(t =>
        ['ONLINE', 'DEGRADED', 'FAULTED', 'OFFLINE', 'UNAVAIL', 'REMOVED', 'SUSPENDED'].includes(t.toUpperCase())
      );
      if (healthToken) health = healthToken.toUpperCase();
      altroot = tokens[tokens.length - 1] || '-';
    }

    pools.push({
      name,
      size,
      allocated,
      free,
      frag,
      cap,
      dedup,
      health,
      altroot,
      vdevType: 'ZFS Storage Pool',
      scrubStatus: 'None requested',
    });
  }

  return pools;
}

export function parseZpoolStatus(raw?: string): Map<string, ZpoolStatusDetail> {
  const map = new Map<string, ZpoolStatusDetail>();
  if (!raw || typeof raw !== 'string') return map;
  const trimmed = raw.trim();
  if (
    trimmed === '' ||
    trimmed.includes('NO_ZPOOL') ||
    trimmed.includes('no pools available') ||
    trimmed.includes('command not found')
  ) {
    return map;
  }

  const lines = raw.split('\n');
  let currentPool: ZpoolStatusDetail | null = null;
  let currentSection: 'header' | 'status' | 'action' | 'scan' | 'config' | 'errors' = 'header';
  let currentRole = '';

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const trimmedLine = line.trim();
    if (!trimmedLine) continue;

    const poolMatch = trimmedLine.match(/^pool:\s+([a-zA-Z0-9_\-\.:]+)/);
    if (poolMatch) {
      if (currentPool) {
        finalizePool(currentPool);
        map.set(currentPool.name, currentPool);
      }
      currentPool = {
        name: poolMatch[1],
        state: 'ONLINE',
        scan: 'None requested',
        vdevType: 'ZFS Storage Pool',
        devices: [],
      };
      currentSection = 'header';
      currentRole = '';
      continue;
    }

    if (!currentPool) continue;

    const stateMatch = trimmedLine.match(/^state:\s+([a-zA-Z0-9_\-]+)/);
    if (stateMatch) {
      currentPool.state = stateMatch[1];
      continue;
    }

    const statusMatch = trimmedLine.match(/^status:\s+(.*)$/);
    if (statusMatch) {
      currentSection = 'status';
      currentPool.statusProse = statusMatch[1].trim();
      continue;
    }

    const actionMatch = trimmedLine.match(/^action:\s+(.*)$/);
    if (actionMatch) {
      currentSection = 'action';
      currentPool.actionProse = actionMatch[1].trim();
      continue;
    }

    if (trimmedLine.startsWith('see:')) {
      continue;
    }

    const scanMatch = trimmedLine.match(/^scan:\s+(.*)$/);
    if (scanMatch) {
      currentSection = 'scan';
      currentPool.scan = scanMatch[1].trim();
      continue;
    }

    if (trimmedLine.startsWith('config:')) {
      currentSection = 'config';
      currentRole = '';
      continue;
    }

    const errorsMatch = trimmedLine.match(/^errors:\s+(.*)$/);
    if (errorsMatch) {
      currentSection = 'errors';
      currentPool.errors = errorsMatch[1].trim();
      continue;
    }

    if (currentSection === 'status' && (line.startsWith('\t') || (line.startsWith(' ') && !trimmedLine.includes(':')))) {
      currentPool.statusProse = (currentPool.statusProse ? currentPool.statusProse + ' ' : '') + trimmedLine;
      continue;
    }

    if (currentSection === 'action' && (line.startsWith('\t') || (line.startsWith(' ') && !trimmedLine.includes(':')))) {
      currentPool.actionProse = (currentPool.actionProse ? currentPool.actionProse + ' ' : '') + trimmedLine;
      continue;
    }

    if (currentSection === 'scan') {
      currentPool.scan = (currentPool.scan ? currentPool.scan + ' ' : '') + trimmedLine;
      continue;
    }

    if (currentSection === 'config') {
      if (trimmedLine.startsWith('NAME') && trimmedLine.includes('STATE')) {
        continue;
      }

      const lower = trimmedLine.toLowerCase();
      if (['logs', 'cache', 'spares', 'spare', 'dedup', 'special'].includes(lower)) {
        currentRole = lower.startsWith('spare') ? 'spare' : lower;
        continue;
      }

      const tokens = trimmedLine.split(/\s+/);
      const name = tokens[0];
      const state = tokens[1] || 'ONLINE';

      if (name === currentPool.name) {
        continue;
      }

      if (/^(mirror|raidz|draid)/i.test(name)) {
        currentRole = name;
        continue;
      }

      let devName = name;
      if (devName.startsWith('/dev/')) {
        const parts = devName.split('/');
        devName = parts[parts.length - 1];
      }

      currentPool.devices.push({
        name: devName,
        state,
        role: currentRole || 'stripe',
        read: tokens[2],
        write: tokens[3],
        cksum: tokens[4],
      });
    }
  }

  if (currentPool) {
    finalizePool(currentPool);
    map.set(currentPool.name, currentPool);
  }

  return map;
}

export function parseZfsPools(zpoolListRaw?: string, zpoolStatusRaw?: string): ZfsPoolItem[] {
  const pools = parseZpoolList(zpoolListRaw);
  const statusMap = parseZpoolStatus(zpoolStatusRaw);

  if (pools.length > 0) {
    for (const pool of pools) {
      const status = statusMap.get(pool.name);
      if (status) {
        if (status.state) {
          pool.health = status.state;
        }
        if (status.vdevType && status.vdevType !== 'ZFS Storage Pool') {
          pool.vdevType = status.vdevType;
        }
        pool.scrubStatus = formatScrubStatus(status.scan);
        pool.devices = status.devices;
        pool.errors = status.errors;
        pool.statusProse = status.statusProse;
        pool.actionProse = status.actionProse;
      }
    }
    return pools;
  }

  if (statusMap.size > 0) {
    const fallbackPools: ZfsPoolItem[] = [];
    for (const status of statusMap.values()) {
      fallbackPools.push({
        name: status.name,
        size: '-',
        allocated: '-',
        free: '-',
        frag: '-',
        cap: '-',
        dedup: '1.00x',
        health: status.state || 'ONLINE',
        altroot: '-',
        vdevType: status.vdevType || 'ZFS Storage Pool',
        scrubStatus: formatScrubStatus(status.scan),
        devices: status.devices,
        errors: status.errors,
        statusProse: status.statusProse,
        actionProse: status.actionProse,
      });
    }
    return fallbackPools;
  }

  return [];
}

export function parseZfsDatasets(raw?: string): ZfsDatasetItem[] {
  if (!raw || typeof raw !== 'string') return [];
  const trimmed = raw.trim();
  if (
    trimmed === '' ||
    trimmed.includes('NO_ZFS') ||
    trimmed.includes('no datasets available') ||
    trimmed.includes('command not found')
  ) {
    return [];
  }

  const lines = trimmed.split('\n');
  const datasets: ZfsDatasetItem[] = [];

  for (const line of lines) {
    const trimmedLine = line.trim();
    if (!trimmedLine) continue;
    if (trimmedLine.startsWith('NAME')) continue;

    const tokens = trimmedLine.split(/\s+/);
    if (tokens.length >= 5) {
      const name = tokens[0];
      const rootPart = name.split('/')[0];
      if (!isValidPoolName(rootPart)) continue;

      datasets.push({
        name,
        used: tokens[1],
        avail: tokens[2],
        refer: tokens[3],
        mount: tokens[4],
      });
    }
  }

  return datasets;
}
