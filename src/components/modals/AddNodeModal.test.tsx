import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import { renderToString } from 'react-dom/server';
import {
  AddNodeModal,
  matchesTailscaleTag,
  type AddNodeModalProps,
  type TailscalePeerOption,
} from './AddNodeModal.tsx';

const samplePeers: TailscalePeerOption[] = [
  {
    hostName: 'likes-it-cold',
    dnsName: 'likes-it-cold.leo-banjo.ts.net',
    ip: '100.105.71.49',
    os: 'linux',
    online: true,
    tags: ['tag:host', 'tag:worker'],
  },
  {
    hostName: 'win-gaming-box',
    dnsName: 'win-gaming-box.leo-banjo.ts.net',
    ip: '100.105.71.55',
    os: 'windows',
    online: true,
    tags: ['tag:windows'],
  },
];

test('AddNodeModal returns null when isOpen is false', () => {
  const html = renderToString(
    React.createElement(AddNodeModal, {
      isOpen: false,
      onClose: () => {},
      onAddNode: () => {},
    })
  );
  assert.strictEqual(html, '');
});

test('AddNodeModal renders top tab navigation bar with 4 tabs', () => {
  const html = renderToString(
    React.createElement(AddNodeModal, {
      isOpen: true,
      onClose: () => {},
      onAddNode: () => {},
    })
  );

  assert.ok(html.includes('Tailscale Mesh'), 'Must include Tailscale Mesh tab');
  assert.ok(html.includes('Manual SSH'), 'Must include Manual SSH tab');
  assert.ok(html.includes('WinRM'), 'Must include WinRM tab');
  assert.ok(html.includes('Follow Tag'), 'Must include Follow Tag tab');
});

test('AddNodeModal default tab renders Tailscale Mesh peer picker and keyless Tailscale SSH options', () => {
  const html = renderToString(
    React.createElement(AddNodeModal, {
      isOpen: true,
      initialTab: 'tailscale',
      peers: samplePeers,
      onClose: () => {},
      onAddNode: () => {},
    })
  );

  // Peer options in dropdown
  assert.ok(html.includes('likes-it-cold'), 'Must render sample peer likes-it-cold');
  assert.ok(html.includes('100.105.71.49'), 'Must render sample peer IP');
  assert.ok(html.includes('Tailscale SSH') || html.includes('tailscale-ssh'), 'Must mention Tailscale SSH');
  assert.ok(html.includes('TAILSCALE MESH IP'), 'Must render Tailscale Mesh IP label');
});

test('AddNodeModal Manual SSH tab renders SSH defaults, vault badge, and local testing only note', () => {
  const html = renderToString(
    React.createElement(AddNodeModal, {
      isOpen: true,
      initialTab: 'manual_ssh',
      onClose: () => {},
      onAddNode: () => {},
    })
  );

  assert.ok(html.includes('SSH PORT') || html.includes('SSH Port') || html.includes('22'));
  assert.ok(html.includes('core'), 'Default SSH user should be core');
  assert.ok(html.includes('(Local testing only)'), 'ssh-agent forwarding must be explicitly marked with (Local testing only)');
  assert.ok(html.includes('SSH Vault Secrets'), 'Must display the SSH Vault Secrets panel');
});

test('AddNodeModal WinRM tab renders WinRM port 5985, transport NTLM, and Administrator user', () => {
  const html = renderToString(
    React.createElement(AddNodeModal, {
      isOpen: true,
      initialTab: 'winrm',
      onClose: () => {},
      onAddNode: () => {},
    })
  );

  assert.ok(html.includes('5985'), 'Default WinRM port must be 5985');
  assert.ok(html.includes('Administrator'), 'Default WinRM user must be Administrator');
  assert.ok(html.includes('NTLM') || html.includes('ntlm'), 'Default WinRM transport should be NTLM');
  assert.ok(html.includes('WinRM'), 'Must mention WinRM');
  assert.ok(
    html.includes('WinRM onboarding is not implemented'),
    'WinRM must be honestly marked as not implemented'
  );
  assert.ok(html.includes('GROUP / TAG') || html.includes('tag:host'), 'WinRM tab must include group / tag selector');
});

test('AddNodeModal Follow Tag tab asks for a plain tag name and offers Follow Tag & Sync Nodes', () => {
  const html = renderToString(
    React.createElement(AddNodeModal, {
      isOpen: true,
      initialTab: 'follow_tag',
      peers: samplePeers,
      onClose: () => {},
      onAddNode: () => {},
    })
  );

  assert.ok(html.includes('TAG NAME'), 'Must include Tag name field');
  // The operator types a plain tag name; the tag: prefix is shown separately.
  assert.ok(html.includes('placeholder="pve"'), 'Tag input must ask for a plain tag name (pve)');
  assert.ok(html.includes('tag:'), 'The tag: prefix must be displayed as a fixed prefix');
  assert.ok(
    html.includes('Follow Tag &amp; Sync Nodes') || html.includes('Follow Tag & Sync Nodes'),
    'Must include Follow Tag & Sync Nodes button'
  );
  assert.ok(html.includes('Matching Peers:'), 'Must show live matching peers preview');
  // No tag is preselected, so nothing matches yet.
  assert.ok(!html.includes('likes-it-cold'), 'No peers should match an empty tag');
});

test('matchesTailscaleTag normalizes the tag: prefix', () => {
  assert.equal(matchesTailscaleTag(['tag:host'], 'host'), true);
  assert.equal(matchesTailscaleTag(['tag:pve'], 'pve'), true);
  assert.equal(matchesTailscaleTag(['tag:pve'], 'tag:pve'), true);
  assert.equal(matchesTailscaleTag(['tag:worker'], 'host'), false);
  assert.equal(matchesTailscaleTag(undefined, 'host'), false);
  assert.equal(matchesTailscaleTag(['tag:host'], ''), false);
});

test('AddNodeModal Manual SSH tab includes secret encryption policy notice', () => {
  const html = renderToString(
    React.createElement(AddNodeModal, {
      isOpen: true,
      initialTab: 'manual_ssh',
      onClose: () => {},
      onAddNode: () => {},
    })
  );

  assert.ok(html.includes('ansible-vault encrypt_string'), 'Must reference ansible-vault encrypt_string');
  assert.ok(
    html.includes('Plaintext is never written to SQLite'),
    'Must confirm zero cleartext persistence'
  );
});

test('AddNodeModal WinRM tab includes HTTPS toggle with port 5986 option', () => {
  const html = renderToString(
    React.createElement(AddNodeModal, {
      isOpen: true,
      initialTab: 'winrm',
      onClose: () => {},
      onAddNode: () => {},
    })
  );

  assert.ok(html.includes('Use HTTPS') || html.includes('5986'), 'Must include HTTPS option');
  assert.ok(html.includes('Administrator'), 'Default WinRM user Administrator');
});

test('AddNodeModal Follow Tag tab offers all 4 shared authentication mechanisms', () => {
  const html = renderToString(
    React.createElement(AddNodeModal, {
      isOpen: true,
      initialTab: 'follow_tag',
      onClose: () => {},
      onAddNode: () => {},
    })
  );

  assert.ok(html.includes('tailscale-ssh'), 'Must offer tailscale-ssh option');
  assert.ok(html.includes('vault-password'), 'Must offer vault-password option');
  assert.ok(html.includes('vault-sshkey'), 'Must offer vault-sshkey option');
  assert.ok(html.includes('ssh-agent'), 'Must offer ssh-agent option');
});

