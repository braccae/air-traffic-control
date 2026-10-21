/**
 * rclonePolicies.ts — pure, browser-safe helpers for rclone remotes.
 *
 * This module deliberately imports nothing from Node (no fs/crypto/child_process)
 * so the client view can render mount previews without dragging server code into
 * the browser bundle. Both the coordinator (rcloneManager) and the React view
 * import from here.
 */
import type { RcloneMountPolicy } from '../types/index.ts';

export const MOUNT_POLICIES: RcloneMountPolicy[] = [
  'systemd-mount',
  'systemd-automount',
  'fstab',
  'script',
];

export const DEFAULT_RCLONE_DROPIN_FILE = 'atc-rclone.conf';

/** rclone remote names become INI section headers: keep them strict. */
export function sanitizeRemoteName(name: string): string {
  const trimmed = (name || '').trim();
  if (!trimmed) throw new Error('Remote name cannot be empty');
  if (trimmed.length > 64) throw new Error('Remote name cannot exceed 64 characters');
  if (!/^[A-Za-z0-9._-]+$/.test(trimmed)) {
    throw new Error(`Invalid remote name "${trimmed}": use letters, digits, dash, underscore, dot`);
  }
  return trimmed;
}

/** Mount targets must be safe absolute paths (they become systemd unit names / fstab fields). */
export function sanitizeMountTarget(target: string): string {
  const trimmed = (target || '').trim();
  if (!trimmed) throw new Error('Mount target cannot be empty');
  if (!trimmed.startsWith('/')) throw new Error(`Mount target must be an absolute path: "${trimmed}"`);
  if (/[\0\r\n;`$&|<>"]/.test(trimmed)) {
    throw new Error(`Mount target contains invalid characters: "${trimmed}"`);
  }
  return trimmed.replace(/\/+/g, '/');
}

export function isValidMountPolicy(policy: string): policy is RcloneMountPolicy {
  return (MOUNT_POLICIES as string[]).includes(policy);
}

/** Reject config values that would break out of an INI section or a unit line. */
export function sanitizeConfigValue(value: string): string {
  const trimmed = (value || '').trim();
  if (/[\r\n\0]/.test(trimmed)) {
    throw new Error('Config values must not contain newlines or control characters');
  }
  return trimmed;
}

function escapeUnitPathComponent(part: string): string {
  let out = '';
  for (let i = 0; i < part.length; i++) {
    const ch = part[i];
    const code = part.charCodeAt(i);
    const isAlphaNum =
      (code >= 97 && code <= 122) ||
      (code >= 65 && code <= 90) ||
      (code >= 48 && code <= 57);
    if (isAlphaNum || ch === ':' || (ch === '.' && i > 0)) {
      out += ch;
    } else {
      out += `\\x${code.toString(16).padStart(2, '0')}`;
    }
  }
  return out;
}

/**
 * Converts an absolute mount target path into systemd's canonical unit stem
 * (matching `systemd-escape -p --suffix=mount <target>` and `backend-go/plugins/mount/rclone`).
 */
export function escapeSystemdMountUnitBase(target: string): string {
  const clean = sanitizeMountTarget(target).replace(/^\/+|\/+$/g, '');
  if (!clean) return '-';
  return clean
    .split('/')
    .filter(Boolean)
    .map(escapeUnitPathComponent)
    .join('-');
}

export function buildSystemdMountUnitName(target: string): string {
  return `${escapeSystemdMountUnitBase(target)}.mount`;
}

export function buildSystemdAutomountUnitName(target: string): string {
  return `${escapeSystemdMountUnitBase(target)}.automount`;
}

export function buildSystemdServiceUnitName(name: string): string {
  return `rclone-${sanitizeRemoteName(name)}.service`;
}

export function buildSystemdDropInPath(unitName: string, dropInFile = DEFAULT_RCLONE_DROPIN_FILE): string {
  return `/etc/systemd/system/${unitName}.d/${dropInFile}`;
}

/** systemd service unit — the `systemd-mount` policy. */
export function buildSystemdServiceUnit(
  name: string,
  target: string,
  mountOptions: string,
  mountUser = 'root'
): string {
  const n = sanitizeRemoteName(name);
  const t = sanitizeMountTarget(target);
  return [
    '[Unit]',
    `Description=rclone mount ${n}`,
    'After=network-online.target',
    'Wants=network-online.target',
    '',
    '[Service]',
    'Type=notify',
    `User=${mountUser}`,
    `ExecStart=/usr/bin/rclone mount ${n}: ${t} ${mountOptions}`,
    `ExecStop=/bin/fusermount -u ${t}`,
    'Restart=on-failure',
    'RestartSec=10',
    '',
    '[Install]',
    'WantedBy=multi-user.target',
    '',
  ].join('\n');
}

/** ATC-owned drop-in for `rclone-<name>.service.d/atc-rclone.conf`. */
export function buildSystemdServiceDropIn(
  name: string,
  target: string,
  mountOptions: string,
  mountUser = 'root'
): string {
  const n = sanitizeRemoteName(name);
  const t = sanitizeMountTarget(target);
  return [
    `# Managed by Air Traffic Control — rclone mount drop-in for ${n} (${t})`,
    '[Service]',
    `User=${mountUser}`,
    'Environment="RCLONE_CONFIG=/root/.config/rclone/rclone.conf"',
    `# Mount options: ${mountOptions}`,
    '',
  ].join('\n');
}

/** .mount + .automount unit pair — the `systemd-automount` policy. */
export function buildSystemdAutomountUnits(
  name: string,
  target: string,
  mountOptions: string
): { mountUnit: string; automountUnit: string; mountDropIn: string } {
  const n = sanitizeRemoteName(name);
  const t = sanitizeMountTarget(target);
  const opts = mountOptions.trim().split(/\s+/).filter(Boolean).join(',');
  const optionsLine = opts ? `_netdev,${opts}` : '_netdev';
  return {
    mountUnit: [
      '[Unit]',
      `Description=rclone mount ${n}`,
      'After=network-online.target',
      'Wants=network-online.target',
      '',
      '[Mount]',
      `What=${n}:`,
      `Where=${t}`,
      'Type=fuse.rclone',
      `Options=${optionsLine}`,
      '',
      '[Install]',
      'WantedBy=multi-user.target',
      '',
    ].join('\n'),
    automountUnit: [
      '[Unit]',
      `Description=Automount rclone remote ${n}`,
      'After=network-online.target',
      '',
      '[Automount]',
      `Where=${t}`,
      'TimeoutIdleSec=600',
      '',
      '[Install]',
      'WantedBy=multi-user.target',
      '',
    ].join('\n'),
    mountDropIn: [
      `# Managed by Air Traffic Control — rclone mount drop-in for ${n} (${t})`,
      '[Mount]',
      `Options=${optionsLine}`,
      '',
    ].join('\n'),
  };
}

/** Single /etc/fstab line — the `fstab` policy. */
export function buildFstabLine(name: string, target: string, mountOptions: string): string {
  const n = sanitizeRemoteName(name);
  const t = sanitizeMountTarget(target);
  const opts = mountOptions.trim().split(/\s+/).join(',');
  return `${n}: ${t} fuse.rclone x-systemd.automount,_netdev,${opts} 0 0`;
}

/** Service script — the `script` policy. */
export function buildMountScript(name: string, target: string, mountOptions: string): string {
  const n = sanitizeRemoteName(name);
  const t = sanitizeMountTarget(target);
  return [
    '#!/bin/sh',
    '# Managed by Air Traffic Control — do not edit by hand.',
    'set -eu',
    `exec /usr/bin/rclone mount ${n}: ${t} ${mountOptions}`,
    '',
  ].join('\n');
}

/**
 * Preview of what a given mount policy installs on a client. The Go plugin
 * (`mount/rclone`) is the authoritative compiler for the staged Ansible task;
 * this previewer renders the same unit and drop-in artifacts for the client page and tests.
 */
export function previewMountArtifacts(
  policy: RcloneMountPolicy,
  name: string,
  target: string,
  mountOptions = '--vfs-cache-mode writes --dir-cache-time 5m'
): string[] {
  const svcUnit = buildSystemdServiceUnitName(name);
  switch (policy) {
    case 'systemd-mount':
      return [
        `/etc/systemd/system/${svcUnit}`,
        buildSystemdDropInPath(svcUnit),
      ];
    case 'systemd-automount': {
      const mountUnit = buildSystemdMountUnitName(target);
      const automountUnit = buildSystemdAutomountUnitName(target);
      return [
        `/etc/systemd/system/${mountUnit}`,
        buildSystemdDropInPath(mountUnit),
        `/etc/systemd/system/${automountUnit}`,
      ];
    }
    case 'fstab':
      return [buildFstabLine(name, target, mountOptions)];
    case 'script':
      return [
        `/usr/local/bin/rclone-mount-${sanitizeRemoteName(name)}.sh`,
        `/etc/systemd/system/${svcUnit}`,
        buildSystemdDropInPath(svcUnit),
      ];
    default:
      return [];
  }
}
