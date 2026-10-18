import { DatabaseSync } from 'node:sqlite';
import fs from 'node:fs';
import path from 'node:path';
import type {
  NodeItem,
  TelemetryData,
  UnprivilegedPodmanUser,
  ServiceUser,
  ContainerItem,
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
    }
  ): void;
  addCustomIdentity(hostId: string, identity: ServiceUser): void;
  getHostContainers(hostId: string): CachedHostContainers | null;
  seedGroupVarsIfEmpty(defaults: Record<string, string>): Record<string, string>;
  upsertGroupVar(file: string, content: string): void;
  getAllGroupVars(): Record<string, string>;
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
  `);

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
    SELECT probed_users_json, identities_json, custom_identities_json, root_containers_json, updated_at
    FROM host_containers_cache WHERE host_id = ?
  `);
  const stmtUpsertContainers = db.prepare(`
    INSERT INTO host_containers_cache (
      host_id, probed_users_json, identities_json, custom_identities_json, root_containers_json, updated_at
    ) VALUES (?, ?, ?, ?, ?, ?)
    ON CONFLICT(host_id) DO UPDATE SET
      probed_users_json = excluded.probed_users_json,
      identities_json = excluded.identities_json,
      custom_identities_json = excluded.custom_identities_json,
      root_containers_json = excluded.root_containers_json,
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
      const res = stmtDeleteHost.run(id);
      return Number(res.changes) > 0;
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
      }
    ): void {
      const existing = this.getHostContainers(hostId);
      const customIdentities = existing?.customIdentities || [];
      stmtUpsertContainers.run(
        hostId,
        JSON.stringify(payload.probedUsers),
        JSON.stringify(payload.identities),
        JSON.stringify(customIdentities),
        JSON.stringify(payload.rootContainers),
        Date.now()
      );
    },

    addCustomIdentity(hostId: string, identity: ServiceUser): void {
      const existing = this.getHostContainers(hostId);
      const probedUsers = existing?.probedUsers || [];
      const identities = existing?.identities || [];
      const rootContainers = existing?.rootContainers || [];
      const customIdentities = [identity, ...(existing?.customIdentities || [])];
      stmtUpsertContainers.run(
        hostId,
        JSON.stringify(probedUsers),
        JSON.stringify(identities),
        JSON.stringify(customIdentities),
        JSON.stringify(rootContainers),
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
            updated_at: number;
          }
        | undefined;
      if (!row) return null;
      return {
        probedUsers: JSON.parse(row.probed_users_json),
        identities: JSON.parse(row.identities_json),
        customIdentities: JSON.parse(row.custom_identities_json),
        rootContainers: JSON.parse(row.root_containers_json),
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

    close(): void {
      db.close();
    },
  };
}
