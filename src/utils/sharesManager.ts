/**
 * sharesManager.ts - Logic and utilities for SMB, NFS, and Linux User management.
 */

export interface NfsShareConfig {
  path: string;
  client?: string;
  options?: string;
}

export interface SmbShareConfig {
  name: string;
  path: string;
  comment?: string;
  readOnly: boolean;
  validUsers?: string;
  guestOk: boolean;
  browseable?: boolean;
}

export interface HostUserItem {
  username: string;
  uid: number;
  gid: number;
  gecos: string;
  home: string;
  shell: string;
  isSystemUser: boolean;
}

/**
 * Escapes single quotes for safe shell command interpolation.
 */
export function escapeShellSingleQuote(str: string): string {
  return str.replace(/'/g, "'\\''");
}

/**
 * Validates and sanitizes a Linux username.
 * Disallows spaces, quotes, shell metacharacters, or names starting with numbers/symbols.
 */
export function sanitizeUsername(username: string): string {
  const trimmed = (username || '').trim();
  if (!trimmed) {
    throw new Error('Username cannot be empty');
  }
  // POSIX / Linux username convention: lowercase/uppercase, digits, underscores, dashes
  // Cannot start with a dash or digit; max 32 chars
  if (!/^[a-zA-Z_][a-zA-Z0-9_-]{0,31}$/.test(trimmed)) {
    throw new Error(
      `Invalid username "${trimmed}". Usernames must start with a letter or underscore and contain only letters, numbers, underscores, or dashes (max 32 chars).`
    );
  }
  return trimmed;
}

/**
 * Validates and sanitizes an absolute filesystem path for shares.
 */
export function sanitizePath(rawPath: string): string {
  const trimmed = (rawPath || '').trim();
  if (!trimmed) {
    throw new Error('Path cannot be empty');
  }
  if (!trimmed.startsWith('/')) {
    throw new Error(`Path must be an absolute path starting with "/": "${trimmed}"`);
  }
  // Disallow control characters, newlines, null bytes, backticks, semicolons, ampersands, pipes, or dollar expansions
  if (/[\0\r\n;`$&|<>"]/.test(trimmed)) {
    throw new Error(`Path contains invalid shell characters: "${trimmed}"`);
  }
  // Normalize consecutive slashes
  return trimmed.replace(/\/+/g, '/');
}

/**
 * Validates and sanitizes an SMB share section name.
 */
export function sanitizeShareName(name: string): string {
  const trimmed = (name || '').trim();
  if (!trimmed) {
    throw new Error('Share name cannot be empty');
  }
  if (trimmed.length > 80) {
    throw new Error('Share name cannot exceed 80 characters');
  }
  // Disallow reserved names
  const reserved = ['global', 'printers', 'print$', 'homes'];
  if (reserved.includes(trimmed.toLowerCase())) {
    throw new Error(`Share name "${trimmed}" is a reserved Samba configuration section`);
  }
  // Only alphanumeric, hyphens, underscores, dots, and spaces (standard Windows/Samba naming)
  if (!/^[a-zA-Z0-9_\-\. ]+$/.test(trimmed)) {
    throw new Error(`Share name contains invalid characters: "${trimmed}"`);
  }
  return trimmed;
}

/**
 * Validates and sanitizes NFS client host / CIDR / wildcard expression.
 */
export function sanitizeNfsClient(client?: string): string {
  const trimmed = (client || '').trim();
  if (!trimmed || trimmed === '*') return '*';

  // Can be multiple clients separated by whitespace
  const tokens = trimmed.split(/\s+/);
  for (const token of tokens) {
    // Valid tokens: CIDR (100.64.0.0/10, 192.168.1.0/24), IP (192.168.1.5), hostname (foo.bar), netgroup (@group), wildcard (*.lab)
    if (!/^[a-zA-Z0-9_.*@\-\/]+$/.test(token)) {
      throw new Error(`Invalid NFS client specifier: "${token}"`);
    }
  }
  return tokens.join(' ');
}

/**
 * Validates and sanitizes NFS export options (e.g. rw,sync,no_subtree_check,no_root_squash).
 */
export function sanitizeNfsOptions(options?: string): string {
  const defaultOpts = 'rw,sync,no_subtree_check,no_root_squash';
  const trimmed = (options || '').trim();
  if (!trimmed) return defaultOpts;

  // Split by comma
  const parts = trimmed.split(',').map(p => p.trim()).filter(Boolean);
  for (const part of parts) {
    // Each option should be alphanumeric, with optional = or - (e.g. anonuid=1000, secure, insecure)
    if (!/^[a-zA-Z0-9_\-=]+$/.test(part)) {
      throw new Error(`Invalid NFS export option: "${part}"`);
    }
  }
  return parts.join(',');
}

/**
 * Generates an NFS export entry line for /etc/exports or /etc/exports.d/*.exports.
 */
export function generateNfsExportLine(config: NfsShareConfig): string {
  const cleanPath = sanitizePath(config.path);
  const cleanClient = sanitizeNfsClient(config.client);
  const cleanOptions = sanitizeNfsOptions(config.options);

  // If cleanClient contains multiple space-separated clients, attach options to each
  const clientTokens = cleanClient.split(/\s+/);
  const exportClients = clientTokens.map(c => `${c}(${cleanOptions})`).join(' ');

  return `${cleanPath} ${exportClients}`;
}

/**
 * Generates an SMB share configuration block in INI format for smb.conf.
 */
export function generateSmbShareBlock(config: SmbShareConfig): string {
  const cleanName = sanitizeShareName(config.name);
  const cleanPath = sanitizePath(config.path);
  const comment = (config.comment || '').trim().replace(/[\r\n]+/g, ' ');
  const readOnlyStr = config.readOnly ? 'yes' : 'no';
  const guestOkStr = config.guestOk ? 'yes' : 'no';
  const browseableStr = config.browseable !== false ? 'yes' : 'no';

  const lines = [
    `[${cleanName}]`,
    `   path = ${cleanPath}`,
    `   read only = ${readOnlyStr}`,
    `   guest ok = ${guestOkStr}`,
    `   browseable = ${browseableStr}`,
    `   create mask = 0664`,
    `   directory mask = 0775`,
  ];

  if (comment) {
    lines.splice(1, 0, `   comment = ${comment}`);
  }

  if (config.validUsers && config.validUsers.trim()) {
    // Sanitize valid users: list of usernames
    const users = config.validUsers
      .split(/[\s,]+/)
      .map(u => u.trim())
      .filter(Boolean)
      .map(u => {
        // Can be @group or username
        if (u.startsWith('@')) {
          return `@${sanitizeUsername(u.substring(1))}`;
        }
        return sanitizeUsername(u);
      })
      .join(' ');

    if (users) {
      lines.push(`   valid users = ${users}`);
    }
  }

  return lines.join('\n');
}

/**
 * Builds the shell command to create/update an NFS export and reload exportfs.
 */
export function buildNfsSaveCommand(config: NfsShareConfig, shareName?: string): string {
  const line = generateNfsExportLine(config);
  const cleanPath = sanitizePath(config.path);
  const cleanName = (shareName || cleanPath.split('/').filter(Boolean).pop() || 'share')
    .toLowerCase()
    .replace(/[^a-z0-9_-]/g, '_');

  const escapedLine = escapeShellSingleQuote(line);

  // We write to /etc/exports.d/cockpit-<cleanName>.exports, create parent dir if needed,
  // and run sudo exportfs -ra. Also ensure fallback to /etc/exports if /etc/exports.d is not supported.
  return [
    `sudo mkdir -p /etc/exports.d`,
    `printf '%s\\n' '${escapedLine}' | sudo tee /etc/exports.d/cockpit-${cleanName}.exports >/dev/null`,
    `sudo exportfs -ra`,
  ].join(' && ');
}

/**
 * Builds the shell command to create/update an SMB share definition and reload samba.
 */
export function buildSmbSaveCommand(config: SmbShareConfig): string {
  const block = generateSmbShareBlock(config);
  const cleanName = sanitizeShareName(config.name);
  const escapedBlock = escapeShellSingleQuote(block);

  // Python helper script to update or append section in /etc/samba/smb.conf
  const pythonScript = [
    `import sys, re`,
    `conf_path = '/etc/samba/smb.conf'`,
    `try:`,
    `    with open(conf_path, 'r') as f: content = f.read()`,
    `except Exception: content = ''`,
    `sec_name = ${JSON.stringify(cleanName)}`,
    `pattern = r'(?ms)^\s*\\[' + re.escape(sec_name) + r'\\]\\s*$.*?(?=^\\s*\\[|\\Z)'`,
    `new_block = sys.stdin.read().strip() + '\\n'`,
    `if re.search(pattern, content):`,
    `    content = re.sub(pattern, new_block, content)`,
    `else:`,
    `    if content and not content.endswith('\\n'): content += '\\n'`,
    `    content += '\\n' + new_block`,
    `tmp_path = sys.argv[1] if len(sys.argv) > 1 else '/tmp/smb.conf.$$.tmp'`,
    `with open(tmp_path, 'w') as f: f.write(content)`,
  ].join('\n');

  const escapedPython = escapeShellSingleQuote(pythonScript);

  return [
    `TMP_FILE="/tmp/smb.conf.$$.tmp"`,
    `printf '%s\\n' '${escapedBlock}' | python3 -c '${escapedPython}' "$TMP_FILE"`,
    `sudo cp /etc/samba/smb.conf /etc/samba/smb.conf.bak 2>/dev/null || true`,
    `sudo mv "$TMP_FILE" /etc/samba/smb.conf`,
    `sudo smbcontrol all reload-config 2>&1 || sudo systemctl reload smbd smb 2>&1 || sudo systemctl restart smbd smb 2>&1 || true`,
  ].join(' && ');
}

/**
 * Parses /etc/passwd contents into structured HostUserItem list.
 */
export function parseEtcPasswd(passwdContent: string, subuidContent = ''): HostUserItem[] {
  if (!passwdContent) return [];

  const subuidUsers = new Set<string>();
  if (subuidContent) {
    for (const line of subuidContent.split('\n')) {
      const trimmed = line.trim();
      if (!trimmed || trimmed.startsWith('#')) continue;
      const parts = trimmed.split(':');
      if (parts[0]) subuidUsers.add(parts[0].trim());
    }
  }

  const lines = passwdContent.split('\n');
  const results: HostUserItem[] = [];

  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#')) continue;

    const parts = trimmed.split(':');
    if (parts.length < 7) continue;

    const [username, , uidStr, gidStr, gecos, home, shell] = parts;
    const uid = parseInt(uidStr, 10);
    const gid = parseInt(gidStr, 10);

    if (isNaN(uid) || isNaN(gid)) continue;

    // Distinguish system users vs regular users:
    // Regular users typically UID >= 1000 and not nobody (65534)
    // Or users configured in subuid
    const isNobody = uid === 65534;
    const hasInteractiveShell = !shell.endsWith('/nologin') && !shell.endsWith('/false');
    const isInSubuid = subuidUsers.has(username);
    const isSystemUser = (uid < 1000 || isNobody) && !isInSubuid;

    results.push({
      username,
      uid,
      gid,
      gecos: gecos || '',
      home: home || '',
      shell: shell || '',
      isSystemUser,
    });
  }

  return results;
}

/**
 * Builds user creation command.
 */
export function buildUserCreateCommand(username: string): string {
  const cleanUser = sanitizeUsername(username);
  return `sudo useradd -m -s /bin/bash '${cleanUser}'`;
}

/**
 * Builds command to set regular Linux password via chpasswd.
 */
export function buildUserPasswdCommand(username: string, password: string): string {
  const cleanUser = sanitizeUsername(username);
  if (!password) {
    throw new Error('Password cannot be empty');
  }
  if (/[\0\r\n]/.test(password)) {
    throw new Error('Password contains invalid control characters');
  }
  const escapedPassword = escapeShellSingleQuote(password);
  return `printf '%s:%s\\n' '${cleanUser}' '${escapedPassword}' | sudo chpasswd`;
}

/**
 * Builds command to set Samba (SMB) password via smbpasswd.
 */
export function buildSmbPasswdCommand(username: string, password: string): string {
  const cleanUser = sanitizeUsername(username);
  if (!password) {
    throw new Error('Password cannot be empty');
  }
  if (/[\0\r\n]/.test(password)) {
    throw new Error('Password contains invalid control characters');
  }
  const escapedPassword = escapeShellSingleQuote(password);
  return `(printf '%s\\n' '${escapedPassword}'; printf '%s\\n' '${escapedPassword}') | sudo smbpasswd -s -a '${cleanUser}'`;
}
