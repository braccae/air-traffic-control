import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import { renderToString } from 'react-dom/server';
import {
  LxcContainers,
  lxcAvailability,
  lxcUnavailableMessage,
  formatLxcSpec,
  type LxcReadResult,
} from './LxcContainers.tsx';

test('lxcAvailability reports unavailable when no libvirt host / null payload', () => {
  assert.equal(lxcAvailability(null), 'unavailable');
  assert.equal(lxcAvailability(undefined), 'unavailable');
  assert.equal(lxcAvailability({ hasLibvirt: false }), 'unavailable');
  assert.equal(
    lxcAvailability({ hasLibvirt: false, libvirtError: 'no libvirt LXC host' }),
    'unavailable'
  );
});

test('lxcAvailability reports empty (not fabricated) for a reachable host with no containers', () => {
  assert.equal(lxcAvailability({ hasLibvirt: true, containers: [] }), 'empty');
});

test('lxcAvailability reports ready only with real containers', () => {
  const data: LxcReadResult = {
    hasLibvirt: true,
    containers: [
      { name: 'web-01', id: '1234', status: 'RUNNING', vcpus: 2, ramMiB: 1024, ramGiB: 1, type: 'lxc' },
    ],
  };
  assert.equal(lxcAvailability(data), 'ready');
});

test('lxcUnavailableMessage prefers the daemon error string', () => {
  assert.equal(
    lxcUnavailableMessage({ hasLibvirt: false, libvirtError: 'error: failed to connect' }),
    'error: failed to connect'
  );
  assert.match(lxcUnavailableMessage({ hasLibvirt: false }), /No libvirt LXC host available/);
});

test('formatLxcSpec keeps unknown (-1) values explicit rather than inventing numbers', () => {
  assert.equal(
    formatLxcSpec({ name: 'c', id: '-', status: 'STOPPED', vcpus: -1, ramMiB: -1, ramGiB: -1, type: 'lxc' }),
    'vCPU unknown / RAM unknown'
  );
  assert.equal(
    formatLxcSpec({ name: 'c', id: '1', status: 'RUNNING', vcpus: 4, ramMiB: 2048, ramGiB: 2, type: 'lxc' }),
    '4 vCPU / 2 GiB'
  );
});

test('LxcContainers renders the LXC header and UNTESTED badge', () => {
  const html = renderToString(React.createElement(LxcContainers, { node: undefined }));
  assert.match(html, /Linux Containers \(LXC\)/);
  assert.match(html, /UNTESTED/);
});
