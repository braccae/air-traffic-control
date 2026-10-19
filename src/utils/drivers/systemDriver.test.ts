import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import type { NodeItem } from '../../types/index.ts';
import {
  registerDriver,
  getDriver,
  getAllDrivers,
  systemDriver,
  storageDriver,
  networkDriver,
  compileDaemonPluginMutation,
  type MutationDriver,
} from './index.ts';

const testNode: NodeItem = {
  id: 'node-fedora',
  name: 'fedora-server',
  shortName: 'fedora-srv',
  group: 'infra',
  datacenter: 'dc1',
  nodeType: 'linux',
  ip: '192.168.1.100',
  port: 22,
  sshUser: 'core',
  tailscaleIp: '100.64.0.1',
  tailscaleHost: 'fedora-srv.ts.net',
  osDescription: 'Fedora CoreOS 40',
  initSystem: 'systemd',
  status: 'online',
  uptime: '4d 2h',
  hasGlances: true,
  hasCockpit: true,
  hasPodman: true,
  hasRestic: false,
  specs: {
    cpu: 'Intel Core i7',
    vcpus: 8,
    ramGiB: 32,
    motherboard: '',
    bios: '',
    ipmi: '',
    ecc: '',
    secureBoot: '',
    iommu: '',
  },
  privilegeMethod: 'run0',
};

describe('Pluggable Driver Registry & Daemon Proxy Delegation', () => {
  it('registers and retrieves default daemon proxy drivers', () => {
    const sys = getDriver('system', testNode);
    assert.ok(sys, 'System driver should be resolved for node');
    assert.strictEqual(sys?.category, 'system');

    const storage = getDriver('storage', testNode);
    assert.ok(storage, 'Storage driver should be resolved for node');
    assert.strictEqual(storage?.category, 'storage');

    const net = getDriver('network', testNode);
    assert.ok(net, 'Network driver should be resolved for node');
    assert.strictEqual(net?.category, 'network');

    const all = getAllDrivers();
    assert.ok(all.length >= 3);
  });

  it('allows registering custom mutation drivers with higher precedence', () => {
    const customDriver: MutationDriver = {
      category: 'custom' as any,
      supports: (node: NodeItem) => node.id === 'node-fedora',
      compileMutation: (node, action, payload) => ({
        id: 'task-custom-1',
        title: `Custom Action ${action} on ${node.shortName}`,
        targetNodeId: node.id,
        targetNodeName: node.name,
        targetHostRoute: node.ip,
        targetSshUser: node.sshUser || 'admin',
        targetSshPort: node.port || 22,
        category: 'system',
        primaryModule: 'custom.module',
        steps: [{ name: action, module: 'custom.module', parameters: payload }],
        become: false,
        becomeMethod: 'sudo',
        status: 'staged',
        createdAt: Date.now(),
      }),
    };

    registerDriver(customDriver);
    const resolved = getDriver('custom' as any, testNode);
    assert.ok(resolved);
    assert.strictEqual(resolved?.category, 'custom');
  });

  it('compileDaemonPluginMutation posts action to Go daemon endpoint and normalizes task', async () => {
    const originalFetch = globalThis.fetch;
    try {
      let interceptedUrl = '';
      let interceptedMethod = '';
      let interceptedBody: any = null;

      globalThis.fetch = (async (input: any, init?: RequestInit) => {
        interceptedUrl = String(input);
        interceptedMethod = init?.method || 'GET';
        interceptedBody = init?.body ? JSON.parse(String(init.body)) : null;

        return {
          ok: true,
          status: 200,
          json: async () => ({
            name: 'Reboot node fedora-srv',
            steps: [
              {
                name: 'Reboot system',
                module: 'ansible.builtin.reboot',
                params: { reboot_timeout: 300, msg: 'Safety reboot from Cockpit' },
              },
            ],
            secretsMasked: {},
          }),
        } as any;
      }) as any;

      const task = await compileDaemonPluginMutation(testNode, 'system', 'service', 'reboot', {
        timeout: 300,
        msg: 'Safety reboot from Cockpit',
      });

      assert.strictEqual(
        interceptedUrl,
        `/api/nodes/${encodeURIComponent(testNode.id)}/plugins/system/service/actions/reboot`
      );
      assert.strictEqual(interceptedMethod, 'POST');
      assert.strictEqual(interceptedBody?.options?.timeout, 300);

      assert.strictEqual(task.targetNodeId, 'node-fedora');
      assert.strictEqual(task.targetNodeName, 'fedora-srv');
      assert.strictEqual(task.category, 'system');
      assert.strictEqual(task.primaryModule, 'ansible.builtin.reboot');
      assert.strictEqual(task.steps.length, 1);
      assert.strictEqual(task.steps[0].module, 'ansible.builtin.reboot');
      assert.strictEqual(task.steps[0].parameters.reboot_timeout, 300);
      assert.strictEqual(task.connection?.plugin, 'connection/tailscale');
      assert.strictEqual(task.status, 'staged');
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  it('fails loudly when Go daemon endpoint returns an error without mock fallback', async () => {
    const originalFetch = globalThis.fetch;
    try {
      globalThis.fetch = (async () => ({
        ok: false,
        status: 500,
        json: async () => ({ error: 'action failed in daemon plugin' }),
      })) as any;

      await assert.rejects(
        async () => {
          await compileDaemonPluginMutation(testNode, 'system', 'service', 'restart', { service: 'nginx' });
        },
        /Daemon failed to compile action restart/
      );
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  it('systemDriver delegates to compileDaemonPluginMutation', async () => {
    const originalFetch = globalThis.fetch;
    try {
      let calledUrl = '';
      globalThis.fetch = (async (input: any) => {
        calledUrl = String(input);
        return {
          ok: true,
          status: 200,
          json: async () => ({
            name: 'Restart service podman',
            steps: [{ name: 'restart podman', module: 'ansible.builtin.systemd', params: { name: 'podman', state: 'restarted' } }],
          }),
        } as any;
      }) as any;

      const task = await systemDriver.compileMutation(testNode, 'restart', { service: 'podman' });
      assert.strictEqual(calledUrl, `/api/nodes/${encodeURIComponent(testNode.id)}/plugins/system/service/actions/restart`);
      assert.strictEqual(task.steps[0].parameters.name, 'podman');
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  it('storageDriver delegates SMB and NFS actions to Go daemon share plugins', async () => {
    const originalFetch = globalThis.fetch;
    try {
      const urls: string[] = [];
      globalThis.fetch = (async (input: any) => {
        urls.push(String(input));
        return {
          ok: true,
          status: 200,
          json: async () => ({
            name: 'Share action',
            steps: [{ name: 'step', module: 'ansible.builtin.template', params: {} }],
          }),
        } as any;
      }) as any;

      await storageDriver.compileMutation(testNode, 'smb', { name: 'backups', path: '/tank/backups' });
      await storageDriver.compileMutation(testNode, 'nfs', { path: '/tank/nfs', client: '*' });

      assert.strictEqual(urls[0], `/api/nodes/${encodeURIComponent(testNode.id)}/plugins/share/smb/actions/create`);
      assert.strictEqual(urls[1], `/api/nodes/${encodeURIComponent(testNode.id)}/plugins/share/nfs/actions/export`);
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  it('networkDriver delegates interface and firewall actions to Go daemon', async () => {
    const originalFetch = globalThis.fetch;
    try {
      const urls: string[] = [];
      globalThis.fetch = (async (input: any) => {
        urls.push(String(input));
        return {
          ok: true,
          status: 200,
          json: async () => ({
            name: 'Network action',
            steps: [{ name: 'step', module: 'ansible.posix.firewalld', params: {} }],
          }),
        } as any;
      }) as any;

      await networkDriver.compileMutation(testNode, 'connect_interface', { name: 'wg0', type: 'wireguard' });
      await networkDriver.compileMutation(testNode, 'firewall', { port: '8080', proto: 'tcp' });

      assert.strictEqual(urls[0], `/api/nodes/${encodeURIComponent(testNode.id)}/plugins/network/interfaces/actions/configure`);
      assert.strictEqual(urls[1], `/api/nodes/${encodeURIComponent(testNode.id)}/plugins/firewall/firewalld/actions/add_rule`);
    } finally {
      globalThis.fetch = originalFetch;
    }
  });
});

describe('Ansible Galaxy Collection Security Guard', () => {
  it('validates safe collection names and rejects shell injection attempts', async () => {
    const { isValidCollectionName } = await import('./index.ts');

    assert.strictEqual(isValidCollectionName('ansible.posix'), true);
    assert.strictEqual(isValidCollectionName('community.general'), true);
    assert.strictEqual(isValidCollectionName('containers.podman'), true);
    assert.strictEqual(isValidCollectionName('cisco.ios'), true);

    assert.strictEqual(isValidCollectionName('ansible.posix; rm -rf /'), false);
    assert.strictEqual(isValidCollectionName('`whoami`'), false);
    assert.strictEqual(isValidCollectionName('ansible.posix & whoami'), false);
    assert.strictEqual(isValidCollectionName('posix'), false);
    assert.strictEqual(isValidCollectionName('ansible.posix.extra'), false);
    assert.strictEqual(isValidCollectionName('../../../etc/passwd'), false);
    assert.strictEqual(isValidCollectionName('ansible.posix$foo'), false);
    assert.strictEqual(isValidCollectionName(''), false);
  });
});
