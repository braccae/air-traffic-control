import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import type { NodeItem } from '../../types/index.ts';
import {
  registerDriver,
  getDriver,
  systemDriver,
  storageDriver,
  networkDriver,
  type MutationDriver,
} from './index.ts';
import { SystemDriver } from './systemDriver.ts';
import { StorageDriver } from './storageDriver.ts';
import { NetworkDriver } from './networkDriver.ts';

const systemdNode: NodeItem = {
  id: 'node-systemd',
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

const openrcNode: NodeItem = {
  id: 'node-alpine',
  name: 'alpine-gateway',
  shortName: 'alpine-gw',
  group: 'network',
  datacenter: 'dc1',
  nodeType: 'linux',
  ip: '192.168.1.1',
  port: 22,
  sshUser: 'root',
  tailscaleIp: '100.64.0.2',
  tailscaleHost: 'alpine-gw.ts.net',
  osDescription: 'Alpine Linux v3.20',
  initSystem: 'openrc',
  status: 'online',
  uptime: '12d 8h',
  hasGlances: false,
  hasCockpit: false,
  hasPodman: false,
  hasRestic: false,
  specs: {
    cpu: 'AMD GX-412TC',
    vcpus: 4,
    ramGiB: 4,
    motherboard: '',
    bios: '',
    ipmi: '',
    ecc: '',
    secureBoot: '',
    iommu: '',
  },
  privilegeMethod: 'doas',
};

const switchNode: NodeItem = {
  id: 'switch-core-01',
  name: 'core-switch-01',
  shortName: 'c-sw01',
  group: 'network',
  datacenter: 'dc1',
  nodeType: 'router_switch',
  ip: '192.168.1.254',
  port: 22,
  sshUser: 'admin',
  tailscaleIp: '100.64.0.254',
  tailscaleHost: 'c-sw01.ts.net',
  osDescription: 'Cisco IOS-XE 17.3',
  status: 'online',
  uptime: '120d',
  hasGlances: false,
  hasCockpit: false,
  hasPodman: false,
  hasRestic: false,
  specs: {
    cpu: 'PowerPC',
    vcpus: 1,
    ramGiB: 2,
    motherboard: '',
    bios: '',
    ipmi: '',
    ecc: '',
    secureBoot: '',
    iommu: '',
  },
};

describe('Pluggable Driver Architecture & Registry', () => {
  it('registers and retrieves default built-in drivers', () => {
    const sys = getDriver('system', systemdNode);
    assert.ok(sys, 'System driver should be resolved for systemd node');
    assert.strictEqual(sys?.category, 'system');

    const storage = getDriver('storage', systemdNode);
    assert.ok(storage, 'Storage driver should be resolved for systemd node');
    assert.strictEqual(storage?.category, 'storage');

    const net = getDriver('network', systemdNode);
    assert.ok(net, 'Network driver should be resolved for systemd node');
    assert.strictEqual(net?.category, 'network');
  });

  it('allows registering custom mutation drivers', () => {
    const customDriver: MutationDriver = {
      category: 'custom',
      supports: (node: NodeItem) => node.nodeType === 'router_switch',
      compileMutation: (node, action, payload) => ({
        id: 'task-custom-1',
        title: `Custom Action ${action} on ${node.shortName}`,
        targetNodeId: node.id,
        targetNodeName: node.name,
        targetHostRoute: node.ip,
        targetSshUser: node.sshUser || 'admin',
        targetSshPort: node.port || 22,
        category: 'custom',
        primaryModule: 'custom.module',
        steps: [{ name: action, module: 'custom.module', parameters: payload }],
        become: false,
        becomeMethod: 'sudo',
        status: 'staged',
        createdAt: Date.now(),
      }),
    };

    registerDriver(customDriver);
    const resolved = getDriver('custom', switchNode);
    assert.ok(resolved);
    assert.strictEqual(resolved?.category, 'custom');
    assert.strictEqual(resolved?.supports(switchNode), true);
  });
});

describe('SystemDriver', () => {
  it('uses ansible.builtin.systemd for standard systemd nodes', () => {
    const driver = new SystemDriver();
    assert.strictEqual(driver.supports(systemdNode), true);

    const task = driver.compileMutation(systemdNode, 'restart', {
      service: 'podman.service',
    });

    assert.strictEqual(task.category, 'system');
    assert.strictEqual(task.primaryModule, 'ansible.builtin.systemd');
    assert.strictEqual(task.steps.length, 1);
    assert.strictEqual(task.steps[0].module, 'ansible.builtin.systemd');
    assert.strictEqual(task.steps[0].parameters.name, 'podman.service');
    assert.strictEqual(task.steps[0].parameters.state, 'restarted');
    assert.strictEqual(task.become, true);
    assert.strictEqual(task.becomeMethod, 'run0');
  });

  it('falls back to ansible.builtin.service for non-systemd (OpenRC / Alpine) nodes', () => {
    const driver = new SystemDriver();
    assert.strictEqual(driver.supports(openrcNode), true);

    const task = driver.compileMutation(openrcNode, 'restart', {
      service: 'sshd',
    });

    assert.strictEqual(task.category, 'system');
    assert.strictEqual(task.primaryModule, 'ansible.builtin.service');
    assert.strictEqual(task.steps.length, 1);
    assert.strictEqual(task.steps[0].module, 'ansible.builtin.service');
    assert.strictEqual(task.steps[0].parameters.name, 'sshd');
    assert.strictEqual(task.steps[0].parameters.state, 'restarted');
    assert.strictEqual(task.become, true);
    assert.strictEqual(task.becomeMethod, 'doas');
  });

  it('compiles system reboot mutation using ansible.builtin.reboot', () => {
    const driver = new SystemDriver();
    const task = driver.compileMutation(systemdNode, 'reboot', {
      timeout: 600,
      message: 'Scheduled kernel update reboot',
    });

    assert.strictEqual(task.category, 'system');
    assert.strictEqual(task.primaryModule, 'ansible.builtin.reboot');
    assert.strictEqual(task.steps.length, 1);
    assert.strictEqual(task.steps[0].module, 'ansible.builtin.reboot');
    assert.strictEqual(task.steps[0].parameters.reboot_timeout, 600);
    assert.strictEqual(task.steps[0].parameters.msg, 'Scheduled kernel update reboot');
    assert.strictEqual(task.become, true);
    assert.strictEqual(task.status, 'staged');
  });
});

describe('StorageDriver', () => {
  it('compiles SMB share creation with secret masking', () => {
    const driver = new StorageDriver();
    assert.strictEqual(driver.supports(systemdNode), true);

    const task = driver.compileMutation(systemdNode, 'smb', {
      name: 'backups',
      path: '/tank/backups',
      users: ['core', 'admin'],
      passwordVaultKey: 'vault_smb_secret',
    });

    assert.strictEqual(task.category, 'storage');
    assert.strictEqual(task.primaryModule, 'ansible.builtin.template');
    assert.ok(task.secretsMasked);
    assert.strictEqual(
      task.secretsMasked?.smb_password,
      '•••••••• (vault: vault_smb_secret)'
    );
    assert.strictEqual(task.steps[0].parameters.share_path, '/tank/backups');
  });

  it('compiles NFS share export mutation', () => {
    const driver = new StorageDriver();
    const task = driver.compileMutation(systemdNode, 'nfs', {
      name: 'nfs-media',
      path: '/tank/media',
      client: '192.168.1.0/24',
      options: 'rw,sync,no_subtree_check',
    });

    assert.strictEqual(task.category, 'storage');
    assert.ok(task.steps.length >= 1);
    assert.strictEqual(task.steps[0].module, 'ansible.builtin.lineinfile');
    assert.strictEqual(task.steps[0].parameters.path, '/etc/exports');
  });
});

describe('NetworkDriver', () => {
  it('compiles Linux firewall rules using ansible.posix.firewalld', () => {
    const driver = new NetworkDriver();
    assert.strictEqual(driver.supports(systemdNode), true);

    const task = driver.compileMutation(systemdNode, 'firewall', {
      port: '8080',
      proto: 'tcp',
      zone: 'public',
    });

    assert.strictEqual(task.category, 'network');
    assert.strictEqual(task.primaryModule, 'ansible.posix.firewalld');
    assert.strictEqual(task.steps[0].parameters.port, '8080/tcp');
    assert.strictEqual(task.steps[0].parameters.zone, 'public');
  });

  it('compiles network interface configuration', () => {
    const driver = new NetworkDriver();
    const task = driver.compileMutation(systemdNode, 'connect_interface', {
      name: 'wg0',
      type: 'wireguard',
      cidr: '10.0.0.1/24',
    });

    assert.strictEqual(task.category, 'network');
    assert.ok(task.steps.length >= 1);
    assert.strictEqual(task.steps[0].parameters.interface, 'wg0');
  });

  it('supports network switch nodes with switch configuration modules', () => {
    const driver = new NetworkDriver();
    assert.strictEqual(driver.supports(switchNode), true);

    const task = driver.compileMutation(switchNode, 'interface', {
      name: 'GigabitEthernet0/1',
      description: 'Uplink to Core Router',
      vlanId: 10,
    });

    assert.strictEqual(task.category, 'network');
    assert.strictEqual(task.primaryModule, 'cisco.ios.ios_config');
    assert.strictEqual(task.steps[0].module, 'cisco.ios.ios_config');
  });
});

describe('Ansible Galaxy Collection Security Guard', () => {
  it('validates safe collection names and rejects shell injection attempts', async () => {
    const { isValidCollectionName } = await import('./index.ts');

    // Valid collection names
    assert.strictEqual(isValidCollectionName('ansible.posix'), true);
    assert.strictEqual(isValidCollectionName('community.general'), true);
    assert.strictEqual(isValidCollectionName('containers.podman'), true);
    assert.strictEqual(isValidCollectionName('cisco.ios'), true);

    // Malicious or invalid collection names
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

