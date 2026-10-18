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
      if (c.status.toLowerCase().includes('running')) {
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

  return identities.map(identity => {
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
}
