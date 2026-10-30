export type ThemeChoice = 'system' | 'macchiato' | 'latte';
export type ResolvedTheme = 'macchiato' | 'latte';
export type ThemeMode = ThemeChoice;

export type AccentColor = 'blue' | 'lavender' | 'teal' | 'green' | 'peach' | 'mauve';

export const THEME_STORAGE_KEY = 'air_traffic_control_theme';
export const ACCENT_STORAGE_KEY = 'air_traffic_control_accent_color';

export interface AccentPreset {
  id: AccentColor;
  label: string;
  hexDark: string;
  hexLight: string;
}

export const ACCENT_PRESETS: AccentPreset[] = [
  { id: 'blue', label: 'Sapphire Blue', hexDark: '#8aadf4', hexLight: '#1e66f5' },
  { id: 'lavender', label: 'Lavender', hexDark: '#b7bdf8', hexLight: '#7287fd' },
  { id: 'teal', label: 'Teal Green', hexDark: '#8cd6cb', hexLight: '#179299' },
  { id: 'green', label: 'Emerald Green', hexDark: '#a6da95', hexLight: '#40a02b' },
  { id: 'peach', label: 'Peach Orange', hexDark: '#f5a97f', hexLight: '#fe640b' },
  { id: 'mauve', label: 'Mauve Purple', hexDark: '#c6a0f6', hexLight: '#8839ef' },
];

export interface StorageLike {
  getItem?(key: string): string | null;
  setItem?(key: string, value: string): void;
}

export interface ElementLike {
  setAttribute(name: string, value: string): void;
  classList?: {
    add(...tokens: string[]): void;
    remove(...tokens: string[]): void;
  };
}

/**
 * Checks system color scheme preference. Defaults to true (dark) if matchMedia is unavailable.
 */
export function getSystemTheme(): boolean {
  if (typeof window !== 'undefined' && window.matchMedia) {
    try {
      return window.matchMedia('(prefers-color-scheme: dark)').matches;
    } catch {
      return true;
    }
  }
  return true;
}

/**
 * Resolves a ThemeChoice into a concrete ResolvedTheme ('macchiato' | 'latte').
 * Handles backward compatibility for legacy 'dark' stored values.
 */
export function resolveTheme(
  choice: ThemeChoice | string | null | undefined,
  systemIsDark: boolean = true
): ResolvedTheme {
  if (choice === 'latte') return 'latte';
  if (choice === 'macchiato' || choice === 'dark') return 'macchiato';
  return systemIsDark ? 'macchiato' : 'latte';
}

/**
 * Retrieves the persisted theme choice from localStorage or returns 'system' by default.
 */
export function getStoredTheme(storage?: StorageLike): ThemeChoice {
  try {
    const s = storage || (typeof window !== 'undefined' ? window.localStorage : undefined);
    if (!s || !s.getItem) return 'system';
    const val = s.getItem(THEME_STORAGE_KEY) || s.getItem('prox_orch_theme');
    if (val === 'latte') return 'latte';
    if (val === 'macchiato' || val === 'dark') return 'macchiato';
    if (val === 'system') return 'system';
    return 'system';
  } catch {
    return 'system';
  }
}

/**
 * Persists the selected theme choice into localStorage.
 */
export function setStoredTheme(theme: ThemeChoice, storage?: StorageLike): void {
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
 * Cycles between theme choices: system -> macchiato -> latte -> system.
 */
export function toggleTheme(current: ThemeChoice | 'dark'): ThemeChoice {
  if (current === 'system') return 'macchiato';
  if (current === 'macchiato') return 'latte';
  if (current === 'latte') return 'system';
  if (current === 'dark') return 'latte';
  return 'system';
}

/**
 * Applies the resolved theme data attribute and class to the document or target root element.
 * Returns the resolved concrete theme.
 */
export function applyThemeToDocument(
  theme: ThemeChoice | ResolvedTheme,
  targetElement?: ElementLike,
  systemIsDark?: boolean
): ResolvedTheme {
  const el = targetElement || (typeof document !== 'undefined' ? document.documentElement : undefined);
  const isDark = typeof systemIsDark === 'boolean' ? systemIsDark : getSystemTheme();
  const resolved = resolveTheme(theme, isDark);

  if (!el) return resolved;

  el.setAttribute('data-theme', resolved);
  el.setAttribute('data-theme-choice', theme);

  if (el.classList) {
    // `theme-*` drives legacy app selectors; the bare flavour classes are the
    // scoping hooks published by `@catppuccin/tailwindcss`, which is what
    // resolves every `--catppuccin-color-*` token for the active flavour.
    if (resolved === 'latte') {
      el.classList.add('theme-latte', 'latte');
      el.classList.remove('theme-macchiato', 'macchiato');
    } else {
      el.classList.add('theme-macchiato', 'macchiato');
      el.classList.remove('theme-latte', 'latte');
    }
  }

  return resolved;
}

/**
 * Returns the human-readable label for the theme.
 */
export function getThemeLabel(theme: ThemeChoice | 'dark', resolved?: ResolvedTheme): string {
  if (theme === 'system') {
    return resolved ? `Theme: System (${resolved === 'macchiato' ? 'Dark' : 'Light'})` : 'Theme: System';
  }
  if (theme === 'macchiato') return 'Theme: Macchiato';
  if (theme === 'latte') return 'Theme: Latte';
  if (theme === 'dark') return 'Theme: Dark';
  return 'Theme: System';
}

/**
 * Returns the Material Symbol icon name for the theme choice.
 */
export function getThemeIcon(theme: ThemeChoice | 'dark'): string {
  if (theme === 'system') return 'desktop_windows';
  if (theme === 'latte') return 'light_mode';
  return 'dark_mode';
}

/**
 * Subscribes to system color scheme changes (prefers-color-scheme: dark).
 * Returns an unsubscription function.
 */
export function watchSystemTheme(callback: (isDark: boolean) => void): () => void {
  if (typeof window === 'undefined' || !window.matchMedia) {
    return () => {};
  }

  try {
    const mq = window.matchMedia('(prefers-color-scheme: dark)');
    const listener = (event: MediaQueryListEvent | { matches: boolean }) => {
      callback(event.matches);
    };

    if (mq.addEventListener) {
      mq.addEventListener('change', listener);
      return () => mq.removeEventListener('change', listener);
    } else if ((mq as any).addListener) {
      (mq as any).addListener(listener);
      return () => (mq as any).removeListener(listener);
    }
  } catch {
    // ignore matchMedia subscription failures
  }

  return () => {};
}

/**
 * Retrieves the stored accent color from localStorage or defaults to 'blue'.
 */
export function getStoredAccentColor(storage?: StorageLike): AccentColor {
  try {
    const s = storage || (typeof window !== 'undefined' ? window.localStorage : undefined);
    if (!s || !s.getItem) return 'blue';
    const val = (s.getItem(ACCENT_STORAGE_KEY) || s.getItem('prox_orch_accent_color')) as AccentColor;
    if (ACCENT_PRESETS.some(p => p.id === val)) return val;
    return 'blue';
  } catch {
    return 'blue';
  }
}

/**
 * Persists the selected accent color to localStorage.
 */
export function setStoredAccentColor(accent: AccentColor, storage?: StorageLike): void {
  try {
    const s = storage || (typeof window !== 'undefined' ? window.localStorage : undefined);
    if (s && s.setItem) {
      s.setItem(ACCENT_STORAGE_KEY, accent);
    }
  } catch {
    // ignore local storage errors
  }
}

/**
 * Applies the selected accent color data-attribute to the document or target element.
 */
export function applyAccentColorToDocument(accent: AccentColor, targetElement?: ElementLike): void {
  const el = targetElement || (typeof document !== 'undefined' ? document.documentElement : undefined);
  if (!el) return;
  el.setAttribute('data-accent', accent);
}

