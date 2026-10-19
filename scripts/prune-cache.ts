import { DatabaseSync } from 'node:sqlite';
import path from 'node:path';

const DB_PATH = path.resolve(process.cwd(), 'data/hosts-cache.sqlite');

function pruneCache() {
  console.log(`[Cache Pruner] Opening database at ${DB_PATH}`);
  const db = new DatabaseSync(DB_PATH);
  db.exec('PRAGMA journal_mode = WAL;');
  db.exec('PRAGMA synchronous = NORMAL;');

  const now = Date.now();
  // Section cache max age: 2 hours (7200000 ms)
  const maxSectionAge = now - 7200000;
  // Telemetry cache max age: 10 minutes (600000 ms)
  const maxTelemetryAge = now - 600000;

  const stmtSection = db.prepare('DELETE FROM host_section_cache WHERE updated_at < ?');
  const sectionResult = stmtSection.run(maxSectionAge);

  const stmtTelemetry = db.prepare('DELETE FROM host_telemetry_cache WHERE updated_at < ?');
  const telemetryResult = stmtTelemetry.run(maxTelemetryAge);

  console.log(`[Cache Pruner] Pruned ${sectionResult.changes} expired section cache records.`);
  console.log(`[Cache Pruner] Pruned ${telemetryResult.changes} expired telemetry records.`);

  db.exec('PRAGMA optimize;');
  console.log('[Cache Pruner] SQLite database optimized.');
  db.close();
}

try {
  pruneCache();
} catch (err) {
  console.error('[Cache Pruner] Error running cache prune:', err);
  process.exit(1);
}
