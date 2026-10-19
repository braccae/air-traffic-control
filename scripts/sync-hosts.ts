import { execSync } from 'node:child_process';
import { DatabaseSync } from 'node:sqlite';
import path from 'node:path';

interface TailscalePeer {
  HostName: string;
  DNSName: string;
  TailscaleIPs: string[];
  Online: boolean;
  OS: string;
  Tags?: string[];
}

interface TailscaleStatus {
  Self?: TailscalePeer;
  Peer?: Record<string, TailscalePeer>;
}

const DB_PATH = path.resolve(process.cwd(), 'data/hosts-cache.sqlite');

function syncHosts() {
  console.log('[Sync Hosts] Fetching live Tailscale status...');
  let rawJson = '';
  try {
    rawJson = execSync('tailscale status --json', { encoding: 'utf-8', timeout: 5000 });
  } catch (err) {
    console.error('[Sync Hosts] Failed to execute tailscale status --json:', err);
    process.exit(1);
  }

  const status: TailscaleStatus = JSON.parse(rawJson);
  const peers: TailscalePeer[] = [];
  if (status.Self) peers.push(status.Self);
  if (status.Peer) peers.push(...Object.values(status.Peer));

  const db = new DatabaseSync(DB_PATH);
  db.exec('PRAGMA journal_mode = WAL;');

  const selectStmt = db.prepare(
    'SELECT id, short_name, tailscale_ip, status FROM hosts WHERE id = ? OR short_name = ? OR tailscale_host LIKE ?'
  );
  const updateStmt = db.prepare(
    'UPDATE hosts SET tailscale_ip = ?, tailscale_host = ?, status = ?, updated_at = ? WHERE id = ?'
  );

  let updatedCount = 0;
  for (const peer of peers) {
    const rawName = peer.HostName || (peer.DNSName ? peer.DNSName.split('.')[0] : '');
    const cleanId = rawName.toLowerCase().replace(/[^a-z0-9_-]/g, '-');
    if (!cleanId) continue;

    const row = selectStmt.get(`node-${cleanId}`, cleanId, `${cleanId}.%`) as
      | { id: string; short_name: string; tailscale_ip: string; status: string }
      | undefined;

    if (row) {
      const tsIp = peer.TailscaleIPs?.[0] || row.tailscale_ip;
      const tsHost = peer.DNSName ? peer.DNSName.replace(/\.$/, '') : `${cleanId}.leo-banjo.ts.net`;
      const hostStatus = peer.Online ? 'online' : 'unreachable';
      updateStmt.run(tsIp, tsHost, hostStatus, Date.now(), row.id);
      updatedCount++;
    }
  }

  console.log(`[Sync Hosts] Successfully synced ${updatedCount} cluster hosts with live Tailscale network state.`);
  db.close();
}

syncHosts();
