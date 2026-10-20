import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import { renderToString } from 'react-dom/server';
import { GlobalSettingsPanel } from './GlobalSettingsPanel.tsx';
import type { GlobalSettingsPage } from '../types/index.ts';

const atcTailscalePage: GlobalSettingsPage = {
  id: 'connection/tailscale',
  title: 'ATC Tailscale',
  icon: 'hub',
  description: "ATC's own Tailscale node",
  order: 15,
  fields: [
    {
      name: 'authKey',
      label: 'Tailscale Auth Key',
      type: 'secret',
      secret: true,
      placeholder: 'tskey-auth-...',
    },
    { name: 'hostname', label: 'Node Hostname', type: 'string', placeholder: 'atc-node' },
    { name: 'ephemeral', label: 'Ephemeral Node', type: 'bool', default: true },
  ],
  values: {
    authKey: '•••••••• (vault: authKey)',
    hostname: 'air-traffic-control-tsnet',
    ephemeral: true,
  },
};

test('secret fields render masked, never pre-filled with the stored value', () => {
  const html = renderToString(<GlobalSettingsPanel page={atcTailscalePage} />);

  // The write-only secret is a password input that starts empty.
  assert.match(html, /type="password"/);
  assert.match(html, /aria-label="Tailscale Auth Key"/);

  // The mask is shown as the stored-state hint (placeholder + caption) only.
  assert.match(html, /Stored: •••••••• \(vault: authKey\)/);
  assert.match(html, /placeholder="•••••••• \(vault: authKey\)"/);

  // A secret must never land in a value attribute.
  const passwordTag = html.slice(html.indexOf('type="password"'));
  const tagEnd = passwordTag.indexOf('>');
  assert.equal(/value="[^"]+"/.test(passwordTag.slice(0, tagEnd)), false);

  // Non-secret fields still render their persisted value.
  assert.match(html, /value="air-traffic-control-tsnet"/);
});

test('a page with no stored secret says so instead of showing a mask', () => {
  const html = renderToString(
    <GlobalSettingsPanel page={{ ...atcTailscalePage, values: { authKey: '', hostname: 'atc' } }} />
  );
  assert.match(html, /No value stored/);
  assert.equal(html.includes('Stored: ••••••••'), false);
});
