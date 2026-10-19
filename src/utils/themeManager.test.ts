import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  THEME_STORAGE_KEY,
  getStoredTheme,
  setStoredTheme,
  toggleTheme,
  applyThemeToDocument,
  getThemeLabel,
  getThemeIcon,
  type ThemeMode,
} from './themeManager.ts';

describe('themeManager', () => {
  describe('getStoredTheme', () => {
    it('defaults to dark when localStorage is empty', () => {
      const mockStorage = {
        getItem: (_key: string) => null,
      };
      assert.equal(getStoredTheme(mockStorage), 'dark');
    });

    it('returns latte when stored value is latte', () => {
      const mockStorage = {
        getItem: (key: string) => (key === THEME_STORAGE_KEY ? 'latte' : null),
      };
      assert.equal(getStoredTheme(mockStorage), 'latte');
    });

    it('returns dark for unknown stored theme string', () => {
      const mockStorage = {
        getItem: () => 'unknown-theme',
      };
      assert.equal(getStoredTheme(mockStorage), 'dark');
    });
  });

  describe('setStoredTheme', () => {
    it('persists selected theme to storage key', () => {
      let storedKey = '';
      let storedValue = '';
      const mockStorage = {
        setItem: (key: string, value: string) => {
          storedKey = key;
          storedValue = value;
        },
      };

      setStoredTheme('latte', mockStorage);
      assert.equal(storedKey, THEME_STORAGE_KEY);
      assert.equal(storedValue, 'latte');

      setStoredTheme('dark', mockStorage);
      assert.equal(storedValue, 'dark');
    });
  });

  describe('toggleTheme', () => {
    it('switches dark to latte and latte to dark', () => {
      assert.equal(toggleTheme('dark'), 'latte');
      assert.equal(toggleTheme('latte'), 'dark');
    });
  });

  describe('applyThemeToDocument', () => {
    it('sets data-theme attribute and syncs theme class on target element', () => {
      const attributes: Record<string, string> = {};
      const classes = new Set<string>();

      const mockElement = {
        setAttribute: (name: string, value: string) => {
          attributes[name] = value;
        },
        classList: {
          add: (c: string) => {
            classes.add(c);
          },
          remove: (c: string) => {
            classes.delete(c);
          },
        },
      };

      applyThemeToDocument('latte', mockElement);
      assert.equal(attributes['data-theme'], 'latte');
      assert.equal(classes.has('theme-latte'), true);

      applyThemeToDocument('dark', mockElement);
      assert.equal(attributes['data-theme'], 'dark');
      assert.equal(classes.has('theme-latte'), false);
    });
  });

  describe('getThemeLabel & getThemeIcon', () => {
    it('returns appropriate label and icon for dark mode', () => {
      assert.equal(getThemeLabel('dark'), 'Theme: Dark');
      assert.equal(getThemeIcon('dark'), 'dark_mode');
    });

    it('returns appropriate label and icon for latte mode', () => {
      assert.equal(getThemeLabel('latte'), 'Theme: Latte');
      assert.equal(getThemeIcon('latte'), 'light_mode');
    });
  });
});
