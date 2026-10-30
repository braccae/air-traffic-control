import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

/**
 * Guards the theme against drift from the official Catppuccin values
 * (Latte for light mode, Macchiato for dark mode).
 *
 * The palette itself is no longer hand-maintained: it comes from
 * `@catppuccin/tailwindcss`, which publishes Latte on `:root` and every other
 * flavour on its own scoping class (`.macchiato` here). These tests pin that
 * source, then assert our own stylesheet never invents a colour of its own.
 *
 * Reference: https://catppuccin.com/palette/
 */

const here = dirname(fileURLToPath(import.meta.url));
const appCss = readFileSync(join(here, '..', 'index.css'), 'utf8');
const pkgCss = readFileSync(
  join(here, '..', '..', 'node_modules', '@catppuccin', 'tailwindcss', 'macchiato.css'),
  'utf8',
);

const PALETTE: Record<string, Record<string, string>> = {
  latte: {
    rosewater: '#dc8a78',
    flamingo: '#dd7878',
    pink: '#ea76cb',
    mauve: '#8839ef',
    red: '#d20f39',
    maroon: '#e64553',
    peach: '#fe640b',
    yellow: '#df8e1d',
    green: '#40a02b',
    teal: '#179299',
    sky: '#04a5e5',
    sapphire: '#209fb5',
    blue: '#1e66f5',
    lavender: '#7287fd',
    text: '#4c4f69',
    subtext1: '#5c5f77',
    subtext0: '#6c6f85',
    overlay2: '#7c7f93',
    overlay1: '#8c8fa1',
    overlay0: '#9ca0b0',
    surface2: '#acb0be',
    surface1: '#bcc0cc',
    surface0: '#ccd0da',
    base: '#eff1f5',
    mantle: '#e6e9ef',
    crust: '#dce0e8',
  },
  macchiato: {
    rosewater: '#f4dbd6',
    flamingo: '#f0c6c6',
    pink: '#f5bde6',
    mauve: '#c6a0f6',
    red: '#ed8796',
    maroon: '#ee99a0',
    peach: '#f5a97f',
    yellow: '#eed49f',
    green: '#a6da95',
    teal: '#8bd5ca',
    sky: '#91d7e3',
    sapphire: '#7dc4e4',
    blue: '#8aadf4',
    lavender: '#b7bdf8',
    text: '#cad3f5',
    subtext1: '#b8c0e0',
    subtext0: '#a5adcb',
    overlay2: '#939ab7',
    overlay1: '#8087a2',
    overlay0: '#6e738d',
    surface2: '#5b6078',
    surface1: '#494d64',
    surface0: '#363a4f',
    base: '#24273a',
    mantle: '#1e2030',
    crust: '#181926',
  },
};

/** Extracts a `@layer base` block body for the given selector. */
function flavourBlock(selector: ':root' | '.macchiato'): string {
  const re = new RegExp(`\\n  ${selector.replace('.', '\\.')} \\{([\\s\\S]*?)\\n  \\}`);
  const match = pkgCss.match(re);
  assert.ok(match, `missing ${selector} block in @catppuccin/tailwindcss`);
  return match![1];
}

test('index.css consumes the official Catppuccin Tailwind package', () => {
  assert.match(
    appCss,
    /@import\s+"@catppuccin\/tailwindcss\/macchiato\.css";/,
    'index.css must import the official palette instead of hand-maintained tokens',
  );
  // Latte ships on :root, Macchiato on the .macchiato scoping class.
  assert.ok(flavourBlock(':root').length > 0);
  assert.ok(flavourBlock('.macchiato').length > 0);
});

for (const [flavour, colours] of Object.entries(PALETTE)) {
  test(`${flavour} palette values in the package match the official palette`, () => {
    const block = flavourBlock(flavour === 'latte' ? ':root' : '.macchiato');
    for (const [name, expected] of Object.entries(colours)) {
      const match = block.match(new RegExp(`--catppuccin-color-${name}:\\s*(#[0-9a-f]{6});`, 'i'));
      assert.ok(match, `missing --catppuccin-color-${name} in the ${flavour} block`);
      assert.equal(
        match![1].toLowerCase(),
        expected,
        `--catppuccin-color-${name} must be ${expected} in ${flavour}`,
      );
    }
  });
}

/**
 * Walks a stylesheet and returns the colour literals used in *declarations*
 * (hex colours inside escaped class selectors such as `.bg-\[\#0a0d1f\]` are
 * component names, not colours, so they are excluded).
 */
function declarationColours(css: string): string[] {
  const withoutSelectors = css.replace(/\\#[0-9a-fA-F]{6}/g, '');
  return withoutSelectors.match(/(?<!\\)#[0-9a-fA-F]{3,8}\b/g) ?? [];
}

test('app stylesheet never hardcodes a colour outside the palette', () => {
  const violations = declarationColours(appCss);
  assert.deepEqual(
    violations,
    [],
    'every colour must resolve through var(--catppuccin-color-*); found: ' + violations.join(', '),
  );
});

test('app stylesheet only references palette variables that the package defines', () => {
  const referenced = new Set(
    Array.from(appCss.matchAll(/var\(--catppuccin-color-([a-z0-9-]+)\)/g), (m) => m[1]),
  );
  assert.ok(referenced.size > 0, 'expected palette variable references');

  const defined = new Set(
    Array.from(pkgCss.matchAll(/--catppuccin-color-([a-z0-9-]+):/g), (m) => m[1]),
  );
  const unknown = Array.from(referenced).filter((name) => !defined.has(name));
  assert.deepEqual(unknown, [], `unknown palette tokens: ${unknown.join(', ')}`);
});

test('legacy --ctp-* token names are fully retired', () => {
  assert.equal(appCss.includes('--ctp-'), false, 'hand-maintained --ctp-* tokens should be gone');
});

test('both flavours are scoped on the document root', () => {
  assert.ok(appCss.includes('[data-theme="latte"]'), 'Latte theme selector missing');
  assert.ok(appCss.includes('[data-theme="macchiato"]'), 'Macchiato theme selector missing');
});
