import type { NodeItem, QueuedAnsibleTask, AnsibleModuleCategory } from '../../types/index.ts';
import { SystemDriver, systemDriver } from './systemDriver.ts';
import { StorageDriver, storageDriver } from './storageDriver.ts';
import { NetworkDriver, networkDriver } from './networkDriver.ts';

export interface MutationDriver {
  category: AnsibleModuleCategory;
  supports(node: NodeItem): boolean;
  compileMutation(node: NodeItem, action: string, payload: any): QueuedAnsibleTask;
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

// Initialize standard built-in drivers
registerDriver(systemDriver);
registerDriver(storageDriver);
registerDriver(networkDriver);

/**
 * Stages an Ansible mutation task in the server queue and triggers a notification toast.
 */
export async function stageTaskInQueue(
  task: QueuedAnsibleTask
): Promise<{ success: boolean; task?: QueuedAnsibleTask; error?: string }> {
  try {
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
      window.dispatchEvent(
        new CustomEvent('prox_orch_toast', {
          detail: {
            type: 'success',
            title: 'Staged in Task Queue (1 pending)',
            message: `${task.title} on ${task.targetNodeName}`,
            actionText: 'View in Console →',
            onAction: () => {
              const el = document.getElementById('global-tasks-drawer');
              el?.scrollIntoView({ behavior: 'smooth' });
            },
          },
        })
      );
    }

    return { success: true, task: data.task };
  } catch (err: any) {
    if (typeof window !== 'undefined') {
      window.dispatchEvent(
        new CustomEvent('prox_orch_toast', {
          detail: {
            type: 'error',
            title: 'Failed to stage in Queue',
            message: err?.message || 'Network error',
          },
        })
      );
    }
    return { success: false, error: err?.message || 'Failed to stage task' };
  }
}

export { SystemDriver, systemDriver };
export { StorageDriver, storageDriver };
export { NetworkDriver, networkDriver };
