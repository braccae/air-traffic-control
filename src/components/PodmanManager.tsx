import React, { useState, useEffect } from 'react';
import type {
  NodeItem,
  PodItem,
  StandaloneDaemon,
  UnprivilegedPodmanUser,
  ContainerItem,
} from '../types/index.ts';
import {
  countPodmanStatuses,
  filterPodsByStatus,
  mergeLiveProbeIntoPods,
  getContainerSecurityBadge,
  groupStandaloneContainersByUser,
  filterUserContainerGroups,
} from '../utils/podmanViewModel.ts';
import { safeFetchJson } from '../utils/apiDefensive.ts';

interface PodmanManagerProps {
  selectedNode?: NodeItem;
  onOpenLogs: (target: string, user?: string, uid?: number) => void;
  onOpenExec: (target: string) => void;
  onRestartPod: (podName: string) => void;
  onCreatePod?: () => void;
  onRunContainer?: () => void;
}

function getPodDisplayCpu(pod: PodItem): string {
  if (pod.totalCpu) return pod.totalCpu;
  let sum = 0;
  let hasValid = false;
  for (const c of pod.containers) {
    if (c.cpu && c.cpu.includes('%')) {
      const v = parseFloat(c.cpu);
      if (!isNaN(v)) {
        sum += v;
        hasValid = true;
      }
    }
  }
  return hasValid ? `${sum.toFixed(1)}%` : '-';
}

function getPodDisplayRam(pod: PodItem): string {
  if (pod.totalRam) return pod.totalRam;
  let sumMiB = 0;
  let hasValid = false;
  for (const c of pod.containers) {
    if (c.memory) {
      const lower = c.memory.toLowerCase();
      if (lower.includes('gib')) {
        const v = parseFloat(lower);
        if (!isNaN(v)) { sumMiB += v * 1024; hasValid = true; }
      } else if (lower.includes('mib')) {
        const v = parseFloat(lower);
        if (!isNaN(v)) { sumMiB += v; hasValid = true; }
      }
    }
  }
  if (!hasValid) return '-';
  return sumMiB >= 1024 ? `${(sumMiB / 1024).toFixed(2)} GiB` : `${Math.round(sumMiB)} MiB`;
}

export const PodmanManager: React.FC<PodmanManagerProps> = ({
  selectedNode,
  onOpenLogs,
  onOpenExec,
  onRestartPod,
}) => {
  const [filter, setFilter] = useState<'all' | 'running' | 'stopped'>('all');
  const [activeTab, setActiveTab] = useState<'all' | 'podman' | 'docker'>('all');
  const [probedUsers, setProbedUsers] = useState<UnprivilegedPodmanUser[]>([]);
  const [rootContainers, setRootContainers] = useState<ContainerItem[]>([]);
  const [rootPods, setRootPods] = useState<PodItem[]>([]);
  const [hasDocker, setHasDocker] = useState(false);
  const [dockerContainers, setDockerContainers] = useState<ContainerItem[]>([]);
  const [customPods, setCustomPods] = useState<PodItem[]>([]);
  const [standaloneState, setStandaloneState] = useState<StandaloneDaemon[]>([]);
  const [loading, setLoading] = useState(false);

  // Inline Create Pod / Run Container forms
  const [showCreatePodForm, setShowCreatePodForm] = useState(false);
  const [newPodName, setNewPodName] = useState('');
  const [newPodPorts, setNewPodPorts] = useState('8080:80');
  const [newPodUser, setNewPodUser] = useState('core');

  const [showRunContainerForm, setShowRunContainerForm] = useState(false);
  const [newContainerName, setNewContainerName] = useState('');
  const [newContainerImage, setNewContainerImage] = useState('docker.io/library/alpine:latest');
  const [newContainerUser, setNewContainerUser] = useState('core');

  const hostParam = selectedNode?.id || selectedNode?.tailscaleHost || 'likes-it-cold';

  const loadContainers = async () => {
    setLoading(true);
    try {
      const res = await safeFetchJson<{
        probedUsers?: UnprivilegedPodmanUser[];
        rootPods?: PodItem[];
        hasDocker?: boolean;
        dockerContainers?: ContainerItem[];
        rootContainers?: any[];
      }>(`/api/nodes/${encodeURIComponent(hostParam)}/containers`);

      if (res.success && res.data) {
        const data = res.data;
        if (Array.isArray(data?.probedUsers)) {
          setProbedUsers(data.probedUsers);
        }
        if (Array.isArray(data?.rootPods)) {
          setRootPods(data.rootPods);
        }
        setHasDocker(Boolean(data?.hasDocker));
        if (Array.isArray(data?.dockerContainers)) {
          setDockerContainers(data.dockerContainers);
        }
        if (Array.isArray(data?.rootContainers)) {
          const mapped: ContainerItem[] = data.rootContainers.map((c: any) => ({
            id: c.id,
            name: c.name,
            image: c.image,
            status: c.status,
            cpu: c.cpu || '',
            memory: c.memory || '',
            user: c.user || 'root',
            uid: c.uid ?? 0,
            rootless: Boolean(c.rootless),
            podId: c.podId,
            podName: c.podName,
          }));
          setRootContainers(mapped);
          setStandaloneState(
            mapped.slice(0, 6).map((c: any) => ({
              name: c.name,
              subtext: `${c.image} (${c.user || 'root'}:${c.uid ?? 0})`,
              status: String(c.status).toLowerCase().includes('exited') ? 'exited' : 'running',
              memory: c.memory || '',
              user: c.user || 'root',
              uid: c.uid ?? 0,
              rootless: Boolean(c.rootless),
            }))
          );
        }
      }
    } catch {
      // Defensive fallback
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    setNewPodUser(selectedNode?.sshUser || 'core');
    setNewContainerUser(selectedNode?.sshUser || 'core');
    loadContainers();
  }, [hostParam]);

  const handleContainerAction = async (container: ContainerItem, action: 'restart' | 'stop' | 'start') => {
    await safeFetchJson('/api/podman/action', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        host: hostParam,
        container: container.name,
        action,
        user: container.user,
        uid: container.uid,
      }),
    });
    onRestartPod(`${container.name} (${action})`);
    loadContainers();
  };

  const handleCreatePodSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!newPodName.trim()) return;
    const created: PodItem = {
      id: `pod-${Date.now()}`,
      name: `pod: ${newPodName.trim()}`,
      containersRunningCount: 1,
      ports: `Ports: ${newPodPorts}`,
      totalCpu: '0.1%',
      totalRam: '16 MiB',
      user: newPodUser,
      uid: newPodUser === 'root' ? 0 : 1000,
      rootless: newPodUser !== 'root',
      containers: [
        {
          id: `infra-${Date.now()}`,
          name: `${newPodName.trim()}-infra`,
          image: 'registry.k8s.io/pause:3.10',
          status: 'Running',
          cpu: '0.01%',
          memory: '8 MiB',
          user: newPodUser,
          uid: newPodUser === 'root' ? 0 : 1000,
          rootless: newPodUser !== 'root',
        },
      ],
    };
    setCustomPods(prev => [created, ...prev]);
    setNewPodName('');
    setShowCreatePodForm(false);
    onRestartPod(created.name);
  };

  const handleRunContainerSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!newContainerName.trim()) return;
    const isRoot = newContainerUser === 'root';
    const newDaemon: StandaloneDaemon = {
      name: newContainerName.trim(),
      subtext: `${newContainerImage} (${newContainerUser}:${isRoot ? 0 : 1000})`,
      status: 'running',
      memory: '32 MiB',
      user: newContainerUser,
      uid: isRoot ? 0 : 1000,
      rootless: !isRoot,
    };
    setStandaloneState(prev => [newDaemon, ...prev]);
    setNewContainerName('');
    setShowRunContainerForm(false);
    onRestartPod(newDaemon.name);
  };

  // Combine custom pods and root pods, and merge probed users
  const allPods = mergeLiveProbeIntoPods([...customPods, ...rootPods], probedUsers);

  // Extract all container IDs that belong to a pod
  const podContainerIds = new Set<string>();
  for (const pod of allPods) {
    for (const c of pod.containers) {
      if (c.id) podContainerIds.add(c.id);
      if (c.name) podContainerIds.add(c.name);
    }
  }

  // Standalone containers are all containers not inside a pod
  const standaloneContainers: ContainerItem[] = [];

  for (const c of rootContainers) {
    if (!podContainerIds.has(c.id) && !podContainerIds.has(c.name)) {
      standaloneContainers.push(c);
    }
  }

  for (const s of standaloneState) {
    if (!podContainerIds.has(s.name) && !standaloneContainers.some(c => c.name === s.name)) {
      standaloneContainers.push({
        id: s.name,
        name: s.name,
        image: s.subtext,
        status: s.status === 'running' ? 'Running' : 'Exited (0)',
        cpu: '',
        memory: s.memory,
        user: s.user || 'root',
        uid: s.uid ?? 0,
        rootless: s.rootless ?? false,
      });
    }
  }

  for (const u of probedUsers) {
    for (const c of u.containers) {
      if (
        !podContainerIds.has(c.id) &&
        !podContainerIds.has(c.name) &&
        !standaloneContainers.some(sc => sc.id === c.id || sc.name === c.name)
      ) {
        standaloneContainers.push({
          ...c,
          user: u.user,
          uid: u.uid,
          rootless: u.uid !== 0,
        });
      }
    }
  }

  const userStandaloneGroups = groupStandaloneContainersByUser(standaloneContainers);
  const filteredUserGroups = filterUserContainerGroups(userStandaloneGroups, filter);
  const filteredPods = filterPodsByStatus(allPods, filter);

  const filteredDocker = dockerContainers.filter(c => {
    if (filter === 'all') return true;
    const s = (c.status || '').toLowerCase();
    const isRunning = s.includes('running') || s.startsWith('up ');
    return filter === 'running' ? isRunning : !isRunning;
  });

  const statusCounts = countPodmanStatuses(allPods, standaloneContainers, dockerContainers);

  return (
    <section className="bg-[#171a2d] rounded p-3 flex flex-col border border-[#26293c]">
      {/* Header bar */}
      <div className="flex flex-wrap items-center justify-between gap-2 pb-2">
        <div className="flex items-center gap-2 flex-wrap">
          <span className="material-symbols-outlined text-[#d9b9ff] text-[20px]">token</span>
          <span className="font-sans text-sm text-[#dfe1fa] font-semibold">
            Podman &amp; Container Runtime ({selectedNode?.shortName || 'host'})
          </span>

          {/* Tab selector */}
          <div className="flex items-center bg-[#26293c] p-0.5 rounded font-mono text-xs border border-[#434750]/40">
            <button
              type="button"
              onClick={() => setActiveTab('all')}
              className={`px-2 py-0.5 rounded cursor-pointer transition-colors ${
                activeTab === 'all'
                  ? 'bg-[#35384c] text-[#dfe1fa] font-bold shadow-xs'
                  : 'text-[#8d909b] hover:text-[#dfe1fa]'
              }`}
            >
              All ({statusCounts.all + (hasDocker ? dockerContainers.length : 0)})
            </button>
            <button
              type="button"
              onClick={() => setActiveTab('podman')}
              className={`px-2 py-0.5 rounded cursor-pointer transition-colors ${
                activeTab === 'podman'
                  ? 'bg-[#35384c] text-[#8cd6cb] font-bold shadow-xs'
                  : 'text-[#8d909b] hover:text-[#8cd6cb]'
              }`}
            >
              Podman Hierarchy ({statusCounts.all})
            </button>
            {hasDocker && (
              <button
                type="button"
                onClick={() => setActiveTab('docker')}
                className={`px-2 py-0.5 rounded cursor-pointer transition-colors flex items-center gap-1 ${
                  activeTab === 'docker'
                    ? 'bg-[#35384c] text-[#b0c9ff] font-bold shadow-xs'
                    : 'text-[#8d909b] hover:text-[#b0c9ff]'
                }`}
              >
                <span className="material-symbols-outlined text-[13px]">deployed_code</span>
                <span>Docker Engine ({dockerContainers.length})</span>
              </button>
            )}
          </div>

          {/* Status filter buttons */}
          <div className="flex items-center bg-[#313447] px-2 py-0.5 rounded font-mono text-xs gap-2 border border-[#434750]/40">
            <button
              type="button"
              onClick={() => setFilter('all')}
              className={`cursor-pointer ${
                filter === 'all' ? 'text-[#b0c9ff] font-bold' : 'text-[#c3c6d2] hover:text-[#dfe1fa]'
              }`}
            >
              All ({statusCounts.all})
            </button>
            <span className="text-[#434750]">|</span>
            <button
              type="button"
              onClick={() => setFilter('running')}
              className={`cursor-pointer ${
                filter === 'running'
                  ? 'text-[#8cd6cb] font-bold'
                  : 'text-[#8cd6cb]/70 hover:text-[#8cd6cb]'
              }`}
            >
              Running ({statusCounts.running})
            </button>
            <span className="text-[#434750]">|</span>
            <button
              type="button"
              onClick={() => setFilter('stopped')}
              className={`cursor-pointer ${
                filter === 'stopped'
                  ? 'text-[#ffb4ab] font-bold'
                  : 'text-[#ffb4ab]/70 hover:text-[#ffb4ab]'
              }`}
            >
              Stopped ({statusCounts.stopped})
            </button>
          </div>

          {loading && (
            <span className="font-mono text-[10px] text-[#8cd6cb] animate-pulse">
              Probing SSH...
            </span>
          )}
        </div>

        <div className="flex items-center gap-1.5">
          <button
            type="button"
            onClick={loadContainers}
            className="flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#8cd6cb] px-2 py-1 rounded font-mono text-xs border border-[#434750]/50 transition-colors cursor-pointer"
            title="Refresh live containers via SSH"
          >
            <span className="material-symbols-outlined text-[14px]">refresh</span>
            <span>Sync</span>
          </button>
          <button
            type="button"
            onClick={() => {
              setShowCreatePodForm(prev => !prev);
              setShowRunContainerForm(false);
            }}
            className="flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] px-2.5 py-1 rounded font-mono text-xs border border-[#434750]/50 transition-colors cursor-pointer"
          >
            <span className="material-symbols-outlined text-[14px] text-[#d9b9ff]">add_circle</span>
            <span>+ Create Pod</span>
          </button>
          <button
            type="button"
            onClick={() => {
              setShowRunContainerForm(prev => !prev);
              setShowCreatePodForm(false);
            }}
            className="flex items-center gap-1 bg-[#b0c9ff] text-[#002e68] font-bold hover:bg-[#d8e2ff] px-2.5 py-1 rounded font-mono text-xs transition-colors shadow-sm cursor-pointer"
          >
            <span className="material-symbols-outlined text-[14px]">play_arrow</span>
            <span>+ Run Container</span>
          </button>
        </div>
      </div>

      {/* Inline Create Pod Form */}
      {showCreatePodForm && (
        <form
          onSubmit={handleCreatePodSubmit}
          className="mb-2 p-2.5 bg-[#0a0d1f] rounded border border-[#b0c9ff]/40 grid grid-cols-1 md:grid-cols-4 gap-2 font-mono text-xs items-end"
        >
          <div>
            <label className="block text-[10px] text-[#8d909b] mb-0.5">POD NAME</label>
            <input
              type="text"
              required
              value={newPodName}
              onChange={e => setNewPodName(e.target.value)}
              placeholder="e.g. ingress-pod"
              className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2 py-1 text-[#dfe1fa]"
            />
          </div>
          <div>
            <label className="block text-[10px] text-[#8d909b] mb-0.5">PUBLISHED PORTS</label>
            <input
              type="text"
              value={newPodPorts}
              onChange={e => setNewPodPorts(e.target.value)}
              placeholder="8080:80, 8443:443"
              className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2 py-1 text-[#dfe1fa]"
            />
          </div>
          <div>
            <label className="block text-[10px] text-[#8d909b] mb-0.5">ROOTLESS USER</label>
            <input
              type="text"
              value={newPodUser}
              onChange={e => setNewPodUser(e.target.value)}
              className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2 py-1 text-[#8cd6cb]"
            />
          </div>
          <div className="flex gap-1.5">
            <button
              type="submit"
              className="px-3 py-1 bg-[#b0c9ff] text-[#002e68] font-bold rounded cursor-pointer"
            >
              Deploy Pod
            </button>
            <button
              type="button"
              onClick={() => setShowCreatePodForm(false)}
              className="px-2 py-1 bg-[#26293c] text-[#c3c6d2] rounded cursor-pointer"
            >
              Cancel
            </button>
          </div>
        </form>
      )}

      {/* Inline Run Container Form */}
      {showRunContainerForm && (
        <form
          onSubmit={handleRunContainerSubmit}
          className="mb-2 p-2.5 bg-[#0a0d1f] rounded border border-[#8cd6cb]/40 grid grid-cols-1 md:grid-cols-4 gap-2 font-mono text-xs items-end"
        >
          <div>
            <label className="block text-[10px] text-[#8d909b] mb-0.5">CONTAINER NAME</label>
            <input
              type="text"
              required
              value={newContainerName}
              onChange={e => setNewContainerName(e.target.value)}
              placeholder="e.g. caddy-edge"
              className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2 py-1 text-[#dfe1fa]"
            />
          </div>
          <div>
            <label className="block text-[10px] text-[#8d909b] mb-0.5">OCI IMAGE</label>
            <input
              type="text"
              required
              value={newContainerImage}
              onChange={e => setNewContainerImage(e.target.value)}
              className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2 py-1 text-[#dfe1fa]"
            />
          </div>
          <div>
            <label className="block text-[10px] text-[#8d909b] mb-0.5">EXECUTION USER</label>
            <input
              type="text"
              value={newContainerUser}
              onChange={e => setNewContainerUser(e.target.value)}
              className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2 py-1 text-[#8cd6cb]"
            />
          </div>
          <div className="flex gap-1.5">
            <button
              type="submit"
              className="px-3 py-1 bg-[#8cd6cb] text-[#002e68] font-bold rounded cursor-pointer"
            >
              Start Container
            </button>
            <button
              type="button"
              onClick={() => setShowRunContainerForm(false)}
              className="px-2 py-1 bg-[#26293c] text-[#c3c6d2] rounded cursor-pointer"
            >
              Cancel
            </button>
          </div>
        </form>
      )}

      {/* Main Content Area */}
      <div className="space-y-3 mt-1 font-mono text-xs">
        {/* SECTION 1: PODS & MEMBER CONTAINERS */}
        {(activeTab === 'all' || activeTab === 'podman') && (
          <div className="space-y-2">
            <div className="flex items-center justify-between pb-1 border-b border-[#26293c]/60">
              <div className="flex items-center gap-1.5 font-mono text-[11px] font-bold text-[#dfe1fa] uppercase tracking-wider">
                <span className="material-symbols-outlined text-[#d9b9ff] text-[16px]">folder_special</span>
                <span>PODMAN PODS &amp; MEMBER CONTAINERS ({filteredPods.length})</span>
              </div>
              <span className="text-[10px] text-[#8d909b] font-mono">
                {filteredPods.reduce((acc, p) => acc + p.containers.length, 0)} member containers
              </span>
            </div>

            {filteredPods.length === 0 ? (
              <div className="bg-[#1b1e31] rounded p-3 text-[#8d909b] border border-[#26293c] text-center">
                No matching Podman pods on {selectedNode?.name || 'this node'} for filter &ldquo;{filter}&rdquo;.
              </div>
            ) : (
              filteredPods.map(pod => {
                const badgeInfo = getContainerSecurityBadge(pod.user, pod.uid, pod.rootless);
                return (
                  <div key={pod.id} className="bg-[#1b1e31] rounded p-2 border border-[#26293c]">
                    {/* Pod header */}
                    <div className="flex items-center justify-between pb-1.5">
                      <div className="flex items-center gap-2 flex-wrap">
                        <span className="material-symbols-outlined text-[#d9b9ff] text-[16px]">
                          folder_special
                        </span>
                        <span className="text-[#dfe1fa] font-bold text-xs">{pod.name}</span>
                        <span className="px-1.5 py-0.2 bg-[#8cd6cb]/20 text-[#8cd6cb] rounded text-[10px] border border-[#8cd6cb]/30">
                          {pod.containersRunningCount} Containers Running
                        </span>
                        <span
                          className={`px-1.5 py-0.5 rounded text-[10px] font-bold border ${
                            badgeInfo.isRootful
                              ? 'bg-[#ffd97d]/15 text-[#ffd97d] border-[#ffd97d]/30'
                              : 'bg-[#8cd6cb]/15 text-[#8cd6cb] border-[#8cd6cb]/30'
                          }`}
                        >
                          {badgeInfo.label}
                        </span>
                        {pod.user && (
                          <span className="px-1.5 py-0.2 bg-[#b0c9ff]/15 text-[#b0c9ff] rounded text-[10px] border border-[#b0c9ff]/30">
                            user: {pod.user} ({pod.uid ?? 0})
                          </span>
                        )}
                        <span className="text-[#8d909b] text-[11px]">{pod.ports}</span>
                      </div>

                      <div className="flex items-center gap-3">
                        <span className="text-[#8d909b]">
                          CPU: <strong className="text-[#dfe1fa]">{getPodDisplayCpu(pod)}</strong>
                        </span>
                        <span className="text-[#8d909b]">
                          RAM: <strong className="text-[#dfe1fa]">{getPodDisplayRam(pod)}</strong>
                        </span>
                        <div className="flex items-center gap-1">
                          <button
                            type="button"
                            onClick={() => onRestartPod(pod.name)}
                            className="p-1 bg-[#26293c] hover:bg-[#35384c] text-[#8cd6cb] rounded cursor-pointer border border-[#434750]/30"
                            title="Restart Pod"
                          >
                            <span className="material-symbols-outlined text-[14px]">refresh</span>
                          </button>
                          <button
                            type="button"
                            onClick={() => onOpenLogs(pod.name, pod.user, pod.uid)}
                            className="p-1 bg-[#26293c] hover:bg-[#35384c] text-[#b0c9ff] rounded cursor-pointer border border-[#434750]/30"
                            title="Pod Logs"
                          >
                            <span className="material-symbols-outlined text-[14px]">terminal</span>
                          </button>
                        </div>
                      </div>
                    </div>

                    {/* Containers Table */}
                    <div className="overflow-x-auto mt-1">
                      <table className="w-full text-left">
                        <thead className="bg-[#0a0d1f]/70 text-[#8d909b] text-[10px]">
                          <tr className="h-6">
                            <th className="px-2">Container Name</th>
                            <th className="px-2">User (UID)</th>
                            <th className="px-2">Image</th>
                            <th className="px-2">Status</th>
                            <th className="px-2">CPU</th>
                            <th className="px-2">Memory</th>
                            <th className="px-2 text-right">Actions</th>
                          </tr>
                        </thead>
                        <tbody className="divide-y divide-[#26293c]/50">
                          {pod.containers.map(c => (
                            <tr key={c.id} className="h-7 hover:bg-[#26293c]/40 transition-colors">
                              <td className="px-2 text-[#dfe1fa] font-medium flex items-center gap-1.5">
                                <span
                                  className={`w-1.5 h-1.5 rounded-full ${
                                    c.status.toLowerCase().includes('exited')
                                      ? 'bg-[#ffb4ab]'
                                      : 'bg-[#8cd6cb]'
                                  }`}
                                ></span>
                                <span>{c.name}</span>
                              </td>
                              <td className="px-2 text-[#b0c9ff]">
                                {c.user ? `${c.user}:${c.uid ?? 0}` : 'root:0'}
                              </td>
                              <td className="px-2 text-[#8d909b] truncate max-w-xs">{c.image}</td>
                              <td className="px-2">
                                <span
                                  className={
                                    c.status.toLowerCase().includes('exited')
                                      ? 'text-[#ffb4ab]'
                                      : 'text-[#8cd6cb]'
                                  }
                                >
                                  {c.status}
                                </span>
                              </td>
                              <td className="px-2 text-[#dfe1fa]">{c.cpu || '-'}</td>
                              <td className="px-2 text-[#dfe1fa]">{c.memory || '-'}</td>
                              <td className="px-2 text-right space-x-1">
                                <button
                                  type="button"
                                  onClick={() => handleContainerAction(c, 'restart')}
                                  className="px-1.5 py-0.5 bg-[#26293c] hover:bg-[#35384c] text-[#ffd97d] rounded text-[10px] cursor-pointer"
                                >
                                  Restart
                                </button>
                                <button
                                  type="button"
                                  onClick={() => onOpenExec(c.name)}
                                  className="px-1.5 py-0.5 bg-[#26293c] hover:bg-[#35384c] text-[#8cd6cb] rounded text-[10px] cursor-pointer"
                                >
                                  Exec
                                </button>
                                <button
                                  type="button"
                                  onClick={() => onOpenLogs(c.name, c.user, c.uid)}
                                  className="px-1.5 py-0.5 bg-[#26293c] hover:bg-[#35384c] text-[#b0c9ff] rounded text-[10px] cursor-pointer"
                                >
                                  Logs
                                </button>
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  </div>
                );
              })
            )}
          </div>
        )}

        {/* SECTION 2: STANDALONE CONTAINERS GROUPED BY USER */}
        {(activeTab === 'all' || activeTab === 'podman') && (
          <div className="space-y-2">
            <div className="flex items-center justify-between pb-1 pt-1 border-b border-[#26293c]/60">
              <div className="flex items-center gap-1.5 font-mono text-[11px] font-bold text-[#dfe1fa] uppercase tracking-wider">
                <span className="material-symbols-outlined text-[#8cd6cb] text-[16px]">group_work</span>
                <span>STANDALONE CONTAINERS (GROUPED BY USER) ({filteredUserGroups.length} Users)</span>
              </div>
              <span className="text-[10px] text-[#8d909b] font-mono">
                {filteredUserGroups.reduce((acc, g) => acc + g.totalContainers, 0)} unpodded containers
              </span>
            </div>

            {filteredUserGroups.length === 0 ? (
              <div className="bg-[#1b1e31] rounded p-3 text-[#8d909b] border border-[#26293c] text-center">
                No standalone containers matching filter &ldquo;{filter}&rdquo;.
              </div>
            ) : (
              filteredUserGroups.map(group => (
                <div key={group.user} className="bg-[#1b1e31] rounded p-2.5 border border-[#26293c]">
                  {/* User group header */}
                  <div className="flex items-center justify-between pb-2 border-b border-[#26293c]/40 flex-wrap gap-2">
                    <div className="flex items-center gap-2">
                      <span className="material-symbols-outlined text-[#8cd6cb] text-[18px]">
                        {group.user === 'root' || group.user === 'core' ? 'badge' : 'deployed_code'}
                      </span>
                      <span className="text-[#dfe1fa] font-bold text-xs">
                        {group.user === 'root' ? 'root (System)' : `user: ${group.user}`}
                      </span>
                      <span className="text-[#8d909b] text-[11px] font-mono">
                        UID {group.uid}
                      </span>
                      <span
                        className={`px-1.5 py-0.5 rounded text-[10px] font-bold border ${
                          !group.rootless
                            ? 'bg-[#ffd97d]/15 text-[#ffd97d] border-[#ffd97d]/30'
                            : 'bg-[#8cd6cb]/15 text-[#8cd6cb] border-[#8cd6cb]/30'
                        }`}
                      >
                        {group.badge}
                      </span>
                    </div>

                    <div className="flex items-center gap-2 text-[10px] font-mono">
                      <span className="px-1.5 py-0.5 bg-[#26293c] text-[#dfe1fa] rounded border border-[#434750]/30">
                        Total: <strong>{group.totalContainers}</strong>
                      </span>
                      <span className="px-1.5 py-0.5 bg-[#8cd6cb]/15 text-[#8cd6cb] rounded border border-[#8cd6cb]/30">
                        Running: <strong>{group.runningContainers}</strong>
                      </span>
                      <span className="px-1.5 py-0.5 bg-[#ffb4ab]/15 text-[#ffb4ab] rounded border border-[#ffb4ab]/30">
                        Stopped: <strong>{group.stoppedContainers}</strong>
                      </span>
                    </div>
                  </div>

                  {/* User containers table */}
                  <div className="overflow-x-auto mt-1">
                    <table className="w-full text-left font-mono text-xs">
                      <thead className="bg-[#0a0d1f]/70 text-[#8d909b] text-[10px]">
                        <tr className="h-6">
                          <th className="px-2">Container Name</th>
                          <th className="px-2">Image</th>
                          <th className="px-2">Status</th>
                          <th className="px-2">CPU</th>
                          <th className="px-2">Memory</th>
                          <th className="px-2 text-right">Actions</th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-[#26293c]/50">
                        {group.containers.map(c => (
                          <tr key={c.id || c.name} className="h-7 hover:bg-[#26293c]/40 transition-colors">
                            <td className="px-2 text-[#dfe1fa] font-medium flex items-center gap-1.5">
                              <span
                                className={`w-1.5 h-1.5 rounded-full ${
                                  c.status.toLowerCase().includes('exited')
                                    ? 'bg-[#ffb4ab]'
                                    : 'bg-[#8cd6cb]'
                                }`}
                              ></span>
                              <span>{c.name}</span>
                            </td>
                            <td className="px-2 text-[#8d909b] truncate max-w-xs">{c.image}</td>
                            <td className="px-2">
                              <span
                                className={
                                  c.status.toLowerCase().includes('exited')
                                    ? 'text-[#ffb4ab]'
                                    : 'text-[#8cd6cb]'
                                }
                              >
                                {c.status}
                              </span>
                            </td>
                            <td className="px-2 text-[#dfe1fa]">{c.cpu || '-'}</td>
                            <td className="px-2 text-[#dfe1fa]">{c.memory || '-'}</td>
                            <td className="px-2 text-right space-x-1">
                              <button
                                type="button"
                                onClick={() => handleContainerAction(c, 'restart')}
                                className="px-1.5 py-0.5 bg-[#26293c] hover:bg-[#35384c] text-[#ffd97d] rounded text-[10px] cursor-pointer"
                              >
                                Restart
                              </button>
                              <button
                                type="button"
                                onClick={() => onOpenExec(c.name)}
                                className="px-1.5 py-0.5 bg-[#26293c] hover:bg-[#35384c] text-[#8cd6cb] rounded text-[10px] cursor-pointer"
                              >
                                Exec
                              </button>
                              <button
                                type="button"
                                onClick={() => onOpenLogs(c.name, c.user, c.uid)}
                                className="px-1.5 py-0.5 bg-[#26293c] hover:bg-[#35384c] text-[#b0c9ff] rounded text-[10px] cursor-pointer"
                              >
                                Logs
                              </button>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </div>
              ))
            )}
          </div>
        )}

        {/* SECTION 3: DOCKER ENGINE CONTAINERS */}
        {(activeTab === 'all' || activeTab === 'docker') && (hasDocker || activeTab === 'docker') && (
          <div className="bg-[#1b1e31] rounded p-2.5 border border-[#26293c] mt-2">
            <div className="flex items-center justify-between pb-2 border-b border-[#26293c]/50 flex-wrap gap-2">
              <div className="flex items-center gap-2">
                <span className="material-symbols-outlined text-[#b0c9ff] text-[18px]">deployed_code</span>
                <span className="font-bold text-xs text-[#dfe1fa]">
                  Docker Engine Containers ({selectedNode?.shortName || 'host'})
                </span>
                <span className="px-1.5 py-0.5 bg-[#b0c9ff]/15 text-[#b0c9ff] border border-[#b0c9ff]/30 rounded text-[10px] font-bold">
                  dockerd (rootful)
                </span>
              </div>
              <div className="flex items-center gap-2 text-[11px] text-[#8d909b]">
                <span>
                  Active:{' '}
                  <strong className="text-[#8cd6cb]">
                    {
                      filteredDocker.filter(
                        c =>
                          c.status.toLowerCase().includes('running') ||
                          c.status.toLowerCase().startsWith('up ')
                      ).length
                    }
                  </strong>
                </span>
                <span>|</span>
                <span>
                  Total: <strong className="text-[#dfe1fa]">{dockerContainers.length}</strong>
                </span>
              </div>
            </div>

            {filteredDocker.length === 0 ? (
              <div className="p-3 text-center text-[#8d909b] text-xs">
                {dockerContainers.length === 0
                  ? `Docker Engine is active on ${selectedNode?.shortName || 'host'}, but no containers are currently running.`
                  : `No Docker containers matching filter "${filter}".`}
              </div>
            ) : (
              <div className="overflow-x-auto mt-1">
                <table className="w-full text-left font-mono text-xs">
                  <thead className="bg-[#0a0d1f]/70 text-[#8d909b] text-[10px]">
                    <tr className="h-6">
                      <th className="px-2">Container ID</th>
                      <th className="px-2">Container Name</th>
                      <th className="px-2">Image</th>
                      <th className="px-2">Status</th>
                      <th className="px-2 text-right">Actions</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-[#26293c]/50">
                    {filteredDocker.map(dc => (
                      <tr key={dc.id || dc.name} className="h-7 hover:bg-[#26293c]/40 transition-colors">
                        <td className="px-2 text-[#8d909b] font-mono text-[11px]">{dc.id}</td>
                        <td className="px-2 text-[#dfe1fa] font-medium flex items-center gap-1.5">
                          <span
                            className={`w-1.5 h-1.5 rounded-full ${
                              dc.status.toLowerCase().includes('exited')
                                ? 'bg-[#ffb4ab]'
                                : 'bg-[#8cd6cb]'
                            }`}
                          ></span>
                          <span>{dc.name}</span>
                        </td>
                        <td className="px-2 text-[#8d909b] truncate max-w-xs">{dc.image}</td>
                        <td className="px-2">
                          <span
                            className={
                              dc.status.toLowerCase().includes('exited')
                                ? 'text-[#ffb4ab]'
                                : 'text-[#8cd6cb]'
                            }
                          >
                            {dc.status}
                          </span>
                        </td>
                        <td className="px-2 text-right space-x-1">
                          <button
                            type="button"
                            onClick={() => onOpenExec(dc.name)}
                            className="px-1.5 py-0.5 bg-[#26293c] hover:bg-[#35384c] text-[#8cd6cb] rounded text-[10px] cursor-pointer"
                          >
                            Exec
                          </button>
                          <button
                            type="button"
                            onClick={() => onOpenLogs(dc.name, 'root', 0)}
                            className="px-1.5 py-0.5 bg-[#26293c] hover:bg-[#35384c] text-[#b0c9ff] rounded text-[10px] cursor-pointer"
                          >
                            Logs
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        )}
      </div>
    </section>
  );
};
