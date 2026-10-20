import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import { renderToString } from 'react-dom/server';
import {
  TailscaleExplorerView,
  mapTailscalePayload,
  stageTailscaleFeature,
} from './TailscaleExplorerView.tsx';
import type { NodeItem } from '../types/index.ts';

const mockNode: NodeItem = {
  id: 'node-dev-pc',
  name: 'dev-pc',
  shortName: 'dev-pc',
  group: 'tag:host',
  datacenter: 'homelab',
  nodeType: 'linux',
  ip: '172.17.0.1',
  port: 22,
  sshUser: 'pants',
  tailscaleIp: '100.97.159.101',
  tailscaleHost: 'dev-pc.leo-banjo.ts.net',
  osDescription: 'Fedora 40',
  status: 'online',
  uptime: '1 day',
  hasGlances: true,
  hasCockpit: false,
  hasPodman: true,
  hasRestic: false,
  specs: {
    cpu: 'AMD',
    vcpus: 8,
    ramGiB: 32,
    motherboard: '',
    bios: '',
    ipmi: '',
    ecc: '',
    secureBoot: '',
    iommu: '',
  },
};

test('mapTailscalePayload maps self + peers + prefs without fabricating data', () => {
  const payload = {
    backendState: 'Running',
    magicDNSSuffix: 'leo-banjo.ts.net',
    self: {
      id: 'self-1',
      hostName: 'dev-pc',
      dnsName: 'dev-pc.leo-banjo.ts.net.',
      tailscaleIPs: ['100.97.159.101'],
      online: true,
      os: 'linux',
      tags: ['tag:host'],
      exitNode: false,
    },
    peers: [
      {
        key: 'nodekey:aaaa',
        hostName: 'pebble',
        dnsName: 'pebble.leo-banjo.ts.net.',
        tailscaleIPs: ['100.72.21.74'],
        online: true,
        relay: '',
        curAddr: '192.168.8.10:41641',
        rxBytes: 1024,
        txBytes: 2048,
        exitNodeOption: true,
        tags: ['tag:host'],
      },
      {
        key: 'nodekey:bbbb',
        hostName: 'flint',
        dnsName: 'flint.leo-banjo.ts.net.',
        tailscaleIPs: ['100.73.87.87'],
        online: false,
        relay: 'derp-abc',
      },
    ],
    prefs: {
      acceptDNS: true,
      acceptRoutes: false,
      ssh: true,
      advertiseExitNode: false,
      advertiseRoutes: '10.0.0.0/8',
      exitNode: '',
      hostname: 'dev-pc',
    },
  };

  const mapped = mapTailscalePayload(payload);

  // Self is included and flagged.
  const self = mapped.peers.find((p) => p.isSelf);
  assert.ok(self, 'self peer must be present');
  assert.strictEqual(self!.HostName, 'dev-pc');
  assert.strictEqual(self!.isDirect, true);

  // Two remote peers rendered with correct direct/relay classification.
  const remote = mapped.peers.filter((p) => !p.isSelf);
  assert.strictEqual(remote.length, 2);
  const pebble = remote.find((p) => p.HostName === 'pebble');
  const flint = remote.find((p) => p.HostName === 'flint');
  assert.ok(pebble && flint);
  assert.strictEqual(pebble!.isDirect, true, 'empty relay => direct');
  assert.strictEqual(pebble!.ExitNodeOption, true);
  assert.strictEqual(flint!.isDirect, false, 'non-empty relay => relayed');

  // Prefs map through unchanged.
  assert.strictEqual(mapped.prefs.acceptDNS, true);
  assert.strictEqual(mapped.prefs.ssh, true);
  assert.strictEqual(mapped.prefs.advertiseRoutes, '10.0.0.0/8');
  assert.strictEqual(mapped.backendState, 'Running');
});

test('mapTailscalePayload tolerates an empty payload', () => {
  const mapped = mapTailscalePayload({});
  assert.deepStrictEqual(mapped.peers, []);
  assert.strictEqual(mapped.prefs.acceptDNS, false);
  assert.strictEqual(mapped.selfName, '');
});

test('TailscaleExplorerView renders Feature Configuration panel for a selected node', () => {
  const html = renderToString(
    React.createElement(TailscaleExplorerView, { node: mockNode, onOpenTerminalForHost: () => {} })
  );
  assert.ok(html.includes('Feature Configuration'), 'Must render the feature configuration panel');
  assert.ok(html.includes('Accept DNS'), 'Must render the accept-dns toggle');
  assert.ok(html.includes('Accept Routes'), 'Must render the accept-routes toggle');
  assert.ok(html.includes('Tailscale SSH'), 'Must render the ssh toggle');
  assert.ok(html.includes('Advertise Routes'), 'Must render the advertise-routes field');
  assert.ok(html.includes('dev-pc'), 'Must reference the selected node name');
});

test('TailscaleExplorerView without a node renders the global mesh header', () => {
  const html = renderToString(React.createElement(TailscaleExplorerView, { onOpenTerminalForHost: () => {} }));
  assert.ok(html.includes('Tailscale Mesh Explorer'), 'Must render global mesh header');
  assert.strictEqual(html.includes('Feature Configuration'), false, 'No feature panel without a node');
});

test('stageTailscaleFeature compiles the action and stages it in the queue', async () => {
  const originalFetch = globalThis.fetch;
  const calls: Array<{ url: string; method: string; body: any }> = [];
  try {
    globalThis.fetch = (async (input: any, init?: RequestInit) => {
      const url = String(input);
      calls.push({ url, method: init?.method || 'GET', body: init?.body ? JSON.parse(String(init.body)) : null });
      if (url.includes('/actions/set_accept_dns')) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            name: 'tailscale set --accept-dns=true',
            steps: [
              { name: 'tailscale set --accept-dns=true', module: 'ansible.builtin.command', params: { cmd: 'tailscale set --accept-dns=true' } },
            ],
          }),
        } as any;
      }
      if (url === '/api/queue/task') {
        return { ok: true, status: 200, json: async () => ({ success: true, task: { id: 'task-ts-1', title: 'tailscale set --accept-dns=true' } }) } as any;
      }
      return { ok: false, status: 404, json: async () => ({}) } as any;
    }) as any;

    const res = await stageTailscaleFeature(mockNode, 'set_accept_dns', { enabled: true });
    assert.strictEqual(res.success, true);

    const actionCall = calls.find((c) => c.url.includes('/actions/set_accept_dns'));
    assert.ok(actionCall, 'Must call the daemon action compile endpoint');
    assert.strictEqual(
      actionCall!.url,
      `/api/nodes/${encodeURIComponent(mockNode.id)}/plugins/network/tailscale/actions/set_accept_dns`
    );
    assert.strictEqual(actionCall!.method, 'POST');
    assert.deepStrictEqual(actionCall!.body?.options, { enabled: true });

    const queueCall = calls.find((c) => c.url === '/api/queue/task');
    assert.ok(queueCall, 'Must stage the compiled task into the Ansible mutation queue');
    assert.strictEqual(queueCall!.body?.primaryModule, 'ansible.builtin.command');
    assert.strictEqual(queueCall!.body?.category, 'network');
    assert.strictEqual(queueCall!.body?.status, 'staged');
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test('stageTailscaleFeature surfaces daemon compile errors honestly (no mock fallback)', async () => {
  const originalFetch = globalThis.fetch;
  try {
    globalThis.fetch = (async () => ({
      ok: false,
      status: 400,
      json: async () => ({ error: 'unsupported tailscale action: bogus' }),
    })) as any;

    const res = await stageTailscaleFeature(mockNode, 'bogus', {});
    assert.strictEqual(res.success, false);
    assert.ok(res.error && res.error.includes('unsupported tailscale action'), 'Must surface the real daemon error');
  } finally {
    globalThis.fetch = originalFetch;
  }
});
