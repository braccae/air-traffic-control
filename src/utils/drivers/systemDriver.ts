import type {
  NodeItem,
  QueuedAnsibleTask,
  AnsibleTaskStep,
  AnsibleModuleCategory,
} from '../../types/index.ts';
import { resolveBecomeMethod, resolveNodeConnection } from '../ansibleQueueCompiler.ts';
import type { MutationDriver } from './index.ts';

export class SystemDriver implements MutationDriver {
  readonly category: AnsibleModuleCategory = 'system';

  supports(node: NodeItem): boolean {
    // Linux/Unix hosts support system mutations
    return node.nodeType !== 'windows' && node.nodeType !== 'router_switch';
  }

  compileMutation(node: NodeItem, action: string, payload: any = {}): QueuedAnsibleTask {
    const { hostRoute, sshUser, sshPort } = resolveNodeConnection(node);
    const becomeMethod = resolveBecomeMethod(node);
    const taskId = `task-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;

    const normalizedAction = action.toLowerCase();

    // System reboot action
    if (normalizedAction === 'reboot') {
      const timeout = typeof payload.timeout === 'number' ? payload.timeout : 300;
      const msg = payload.message || payload.msg || `Reboot initiated by Cockpit for ${node.shortName || node.id}`;

      const step: AnsibleTaskStep = {
        name: `Reboot node ${node.shortName || node.id}`,
        module: 'ansible.builtin.reboot',
        parameters: {
          msg,
          reboot_timeout: timeout,
          ...(payload.connectTimeout ? { connect_timeout: payload.connectTimeout } : {}),
          ...(payload.preRebootDelay ? { pre_reboot_delay: payload.preRebootDelay } : {}),
          ...(payload.postRebootDelay ? { post_reboot_delay: payload.postRebootDelay } : {}),
        },
      };

      return {
        id: taskId,
        title: `Reboot node ${node.name || node.shortName || node.id}`,
        targetNodeId: node.id,
        targetNodeName: node.name || node.shortName || node.id,
        targetHostRoute: hostRoute,
        targetSshUser: sshUser,
        targetSshPort: sshPort,
        category: 'system',
        primaryModule: 'ansible.builtin.reboot',
        steps: [step],
        become: true,
        becomeMethod,
        status: 'staged',
        createdAt: Date.now(),
      };
    }

    // Service management actions (start, stop, restart, reload, enable, disable)
    const serviceName = payload.service || payload.serviceName || payload.name || 'system';

    // Map common action names to state
    const stateMap: Record<string, string> = {
      start: 'started',
      started: 'started',
      stop: 'stopped',
      stopped: 'stopped',
      restart: 'restarted',
      restarted: 'restarted',
      reload: 'reloaded',
      reloaded: 'reloaded',
    };
    const state = stateMap[normalizedAction] || 'restarted';
    const actionTitle = state.charAt(0).toUpperCase() + state.slice(1);

    // Autodetect systemd vs non-systemd (e.g. node.initSystem === 'openrc' or Alpine)
    const isNonSystemd =
      (node.initSystem && node.initSystem !== 'systemd') ||
      node.osDescription?.toLowerCase().includes('alpine') ||
      node.tags?.includes('alpine') ||
      node.tags?.includes('openrc');

    const primaryModule = isNonSystemd ? 'ansible.builtin.service' : 'ansible.builtin.systemd';

    const step: AnsibleTaskStep = {
      name: `${actionTitle} ${serviceName} service`,
      module: primaryModule,
      parameters: {
        name: serviceName,
        state,
        ...(payload.enabled !== undefined ? { enabled: Boolean(payload.enabled) } : {}),
      },
    };

    return {
      id: taskId,
      title: `${actionTitle} service ${serviceName}`,
      targetNodeId: node.id,
      targetNodeName: node.name || node.shortName || node.id,
      targetHostRoute: hostRoute,
      targetSshUser: sshUser,
      targetSshPort: sshPort,
      category: 'system',
      primaryModule,
      steps: [step],
      become: true,
      becomeMethod,
      status: 'staged',
      createdAt: Date.now(),
    };
  }
}

export const systemDriver = new SystemDriver();
