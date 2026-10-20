import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  compileServiceTask,
  compileFirewallTask,
  compileShareTask,
  compileContainerTask,
  compilePackageTask,
  compileOnTheFlyPlaybook,
} from './ansibleQueueCompiler.ts';
import type { NodeItem, QueuedAnsibleTask } from '../types/index.ts';

function expect<T>(actual: T) {
  return {
    toBe(expected: any) {
      assert.strictEqual(actual, expected);
    },
    toEqual(expected: any) {
      assert.deepStrictEqual(actual, expected);
    },
    toContain(expected: string) {
      assert.ok(
        typeof actual === 'string' && actual.includes(expected),
        `Expected ${JSON.stringify(actual)} to contain ${JSON.stringify(expected)}`
      );
    },
    toBeDefined() {
      assert.notStrictEqual(actual, undefined);
    },
  };
}

const mockNode: NodeItem = {
  id: 'node-1',
  name: 'likes-it-cold',
  shortName: 'likes-it-cold',
  datacenter: 'homelab',
  nodeType: 'linux',
  ip: '192.168.16.50',
  tailscaleIp: '100.115.92.19',
  tailscaleHost: 'likes-it-cold.ts.net',
  sshUser: 'core',
  port: 22,
  status: 'online',
  uptime: '10d',
  hasGlances: false,
  hasCockpit: false,
  hasPodman: true,
  hasRestic: false,
  specs: {
    cpu: 'Intel',
    vcpus: 4,
    ramGiB: 16,
    motherboard: '',
    bios: '',
    ipmi: '',
    ecc: '',
    secureBoot: '',
    iommu: '',
  },
  osDescription: 'Fedora CoreOS',
  group: 'tag:host',
};

describe('ansibleQueueCompiler', () => {
  it('compiles service mutation using native ansible.builtin.systemd_service with run0', () => {
    const task = compileServiceTask(mockNode, 'caddy', 'restarted');
    expect(task.primaryModule).toBe('ansible.builtin.systemd_service');
    expect(task.becomeMethod).toBe('run0');
    expect(task.become).toBe(true);
    expect(task.category).toBe('system');
    expect(task.targetHostRoute).toBe('likes-it-cold.ts.net');
    expect(task.targetSshUser).toBe('core');
    expect(task.targetSshPort).toBe(22);
    expect(task.steps.length).toBe(1);
    expect(task.steps[0].module).toBe('ansible.builtin.systemd_service');
    expect(task.steps[0].parameters.name).toBe('caddy');
    expect(task.steps[0].parameters.state).toBe('restarted');

    const yaml = compileOnTheFlyPlaybook(task);
    expect(yaml).toContain('ansible.builtin.systemd_service:');
    expect(yaml).toContain('ansible_host: "likes-it-cold.ts.net"');
    expect(yaml).toContain('ansible_user: "core"');
    expect(yaml).toContain('ansible_port: 22');
    expect(yaml).toContain('become: true');
    expect(yaml).toContain('become_method: run0');
  });

  it('compiles share mutation with masked secrets in task metadata', () => {
    const task = compileShareTask(mockNode, 'media_vault', '/mnt/media', ['core', 'media'], 'vault_smb_media');
    expect(task.primaryModule).toBe('ansible.builtin.template');
    expect(task.category).toBe('storage');
    expect(task.secretsMasked?.smb_password).toBe('•••••••• (vault: vault_smb_media)');

    const unvaultedTask = compileShareTask(mockNode, 'public_share', '/mnt/public', ['core']);
    expect(unvaultedTask.secretsMasked?.smb_password).toBe('••••••••');
  });

  it('compiles firewall mutation using ansible.posix.firewalld', () => {
    const task = compileFirewallTask(mockNode, '8080', 'tcp', 'public');
    expect(task.primaryModule).toBe('ansible.posix.firewalld');
    expect(task.category).toBe('network');
    expect(task.become).toBe(true);
    expect(task.steps[0].parameters.port).toBe('8080/tcp');
    expect(task.steps[0].parameters.zone).toBe('public');
    expect(task.steps[0].parameters.permanent).toBe(true);

    const yaml = compileOnTheFlyPlaybook(task);
    expect(yaml).toContain('ansible.posix.firewalld:');
    expect(yaml).toContain('port: 8080/tcp');
    expect(yaml).toContain('zone: public');
  });

  it('compiles container mutation using containers.podman.podman_container', () => {
    const startTask = compileContainerTask(mockNode, 'traefik', 'start');
    expect(startTask.primaryModule).toBe('containers.podman.podman_container');
    expect(startTask.category).toBe('container');
    expect(startTask.steps[0].parameters.state).toBe('started');

    const stopTask = compileContainerTask(mockNode, 'traefik', 'stop');
    expect(stopTask.steps[0].parameters.state).toBe('stopped');

    const restartTask = compileContainerTask(mockNode, 'traefik', 'restart');
    expect(restartTask.steps[0].parameters.state).toBe('restarted');

    const yaml = compileOnTheFlyPlaybook(restartTask);
    expect(yaml).toContain('containers.podman.podman_container:');
    expect(yaml).toContain('name: traefik');
    expect(yaml).toContain('state: restarted');
  });

  it('compiles package mutation using ansible.builtin.package', () => {
    const task = compilePackageTask(mockNode, 'htop', 'present');
    expect(task.primaryModule).toBe('ansible.builtin.package');
    expect(task.category).toBe('package');
    expect(task.become).toBe(true);
    expect(task.steps[0].parameters.name).toBe('htop');
    expect(task.steps[0].parameters.state).toBe('present');

    const yaml = compileOnTheFlyPlaybook(task);
    expect(yaml).toContain('ansible.builtin.package:');
    expect(yaml).toContain('name: htop');
    expect(yaml).toContain('state: present');
  });

  it('respects node privilege method when configured', () => {
    const sudoNode: NodeItem = {
      ...mockNode,
      privilegeMethod: 'sudo',
    };
    const task = compileServiceTask(sudoNode, 'nginx', 'started');
    expect(task.becomeMethod).toBe('sudo');

    const yaml = compileOnTheFlyPlaybook(task);
    expect(yaml).toContain('become_method: sudo');
  });

  it('prioritizes node.shortName over node.name for clean targetNodeName inventory targeting', () => {
    const nodeWithDisplayName: NodeItem = {
      ...mockNode,
      id: 'node-dev-pc',
      name: 'dev-pc (local workstation)',
      shortName: 'dev-pc',
    };

    const serviceTask = compileServiceTask(nodeWithDisplayName, 'nginx', 'restarted');
    expect(serviceTask.targetNodeName).toBe('dev-pc');

    const firewallTask = compileFirewallTask(nodeWithDisplayName, '80', 'tcp');
    expect(firewallTask.targetNodeName).toBe('dev-pc');

    const shareTask = compileShareTask(nodeWithDisplayName, 'share', '/path', ['user']);
    expect(shareTask.targetNodeName).toBe('dev-pc');

    const containerTask = compileContainerTask(nodeWithDisplayName, 'redis', 'start');
    expect(containerTask.targetNodeName).toBe('dev-pc');

    const packageTask = compilePackageTask(nodeWithDisplayName, 'git', 'present');
    expect(packageTask.targetNodeName).toBe('dev-pc');
  });

  it('compiles playbook honoring connection block with local and winrm plugins', () => {
    const localTask: QueuedAnsibleTask = {
      id: 'task-local',
      title: 'Local task',
      targetNodeId: 'localhost',
      targetNodeName: 'localhost',
      category: 'system',
      primaryModule: 'ansible.builtin.command',
      steps: [{ name: 'test', module: 'ansible.builtin.command', parameters: { cmd: 'hostname' } }],
      become: false,
      status: 'staged',
      createdAt: Date.now(),
      connection: {
        plugin: 'connection/local',
        host: 'localhost',
        user: 'root',
        port: 0,
      },
    };

    const yaml = compileOnTheFlyPlaybook(localTask);
    expect(yaml).toContain('ansible_connection: "local"');
    expect(yaml).toContain('ansible_host: "localhost"');

    const winrmTask: QueuedAnsibleTask = {
      ...localTask,
      id: 'task-winrm',
      connection: {
        plugin: 'connection/winrm',
        host: '192.168.1.200',
        user: 'Administrator',
        port: 5986,
      },
    };
    const winrmYaml = compileOnTheFlyPlaybook(winrmTask);
    expect(winrmYaml).toContain('ansible_connection: "winrm"');
    expect(winrmYaml).toContain('ansible_host: "192.168.1.200"');
  });
});
