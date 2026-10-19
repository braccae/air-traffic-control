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
      if (['logs', 'log', 'cache', 'spares', 'spare', 'dedup', 'special'].includes(lower)) {
        currentRole = lower.startsWith('spare') ? 'spare' : lower.startsWith('log') ? 'logs' : lower;
        continue;
      }

      const tokens = trimmedLine.split(/\s+/);
      const name = tokens[0];
      const state = tokens[1] || 'ONLINE';

      if (name === currentPool.name) {
        continue;
      }

      if (/^(mirror|raidz|draid|replacing|spare)/i.test(name)) {
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

export interface StorageMountItem {
  target: string;
  source: string;
  fstype: string;
  size?: string;
  used?: string;
  avail?: string;
  options?: string;
}

export interface BtrfsDevice {
  devid: string;
  size?: string;
  used?: string;
  path: string;
}

export interface BtrfsFilesystem {
  label: string;
  uuid: string;
  totalDevices: number;
  fsBytesUsed?: string;
  devices: BtrfsDevice[];
  mountpoints?: string[];
}

export type StorageEngineType = 'zfs' | 'btrfs' | 'lvm_ext' | 'none';

export function parseStorageMounts(raw?: string): StorageMountItem[] {
  if (!raw || typeof raw !== 'string') return [];
  const trimmed = raw.trim();
  if (!trimmed || trimmed === 'NO_FINDMNT' || trimmed.includes('command not found')) {
    return [];
  }

  // Attempt JSON parsing first (from `findmnt -J`)
  if (trimmed.startsWith('{') || trimmed.startsWith('[')) {
    try {
      const parsed = JSON.parse(trimmed);
      const list = Array.isArray(parsed)
        ? parsed
        : Array.isArray(parsed?.filesystems)
        ? parsed.filesystems
        : [];

      return list
        .map((item: any) => ({
          target: String(item.target || item.TARGET || ''),
          source: String(item.source || item.SOURCE || ''),
          fstype: String(item.fstype || item.FSTYPE || ''),
          size: item.size || item.SIZE ? String(item.size || item.SIZE) : undefined,
          used: item.used || item.USED ? String(item.used || item.USED) : undefined,
          avail: item.avail || item.AVAIL ? String(item.avail || item.AVAIL) : undefined,
          options: item.options || item.OPTIONS ? String(item.options || item.OPTIONS) : undefined,
        }))
        .filter((item: StorageMountItem) => item.target || item.source);
    } catch {
      // Fallback to text parsing
    }
  }

  // Fallback: parse plain text findmnt table
  const lines = trimmed.split('\n');
  const results: StorageMountItem[] = [];

  for (const line of lines) {
    const trimmedLine = line.trim();
    if (!trimmedLine || trimmedLine.startsWith('TARGET') || trimmedLine.startsWith('---')) continue;

    const tokens = trimmedLine.split(/\s+/);
    if (tokens.length >= 3) {
      results.push({
        target: tokens[0],
        source: tokens[1],
        fstype: tokens[2],
        options: tokens[3],
        size: tokens[4],
        used: tokens[5],
        avail: tokens[6],
      });
    }
  }

  return results;
}

export function parseBtrfsFilesystems(btrfsRaw?: string, findmntRaw?: string): BtrfsFilesystem[] {
  const mounts = parseStorageMounts(findmntRaw);
  const filesystems: BtrfsFilesystem[] = [];

  if (btrfsRaw && typeof btrfsRaw === 'string') {
    const trimmed = btrfsRaw.trim();
    if (
      trimmed &&
      !trimmed.includes('NO_BTRFS') &&
      !trimmed.includes('command not found') &&
      !trimmed.includes('ERROR: not a btrfs filesystem')
    ) {
      const lines = trimmed.split('\n');
      let currentFs: BtrfsFilesystem | null = null;

      for (const line of lines) {
        const trimmedLine = line.trim();
        if (!trimmedLine) continue;

        // Label: 'data-vault' uuid: ... or Label: none uuid: ...
        const labelMatch = trimmedLine.match(/^Label:\s*(?:'([^']*)'|"([^"]*)"|(\S+))\s+uuid:\s*([a-fA-F0-9\-]+)/);
        if (labelMatch) {
          if (currentFs) {
            filesystems.push(currentFs);
          }
          currentFs = {
            label: labelMatch[1] ?? labelMatch[2] ?? labelMatch[3] ?? 'none',
            uuid: labelMatch[4],
            totalDevices: 1,
            devices: [],
            mountpoints: [],
          };
          continue;
        }

        if (!currentFs) continue;

        // Total devices 2 FS bytes used 609.81GiB
        const devTotalMatch = trimmedLine.match(/Total devices\s+(\d+)(?:\s+FS bytes used\s+(\S+))?/i);
        if (devTotalMatch) {
          currentFs.totalDevices = parseInt(devTotalMatch[1], 10);
          if (devTotalMatch[2]) {
            currentFs.fsBytesUsed = devTotalMatch[2];
          }
          continue;
        }

        // devid 1 size 1.00TiB used 400.00GiB path /dev/sdb1
        const devMatch = trimmedLine.match(/devid\s+(\d+)\s+size\s+(\S+)\s+used\s+(\S+)\s+path\s+(\S+)/);
        if (devMatch) {
          currentFs.devices.push({
            devid: devMatch[1],
            size: devMatch[2],
            used: devMatch[3],
            path: devMatch[4],
          });
          continue;
        }
      }

      if (currentFs) {
        filesystems.push(currentFs);
      }
    }
  }

  // Associate mountpoints from findmnt with parsed filesystems
  if (filesystems.length > 0 && mounts.length > 0) {
    for (const fs of filesystems) {
      const matchedMounts = mounts.filter(m => {
        if (m.fstype !== 'btrfs') return false;
        // Matches if source starts with device path (e.g. /dev/sdb1[/@data])
        if (fs.devices.some(d => m.source.startsWith(d.path))) return true;
        // If single btrfs filesystem exists, any btrfs mount belongs to it
        if (filesystems.length === 1) return true;
        // Check uuid/label in source
        if (fs.label !== 'none' && m.source.includes(fs.label)) return true;
        if (m.source.includes(fs.uuid)) return true;
        return false;
      });

      const uniqueMounts = Array.from(new Set(matchedMounts.map(m => m.target)));
      fs.mountpoints = uniqueMounts;
    }
    return filesystems;
  }

  if (filesystems.length > 0) {
    return filesystems;
  }

  // Fallback: If btrfs command gave nothing (e.g. unprivileged user), derive from findmnt if btrfs mounts exist
  const btrfsMounts = mounts.filter(m => m.fstype === 'btrfs');
  if (btrfsMounts.length > 0) {
    const deviceGroups = new Map<string, string[]>();
    for (const m of btrfsMounts) {
      const baseDev = m.source.replace(/\[.*\]$/, '');
      const list = deviceGroups.get(baseDev) || [];
      if (!list.includes(m.target)) {
        list.push(m.target);
      }
      deviceGroups.set(baseDev, list);
    }

    const derived: BtrfsFilesystem[] = [];
    let idx = 1;
    for (const [devPath, mountList] of deviceGroups.entries()) {
      derived.push({
        label: 'none',
        uuid: `derived-btrfs-${idx++}`,
        totalDevices: 1,
        devices: [
          {
            devid: '1',
            path: devPath,
          },
        ],
        mountpoints: mountList,
      });
    }
    return derived;
  }

  return [];
}

export function detectStorageEngine(
  zpoolListRaw?: string,
  btrfsRaw?: string,
  findmntRaw?: string
): StorageEngineType {
  const pools = parseZfsPools(zpoolListRaw);
  if (pools.length > 0) {
    return 'zfs';
  }

  const btrfsFs = parseBtrfsFilesystems(btrfsRaw, findmntRaw);
  if (btrfsFs.length > 0) {
    return 'btrfs';
  }

  const mounts = parseStorageMounts(findmntRaw);
  if (mounts.some(m => m.fstype === 'btrfs')) {
    return 'btrfs';
  }

  const hasLvmOrExt = mounts.some(m =>
    ['ext4', 'ext3', 'ext2', 'xfs'].includes(m.fstype) ||
    m.source.includes('/dev/mapper/') ||
    m.source.includes('/dev/dm-')
  );
  if (hasLvmOrExt) {
    return 'lvm_ext';
  }

  return 'none';
}

