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
  filterStandaloneDaemons,
  mergeLiveProbeIntoPods,
} from '../utils/podmanViewModel.ts';

interface PodmanManagerProps {
  selectedNode?: NodeItem;
  onOpenLogs: (target: string) => void;
  onOpenExec: (target: string) => void;
  onRestartPod: (podName: string) => void;
  onCreatePod?: () => void;
  onRunContainer?: () => void;
}

export const PodmanManager: React.FC<PodmanManagerProps> = ({
  selectedNode,
  onOpenLogs,
  onOpenExec,
  onRestartPod,
}) => {
  const [filter, setFilter] = useState<'all' | 'running' | 'stopped'>('all');
  const [probedUsers, setProbedUsers] = useState<UnprivilegedPodmanUser[]>([]);
  const [rootContainers, setRootContainers] = useState<ContainerItem[]>([]);
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

  const loadContainers = () => {
    setLoading(true);
    fetch(`/api/nodes/${encodeURIComponent(hostParam)}/containers`)
      .then(res => res.json())
      .then(data => {
        if (Array.isArray(data?.probedUsers)) {
          setProbedUsers(data.probedUsers);
        }
        if (Array.isArray(data?.rootContainers)) {
          setRootContainers(
            data.rootContainers.map((c: any) => ({
              id: c.id,
              name: c.name,
              image: c.image,
              status: c.status,
              cpu: '0.4%',
              memory: '96 MiB',
              user: c.user || 'root',
              uid: c.uid ?? 0,
              rootless: Boolean(c.rootless),
            }))
          );
          setStandaloneState(
            data.rootContainers.slice(0, 6).map((c: any) => ({
              name: c.name,
              subtext: `${c.image} (${c.user || 'root'}:${c.uid ?? 0})`,
              status: String(c.status).toLowerCase().includes('exited') ? 'exited' : 'running',
              memory: '64 MiB',
              user: c.user || 'root',
              uid: c.uid ?? 0,
              rootless: Boolean(c.rootless),
            }))
          );
        }
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    setNewPodUser(selectedNode?.sshUser || 'core');
    setNewContainerUser(selectedNode?.sshUser || 'core');
    loadContainers();
  }, [hostParam]);

  const handleContainerAction = (container: ContainerItem, action: 'restart' | 'stop' | 'start') => {
    fetch('/api/podman/action', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        host: hostParam,
        container: container.name,
        action,
        user: container.user,
        uid: container.uid,
      }),
    }).then(() => {
      onRestartPod(`${container.name} (${action})`);
      loadContainers();
    });
  };

  const handleStartStandalone = (name: string) => {
    setStandaloneState(prev =>
      prev.map(item =>
        item.name === name ? { ...item, status: 'running', memory: '32 MiB' } : item
      )
    );
    fetch('/api/podman/action', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ host: hostParam, container: name, action: 'start' }),
    }).catch(() => {});
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
      uid: 1000,
      rootless: newPodUser !== 'root',
      containers: [
        {
          id: `infra-${Date.now()}`,
          name: `${newPodName.trim()}-infra`,
          image: 'localhost/podman-pause:latest',
          status: 'Running',
          cpu: '0.01%',
          memory: '8 MiB',
          user: newPodUser,
          uid: 1000,
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
    const newDaemon: StandaloneDaemon = {
      name: newContainerName.trim(),
      subtext: `${newContainerImage} (${newContainerUser}:1000)`,
      status: 'running',
      memory: '32 MiB',
      user: newContainerUser,
      uid: 1000,
      rootless: newContainerUser !== 'root',
    };
    setStandaloneState(prev => [newDaemon, ...prev]);
    setNewContainerName('');
    setShowRunContainerForm(false);
    onRestartPod(newDaemon.name);
  };

  const systemPods: PodItem[] =
    rootContainers.length > 0
      ? [
          {
            id: `system-${hostParam}`,
            name: `pod: system-services (${selectedNode?.shortName || 'host'})`,
            containersRunningCount: rootContainers.filter(
              c =>
                c.status.toLowerCase().includes('running') ||
                c.status.toLowerCase().startsWith('up ')
            ).length,
            ports: `Host: ${selectedNode?.tailscaleHost || 'local'}`,
            totalCpu: '1.8%',
            totalRam: `${Math.max(128, rootContainers.length * 64)} MiB`,
            user: 'root',
            uid: 0,
            rootless: false,
            containers: rootContainers,
          },
        ]
      : [];

  const allPods = mergeLiveProbeIntoPods([...customPods, ...systemPods], probedUsers);
  const statusCounts = countPodmanStatuses(allPods, standaloneState);
  const filteredPods = filterPodsByStatus(allPods, filter);
  const filteredDaemons = filterStandaloneDaemons(standaloneState, filter);

  return (
    <section className="bg-[#171a2d] rounded p-3 flex flex-col border border-[#26293c]">
      {/* Header bar */}
      <div className="flex flex-wrap items-center justify-between gap-2 pb-2">
        <div className="flex items-center gap-2">
          <span className="material-symbols-outlined text-[#d9b9ff] text-[20px]">token</span>
          <span className="font-sans text-sm text-[#dfe1fa] font-semibold">
            Podman &amp; Container Runtime ({selectedNode?.shortName || 'host'})
          </span>

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

      {/* Pods list */}
      <div className="space-y-2 mt-1 font-mono text-xs">
        {filteredPods.length === 0 ? (
          <div className="bg-[#1b1e31] rounded p-3 text-[#8d909b] border border-[#26293c] text-center">
            No matching Podman pods or containers on {selectedNode?.name || 'this node'} for filter &ldquo;{filter}&rdquo;.
          </div>
        ) : (
          filteredPods.map(pod => (
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
                  {pod.user && (
                    <span className="px-1.5 py-0.2 bg-[#b0c9ff]/15 text-[#b0c9ff] rounded text-[10px] border border-[#b0c9ff]/30">
                      {pod.rootless ? 'rootless' : 'system'}: {pod.user} ({pod.uid ?? 0})
                    </span>
                  )}
                  <span className="text-[#8d909b] text-[11px]">{pod.ports}</span>
                </div>

                <div className="flex items-center gap-3">
                  <span className="text-[#8d909b]">
                    CPU: <strong className="text-[#dfe1fa]">{pod.totalCpu}</strong>
                  </span>
                  <span className="text-[#8d909b]">
                    RAM: <strong className="text-[#dfe1fa]">{pod.totalRam}</strong>
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
                      onClick={() => onOpenLogs(pod.name)}
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
                        <td className="px-2 text-[#dfe1fa]">{c.cpu}</td>
                        <td className="px-2 text-[#dfe1fa]">{c.memory}</td>
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
                            onClick={() => onOpenLogs(c.name)}
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

        {/* Standalone Operational Containers */}
        {filteredDaemons.length > 0 && (
          <div className="bg-[#1b1e31] rounded p-2 border border-[#26293c]">
            <span className="font-mono text-[10px] text-[#8d909b] font-bold uppercase block pb-1">
              ACTIVE SYSTEM &amp; QUADLET CONTAINERS ({selectedNode?.shortName || 'host'})
            </span>
            <div className="grid grid-cols-1 md:grid-cols-3 gap-2">
              {filteredDaemons.map(item => (
                <div
                  key={item.name}
                  className="bg-[#0a0d1f] p-2 rounded flex items-center justify-between border border-[#26293c]/50"
                >
                  <div className="flex items-center gap-1.5 min-w-0">
                    <span
                      className={`w-1.5 h-1.5 rounded-full shrink-0 ${
                        item.status === 'running' ? 'bg-[#8cd6cb]' : 'bg-[#ffb4ab]'
                      }`}
                    ></span>
                    <div className="flex flex-col min-w-0">
                      <span className="text-[#dfe1fa] font-bold text-xs truncate">{item.name}</span>
                      <span className="text-[#8d909b] text-[10px] truncate">{item.subtext}</span>
                    </div>
                  </div>
                  {item.status === 'running' ? (
                    <button
                      type="button"
                      onClick={() => onOpenLogs(item.name)}
                      className="px-2 py-0.5 bg-[#26293c] hover:bg-[#35384c] text-[#8cd6cb] text-[10px] rounded cursor-pointer shrink-0"
                    >
                      Logs
                    </button>
                  ) : (
                    <button
                      type="button"
                      onClick={() => handleStartStandalone(item.name)}
                      className="px-2 py-0.5 bg-[#26293c] hover:bg-[#35384c] text-[#b0c9ff] text-[10px] rounded cursor-pointer shrink-0"
                    >
                      Start
                    </button>
                  )}
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
    </section>
  );
};
