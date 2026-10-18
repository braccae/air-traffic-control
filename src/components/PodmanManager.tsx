import React, { useState, useEffect } from 'react';
import type { PodItem, StandaloneDaemon, UnprivilegedPodmanUser } from '../types/index.ts';
import {
  countPodmanStatuses,
  filterPodsByStatus,
  filterStandaloneDaemons,
  mergeLiveProbeIntoPods,
} from '../utils/podmanViewModel.ts';

interface PodmanManagerProps {
  onOpenLogs: (target: string) => void;
  onOpenExec: (target: string) => void;
  onRestartPod: (podName: string) => void;
  onCreatePod: () => void;
  onRunContainer: () => void;
}

export const PodmanManager: React.FC<PodmanManagerProps> = ({
  onOpenLogs,
  onOpenExec,
  onRestartPod,
  onCreatePod,
  onRunContainer,
}) => {
  const [filter, setFilter] = useState<'all' | 'running' | 'stopped'>('all');
  const [standaloneState, setStandaloneState] = useState<StandaloneDaemon[]>([
    {
      name: 'restic-exporter',
      subtext: 'prom-metrics:9150 (restic-svc:984)',
      status: 'running',
      memory: '12 MiB',
      user: 'restic-svc',
      uid: 984,
      rootless: true,
    },
    {
      name: 'tailscale-sidecar',
      subtext: 'tun:tailscale0 (deployer:1000)',
      status: 'running',
      memory: '28 MiB',
      user: 'deployer',
      uid: 1000,
      rootless: true,
    },
    {
      name: 'node-exporter-old',
      subtext: 'Exited (code 0)',
      status: 'exited',
      memory: '0 B',
      user: 'podman',
      uid: 1001,
      rootless: true,
    },
  ]);

  const handleStartStandalone = (name: string) => {
    setStandaloneState(prev =>
      prev.map(item =>
        item.name === name
          ? { ...item, status: 'running', subtext: 'prom-metrics:9100', memory: '16 MiB' }
          : item
      )
    );
  };

  const pods: PodItem[] = [
    {
      id: 'media-stack',
      name: 'pod: media-stack',
      containersRunningCount: 3,
      ports: 'Ports: 8096:8096, 443:443',
      totalCpu: '2.4%',
      totalRam: '1.82 GiB',
      user: 'deployer',
      uid: 1000,
      rootless: true,
      containers: [
        {
          id: 'infra-con-media',
          name: 'infra-con-media',
          image: 'k8s.gcr.io/pause:3.9',
          status: 'Running',
          cpu: '0.01%',
          memory: '4.2 MiB',
          user: 'deployer',
          uid: 1000,
          rootless: true,
        },
        {
          id: 'jellyfin-media-server',
          name: 'jellyfin-media-server',
          image: 'jellyfin/jellyfin:10.8.13',
          status: 'Running (VAAPI)',
          cpu: '2.14%',
          memory: '1.68 GiB',
          user: 'deployer',
          uid: 1000,
          rootless: true,
        },
        {
          id: 'caddy-gateway',
          name: 'caddy-gateway',
          image: 'caddy:2.7.6-alpine',
          status: 'Running',
          cpu: '0.25%',
          memory: '138 MiB',
          user: 'deployer',
          uid: 1000,
          rootless: true,
        },
      ],
    },
    {
      id: 'ai-inference',
      name: 'pod: ai-inference',
      containersRunningCount: 2,
      ports: 'Ports: 11434:11434, 3000:8080',
      totalCpu: '4.8%',
      totalRam: '6.9 GiB',
      gpuTag: 'NVIDIA RTX 4090 Passthrough',
      vram: 'VRAM: 8.2 / 24.0 GiB',
      gpuUtil: 32,
      user: 'podman',
      uid: 1001,
      rootless: true,
      containers: [
        {
          id: 'ollama-vllm-engine',
          name: 'ollama-vllm-engine',
          image: 'ollama/ollama:latest',
          status: 'Running (CUDA 12.4)',
          cpu: '3.8%',
          memory: '6.4 GiB VRAM',
          gpuAcc: 'RTX 4090 [0000:01:00.0]',
          user: 'podman',
          uid: 1001,
          rootless: true,
        },
        {
          id: 'open-webui',
          name: 'open-webui',
          image: 'ghcr.io/open-webui/open-webui:main',
          status: 'Running',
          cpu: '0.9%',
          memory: '512 MiB System',
          gpuAcc: 'Shared IPC',
          user: 'podman',
          uid: 1001,
          rootless: true,
        },
      ],
    },
  ];

  const [probedUsers, setProbedUsers] = useState<UnprivilegedPodmanUser[]>([]);

  useEffect(() => {
    fetch('/api/podman/probe')
      .then(res => res.json())
      .then(data => {
        if (Array.isArray(data)) {
          setProbedUsers(data);
        }
      })
      .catch(() => {});
  }, []);

  const allPods = mergeLiveProbeIntoPods(pods, probedUsers);
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
            Podman Pods &amp; Containers
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
                filter === 'running' ? 'text-[#8cd6cb] font-bold' : 'text-[#8cd6cb]/70 hover:text-[#8cd6cb]'
              }`}
            >
              Running ({statusCounts.running})
            </button>
            <span className="text-[#434750]">|</span>
            <button
              type="button"
              onClick={() => setFilter('stopped')}
              className={`cursor-pointer ${
                filter === 'stopped' ? 'text-[#ffb4ab] font-bold' : 'text-[#ffb4ab]/70 hover:text-[#ffb4ab]'
              }`}
            >
              Stopped ({statusCounts.stopped})
            </button>
          </div>
        </div>

        <div className="flex items-center gap-1.5">
          <button
            type="button"
            onClick={onCreatePod}
            className="flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] px-2.5 py-1 rounded font-mono text-xs border border-[#434750]/50 transition-colors cursor-pointer"
          >
            <span className="material-symbols-outlined text-[14px] text-[#d9b9ff]">add_circle</span>
            <span>+ Create Pod</span>
          </button>
          <button
            type="button"
            onClick={onRunContainer}
            className="flex items-center gap-1 bg-[#b0c9ff] text-[#002e68] font-bold hover:bg-[#d8e2ff] px-2.5 py-1 rounded font-mono text-xs transition-colors shadow-sm cursor-pointer"
          >
            <span className="material-symbols-outlined text-[14px]">play_arrow</span>
            <span>+ Run Container</span>
          </button>
        </div>
      </div>

      {/* Pods list */}
      <div className="space-y-2 mt-1 font-mono text-xs">
        {filteredPods.map(pod => (
          <div key={pod.id} className="bg-[#1b1e31] rounded p-2 border border-[#26293c]">
            {/* Pod header */}
            <div className="flex items-center justify-between pb-1.5">
              <div className="flex items-center gap-2">
                <span className="material-symbols-outlined text-[#d9b9ff] text-[16px]">
                  folder_special
                </span>
                <span className="text-[#dfe1fa] font-bold text-xs">{pod.name}</span>
                <span className="px-1.5 py-0.2 bg-[#8cd6cb]/20 text-[#8cd6cb] rounded text-[10px] border border-[#8cd6cb]/30">
                  {pod.containersRunningCount} Containers Running
                </span>
                {pod.user && (
                  <span className="px-1.5 py-0.2 bg-[#b0c9ff]/15 text-[#b0c9ff] rounded text-[10px] border border-[#b0c9ff]/30">
                    rootless: {pod.user} ({pod.uid})
                  </span>
                )}
                {pod.gpuTag && (
                  <span className="px-1.5 py-0.2 bg-[#573583]/40 text-[#d9b9ff] rounded text-[10px] border border-[#573583]">
                    {pod.gpuTag}
                  </span>
                )}
                <span className="text-[#8d909b] text-[11px]">{pod.ports}</span>
                {pod.vram && <span className="text-[#8d909b] text-[11px]">{pod.vram}</span>}
              </div>

              <div className="flex items-center gap-3">
                {pod.gpuUtil !== undefined ? (
                  <span className="text-[#8d909b]">
                    GPU Util: <strong className="text-[#d9b9ff]">{pod.gpuUtil}%</strong>
                  </span>
                ) : (
                  <>
                    <span className="text-[#8d909b]">
                      CPU: <strong className="text-[#dfe1fa]">{pod.totalCpu}</strong>
                    </span>
                    <span className="text-[#8d909b]">
                      RAM: <strong className="text-[#dfe1fa]">{pod.totalRam}</strong>
                    </span>
                  </>
                )}
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
                    {pod.gpuTag ? <th className="px-2">GPU Acceleration</th> : <th className="px-2">CPU</th>}
                    <th className="px-2">{pod.gpuTag ? 'Memory / VRAM' : 'Memory'}</th>
                    <th className="px-2 text-right">Actions</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-[#26293c]/50">
                  {pod.containers.map(c => (
                    <tr key={c.id} className="h-7 hover:bg-[#26293c]/40 transition-colors">
                      <td className="px-2 text-[#dfe1fa] font-medium flex items-center gap-1.5">
                        <span className="w-1.5 h-1.5 rounded-full bg-[#8cd6cb]"></span>
                        <span>{c.name}</span>
                      </td>
                      <td className="px-2 text-[#b0c9ff]">
                        {c.user ? `${c.user}:${c.uid}` : 'root:0'}
                      </td>
                      <td className="px-2 text-[#8d909b] truncate max-w-xs">{c.image}</td>
                      <td className="px-2">
                        <span className="text-[#8cd6cb]">{c.status}</span>
                      </td>
                      <td className="px-2 text-[#dfe1fa]">{c.gpuAcc ? <span className="text-[#d9b9ff]">{c.gpuAcc}</span> : c.cpu}</td>
                      <td className="px-2 text-[#dfe1fa]">{c.memory}</td>
                      <td className="px-2 text-right space-x-1">
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
        ))}

        {/* Standalone Operational Containers */}
        <div className="bg-[#1b1e31] rounded p-2 border border-[#26293c]">
          <span className="font-mono text-[10px] text-[#8d909b] font-bold uppercase block pb-1">
            STANDALONE DAEMONS &amp; EXPORTERS
          </span>
          <div className="grid grid-cols-1 md:grid-cols-3 gap-2">
            {filteredDaemons.map(item => (
              <div
                key={item.name}
                className="bg-[#0a0d1f] p-2 rounded flex items-center justify-between border border-[#26293c]/50"
              >
                <div className="flex items-center gap-1.5">
                  <span
                    className={`w-1.5 h-1.5 rounded-full ${
                      item.status === 'running' ? 'bg-[#8cd6cb]' : 'bg-[#ffb4ab]'
                    }`}
                  ></span>
                  <div className="flex flex-col">
                    <span className="text-[#dfe1fa] font-bold text-xs">{item.name}</span>
                    <span className="text-[#8d909b] text-[10px]">{item.subtext}</span>
                  </div>
                </div>
                {item.status === 'running' ? (
                  <span className="text-[#8cd6cb] text-xs">{item.memory}</span>
                ) : (
                  <button
                    type="button"
                    onClick={() => handleStartStandalone(item.name)}
                    className="px-2 py-0.5 bg-[#26293c] hover:bg-[#35384c] text-[#b0c9ff] text-[10px] rounded cursor-pointer"
                  >
                    Start
                  </button>
                )}
              </div>
            ))}
          </div>
        </div>
      </div>
    </section>
  );
};
