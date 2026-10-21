import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import { renderToString } from 'react-dom/server';
import { ContextMenu } from './ContextMenu.tsx';
import { ServerViewSidebar } from './ServerViewSidebar.tsx';
import {
  clampMenuPosition,
  estimateMenuSize,
  isContainerRunning,
  MENU_VIEWPORT_MARGIN_PX,
  buildNodeContextMenu,
  buildContainerContextMenu,
  buildTaskContextMenu,
  buildShareContextMenu,
  type ContextMenuItem,
} from '../utils/contextMenuModel.ts';
import type { NodeItem } from '../types/index.ts';

const node: NodeItem = {
  id: 'node-likes-it-cold',
  name: 'likes-it-cold',
  shortName: 'likes-it-cold',
  group: 'tag:host',
  datacenter: 'leo-banjo.ts.net',
  nodeType: 'linux',
  ip: '192.168.8.249',
  port: 22,
  sshUser: 'core',
  tailscaleIp: '100.105.71.49',
  tailscaleHost: 'likes-it-cold.leo-banjo.ts.net',
  osDescription: 'AlmaLinux 10.2',
  status: 'online',
  uptime: '3 days',
  hasGlances: false,
  hasCockpit: true,
  hasPodman: true,
  hasRestic: true,
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
} as NodeItem;

const item = (overrides: Partial<ContextMenuItem> = {}): ContextMenuItem => ({
  id: 'x',
  label: 'X',
  onSelect: () => {},
  ...overrides,
});

// ---------------------------------------------------------------------------
// Positioning
// ---------------------------------------------------------------------------

test('clampMenuPosition keeps a menu that fits inside the viewport', () => {
  const pos = clampMenuPosition({ x: 120, y: 90 }, { width: 236, height: 200 }, { width: 1280, height: 800 });
  assert.deepEqual(pos, { x: 120, y: 90 });
});

test('clampMenuPosition flips a menu that would overflow the right/bottom edges', () => {
  const viewport = { width: 800, height: 600 };
  const size = { width: 236, height: 300 };
  const pos = clampMenuPosition({ x: 790, y: 590 }, size, viewport);

  assert.equal(pos.x, viewport.width - size.width - MENU_VIEWPORT_MARGIN_PX);
  assert.equal(pos.y, viewport.height - size.height - MENU_VIEWPORT_MARGIN_PX);
  assert.ok(pos.x + size.width + MENU_VIEWPORT_MARGIN_PX <= viewport.width);
  assert.ok(pos.y + size.height + MENU_VIEWPORT_MARGIN_PX <= viewport.height);
});

test('clampMenuPosition never returns negative coordinates near the top-left corner', () => {
  const pos = clampMenuPosition({ x: -40, y: -12 }, { width: 236, height: 100 }, { width: 800, height: 600 });
  assert.equal(pos.x, MENU_VIEWPORT_MARGIN_PX);
  assert.equal(pos.y, MENU_VIEWPORT_MARGIN_PX);
});

test('clampMenuPosition falls back to the margin when the menu is taller than the viewport', () => {
  const pos = clampMenuPosition({ x: 400, y: 900 }, { width: 236, height: 2000 }, { width: 800, height: 600 });
  assert.equal(pos.y, MENU_VIEWPORT_MARGIN_PX);
});

test('estimateMenuSize grows with item count and separators', () => {
  const two = estimateMenuSize([item(), item()]);
  const three = estimateMenuSize([item(), item(), item({ separatorBefore: true })]);
  assert.ok(three.height > two.height);
  assert.equal(two.width, 236);
});

// ---------------------------------------------------------------------------
// Node rows
// ---------------------------------------------------------------------------

test('node menu exposes terminal/edit/delete plus copy actions wired to existing handlers', () => {
  const calls: string[] = [];
  const menu = buildNodeContextMenu(node, {
    onSelect: n => calls.push(`select:${n.id}`),
    onOpenTerminal: n => calls.push(`terminal:${n.id}`),
    onOpenLogs: n => calls.push(`logs:${n.id}`),
    onEdit: n => calls.push(`edit:${n.id}`),
    onDelete: n => calls.push(`delete:${n.id}`),
    onCopy: (text, label) => calls.push(`copy:${label}=${text}`),
  });

  const ids = menu.map(m => m.id);
  assert.ok(ids.includes('node.terminal'), 'node menu must offer Open Terminal');
  assert.ok(ids.includes('node.edit'));
  assert.ok(ids.includes('node.delete'));
  assert.ok(ids.includes('node.copyIp'));

  const byId = new Map(menu.map(m => [m.id, m]));
  byId.get('node.terminal')!.onSelect();
  byId.get('node.edit')!.onSelect();
  byId.get('node.copyIp')!.onSelect();

  assert.deepEqual(calls, [
    'terminal:node-likes-it-cold',
    'edit:node-likes-it-cold',
    'copy:IP Address=100.105.71.49',
  ]);
});

test('node menu hides actions the view did not provide and disables empty copy targets', () => {
  const menu = buildNodeContextMenu({ id: 'n1', name: 'n1' }, { onCopy: () => {} });
  const ids = menu.map(m => m.id);
  assert.ok(!ids.includes('node.terminal'));
  assert.ok(!ids.includes('node.edit'));
  const copyId = menu.find(m => m.id === 'node.copyIp');
  assert.equal(copyId?.disabled, true);
});

test('node menu marks removal as destructive', () => {
  const menu = buildNodeContextMenu(node, { onDelete: () => {}, onCopy: () => {} });
  assert.equal(menu.find(m => m.id === 'node.delete')?.danger, true);
});

// ---------------------------------------------------------------------------
// Container rows
// ---------------------------------------------------------------------------

test('container menu start/stop/restart reflect live container status', () => {
  const running = buildContainerContextMenu(
    { id: 'abc123', name: 'uosserver', image: 'localhost/uos', status: 'Up 4 hours', user: 'core', uid: 1001 },
    { onStart: () => {}, onStop: () => {}, onRestart: () => {}, onExec: () => {}, onLogs: () => {} }
  );
  assert.equal(running.find(i => i.id === 'container.start')?.disabled, true);
  assert.equal(running.find(i => i.id === 'container.stop')?.disabled, false);
  assert.equal(running.find(i => i.id === 'container.restart')?.disabled, false);

  const stopped = buildContainerContextMenu({ id: 'def456', name: 'old', status: 'Exited (0) 2 days ago' }, {
    onStart: () => {},
    onStop: () => {},
    onRestart: () => {},
  });
  assert.equal(stopped.find(i => i.id === 'container.start')?.disabled, false);
  assert.equal(stopped.find(i => i.id === 'container.stop')?.disabled, true);
  assert.equal(stopped.find(i => i.id === 'container.restart')?.disabled, true);
});

test('container menu routes actions to the row container', () => {
  const events: string[] = [];
  const container = { id: 'abc123', name: 'uosserver', image: 'localhost/uos', status: 'Up 4 hours', user: 'core', uid: 1001 };
  const menu = buildContainerContextMenu(container, {
    onStart: c => events.push(`start:${c.name}`),
    onStop: c => events.push(`stop:${c.name}`),
    onRestart: c => events.push(`restart:${c.name}`),
    onExec: c => events.push(`exec:${c.name}`),
    onLogs: c => events.push(`logs:${c.name}`),
    onCopy: (text, label) => events.push(`copy:${label}=${text}`),
  });

  menu.find(i => i.id === 'container.restart')!.onSelect();
  menu.find(i => i.id === 'container.logs')!.onSelect();
  menu.find(i => i.id === 'container.copyId')!.onSelect();

  assert.deepEqual(events, [
    'restart:uosserver',
    'logs:uosserver',
    'copy:Container ID=abc123',
  ]);
});

test('isContainerRunning understands the statuses the probe emits', () => {
  assert.equal(isContainerRunning('Up 3 hours'), true);
  assert.equal(isContainerRunning('Exited (1) 5 minutes ago'), false);
  assert.equal(isContainerRunning('stopped'), false);
  assert.equal(isContainerRunning(undefined), false);
});

// ---------------------------------------------------------------------------
// Task + share rows
// ---------------------------------------------------------------------------

test('task menu opens the real task-detail handler and copies row fields', () => {
  const events: string[] = [];
  const task = { id: 'task-42', description: 'podman action: uosserver', nodeTarget: 'likes-it-cold.leo-banjo.ts.net' };
  const menu = buildTaskContextMenu(task, {
    onOpenDetails: t => events.push(`details:${t.id}`),
    onCopy: (text, label) => events.push(`copy:${label}=${text}`),
  });

  menu.find(i => i.id === 'task.details')!.onSelect();
  menu.find(i => i.id === 'task.copyTarget')!.onSelect();

  assert.deepEqual(events, [
    'details:task-42',
    'copy:Target Host=likes-it-cold.leo-banjo.ts.net',
  ]);
});

test('share menu reuses the share editor handler and exposes the exported path', () => {
  const events: string[] = [];
  const share = { id: 'nfs-media', name: 'media', protocol: 'NFS Export', path: '/srv/media' };
  const menu = buildShareContextMenu(share, {
    onEdit: s => events.push(`edit:${s.name}`),
    onRefresh: () => events.push('refresh'),
    onCopy: (text, label) => events.push(`copy:${label}=${text}`),
  });

  menu.find(i => i.id === 'share.edit')!.onSelect();
  menu.find(i => i.id === 'share.refresh')!.onSelect();
  menu.find(i => i.id === 'share.copyPath')!.onSelect();

  assert.deepEqual(events, ['edit:media', 'refresh', 'copy:Exported Path=/srv/media']);
});

// ---------------------------------------------------------------------------
// Rendering + wiring
// ---------------------------------------------------------------------------

test('ContextMenu renders nothing during server-side rendering (no document)', () => {
  const html = renderToString(
    React.createElement(ContextMenu, {
      items: [item({ id: 'a', label: 'Alpha' })],
      position: { x: 10, y: 10 },
      onClose: () => {},
    })
  );
  assert.equal(html, '');
});

test('sidebar node rows carry the shared context-menu hook', () => {
  const html = renderToString(
    React.createElement(ServerViewSidebar, {
      nodes: [node],
      selectedNodeId: node.id,
      onSelectNode: () => {},
      onEditNode: () => {},
      onOpenTerminal: () => {},
      onOpenLogs: () => {},
      onDeleteNode: () => {},
      selectedHostGroup: 'all',
      onSelectHostGroup: () => {},
    })
  );

  const occurrences = html.split('data-context-menu="node-row"').length - 1;
  assert.equal(occurrences, 1, 'each rendered node row must opt into the shared context menu');
});
