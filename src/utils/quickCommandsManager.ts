export type QuickCommandCategory =
  | 'system'
  | 'containers'
  | 'storage'
  | 'network'
  | 'custom';

export interface QuickCommandItem {
  id: string;
  label: string;
  cmd: string;
  description?: string;
  category?: QuickCommandCategory;
}

export const QUICK_COMMAND_STORAGE_KEY = 'prox_orch_quick_commands';
export const QUICK_COMMAND_EVENT = 'quick_commands_updated';

export const DEFAULT_QUICK_COMMANDS: QuickCommandItem[] = [
  {
    id: 'qc-htop',
    label: 'htop',
    cmd: 'htop',
    description: 'Interactive process viewer',
    category: 'system',
  },
  {
    id: 'qc-podman-ps',
    label: 'podman ps',
    cmd: 'podman ps',
    description: 'List active containers',
    category: 'containers',
  },
  {
    id: 'qc-journalctl',
    label: 'journalctl',
    cmd: 'journalctl -f -n 50',
    description: 'Follow recent system logs',
    category: 'system',
  },
  {
    id: 'qc-ip-a',
    label: 'ip a',
    cmd: 'ip -br a',
    description: 'Brief network interfaces and IPs',
    category: 'network',
  },
  {
    id: 'qc-uptime',
    label: 'uptime',
    cmd: 'uptime',
    description: 'System uptime and load averages',
    category: 'system',
  },
  {
    id: 'qc-df-h',
    label: 'df -h',
    cmd: 'df -h',
    description: 'Filesystem disk space usage',
    category: 'storage',
  },
];

export interface StorageLike {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

export interface EventTargetLike {
  dispatchEvent(event: Event): boolean;
}

function getSafeStorage(storage?: StorageLike): StorageLike | null {
  if (storage) return storage;
  try {
    if (typeof localStorage !== 'undefined') {
      return localStorage;
    }
  } catch {
    // localStorage may throw SecurityError in restricted iframes
  }
  return null;
}

function getSafeEventTarget(eventTarget?: EventTargetLike): EventTargetLike | null {
  if (eventTarget) return eventTarget;
  try {
    if (typeof window !== 'undefined') {
      return window;
    }
  } catch {
    // window not defined
  }
  return null;
}

export function loadQuickCommands(storage?: StorageLike): QuickCommandItem[] {
  const store = getSafeStorage(storage);
  if (!store) {
    return DEFAULT_QUICK_COMMANDS.map((cmd) => ({ ...cmd }));
  }

  try {
    const raw = store.getItem(QUICK_COMMAND_STORAGE_KEY);
    if (!raw) {
      return DEFAULT_QUICK_COMMANDS.map((cmd) => ({ ...cmd }));
    }

    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed) || parsed.length === 0) {
      return DEFAULT_QUICK_COMMANDS.map((cmd) => ({ ...cmd }));
    }

    const validCommands: QuickCommandItem[] = [];
    for (let i = 0; i < parsed.length; i++) {
      const item = parsed[i];
      if (
        item &&
        typeof item === 'object' &&
        typeof item.label === 'string' &&
        item.label.trim().length > 0 &&
        typeof item.cmd === 'string' &&
        item.cmd.trim().length > 0
      ) {
        validCommands.push({
          id:
            typeof item.id === 'string' && item.id.trim().length > 0
              ? item.id.trim()
              : `qc-${Date.now()}-${i}`,
          label: item.label.trim(),
          cmd: item.cmd.trim(),
          description:
            typeof item.description === 'string' && item.description.trim().length > 0
              ? item.description.trim()
              : undefined,
          category: item.category || 'custom',
        });
      }
    }

    if (validCommands.length === 0) {
      return DEFAULT_QUICK_COMMANDS.map((cmd) => ({ ...cmd }));
    }

    return validCommands;
  } catch {
    return DEFAULT_QUICK_COMMANDS.map((cmd) => ({ ...cmd }));
  }
}

export function saveQuickCommands(
  commands: QuickCommandItem[],
  storage?: StorageLike,
  eventTarget?: EventTargetLike
): void {
  const store = getSafeStorage(storage);
  if (store) {
    try {
      store.setItem(QUICK_COMMAND_STORAGE_KEY, JSON.stringify(commands));
    } catch (err) {
      console.warn('Failed to save quick commands to storage:', err);
    }
  }

  const target = getSafeEventTarget(eventTarget);
  if (target && typeof CustomEvent !== 'undefined') {
    try {
      const event = new CustomEvent(QUICK_COMMAND_EVENT, { detail: commands });
      target.dispatchEvent(event);
    } catch {
      // Ignore event dispatch failure in non-browser or mock environments
    }
  }
}

export function addQuickCommand(
  cmd: Omit<QuickCommandItem, 'id'>,
  storage?: StorageLike,
  eventTarget?: EventTargetLike
): QuickCommandItem[] {
  const current = loadQuickCommands(storage);
  const newId = typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
    ? `qc-${crypto.randomUUID().slice(0, 8)}`
    : `qc-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
  const newItem: QuickCommandItem = {
    id: newId,
    label: cmd.label.trim(),
    cmd: cmd.cmd.trim(),
    description: cmd.description?.trim(),
    category: cmd.category || 'custom',
  };

  const updated = [...current, newItem];
  saveQuickCommands(updated, storage, eventTarget);
  return updated;
}

export function updateQuickCommand(
  id: string,
  updates: Partial<QuickCommandItem>,
  storage?: StorageLike,
  eventTarget?: EventTargetLike
): QuickCommandItem[] {
  const current = loadQuickCommands(storage);
  const updated = current.map((item) => {
    if (item.id !== id) return item;
    return {
      ...item,
      ...updates,
      id, // Preserve original ID invariant
      label: updates.label !== undefined ? updates.label.trim() : item.label,
      cmd: updates.cmd !== undefined ? updates.cmd.trim() : item.cmd,
      description:
        updates.description !== undefined
          ? updates.description.trim() || undefined
          : item.description,
      category: updates.category !== undefined ? updates.category : item.category,
    };
  });

  saveQuickCommands(updated, storage, eventTarget);
  return updated;
}

export function deleteQuickCommand(
  id: string,
  storage?: StorageLike,
  eventTarget?: EventTargetLike
): QuickCommandItem[] {
  const current = loadQuickCommands(storage);
  const updated = current.filter((item) => item.id !== id);
  saveQuickCommands(updated, storage, eventTarget);
  return updated;
}

export function resetQuickCommands(
  storage?: StorageLike,
  eventTarget?: EventTargetLike
): QuickCommandItem[] {
  const defaults = DEFAULT_QUICK_COMMANDS.map((cmd) => ({ ...cmd }));
  saveQuickCommands(defaults, storage, eventTarget);
  return defaults;
}
