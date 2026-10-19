import test from 'node:test';
import assert from 'node:assert/strict';
import {
  escapeShellSingleQuote,
  sanitizeUsername,
  sanitizePath,
  sanitizeShareName,
  sanitizeNfsClient,
  sanitizeNfsOptions,
  generateNfsExportLine,
  generateSmbShareBlock,
  buildNfsSaveCommand,
  buildSmbSaveCommand,
  parseEtcPasswd,
  buildUserCreateCommand,
  buildUserPasswdCommand,
  buildSmbPasswdCommand,
} from './sharesManager.ts';

test('escapeShellSingleQuote escapes single quotes correctly', () => {
  assert.equal(escapeShellSingleQuote('simple'), 'simple');
  assert.equal(escapeShellSingleQuote("foo'bar"), "foo'\\''bar");
  assert.equal(escapeShellSingleQuote("a'b'c"), "a'\\''b'\\''c");
});

test('sanitizeUsername accepts valid usernames and rejects invalid or malicious names', () => {
  assert.equal(sanitizeUsername('core'), 'core');
  assert.equal(sanitizeUsername('apps_user'), 'apps_user');
  assert.equal(sanitizeUsername('media-01'), 'media-01');
  assert.equal(sanitizeUsername('User_Admin'), 'User_Admin');

  // Empty or whitespace
  assert.throws(() => sanitizeUsername(''), /cannot be empty/);
  assert.throws(() => sanitizeUsername('   '), /cannot be empty/);

  // Shell injections & illegal chars
  assert.throws(() => sanitizeUsername('user; rm -rf /'), /Invalid username/);
  assert.throws(() => sanitizeUsername('user && id'), /Invalid username/);
  assert.throws(() => sanitizeUsername('`reboot`'), /Invalid username/);
  assert.throws(() => sanitizeUsername('$(whoami)'), /Invalid username/);
  assert.throws(() => sanitizeUsername('user|nc'), /Invalid username/);
  assert.throws(() => sanitizeUsername('9user'), /Invalid username/);
  assert.throws(() => sanitizeUsername('-user'), /Invalid username/);
});

test('sanitizePath accepts absolute paths and rejects relative or malicious paths', () => {
  assert.equal(sanitizePath('/mnt/data'), '/mnt/data');
  assert.equal(sanitizePath('/tank/backup/shares/'), '/tank/backup/shares/');
  assert.equal(sanitizePath('/srv//nfs///media'), '/srv/nfs/media');

  // Relative paths
  assert.throws(() => sanitizePath('relative/path'), /must be an absolute path/);
  assert.throws(() => sanitizePath('./data'), /must be an absolute path/);
  assert.throws(() => sanitizePath(''), /cannot be empty/);

  // Injections
  assert.throws(() => sanitizePath('/mnt/data; rm -rf /'), /invalid shell characters/);
  assert.throws(() => sanitizePath('/mnt/data`id`'), /invalid shell characters/);
  assert.throws(() => sanitizePath('/mnt/data$(cat /etc/passwd)'), /invalid shell characters/);
  assert.throws(() => sanitizePath('/mnt/data\n/etc'), /invalid shell characters/);
});

test('sanitizeShareName validates section names and rejects reserved words and injection', () => {
  assert.equal(sanitizeShareName('media'), 'media');
  assert.equal(sanitizeShareName('Backups_2026'), 'Backups_2026');
  assert.equal(sanitizeShareName('Public Share'), 'Public Share');
  assert.equal(sanitizeShareName('dataset-01.local'), 'dataset-01.local');

  // Reserved names
  assert.throws(() => sanitizeShareName('global'), /reserved Samba configuration/);
  assert.throws(() => sanitizeShareName('GLOBAL'), /reserved Samba configuration/);
  assert.throws(() => sanitizeShareName('printers'), /reserved Samba configuration/);
  assert.throws(() => sanitizeShareName('print$'), /reserved Samba configuration/);
  assert.throws(() => sanitizeShareName('homes'), /reserved Samba configuration/);

  // Injections
  assert.throws(() => sanitizeShareName('share; rm -rf'), /invalid characters/);
  assert.throws(() => sanitizeShareName('share[0]'), /invalid characters/);
  assert.throws(() => sanitizeShareName(''), /cannot be empty/);
});

test('sanitizeNfsClient and sanitizeNfsOptions validate inputs', () => {
  assert.equal(sanitizeNfsClient(''), '*');
  assert.equal(sanitizeNfsClient('*'), '*');
  assert.equal(sanitizeNfsClient('100.64.0.0/10'), '100.64.0.0/10');
  assert.equal(sanitizeNfsClient('192.168.1.50 node2.lab'), '192.168.1.50 node2.lab');
  assert.equal(sanitizeNfsClient('@trusted-hosts'), '@trusted-hosts');

  assert.throws(() => sanitizeNfsClient('100.64.0.0/10; rm -rf'), /Invalid NFS client/);
  assert.throws(() => sanitizeNfsClient('`id`'), /Invalid NFS client/);

  assert.equal(sanitizeNfsOptions(''), 'rw,sync,no_subtree_check,no_root_squash');
  assert.equal(sanitizeNfsOptions('ro,sync,no_subtree_check'), 'ro,sync,no_subtree_check');
  assert.equal(sanitizeNfsOptions('rw,all_squash,anonuid=1000'), 'rw,all_squash,anonuid=1000');

  assert.throws(() => sanitizeNfsOptions('rw; echo hacked'), /Invalid NFS export option/);
});

test('generateNfsExportLine formats valid NFS export entries', () => {
  const line1 = generateNfsExportLine({
    path: '/tank/share',
    client: '100.64.0.0/10',
    options: 'rw,sync,no_subtree_check',
  });
  assert.equal(line1, '/tank/share 100.64.0.0/10(rw,sync,no_subtree_check)');

  const line2 = generateNfsExportLine({
    path: '/mnt/public',
  });
  assert.equal(line2, '/mnt/public *(rw,sync,no_subtree_check,no_root_squash)');

  const lineMulti = generateNfsExportLine({
    path: '/srv/data',
    client: '192.168.1.10 192.168.1.20',
    options: 'ro,sync',
  });
  assert.equal(lineMulti, '/srv/data 192.168.1.10(ro,sync) 192.168.1.20(ro,sync)');
});

test('generateSmbShareBlock produces standard smb.conf section', () => {
  const block1 = generateSmbShareBlock({
    name: 'media',
    path: '/tank/media',
    comment: 'Media Storage for Jellyfin',
    readOnly: false,
    validUsers: 'core apps',
    guestOk: false,
  });

  assert.match(block1, /\[media\]/);
  assert.match(block1, /path = \/tank\/media/);
  assert.match(block1, /comment = Media Storage for Jellyfin/);
  assert.match(block1, /read only = no/);
  assert.match(block1, /guest ok = no/);
  assert.match(block1, /valid users = core apps/);
  assert.match(block1, /browseable = yes/);

  const blockReadOnly = generateSmbShareBlock({
    name: 'public_archive',
    path: '/data/archive',
    readOnly: true,
    guestOk: true,
  });

  assert.match(blockReadOnly, /\[public_archive\]/);
  assert.match(blockReadOnly, /read only = yes/);
  assert.match(blockReadOnly, /guest ok = yes/);
  assert.doesNotMatch(blockReadOnly, /valid users =/);
});

test('buildNfsSaveCommand creates command targeting /etc/exports.d and exportfs', () => {
  const cmd = buildNfsSaveCommand({
    path: '/tank/nfs_share',
    client: '*',
  });

  assert.match(cmd, /sudo mkdir -p \/etc\/exports\.d/);
  assert.match(cmd, /\/etc\/exports\.d\/cockpit-nfs_share\.exports/);
  assert.match(cmd, /sudo exportfs -ra/);
});

test('buildSmbSaveCommand builds Python-based safe atomic smb.conf updater', () => {
  const cmd = buildSmbSaveCommand({
    name: 'backup',
    path: '/mnt/backup',
    readOnly: false,
    guestOk: false,
  });

  assert.match(cmd, /python3 -c/);
  assert.match(cmd, /\/etc\/samba\/smb\.conf/);
  assert.match(cmd, /smbcontrol all reload-config/);
});

test('parseEtcPasswd extracts users and categorizes system vs normal users', () => {
  const samplePasswd = [
    'root:x:0:0:root:/root:/bin/bash',
    'daemon:x:1:1:daemon:/usr/sbin:/usr/sbin/nologin',
    'bin:x:2:2:bin:/bin:/usr/sbin/nologin',
    'systemd-resolve:x:102:103:systemd Resolver,,,:/run/systemd/resolve:/usr/sbin/nologin',
    'core:x:1000:1000:Core Admin:/home/core:/bin/bash',
    'apps:x:1001:1001:Application User:/home/apps:/bin/zsh',
    'podman_svc:x:995:995:Podman Daemon:/var/lib/podman:/bin/false',
    'nobody:x:65534:65534:nobody:/nonexistent:/usr/sbin/nologin',
  ].join('\n');

  const sampleSubuid = 'core:100000:65536\napps:165536:65536\npodman_svc:231072:65536\n';

  const users = parseEtcPasswd(samplePasswd, sampleSubuid);
  assert.equal(users.length, 8);

  const core = users.find(u => u.username === 'core');
  assert.ok(core);
  assert.equal(core.uid, 1000);
  assert.equal(core.isSystemUser, false);
  assert.equal(core.shell, '/bin/bash');

  const root = users.find(u => u.username === 'root');
  assert.ok(root);
  assert.equal(root.uid, 0);
  assert.equal(root.isSystemUser, true);

  const nobody = users.find(u => u.username === 'nobody');
  assert.ok(nobody);
  assert.equal(nobody.isSystemUser, true);

  // podman_svc is in subuid, so even with UID 995 it is flagged as service/subuid user
  const podmanSvc = users.find(u => u.username === 'podman_svc');
  assert.ok(podmanSvc);
  assert.equal(podmanSvc.isSystemUser, false);
});

test('buildUserCreateCommand, buildUserPasswdCommand and buildSmbPasswdCommand generate safe commands', () => {
  const createCmd = buildUserCreateCommand('jellyfin_user');
  assert.equal(createCmd, "sudo useradd -m -s /bin/bash 'jellyfin_user'");

  const passwdCmd = buildUserPasswdCommand('jellyfin_user', "pass'word!123");
  assert.match(passwdCmd, /sudo chpasswd/);
  assert.match(passwdCmd, /'jellyfin_user'/);
  assert.match(passwdCmd, /'pass'\\''word!123'/);

  const smbPasswdCmd = buildSmbPasswdCommand('jellyfin_user', "smb'pass$$");
  assert.match(smbPasswdCmd, /sudo smbpasswd -s -a 'jellyfin_user'/);
  assert.match(smbPasswdCmd, /'smb'\\''pass\$\$'/);
});
