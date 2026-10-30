import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import { renderToString } from 'react-dom/server';
import {
  ProxmoxGuestsPanel,
  ProxmoxActionToolbar,
  actionSpecForGuest,
  proxmoxGuestKey,
  proxmoxGuestLabel,
  type ProxmoxGuest,
} from './ProxmoxView.tsx';
import type { ActionSpec } from '../plugins/kit/index.ts';

// Guest data shaped exactly like GET /api/nodes/:id/plugins/vm/proxmox returns.
const guests: ProxmoxGuest[] = [
  {
    vmid: 101,
    name: 'pong-web',
    node: 'pong',
    type: 'qemu',
    status: 'running',
    maxMem: 2147483648,
    maxDisk: 34359738368,
    maxCpu: 4,
    uptime: 1000,
  },
  {
    vmid: 102,
    name: 'pong-db',
    node: 'pong',
    type: 'qemu',
    status: 'stopped',
    maxMem: 4294967296,
    maxDisk: 68719476736,
    maxCpu: 8,
    uptime: 0,
  },
];

const containers: ProxmoxGuest[] = [
  {
    vmid: 201,
    name: 'pong-ct',
    node: 'pong',
    type: 'lxc',
    status: 'running',
    maxMem: 536870912,
    maxDisk: 8589934592,
    maxCpu: 1,
    uptime: 500,
  },
];

const actions: ActionSpec[] = [
  {
    name: 'vm_start',
    label: 'Start VM',
    options: [
      { name: 'node', type: 'string', required: true },
      { name: 'vmid', type: 'int', required: true },
    ],
  },
  {
    name: 'ct_shutdown',
    label: 'Shutdown Container',
    options: [
      { name: 'node', type: 'string', required: true },
      { name: 'vmid', type: 'int', required: true },
    ],
  },
  {
    name: 'resize_disk',
    label: 'Resize Disk',
    options: [
      { name: 'node', type: 'string', required: true },
      { name: 'guestType', type: 'select', enum: ['qemu', 'lxc'], required: true },
      { name: 'vmid', type: 'int', required: true },
      { name: 'disk', type: 'string', required: true },
      { name: 'size', type: 'string', required: true },
    ],
  },
];

const readData = {
  endpoint: 'https://pong:8006',
  version: '8.2.4',
  release: '8.2',
  node: 'pong',
  vms: guests,
  containers,
  summary: { node: 'pong', nodes: 3, vms: 2, containers: 1, running: 2, excluded: 2, filteredTo: 'pong' },
  errors: [] as string[],
};

const includes = (html: string, needle: string) => html.includes(needle);

test('ProxmoxGuestsPanel hides the action toolbar until a guest is selected', () => {
  const html = renderToString(
    React.createElement(ProxmoxGuestsPanel, { data: readData, actions })
  );

  // No selection: no lifecycle buttons at all.
  assert.equal(includes(html, 'proxmox-action-toolbar'), false, 'toolbar must not render without a selection');
  assert.equal(includes(html, 'Start VM'), false, 'Start VM must be hidden without a selection');
  assert.equal(includes(html, 'Shutdown Container'), false, 'Shutdown Container must be hidden without a selection');
  assert.equal(includes(html, 'Resize Disk'), false, 'Resize Disk must be hidden without a selection');

  // The hint tells the operator what to do instead.
  assert.ok(includes(html, 'Select a guest to manage it'), 'must show the selection hint');

  // Guest rows are rendered and none is highlighted.
  assert.ok(includes(html, 'data-testid="proxmox-guest-qemu-101"'), 'must render the VM row');
  assert.ok(includes(html, 'data-testid="proxmox-guest-lxc-201"'), 'must render the container row');
  assert.equal(includes(html, 'data-selected="true"'), false, 'no row may be marked selected');
  assert.equal(includes(html, 'aria-selected="true"'), false, 'no row may be aria-selected');
});

test('ProxmoxGuestsPanel renders the action toolbar once a guest is selected', () => {
  const html = renderToString(
    React.createElement(ProxmoxGuestsPanel, {
      data: readData,
      actions,
      initialSelectedKey: proxmoxGuestKey(guests[0]),
    })
  );

  assert.ok(includes(html, 'data-testid="proxmox-action-toolbar"'), 'toolbar must render with a selection');
  assert.ok(includes(html, 'data-selected-vmid="101"'), 'toolbar must target the selected guest');
  assert.ok(includes(html, 'Start VM'), 'Start VM must be offered for the selected guest');
  assert.ok(includes(html, 'Shutdown Container'), 'container actions must be offered for the selected guest');
  assert.ok(includes(html, 'Resize Disk'), 'disk actions must be offered for the selected guest');
  assert.ok(includes(html, 'data-selected="true"'), 'the selected row must be highlighted');
  assert.equal(includes(html, 'Select a guest to manage it'), false, 'the hint must be replaced by the toolbar');
  assert.ok(includes(html, 'Clear'), 'there must be a way to clear the selection');
});

test('ProxmoxGuestsPanel selects containers too', () => {
  const html = renderToString(
    React.createElement(ProxmoxGuestsPanel, {
      data: readData,
      actions,
      initialSelectedKey: 'lxc-201',
    })
  );

  assert.ok(includes(html, 'data-testid="proxmox-action-toolbar"'));
  assert.ok(includes(html, 'data-selected-vmid="201"'));
  assert.ok(includes(html, 'aria-label="Actions for CT 201 · pong-ct"'), 'toolbar must name the selected container');
});

test('ProxmoxGuestsPanel drops a selection whose guest no longer exists', () => {
  const html = renderToString(
    React.createElement(ProxmoxGuestsPanel, {
      data: readData,
      actions,
      initialSelectedKey: 'qemu-999',
    })
  );

  assert.equal(includes(html, 'proxmox-action-toolbar'), false);
  assert.ok(includes(html, 'Select a guest to manage it'));
});

test('ProxmoxGuestsPanel shows no actions when the node has no guests', () => {
  const html = renderToString(
    React.createElement(ProxmoxGuestsPanel, {
      data: { ...readData, vms: [], containers: [] },
      actions,
    })
  );

  assert.equal(includes(html, 'proxmox-action-toolbar'), false);
  assert.equal(includes(html, 'Start VM'), false);
  assert.ok(includes(html, 'No guests on this node.'), 'must state that the node has no guests');
});

test('ProxmoxActionToolbar renders only the hint without a selection', () => {
  const html = renderToString(
    React.createElement(ProxmoxActionToolbar, {
      actions,
      selected: null,
      onAction: () => {},
      onClearSelection: () => {},
    })
  );

  assert.equal(includes(html, 'role="toolbar"'), false);
  assert.ok(includes(html, 'Select a guest to manage it'));
});

test('actionSpecForGuest seeds node, vmid and guestType without mutating the spec', () => {
  const resize = actions[2];
  const seeded = actionSpecForGuest(resize, containers[0]);

  assert.equal(seeded.name, 'resize_disk');
  const byName = Object.fromEntries((seeded.options || []).map(o => [o.name, o]));
  assert.equal(byName.node.default, 'pong');
  assert.equal(byName.vmid.default, 201);
  assert.equal(byName.guestType.default, 'lxc');

  // The shared action spec must stay untouched (it is reused across guests).
  const original = Object.fromEntries((resize.options || []).map(o => [o.name, o]));
  assert.equal(original.node.default, undefined);
  assert.equal(original.vmid.default, undefined);
});

test('actionSpecForGuest does not reuse the selected guest VMID for create actions', () => {
  const createAction: ActionSpec = {
    name: 'vm_create',
    label: 'Create VM',
    options: [
      { name: 'node', type: 'string', required: true },
      { name: 'vmid', type: 'int', required: true },
      { name: 'name', type: 'string', required: true },
    ],
  };
  const seeded = actionSpecForGuest(createAction, guests[0]);
  const byName = Object.fromEntries((seeded.options || []).map(o => [o.name, o]));
  assert.equal(byName.node.default, 'pong', 'create actions should still default to the node');
  assert.equal(byName.vmid.default, undefined, 'create actions must not inherit an existing VMID');
});

test('guest keys and labels identify a guest uniquely across types', () => {
  assert.equal(proxmoxGuestKey({ type: 'qemu', vmid: 101 }), 'qemu-101');
  assert.equal(proxmoxGuestKey({ type: 'lxc', vmid: 101 }), 'lxc-101');
  assert.equal(proxmoxGuestLabel(guests[0]), 'VM 101 · pong-web');
  assert.equal(proxmoxGuestLabel(containers[0]), 'CT 201 · pong-ct');
});
