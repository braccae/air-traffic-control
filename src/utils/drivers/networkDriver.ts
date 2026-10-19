import type {
  NodeItem,
  QueuedAnsibleTask,
  AnsibleTaskStep,
  AnsibleModuleCategory,
} from '../../types/index.ts';
import { resolveBecomeMethod, resolveNodeConnection } from '../ansibleQueueCompiler.ts';
import type { MutationDriver } from './index.ts';

export class NetworkDriver implements MutationDriver {
  readonly category: AnsibleModuleCategory = 'network';

  supports(node: NodeItem): boolean {
    return true; // Supports Linux nodes and network hardware
  }

  compileMutation(node: NodeItem, action: string, payload: any = {}): QueuedAnsibleTask {
    const { hostRoute, sshUser, sshPort } = resolveNodeConnection(node);
    const becomeMethod = resolveBecomeMethod(node);
    const taskId = `task-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
    const normalizedAction = action.toLowerCase();

    // Switch / Router device targeting
    if (node.nodeType === 'router_switch') {
      const module = payload.module || 'cisco.ios.ios_config';
      const lines = Array.isArray(payload.lines)
        ? payload.lines
        : [
            payload.name ? `interface ${payload.name}` : null,
            payload.description ? ` description ${payload.description}` : null,
            payload.vlanId ? ` switchport access vlan ${payload.vlanId}` : null,
          ].filter(Boolean);

      const step: AnsibleTaskStep = {
        name: `Configure switch interface ${payload.name || node.shortName}`,
        module,
        parameters: {
          lines,
          ...(payload.parents ? { parents: payload.parents } : {}),
        },
      };

      return {
        id: taskId,
        title: `Configure switch ${node.shortName} interface ${payload.name || ''}`.trim(),
        targetNodeId: node.id,
        targetNodeName: node.name || node.shortName || node.id,
        targetHostRoute: hostRoute,
        targetSshUser: sshUser,
        targetSshPort: sshPort,
        category: 'network',
        primaryModule: module,
        steps: [step],
        become: false,
        becomeMethod: 'sudo',
        status: 'staged',
        createdAt: Date.now(),
      };
    }

    // Network Interface configuration on Linux host
    if (normalizedAction === 'connect_interface' || normalizedAction === 'interface') {
      const ifaceName = payload.name || payload.interface || 'eth0';
      const ifaceType = payload.type || 'wireguard';

      const step: AnsibleTaskStep = {
        name: `Configure network interface ${ifaceName} (${ifaceType})`,
        module: 'ansible.builtin.template',
        parameters: {
          interface: ifaceName,
          type: ifaceType,
          ...(payload.cidr ? { cidr: payload.cidr } : {}),
          ...(payload.gateway ? { gateway: payload.gateway } : {}),
          ...(payload.dns ? { dns: payload.dns } : {}),
          ...(payload.parent ? { parent: payload.parent } : {}),
          ...(payload.vlanId ? { vlanId: payload.vlanId } : {}),
        },
      };

      return {
        id: taskId,
        title: `Configure network interface ${ifaceName}`,
        targetNodeId: node.id,
        targetNodeName: node.name || node.shortName || node.id,
        targetHostRoute: hostRoute,
        targetSshUser: sshUser,
        targetSshPort: sshPort,
        category: 'network',
        primaryModule: 'ansible.builtin.template',
        steps: [step],
        become: true,
        becomeMethod,
        status: 'staged',
        createdAt: Date.now(),
      };
    }

    // Firewall rules (default)
    const port = payload.port || '80';
    const proto = payload.proto || 'tcp';
    const portProto = port.includes('/') ? port : `${port}/${proto}`;
    const zone = payload.zone;

    const parameters: Record<string, any> = {
      port: portProto,
      permanent: true,
      state: payload.state || 'enabled',
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
      id: taskId,
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
}

export const networkDriver = new NetworkDriver();
