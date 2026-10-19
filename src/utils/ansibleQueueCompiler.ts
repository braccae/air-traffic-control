import type {
  NodeItem,
  QueuedAnsibleTask,
  AnsibleTaskStep,
} from '../types/index.ts';
import { resolveConnectionRoute } from './hardwareProbe.ts';

/**
 * Resolves the preferred privilege elevation method for the node,
 * favoring systemd 256+ run0 while honoring explicit sudo or doas configurations.
 */
export function resolveBecomeMethod(node: NodeItem): 'run0' | 'sudo' | 'doas' {
  if (node.privilegeMethod === 'sudo' || node.privilegeMethod === 'doas') {
    return node.privilegeMethod;
  }
  return 'run0';
}

/**
 * Resolves network connection target, SSH user, and port for a node.
 */
export function resolveNodeConnection(node: NodeItem): {
  hostRoute: string;
  sshUser: string;
  sshPort: number;
} {
  let hostRoute = '';
  try {
    const route = resolveConnectionRoute(node);
    hostRoute = route.primary;
  } catch {
    hostRoute = node.tailscaleHost || node.tailscaleIp || node.ip || 'localhost';
  }
  if (!hostRoute) {
    hostRoute = node.tailscaleHost || node.tailscaleIp || node.ip || 'localhost';
  }

  const sshUser = node.sshUser || 'core';
  const sshPort = node.port || 22;

  return { hostRoute, sshUser, sshPort };
}

/**
 * Compiles a system service mutation into a QueuedAnsibleTask using ansible.builtin.systemd.
 */
export function compileServiceTask(
  node: NodeItem,
  serviceName: string,
  action: 'restarted' | 'started' | 'stopped' | 'reloaded'
): QueuedAnsibleTask {
  const { hostRoute, sshUser, sshPort } = resolveNodeConnection(node);
  const becomeMethod = resolveBecomeMethod(node);
  const actionTitle = action.charAt(0).toUpperCase() + action.slice(1);

  const step: AnsibleTaskStep = {
    name: `${actionTitle} ${serviceName} service`,
    module: 'ansible.builtin.systemd',
    parameters: {
      name: serviceName,
      state: action,
    },
  };

  return {
    id: `task-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
    title: `${actionTitle} service ${serviceName}`,
    targetNodeId: node.id,
    targetNodeName: node.name || node.shortName || node.id,
    targetHostRoute: hostRoute,
    targetSshUser: sshUser,
    targetSshPort: sshPort,
    category: 'system',
    primaryModule: 'ansible.builtin.systemd',
    steps: [step],
    become: true,
    becomeMethod,
    status: 'staged',
    createdAt: Date.now(),
  };
}

/**
 * Compiles a firewall mutation into a QueuedAnsibleTask using ansible.posix.firewalld.
 */
export function compileFirewallTask(
  node: NodeItem,
  port: string,
  proto: string,
  zone?: string
): QueuedAnsibleTask {
  const { hostRoute, sshUser, sshPort } = resolveNodeConnection(node);
  const becomeMethod = resolveBecomeMethod(node);
  const portProto = `${port}/${proto}`;

  const parameters: Record<string, any> = {
    port: portProto,
    permanent: true,
    state: 'enabled',
    immediate: true,
  };
  if (zone) {
    parameters.zone = zone;
  }

  const step: AnsibleTaskStep = {
    name: `Allow port ${portProto} in firewall`,
    module: 'ansible.posix.firewalld',
    parameters,
  };

  return {
    id: `task-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
    title: `Allow port ${portProto} through firewall`,
    targetNodeId: node.id,
    targetNodeName: node.name || node.shortName || node.id,
    targetHostRoute: hostRoute,
    targetSshUser: sshUser,
    targetSshPort: sshPort,
    category: 'network',
    primaryModule: 'ansible.posix.firewalld',
    steps: [step],
    become: true,
    becomeMethod,
    status: 'staged',
    createdAt: Date.now(),
  };
}

/**
 * Compiles a network share configuration mutation into a QueuedAnsibleTask with secret masking.
 */
export function compileShareTask(
  node: NodeItem,
  shareName: string,
  path: string,
  users: string[],
  passwordVaultKey?: string
): QueuedAnsibleTask {
  const { hostRoute, sshUser, sshPort } = resolveNodeConnection(node);
  const becomeMethod = resolveBecomeMethod(node);

  const step: AnsibleTaskStep = {
    name: `Configure share ${shareName} at ${path}`,
    module: 'ansible.builtin.template',
    parameters: {
      src: 'smb.conf.j2',
      dest: `/etc/samba/smb.conf.d/${shareName}.conf`,
      owner: 'root',
      group: 'root',
      mode: '0644',
      share_path: path,
      valid_users: users.join(' '),
    },
  };

  const secretsMasked: Record<string, string> = {
    smb_password: passwordVaultKey ? `•••••••• (vault: ${passwordVaultKey})` : '••••••••',
  };

  return {
    id: `task-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
    title: `Configure share ${shareName}`,
    targetNodeId: node.id,
    targetNodeName: node.name || node.shortName || node.id,
    targetHostRoute: hostRoute,
    targetSshUser: sshUser,
    targetSshPort: sshPort,
    category: 'storage',
    primaryModule: 'ansible.builtin.template',
    steps: [step],
    secretsMasked,
    become: true,
    becomeMethod,
    status: 'staged',
    createdAt: Date.now(),
  };
}

/**
 * Compiles a container lifecycle mutation into a QueuedAnsibleTask using containers.podman.podman_container.
 */
export function compileContainerTask(
  node: NodeItem,
  containerName: string,
  action: 'start' | 'stop' | 'restart'
): QueuedAnsibleTask {
  const { hostRoute, sshUser, sshPort } = resolveNodeConnection(node);
  const becomeMethod = resolveBecomeMethod(node);

  const stateMap: Record<string, string> = {
    start: 'started',
    stop: 'stopped',
    restart: 'restarted',
  };
  const targetState = stateMap[action] || 'started';
  const actionTitle = action.charAt(0).toUpperCase() + action.slice(1);

  const step: AnsibleTaskStep = {
    name: `${actionTitle} container ${containerName}`,
    module: 'containers.podman.podman_container',
    parameters: {
      name: containerName,
      state: targetState,
    },
  };

  return {
    id: `task-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
    title: `${actionTitle} container ${containerName}`,
    targetNodeId: node.id,
    targetNodeName: node.name || node.shortName || node.id,
    targetHostRoute: hostRoute,
    targetSshUser: sshUser,
    targetSshPort: sshPort,
    category: 'container',
    primaryModule: 'containers.podman.podman_container',
    steps: [step],
    become: false,
    becomeMethod,
    status: 'staged',
    createdAt: Date.now(),
  };
}

/**
 * Compiles a package install/update/remove mutation into a QueuedAnsibleTask using ansible.builtin.package.
 */
export function compilePackageTask(
  node: NodeItem,
  packageName: string,
  state: 'present' | 'latest' | 'absent'
): QueuedAnsibleTask {
  const { hostRoute, sshUser, sshPort } = resolveNodeConnection(node);
  const becomeMethod = resolveBecomeMethod(node);

  const step: AnsibleTaskStep = {
    name: `Ensure package ${packageName} is ${state}`,
    module: 'ansible.builtin.package',
    parameters: {
      name: packageName,
      state,
    },
  };

  return {
    id: `task-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
    title: `Ensure package ${packageName} is ${state}`,
    targetNodeId: node.id,
    targetNodeName: node.name || node.shortName || node.id,
    targetHostRoute: hostRoute,
    targetSshUser: sshUser,
    targetSshPort: sshPort,
    category: 'package',
    primaryModule: 'ansible.builtin.package',
    steps: [step],
    become: true,
    becomeMethod,
    status: 'staged',
    createdAt: Date.now(),
  };
}

/**
 * Formats a value for YAML task parameter generation.
 */
function formatYamlParamValue(val: any, indentLevel: number): string {
  if (typeof val === 'boolean') {
    return val ? 'true' : 'false';
  }
  if (typeof val === 'number') {
    return String(val);
  }
  if (typeof val === 'string') {
    if (/[\n\r]/.test(val)) {
      const indent = ' '.repeat(indentLevel);
      const lines = val.split('\n').map(l => `${indent}  ${l}`).join('\n');
      return `|\n${lines}`;
    }
    if (/[:#{}[\],&*?|<>=!%@`]/.test(val) || val === '' || /^(true|false|yes|no|null)$/i.test(val)) {
      return JSON.stringify(val);
    }
    return val;
  }
  if (Array.isArray(val)) {
    if (val.length === 0) return '[]';
    const indent = ' '.repeat(indentLevel);
    return '\n' + val.map(item => `${indent}- ${formatYamlParamValue(item, indentLevel + 2)}`).join('\n');
  }
  if (typeof val === 'object' && val !== null) {
    const entries = Object.entries(val);
    if (entries.length === 0) return '{}';
    const indent = ' '.repeat(indentLevel);
    return '\n' + entries.map(([k, v]) => `${indent}${k}: ${formatYamlParamValue(v, indentLevel + 2)}`).join('\n');
  }
  return String(val ?? '');
}

/**
 * Compiles a QueuedAnsibleTask into an on-the-fly runnable Ansible Playbook in YAML format.
 */
export function compileOnTheFlyPlaybook(task: QueuedAnsibleTask): string {
  const lines: string[] = [
    '---',
    `- name: ${JSON.stringify(task.title)}`,
    '  hosts: all',
    '  gather_facts: false',
    '  vars:',
    `    ansible_host: ${JSON.stringify(task.targetHostRoute)}`,
    `    ansible_user: ${JSON.stringify(task.targetSshUser)}`,
    `    ansible_port: ${task.targetSshPort}`,
    `  become: ${task.become ? 'true' : 'false'}`,
    `  become_method: ${task.becomeMethod}`,
    '  tasks:',
  ];

  for (const step of task.steps) {
    lines.push(`    - name: ${JSON.stringify(step.name)}`);
    lines.push(`      ${step.module}:`);
    for (const [key, val] of Object.entries(step.parameters)) {
      lines.push(`        ${key}: ${formatYamlParamValue(val, 10)}`);
    }
  }

  return lines.join('\n') + '\n';
}
