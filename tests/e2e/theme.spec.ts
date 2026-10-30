import { test, expect } from '@playwright/test';

/**
 * Guards the Catppuccin integration:
 *  - `@catppuccin/tailwindcss` resolves the palette for the active flavour;
 *  - the app's flavour scope is OS-independent (no leakage from the package's
 *    `prefers-color-scheme` dark block);
 *  - legacy hex utilities are remapped onto palette tokens.
 */

const OFFICIAL = {
  latte: { base: '#eff1f5', crust: '#dce0e8', teal: 'rgb(23, 146, 153)', text: '#4c4f69' },
  macchiato: { base: '#24273a', crust: '#181926', teal: 'rgb(139, 213, 202)', text: '#cad3f5' },
} as const;

for (const scheme of ['light', 'dark'] as const) {
  for (const theme of ['latte', 'macchiato'] as const) {
    test(`theme ${theme} resolves the official palette (os ${scheme})`, async ({ browser }) => {
      const context = await browser.newContext({ colorScheme: scheme });
      const page = await context.newPage();
      await page.goto('/');
      await page.evaluate((t) => localStorage.setItem('air_traffic_control_theme', t), theme);
      await page.reload();
      await page.waitForSelector('#root');

      const probe = await page.evaluate(() => {
        const root = getComputedStyle(document.documentElement);
        // Exercise the generated hex-utility overrides with a throwaway element.
        const chip = document.createElement('div');
        chip.className = 'bg-[#8cd6cb]/20 text-[#8cd6cb]';
        document.body.appendChild(chip);
        const chipStyle = getComputedStyle(chip);
        const result = {
          htmlClass: document.documentElement.className,
          dataTheme: document.documentElement.getAttribute('data-theme'),
          base: root.getPropertyValue('--catppuccin-color-base').trim(),
          crust: root.getPropertyValue('--catppuccin-color-crust').trim(),
          text: root.getPropertyValue('--catppuccin-color-text').trim(),
          bodyBg: getComputedStyle(document.body).backgroundColor,
          chipColor: chipStyle.color,
        };
        chip.remove();
        return result;
      });

      const expected = OFFICIAL[theme];
      expect(probe.dataTheme).toBe(theme);
      expect(probe.htmlClass).toContain(theme);
      expect(probe.base).toBe(expected.base);
      expect(probe.crust).toBe(expected.crust);
      expect(probe.text).toBe(expected.text);
      // Body background must follow the flavour, not the OS colour scheme.
      const [r, g, b] = expected.base
        .replace('#', '')
        .match(/../g)!
        .map((h) => parseInt(h, 16));
      expect(probe.bodyBg).toBe(`rgb(${r}, ${g}, ${b})`);
      // Legacy hex utility remapped to the palette teal.
      expect(probe.chipColor).toBe(expected.teal);

      await context.close();
    });
  }
}
