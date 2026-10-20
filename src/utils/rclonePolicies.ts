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

/** .mount + .automount unit pair — the `systemd-automount` policy. */
export function buildSystemdAutomountUnits(
  name: string,
  target: string,
  mountOptions: string
): { mountUnit: string; automountUnit: string } {
  const n = sanitizeRemoteName(name);
  const t = sanitizeMountTarget(target);
  const opts = mountOptions.trim().split(/\s+/).join(',');
  return {
    mountUnit: [
      '[Unit]',
      `Description=rclone mount ${n}`,
      'After=network-online.target',
      '',
      '[Mount]',
      `What=${n}:`,
      `Where=${t}`,
      'Type=fuse.rclone',
      `Options=_netdev,${opts}`,
      '',
      '[Install]',
      'WantedBy=multi-user.target',
      '',
    ].join('\n'),
    automountUnit: [
      '[Unit]',
      `Description=Automount rclone remote ${n}`,
      '',
      '[Automount]',
      `Where=${t}`,
      '',
      '[Install]',
      'WantedBy=multi-user.target',
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
 * this previewer renders the same artifacts for the client page and tests.
 */
export function previewMountArtifacts(
  policy: RcloneMountPolicy,
  name: string,
  target: string,
  mountOptions = '--vfs-cache-mode writes --dir-cache-time 5m'
): string[] {
  switch (policy) {
    case 'systemd-mount':
      return [`/etc/systemd/system/rclone-${name}.service`];
    case 'systemd-automount':
      return [
        `/etc/systemd/system/rclone-${name}.mount`,
        `/etc/systemd/system/rclone-${name}.automount`,
      ];
    case 'fstab':
      return [buildFstabLine(name, target, mountOptions)];
    case 'script':
      return [
        `/usr/local/bin/rclone-mount-${name}.sh`,
        `/etc/systemd/system/rclone-${name}.service`,
      ];
    default:
      return [];
  }
}
