export type NodeType = 'linux' | 'windows' | 'router_switch' | 'pbs' | 'lxc' | 'k3s';

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
  tailscaleIp: string;
  tailscaleHost: string;
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
}

export interface ContainerItem {
  id: string;
  podId?: string;
  name: string;
  image: string;
  status: string;
  cpu: string;
  memory: string;
  gpuAcc?: string;
  ports?: string;
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
  containers: ContainerItem[];
}

export interface StandaloneDaemon {
  name: string;
  subtext: string;
  status: 'running' | 'exited';
  memory: string;
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
}
