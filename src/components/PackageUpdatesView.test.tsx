import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import { renderToString } from 'react-dom/server';
import { BootcImageSwitchPanel, stageBootcImageSwitch } from './PackageUpdatesView.tsx';
import type { NodeItem } from '../types/index.ts';

const mockNode: NodeItem = {
  id: 'node-bootc-1',
  name: 'atomic-host',
  shortName: 'atomic-host',
  group: 'tag:host',
  datacenter: 'homelab',
  nodeType: 'linux',
  ip: '192.168.16.60',
  port: 22,
  sshUser: 'core',
  tailscaleIp: '100.115.92.60',
  tailscaleHost: 'atomic-host.ts.net',
  osDescription: 'Fedora CoreOS 40',
  status: 'online',
  uptime: '3 days',
  hasGlances: true,
  hasCockpit: false,
  hasPodman: true,
  hasRestic: false,
  specs: {
    cpu: 'Intel',
    vcpus: 4,
    ramGiB: 16,
    motherboard: '',
    bios: '',
    ipmi: '',
    ecc: '',
    secureBoot: '',
    iommu: '',
  },
};

test('BootcImageSwitchPanel renders image input and stage button', () => {
  const html = renderToString(
    React.createElement(BootcImageSwitchPanel, {
      imageRef: '',
      onChange: () => {},
      onStage: () => {},
      staging: false,
    })
  );

  assert.ok(html.includes('Switch Container Image'), 'Must render the switch panel title');
  assert.ok(html.includes('Stage Image Switch'), 'Must render the stage button');
  assert.ok(html.includes('quay.io/org/os:latest'), 'Must render the image ref placeholder');
});

test('BootcImageSwitchPanel reflects staging state in the button label', () => {
  const html = renderToString(
    React.createElement(BootcImageSwitchPanel, {
      imageRef: 'quay.io/acme/os:latest',
      onChange: () => {},
      onStage: () => {},
      staging: true,
    })
  );

  assert.ok(html.includes('Staging...'), 'Must show staging progress label');
  assert.strictEqual(html.includes('Stage Image Switch'), false, 'Must not show idle label while staging');
});

test('stageBootcImageSwitch rejects an empty image ref without contacting the daemon', async () => {
  const originalFetch = globalThis.fetch;
  let contacted = false;
  try {
    globalThis.fetch = (async () => {
      contacted = true;
      return { ok: true, status: 200, json: async () => ({}) } as any;
    }) as any;

    const res = await stageBootcImageSwitch(mockNode, '   ');
    assert.strictEqual(res.success, false);
    assert.ok(res.error && res.error.length > 0, 'Must surface a validation error');
    assert.strictEqual(contacted, false, 'Must NOT contact the daemon for an empty image ref');
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test('stageBootcImageSwitch compiles the bootc switch action and stages it in the queue', async () => {
  const originalFetch = globalThis.fetch;
  const calls: Array<{ url: string; method: string; body: any }> = [];
  try {
    globalThis.fetch = (async (input: any, init?: RequestInit) => {
      const url = String(input);
      calls.push({
        url,
        method: init?.method || 'GET',
        body: init?.body ? JSON.parse(String(init.body)) : null,
      });
      if (url.includes('/actions/switch')) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            name: 'bootc switch',
            steps: [
              {
                name: 'bootc switch',
                module: 'ansible.builtin.command',
                params: { cmd: 'bootc switch quay.io/acme/os:latest' },
              },
            ],
          }),
        } as any;
      }
      if (url === '/api/queue/task') {
        return { ok: true, status: 200, json: async () => ({ success: true, task: { id: 'task-bootc-1' } }) } as any;
      }
      return { ok: false, status: 404, json: async () => ({}) } as any;
    }) as any;

    const res = await stageBootcImageSwitch(mockNode, 'quay.io/acme/os:latest');
    assert.strictEqual(res.success, true);
    assert.ok(res.title && res.title.includes('quay.io/acme/os:latest'), 'Title must include the new image ref');

    const actionCall = calls.find(c => c.url.includes('/actions/switch'));
    assert.ok(actionCall, 'Must call the bootc switch action endpoint');
    assert.strictEqual(actionCall!.method, 'POST');
    assert.strictEqual(
      actionCall!.url,
      `/api/nodes/${encodeURIComponent(mockNode.id)}/plugins/package/bootc/actions/switch`
    );
    assert.strictEqual(actionCall!.body?.options?.image, 'quay.io/acme/os:latest');

    const queueCall = calls.find(c => c.url === '/api/queue/task');
    assert.ok(queueCall, 'Must stage the compiled task into the Ansible mutation queue');
    assert.strictEqual(queueCall!.method, 'POST');
    assert.strictEqual(queueCall!.body?.primaryModule, 'ansible.builtin.command');
    assert.strictEqual(queueCall!.body?.category, 'package');
    assert.strictEqual(queueCall!.body?.status, 'staged');
    assert.ok(
      typeof queueCall!.body?.title === 'string' && queueCall!.body.title.includes('quay.io/acme/os:latest'),
      'Staged task title must reference the new image'
    );
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test('stageBootcImageSwitch surfaces daemon compile errors honestly (no mock fallback)', async () => {
  const originalFetch = globalThis.fetch;
  try {
    globalThis.fetch = (async () => ({
      ok: false,
      status: 500,
      json: async () => ({ error: 'bootc binary not found on host' }),
    })) as any;

    const res = await stageBootcImageSwitch(mockNode, 'quay.io/acme/os:latest');
    assert.strictEqual(res.success, false);
    assert.ok(res.error && res.error.includes('bootc binary not found on host'), 'Must surface the real daemon error');
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test('stageBootcImageSwitch reports a queue staging failure honestly', async () => {
  const originalFetch = globalThis.fetch;
  try {
    globalThis.fetch = (async (input: any) => {
      const url = String(input);
      if (url.includes('/actions/switch')) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            name: 'bootc switch',
            steps: [
              { name: 'bootc switch', module: 'ansible.builtin.command', params: { cmd: 'bootc switch quay.io/acme/os:latest' } },
            ],
          }),
        } as any;
      }
      return { ok: false, status: 500, json: async () => ({ error: 'queue persistence unavailable' }) } as any;
    }) as any;

    const res = await stageBootcImageSwitch(mockNode, 'quay.io/acme/os:latest');
    assert.strictEqual(res.success, false);
    assert.ok(res.error && res.error.includes('queue persistence unavailable'), 'Must surface the queue error');
  } finally {
    globalThis.fetch = originalFetch;
  }
});
