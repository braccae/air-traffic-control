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
  composeProject?: string;
  composeService?: string;
  composeConfigFiles?: string;
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

/** rclone mount strategies. The chosen strategy is what actually gets installed. */
export type RcloneMountPolicy = 'systemd-mount' | 'systemd-automount' | 'fstab' | 'script';

/** A remote stored in the ATC database. Secret field values in `configJson` are encrypted at rest. */
export interface RcloneRemoteRecord {
  id: string;
  name: string;
  provider: string;
  type: string;
  configJson: string;
  createdAt: number;
  updatedAt: number;
}

/** An installed (or pending) remote mount on a specific client. */
export interface RcloneMountRecord {
  nodeId: string;
  remoteId: string;
  remoteName: string;
  mountTarget: string;
  policy: RcloneMountPolicy;
  status: 'pending' | 'installed' | 'failed';
  error?: string;
  installedAt: number;
  updatedAt: number;
}

/**
 * Metadata for one stored vault variable. Deliberately has no value/ciphertext
 * field: the list API never exposes secret material (Secret Protection Invariant).
 */
export interface VaultVariable {
  /** Variable name as it would appear in Ansible, e.g. `restic_password`. */
  name: string;
  createdAt: number;
  updatedAt: number;
}

/**
 * One audit line for a vault decryption. Records the variable NAME and metadata
 * only — never the decrypted plaintext (see the Secret Protection Invariant).
 */
export interface VaultAuditEvent {
  id: string;
  /** Epoch milliseconds when the decryption completed. */
  timestamp: number;
  /** Vault variable name (e.g. `api_key`). Never the value. */
  variableName: string;
  /** Acting user / request context (OS user, API client label, or 'system'). */
  actor: string;
  /** Target host the secret was destined for, or 'n/a'. */
  host: string;
  /** Task / action that triggered the decryption (route or playbook step). */
  action: string;
  /** Which password source resolved the decryption. */
  passwordSource: 'file' | 'env' | 'ask_password' | 'inline' | string;
}

/**
 * A global ("datacenter-wide") settings page contributed by an addon module.
 * Mirrors backend-go/pkg/plugin.SettingsPage plus the current persisted values.
 */
export type GlobalSettingsFieldType = 'string' | 'text' | 'int' | 'bool' | 'select' | 'secret';

export interface GlobalSettingsFieldOption {
  value: string;
  label: string;
}

export interface GlobalSettingsField {
  name: string;
  label: string;
  type: GlobalSettingsFieldType;
  description?: string;
  placeholder?: string;
  default?: unknown;
  required?: boolean;
  secret?: boolean;
  options?: GlobalSettingsFieldOption[];
  min?: number;
  max?: number;
}

export interface GlobalSettingsPage {
  id: string;
  title: string;
  icon: string;
  description: string;
  order: number;
  fields: GlobalSettingsField[];
  values: Record<string, unknown>;
}

/**
 * Runs an existing playbook file (ansible/playbooks/<playbook>) as a queue task
 * instead of compiling an ephemeral playbook from `steps`. The queue runner
 * still executes it with ANSIBLE_STDOUT_CALLBACK=json.
 */
export interface QueuedPlaybookRun {
  /** File name inside ansible/playbooks, e.g. `site.yml`. */
  playbook: string;
  /** Inventory limit — `all` or a tag/group/host pattern. */
  target: string;
  checkMode?: boolean;
  tags?: string;
  extraVars?: string | Record<string, unknown>;
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
  /** Present when this task runs a playbook file rather than compiled steps. */
  playbookRun?: QueuedPlaybookRun;
  secretsMasked?: Record<string, string>;
  become: boolean;
  becomeMethod?: 'run0' | 'sudo' | 'doas' | string;
  status: 'staged' | 'running' | 'completed' | 'failed' | 'cancelled';
  createdAt: number;
  error?: string;
  executedAt?: number;
}

