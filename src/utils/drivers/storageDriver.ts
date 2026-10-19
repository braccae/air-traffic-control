import type {
  NodeItem,
  QueuedAnsibleTask,
  AnsibleTaskStep,
  AnsibleModuleCategory,
} from '../../types/index.ts';
import { resolveBecomeMethod, resolveNodeConnection } from '../ansibleQueueCompiler.ts';
import type { MutationDriver } from './index.ts';

export class StorageDriver implements MutationDriver {
  readonly category: AnsibleModuleCategory = 'storage';

  supports(node: NodeItem): boolean {
    // Router/switches don't support standard Linux storage mutations
    return node.nodeType !== 'router_switch';
  }

  compileMutation(node: NodeItem, action: string, payload: any = {}): QueuedAnsibleTask {
    const { hostRoute, sshUser, sshPort } = resolveNodeConnection(node);
    const becomeMethod = resolveBecomeMethod(node);
    const taskId = `task-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
    const normalizedAction = action.toLowerCase();

    // NFS export action
    if (normalizedAction === 'nfs' || payload.protocol === 'nfs') {
      const path = payload.path || '/export';
      const client = payload.client || '*';
      const options = payload.options || 'rw,sync,no_subtree_check';
      const exportLine = `${path} ${client}(${options})`;
      const shareName = payload.name || path.replace(/\//g, '_').replace(/^_/, '');

      const steps: AnsibleTaskStep[] = [
        {
          name: `Configure NFS export for ${path}`,
          module: 'ansible.builtin.lineinfile',
          parameters: {
            path: '/etc/exports',
            line: exportLine,
            state: payload.state === 'absent' ? 'absent' : 'present',
            create: true,
            mode: '0644',
          },
        },
        {
          name: 'Reload NFS exports table',
          module: 'ansible.builtin.command',
          parameters: {
            cmd: 'exportfs -ra',
          },
        },
      ];

      return {
        id: taskId,
        title: `Configure NFS export ${shareName} at ${path}`,
        targetNodeId: node.id,
        targetNodeName: node.name || node.shortName || node.id,
        targetHostRoute: hostRoute,
        targetSshUser: sshUser,
        targetSshPort: sshPort,
        category: 'storage',
        primaryModule: 'ansible.builtin.lineinfile',
        steps,
        become: true,
        becomeMethod,
        status: 'staged',
        createdAt: Date.now(),
      };
    }

    // SMB / Samba share action (default)
    const shareName = payload.name || payload.shareName || 'share';
    const sharePath = payload.path || payload.sharePath || '/srv/share';
    const users = Array.isArray(payload.users)
      ? payload.users
      : typeof payload.validUsers === 'string'
      ? payload.validUsers.split(/\s+/).filter(Boolean)
      : ['core'];

    const step: AnsibleTaskStep = {
      name: `Configure share ${shareName} at ${sharePath}`,
      module: 'ansible.builtin.template',
      parameters: {
        src: payload.templateSrc || 'smb.conf.j2',
        dest: `/etc/samba/smb.conf.d/${shareName}.conf`,
        owner: 'root',
        group: 'root',
        mode: '0644',
        share_path: sharePath,
        valid_users: users.join(' '),
        ...(payload.readOnly !== undefined ? { read_only: payload.readOnly ? 'yes' : 'no' } : {}),
        ...(payload.guestOk !== undefined ? { guest_ok: payload.guestOk ? 'yes' : 'no' } : {}),
        ...(payload.comment ? { comment: payload.comment } : {}),
      },
    };

    const secretsMasked: Record<string, string> = {};
    if (payload.passwordVaultKey) {
      secretsMasked.smb_password = `•••••••• (vault: ${payload.passwordVaultKey})`;
    } else if (payload.password) {
      secretsMasked.smb_password = '••••••••';
    }

    return {
      id: taskId,
      title: `Configure share ${shareName}`,
      targetNodeId: node.id,
      targetNodeName: node.name || node.shortName || node.id,
      targetHostRoute: hostRoute,
      targetSshUser: sshUser,
      targetSshPort: sshPort,
      category: 'storage',
      primaryModule: 'ansible.builtin.template',
      steps: [step],
      ...(Object.keys(secretsMasked).length > 0 ? { secretsMasked } : {}),
      become: true,
      becomeMethod,
      status: 'staged',
      createdAt: Date.now(),
    };
  }
}

export const storageDriver = new StorageDriver();
