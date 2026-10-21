import { describe, it } from 'node:test';
import assert from 'node:assert';
import http from 'node:http';
import {
  buildDetectPackageManagerCommand,
  parseDetectPackageManagerOutput,
  detectPackageManagerFromOs,
  isImmutablePackageManager,
  buildCheckUpdatesCommand,
  buildApplyUpdatesCommand,
  isKernelOrSystemUpdate,
  isRebootRequired,
  parseCheckUpdatesOutput,
  registerPackageRoutes,
  type PackageUpdate,
} from './packageManager.ts';
import type { NodeItem } from '../types/index.ts';

describe('packageManager - Detection & OS Probing', () => {
  it('builds a shell probe command that tests in priority order (bootc, rpm-ostree, dnf, apt-get, apk)', () => {
    const cmd = buildDetectPackageManagerCommand();
    assert.ok(cmd.includes('bootc'), 'Should probe bootc');
    assert.ok(cmd.includes('rpm-ostree'), 'Should probe rpm-ostree');
    assert.ok(cmd.includes('dnf'), 'Should probe dnf');
    assert.ok(cmd.includes('apt-get'), 'Should probe apt-get');
    assert.ok(cmd.includes('apk'), 'Should probe apk');
  });

  it('parses probe output correctly even with whitespace or SSH MOTD banners', () => {
    assert.strictEqual(parseDetectPackageManagerOutput('dnf\n'), 'dnf');
    assert.strictEqual(parseDetectPackageManagerOutput('  apt  '), 'apt');
    assert.strictEqual(parseDetectPackageManagerOutput('apt-get'), 'apt');
    assert.strictEqual(parseDetectPackageManagerOutput('apk\r\n'), 'apk');
    assert.strictEqual(parseDetectPackageManagerOutput('rpm-ostree'), 'rpm-ostree');
    assert.strictEqual(parseDetectPackageManagerOutput('bootc'), 'bootc');

    const withBanner = `
Welcome to Fedora CoreOS
Last login: Mon Oct 19 01:00:00 2026
rpm-ostree
`;
    assert.strictEqual(parseDetectPackageManagerOutput(withBanner), 'rpm-ostree');
    assert.strictEqual(parseDetectPackageManagerOutput('unknown'), null);
    assert.strictEqual(parseDetectPackageManagerOutput(''), null);
  });

  it('detects package manager from OS description string fallback', () => {
    assert.strictEqual(detectPackageManagerFromOs('AlmaLinux 10.2 (Lavender Lion)'), 'dnf');
    assert.strictEqual(detectPackageManagerFromOs('Fedora Linux 41 (Server Edition)'), 'dnf');
    assert.strictEqual(detectPackageManagerFromOs('Rocky Linux 9.4 (Blue Onyx)'), 'dnf');
    assert.strictEqual(detectPackageManagerFromOs('CentOS Stream 9'), 'dnf');
    assert.strictEqual(detectPackageManagerFromOs('Red Hat Enterprise Linux 9.4'), 'dnf');

    assert.strictEqual(detectPackageManagerFromOs('Debian GNU/Linux 12 (bookworm)'), 'apt');
    assert.strictEqual(detectPackageManagerFromOs('Ubuntu 24.04 LTS (Noble Numbat)'), 'apt');
    assert.strictEqual(detectPackageManagerFromOs('Raspbian GNU/Linux 11 (bullseye)'), 'apt');

    assert.strictEqual(detectPackageManagerFromOs('Alpine Linux v3.20'), 'apk');

    assert.strictEqual(detectPackageManagerFromOs('Fedora Silverblue 41'), 'rpm-ostree');
    assert.strictEqual(detectPackageManagerFromOs('Fedora Kinoite 41'), 'rpm-ostree');
    assert.strictEqual(detectPackageManagerFromOs('Fedora CoreOS 41.20241015'), 'rpm-ostree');

    assert.strictEqual(detectPackageManagerFromOs('CentOS Bootc 9'), 'bootc');
    assert.strictEqual(detectPackageManagerFromOs(''), null);
    assert.strictEqual(detectPackageManagerFromOs('FreeBSD 14.0-RELEASE'), null);
  });

  it('identifies immutable/atomic managers correctly', () => {
    assert.strictEqual(isImmutablePackageManager('rpm-ostree'), true);
    assert.strictEqual(isImmutablePackageManager('bootc'), true);
    assert.strictEqual(isImmutablePackageManager('dnf'), false);
    assert.strictEqual(isImmutablePackageManager('apt'), false);
    assert.strictEqual(isImmutablePackageManager('apk'), false);
  });
});

describe('packageManager - Command Construction', () => {
  it('buildCheckUpdatesCommand produces accurate check command for each manager', () => {
    assert.strictEqual(buildCheckUpdatesCommand('dnf'), 'dnf check-update -q || [ $? -eq 100 ]');
    assert.strictEqual(buildCheckUpdatesCommand('apt'), 'apt-get -s -o Debug::NoLocking=true upgrade');
    assert.strictEqual(buildCheckUpdatesCommand('apk'), "apk version -v -l '<'");
    assert.strictEqual(buildCheckUpdatesCommand('rpm-ostree'), 'rpm-ostree upgrade --check');
    assert.strictEqual(buildCheckUpdatesCommand('bootc'), 'bootc status');
  });

  it('buildApplyUpdatesCommand handles all upgrades and specific packages', () => {
    assert.strictEqual(buildApplyUpdatesCommand('dnf'), 'dnf upgrade -y');
    assert.strictEqual(
      buildApplyUpdatesCommand('dnf', ['curl', 'openssl']),
      'dnf upgrade -y curl openssl'
    );

    assert.strictEqual(
      buildApplyUpdatesCommand('apt'),
      'DEBIAN_FRONTEND=noninteractive apt-get upgrade -y'
    );
    assert.strictEqual(
      buildApplyUpdatesCommand('apt', ['systemd', 'nginx']),
      'DEBIAN_FRONTEND=noninteractive apt-get install --only-upgrade -y systemd nginx'
    );

    assert.strictEqual(buildApplyUpdatesCommand('apk'), 'apk upgrade');
    assert.strictEqual(buildApplyUpdatesCommand('apk', ['musl']), 'apk add --upgrade musl');

    assert.strictEqual(
      buildApplyUpdatesCommand('dnf', ['curl', 'evil; rm -rf /', 'valid-pkg_1.0+b1']),
      'dnf upgrade -y curl valid-pkg_1.0+b1'
    );

    assert.strictEqual(buildApplyUpdatesCommand('rpm-ostree'), 'rpm-ostree upgrade');
    assert.strictEqual(buildApplyUpdatesCommand('bootc'), 'bootc upgrade');
  });

  it('isKernelOrSystemUpdate and isRebootRequired detect critical reboot needs', () => {
    assert.strictEqual(isKernelOrSystemUpdate('kernel'), true);
    assert.strictEqual(isKernelOrSystemUpdate('kernel-core'), true);
    assert.strictEqual(isKernelOrSystemUpdate('linux-image-generic'), true);
    assert.strictEqual(isKernelOrSystemUpdate('systemd-libs'), true);
    assert.strictEqual(isKernelOrSystemUpdate('glibc'), true);
    assert.strictEqual(isKernelOrSystemUpdate('musl'), true);
    assert.strictEqual(isKernelOrSystemUpdate('curl'), false);
    assert.strictEqual(isKernelOrSystemUpdate('nginx'), false);

    const normalUpdates: PackageUpdate[] = [{ name: 'curl', newVersion: '7.81.0-2' }];
    const kernelUpdates: PackageUpdate[] = [
      { name: 'curl', newVersion: '7.81.0-2' },
      { name: 'kernel-tools', newVersion: '6.5.0' },
    ];

    assert.strictEqual(isRebootRequired('dnf', normalUpdates), false);
    assert.strictEqual(isRebootRequired('dnf', kernelUpdates), true);
    // Immutable OS updates always require reboot
    assert.strictEqual(isRebootRequired('rpm-ostree', normalUpdates), true);
    assert.strictEqual(isRebootRequired('bootc', []), false);
    assert.strictEqual(isRebootRequired('bootc', [], 'deployment staged pending reboot'), true);
  });
});

describe('packageManager - Output Parsing', () => {
  describe('dnf output parsing', () => {
    it('parses standard dnf check-update output with arch and repo', () => {
      const sample = `
bind-libs.x86_64                            32:9.16.23-14.el9_4.1            updates
kernel.x86_64                               5.14.0-427.28.1.el9_4            baseos
systemd.x86_64                              252-32.el9_4.7                   baseos-security
curl.x86_64                                 7.76.1-29.el9_4                  updates
Obsoleting Packages
grub2-common.noarch                         1:2.06-70.el9                    baseos
`;
      const updates = parseCheckUpdatesOutput('dnf', sample);
      assert.strictEqual(updates.length, 4);

      assert.deepStrictEqual(updates[0], {
        name: 'bind-libs',
        newVersion: '32:9.16.23-14.el9_4.1',
        arch: 'x86_64',
        repository: 'updates',
        security: false,
      });

      assert.deepStrictEqual(updates[1], {
        name: 'kernel',
        newVersion: '5.14.0-427.28.1.el9_4',
        arch: 'x86_64',
        repository: 'baseos',
        security: false,
      });

      assert.strictEqual(updates[2].name, 'systemd');
      assert.strictEqual(updates[2].security, true); // baseos-security repo
    });

    it('handles wrapped long package names in dnf output', () => {
      const sample = `
container-selinux.noarch
                         2:2.232.0-1.el9                     updates
`;
      const updates = parseCheckUpdatesOutput('dnf', sample);
      assert.strictEqual(updates.length, 1);
      assert.strictEqual(updates[0].name, 'container-selinux');
      assert.strictEqual(updates[0].arch, 'noarch');
      assert.strictEqual(updates[0].newVersion, '2:2.232.0-1.el9');
      assert.strictEqual(updates[0].repository, 'updates');
    });

    it('returns empty array when dnf check-update returns no updates', () => {
      assert.deepStrictEqual(parseCheckUpdatesOutput('dnf', ''), []);
      assert.deepStrictEqual(parseCheckUpdatesOutput('dnf', '   \n  \n'), []);
    });
  });

  describe('apt output parsing', () => {
    it('parses apt-get -s upgrade simulated output with current and new versions', () => {
      const sample = `
Reading package lists... Done
Building dependency tree... Done
The following packages will be upgraded:
  curl libcurl4 linux-image-generic
Inst curl [7.81.0-1ubuntu1.15] (7.81.0-1ubuntu1.16 Ubuntu:22.04/jammy-security [amd64])
Inst libcurl4 [7.81.0-1ubuntu1.15] (7.81.0-1ubuntu1.16 Ubuntu:22.04/jammy-updates [amd64])
Inst linux-image-generic [5.15.0.91.88] (5.15.0.94.91 Ubuntu:22.04/jammy-updates [amd64])
Conf curl (7.81.0-1ubuntu1.16 Ubuntu:22.04/jammy-security [amd64])
`;
      const updates = parseCheckUpdatesOutput('apt', sample);
      assert.strictEqual(updates.length, 3);

      assert.strictEqual(updates[0].name, 'curl');
      assert.strictEqual(updates[0].currentVersion, '7.81.0-1ubuntu1.15');
      assert.strictEqual(updates[0].newVersion, '7.81.0-1ubuntu1.16');
      assert.strictEqual(updates[0].arch, 'amd64');
      assert.strictEqual(updates[0].security, true); // jammy-security

      assert.strictEqual(updates[1].name, 'libcurl4');
      assert.strictEqual(updates[1].security, false);

      assert.strictEqual(updates[2].name, 'linux-image-generic');
      assert.strictEqual(updates[2].newVersion, '5.15.0.94.91');
    });

    it('parses apt list --upgradable format', () => {
      const sample = `
Listing... Done
curl/jammy-security,jammy-updates 7.81.0-1ubuntu1.16 amd64 [upgradable from: 7.81.0-1ubuntu1.15]
git/jammy-updates 1:2.34.1-1ubuntu1.10 amd64 [upgradable from: 1:2.34.1-1ubuntu1.9]
`;
      const updates = parseCheckUpdatesOutput('apt', sample);
      assert.strictEqual(updates.length, 2);

      assert.strictEqual(updates[0].name, 'curl');
      assert.strictEqual(updates[0].currentVersion, '7.81.0-1ubuntu1.15');
      assert.strictEqual(updates[0].newVersion, '7.81.0-1ubuntu1.16');
      assert.strictEqual(updates[0].arch, 'amd64');
      assert.strictEqual(updates[0].security, true);

      assert.strictEqual(updates[1].name, 'git');
      assert.strictEqual(updates[1].currentVersion, '1:2.34.1-1ubuntu1.9');
      assert.strictEqual(updates[1].newVersion, '1:2.34.1-1ubuntu1.10');
      assert.strictEqual(updates[1].security, false);
    });
  });

  describe('apk output parsing', () => {
    it('parses apk version -v -l output format', () => {
      const sample = `
musl-1.2.4_git20230717-r1 < 1.2.4_git20230717-r2
openssl-3.1.4-r1 < 3.1.4-r2
busybox-1.36.1-r4 < 1.36.1-r5
linux-virt-6.1.66-r0 < 6.1.68-r0
`;
      const updates = parseCheckUpdatesOutput('apk', sample);
      assert.strictEqual(updates.length, 4);

      assert.deepStrictEqual(updates[0], {
        name: 'musl',
        currentVersion: '1.2.4_git20230717-r1',
        newVersion: '1.2.4_git20230717-r2',
        security: true, // musl is system C library
      });

      assert.strictEqual(updates[1].name, 'openssl');
      assert.strictEqual(updates[1].currentVersion, '3.1.4-r1');
      assert.strictEqual(updates[1].newVersion, '3.1.4-r2');
      assert.strictEqual(updates[1].security, true);

      assert.strictEqual(updates[2].name, 'busybox');
      assert.strictEqual(updates[2].newVersion, '1.36.1-r5');

      assert.strictEqual(updates[3].name, 'linux-virt');
      assert.strictEqual(updates[3].newVersion, '6.1.68-r0');
      assert.strictEqual(updates[3].security, true);
    });
  });

  describe('rpm-ostree output parsing', () => {
    it('parses rpm-ostree upgrade --check with Upgraded: packages', () => {
      const sample = `
AvailableUpdate:
  Version: 39.20231110.0 (2023-11-10T14:48:20Z)
  Commit: 8e50bc5e8a93a8d11c0f0d2c0b05b63cf26bfb76f183709b11029c0173e6a9f5
  Diff: 24 upgraded, 2 added, 1 removed
  Upgraded:
    kernel 6.5.9-300.fc39.x86_64 -> 6.5.10-300.fc39.x86_64
    systemd 254.5-1.fc39 -> 254.5-2.fc39
    firefox 119.0-1.fc39 -> 119.0.1-1.fc39
  Added:
    kernel-modules-extra 6.5.10-300.fc39
`;
      const updates = parseCheckUpdatesOutput('rpm-ostree', sample);
      assert.strictEqual(updates.length, 3);

      assert.strictEqual(updates[0].name, 'kernel');
      assert.strictEqual(updates[0].currentVersion, '6.5.9-300.fc39.x86_64');
      assert.strictEqual(updates[0].newVersion, '6.5.10-300.fc39.x86_64');
      assert.strictEqual(updates[0].arch, 'x86_64');
      assert.strictEqual(updates[0].security, true);

      assert.strictEqual(updates[1].name, 'systemd');
      assert.strictEqual(updates[1].security, true);

      assert.strictEqual(updates[2].name, 'firefox');
      assert.strictEqual(updates[2].security, false);
    });

    it('falls back to ostree system image update when no package diff is provided', () => {
      const sample = `
AvailableUpdate:
  Version: 40.20240415.0 (2024-04-15T12:00:00Z)
  Commit: abcdef123456
`;
      const updates = parseCheckUpdatesOutput('rpm-ostree', sample);
      assert.strictEqual(updates.length, 1);
      assert.strictEqual(updates[0].name, 'ostree-system-image');
      assert.strictEqual(updates[0].newVersion, '40.20240415.0');
    });

    it('returns empty array when rpm-ostree reports no update available', () => {
      assert.deepStrictEqual(
        parseCheckUpdatesOutput('rpm-ostree', 'Resolving metadata pull... done\nNo update available.'),
        []
      );
    });
  });

  describe('bootc output parsing', () => {
    it('parses bootc status JSON format with staged update', () => {
      const json = JSON.stringify({
        status: {
          booted: {
            image: {
              image: { image: 'quay.io/centos-bootc/centos-bootc:stream9' },
              version: '9.20240315.0',
            },
          },
          staged: {
            image: {
              image: { image: 'quay.io/centos-bootc/centos-bootc:stream9' },
              version: '9.20240320.0',
            },
          },
        },
      });

      const updates = parseCheckUpdatesOutput('bootc', json);
      assert.strictEqual(updates.length, 1);
      assert.strictEqual(updates[0].name, 'quay.io/centos-bootc/centos-bootc:stream9');
      assert.strictEqual(updates[0].currentVersion, '9.20240315.0');
      assert.strictEqual(updates[0].newVersion, '9.20240320.0');
    });

    it('parses bootc status text format with AvailableUpdate', () => {
      const text = `
Booted: quay.io/centos-bootc/centos-bootc:stream9 (9.20240315.0)
AvailableUpdate: quay.io/centos-bootc/centos-bootc:stream9 (9.20240320.0)
`;
      const updates = parseCheckUpdatesOutput('bootc', text);
      assert.strictEqual(updates.length, 1);
      assert.strictEqual(updates[0].name, 'quay.io/centos-bootc/centos-bootc:stream9');
      assert.strictEqual(updates[0].currentVersion, '9.20240315.0');
      assert.strictEqual(updates[0].newVersion, '9.20240320.0');
    });

    it('returns empty array when bootc has no update available', () => {
      assert.deepStrictEqual(parseCheckUpdatesOutput('bootc', 'No update available.'), []);
    });
  });
});

describe('packageManager - API Route Integration', () => {
  let server: import('node:http').Server;
  let baseUrl: string;

  const mockNode: NodeItem = {
    id: 'node-alma',
    name: 'likes-it-cold',
    shortName: 'likes-it-cold',
    group: 'tag:host',
    datacenter: 'leo-banjo.ts.net',
    nodeType: 'linux',
    ip: '100.105.71.49',
    port: 22,
    sshUser: 'core',
    tailscaleIp: '100.105.71.49',
    tailscaleHost: 'likes-it-cold.leo-banjo.ts.net',
    osDescription: 'AlmaLinux 10.2 (Lavender Lion)',
    status: 'online',
    uptime: '3d 14h',
    hasGlances: false,
    hasCockpit: true,
    hasPodman: true,
    hasRestic: true,
    specs: {
      cpu: 'AMD Ryzen',
      vcpus: 8,
      ramGiB: 32,
      motherboard: 'Lenovo',
      bios: 'M25KT2FA',
      ipmi: 'N/A',
      ecc: 'Non-ECC',
      secureBoot: 'Enabled',
      iommu: 'Enabled',
    },
  };

  const sampleDnfOutput = `
bind-libs.x86_64                            32:9.16.23-14.el9_4.1            updates
kernel.x86_64                               5.14.0-427.28.1.el9_4            baseos
`;

  function createApp() {
    const routes: Array<{ method: string; path: string; handler: Function }> = [];
    const app = {
      get: (p: string, h: Function) => routes.push({ method: 'GET', path: p, handler: h }),
      post: (p: string, h: Function) => routes.push({ method: 'POST', path: p, handler: h }),
      put: (p: string, h: Function) => routes.push({ method: 'PUT', path: p, handler: h }),
      delete: (p: string, h: Function) => routes.push({ method: 'DELETE', path: p, handler: h }),
    };

    const server = http.createServer((req, res) => {
      const parsedUrl = new URL(req.url || '/', 'http://127.0.0.1');
      const pathname = parsedUrl.pathname;
      const method = req.method || 'GET';

      let body = '';
      req.on('data', chunk => { body += chunk; });
      req.on('end', async () => {
        let parsedBody = {};
        if (body) {
          try { parsedBody = JSON.parse(body); } catch {}
        }

        for (const r of routes) {
          if (r.method !== method) continue;
          const rParts = r.path.split('/');
          const uParts = pathname.split('/');
          if (rParts.length !== uParts.length) continue;

          const params: Record<string, string> = {};
          let match = true;
          for (let i = 0; i < rParts.length; i++) {
            if (rParts[i].startsWith(':')) {
              params[rParts[i].slice(1)] = decodeURIComponent(uParts[i]);
            } else if (rParts[i] !== uParts[i]) {
              match = false;
              break;
            }
          }

          if (match) {
            const reqMock = { params, body: parsedBody, query: Object.fromEntries(parsedUrl.searchParams) };
            const resMock = {
              status(code: number) {
                res.statusCode = code;
                return resMock;
              },
              json(data: any) {
                res.setHeader('Content-Type', 'application/json');
                res.end(JSON.stringify(data));
                return resMock;
              },
            };
            await r.handler(reqMock, resMock);
            return;
          }
        }

        res.statusCode = 404;
        res.end(JSON.stringify({ error: 'Not found' }));
      });
    });

    return { app, server };
  }

  async function startServer(server: http.Server): Promise<{ baseUrl: string; close: () => Promise<void> }> {
    return new Promise((resolve) => {
      server.listen(0, '127.0.0.1', () => {
        const port = (server.address() as import('node:net').AddressInfo).port;
        resolve({
          baseUrl: `http://127.0.0.1:${port}`,
          close: () => new Promise<void>((r) => server.close(() => r())),
        });
      });
    });
  }

  it('GET /api/nodes/:id/packages/check returns 404 for unknown host', async () => {
    const { app, server } = createApp();
    registerPackageRoutes(app, {
      findNode: () => null,
    });

    const s = await startServer(server);
    try {
      const res = await fetch(`${s.baseUrl}/api/nodes/non-existent/packages/check`);
      assert.strictEqual(res.status, 404);
      const data = await res.json();
      assert.strictEqual(data.success, false);
    } finally {
      await s.close();
    }
  });

  it('GET /api/nodes/:id/packages/check probes host and parses updates', async () => {
    const { app, server } = createApp();

    const savedSections: Record<string, any> = {};

    registerPackageRoutes(app, {
      findNode: (id) => (id === 'node-alma' ? mockNode : null),
      runNodeCommand: async (_node, cmd) => {
        if (cmd.includes('command -v bootc')) {
          return 'dnf';
        }
        return '';
      },
      runNodeElevatedCommand: async (_node, cmd) => {
        if (cmd.includes('dnf check-update')) {
          return sampleDnfOutput;
        }
        return '';
      },
      hostCache: {
        getHostSection: () => null,
        upsertHostSection: (_id, sec, data) => {
          savedSections[sec] = data;
        },
        invalidateHostSections: () => {},
      },
    });

    const s = await startServer(server);
    try {
      const res = await fetch(`${s.baseUrl}/api/nodes/node-alma/packages/check`);
      assert.strictEqual(res.status, 200);
      const data = (await res.json()) as any;
      assert.strictEqual(data.success, true);
      assert.strictEqual(data.manager, 'dnf');
      assert.strictEqual(data.isImmutable, false);
      assert.strictEqual(data.updatesCount, 2);
      assert.strictEqual(data.rebootRequired, true); // contains kernel
      assert.strictEqual(savedSections['packages']?.updatesCount, 2);
    } finally {
      await s.close();
    }
  });

  it('POST /api/nodes/:id/packages/apply creates and executes background update task', async () => {
    const { app, server } = createApp();

    const tasks: any[] = [];
    const broadcastEvents: any[] = [];
    let invalidated = false;
    let executedCommand = '';

    registerPackageRoutes(app, {
      findNode: (id) => (id === 'node-alma' ? mockNode : null),
      runNodeCommand: async () => 'dnf',
      runNodeElevatedCommand: async (_node, cmd) => {
        executedCommand = cmd;
        return 'Complete!';
      },
      hostCache: {
        getHostSection: () => null,
        upsertHostSection: () => {},
        invalidateHostSections: () => {
          invalidated = true;
        },
        saveTask: (t) => {
          tasks.push(t);
        },
      },
      broadcast: (ev) => {
        broadcastEvents.push(ev);
      },
      tasks,
    });

    const s = await startServer(server);
    try {
      const res = await fetch(`${s.baseUrl}/api/nodes/node-alma/packages/apply`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ manager: 'dnf' }),
      });
      assert.strictEqual(res.status, 200);
      const data = (await res.json()) as any;
      assert.strictEqual(data.success, true);
      assert.ok(data.taskId);
      assert.strictEqual(invalidated, true);
      assert.ok(broadcastEvents.some((e) => e.type === 'task_started'));

      // Allow async task completion
      await new Promise((r) => setTimeout(r, 50));
      assert.strictEqual(executedCommand, 'dnf upgrade -y');
    } finally {
      await s.close();
    }
  });
});

