export type NodeType = 'linux' | 'windows' | 'router_switch' | 'pbs' | 'pve' | 'vm' | 'lxc' | 'k3s';

export interface NodeItem {
  id: string;
  name: string;
  shortName: string;
  group: string;
  datacenter: string;
  nodeType: NodeType;
  badge?: string;
  badgeColor?: string;
  ip: string;
  port: number;
  sshUser?: string;
  authMethod?: string;
  tags?: string[];
  sshConnected?: boolean;
  rttMs?: string;
  tailscaleIp: string;
  tailscaleHost: string;
  tailscaleTag?: string;
  fallbackHost?: string;
  fallbackIp?: string;
  apiPort?: number;
  apiUser?: string;
  winrmPort?: number;
  rdpPort?: number;
  hypervisorHost?: string;
  vmId?: number;
  osDescription: string;
  status: 'online' | 'stopped' | 'degraded';
  uptime: string;
  hasGlances: boolean;
  hasCockpit: boolean;
  hasPodman: boolean;
  hasRestic: boolean;
  specs: {
    cpu: string;
    vcpus: number;
    ramGiB: number;
    motherboard: string;
    bios: string;
    ipmi: string;
    ecc: string;
    secureBoot: string;
    iommu: string;
  };
  privilegeMethod?: 'run0' | 'sudo' | 'doas' | 'none';
  privilegeActive?: boolean;
  initSystem?: 'systemd' | 'openrc' | 'sysvinit' | 'runit' | 's6' | string;
  connectionProtocol?: 'ssh' | 'winrm' | 'tailscale_ssh';
  vaultSecretId?: string;
  folder?: string; // custom folder ID if non-tailscale
  isDynamicTagMember?: boolean;
  followedTag?: string;
  pluginOverrides?: {
    connection?: string;
    elevation?: string;
    package?: string;
    [key: string]: string | undefined;
  };
}

export interface TagInventoryChunk {
  id: string; // e.g. "chunk-tag:worker"
  tag: string; // e.g. "tag:worker"
  sshUser?: string;
  vaultSecretId?: string; // encrypted password or ssh key vault entry
  authMethod: 'tailscale-ssh' | 'vault-password' | 'vault-sshkey' | 'ssh-agent';
  createdAt: number;
}

export interface CustomFolder {
  id: string; // e.g. "folder-cold-storage"
  name: string; // e.g. "Cold Storage"
  createdAt: number;
}

export type PrivilegeMethod = 'run0' | 'sudo' | 'doas' | 'none';

export interface PrivilegeStatus {
  method: PrivilegeMethod;
  active: boolean;
  user: string;
  canElevate: boolean;
  error?: string;
  details?: {
    hasRun0?: boolean;
    run0Active?: boolean;
    hasSudo?: boolean;
    sudoActive?: boolean;
    hasDoas?: boolean;
    doasActive?: boolean;
  };
}

export interface TelemetryData {
  cpuUsage: number;
  cpuModel: string;
  vCpus: number;
  memoryUsedGiB: number;
  memoryTotalGiB: number;
  arcCacheGiB: number;
  zfsTankUsedTb: number;
  zfsTankTotalTb: number;
  zfsRaidType: string;
  zfsScrubStatus: string;
  loadAvg1m: number;
  loadAvg5m: number;
  loadAvg15m: number;
  ioDelayPercent: number;
  cpuTempC: number;
  fanSpeedRpm: number;
  packageWatts: number;
  sparkline: number[];
  metricsSource?: 'glances' | 'pcp' | 'cockpit-bridge' | 'kernel';
  metricsWarning?: string;
  buffCacheGiB?: number;
}

export interface ContainerItem {
  id: string;
  podId?: string;
  podName?: string;
  name: string;
  image: string;
  status: string;
  cpu: string;
  memory: string;
  gpuAcc?: string;
  ports?: string;
  user?: string;
  uid?: number;
  rootless?: boolean;
}

export interface PodItem {
  id: string;
  name: string;
  containersRunningCount: number;
  ports: string;
  totalCpu: string;
  totalRam: string;
  gpuTag?: string;
  vram?: string;
  gpuUtil?: number;
  user?: string;
  uid?: number;
  rootless?: boolean;
  containers: ContainerItem[];
}

export interface UnprivilegedPodmanUser {
  user: string;
  uid: number;
  gid?: number;
  homeDir?: string;
  subUidRange?: string;
  linger: boolean;
  activeProcesses: string[];
  containers: ContainerItem[];
  pods?: PodItem[];
}

export interface StandaloneDaemon {
  name: string;
  subtext: string;
  status: 'running' | 'exited';
  memory: string;
  user?: string;
  uid?: number;
  rootless?: boolean;
}

export interface VMItem {
  vmid: number;
  name: string;
  subtext: string;
  status: 'RUNNING' | 'STOPPED';
  vcpus: number;
  ramGiB: number;
  type: 'linux' | 'windows';
}

export interface ResticBackupInfo {
  repoUrl: string;
  encryption: string;
  policy: string;
  retention: string;
  objectLock: boolean;
  lastSnapshotAgo: string;
  lastSnapshotId: string;
  dedupSize: string;
  newChunks: string;
}

export interface ServiceUser {
  user: string;
  uidGid: string;
  rolesGroups: string;
  authMechanism: string;
  lingerSubUid: string;
  icon: string;
}

export interface GlobalTaskItem {
  id: string;
  startTime: string;
  nodeTarget: string;
  user: string;
  description: string;
  progress: number;
  progressEta: string;
  status: 'RUNNING' | 'OK' | 'FAILED';
  category: 'active' | 'ansible' | 'syslog' | 'vault';
  logs?: string[];
}

export type LogSeverity = 'emerg' | 'alert' | 'crit' | 'err' | 'warning' | 'notice' | 'info' | 'debug';

export interface SystemLogsFilter {
  source?: 'journald' | 'dmesg' | 'audit' | 'sshd' | string;
  unit?: string;
  user?: string;
  priority?: string;
  since?: string;
  grep?: string;
  lines?: number;
}

export interface SystemLogsResponse {
  success: boolean;
  lines: string[];
  host: string;
  filter: SystemLogsFilter;
  error?: string;
}

export interface ParsedLogEntry {
  raw: string;
  id: string;
  timestamp?: string;
  hostname?: string;
  unit?: string;
  message: string;
  level: 'error' | 'warning' | 'info' | 'debug';
}

export type AnsibleModuleCategory =
  | 'system'
  | 'container'
  | 'storage'
  | 'network'
  | 'package'
  | 'database'
  | 'k8s'
  | 'custom';

export interface AnsibleTaskStep {
  name: string;
  module: string;
  parameters: Record<string, any>;
}

export interface QueuedAnsibleTask {
  id: string;
  title: string;
  targetNodeId: string;
  targetNodeName: string;
  targetHostRoute?: string;
  targetSshUser?: string;
  targetSshPort?: number;
  connection?: {
    plugin?: string; // 'connection/ssh', 'connection/tailscale', 'connection/local', 'connection/winrm', etc.
    host?: string;
    port?: number;
    user?: string;
    becomeMethod?: string;
    extraArgs?: string[];
    proxyCommand?: string;
  };
  category: AnsibleModuleCategory;
  primaryModule: string;
  steps: AnsibleTaskStep[];
  secretsMasked?: Record<string, string>;
  become: boolean;
  becomeMethod?: 'run0' | 'sudo' | 'doas' | string;
  status: 'staged' | 'running' | 'completed' | 'failed' | 'cancelled';
  createdAt: number;
  error?: string;
  executedAt?: number;
}

