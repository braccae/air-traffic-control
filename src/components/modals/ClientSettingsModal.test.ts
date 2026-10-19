import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import { renderToString } from 'react-dom/server';
import { ClientSettingsModal, type ClientSettingsModalProps } from './ClientSettingsModal.tsx';
import { ACCENT_PRESETS } from '../../utils/themeManager.ts';

test('ClientSettingsModal renders nothing when isOpen is false', () => {
  const html = renderToString(
    React.createElement(ClientSettingsModal, {
      isOpen: false,
      onClose: () => {},
      themeChoice: 'system',
      onSelectTheme: () => {},
      accentColor: 'blue',
      onSelectAccentColor: () => {},
      highContrast: true,
      onToggleHighContrast: () => {},
    })
  );

  assert.strictEqual(html, '', 'Closed modal must render empty string');
});

test('ClientSettingsModal renders title, theme selection cards, and active theme badge when open', () => {
  const html = renderToString(
    React.createElement(ClientSettingsModal, {
      isOpen: true,
      onClose: () => {},
      themeChoice: 'macchiato',
      onSelectTheme: () => {},
      accentColor: 'blue',
      onSelectAccentColor: () => {},
      highContrast: true,
      onToggleHighContrast: () => {},
    })
  );

  assert.ok(html.includes('Client Settings &amp; Preferences'), 'Must contain modal title');
  assert.ok(html.includes('Theme Selection'), 'Must contain theme section header');
  assert.ok(html.includes('System Default'), 'Must contain System Default theme option');
  assert.ok(html.includes('Catppuccin Macchiato'), 'Must contain Catppuccin Macchiato theme option');
  assert.ok(html.includes('Catppuccin Latte'), 'Must contain Catppuccin Latte theme option');
  assert.ok(html.includes('Active: macchiato'), 'Must indicate active theme');
});

test('ClientSettingsModal renders all accent color presets with hex codes and labels', () => {
  const html = renderToString(
    React.createElement(ClientSettingsModal, {
      isOpen: true,
      onClose: () => {},
      themeChoice: 'system',
      onSelectTheme: () => {},
      accentColor: 'lavender',
      onSelectAccentColor: () => {},
      highContrast: false,
      onToggleHighContrast: () => {},
    })
  );

  assert.ok(html.includes('Accent Color Customization'), 'Must contain accent section header');
  assert.ok(html.includes('Active: lavender'), 'Must indicate active accent');

  for (const preset of ACCENT_PRESETS) {
    assert.ok(html.includes(preset.label), `Must contain preset label: ${preset.label}`);
    assert.ok(html.includes(preset.hexDark), `Must contain preset hex: ${preset.hexDark}`);
  }
});

test('ClientSettingsModal renders Display & Accessibility section with High Contrast and Reduce Pulse options', () => {
  const html = renderToString(
    React.createElement(ClientSettingsModal, {
      isOpen: true,
      onClose: () => {},
      themeChoice: 'system',
      onSelectTheme: () => {},
      accentColor: 'blue',
      onSelectAccentColor: () => {},
      highContrast: true,
      onToggleHighContrast: () => {},
      reducedMotion: true,
      onToggleReducedMotion: () => {},
    })
  );

  assert.ok(html.includes('Display &amp; Accessibility'), 'Must contain display section');
  assert.ok(html.includes('High Contrast Borders'), 'Must contain High Contrast option');
  assert.ok(html.includes('Reduce Pulse Animations'), 'Must contain Reduce Pulse option');
});
