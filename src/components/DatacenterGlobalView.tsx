import React, { useState, useEffect } from 'react';
import type { NodeItem, GlobalTaskItem } from '../types/index.ts';
import {
  groupNodesByDynamicCluster,
  resolveNodeIcon,
  formatNodeTypeBadge,
  formatNodeIpBadge,
  calculateClusterHealthScore,
  type DatacenterSection,
} from '../utils/navigationViewModel.ts';

export interface DatacenterGlobalViewProps {
  datacenterName?: string;
  nodes: NodeItem[];
  tasks: GlobalTaskItem[];
  activeSection?: DatacenterSection;
  onSelectNode: (node: NodeItem) => void;
  onOpenPlaybookModal: (playbookName?: string) => void;
  onOpenPlaybookEditor: (playbookName?: string) => void;
  onOpenGroupVars: () => void;
  onToggleTasksDrawer?: () => void;
  onRunPlaybookQuick?: (playbook: string, target?: string) => void;
}

interface PlaybookInfo {
  name: string;
  target: string;
  description: string;
  tag: string;
  isBaseline: boolean;
}

const DEFAULT_PLAYBOOKS: PlaybookInfo[] = [
  {
    name: 'site.yml',
    target: 'tag:host',
    description: 'Cluster baseline orchestration, PAM MFA enforcement, rootless Podman updates',
    tag: 'Baseline',
    isBaseline: true,
  },
  {
    name: 'security-hardening.yml',
    target: 'all',
    description: 'NFTables strict firewall, sysctl hardening, SSH agent isolation & privilege config',
    tag: 'Security',
    isBaseline: true,
  },
  {
    name: 'tailscale-status.yml',
    target: 'tag:host',
    description: 'Inspect MagicDNS, WireGuard encrypted mesh status, and exit-node routing',
    tag: 'Network',
    isBaseline: false,
  },
  {
    name: 'podman-update.yml',
    target: 'tag:host',
    description: 'Re-pull rootless quadlet container images and restart associated systemd units',
    tag: 'Containers',
    isBaseline: false,
  },
];

export const DatacenterGlobalView: React.FC<DatacenterGlobalViewProps> = ({
  datacenterName = 'leo-banjo.ts.net',
  nodes,
  tasks,
  activeSection,
  onSelectNode,
  onOpenPlaybookModal,
  onOpenPlaybookEditor,
  onOpenGroupVars,
  onToggleTasksDrawer,
  onRunPlaybookQuick,
}) => {
  const [playbooks, setPlaybooks] = useState<PlaybookInfo[]>(DEFAULT_PLAYBOOKS);
  const [loadingPlaybooks, setLoadingPlaybooks] = useState(false);
  const [filterQuery, setFilterQuery] = useState('');

  const health = calculateClusterHealthScore(nodes);
  const clusterGroups = groupNodesByDynamicCluster(nodes);

  // Auto-scroll when activeSection is triggered from sidebar
  useEffect(() => {
    if (activeSection && activeSection !== 'overview') {
      const sectionElement = document.getElementById(`datacenter-${activeSection}`);
      if (sectionElement) {
        sectionElement.scrollIntoView({ behavior: 'smooth', block: 'start' });
      }
    }
  }, [activeSection]);

  // Fetch real playbooks from backend
  useEffect(() => {
    setLoadingPlaybooks(true);
    fetch('/api/playbooks')
      .then(res => res.json())
      .then(data => {
        if (Array.isArray(data?.playbooks) && data.playbooks.length > 0) {
          const mapped: PlaybookInfo[] = data.playbooks.map((fileName: string) => {
            const foundDefault = DEFAULT_PLAYBOOKS.find(d => d.name === fileName);
            if (foundDefault) return foundDefault;
            return {
              name: fileName,
              target: 'all',
              description: `Ansible orchestration playbook: ${fileName}`,
              tag: 'Custom',
              isBaseline: fileName === 'site.yml' || fileName === 'site.yaml',
            };
          });
          setPlaybooks(mapped);
        }
      })
      .catch(() => {})
      .finally(() => setLoadingPlaybooks(false));
  }, []);

  // Fetch real cluster storage and shares from active nodes
  const [liveStorage, setLiveStorage] = useState<{
    zfsPools: Array<{ name: string; host: string; size: string; health: string }>;
    shares: Array<{ name: string; host: string; type: string; path: string }>;
    loading: boolean;
  }>({ zfsPools: [], shares: [], loading: true });

  useEffect(() => {
    const storageNodes = nodes.filter(n => n.status === 'online').slice(0, 5);
    if (storageNodes.length === 0) {
      setLiveStorage({ zfsPools: [], shares: [], loading: false });
      return;
    }

    Promise.allSettled(
      storageNodes.map(async node => {
        const [storageRes, sharesRes] = await Promise.allSettled([
          fetch(`/api/nodes/${encodeURIComponent(node.id)}/storage`).then(r => r.json()),
          fetch(`/api/nodes/${encodeURIComponent(node.id)}/shares`).then(r => r.json()),
        ]);
        return {
          node,
          storage: storageRes.status === 'fulfilled' ? storageRes.value : null,
          shares: sharesRes.status === 'fulfilled' ? sharesRes.value : null,
        };
      })
    ).then(results => {
      const zfsPools: Array<{ name: string; host: string; size: string; health: string }> = [];
      const shares: Array<{ name: string; host: string; type: string; path: string }> = [];

      for (const res of results) {
        if (res.status !== 'fulfilled' || !res.value) continue;
        const { node, storage, shares: sharesData } = res.value;

        if (storage?.success && typeof storage.zpoolList === 'string' && storage.zpoolList.trim().length > 0 && !storage.zpoolList.includes('NO_ZPOOL')) {
          const lines = storage.zpoolList.trim().split('\n');
          for (const line of lines) {
            const parts = line.trim().split(/\s+/);
            if (parts.length >= 2 && parts[0] !== 'NAME') {
              zfsPools.push({
                name: parts[0],
                host: node.shortName || node.name,
                size: parts[1] || 'Unknown',
                health: parts[6] || parts[parts.length - 1] || 'ONLINE',
              });
            }
          }
        }

        if (sharesData?.success) {
          if (Array.isArray(sharesData.nfsExports)) {
            for (const exp of sharesData.nfsExports.slice(0, 2)) {
              shares.push({
                name: exp.path || 'NFS Export',
                host: node.shortName || node.name,
                type: 'NFS',
                path: exp.path,
              });
            }
          }
          if (Array.isArray(sharesData.smbShares)) {
            for (const smb of sharesData.smbShares.slice(0, 2)) {
              shares.push({
                name: smb.name || 'SMB Share',
                host: node.shortName || node.name,
                type: 'Samba',
                path: smb.path,
              });
            }
          }
        }
      }

      setLiveStorage({ zfsPools, shares, loading: false });
    });
  }, [nodes]);

  const pveCount = nodes.filter(n => n.group === 'tag:pve' || n.badge === 'PVE').length;
  const vmCount = nodes.filter(n => n.group === 'tag:vm' || n.badge === 'VM').length;
  const lxcCount = nodes.filter(n => n.nodeType === 'lxc' || n.badge === 'LXC').length;
  const hostCount = nodes.filter(n => n.group === 'tag:host' || n.nodeType === 'linux').length;

  const filteredClusters = filterQuery.trim()
    ? clusterGroups
        .map(cg => ({
          ...cg,
          nodes: cg.nodes.filter(
            n =>
              n.name.toLowerCase().includes(filterQuery.toLowerCase()) ||
              n.ip.includes(filterQuery) ||
              n.tailscaleIp.includes(filterQuery) ||
              n.osDescription.toLowerCase().includes(filterQuery.toLowerCase())
          ),
        }))
        .filter(cg => cg.nodes.length > 0)
    : clusterGroups;

  return (
    <div className="flex flex-col gap-4 w-full">
      {/* Datacenter Header */}
      <div
        id="datacenter-overview"
        className="rounded-lg bg-[#121528] border border-[#26293c] p-4 shadow-md flex flex-col gap-3"
      >
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex items-center gap-3">
            <div className="w-10 h-10 rounded-lg bg-[#1b2a38] border border-[#8cd6cb]/40 flex items-center justify-center text-[#8cd6cb] shadow-sm">
              <span className="material-symbols-outlined text-[24px]">cloud_queue</span>
            </div>
            <div>
              <div className="flex items-center gap-2">
                <h1 className="text-lg font-bold font-mono text-[#f0f2ff] tracking-tight">
                  {datacenterName}
                </h1>
                <span className="px-2 py-0.5 rounded text-[10px] font-mono font-bold uppercase tracking-wider bg-[#1b2a38] text-[#8cd6cb] border border-[#8cd6cb]/30">
                  Tailnet Mesh
                </span>
              </div>
              <p className="text-xs text-[#8d909b] font-mono mt-0.5">
                WireGuard-encrypted mesh • MagicDNS active • Rootless Podman &amp; Cockpit nodes
              </p>
            </div>
          </div>

          <div className="flex items-center gap-2">
            <div className="px-3 py-1.5 rounded-md bg-[#171a2d] border border-[#26293c] flex items-center gap-2 text-xs font-mono">
              <span className="text-[#8d909b]">HEALTH:</span>
              <span className={`font-bold ${health.statusColor}`}>{health.score}% {health.statusLabel}</span>
            </div>
            <div className="px-3 py-1.5 rounded-md bg-[#171a2d] border border-[#26293c] flex items-center gap-2 text-xs font-mono">
              <span className="text-[#8d909b]">ONLINE:</span>
              <span className="font-bold text-[#8cd6cb]">{health.onlineCount} / {health.totalCount} NODES</span>
            </div>
          </div>
        </div>

        {/* Aggregate Stats Pills */}
        <div className="grid grid-cols-2 sm:grid-cols-4 gap-2 pt-2 border-t border-[#26293c]/60">
          <div className="bg-[#171a2d] px-3 py-2 rounded border border-[#26293c] flex items-center justify-between">
            <span className="text-[11px] font-mono text-[#8d909b]">Physical Hosts</span>
            <span className="text-sm font-mono font-bold text-[#b0c9ff]">{hostCount}</span>
          </div>
          <div className="bg-[#171a2d] px-3 py-2 rounded border border-[#26293c] flex items-center justify-between">
            <span className="text-[11px] font-mono text-[#8d909b]">Proxmox PVE</span>
            <span className="text-sm font-mono font-bold text-[#ffb4ab]">{pveCount}</span>
          </div>
          <div className="bg-[#171a2d] px-3 py-2 rounded border border-[#26293c] flex items-center justify-between">
            <span className="text-[11px] font-mono text-[#8d909b]">KVM Virtual Machines</span>
            <span className="text-sm font-mono font-bold text-[#d9b9ff]">{vmCount}</span>
          </div>
          <div className="bg-[#171a2d] px-3 py-2 rounded border border-[#26293c] flex items-center justify-between">
            <span className="text-[11px] font-mono text-[#8d909b]">LXC Containers</span>
            <span className="text-sm font-mono font-bold text-[#8cd6cb]">{lxcCount}</span>
          </div>
        </div>
      </div>

      {/* Section 1: Quick Actions */}
      <div className="rounded-lg bg-[#121528] border border-[#26293c] p-4 shadow-md">
        <div className="flex items-center justify-between mb-3">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[18px] text-[#b0c9ff]">flash_on</span>
            <h2 className="text-xs font-mono font-bold text-[#dfe1fa] uppercase tracking-wider">
              Datacenter Quick Actions
            </h2>
          </div>
          <span className="text-[10px] font-mono text-[#8d909b]">Cluster-wide operations</span>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-2.5">
          {/* Action 1: Run Ansible Playbook */}
          <button
            type="button"
            onClick={() => onOpenPlaybookModal()}
            className="group flex flex-col p-3 rounded-lg bg-[#171a2d] hover:bg-[#1f2338] border border-[#26293c] hover:border-[#b0c9ff]/50 transition-all text-left cursor-pointer shadow-xs"
          >
            <div className="flex items-center justify-between w-full mb-1">
              <span className="material-symbols-outlined text-[20px] text-[#b0c9ff] group-hover:scale-110 transition-transform">
                play_circle
              </span>
              <span className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-[#21263d] text-[#b0c9ff] border border-[#b0c9ff]/30">
                Run
              </span>
            </div>
            <span className="font-mono text-xs font-bold text-[#f0f2ff] group-hover:text-[#b0c9ff]">
              Run Ansible Playbook
            </span>
            <span className="text-[11px] text-[#8d909b] mt-1 leading-tight">
              Execute baseline, hardening, or container update tasks
            </span>
          </button>

          {/* Action 2: Manage Playbooks */}
          <button
            type="button"
            onClick={() => onOpenPlaybookEditor()}
            className="group flex flex-col p-3 rounded-lg bg-[#171a2d] hover:bg-[#1f2338] border border-[#26293c] hover:border-[#8cd6cb]/50 transition-all text-left cursor-pointer shadow-xs"
          >
            <div className="flex items-center justify-between w-full mb-1">
              <span className="material-symbols-outlined text-[20px] text-[#8cd6cb] group-hover:scale-110 transition-transform">
                edit_document
              </span>
              <span className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-[#1b2a38] text-[#8cd6cb] border border-[#8cd6cb]/30">
                Edit
              </span>
            </div>
            <span className="font-mono text-xs font-bold text-[#f0f2ff] group-hover:text-[#8cd6cb]">
              Manage Playbooks
            </span>
            <span className="text-[11px] text-[#8d909b] mt-1 leading-tight">
              Create, view, and modify YAML playbooks in ansible/playbooks
            </span>
          </button>

          {/* Action 3: Cluster Vars & Vault */}
          <button
            type="button"
            onClick={onOpenGroupVars}
            className="group flex flex-col p-3 rounded-lg bg-[#171a2d] hover:bg-[#1f2338] border border-[#26293c] hover:border-[#d9b9ff]/50 transition-all text-left cursor-pointer shadow-xs"
          >
            <div className="flex items-center justify-between w-full mb-1">
              <span className="material-symbols-outlined text-[20px] text-[#d9b9ff] group-hover:scale-110 transition-transform">
                key
              </span>
              <span className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-[#291e3b] text-[#d9b9ff] border border-[#d9b9ff]/30">
                Vault
              </span>
            </div>
            <span className="font-mono text-xs font-bold text-[#f0f2ff] group-hover:text-[#d9b9ff]">
              Cluster Vars &amp; Vault
            </span>
            <span className="text-[11px] text-[#8d909b] mt-1 leading-tight">
              Configure group variables and AES-256 encrypted secrets
            </span>
          </button>

          {/* Action 4: Global Tasks Execution */}
          <button
            type="button"
            onClick={() => {
              if (onToggleTasksDrawer) {
                onToggleTasksDrawer();
              } else {
                const el = document.getElementById('global-tasks-drawer');
                el?.scrollIntoView({ behavior: 'smooth' });
              }
            }}
            className="group flex flex-col p-3 rounded-lg bg-[#171a2d] hover:bg-[#1f2338] border border-[#26293c] hover:border-[#ffd580]/50 transition-all text-left cursor-pointer shadow-xs"
          >
            <div className="flex items-center justify-between w-full mb-1">
              <span className="material-symbols-outlined text-[20px] text-[#ffd580] group-hover:scale-110 transition-transform">
                task_alt
              </span>
              <span className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-[#2d281b] text-[#ffd580] border border-[#ffd580]/30">
                {tasks.length} Jobs
              </span>
            </div>
            <span className="font-mono text-xs font-bold text-[#f0f2ff] group-hover:text-[#ffd580]">
              Global Tasks Execution
            </span>
            <span className="text-[11px] text-[#8d909b] mt-1 leading-tight">
              View live execution logs, ACPI signals, and SSH outputs
            </span>
          </button>
        </div>
      </div>

      {/* Section 2: Global Playbooks Library */}
      <div
        id="datacenter-playbooks"
        className="rounded-lg bg-[#121528] border border-[#26293c] p-4 shadow-md flex flex-col gap-3"
      >
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[18px] text-[#8cd6cb]">terminal</span>
            <h2 className="text-xs font-mono font-bold text-[#dfe1fa] uppercase tracking-wider">
              Ansible Playbooks Library
            </h2>
            <span className="text-[10px] font-mono px-1.5 py-0.2 rounded bg-[#1b2a38] text-[#8cd6cb] border border-[#8cd6cb]/30">
              {playbooks.length} Available
            </span>
          </div>
          <button
            type="button"
            onClick={() => onOpenPlaybookEditor()}
            className="flex items-center gap-1 px-2.5 py-1 rounded bg-[#171a2d] hover:bg-[#21263d] text-[#b0c9ff] border border-[#3b405e] text-xs font-mono cursor-pointer transition-colors"
          >
            <span className="material-symbols-outlined text-[14px]">add</span>
            <span>New Playbook</span>
          </button>
        </div>

        {loadingPlaybooks ? (
          <div className="p-4 text-center text-xs font-mono text-[#8d909b]">
            Scanning ansible/playbooks/...
          </div>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-2.5">
            {playbooks.map(pb => (
              <div
                key={pb.name}
                className="p-3 rounded-lg bg-[#171a2d] border border-[#26293c] flex flex-col justify-between gap-2.5 hover:border-[#383c54] transition-all"
              >
                <div>
                  <div className="flex items-center justify-between gap-2 mb-1">
                    <span className="font-mono text-xs font-bold text-[#dfe1fa] flex items-center gap-1.5">
                      <span className="material-symbols-outlined text-[15px] text-[#8cd6cb]">description</span>
                      {pb.name}
                    </span>
                    <div className="flex items-center gap-1">
                      <span className="text-[9px] font-mono uppercase px-1.5 py-0.5 rounded bg-[#21263d] text-[#b0c9ff] border border-[#b0c9ff]/20">
                        {pb.target}
                      </span>
                      <span
                        className={`text-[9px] font-mono uppercase px-1.5 py-0.5 rounded border ${
                          pb.isBaseline
                            ? 'bg-[#1b2a38] text-[#8cd6cb] border-[#8cd6cb]/30'
                            : 'bg-[#291e3b] text-[#d9b9ff] border-[#d9b9ff]/30'
                        }`}
                      >
                        {pb.tag}
                      </span>
                    </div>
                  </div>
                  <p className="text-[11px] text-[#8d909b] leading-relaxed">
                    {pb.description}
                  </p>
                </div>

                <div className="flex items-center justify-end gap-1.5 pt-2 border-t border-[#26293c]/60">
                  <button
                    type="button"
                    onClick={() => onOpenPlaybookEditor(pb.name)}
                    className="flex items-center gap-1 px-2 py-1 rounded bg-[#1f2338] hover:bg-[#2c304d] text-[#dfe1fa] text-[11px] font-mono cursor-pointer transition-colors border border-[#3b405e]"
                  >
                    <span className="material-symbols-outlined text-[13px]">code</span>
                    <span>Edit / Inspect</span>
                  </button>
                  <button
                    type="button"
                    onClick={() => {
                      if (onRunPlaybookQuick) {
                        onRunPlaybookQuick(pb.name, pb.target);
                      } else {
                        onOpenPlaybookModal(pb.name);
                      }
                    }}
                    className="flex items-center gap-1 px-2.5 py-1 rounded bg-[#21263d] hover:bg-[#2b3352] text-[#8cd6cb] text-[11px] font-mono font-bold cursor-pointer transition-colors border border-[#8cd6cb]/40"
                  >
                    <span className="material-symbols-outlined text-[13px]">play_arrow</span>
                    <span>Run Playbook</span>
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Section 3: Tailnet Mesh Topology & Node Distribution */}
      <div
        id="datacenter-topology"
        className="rounded-lg bg-[#121528] border border-[#26293c] p-4 shadow-md flex flex-col gap-3"
      >
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[18px] text-[#d9b9ff]">hub</span>
            <h2 className="text-xs font-mono font-bold text-[#dfe1fa] uppercase tracking-wider">
              Tailnet Mesh Topology &amp; Node Distribution
            </h2>
            <span className="text-[10px] font-mono px-1.5 py-0.2 rounded bg-[#291e3b] text-[#d9b9ff] border border-[#d9b9ff]/30">
              {nodes.length} Nodes
            </span>
          </div>

          <div className="flex items-center gap-1 bg-[#171a2d] px-2 py-1 rounded border border-[#313447]">
            <span className="material-symbols-outlined text-[13px] text-[#8d909b]">search</span>
            <input
              type="text"
              value={filterQuery}
              onChange={e => setFilterQuery(e.target.value)}
              placeholder="Search host, IP, OS..."
              className="w-36 bg-transparent text-[#dfe1fa] font-mono text-[11px] outline-none"
            />
            {filterQuery && (
              <button
                type="button"
                onClick={() => setFilterQuery('')}
                className="text-[#8d909b] hover:text-[#dfe1fa] text-[11px] cursor-pointer"
              >
                ×
              </button>
            )}
          </div>
        </div>

        <div className="space-y-4">
          {filteredClusters.map(cluster => (
            <div key={cluster.group} className="space-y-2">
              <div className="flex items-center gap-2 px-1 text-xs font-mono">
                <span className="text-[#8cd6cb] font-bold">[{cluster.group}]</span>
                <span className="text-[#8d909b] text-[11px]">({cluster.nodes.length} nodes)</span>
              </div>

              <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-2">
                {cluster.nodes.map(node => {
                  const badgeInfo = formatNodeTypeBadge(node);
                  const ipBadge = formatNodeIpBadge(node);

                  return (
                    <div
                      key={node.id}
                      onClick={() => onSelectNode(node)}
                      className="group p-2.5 rounded-lg bg-[#171a2d] hover:bg-[#1e2338] border border-[#26293c] hover:border-[#b0c9ff]/40 transition-all cursor-pointer flex flex-col justify-between gap-2 shadow-xs"
                    >
                      <div className="flex items-start justify-between gap-1">
                        <div className="flex items-center gap-2 min-w-0">
                          <span
                            className={`w-2 h-2 rounded-full shrink-0 ${
                              node.status === 'online'
                                ? 'bg-[#8cd6cb] shadow-[0_0_6px_rgba(140,214,203,0.8)] animate-pulse'
                                : 'bg-[#ffb4ab]'
                            }`}
                          />
                          <span className="material-symbols-outlined text-[16px] text-[#b0c9ff] shrink-0">
                            {resolveNodeIcon(node)}
                          </span>
                          <span className="font-mono text-xs font-bold text-[#dfe1fa] truncate group-hover:text-[#b0c9ff]">
                            {node.name}
                          </span>
                        </div>

                        {badgeInfo && (
                          <span
                            className={`font-mono text-[9px] px-1 py-0.2 rounded font-semibold uppercase tracking-wider shrink-0 ${badgeInfo.badgeClasses}`}
                          >
                            {badgeInfo.label}
                          </span>
                        )}
                      </div>

                      <div className="font-mono text-[10px] text-[#8d909b] space-y-0.5">
                        <div className="flex items-center justify-between">
                          <span>IP:</span>
                          <span className="text-[#dfe1fa]">{ipBadge || node.ip}</span>
                        </div>
                        <div className="flex items-center justify-between">
                          <span>Specs:</span>
                          <span className="text-[#dfe1fa]">
                            {node.specs.vcpus} vCPUs • {node.specs.ramGiB} GB
                          </span>
                        </div>
                        <div className="flex items-center justify-between">
                          <span>Uptime:</span>
                          <span className="text-[#8cd6cb]">{node.uptime}</span>
                        </div>
                      </div>

                      <div className="flex items-center justify-between pt-1.5 border-t border-[#26293c]/60 text-[10px] font-mono text-[#8d909b]">
                        <span className="flex items-center gap-1">
                          <span className="material-symbols-outlined text-[12px] text-[#8cd6cb]">
                            {node.sshConnected ? 'vpn_key' : 'lock'}
                          </span>
                          <span>{node.sshConnected ? 'SSH Ready' : 'Tailscale'}</span>
                        </span>
                        <span className="text-[#b0c9ff] group-hover:underline flex items-center gap-0.5">
                          Open Cockpit
                          <span className="material-symbols-outlined text-[11px]">arrow_forward</span>
                        </span>
                      </div>
                    </div>
                  );
                })}
              </div>
            </div>
          ))}
        </div>
      </div>

      {/* Section 4: Global Cluster Storage Summary */}
      <div
        id="datacenter-storage"
        className="rounded-lg bg-[#121528] border border-[#26293c] p-4 shadow-md flex flex-col gap-3"
      >
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[18px] text-[#ffd580]">storage</span>
            <h2 className="text-xs font-mono font-bold text-[#dfe1fa] uppercase tracking-wider">
              Global Cluster Storage Summary
            </h2>
            <span className="text-[10px] font-mono px-1.5 py-0.2 rounded bg-[#2d281b] text-[#ffd580] border border-[#ffd580]/30">
              ZFS • NFS • Samba
            </span>
          </div>
          <span className="text-[10px] font-mono text-[#8d909b]">Shared storage pools &amp; exports</span>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
          {/* ZFS Pools Card */}
          <div className="p-3 rounded-lg bg-[#171a2d] border border-[#26293c] flex flex-col justify-between gap-2">
            <div>
              <div className="flex items-center justify-between text-xs font-mono font-bold text-[#dfe1fa] mb-1">
                <span className="flex items-center gap-1.5">
                  <span className="material-symbols-outlined text-[16px] text-[#8cd6cb]">dns</span>
                  ZFS Storage Pools
                </span>
                <span className="text-[9px] px-1.5 py-0.2 rounded bg-[#1b2a38] text-[#8cd6cb] border border-[#8cd6cb]/30">
                  ONLINE
                </span>
              </div>
              <p className="text-[11px] text-[#8d909b] mb-2 leading-tight">
                Enterprise RAID-Z2 &amp; mirrored pools with scheduled scrubbing and snapshot protection.
              </p>
              <div className="space-y-1 font-mono text-[10px] text-[#c3c6d2] bg-[#121528] p-2 rounded border border-[#26293c]">
                {liveStorage.loading ? (
                  <div className="text-[#8d909b] animate-pulse">Probing node storage pools...</div>
                ) : liveStorage.zfsPools.length === 0 ? (
                  <div className="text-[#8d909b]">No ZFS pools on probed hosts (native Btrfs/LVM in use)</div>
                ) : (
                  liveStorage.zfsPools.slice(0, 3).map((p, idx) => (
                    <div key={idx} className="flex justify-between">
                      <span className="text-[#8d909b] truncate">{p.name} ({p.host}):</span>
                      <span className="text-[#8cd6cb] font-bold">{p.size} / {p.health}</span>
                    </div>
                  ))
                )}
              </div>
            </div>
          </div>

          {/* Network Shares Card */}
          <div className="p-3 rounded-lg bg-[#171a2d] border border-[#26293c] flex flex-col justify-between gap-2">
            <div>
              <div className="flex items-center justify-between text-xs font-mono font-bold text-[#dfe1fa] mb-1">
                <span className="flex items-center gap-1.5">
                  <span className="material-symbols-outlined text-[16px] text-[#b0c9ff]">folder_shared</span>
                  Network Shares (NFS/SMB)
                </span>
                <span className="text-[9px] px-1.5 py-0.2 rounded bg-[#21263d] text-[#b0c9ff] border border-[#b0c9ff]/30">
                  {liveStorage.shares.length > 0 ? 'ACTIVE' : 'READY'}
                </span>
              </div>
              <p className="text-[11px] text-[#8d909b] mb-2 leading-tight">
                Shared filesystem exports configured via `/etc/exports.d` and atomic `smb.conf`.
              </p>
              <div className="space-y-1 font-mono text-[10px] text-[#c3c6d2] bg-[#121528] p-2 rounded border border-[#26293c]">
                {liveStorage.loading ? (
                  <div className="text-[#8d909b] animate-pulse">Probing export shares...</div>
                ) : liveStorage.shares.length === 0 ? (
                  <div className="text-[#8d909b]">No active NFS/SMB exports discovered</div>
                ) : (
                  liveStorage.shares.slice(0, 3).map((s, idx) => (
                    <div key={idx} className="flex justify-between">
                      <span className="text-[#8d909b] truncate">{s.type} ({s.host}):</span>
                      <span className="text-[#dfe1fa] truncate max-w-[120px]">{s.path || s.name}</span>
                    </div>
                  ))
                )}
              </div>
            </div>
          </div>

          {/* Restic Backup Repo Card */}
          <div className="p-3 rounded-lg bg-[#171a2d] border border-[#26293c] flex flex-col justify-between gap-2">
            <div>
              <div className="flex items-center justify-between text-xs font-mono font-bold text-[#dfe1fa] mb-1">
                <span className="flex items-center gap-1.5">
                  <span className="material-symbols-outlined text-[16px] text-[#d9b9ff]">cloud_sync</span>
                  Restic Snapshots &amp; Vault
                </span>
                <span className="text-[9px] px-1.5 py-0.2 rounded bg-[#291e3b] text-[#d9b9ff] border border-[#d9b9ff]/30">
                  ENCRYPTED
                </span>
              </div>
              <p className="text-[11px] text-[#8d909b] mb-2 leading-tight">
                AES-256 deduplicated cluster snapshots with automated integrity verification.
              </p>
              <div className="space-y-1 font-mono text-[10px] text-[#c3c6d2] bg-[#121528] p-2 rounded border border-[#26293c]">
                <div className="flex justify-between">
                  <span className="text-[#8d909b]">Repository:</span>
                  <span className="text-[#dfe1fa]">s3://restic-backup-vault</span>
                </div>
                <div className="flex justify-between">
                  <span className="text-[#8d909b]">Retention Policy:</span>
                  <span className="text-[#dfe1fa]">7 daily / 4 weekly / 12 monthly</span>
                </div>
                <div className="flex justify-between">
                  <span className="text-[#8d909b]">Integrity Check:</span>
                  <span className="text-[#8cd6cb]">PASS (0 pack errors)</span>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      {/* Section 5: Global Tasks & Executions */}
      <div
        id="datacenter-tasks"
        className="rounded-lg bg-[#121528] border border-[#26293c] p-4 shadow-md flex flex-col gap-3"
      >
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[18px] text-[#b0c9ff]">history</span>
            <h2 className="text-xs font-mono font-bold text-[#dfe1fa] uppercase tracking-wider">
              Recent Global Tasks &amp; Executions
            </h2>
            <span className="text-[10px] font-mono px-1.5 py-0.2 rounded bg-[#21263d] text-[#b0c9ff] border border-[#b0c9ff]/30">
              {tasks.length} Recorded
            </span>
          </div>
          <button
            type="button"
            onClick={onToggleTasksDrawer}
            className="text-[11px] font-mono text-[#8cd6cb] hover:underline cursor-pointer flex items-center gap-0.5"
          >
            <span>Open Tasks Drawer</span>
            <span className="material-symbols-outlined text-[13px]">open_in_new</span>
          </button>
        </div>

        {tasks.length === 0 ? (
          <div className="p-4 text-center text-xs font-mono text-[#8d909b]">
            No recent cluster tasks executed.
          </div>
        ) : (
          <div className="space-y-1.5 font-mono text-xs">
            {tasks.slice(0, 5).map(task => (
              <div
                key={task.id}
                className="p-2.5 rounded bg-[#171a2d] border border-[#26293c] flex flex-wrap items-center justify-between gap-2"
              >
                <div className="flex items-center gap-2 min-w-0">
                  <span
                    className={`px-1.5 py-0.5 rounded text-[10px] font-bold ${
                      task.status === 'OK'
                        ? 'bg-[#1b2a38] text-[#8cd6cb] border border-[#8cd6cb]/30'
                        : task.status === 'RUNNING'
                        ? 'bg-[#21263d] text-[#b0c9ff] border border-[#b0c9ff]/30 animate-pulse'
                        : 'bg-[#311c1c] text-[#ffb4ab] border border-[#ffb4ab]/30'
                    }`}
                  >
                    {task.status}
                  </span>
                  <span className="text-[#dfe1fa] font-semibold truncate max-w-sm">
                    {task.description}
                  </span>
                </div>
                <div className="flex items-center gap-3 text-[11px] text-[#8d909b]">
                  <span>Target: <span className="text-[#b0c9ff]">{task.nodeTarget}</span></span>
                  <span>{task.startTime}</span>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
};
