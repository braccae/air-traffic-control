export type WindowType = 'terminal' | 'modal';

export interface ManagedWindow<T = Record<string, any>> {
  id: string;
  title: string;
  icon?: string;
  type: WindowType;
  isMinimized: boolean;
  isOpen: boolean;
  meta?: T;
  createdAt?: number;
}

export type WindowItem<T = Record<string, any>> = ManagedWindow<T>;

/**
 * Registers or updates a managed window in the registry.
 */
export function registerWindow<T = Record<string, any>>(
  windows: ManagedWindow<T>[],
  window: Partial<ManagedWindow<T>> & { id: string; title: string; type: WindowType }
): ManagedWindow<T>[] {
  const existingIndex = windows.findIndex((w) => w.id === window.id);
  const now = Date.now();

  if (existingIndex >= 0) {
    const updated = [...windows];
    const prev = updated[existingIndex];
    updated[existingIndex] = {
      ...prev,
      ...window,
      isOpen: window.isOpen !== undefined ? window.isOpen : true,
      isMinimized: window.isMinimized !== undefined ? window.isMinimized : false,
      meta: window.meta !== undefined ? { ...prev.meta, ...window.meta } : prev.meta,
    };
    return updated;
  }

  const defaultIcon = window.icon || (window.type === 'terminal' ? 'terminal' : 'widgets');

  return [
    ...windows,
    {
      icon: defaultIcon,
      isMinimized: false,
      isOpen: true,
      createdAt: now,
      ...window,
    },
  ];
}

/**
 * Minimizes a window by ID.
 */
export function minimizeWindow<T = Record<string, any>>(
  windows: ManagedWindow<T>[],
  id: string
): ManagedWindow<T>[] {
  return windows.map((w) => (w.id === id ? { ...w, isMinimized: true } : w));
}

/**
 * Restores a minimized window to open/visible state.
 */
export function restoreWindow<T = Record<string, any>>(
  windows: ManagedWindow<T>[],
  id: string
): ManagedWindow<T>[] {
  return windows.map((w) => (w.id === id ? { ...w, isMinimized: false, isOpen: true } : w));
}

/**
 * Toggles the minimized state of a window.
 */
export function toggleMinimizeWindow<T = Record<string, any>>(
  windows: ManagedWindow<T>[],
  id: string
): ManagedWindow<T>[] {
  return windows.map((w) =>
    w.id === id
      ? {
          ...w,
          isMinimized: !w.isMinimized,
          isOpen: !w.isMinimized ? true : w.isOpen,
        }
      : w
  );
}

/**
 * Closes a window and removes it from the managed list.
 */
export function closeWindow<T = Record<string, any>>(
  windows: ManagedWindow<T>[],
  id: string
): ManagedWindow<T>[] {
  return windows.filter((w) => w.id !== id);
}

/**
 * Updates metadata for a window without modifying other properties.
 */
export function updateWindowMeta<T = Record<string, any>>(
  windows: ManagedWindow<T>[],
  id: string,
  metaUpdates: Partial<T>
): ManagedWindow<T>[] {
  return windows.map((w) =>
    w.id === id
      ? {
          ...w,
          meta: { ...(w.meta as T), ...metaUpdates },
        }
      : w
  );
}

/**
 * Returns all windows that are currently marked as minimized.
 */
export function getMinimizedWindows<T = Record<string, any>>(
  windows: ManagedWindow<T>[]
): ManagedWindow<T>[] {
  return windows.filter((w) => w.isOpen && w.isMinimized);
}

/**
 * Returns all open windows.
 */
export function getOpenWindows<T = Record<string, any>>(
  windows: ManagedWindow<T>[]
): ManagedWindow<T>[] {
  return windows.filter((w) => w.isOpen);
}

/**
 * Object-oriented WindowManager supporting subscriber notifications.
 */
export class WindowManager<T = Record<string, any>> {
  private windows: ManagedWindow<T>[] = [];
  private listeners: Array<(windows: ManagedWindow<T>[]) => void> = [];

  constructor(initialWindows: ManagedWindow<T>[] = []) {
    this.windows = initialWindows;
  }

  getWindows(): ManagedWindow<T>[] {
    return this.windows;
  }

  getMinimized(): ManagedWindow<T>[] {
    return getMinimizedWindows(this.windows);
  }

  getOpen(): ManagedWindow<T>[] {
    return getOpenWindows(this.windows);
  }

  register(
    window: Partial<ManagedWindow<T>> & { id: string; title: string; type: WindowType }
  ): ManagedWindow<T>[] {
    this.windows = registerWindow(this.windows, window);
    this.notify();
    return this.windows;
  }

  minimize(id: string): ManagedWindow<T>[] {
    this.windows = minimizeWindow(this.windows, id);
    this.notify();
    return this.windows;
  }

  restore(id: string): ManagedWindow<T>[] {
    this.windows = restoreWindow(this.windows, id);
    this.notify();
    return this.windows;
  }

  toggleMinimize(id: string): ManagedWindow<T>[] {
    this.windows = toggleMinimizeWindow(this.windows, id);
    this.notify();
    return this.windows;
  }

  close(id: string): ManagedWindow<T>[] {
    this.windows = closeWindow(this.windows, id);
    this.notify();
    return this.windows;
  }

  updateMeta(id: string, metaUpdates: Partial<T>): ManagedWindow<T>[] {
    this.windows = updateWindowMeta(this.windows, id, metaUpdates);
    this.notify();
    return this.windows;
  }

  subscribe(listener: (windows: ManagedWindow<T>[]) => void): () => void {
    this.listeners.push(listener);
    return () => {
      this.listeners = this.listeners.filter((l) => l !== listener);
    };
  }

  private notify(): void {
    for (const listener of this.listeners) {
      listener(this.windows);
    }
  }
}
