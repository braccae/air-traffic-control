import type { NodeItem, QueuedAnsibleTask, AnsibleModuleCategory } from '../../types/index.ts';

export interface MutationDriver {
  category: AnsibleModuleCategory;
  supports(node: NodeItem): boolean;
  compileMutation(node: NodeItem, action: string, payload: any): QueuedAnsibleTask | Promise<QueuedAnsibleTask>;
}

const driverRegistry: MutationDriver[] = [];

/**
 * Registers a mutation driver in the global registry.
 * Newer drivers take precedence over default drivers.
 */
export function registerDriver(driver: MutationDriver): void {
  driverRegistry.unshift(driver);
}

/**
 * Resolves the appropriate MutationDriver for a given category and target node.
 */
export function getDriver(
  category: AnsibleModuleCategory,
  node: NodeItem
): MutationDriver | undefined {
  return driverRegistry.find(d => d.category === category && d.supports(node));
}

/**
 * Returns all currently registered drivers.
 */
export function getAllDrivers(): MutationDriver[] {
  return [...driverRegistry];
}

/**
 * Validates Ansible Galaxy collection namespace and name to prevent command injection.
 * Format must strictly be <namespace>.<collection> (e.g. 'ansible.posix', 'community.general').
 */
export function isValidCollectionName(collection: string): boolean {
  return /^[a-zA-Z0-9_]+\.[a-zA-Z0-9_]+$/.test(collection);
}

/**
 * Normalizes a TaskSpec or QueuedAnsibleTask returned by the Go daemon into a valid QueuedAnsibleTask.
 */
function normalizeCompiledTask(node: NodeItem, kind: string, taskSpec: any): QueuedAnsibleTask {
  const steps = Array.isArray(taskSpec?.steps)
    ? taskSpec.steps.map((s: any) => ({
        name: s.name || 'Task step',
        module: s.module || 'ansible.builtin.command',
        parameters: s.params || s.parameters || {},
      }))
    : [];

  const category = (taskSpec?.category || kind || 'system') as AnsibleModuleCategory;
  const primaryModule = taskSpec?.primaryModule || steps[0]?.module || 'ansible.builtin.command';

  return {
    id: taskSpec?.id || `task-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
    title: taskSpec?.title || taskSpec?.name || `Apply ${kind} mutation on ${node.shortName || node.name}`,
    targetNodeId: node.id,
    targetNodeName: node.shortName || node.name || node.id,
    targetHostRoute: node.tailscaleHost || node.tailscaleIp || node.ip || '127.0.0.1',
    targetSshUser: node.sshUser || 'core',
    targetSshPort: node.port || 22,
    connection: taskSpec?.connection || {
      plugin: node.tailscaleHost || node.tailscaleIp ? 'connection/tailscale' : 'connection/ssh',
      host: node.tailscaleHost || node.tailscaleIp || node.ip || '127.0.0.1',
      user: node.sshUser || 'core',
      port: node.port || 22,
      becomeMethod: node.privilegeMethod || 'run0',
    },
    category,
    primaryModule,
    steps,
    secretsMasked: taskSpec?.secretsMasked,
    become: taskSpec?.become !== undefined ? Boolean(taskSpec.become) : true,
    becomeMethod: taskSpec?.becomeMethod || node.privilegeMethod || 'run0',
    status: 'staged',
    createdAt: Date.now(),
  };
}

/**
 * Compiles a host mutation task by delegating to the Go daemon via POST /api/nodes/:id/plugins/:kind/:name/actions/:action.
 * In accordance with the Zero-Mock Policy, fails loudly when daemon is unreachable or returns an error.
 */
export async function compileDaemonPluginMutation(
  node: NodeItem,
  kind: string,
  pluginName: string,
  action: string,
  options: Record<string, any> = {}
): Promise<QueuedAnsibleTask> {
  const endpoint = `/api/nodes/${encodeURIComponent(node.id)}/plugins/${encodeURIComponent(kind)}/${encodeURIComponent(pluginName)}/actions/${encodeURIComponent(action)}`;
  
  const res = await fetch(endpoint, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ options }),
  });

  const body = await res.json().catch(() => ({}));
  if (!res.ok) {
    const detail = body?.error || `status ${res.status}`;
    throw new Error(`Daemon failed to compile action ${action} for ${kind}/${pluginName}: ${detail}`);
  }

  return normalizeCompiledTask(node, kind, body);
}

/**
 * Default proxy driver delegating system actions to Go daemon (system/service).
 */
export class DaemonSystemDriver implements MutationDriver {
  readonly category: AnsibleModuleCategory = 'system';

  supports(_node: NodeItem): boolean {
    return true;
  }

  compileMutation(node: NodeItem, action: string, payload: any = {}): Promise<QueuedAnsibleTask> {
    const act = action.toLowerCase();
    return compileDaemonPluginMutation(node, 'system', 'service', act, payload);
  }
}

/**
 * Default proxy driver delegating storage actions to Go daemon (share/smb, share/nfs, storage/zfs).
 */
export class DaemonStorageDriver implements MutationDriver {
  readonly category: AnsibleModuleCategory = 'storage';

  supports(_node: NodeItem): boolean {
    return true;
  }

  compileMutation(node: NodeItem, action: string, payload: any = {}): Promise<QueuedAnsibleTask> {
    const act = action.toLowerCase();
    if (act === 'nfs' || payload?.protocol === 'nfs') {
      return compileDaemonPluginMutation(node, 'share', 'nfs', 'export', payload);
    }
    if (act === 'scrub') {
      return compileDaemonPluginMutation(node, 'storage', 'zfs', 'scrub', payload);
    }
    return compileDaemonPluginMutation(node, 'share', 'smb', 'create', payload);
  }
}

/**
 * Default proxy driver delegating network actions to Go daemon (network/interfaces, firewall/firewalld).
 */
export class DaemonNetworkDriver implements MutationDriver {
  readonly category: AnsibleModuleCategory = 'network';

  supports(_node: NodeItem): boolean {
    return true;
  }

  compileMutation(node: NodeItem, action: string, payload: any = {}): Promise<QueuedAnsibleTask> {
    const act = action.toLowerCase();
    if (act === 'connect_interface' || act === 'interface') {
      return compileDaemonPluginMutation(node, 'network', 'interfaces', 'configure', payload);
    }
    return compileDaemonPluginMutation(node, 'firewall', 'firewalld', 'add_rule', payload);
  }
}

export const systemDriver = new DaemonSystemDriver();
export const storageDriver = new DaemonStorageDriver();
export const networkDriver = new DaemonNetworkDriver();

// Backwards-compatible aliases for class names
export {
  DaemonSystemDriver as SystemDriver,
  DaemonStorageDriver as StorageDriver,
  DaemonNetworkDriver as NetworkDriver,
};

// Register default daemon proxy drivers
registerDriver(systemDriver);
registerDriver(storageDriver);
registerDriver(networkDriver);

/**
 * Stages an Ansible mutation task in the server queue and triggers a notification toast.
 */
export async function stageTaskInQueue(
  taskOrPromise: QueuedAnsibleTask | Promise<QueuedAnsibleTask>
): Promise<{ success: boolean; task?: QueuedAnsibleTask; error?: string }> {
  try {
    const task = await Promise.resolve(taskOrPromise);
    const res = await fetch('/api/queue/task', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(task),
    });
    const data = await res.json();
    if (!res.ok || !data.success) {
      throw new Error(data.error || 'Failed to stage task in queue');
    }

    if (typeof window !== 'undefined') {
      const toastDetail = {
        type: 'success' as const,
        title: 'Staged in Task Queue (1 pending)',
        message: `${task.title} on ${task.targetNodeName}`,
        actionText: 'View in Console →',
        onAction: () => {
          const el = document.getElementById('global-tasks-drawer');
          el?.scrollIntoView({ behavior: 'smooth' });
        },
      };
      window.dispatchEvent(new CustomEvent('air_traffic_control_toast', { detail: toastDetail }));
      window.dispatchEvent(new CustomEvent('prox_orch_toast', { detail: toastDetail }));
    }

    return { success: true, task: data.task };
  } catch (err: any) {
    if (typeof window !== 'undefined') {
      const errDetail = {
        type: 'error' as const,
        title: 'Failed to stage in Queue',
        message: err?.message || 'Network error',
      };
      window.dispatchEvent(new CustomEvent('air_traffic_control_toast', { detail: errDetail }));
      window.dispatchEvent(new CustomEvent('prox_orch_toast', { detail: errDetail }));
    }
    return { success: false, error: err?.message || 'Failed to stage task' };
  }
}
