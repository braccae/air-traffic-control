import type { PodItem, ServiceUser, StandaloneDaemon, UnprivilegedPodmanUser } from '../types/index.ts';
import { filterContainersByStatus } from './orchestrator.ts';

export interface CorrelatedServiceIdentity extends ServiceUser {
  uid: number;
  isRunningPodman: boolean;
  containerCount: number;
  subUidRange?: string;
  linger?: boolean;
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
  daemons: StandaloneDaemon[]
): { all: number; running: number; stopped: number } {
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
    if (d.status === 'running') {
      running++;
    } else {
      stopped++;
    }
  }

  return {
    all: running + stopped,
    running,
    stopped,
  };
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

  const existingPodUserSet = new Set(defaultPods.map(p => p.user).filter(Boolean));
  const livePods: PodItem[] = [];

  for (const u of probedUsers) {
    if (u.uid <= 0 || u.user === 'root' || u.containers.length === 0) continue;
    // If this user is a newly discovered live host user (e.g. 'pants'), group their containers into a rootless pod card
    if (!existingPodUserSet.has(u.user)) {
      const normalizedContainers = u.containers.map(c => ({
        id: c.id,
        name: c.name,
        image: c.image || 'local/rootless:latest',
        status: c.status.toLowerCase().startsWith('up ') ? `Running (${c.status})` : c.status,
        cpu: c.cpu || '0.4%',
        memory: c.memory || '64 MiB',
        user: u.user,
        uid: u.uid,
        rootless: true,
      }));
      const runningCount = normalizedContainers.filter(c =>
        c.status.toLowerCase().includes('running')
      ).length;

      livePods.push({
        id: `rootless-${u.user}-${u.uid}`,
        name: `pod: rootless-${u.user}`,
        containersRunningCount: runningCount,
        ports: u.subUidRange ? `SubUID: ${u.subUidRange}` : `Home: ${u.homeDir}`,
        totalCpu: '1.2%',
        totalRam: `${Math.max(1, normalizedContainers.length * 64)} MiB`,
        user: u.user,
        uid: u.uid,
        rootless: true,
        containers: normalizedContainers,
      });
    }
  }

  return [...livePods, ...defaultPods];
}

