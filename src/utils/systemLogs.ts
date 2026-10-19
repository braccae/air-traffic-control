import type { SystemLogsFilter, ParsedLogEntry } from '../types/index.ts';

const VALID_PRIORITIES: Record<string, string> = {
  emerg: 'emerg',
  emergency: 'emerg',
  '0': 'emerg',
  alert: 'alert',
  '1': 'alert',
  crit: 'crit',
  critical: 'crit',
  '2': 'crit',
  err: 'err',
  error: 'err',
  '3': 'err',
  warn: 'warning',
  warning: 'warning',
  '4': 'warning',
  notice: 'notice',
  '5': 'notice',
  info: 'info',
  '6': 'info',
  debug: 'debug',
  '7': 'debug',
};

const VALID_SOURCES = new Set(['journald', 'dmesg', 'audit', 'sshd']);

/**
 * Sanitizes and validates query parameters for system logs retrieval
 * to prevent command injection and ensure predictable journalctl flags.
 */
export function sanitizeLogFilter(raw: Partial<SystemLogsFilter> | Record<string, unknown> = {}): SystemLogsFilter {
  // Lines: clamp to [1, 1000], default 100
  let lines = 100;
  if (raw.lines !== undefined && raw.lines !== null && raw.lines !== '') {
    const parsed = parseInt(String(raw.lines), 10);
    if (!isNaN(parsed)) {
      lines = Math.max(1, Math.min(1000, parsed));
    }
  }

  // Source: must be in VALID_SOURCES, default journald
  const rawSource = typeof raw.source === 'string' ? raw.source.trim().toLowerCase() : '';
  const source = VALID_SOURCES.has(rawSource) ? rawSource : 'journald';

  // Unit: only allow alphanumeric, dot, hyphen, underscore, at sign
  let unit: string | undefined;
  if (typeof raw.unit === 'string' && raw.unit.trim()) {
    const trimmed = raw.unit.trim();
    if (/^[a-zA-Z0-9_@.-]+$/.test(trimmed)) {
      unit = trimmed;
    }
  }

  // User: only allow valid username characters (alphanumeric, underscore, hyphen)
  let user: string | undefined;
  if (typeof raw.user === 'string' && raw.user.trim()) {
    const trimmed = raw.user.trim();
    if (/^[a-zA-Z0-9_-]+$/.test(trimmed)) {
      user = trimmed;
    }
  }

  // Priority: map to standard journalctl priority level or empty
  let priority: string | undefined;
  if (typeof raw.priority === 'string' && raw.priority.trim()) {
    const norm = raw.priority.trim().toLowerCase();
    if (norm !== 'all' && VALID_PRIORITIES[norm]) {
      priority = VALID_PRIORITIES[norm];
    }
  }

  // Since: normalize known shorthands or accept safe date/time strings
  let since: string | undefined;
  if (typeof raw.since === 'string' && raw.since.trim()) {
    const s = raw.since.trim();
    const lower = s.toLowerCase();
    if (lower === '15m' || lower === '15 minutes ago') {
      since = '15 minutes ago';
    } else if (lower === '1h' || lower === '1 hour ago') {
      since = '1 hour ago';
    } else if (lower === '6h' || lower === '6 hours ago') {
      since = '6 hours ago';
    } else if (lower === '24h' || lower === '24 hours ago' || lower === '1d') {
      since = '24 hours ago';
    } else if (lower === '7d' || lower === '7 days ago') {
      since = '7 days ago';
    } else if (lower === 'all') {
      since = undefined;
    } else if (/^[a-zA-Z0-9\s:_-]+$/.test(s)) {
      since = s;
    }
  }

  // Grep: strip dangerous control characters and newlines
  let grep: string | undefined;
  if (typeof raw.grep === 'string' && raw.grep.trim()) {
    const cleaned = raw.grep.replace(/[\r\n\x00-\x1f\x7f]/g, '').trim();
    if (cleaned.length > 0) {
      grep = cleaned;
    }
  }

  return {
    source,
    unit,
    user,
    priority,
    since,
    grep,
    lines,
  };
}

/**
 * Escapes a string safely for POSIX shell single quotes: ' -> '\''
 */
export function escapeShellSingleQuote(str: string): string {
  return str.replace(/'/g, "'\\''");
}

/**
 * Constructs the sanitized remote execution command for journalctl/dmesg
 */
export function buildJournalctlCommand(filter: SystemLogsFilter): string {
  const sanitized = sanitizeLogFilter(filter);
  const parts: string[] = ['--no-pager', `-n ${sanitized.lines}`];

  if (sanitized.source === 'dmesg') {
    parts.push('-k');
  } else if (sanitized.source === 'audit') {
    parts.push('-u sshd');
  } else if (sanitized.source === 'sshd') {
    parts.push('-u sshd');
  }

  if (sanitized.unit && sanitized.source !== 'audit' && sanitized.source !== 'sshd') {
    parts.push(`-u ${sanitized.unit}`);
  }

  if (sanitized.user) {
    if (/^\d+$/.test(sanitized.user)) {
      parts.push(`_UID=${sanitized.user}`);
    } else if (sanitized.user === 'root') {
      parts.push('_UID=0');
    } else if (sanitized.user === 'core') {
      parts.push('_UID=1000');
    } else {
      parts.push(`_UID=$(id -u ${sanitized.user} 2>/dev/null || echo 65534)`);
    }
  }

  if (sanitized.priority) {
    parts.push(`-p ${sanitized.priority}`);
  }

  if (sanitized.since) {
    parts.push(`--since="${sanitized.since}"`);
  }

  if (sanitized.grep) {
    parts.push(`-g '${escapeShellSingleQuote(sanitized.grep)}'`);
  }

  const flags = parts.join(' ');

  if (sanitized.source === 'dmesg') {
    return `(journalctl ${flags} 2>/dev/null || sudo -n journalctl ${flags} 2>/dev/null || dmesg -T 2>/dev/null | tail -n ${sanitized.lines})`;
  }

  return `journalctl ${flags} 2>/dev/null || sudo -n journalctl ${flags} 2>&1`;
}

/**
 * Classifies log severity based on log line keywords and journal markers
 */
export function classifyLogLevel(line: string): 'error' | 'warning' | 'info' | 'debug' {
  const lower = line.toLowerCase();
  if (
    /\b(emerg|emergency|alert|crit|critical|err|error|failed|failure|fatal|panic|fault|oom-killer|segfault|denied)\b/i.test(lower) ||
    /\[\s*(err|error|crit|alert|emerg)\s*\]/i.test(lower) ||
    /\*error\*/i.test(lower)
  ) {
    return 'error';
  }
  if (
    /\b(warn|warning|warned|deprecated|timeout|timed\s*out|unreachable|retrying|backoff)\b/i.test(lower) ||
    /\[\s*warn(ing)?\s*\]/i.test(lower)
  ) {
    return 'warning';
  }
  if (
    /\b(debug|trace|verbose)\b/i.test(lower) ||
    /\[\s*(debug|trace)\s*\]/i.test(lower)
  ) {
    return 'debug';
  }
  return 'info';
}

/**
 * Parses a raw log line into structured timestamp, unit, message, and severity
 */
export function parseLogLine(raw: string, index = 0): ParsedLogEntry {
  const line = raw.trim();
  const level = classifyLogLevel(line);

  // ISO format: "2026-10-18T21:57:27-04:00 dev-pc kded6[2645]: Failed to stat..."
  const isoMatch = line.match(/^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}[^\s]*)\s+([^\s]+)\s+([^:]+):\s*(.*)$/);
  if (isoMatch) {
    return {
      id: `log-${index}`,
      raw: line,
      timestamp: isoMatch[1],
      hostname: isoMatch[2],
      unit: isoMatch[3],
      message: isoMatch[4],
      level,
    };
  }

  // Syslog format: "Oct 18 21:44:43 dev-pc sshd[1110]: drop connection..."
  const syslogMatch = line.match(/^([A-Z][a-z]{2}\s+\d+\s+\d{2}:\d{2}:\d{2})\s+([^\s]+)\s+([^:]+):\s*(.*)$/);
  if (syslogMatch) {
    return {
      id: `log-${index}`,
      raw: line,
      timestamp: syslogMatch[1],
      hostname: syslogMatch[2],
      unit: syslogMatch[3],
      message: syslogMatch[4],
      level,
    };
  }

  // Dmesg format: "[ 123.456789] message" or "[Sun Oct 18 20:46:11 2026] message"
  const dmesgMatch = line.match(/^\[([^\]]+)\]\s*(.*)$/);
  if (dmesgMatch) {
    return {
      id: `log-${index}`,
      raw: line,
      timestamp: dmesgMatch[1],
      hostname: undefined,
      unit: 'kernel',
      message: dmesgMatch[2],
      level,
    };
  }

  // Fallback
  return {
    id: `log-${index}`,
    raw: line,
    timestamp: undefined,
    hostname: undefined,
    unit: undefined,
    message: line,
    level,
  };
}

/**
 * Filters log entries client-side by text search, severity level, or unit substring
 */
export function filterLogEntries(
  entries: ParsedLogEntry[],
  filter: {
    search?: string;
    level?: 'all' | 'error' | 'warning' | 'info' | 'debug' | string;
    unit?: string;
  }
): ParsedLogEntry[] {
  let result = entries;

  if (filter.level && filter.level !== 'all') {
    if (filter.level === 'error') {
      result = result.filter(e => e.level === 'error');
    } else if (filter.level === 'warning') {
      result = result.filter(e => e.level === 'error' || e.level === 'warning');
    } else if (filter.level === 'info') {
      result = result.filter(e => e.level !== 'debug');
    } else if (filter.level === 'debug') {
      result = result.filter(e => e.level === 'debug');
    }
  }

  if (filter.unit && filter.unit.trim()) {
    const u = filter.unit.trim().toLowerCase();
    result = result.filter(e => (e.unit || '').toLowerCase().includes(u));
  }

  if (filter.search && filter.search.trim()) {
    const q = filter.search.trim().toLowerCase();
    result = result.filter(e => e.raw.toLowerCase().includes(q));
  }

  return result;
}
