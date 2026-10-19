import { describe, it, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert';
import express from 'express';
import type { Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import type { NodeItem } from '../types/index.ts';
import {
  wrapElevatedCommand,
  stripLeadingPrivilege,
  evaluatePrivilege,
  parsePrivilegeProbeOutput,
  formatPrivilegeBadge,
  buildPrivilegeProbeCommand,
  registerPrivilegeRoutes,
  type PrivilegeMethod,
  type PrivilegeStatus,
} from './privilegeEscalation.ts';

describe('Privilege Escalation Helper - Command Wrapping', () => {
  it('wraps command with run0 --background=no when method is run0', () => {
    const wrapped = wrapElevatedCommand('systemctl restart nginx', 'run0', 'core');
    assert.strictEqual(wrapped, 'run0 --background=no systemctl restart nginx');
  });

  it('wraps command with sudo -n when method is sudo', () => {
    const wrapped = wrapElevatedCommand('ip link set eth0 up', 'sudo', 'core');
    assert.strictEqual(wrapped, 'sudo -n ip link set eth0 up');
  });

  it('does not wrap command when method is none', () => {
    const wrapped = wrapElevatedCommand('uname -a', 'none', 'core');
    assert.strictEqual(wrapped, 'uname -a');
  });

  it('does not wrap command when user is root even if method is run0 or sudo', () => {
    const wrappedRun0 = wrapElevatedCommand('systemctl restart nginx', 'run0', 'root');
    assert.strictEqual(wrappedRun0, 'systemctl restart nginx');

    const wrappedSudo = wrapElevatedCommand('ip link set eth0 up', 'sudo', 'root');
    assert.strictEqual(wrappedSudo, 'ip link set eth0 up');
  });

  it('strips existing sudo prefix when wrapping with run0', () => {
    const wrapped = wrapElevatedCommand('sudo ip link set wg0 up', 'run0', 'core');
    assert.strictEqual(wrapped, 'run0 --background=no ip link set wg0 up');

    const wrappedFlag = wrapElevatedCommand('sudo -n zpool scrub tank', 'run0', 'core');
    assert.strictEqual(wrappedFlag, 'run0 --background=no zpool scrub tank');
  });

  it('strips existing run0 prefix when wrapping with sudo', () => {
    const wrapped = wrapElevatedCommand('run0 --background=no smartctl -a /dev/sda', 'sudo', 'core');
    assert.strictEqual(wrapped, 'sudo -n smartctl -a /dev/sda');

    const wrappedShort = wrapElevatedCommand('run0 smartctl -a /dev/sda', 'sudo', 'core');
    assert.strictEqual(wrappedShort, 'sudo -n smartctl -a /dev/sda');
  });

  it('strips existing sudo prefix when user is root', () => {
    const wrapped = wrapElevatedCommand('sudo systemctl reboot', 'none', 'root');
    assert.strictEqual(wrapped, 'systemctl reboot');
  });

  it('handles empty or whitespace-only commands safely', () => {
    assert.strictEqual(wrapElevatedCommand('', 'run0', 'core'), '');
    assert.strictEqual(wrapElevatedCommand('   ', 'sudo', 'core'), '');
  });

  it('stripLeadingPrivilege correctly strips prefixes', () => {
    assert.strictEqual(stripLeadingPrivilege('sudo systemctl restart foo'), 'systemctl restart foo');
    assert.strictEqual(stripLeadingPrivilege('sudo -n cat /etc/shadow'), 'cat /etc/shadow');
    assert.strictEqual(stripLeadingPrivilege('run0 --background=no id -u'), 'id -u');
    assert.strictEqual(stripLeadingPrivilege('run0 whoami'), 'whoami');
    assert.strictEqual(stripLeadingPrivilege('uptime'), 'uptime');
  });
});

describe('Privilege Escalation Helper - Privilege Evaluation', () => {
  it('prefers run0 when both run0 and sudo are active', () => {
    const status = evaluatePrivilege({
      user: 'core',
      run0Success: true,
      run0Available: true,
      sudoSuccess: true,
      sudoAvailable: true,
    });

    assert.strictEqual(status.method, 'run0');
    assert.strictEqual(status.active, true);
    assert.strictEqual(status.canElevate, true);
    assert.strictEqual(status.user, 'core');
  });

  it('falls back to sudo when run0 is not active but sudo is active', () => {
    const status = evaluatePrivilege({
      user: 'core',
      run0Success: false,
      run0Available: true,
      sudoSuccess: true,
      sudoAvailable: true,
    });

    assert.strictEqual(status.method, 'sudo');
    assert.strictEqual(status.active, true);
    assert.strictEqual(status.canElevate, true);
  });

  it('prefers run0 for elevation requests when both are available but inactive', () => {
    const status = evaluatePrivilege({
      user: 'apps',
      run0Success: false,
      run0Available: true,
      sudoSuccess: false,
      sudoAvailable: true,
    });

    assert.strictEqual(status.method, 'run0');
    assert.strictEqual(status.active, false);
    assert.strictEqual(status.canElevate, true);
  });

  it('uses sudo when only sudo is available (inactive)', () => {
    const status = evaluatePrivilege({
      user: 'apps',
      run0Success: false,
      run0Available: false,
      sudoSuccess: false,
      sudoAvailable: true,
    });

    assert.strictEqual(status.method, 'sudo');
    assert.strictEqual(status.active, false);
    assert.strictEqual(status.canElevate, true);
  });

  it('returns none when neither run0 nor sudo is available', () => {
    const status = evaluatePrivilege({
      user: 'guest',
      run0Success: false,
      run0Available: false,
      sudoSuccess: false,
      sudoAvailable: false,
    });

    assert.strictEqual(status.method, 'none');
    assert.strictEqual(status.active, false);
    assert.strictEqual(status.canElevate, false);
  });

  it('marks root user as active with none method', () => {
    const status = evaluatePrivilege({
      user: 'root',
      run0Success: false,
      run0Available: false,
      sudoSuccess: false,
      sudoAvailable: false,
    });

    assert.strictEqual(status.method, 'none');
    assert.strictEqual(status.active, true);
    assert.strictEqual(status.canElevate, true);
    assert.strictEqual(status.user, 'root');
  });
});

describe('Privilege Escalation Helper - Output Parsing', () => {
  it('parses probe output with active run0 and active sudo', () => {
    const output = [
      'USER:core',
      'RUN0:ACTIVE',
      'SUDO:ACTIVE',
    ].join('\n');

    const status = parsePrivilegeProbeOutput(output, 'core');
    assert.strictEqual(status.method, 'run0');
    assert.strictEqual(status.active, true);
    assert.strictEqual(status.canElevate, true);
    assert.strictEqual(status.user, 'core');
  });

  it('parses probe output with inactive run0 and active sudo', () => {
    const output = [
      'USER:core',
      'RUN0:AVAILABLE',
      'SUDO:ACTIVE',
    ].join('\n');

    const status = parsePrivilegeProbeOutput(output, 'core');
    assert.strictEqual(status.method, 'sudo');
    assert.strictEqual(status.active, true);
    assert.strictEqual(status.canElevate, true);
  });

  it('parses probe output where both are available but passwordless is false', () => {
    const output = [
      'USER:operator',
      'RUN0:AVAILABLE',
      'SUDO:AVAILABLE',
    ].join('\n');

    const status = parsePrivilegeProbeOutput(output, 'operator');
    assert.strictEqual(status.method, 'run0');
    assert.strictEqual(status.active, false);
    assert.strictEqual(status.canElevate, true);
    assert.strictEqual(status.user, 'operator');
  });

  it('parses probe output where system has no run0 and no sudo', () => {
    const output = [
      'USER:nobody',
      'RUN0:NONE',
      'SUDO:NONE',
    ].join('\n');

    const status = parsePrivilegeProbeOutput(output, 'nobody');
    assert.strictEqual(status.method, 'none');
    assert.strictEqual(status.active, false);
    assert.strictEqual(status.canElevate, false);
  });

  it('parses root user correctly from probe output', () => {
    const output = [
      'USER:root',
      'RUN0:ACTIVE',
      'SUDO:ACTIVE',
    ].join('\n');

    const status = parsePrivilegeProbeOutput(output, 'root');
    assert.strictEqual(status.user, 'root');
    assert.strictEqual(status.active, true);
    assert.strictEqual(status.canElevate, true);
  });
});

describe('Privilege Escalation Helper - Badge Formatting', () => {
  it('formats badge for root user', () => {
    const status: PrivilegeStatus = {
      method: 'none',
      active: true,
      user: 'root',
      canElevate: true,
    };
    const badge = formatPrivilegeBadge(status);
    assert.strictEqual(badge.label, 'root');
    assert.strictEqual(badge.icon, 'verified_user');
  });

  it('formats badge for active run0', () => {
    const status: PrivilegeStatus = {
      method: 'run0',
      active: true,
      user: 'core',
      canElevate: true,
    };
    const badge = formatPrivilegeBadge(status);
    assert.strictEqual(badge.label, 'run0 (Active)');
    assert.strictEqual(badge.icon, 'bolt');
  });

  it('formats badge for active sudo', () => {
    const status: PrivilegeStatus = {
      method: 'sudo',
      active: true,
      user: 'core',
      canElevate: true,
    };
    const badge = formatPrivilegeBadge(status);
    assert.strictEqual(badge.label, 'sudo (Active)');
    assert.strictEqual(badge.icon, 'bolt');
  });

  it('formats badge for unprivileged user with no elevation available', () => {
    const status: PrivilegeStatus = {
      method: 'none',
      active: false,
      user: 'core',
      canElevate: false,
    };
    const badge = formatPrivilegeBadge(status);
    assert.strictEqual(badge.label, 'Unprivileged (core)');
    assert.strictEqual(badge.icon, 'lock');
  });
});

describe('Privilege Escalation Helper - Probe Command Generation', () => {
  it('generates a valid shell command probing user, run0, and sudo', () => {
    const cmd = buildPrivilegeProbeCommand();
    assert.ok(cmd.includes('run0'));
    assert.ok(cmd.includes('sudo'));
    assert.ok(cmd.includes('USER:'));
  });
});

describe('Privilege Escalation Express Routes', () => {
  let server: Server;
  let baseUrl: string;

  const mockNode: NodeItem = {
    id: 'node-test-1',
    name: 'test-node',
    shortName: 'test-node',
    group: 'tag:host',
    datacenter: 'leo-banjo.ts.net',
    nodeType: 'linux',
    ip: '100.1.2.3',
    port: 22,
    sshUser: 'core',
    tailscaleIp: '100.1.2.3',
    tailscaleHost: 'test-node.leo-banjo.ts.net',
    osDescription: 'AlmaLinux 10.2',
    status: 'online',
    uptime: '1d',
    hasGlances: false,
    hasCockpit: true,
    hasPodman: true,
    hasRestic: true,
    specs: {
      cpu: 'Mock CPU',
      vcpus: 4,
      ramGiB: 16,
      motherboard: 'Mock',
      bios: 'Mock',
      ipmi: 'N/A',
      ecc: 'Non-ECC',
      secureBoot: 'Enabled',
      iommu: 'Enabled',
    },
  };

  let mockProbeResult: PrivilegeStatus = {
    method: 'run0',
    active: true,
    user: 'core',
    canElevate: true,
  };

  let mockTestResult = {
    success: true,
    method: 'run0' as PrivilegeMethod,
    output: '0',
    error: undefined as string | undefined,
  };

  beforeEach(async () => {
    const app = express();
    app.use(express.json());

    registerPrivilegeRoutes(app, {
      findNode: (id) => (id === 'node-test-1' ? mockNode : null),
      probeNodePrivilege: async () => mockProbeResult,
      testNodePrivilege: async (_node, method) => ({
        ...mockTestResult,
        method: method || mockTestResult.method,
      }),
    });

    await new Promise<void>((resolve) => {
      server = app.listen(0, '127.0.0.1', () => {
        const addr = server.address() as AddressInfo;
        baseUrl = `http://127.0.0.1:${addr.port}`;
        resolve();
      });
    });
  });

  afterEach(async () => {
    if (server) {
      await new Promise<void>((resolve) => server.close(() => resolve()));
    }
  });

  it('GET /api/nodes/:id/privilege returns privilege status for known node', async () => {
    const res = await fetch(`${baseUrl}/api/nodes/node-test-1/privilege`);
    assert.strictEqual(res.status, 200);
    const data = (await res.json()) as PrivilegeStatus;
    assert.strictEqual(data.method, 'run0');
    assert.strictEqual(data.active, true);
    assert.strictEqual(data.user, 'core');
    assert.strictEqual(data.canElevate, true);
  });

  it('GET /api/nodes/:id/privilege returns 404 for unknown node', async () => {
    const res = await fetch(`${baseUrl}/api/nodes/non-existent-node/privilege`);
    assert.strictEqual(res.status, 404);
  });

  it('POST /api/nodes/:id/privilege/test runs elevation test and returns success', async () => {
    const res = await fetch(`${baseUrl}/api/nodes/node-test-1/privilege/test`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ method: 'run0' }),
    });
    assert.strictEqual(res.status, 200);
    const data = (await res.json()) as any;
    assert.strictEqual(data.success, true);
    assert.strictEqual(data.method, 'run0');
    assert.strictEqual(data.output, '0');
  });

  it('POST /api/nodes/:id/privilege/test returns failure when elevation test fails', async () => {
    mockTestResult = {
      success: false,
      method: 'sudo',
      output: '',
      error: 'sudo: a password is required',
    };

    const res = await fetch(`${baseUrl}/api/nodes/node-test-1/privilege/test`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ method: 'sudo' }),
    });
    assert.strictEqual(res.status, 200);
    const data = (await res.json()) as any;
    assert.strictEqual(data.success, false);
    assert.strictEqual(data.method, 'sudo');
    assert.strictEqual(data.error, 'sudo: a password is required');
  });

  it('POST /api/nodes/:id/privilege/test returns 404 for unknown node', async () => {
    const res = await fetch(`${baseUrl}/api/nodes/unknown/privilege/test`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ method: 'run0' }),
    });
    assert.strictEqual(res.status, 404);
  });
});
