import { DatabaseSync } from 'node:sqlite';
import fs from 'node:fs';
import path from 'node:path';
import type {
  NodeItem,
  TelemetryData,
  UnprivilegedPodmanUser,
  ServiceUser,
  ContainerItem,
  GlobalTaskItem,
  PodItem,
  CustomFolder,
  TagInventoryChunk,
  RcloneRemoteRecord,
  RcloneMountRecord,
  VaultAuditEvent,
  VaultVariable,
} from '../types/index.ts';

export interface CachedHostTelemetry {
  telemetry: Partial<TelemetryData> & { uptime?: string; osDescription?: string };
  updatedAt: number;
}

export type CachedRootContainer = Partial<ContainerItem> & Pick<ContainerItem, 'id' | 'name' | 'image' | 'status'>;

export interface CachedHostContainers {
  probedUsers: UnprivilegedPodmanUser[];
  identities: ServiceUser[];
  customIdentities: ServiceUser[];
  rootContainers: CachedRootContainer[];
  rootPods?: PodItem[];
  hasDocker?: boolean;
  dockerContainers?: ContainerItem[];
  updatedAt: number;
}

export interface HostCacheStore {
  seedHostsIfEmpty(initialHosts: NodeItem[]): NodeItem[];
  getAllHosts(): NodeItem[];
  getHostById(id: string): NodeItem | null;
  upsertHost(node: NodeItem): void;
  upsertHosts(nodes: NodeItem[]): void;
  deleteHost(id: string): boolean;
  upsertHostTelemetry(
    hostId: string,
    telemetry: Partial<TelemetryData> & { uptime?: string; osDescription?: string }
  ): void;
  getHostTelemetry(hostId: string): CachedHostTelemetry | null;
  upsertHostContainers(
    hostId: string,
    payload: {
      probedUsers: UnprivilegedPodmanUser[];
      identities: ServiceUser[];
      rootContainers: CachedRootContainer[];
      rootPods?: PodItem[];
      hasDocker?: boolean;
      dockerContainers?: ContainerItem[];
    }
  ): void;
  addCustomIdentity(hostId: string, identity: ServiceUser): void;
  getHostContainers(hostId: string): CachedHostContainers | null;
  getHostSection<T = any>(hostId: string, section: string): { data: T; updatedAt: number } | null;
  upsertHostSection<T = any>(hostId: string, section: string, data: T): void;
  invalidateHostSections(hostId?: string): void;
  saveTask(task: GlobalTaskItem): void;
  getAllTasks(limit?: number): GlobalTaskItem[];
  updateTask(id: string, updates: Partial<GlobalTaskItem>): void;
  seedGroupVarsIfEmpty(defaults: Record<string, string>): Record<string, string>;
  upsertGroupVar(file: string, content: string): void;
  getAllGroupVars(): Record<string, string>;
  upsertCustomFolder(folder: CustomFolder): void;
  getAllCustomFolders(): CustomFolder[];
  deleteCustomFolder(id: string): boolean;
  upsertTagChunk(chunk: TagInventoryChunk): void;
  getAllTagChunks(): TagInventoryChunk[];
  deleteTagChunk(id: string): boolean;
  upsertRcloneRemote(remote: RcloneRemoteRecord): void;
  getAllRcloneRemotes(): RcloneRemoteRecord[];
  getRcloneRemote(id: string): RcloneRemoteRecord | null;
  getRcloneRemoteByName(name: string): RcloneRemoteRecord | null;
  deleteRcloneRemote(id: string): boolean;
  upsertRcloneMount(mount: RcloneMountRecord): void;
  getRcloneMounts(nodeId?: string): RcloneMountRecord[];
  getRcloneMount(nodeId: string, remoteId: string): RcloneMountRecord | null;
  deleteRcloneMount(nodeId: string, remoteId: string): boolean;
  recordVaultAuditEvent(event: VaultAuditEvent): void;
  getVaultAuditEvents(filter?: { variableName?: string; host?: string; limit?: number }): VaultAuditEvent[];
  countVaultAuditEvents(): number;
  listVaultVariables(): VaultVariable[];
  getVaultVariableBlock(name: string): string | null;
  upsertVaultVariable(name: string, vaultBlock: string): void;
  deleteVaultVariable(name: string): boolean;
  close(): void;
}

export function createHostCacheStore(dbPath = ':memory:'): HostCacheStore {
  if (dbPath !== ':memory:') {
    const dir = path.dirname(dbPath);
    if (!fs.existsSync(dir)) {
      fs.mkdirSync(dir, { recursive: true });
    }
  }

  const db = new DatabaseSync(dbPath);

  db.exec(`
    PRAGMA journal_mode = WAL;
    PRAGMA synchronous = NORMAL;
    PRAGMA foreign_keys = ON;

    CREATE TABLE IF NOT EXISTS hosts (
      id TEXT PRIMARY KEY,
      name TEXT NOT NULL,
      short_name TEXT NOT NULL,
      group_name TEXT NOT NULL,
      ip TEXT NOT NULL,
      port INTEGER NOT NULL,
      ssh_user TEXT NOT NULL,
      tailscale_ip TEXT NOT NULL,
      tailscale_host TEXT NOT NULL,
      status TEXT NOT NULL,
      sort_order INTEGER NOT NULL DEFAULT 0,
      node_json TEXT NOT NULL,
      updated_at INTEGER NOT NULL
    );

    CREATE TABLE IF NOT EXISTS host_telemetry_cache (
      host_id TEXT PRIMARY KEY,
      telemetry_json TEXT NOT NULL,
      updated_at INTEGER NOT NULL
    );

    CREATE TABLE IF NOT EXISTS host_containers_cache (
      host_id TEXT PRIMARY KEY,
      probed_users_json TEXT NOT NULL DEFAULT '[]',
      identities_json TEXT NOT NULL DEFAULT '[]',
      custom_identities_json TEXT NOT NULL DEFAULT '[]',
      root_containers_json TEXT NOT NULL DEFAULT '[]',
      updated_at INTEGER NOT NULL
    );

    CREATE TABLE IF NOT EXISTS group_vars_cache (
      file_path TEXT PRIMARY KEY,
      content TEXT NOT NULL,
      updated_at INTEGER NOT NULL
    );

    CREATE TABLE IF NOT EXISTS host_section_cache (
      host_id TEXT NOT NULL,
      section TEXT NOT NULL,
      data_json TEXT NOT NULL,
      updated_at INTEGER NOT NULL,
      PRIMARY KEY (host_id, section)
    );

    CREATE TABLE IF NOT EXISTS tasks (
      id TEXT PRIMARY KEY,
      start_time TEXT NOT NULL,
      node_target TEXT NOT NULL,
      user TEXT NOT NULL,
      description TEXT NOT NULL,
      progress INTEGER NOT NULL,
      progress_eta TEXT,
      status TEXT NOT NULL,
      category TEXT NOT NULL,
      log_json TEXT,
      updated_at INTEGER NOT NULL
    );

    CREATE TABLE IF NOT EXISTS inventory_folders (
      id TEXT PRIMARY KEY,
      name TEXT NOT NULL,
      created_at INTEGER NOT NULL
    );

    CREATE TABLE IF NOT EXISTS tag_inventory_chunks (
      id TEXT PRIMARY KEY,
      tag TEXT NOT NULL,
      ssh_user TEXT,
      vault_secret_id TEXT,
      auth_method TEXT NOT NULL,
      created_at INTEGER NOT NULL
    );

    CREATE TABLE IF NOT EXISTS rclone_remotes (
      id TEXT PRIMARY KEY,
      name TEXT NOT NULL,
      provider TEXT NOT NULL,
      type TEXT NOT NULL,
      config_json TEXT NOT NULL,
      created_at INTEGER NOT NULL,
      updated_at INTEGER NOT NULL
    );

    CREATE TABLE IF NOT EXISTS rclone_mounts (
      node_id TEXT NOT NULL,
      remote_id TEXT NOT NULL,
      remote_name TEXT NOT NULL,
      mount_target TEXT NOT NULL,
      policy TEXT NOT NULL,
      status TEXT NOT NULL,
      error TEXT,
      installed_at INTEGER NOT NULL,
      updated_at INTEGER NOT NULL,
      PRIMARY KEY (node_id, remote_id)
    );

    -- Vault decryption audit trail. Deliberately has no column for plaintext:
    -- the Secret Protection Invariant is enforced by the schema, not by trust.
    CREATE TABLE IF NOT EXISTS vault_audit_log (
      id TEXT PRIMARY KEY,
      timestamp INTEGER NOT NULL,
      variable_name TEXT NOT NULL,
      actor TEXT NOT NULL,
      host TEXT NOT NULL,
      action TEXT NOT NULL,
      password_source TEXT NOT NULL
    );

    -- Ansible Vault variables. Only the ansible-vault ciphertext block is stored:
    -- there is no plaintext column, so the Secret Protection Invariant is a
    -- property of the schema rather than of the code paths reading it.
    CREATE TABLE IF NOT EXISTS vault_variables (
      name TEXT PRIMARY KEY,
      vault_block TEXT NOT NULL,
      created_at INTEGER NOT NULL,
      updated_at INTEGER NOT NULL
    );

    CREATE INDEX IF NOT EXISTS idx_hosts_sort ON hosts(sort_order ASC, id ASC);
    CREATE INDEX IF NOT EXISTS idx_hosts_tailscale ON hosts(tailscale_host, tailscale_ip);
    CREATE INDEX IF NOT EXISTS idx_sections_host ON host_section_cache(host_id, section);
    CREATE INDEX IF NOT EXISTS idx_telemetry_updated ON host_telemetry_cache(updated_at);
    CREATE INDEX IF NOT EXISTS idx_tasks_updated ON tasks(updated_at DESC);
    CREATE INDEX IF NOT EXISTS idx_folders_name ON inventory_folders(name ASC);
    CREATE INDEX IF NOT EXISTS idx_chunks_created ON tag_inventory_chunks(created_at ASC);
    CREATE INDEX IF NOT EXISTS idx_vault_audit_ts ON vault_audit_log(timestamp DESC);
    CREATE INDEX IF NOT EXISTS idx_vault_audit_var ON vault_audit_log(variable_name);
    CREATE INDEX IF NOT EXISTS idx_vault_audit_host ON vault_audit_log(host);
  `);

  try {
    db.exec(`ALTER TABLE host_containers_cache ADD COLUMN root_pods_json TEXT DEFAULT '[]';`);
  } catch {}
  try {
    db.exec(`ALTER TABLE host_containers_cache ADD COLUMN has_docker INTEGER DEFAULT 0;`);
  } catch {}
  try {
    db.exec(`ALTER TABLE host_containers_cache ADD COLUMN docker_containers_json TEXT DEFAULT '[]';`);
  } catch {}

  const stmtCountHosts = db.prepare('SELECT COUNT(*) as cnt FROM hosts');
  const stmtSelectAllHosts = db.prepare('SELECT node_json FROM hosts ORDER BY sort_order ASC, id ASC');
  const stmtSelectHostById = db.prepare('SELECT node_json, sort_order FROM hosts WHERE id = ?');
  const stmtUpsertHost = db.prepare(`
    INSERT INTO hosts (
      id, name, short_name, group_name, ip, port, ssh_user,
      tailscale_ip, tailscale_host, status, sort_order, node_json, updated_at
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    ON CONFLICT(id) DO UPDATE SET
      name = excluded.name,
      short_name = excluded.short_name,
      group_name = excluded.group_name,
      ip = excluded.ip,
      port = excluded.port,
      ssh_user = excluded.ssh_user,
      tailscale_ip = excluded.tailscale_ip,
      tailscale_host = excluded.tailscale_host,
      status = excluded.status,
      node_json = excluded.node_json,
      updated_at = excluded.updated_at
  `);
  const stmtDeleteHost = db.prepare('DELETE FROM hosts WHERE id = ?');
  const stmtDeleteTelemetry = db.prepare('DELETE FROM host_telemetry_cache WHERE host_id = ?');
  const stmtDeleteContainers = db.prepare('DELETE FROM host_containers_cache WHERE host_id = ?');
  const stmtDeleteSections = db.prepare('DELETE FROM host_section_cache WHERE host_id = ?');

  const stmtSelectSection = db.prepare(
    'SELECT data_json, updated_at FROM host_section_cache WHERE host_id = ? AND section = ?'
  );
  const stmtUpsertSection = db.prepare(`
    INSERT INTO host_section_cache (host_id, section, data_json, updated_at)
    VALUES (?, ?, ?, ?)
    ON CONFLICT(host_id, section) DO UPDATE SET
      data_json = excluded.data_json,
      updated_at = excluded.updated_at
  `);

  const stmtUpsertTelemetry = db.prepare(`
    INSERT INTO host_telemetry_cache (host_id, telemetry_json, updated_at)
    VALUES (?, ?, ?)
    ON CONFLICT(host_id) DO UPDATE SET
      telemetry_json = excluded.telemetry_json,
      updated_at = excluded.updated_at
  `);
  const stmtSelectTelemetry = db.prepare(
    'SELECT telemetry_json, updated_at FROM host_telemetry_cache WHERE host_id = ?'
  );

  const stmtSelectContainers = db.prepare(`
    SELECT probed_users_json, identities_json, custom_identities_json, root_containers_json,
           root_pods_json, has_docker, docker_containers_json, updated_at
    FROM host_containers_cache WHERE host_id = ?
  `);
  const stmtUpsertContainers = db.prepare(`
    INSERT INTO host_containers_cache (
      host_id, probed_users_json, identities_json, custom_identities_json, root_containers_json,
      root_pods_json, has_docker, docker_containers_json, updated_at
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
    ON CONFLICT(host_id) DO UPDATE SET
      probed_users_json = excluded.probed_users_json,
      identities_json = excluded.identities_json,
      custom_identities_json = excluded.custom_identities_json,
      root_containers_json = excluded.root_containers_json,
      root_pods_json = excluded.root_pods_json,
      has_docker = excluded.has_docker,
      docker_containers_json = excluded.docker_containers_json,
      updated_at = excluded.updated_at
  `);

  const stmtCountGroupVars = db.prepare('SELECT COUNT(*) as cnt FROM group_vars_cache');
  const stmtSelectAllGroupVars = db.prepare('SELECT file_path, content FROM group_vars_cache');
  const stmtUpsertGroupVar = db.prepare(`
    INSERT INTO group_vars_cache (file_path, content, updated_at)
    VALUES (?, ?, ?)
    ON CONFLICT(file_path) DO UPDATE SET
      content = excluded.content,
      updated_at = excluded.updated_at
  `);

  const stmtSelectAllFolders = db.prepare('SELECT id, name, created_at FROM inventory_folders ORDER BY name ASC');
  const stmtUpsertFolder = db.prepare(`
    INSERT INTO inventory_folders (id, name, created_at)
    VALUES (?, ?, ?)
    ON CONFLICT(id) DO UPDATE SET name = excluded.name
  `);
  const stmtDeleteFolder = db.prepare('DELETE FROM inventory_folders WHERE id = ?');

  const stmtSelectAllTagChunks = db.prepare(`
    SELECT id, tag, ssh_user, vault_secret_id, auth_method, created_at
    FROM tag_inventory_chunks ORDER BY created_at ASC
  `);
  const stmtUpsertTagChunk = db.prepare(`
    INSERT INTO tag_inventory_chunks (id, tag, ssh_user, vault_secret_id, auth_method, created_at)
    VALUES (?, ?, ?, ?, ?, ?)
    ON CONFLICT(id) DO UPDATE SET
      tag = excluded.tag,
      ssh_user = excluded.ssh_user,
      vault_secret_id = excluded.vault_secret_id,
      auth_method = excluded.auth_method
  `);
  const stmtDeleteTagChunk = db.prepare('DELETE FROM tag_inventory_chunks WHERE id = ?');

  const stmtSelectAllRcloneRemotes = db.prepare(`
    SELECT id, name, provider, type, config_json, created_at, updated_at
    FROM rclone_remotes ORDER BY name ASC
  `);
  const stmtSelectRcloneRemoteById = db.prepare(
    'SELECT id, name, provider, type, config_json, created_at, updated_at FROM rclone_remotes WHERE id = ?'
  );
  const stmtSelectRcloneRemoteByName = db.prepare(
    'SELECT id, name, provider, type, config_json, created_at, updated_at FROM rclone_remotes WHERE name = ?'
  );
  const stmtUpsertRcloneRemote = db.prepare(`
    INSERT INTO rclone_remotes (id, name, provider, type, config_json, created_at, updated_at)
    VALUES (?, ?, ?, ?, ?, ?, ?)
    ON CONFLICT(id) DO UPDATE SET
      name = excluded.name,
      provider = excluded.provider,
      type = excluded.type,
      config_json = excluded.config_json,
      updated_at = excluded.updated_at
  `);
  const stmtDeleteRcloneRemote = db.prepare('DELETE FROM rclone_remotes WHERE id = ?');
  const stmtDeleteRcloneMountsForRemote = db.prepare('DELETE FROM rclone_mounts WHERE remote_id = ?');

  const stmtSelectRcloneMounts = db.prepare(`
    SELECT node_id, remote_id, remote_name, mount_target, policy, status, error, installed_at, updated_at
    FROM rclone_mounts ORDER BY remote_name ASC
  `);
  const stmtSelectRcloneMountsForNode = db.prepare(`
    SELECT node_id, remote_id, remote_name, mount_target, policy, status, error, installed_at, updated_at
    FROM rclone_mounts WHERE node_id = ? ORDER BY remote_name ASC
  `);
  const stmtSelectRcloneMount = db.prepare(`
    SELECT node_id, remote_id, remote_name, mount_target, policy, status, error, installed_at, updated_at
    FROM rclone_mounts WHERE node_id = ? AND remote_id = ?
  `);
  const stmtUpsertRcloneMount = db.prepare(`
    INSERT INTO rclone_mounts (
      node_id, remote_id, remote_name, mount_target, policy, status, error, installed_at, updated_at
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
    ON CONFLICT(node_id, remote_id) DO UPDATE SET
      remote_name = excluded.remote_name,
      mount_target = excluded.mount_target,
      policy = excluded.policy,
      status = excluded.status,
      error = excluded.error,
      updated_at = excluded.updated_at
  `);
  const stmtDeleteRcloneMount = db.prepare('DELETE FROM rclone_mounts WHERE node_id = ? AND remote_id = ?');

  const stmtInsertVaultAudit = db.prepare(`
    INSERT INTO vault_audit_log (id, timestamp, variable_name, actor, host, action, password_source)
    VALUES (?, ?, ?, ?, ?, ?, ?)
  `);
  const stmtSelectVaultAudit = db.prepare(`
    SELECT id, timestamp, variable_name, actor, host, action, password_source
    FROM vault_audit_log ORDER BY timestamp DESC, rowid DESC LIMIT ?
  `);
  const stmtSelectVaultAuditByVariable = db.prepare(`
    SELECT id, timestamp, variable_name, actor, host, action, password_source
    FROM vault_audit_log WHERE variable_name = ? ORDER BY timestamp DESC, rowid DESC LIMIT ?
  `);
  const stmtSelectVaultAuditByHost = db.prepare(`
    SELECT id, timestamp, variable_name, actor, host, action, password_source
    FROM vault_audit_log WHERE host = ? ORDER BY timestamp DESC, rowid DESC LIMIT ?
  `);
  const stmtSelectVaultAuditByVariableAndHost = db.prepare(`
    SELECT id, timestamp, variable_name, actor, host, action, password_source
    FROM vault_audit_log WHERE variable_name = ? AND host = ? ORDER BY timestamp DESC, rowid DESC LIMIT ?
  `);
  const stmtCountVaultAudit = db.prepare('SELECT COUNT(*) as cnt FROM vault_audit_log');

  // Note: the list statement deliberately does not select vault_block.
  const stmtListVaultVariables = db.prepare(`
    SELECT name, created_at, updated_at FROM vault_variables ORDER BY name ASC
  `);
  const stmtSelectVaultVariableBlock = db.prepare('SELECT vault_block FROM vault_variables WHERE name = ?');
  const stmtUpsertVaultVariable = db.prepare(`
    INSERT INTO vault_variables (name, vault_block, created_at, updated_at)
    VALUES (?, ?, ?, ?)
    ON CONFLICT(name) DO UPDATE SET
      vault_block = excluded.vault_block,
      updated_at = excluded.updated_at
  `);
  const stmtDeleteVaultVariable = db.prepare('DELETE FROM vault_variables WHERE name = ?');

  const mapVaultAuditRow = (r: any): VaultAuditEvent => ({
    id: String(r.id),
    timestamp: Number(r.timestamp),
    variableName: String(r.variable_name),
    actor: String(r.actor),
    host: String(r.host),
    action: String(r.action),
    passwordSource: String(r.password_source),
  });

  type RcloneRemoteRow = {
    id: string;
    name: string;
    provider: string;
    type: string;
    config_json: string;
    created_at: number;
    updated_at: number;
  };
  type RcloneMountRow = {
    node_id: string;
    remote_id: string;
    remote_name: string;
    mount_target: string;
    policy: string;
    status: string;
    error: string | null;
    installed_at: number;
    updated_at: number;
  };
  const mapRemote = (r: RcloneRemoteRow): RcloneRemoteRecord => ({
    id: r.id,
    name: r.name,
    provider: r.provider,
    type: r.type,
    configJson: r.config_json,
    createdAt: Number(r.created_at),
    updatedAt: Number(r.updated_at),
  });
  const mapMount = (r: RcloneMountRow): RcloneMountRecord => ({
    nodeId: r.node_id,
    remoteId: r.remote_id,
    remoteName: r.remote_name,
    mountTarget: r.mount_target,
    policy: r.policy as RcloneMountRecord['policy'],
    status: r.status as RcloneMountRecord['status'],
    error: r.error || undefined,
    installedAt: Number(r.installed_at),
    updatedAt: Number(r.updated_at),
  });

  function upsertSingleHost(node: NodeItem, sortOrder?: number): void {
    const existing = stmtSelectHostById.get(node.id) as { sort_order?: number } | undefined;
    const order = sortOrder ?? existing?.sort_order ?? 999;
    stmtUpsertHost.run(
      node.id,
      node.name,
      node.shortName,
      node.group,
      node.ip,
      node.port ?? 22,
      node.sshUser ?? 'core',
      node.tailscaleIp,
      node.tailscaleHost,
      node.status,
      order,
      JSON.stringify(node),
      Date.now()
    );
  }

  return {
    seedHostsIfEmpty(initialHosts: NodeItem[]): NodeItem[] {
      const row = stmtCountHosts.get() as { cnt: number };
      if (!row || Number(row.cnt) === 0) {
        initialHosts.forEach((host, idx) => upsertSingleHost(host, idx));
      }
      return this.getAllHosts();
    },

    getAllHosts(): NodeItem[] {
      const rows = stmtSelectAllHosts.all() as Array<{ node_json: string }>;
      return rows.map(r => JSON.parse(r.node_json) as NodeItem);
    },

    getHostById(id: string): NodeItem | null {
      const row = stmtSelectHostById.get(id) as { node_json: string } | undefined;
      if (!row) return null;
      return JSON.parse(row.node_json) as NodeItem;
    },

    upsertHost(node: NodeItem): void {
      upsertSingleHost(node);
    },

    upsertHosts(nodes: NodeItem[]): void {
      nodes.forEach((node, idx) => upsertSingleHost(node, idx));
    },

    deleteHost(id: string): boolean {
      stmtDeleteTelemetry.run(id);
      stmtDeleteContainers.run(id);
      stmtDeleteSections.run(id);
      const res = stmtDeleteHost.run(id);
      return Number(res.changes) > 0;
    },

    getHostSection<T = any>(hostId: string, section: string): { data: T; updatedAt: number } | null {
      const row = stmtSelectSection.get(hostId, section) as
        | { data_json: string; updated_at: number }
        | undefined;
      if (!row) return null;
      try {
        return {
          data: JSON.parse(row.data_json) as T,
          updatedAt: Number(row.updated_at),
        };
      } catch {
        return null;
      }
    },

    upsertHostSection<T = any>(hostId: string, section: string, data: T): void {
      stmtUpsertSection.run(hostId, section, JSON.stringify(data), Date.now());
    },

    upsertHostTelemetry(
      hostId: string,
      telemetry: Partial<TelemetryData> & { uptime?: string; osDescription?: string }
    ): void {
      stmtUpsertTelemetry.run(hostId, JSON.stringify(telemetry), Date.now());
    },

    getHostTelemetry(hostId: string): CachedHostTelemetry | null {
      const row = stmtSelectTelemetry.get(hostId) as
        | { telemetry_json: string; updated_at: number }
        | undefined;
      if (!row) return null;
      return {
        telemetry: JSON.parse(row.telemetry_json),
        updatedAt: Number(row.updated_at),
      };
    },

    upsertHostContainers(
      hostId: string,
      payload: {
        probedUsers: UnprivilegedPodmanUser[];
        identities: ServiceUser[];
        rootContainers: CachedRootContainer[];
        rootPods?: PodItem[];
        hasDocker?: boolean;
        dockerContainers?: ContainerItem[];
      }
    ): void {
      const existing = this.getHostContainers(hostId);
      const customIdentities = existing?.customIdentities || [];
      const rootPods = payload.rootPods ?? existing?.rootPods ?? [];
      const hasDocker = payload.hasDocker !== undefined ? payload.hasDocker : (existing?.hasDocker ?? false);
      const dockerContainers = payload.dockerContainers ?? existing?.dockerContainers ?? [];
      stmtUpsertContainers.run(
        hostId,
        JSON.stringify(payload.probedUsers),
        JSON.stringify(payload.identities),
        JSON.stringify(customIdentities),
        JSON.stringify(payload.rootContainers),
        JSON.stringify(rootPods),
        hasDocker ? 1 : 0,
        JSON.stringify(dockerContainers),
        Date.now()
      );
    },

    addCustomIdentity(hostId: string, identity: ServiceUser): void {
      const existing = this.getHostContainers(hostId);
      const probedUsers = existing?.probedUsers || [];
      const identities = existing?.identities || [];
      const rootContainers = existing?.rootContainers || [];
      const rootPods = existing?.rootPods || [];
      const hasDocker = existing?.hasDocker || false;
      const dockerContainers = existing?.dockerContainers || [];
      const customIdentities = [identity, ...(existing?.customIdentities || [])];
      stmtUpsertContainers.run(
        hostId,
        JSON.stringify(probedUsers),
        JSON.stringify(identities),
        JSON.stringify(customIdentities),
        JSON.stringify(rootContainers),
        JSON.stringify(rootPods),
        hasDocker ? 1 : 0,
        JSON.stringify(dockerContainers),
        Date.now()
      );
    },

    getHostContainers(hostId: string): CachedHostContainers | null {
      const row = stmtSelectContainers.get(hostId) as
        | {
            probed_users_json: string;
            identities_json: string;
            custom_identities_json: string;
            root_containers_json: string;
            root_pods_json?: string;
            has_docker?: number;
            docker_containers_json?: string;
            updated_at: number;
          }
        | undefined;
      if (!row) return null;
      return {
        probedUsers: JSON.parse(row.probed_users_json),
        identities: JSON.parse(row.identities_json),
        customIdentities: JSON.parse(row.custom_identities_json),
        rootContainers: JSON.parse(row.root_containers_json),
        rootPods: row.root_pods_json ? JSON.parse(row.root_pods_json) : [],
        hasDocker: Boolean(row.has_docker),
        dockerContainers: row.docker_containers_json ? JSON.parse(row.docker_containers_json) : [],
        updatedAt: Number(row.updated_at),
      };
    },

    seedGroupVarsIfEmpty(defaults: Record<string, string>): Record<string, string> {
      const row = stmtCountGroupVars.get() as { cnt: number };
      if (!row || Number(row.cnt) === 0) {
        const now = Date.now();
        for (const [file, content] of Object.entries(defaults)) {
          stmtUpsertGroupVar.run(file, content, now);
        }
      }
      return this.getAllGroupVars();
    },

    upsertGroupVar(file: string, content: string): void {
      stmtUpsertGroupVar.run(file, content, Date.now());
    },

    getAllGroupVars(): Record<string, string> {
      const rows = stmtSelectAllGroupVars.all() as Array<{ file_path: string; content: string }>;
      const out: Record<string, string> = {};
      for (const r of rows) {
        out[r.file_path] = r.content;
      }
      return out;
    },

    invalidateHostSections(hostId?: string): void {
      if (hostId) {
        db.prepare('DELETE FROM host_section_cache WHERE host_id = ?').run(hostId);
      } else {
        db.prepare('DELETE FROM host_section_cache').run();
      }
    },

    saveTask(task: GlobalTaskItem): void {
      db.prepare(`
        INSERT INTO tasks (id, start_time, node_target, user, description, progress, progress_eta, status, category, log_json, updated_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(id) DO UPDATE SET
          progress = excluded.progress,
          progress_eta = excluded.progress_eta,
          status = excluded.status,
          log_json = excluded.log_json,
          updated_at = excluded.updated_at
      `).run(
        task.id,
        task.startTime,
        task.nodeTarget,
        task.user,
        task.description,
        task.progress,
        task.progressEta || '',
        task.status,
        task.category,
        task.logs ? JSON.stringify(task.logs) : '[]',
        Date.now()
      );
    },

    getAllTasks(limit = 50): GlobalTaskItem[] {
      const rows = db.prepare(`SELECT * FROM tasks ORDER BY updated_at DESC LIMIT ?`).all(limit) as Array<{
        id: string;
        start_time: string;
        node_target: string;
        user: string;
        description: string;
        progress: number;
        progress_eta: string;
        status: string;
        category: string;
        log_json: string;
      }>;
      return rows.map(r => ({
        id: r.id,
        startTime: r.start_time,
        nodeTarget: r.node_target,
        user: r.user,
        description: r.description,
        progress: Number(r.progress),
        progressEta: r.progress_eta || '',
        status: r.status as any,
        category: r.category as any,
        logs: r.log_json ? JSON.parse(r.log_json) : [],
      }));
    },

    updateTask(id: string, updates: Partial<GlobalTaskItem>): void {
      const existing = db.prepare('SELECT * FROM tasks WHERE id = ?').get(id) as any;
      if (!existing) return;
      const progress = updates.progress ?? existing.progress;
      const progressEta = updates.progressEta ?? existing.progress_eta;
      const status = updates.status ?? existing.status;
      const logs = updates.logs ? JSON.stringify(updates.logs) : existing.log_json;
      db.prepare(`
        UPDATE tasks SET progress = ?, progress_eta = ?, status = ?, log_json = ?, updated_at = ? WHERE id = ?
      `).run(progress, progressEta, status, logs, Date.now(), id);
    },

    upsertCustomFolder(folder: CustomFolder): void {
      stmtUpsertFolder.run(folder.id, folder.name, folder.createdAt);
    },

    getAllCustomFolders(): CustomFolder[] {
      const rows = stmtSelectAllFolders.all() as Array<{ id: string; name: string; created_at: number }>;
      return rows.map(r => ({
        id: r.id,
        name: r.name,
        createdAt: Number(r.created_at),
      }));
    },

    deleteCustomFolder(id: string): boolean {
      db.exec('BEGIN IMMEDIATE');
      try {
        const hosts = this.getAllHosts();
        for (const host of hosts) {
          if (host.folder === id) {
            delete host.folder;
            upsertSingleHost(host);
          }
        }
        const res = stmtDeleteFolder.run(id);
        db.exec('COMMIT');
        return Number(res.changes) > 0;
      } catch (err) {
        db.exec('ROLLBACK');
        throw err;
      }
    },

    upsertTagChunk(chunk: TagInventoryChunk): void {
      stmtUpsertTagChunk.run(
        chunk.id,
        chunk.tag,
        chunk.sshUser || null,
        chunk.vaultSecretId || null,
        chunk.authMethod,
        chunk.createdAt
      );
    },

    getAllTagChunks(): TagInventoryChunk[] {
      const rows = stmtSelectAllTagChunks.all() as Array<{
        id: string;
        tag: string;
        ssh_user: string | null;
        vault_secret_id: string | null;
        auth_method: string;
        created_at: number;
      }>;
      return rows.map(r => ({
        id: r.id,
        tag: r.tag,
        sshUser: r.ssh_user || undefined,
        vaultSecretId: r.vault_secret_id || undefined,
        authMethod: r.auth_method as TagInventoryChunk['authMethod'],
        createdAt: Number(r.created_at),
      }));
    },

    deleteTagChunk(id: string): boolean {
      const res = stmtDeleteTagChunk.run(id);
      return Number(res.changes) > 0;
    },

    upsertRcloneRemote(remote: RcloneRemoteRecord): void {
      stmtUpsertRcloneRemote.run(
        remote.id,
        remote.name,
        remote.provider,
        remote.type,
        remote.configJson,
        remote.createdAt,
        remote.updatedAt
      );
    },

    getAllRcloneRemotes(): RcloneRemoteRecord[] {
      const rows = stmtSelectAllRcloneRemotes.all() as RcloneRemoteRow[];
      return rows.map(mapRemote);
    },

    getRcloneRemote(id: string): RcloneRemoteRecord | null {
      const row = stmtSelectRcloneRemoteById.get(id) as RcloneRemoteRow | undefined;
      return row ? mapRemote(row) : null;
    },

    getRcloneRemoteByName(name: string): RcloneRemoteRecord | null {
      const row = stmtSelectRcloneRemoteByName.get(name) as RcloneRemoteRow | undefined;
      return row ? mapRemote(row) : null;
    },

    deleteRcloneRemote(id: string): boolean {
      db.exec('BEGIN IMMEDIATE');
      try {
        stmtDeleteRcloneMountsForRemote.run(id);
        const res = stmtDeleteRcloneRemote.run(id);
        db.exec('COMMIT');
        return Number(res.changes) > 0;
      } catch (err) {
        db.exec('ROLLBACK');
        throw err;
      }
    },

    upsertRcloneMount(mount: RcloneMountRecord): void {
      stmtUpsertRcloneMount.run(
        mount.nodeId,
        mount.remoteId,
        mount.remoteName,
        mount.mountTarget,
        mount.policy,
        mount.status,
        mount.error || null,
        mount.installedAt,
        mount.updatedAt
      );
    },

    getRcloneMounts(nodeId?: string): RcloneMountRecord[] {
      const rows = nodeId
        ? (stmtSelectRcloneMountsForNode.all(nodeId) as RcloneMountRow[])
        : (stmtSelectRcloneMounts.all() as RcloneMountRow[]);
      return rows.map(mapMount);
    },

    getRcloneMount(nodeId: string, remoteId: string): RcloneMountRecord | null {
      const row = stmtSelectRcloneMount.get(nodeId, remoteId) as RcloneMountRow | undefined;
      return row ? mapMount(row) : null;
    },

    deleteRcloneMount(nodeId: string, remoteId: string): boolean {
      const res = stmtDeleteRcloneMount.run(nodeId, remoteId);
      return Number(res.changes) > 0;
    },

    recordVaultAuditEvent(event: VaultAuditEvent): void {
      stmtInsertVaultAudit.run(
        event.id,
        event.timestamp,
        event.variableName,
        event.actor,
        event.host,
        event.action,
        event.passwordSource
      );
    },

    getVaultAuditEvents(
      filter?: { variableName?: string; host?: string; limit?: number }
    ): VaultAuditEvent[] {
      const rawLimit = Number(filter?.limit ?? 200);
      const limit = Number.isFinite(rawLimit) ? Math.min(Math.max(Math.trunc(rawLimit), 1), 1000) : 200;
      const variableName = filter?.variableName?.trim();
      const host = filter?.host?.trim();

      let rows: any[];
      if (variableName && host) {
        rows = stmtSelectVaultAuditByVariableAndHost.all(variableName, host, limit) as any[];
      } else if (variableName) {
        rows = stmtSelectVaultAuditByVariable.all(variableName, limit) as any[];
      } else if (host) {
        rows = stmtSelectVaultAuditByHost.all(host, limit) as any[];
      } else {
        rows = stmtSelectVaultAudit.all(limit) as any[];
      }
      return rows.map(mapVaultAuditRow);
    },

    countVaultAuditEvents(): number {
      const row = stmtCountVaultAudit.get() as { cnt: number } | undefined;
      return Number(row?.cnt ?? 0);
    },

    listVaultVariables(): VaultVariable[] {
      const rows = stmtListVaultVariables.all() as Array<{
        name: string;
        created_at: number;
        updated_at: number;
      }>;
      return rows.map(r => ({
        name: String(r.name),
        createdAt: Number(r.created_at),
        updatedAt: Number(r.updated_at),
      }));
    },

    getVaultVariableBlock(name: string): string | null {
      const row = stmtSelectVaultVariableBlock.get(name) as { vault_block: string } | undefined;
      return row ? String(row.vault_block) : null;
    },

    upsertVaultVariable(name: string, vaultBlock: string): void {
      const now = Date.now();
      stmtUpsertVaultVariable.run(name, vaultBlock, now, now);
    },

    deleteVaultVariable(name: string): boolean {
      const res = stmtDeleteVaultVariable.run(name);
      return Number(res.changes) > 0;
    },

    close(): void {
      db.close();
    },
  };
}

export interface InventoryRoutesOptions {
  hostCache: HostCacheStore;
  broadcast?: (msg: any) => void;
  getNodes?: () => NodeItem[];
  setNodes?: (nodes: NodeItem[]) => void;
}

export function registerInventoryRoutes(
  app: any,
  options: InventoryRoutesOptions
): void {
  const { hostCache, broadcast, getNodes, setNodes } = options;

  app.get('/api/inventory/chunks', (_req: any, res: any) => {
    res.json({ success: true, chunks: hostCache.getAllTagChunks() });
  });

  const VALID_AUTH_METHODS = new Set(['tailscale-ssh', 'vault-password', 'vault-sshkey', 'ssh-agent']);
  app.post('/api/inventory/chunks', (req: any, res: any) => {
    const { tag, authMethod, sshUser, vaultSecretId, id, createdAt } = req.body || {};
    if (!tag || !authMethod || !VALID_AUTH_METHODS.has(authMethod)) {
      res.status(400).json({ success: false, error: 'Valid tag and authMethod are required' });
      return;
    }
    const chunk: TagInventoryChunk = {
      id: id || `chunk-${tag}`,
      tag,
      sshUser: sshUser || undefined,
      vaultSecretId: vaultSecretId || undefined,
      authMethod,
      createdAt: createdAt || Date.now(),
    };
    hostCache.upsertTagChunk(chunk);
    const chunks = hostCache.getAllTagChunks();
    if (broadcast) {
      broadcast({ type: 'chunks_updated', chunks });
    }
    res.json({ success: true, chunk });
  });

  app.delete('/api/inventory/chunks/:id', (req: any, res: any) => {
    const deleted = hostCache.deleteTagChunk(req.params.id);
    const chunks = hostCache.getAllTagChunks();
    if (broadcast) {
      broadcast({ type: 'chunks_updated', chunks });
    }
    res.json({ success: true, deleted });
  });

  app.get('/api/inventory/folders', (_req: any, res: any) => {
    res.json({ success: true, folders: hostCache.getAllCustomFolders() });
  });

  app.post('/api/inventory/folders', (req: any, res: any) => {
    const { name, id, createdAt } = req.body || {};
    if (!name || typeof name !== 'string' || !name.trim()) {
      res.status(400).json({ success: false, error: 'name is required' });
      return;
    }
    const folder: CustomFolder = {
      id: id || 'folder-' + Date.now(),
      name: name.trim(),
      createdAt: createdAt || Date.now(),
    };
    hostCache.upsertCustomFolder(folder);
    const folders = hostCache.getAllCustomFolders();
    if (broadcast) {
      broadcast({ type: 'folders_updated', folders });
    }
    res.json({ success: true, folder });
  });

  app.delete('/api/inventory/folders/:id', (req: any, res: any) => {
    const deleted = hostCache.deleteCustomFolder(req.params.id);
    if (setNodes && getNodes) {
      setNodes(hostCache.getAllHosts());
    }
    const folders = hostCache.getAllCustomFolders();
    if (broadcast) {
      broadcast({ type: 'folders_updated', folders });
      broadcast({ type: 'nodes_updated', nodes: hostCache.getAllHosts() });
    }
    res.json({ success: true, deleted });
  });
}

