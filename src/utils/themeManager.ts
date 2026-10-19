export type ThemeMode = 'dark' | 'latte';

export const THEME_STORAGE_KEY = 'prox_orch_theme';

export interface StorageLike {
  getItem?(key: string): string | null;
  setItem?(key: string, value: string): void;
}

export interface ElementLike {
  setAttribute(name: string, value: string): void;
  classList?: {
    add(token: string): void;
    remove(token: string): void;
  };
}

/**
 * Retrieves the persisted theme from localStorage or returns 'dark' by default.
 */
export function getStoredTheme(storage?: StorageLike): ThemeMode {
  try {
    const s = storage || (typeof window !== 'undefined' ? window.localStorage : undefined);
    if (!s || !s.getItem) return 'dark';
    const val = s.getItem(THEME_STORAGE_KEY);
    return val === 'latte' ? 'latte' : 'dark';
  } catch {
    return 'dark';
  }
}

/**
 * Persists the selected theme into localStorage.
 */
export function setStoredTheme(theme: ThemeMode, storage?: StorageLike): void {
  try {
    const s = storage || (typeof window !== 'undefined' ? window.localStorage : undefined);
    if (s && s.setItem) {
      s.setItem(THEME_STORAGE_KEY, theme);
    }
  } catch {
    // ignore local storage errors in sandboxed environments
  }
}

/**
 * Toggles between 'dark' and 'latte' theme modes.
 */
export function toggleTheme(current: ThemeMode): ThemeMode {
  return current === 'latte' ? 'dark' : 'latte';
}

/**
 * Applies the theme data attribute and class to the document or target root element.
 */
export function applyThemeToDocument(
  theme: ThemeMode,
  targetElement?: ElementLike
): void {
  const el = targetElement || (typeof document !== 'undefined' ? document.documentElement : undefined);
  if (!el) return;

  el.setAttribute('data-theme', theme);

  if (el.classList) {
    if (theme === 'latte') {
      el.classList.add('theme-latte');
    } else {
      el.classList.remove('theme-latte');
    }
  }
}

/**
 * Returns the human-readable label for the theme.
 */
export function getThemeLabel(theme: ThemeMode): string {
  return theme === 'latte' ? 'Theme: Latte' : 'Theme: Dark';
}

/**
 * Returns the Material Symbol icon name for the theme.
 */
export function getThemeIcon(theme: ThemeMode): string {
  return theme === 'latte' ? 'light_mode' : 'dark_mode';
}
