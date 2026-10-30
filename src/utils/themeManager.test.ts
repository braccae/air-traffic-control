import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  THEME_STORAGE_KEY,
  getStoredTheme,
  setStoredTheme,
  resolveTheme,
  toggleTheme,
  applyThemeToDocument,
  getThemeLabel,
  getThemeIcon,
  watchSystemTheme,
  getStoredAccentColor,
  setStoredAccentColor,
  applyAccentColorToDocument,
  type ThemeChoice,
  type ResolvedTheme,
} from './themeManager.ts';

describe('themeManager', () => {
  describe('getStoredTheme', () => {
    it('defaults to system when localStorage is empty', () => {
      const mockStorage = {
        getItem: (_key: string) => null,
      };
      assert.equal(getStoredTheme(mockStorage), 'system');
    });

    it('returns system when stored value is system', () => {
      const mockStorage = {
        getItem: (key: string) => (key === THEME_STORAGE_KEY ? 'system' : null),
      };
      assert.equal(getStoredTheme(mockStorage), 'system');
    });

    it('returns macchiato when stored value is macchiato', () => {
      const mockStorage = {
        getItem: (key: string) => (key === THEME_STORAGE_KEY ? 'macchiato' : null),
      };
      assert.equal(getStoredTheme(mockStorage), 'macchiato');
    });

    it('returns latte when stored value is latte', () => {
      const mockStorage = {
        getItem: (key: string) => (key === THEME_STORAGE_KEY ? 'latte' : null),
      };
      assert.equal(getStoredTheme(mockStorage), 'latte');
    });

    it('returns macchiato for legacy dark stored value', () => {
      const mockStorage = {
        getItem: (key: string) => (key === THEME_STORAGE_KEY ? 'dark' : null),
      };
      assert.equal(getStoredTheme(mockStorage), 'macchiato');
    });

    it('returns system for unknown stored theme string', () => {
      const mockStorage = {
        getItem: () => 'unknown-theme',
      };
      assert.equal(getStoredTheme(mockStorage), 'system');
    });

    it('returns system when storage is undefined', () => {
      assert.equal(getStoredTheme({} as any), 'system');
    });
  });

  describe('setStoredTheme', () => {
    it('persists selected theme choice to storage key', () => {
      let storedKey = '';
      let storedValue = '';
      const mockStorage = {
        setItem: (key: string, value: string) => {
          storedKey = key;
          storedValue = value;
        },
      };

      setStoredTheme('system', mockStorage);
      assert.equal(storedKey, THEME_STORAGE_KEY);
      assert.equal(storedValue, 'system');

      setStoredTheme('macchiato', mockStorage);
      assert.equal(storedValue, 'macchiato');

      setStoredTheme('latte', mockStorage);
      assert.equal(storedValue, 'latte');
    });

    it('handles storage errors gracefully without throwing', () => {
      const mockStorage = {
        setItem: () => {
          throw new Error('QuotaExceeded');
        },
      };
      assert.doesNotThrow(() => {
        setStoredTheme('macchiato', mockStorage);
      });
    });
  });

  describe('resolveTheme', () => {
    it('resolves system to macchiato when system preference is dark', () => {
      assert.equal(resolveTheme('system', true), 'macchiato');
    });

    it('resolves system to latte when system preference is light', () => {
      assert.equal(resolveTheme('system', false), 'latte');
    });

    it('resolves macchiato to macchiato regardless of system preference', () => {
      assert.equal(resolveTheme('macchiato', false), 'macchiato');
      assert.equal(resolveTheme('macchiato', true), 'macchiato');
    });

    it('resolves latte to latte regardless of system preference', () => {
      assert.equal(resolveTheme('latte', false), 'latte');
      assert.equal(resolveTheme('latte', true), 'latte');
    });

    it('resolves legacy dark to macchiato', () => {
      assert.equal(resolveTheme('dark', false), 'macchiato');
      assert.equal(resolveTheme('dark', true), 'macchiato');
    });

    it('resolves unknown or empty choice using system preference', () => {
      assert.equal(resolveTheme(undefined as any, true), 'macchiato');
      assert.equal(resolveTheme(null as any, false), 'latte');
      assert.equal(resolveTheme('custom' as any, true), 'macchiato');
      assert.equal(resolveTheme('custom' as any, false), 'latte');
    });
  });

  describe('toggleTheme', () => {
    it('cycles system -> macchiato -> latte -> system', () => {
      assert.equal(toggleTheme('system'), 'macchiato');
      assert.equal(toggleTheme('macchiato'), 'latte');
      assert.equal(toggleTheme('latte'), 'system');
    });

    it('handles legacy dark by toggling to latte', () => {
      assert.equal(toggleTheme('dark' as any), 'latte');
    });
  });

  describe('applyThemeToDocument', () => {
    it('sets data-theme="macchiato" and manages classes for macchiato', () => {
      const attributes: Record<string, string> = {};
      const classes = new Set<string>();

      const mockElement = {
        setAttribute: (name: string, value: string) => {
          attributes[name] = value;
        },
        classList: {
          add: (...tokens: string[]) => {
            tokens.forEach((c) => classes.add(c));
          },
          remove: (...tokens: string[]) => {
            tokens.forEach((c) => classes.delete(c));
          },
        },
      };

      const resolved = applyThemeToDocument('macchiato', mockElement);
      assert.equal(resolved, 'macchiato');
      assert.equal(attributes['data-theme'], 'macchiato');
      assert.equal(attributes['data-theme-choice'], 'macchiato');
      assert.equal(classes.has('theme-macchiato'), true);
      assert.equal(classes.has('theme-latte'), false);
    });

    it('sets data-theme="latte" and manages classes for latte', () => {
      const attributes: Record<string, string> = {};
      const classes = new Set<string>(['theme-macchiato']);

      const mockElement = {
        setAttribute: (name: string, value: string) => {
          attributes[name] = value;
        },
        classList: {
          add: (...tokens: string[]) => {
            tokens.forEach((c) => classes.add(c));
          },
          remove: (...tokens: string[]) => {
            tokens.forEach((c) => classes.delete(c));
          },
        },
      };

      const resolved = applyThemeToDocument('latte', mockElement);
      assert.equal(resolved, 'latte');
      assert.equal(attributes['data-theme'], 'latte');
      assert.equal(attributes['data-theme-choice'], 'latte');
      assert.equal(classes.has('theme-latte'), true);
      assert.equal(classes.has('theme-macchiato'), false);
    });

    it('resolves system choice using systemIsDark parameter', () => {
      const attributes: Record<string, string> = {};
      const classes = new Set<string>();

      const mockElement = {
        setAttribute: (name: string, value: string) => {
          attributes[name] = value;
        },
        classList: {
          add: (...tokens: string[]) => {
            tokens.forEach((c) => classes.add(c));
          },
          remove: (...tokens: string[]) => {
            tokens.forEach((c) => classes.delete(c));
          },
        },
      };

      const resolvedDark = applyThemeToDocument('system', mockElement, true);
      assert.equal(resolvedDark, 'macchiato');
      assert.equal(attributes['data-theme'], 'macchiato');
      assert.equal(attributes['data-theme-choice'], 'system');
      assert.equal(classes.has('theme-macchiato'), true);

      const resolvedLight = applyThemeToDocument('system', mockElement, false);
      assert.equal(resolvedLight, 'latte');
      assert.equal(attributes['data-theme'], 'latte');
      assert.equal(attributes['data-theme-choice'], 'system');
      assert.equal(classes.has('theme-latte'), true);
    });
  });

  describe('getThemeLabel & getThemeIcon', () => {
    it('returns appropriate label and icon for system mode', () => {
      assert.equal(getThemeLabel('system'), 'Theme: System');
      assert.equal(getThemeIcon('system'), 'desktop_windows');
    });

    it('returns appropriate label and icon for macchiato mode', () => {
      assert.equal(getThemeLabel('macchiato'), 'Theme: Macchiato');
      assert.equal(getThemeIcon('macchiato'), 'dark_mode');
    });

    it('returns appropriate label and icon for latte mode', () => {
      assert.equal(getThemeLabel('latte'), 'Theme: Latte');
      assert.equal(getThemeIcon('latte'), 'light_mode');
    });

    it('handles legacy dark gracefully', () => {
      assert.equal(getThemeLabel('dark' as any), 'Theme: Dark');
      assert.equal(getThemeIcon('dark' as any), 'dark_mode');
    });
  });

  describe('watchSystemTheme', () => {
    it('returns a no-op cleanup when matchMedia is unavailable', () => {
      const originalWindow = (globalThis as any).window;
      try {
        (globalThis as any).window = undefined;
        const cleanup = watchSystemTheme(() => {});
        assert.equal(typeof cleanup, 'function');
        assert.doesNotThrow(() => cleanup());
      } finally {
        (globalThis as any).window = originalWindow;
      }
    });

    it('registers media query listener and invokes callback on change', () => {
      let listener: ((e: { matches: boolean }) => void) | null = null;
      let removed = false;

      const mockMediaQueryList = {
        matches: true,
        addEventListener: (event: string, cb: any) => {
          if (event === 'change') listener = cb;
        },
        removeEventListener: (event: string, cb: any) => {
          if (event === 'change' && cb === listener) removed = true;
        },
      };

      const originalWindow = (globalThis as any).window;
      try {
        (globalThis as any).window = {
          matchMedia: (query: string) => {
            assert.equal(query, '(prefers-color-scheme: dark)');
            return mockMediaQueryList;
          },
        };

        let detectedIsDark = false;
        const cleanup = watchSystemTheme((isDark) => {
          detectedIsDark = isDark;
        });

        assert.equal(typeof listener, 'function');
        // Simulate OS theme switch to light
        listener!({ matches: false });
        assert.equal(detectedIsDark, false);

        // Simulate OS theme switch to dark
        listener!({ matches: true });
        assert.equal(detectedIsDark, true);

        // Cleanup
        cleanup();
        assert.equal(removed, true);
      } finally {
        (globalThis as any).window = originalWindow;
      }
    });
  });

  describe('Accent Colors', () => {
    it('defaults to blue when storage is empty', () => {
      const mockStorage = {
        getItem: () => null,
        setItem: () => {},
      };
      assert.equal(getStoredAccentColor(mockStorage), 'blue');
    });

    it('persists and retrieves accent color', () => {
      let saved = '';
      const mockStorage = {
        getItem: () => saved,
        setItem: (_k: string, v: string) => {
          saved = v;
        },
      };
      setStoredAccentColor('teal', mockStorage);
      assert.equal(saved, 'teal');
      assert.equal(getStoredAccentColor(mockStorage), 'teal');
    });

    it('applies accent data attribute to document element', () => {
      const attributes: Record<string, string> = {};
      const mockElement = {
        setAttribute: (name: string, value: string) => {
          attributes[name] = value;
        },
      };
      applyAccentColorToDocument('peach', mockElement);
      assert.equal(attributes['data-accent'], 'peach');
    });
  });
});
