import test from 'node:test';
import assert from 'node:assert/strict';
import { resolvePluginIcon } from './pluginIcons.ts';

test('resolvePluginIcon maps invalid icon strings to valid Material Symbols', () => {
  assert.equal(resolvePluginIcon('activity'), 'settings_suggest');
  assert.equal(resolvePluginIcon('container'), 'deployed_code');
  assert.equal(resolvePluginIcon('server'), 'dns');
  assert.equal(resolvePluginIcon('box'), 'package_2');
  assert.equal(resolvePluginIcon('shield-check'), 'verified_user');
  assert.equal(resolvePluginIcon('shield-alert'), 'admin_panel_settings');
  assert.equal(resolvePluginIcon('user-check'), 'person_check');
});

test('resolvePluginIcon preserves valid Material Symbols', () => {
  assert.equal(resolvePluginIcon('terminal'), 'terminal');
  assert.equal(resolvePluginIcon('insights'), 'insights');
  assert.equal(resolvePluginIcon('inventory_2'), 'inventory_2');
  assert.equal(resolvePluginIcon('security'), 'security');
});

test('resolvePluginIcon falls back to extension for unknown or empty strings', () => {
  assert.equal(resolvePluginIcon(''), 'extension');
  assert.equal(resolvePluginIcon(undefined), 'extension');
  assert.equal(resolvePluginIcon('non_existent_crazy_icon_name'), 'extension');
});
