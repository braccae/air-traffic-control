import type {
  ContainerItem,
  PodItem,
  ServiceUser,
  StandaloneDaemon,
  UnprivilegedPodmanUser,
} from '../types/index.ts';
import { filterContainersByStatus } from './orchestrator.ts';

export interface CorrelatedServiceIdentity extends ServiceUser {
  uid: number;
  isRunningPodman: boolean;
  containerCount: number;
  subUidRange?: string;
  linger?: boolean;
}

export interface UserContainerGroup {
  user: string;
  uid: number;
  rootless: boolean;
  badge: string;
  totalContainers: number;
  runningContainers: number;
  stoppedContainers: number;
  containers: ContainerItem[];
}

export interface SecurityBadgeInfo {
  isRootful: boolean;
  label: string;
  rootless: boolean;
}

export interface DockerProbeResult {
  hasDocker: boolean;
  dockerContainers: ContainerItem[];
}

/**
 * Returns security classification and badge label for a container or pod.
 * If user === 'root', uid === 0, or rootless === false, it is "Rootful / System".
 * Otherwise it is "Rootless (UID <uid>)".
 */
export function getContainerSecurityBadge(
  user?: string,
  uid?: number,
  rootless?: boolean
): SecurityBadgeInfo {
  const isRootful = !rootless && (user === 'root' || uid === 0 || (!user && uid === undefined));
  if (isRootful) {
    return {
      isRootful: true,
      label: 'Rootful / System',
      rootless: false,
    };
  }
  const effectiveUid = uid ?? (user === 'root' ? 0 : 1000);
  return {
    isRootful: false,
    label: `Rootless (UID ${effectiveUid})`,
    rootless: true,
  };
}

/**
 * Groups unpodded/standalone containers by user, placing 'root' first and
 * sorting other users alphabetically, while computing total, running, and stopped counts.
 */
export function groupStandaloneContainersByUser(
  containers: ContainerItem[]
): UserContainerGroup[] {
  const map = new Map<string, ContainerItem[]>();

  for (const c of containers) {
    const username = c.user || (c.uid === 0 ? 'root' : 'core');
    if (!map.has(username)) {
      map.set(username, []);
    }
    map.get(username)!.push(c);
  }

  const groups: UserContainerGroup[] = [];

  for (const [user, uContainers] of map.entries()) {
    const first = uContainers[0];
    const uid = first?.uid ?? (user === 'root' ? 0 : 1000);
    const rootless = user !== 'root' && uid !== 0 && first?.rootless !== false;
    const badgeInfo = getContainerSecurityBadge(user, uid, rootless);

    let running = 0;
    let stopped = 0;
    for (const c of uContainers) {
      const s = (c.status || '').toLowerCase();
      if (s.includes('running') || s.startsWith('up ')) {
        running++;
      } else {
        stopped++;
      }
    }

    groups.push({
      user,
      uid,
      rootless: !badgeInfo.isRootful,
      badge: badgeInfo.label,
      totalContainers: uContainers.length,
      runningContainers: running,
      stoppedContainers: stopped,
      containers: uContainers,
    });
  }

  groups.sort((a, b) => {
    if (a.user === 'root' && b.user !== 'root') return -1;
    if (b.user === 'root' && a.user !== 'root') return 1;
    return a.user.localeCompare(b.user);
  });

  return groups;
}

/**
 * Filters each user container group by status ('all' | 'running' | 'stopped')
 * and omits groups with 0 matching containers when filtering by 'running' or 'stopped'.
 */
export function filterUserContainerGroups(
  groups: UserContainerGroup[],
  filter: 'all' | 'running' | 'stopped'
): UserContainerGroup[] {
  if (filter === 'all') {
    return groups;
  }

  return groups
    .map(group => {
      const filtered = filterContainersByStatus(group.containers, filter);
      let running = 0;
      let stopped = 0;
      for (const c of filtered) {
        const s = (c.status || '').toLowerCase();
        if (s.includes('running') || s.startsWith('up ')) {
          running++;
        } else {
          stopped++;
        }
      }
      return {
        ...group,
        totalContainers: filtered.length,
        runningContainers: running,
        stoppedContainers: stopped,
        containers: filtered,
      };
    })
    .filter(g => g.containers.length > 0);
}

/**
 * Parses raw Docker ps output (e.g. from `docker ps --format "{{.ID}}:::{{.Names}}:::{{.Image}}:::{{.Status}}"`)
 * and CLI detection to return whether Docker is present and the list of Docker containers.
 */
export function parseDockerPsOutput(
  rawPsOutput: string,
  cliCheckOutput?: string
): DockerProbeResult {
  if (
    (cliCheckOutput && cliCheckOutput.toLowerCase().includes('podman')) ||
    (rawPsOutput && rawPsOutput.toLowerCase().includes('emulate docker cli using podman'))
  ) {
    return {
      hasDocker: false,
      dockerContainers: [],
    };
  }

  const isCliFound = cliCheckOutput
    ? !cliCheckOutput.includes('NO_DOCKER') && cliCheckOutput.trim().length > 0
    : !rawPsOutput.includes('NO_DOCKER') && rawPsOutput.trim().length > 0;

  const containers: ContainerItem[] = [];
  const lines = (rawPsOutput || '').split('\n');

  for (const rawLine of lines) {
    const line = rawLine.trim();
    if (!line.includes(':::') || line.includes('{{')) continue;
    const parts = line.split(':::');
    if (parts.length < 4) continue;
    const [id, name, image, status, composeProject, composeService, composeConfigFiles] = parts;
    if (!id || !name) continue;
    const nameLower = name.toLowerCase();
    const imgLower = (image || '').toLowerCase();
    if (
      nameLower.includes('pause') ||
      nameLower.endsWith('-infra') ||
      imgLower.includes('pause') ||
      imgLower.includes('podman-pause')
    ) {
      continue;
    }
    const item: ContainerItem = {
      id: id.slice(0, 12),
      name,
      image: image || 'docker/image:latest',
      status: status || 'Running',
      cpu: '0.2%',
      memory: '48 MiB',
      user: 'docker',
      uid: 0,
      rootless: false,
    };
    if (composeProject?.trim()) item.composeProject = composeProject.trim();
    if (composeService?.trim()) item.composeService = composeService.trim();
    if (composeConfigFiles?.trim()) item.composeConfigFiles = composeConfigFiles.trim();
    containers.push(item);
  }

  const hasDocker = Boolean(isCliFound || containers.length > 0);
  return {
    hasDocker,
    dockerContainers: containers,
  };
}

/**
 * Separates a list of containers into pods (containers belonging to a pod)
 * and standalone containers (unpodded containers).
 */
export function calculatePodTotals(containers: ContainerItem[]): { totalCpu: string; totalRam: string } {
  let cpuSum = 0;
  let hasCpu = false;
  let memBytes = 0;
  let hasMem = false;

  for (const c of containers) {
    if (c.cpu && c.cpu.trim() !== '' && c.cpu !== '-') {
      const num = Number.parseFloat(c.cpu.replace('%', '').trim());
      if (!Number.isNaN(num)) {
        cpuSum += num;
        hasCpu = true;
      }
    }
    if (c.memory && c.memory.trim() !== '' && c.memory !== '-') {
      const b = parseMemoryStringToBytes(c.memory);
      if (b > 0) {
        memBytes += b;
        hasMem = true;
      }
    }
  }

  const totalCpu = hasCpu ? `${cpuSum.toFixed(2)}%` : '';
  let totalRam = '';
  if (hasMem) {
    if (memBytes >= 1024 * 1024 * 1024) {
      totalRam = `${(memBytes / (1024 * 1024 * 1024)).toFixed(2)} GiB`;
    } else if (memBytes >= 1024 * 1024) {
      totalRam = `${(memBytes / (1024 * 1024)).toFixed(1)} MiB`;
    } else if (memBytes >= 1024) {
      totalRam = `${(memBytes / 1024).toFixed(0)} KiB`;
    } else {
      totalRam = `${memBytes.toFixed(0)} B`;
    }
  }

  return { totalCpu, totalRam };
}

export function parseMemoryStringToBytes(mem: string): number {
  const clean = mem.includes('/') ? mem.split('/')[0].trim() : mem.trim();
  const match = clean.match(/^([0-9.]+)\s*([a-zA-Z]+)?$/);
  if (!match) return 0;
  const num = Number.parseFloat(match[1]);
  if (Number.isNaN(num)) return 0;
  const unit = (match[2] || '').toLowerCase();
  if (unit.startsWith('g')) return num * 1024 * 1024 * 1024;
  if (unit.startsWith('m')) return num * 1024 * 1024;
  if (unit.startsWith('k')) return num * 1024;
  return num;
}

export function separatePodsAndStandaloneContainers(
  containers: ContainerItem[],
  existingPods: PodItem[] = []
): { pods: PodItem[]; standaloneContainers: ContainerItem[] } {
  const podMap = new Map<string, PodItem>();
  for (const p of existingPods) {
    podMap.set(p.id, { ...p, containers: [...p.containers] });
    podMap.set(p.name, podMap.get(p.id)!);
    const cleanName = p.name.replace(/^pod:\s*/, '');
    if (cleanName !== p.name) {
      podMap.set(cleanName, podMap.get(p.id)!);
    }
  }

  const standalone: ContainerItem[] = [];

  for (const c of containers) {
    const rawPodId = c.podId || (c as any).Pod || '';
    const rawPodName = (c as any).podName || (c as any).PodName || '';
    const podIdentifier = rawPodId || rawPodName;

    if (podIdentifier && String(podIdentifier).trim() !== '') {
      const pid = String(rawPodId || rawPodName).trim();
      const pname = String(rawPodName || rawPodId).trim();
      let matchedPod = podMap.get(pid) || podMap.get(pname);
      if (!matchedPod) {
        for (const existing of podMap.values()) {
          const matchId = pid && (existing.id.startsWith(pid) || pid.startsWith(existing.id));
          const matchName = pname && (
            existing.name === pname ||
            existing.name === `pod: ${pname}` ||
            existing.name.replace(/^pod:\s*/, '') === pname.replace(/^pod:\s*/, '')
          );
          if (matchId || matchName) {
            matchedPod = existing;
            break;
          }
        }
      }

      if (!matchedPod) {
        matchedPod = {
          id: pid,
          name: pname.startsWith('pod:') ? pname : `pod: ${pname}`,
          containersRunningCount: 0,
          ports: c.ports || 'Host Mesh',
          totalCpu: c.cpu || '',
          totalRam: c.memory || '',
          user: c.user || 'root',
          uid: c.uid ?? 0,
          rootless: c.rootless ?? (c.user !== 'root' && (c.uid ?? 0) !== 0),
          containers: [],
        };
        podMap.set(pid, matchedPod);
        podMap.set(pname, matchedPod);
      }
      if (!matchedPod.containers.some(existing => existing.id === c.id || existing.name === c.name)) {
        matchedPod.containers.push(c);
        const s = (c.status || '').toLowerCase();
        if (s.includes('running') || s.startsWith('up ')) {
          matchedPod.containersRunningCount++;
        }
      }
    } else {
      standalone.push(c);
    }
  }

  const uniquePods = Array.from(new Set(podMap.values()));
  for (const p of uniquePods) {
    const totals = calculatePodTotals(p.containers);
    if (!p.totalCpu && totals.totalCpu) p.totalCpu = totals.totalCpu;
    if (!p.totalRam && totals.totalRam) p.totalRam = totals.totalRam;
  }

  return {
    pods: uniquePods,
    standaloneContainers: standalone,
  };
}

/**
 * Filters each pod's containers by status ('all' | 'running' | 'stopped')
 * and omits pods with 0 matching containers when filtering by 'running' or 'stopped'.
 */
export function filterPodsByStatus(
  pods: PodItem[],
  filter: 'all' | 'running' | 'stopped'
): PodItem[] {
  if (filter === 'all') {
    return pods;
  }

  return pods
    .map(pod => {
      const filteredContainers = filterContainersByStatus(pod.containers, filter);
      return {
        ...pod,
        containers: filteredContainers,
      };
    })
    .filter(pod => pod.containers.length > 0);
}

/**
 * Filters standalone daemons by 'all', 'running', or 'stopped' (matching 'exited').
 */
export function filterStandaloneDaemons(
  daemons: StandaloneDaemon[],
  filter: 'all' | 'running' | 'stopped'
): StandaloneDaemon[] {
  if (filter === 'all') {
    return daemons;
  }
  if (filter === 'running') {
    return daemons.filter(d => d.status === 'running');
  }
  return daemons.filter(d => d.status !== 'running');
}

/**
 * Computes accurate counts of all, running, and stopped containers across pods and standalone daemons.
 */
export function countPodmanStatuses(
  pods: PodItem[],
  daemons: (StandaloneDaemon | ContainerItem)[] = [],
  dockerContainers: ContainerItem[] = []
): { all: number; running: number; stopped: number; docker?: number } {
  let running = 0;
  let stopped = 0;

  for (const pod of pods) {
    for (const c of pod.containers) {
      const s = c.status.toLowerCase();
      if (s.includes('running') || s.startsWith('up ')) {
        running++;
      } else {
        stopped++;
      }
    }
  }

  for (const d of daemons) {
    const s = d.status.toLowerCase();
    if (s === 'running' || s.startsWith('up ')) {
      running++;
    } else {
      stopped++;
    }
  }

  const result: { all: number; running: number; stopped: number; docker?: number } = {
    all: running + stopped,
    running,
    stopped,
  };

  if (dockerContainers && dockerContainers.length > 0) {
    result.docker = dockerContainers.length;
    if (pods.length === 0 && daemons.length === 0) {
      for (const dc of dockerContainers) {
        const s = (dc.status || '').toLowerCase();
        if (s.includes('running') || s.startsWith('up ')) {
          running++;
        } else {
          stopped++;
        }
      }
      result.all = running + stopped;
      result.running = running;
      result.stopped = stopped;
    }
  }

  return result;
}

/**
 * Enriches and correlates ServiceUser entries with probed unprivileged Podman users,
 * identifying which users are actively running rootless Podman containers and verifying UIDs (> 0) and container counts.
 * Also appends any newly discovered live unprivileged users that weren't in the static list.
 */
export function correlateIdentitiesWithProbe(
  identities: ServiceUser[],
  probedUsers: UnprivilegedPodmanUser[]
): CorrelatedServiceIdentity[] {
  const probeByUser = new Map<string, UnprivilegedPodmanUser>();
  for (const p of probedUsers) {
    if (p.uid > 0 && p.user !== 'root') {
      probeByUser.set(p.user, p);
    }
  }

  const seenUsers = new Set<string>();
  const results: CorrelatedServiceIdentity[] = identities.map(identity => {
    seenUsers.add(identity.user);
    const parsedUid = Number.parseInt(identity.uidGid.split(':')[0], 10);
    const matched = probeByUser.get(identity.user);
    const uid = matched ? matched.uid : Number.isNaN(parsedUid) ? 0 : parsedUid;
    const containerCount = matched && uid > 0 ? matched.containers.length : 0;

    return {
      ...identity,
      uid,
      isRunningPodman: uid > 0 && containerCount > 0,
      containerCount,
      subUidRange: matched?.subUidRange,
      linger: matched?.linger,
    };
  });

  for (const [user, p] of probeByUser.entries()) {
    if (!seenUsers.has(user) && p.containers.length > 0) {
      results.unshift({
        user: p.user,
        uidGid: `${p.uid}:${p.gid || p.uid}`,
        rolesGroups: 'podman, rootless-containers',
        authMechanism: p.subUidRange ? `Rootless SubUID ${p.subUidRange}` : 'Local Rootless Runtime',
        lingerSubUid: p.linger ? 'Linger: Enabled' : `SubUID: ${p.subUidRange || 'active'}`,
        icon: 'deployed_code',
        uid: p.uid,
        isRunningPodman: true,
        containerCount: p.containers.length,
        subUidRange: p.subUidRange,
        linger: p.linger,
      });
    }
  }

  return results;
}

/**
 * Merges live probed unprivileged Podman users into the PodmanManager pods list so real host containers are displayed.
 */
export function mergeLiveProbeIntoPods(
  defaultPods: PodItem[],
  probedUsers: UnprivilegedPodmanUser[]
): PodItem[] {
  if (!probedUsers || probedUsers.length === 0) {
    return defaultPods;
  }

  const podMap = new Map<string, PodItem>();
  for (const p of defaultPods) {
    podMap.set(p.id, { ...p, containers: [...p.containers] });
    podMap.set(p.name, podMap.get(p.id)!);
    const cleanName = p.name.replace(/^pod:\s*/, '');
    if (cleanName !== p.name) {
      podMap.set(cleanName, podMap.get(p.id)!);
    }
  }

  const livePods: PodItem[] = [];

  for (const u of probedUsers) {
    if (u.uid < 0) continue;
    if ((!u.containers || u.containers.length === 0) && (!u.pods || u.pods.length === 0)) continue;

    // 1. Explicit user pods (u.pods)
    if (u.pods && u.pods.length > 0) {
      for (const p of u.pods) {
        const podId = p.id;
        const podName = p.name.startsWith('pod:') ? p.name : `pod: ${p.name}`;
        let matched = podMap.get(podId) || podMap.get(p.name);
        if (!matched) {
          for (const existing of podMap.values()) {
            const matchId = podId && (existing.id.startsWith(podId) || podId.startsWith(existing.id));
            const matchName = p.name && (
              existing.name === p.name ||
              existing.name === podName ||
              existing.name.replace(/^pod:\s*/, '') === p.name.replace(/^pod:\s*/, '')
            );
            if (matchId || matchName) {
              matched = existing;
              break;
            }
          }
        }

        if (!matched) {
          matched = {
            ...p,
            name: podName,
            user: u.user,
            uid: u.uid,
            rootless: u.user !== 'root' && u.uid !== 0,
            containers: [...(p.containers || [])],
          };
          podMap.set(podId, matched);
          podMap.set(p.name, matched);
          livePods.push(matched);
        }

        // Attach any user containers belonging to this pod
        for (const c of u.containers || []) {
          const cPodId = c.podId || (c as any).Pod;
          const cPodName = c.podName || (c as any).PodName;
          const matchId = cPodId && (podId.startsWith(cPodId) || cPodId.startsWith(podId));
          const matchName = (cPodName && (cPodName === p.name || cPodName === podName)) ||
                            (cPodId && (cPodId === p.name || cPodId === podName));
          if (matchId || matchName) {
            if (!matched.containers.some(ec => ec.id === c.id || ec.name === c.name)) {
              matched.containers.push({
                ...c,
                user: u.user,
                uid: u.uid,
                rootless: u.user !== 'root' && u.uid !== 0,
              });
            }
          }
        }
        matched.containersRunningCount = matched.containers.filter(c => {
          const s = (c.status || '').toLowerCase();
          return s.includes('running') || s.startsWith('up ');
        }).length;
      }
    }

    // 2. Containers with podId or podName that match existing pods
    for (const c of u.containers || []) {
      const pId = c.podId || (c as any).Pod;
      const pName = c.podName || (c as any).PodName;
      if (pId || pName) {
        let matched = (pId ? podMap.get(pId) : null) || (pName ? podMap.get(pName) : null);
        if (!matched) {
          for (const existing of podMap.values()) {
            const matchId = pId && (existing.id.startsWith(pId) || pId.startsWith(existing.id));
            const matchName = pName && (
              existing.name === pName ||
              existing.name === `pod: ${pName}` ||
              existing.name.replace(/^pod:\s*/, '') === pName.replace(/^pod:\s*/, '')
            );
            if (matchId || matchName) {
              matched = existing;
              break;
            }
          }
        }
        if (matched) {
          if (!matched.containers.some(ec => ec.id === c.id || ec.name === c.name)) {
            matched.containers.push({
              ...c,
              user: u.user,
              uid: u.uid,
              rootless: u.user !== 'root' && u.uid !== 0,
            });
            const s = (c.status || '').toLowerCase();
            if (s.includes('running') || s.startsWith('up ')) {
              matched.containersRunningCount++;
            }
          }
        }
      }
    }
  }

  const allMergedPods = Array.from(new Set([...livePods, ...defaultPods]));
  for (const p of allMergedPods) {
    const totals = calculatePodTotals(p.containers);
    if (!p.totalCpu && totals.totalCpu) p.totalCpu = totals.totalCpu;
    if (!p.totalRam && totals.totalRam) p.totalRam = totals.totalRam;
  }
  return allMergedPods;
}

