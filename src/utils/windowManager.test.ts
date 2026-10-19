import { test, describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  registerWindow,
  minimizeWindow,
  restoreWindow,
  toggleMinimizeWindow,
  closeWindow,
  updateWindowMeta,
  getMinimizedWindows,
  getOpenWindows,
  WindowManager,
  type ManagedWindow,
} from './windowManager.ts';

describe('WindowManager Unit Tests', () => {
  it('registers a new window with default flags and metadata', () => {
    const list: ManagedWindow[] = [];
    const updated = registerWindow(list, {
      id: 'term-1',
      title: 'likes-it-cold (100.105.71.49)',
      icon: 'terminal',
      type: 'terminal',
      meta: { nodeId: 'node-likes-it-cold' },
    });

    assert.equal(updated.length, 1);
    assert.equal(updated[0].id, 'term-1');
    assert.equal(updated[0].title, 'likes-it-cold (100.105.71.49)');
    assert.equal(updated[0].icon, 'terminal');
    assert.equal(updated[0].type, 'terminal');
    assert.equal(updated[0].isOpen, true);
    assert.equal(updated[0].isMinimized, false);
    assert.equal(updated[0].meta?.nodeId, 'node-likes-it-cold');
    assert.ok(typeof updated[0].createdAt === 'number');
  });

  it('updates existing window properties if registered again', () => {
    const initial: ManagedWindow[] = [
      {
        id: 'modal-edit',
        title: 'Edit: ping',
        icon: 'settings',
        type: 'modal',
        isOpen: false,
        isMinimized: true,
        createdAt: 1000,
      },
    ];

    const updated = registerWindow(initial, {
      id: 'modal-edit',
      title: 'Edit: ping (updated)',
      type: 'modal',
      isOpen: true,
      isMinimized: false,
    });

    assert.equal(updated.length, 1);
    assert.equal(updated[0].title, 'Edit: ping (updated)');
    assert.equal(updated[0].isOpen, true);
    assert.equal(updated[0].isMinimized, false);
  });

  it('minimizes an open window', () => {
    const list: ManagedWindow[] = [
      {
        id: 'term-1',
        title: 'Terminal 1',
        type: 'terminal',
        isOpen: true,
        isMinimized: false,
      },
      {
        id: 'modal-1',
        title: 'Playbook Editor',
        type: 'modal',
        isOpen: true,
        isMinimized: false,
      },
    ];

    const minimized = minimizeWindow(list, 'term-1');
    assert.equal(minimized[0].isMinimized, true);
    assert.equal(minimized[0].isOpen, true);
    assert.equal(minimized[1].isMinimized, false);
  });

  it('restores a minimized window', () => {
    const list: ManagedWindow[] = [
      {
        id: 'term-1',
        title: 'Terminal 1',
        type: 'terminal',
        isOpen: true,
        isMinimized: true,
      },
    ];

    const restored = restoreWindow(list, 'term-1');
    assert.equal(restored[0].isMinimized, false);
    assert.equal(restored[0].isOpen, true);
  });

  it('toggles minimize state back and forth', () => {
    let list: ManagedWindow[] = [
      {
        id: 'win-1',
        title: 'Window 1',
        type: 'modal',
        isOpen: true,
        isMinimized: false,
      },
    ];

    list = toggleMinimizeWindow(list, 'win-1');
    assert.equal(list[0].isMinimized, true);

    list = toggleMinimizeWindow(list, 'win-1');
    assert.equal(list[0].isMinimized, false);
  });

  it('closes a window by removing it from the list', () => {
    const list: ManagedWindow[] = [
      {
        id: 'win-1',
        title: 'Window 1',
        type: 'modal',
        isOpen: true,
        isMinimized: false,
      },
      {
        id: 'win-2',
        title: 'Window 2',
        type: 'terminal',
        isOpen: true,
        isMinimized: true,
      },
    ];

    const closed = closeWindow(list, 'win-1');
    assert.equal(closed.length, 1);
    assert.equal(closed[0].id, 'win-2');
  });

  it('updates metadata persistently without losing other properties', () => {
    const list: ManagedWindow<{ line: number; unsaved: boolean }>[] = [
      {
        id: 'playbook-editor',
        title: 'site.yml',
        type: 'modal',
        isOpen: true,
        isMinimized: false,
        meta: { line: 10, unsaved: false },
      },
    ];

    const updated = updateWindowMeta<{ line: number; unsaved: boolean }>(list, 'playbook-editor', { unsaved: true });
    assert.equal(updated[0].meta?.line, 10);
    assert.equal(updated[0].meta?.unsaved, true);
  });

  it('filters minimized windows and open windows accurately', () => {
    const list: ManagedWindow[] = [
      { id: '1', title: 'Open Active', type: 'terminal', isOpen: true, isMinimized: false },
      { id: '2', title: 'Open Minimized', type: 'modal', isOpen: true, isMinimized: true },
      { id: '3', title: 'Closed Window', type: 'modal', isOpen: false, isMinimized: true },
    ];

    const min = getMinimizedWindows(list);
    assert.equal(min.length, 1);
    assert.equal(min[0].id, '2');

    const open = getOpenWindows(list);
    assert.equal(open.length, 2);
    assert.deepEqual(open.map(w => w.id), ['1', '2']);
  });

  it('WindowManager class provides reactive subscription and methods', () => {
    const manager = new WindowManager([
      { id: 'init', title: 'Initial', type: 'terminal', isOpen: true, isMinimized: false },
    ]);

    let notificationCount = 0;
    let latestWindows: ManagedWindow[] = [];
    const unsubscribe = manager.subscribe((windows) => {
      notificationCount++;
      latestWindows = windows;
    });

    manager.register({ id: 'modal-1', title: 'Add Node', type: 'modal' });
    assert.equal(notificationCount, 1);
    assert.equal(latestWindows.length, 2);

    manager.minimize('modal-1');
    assert.equal(notificationCount, 2);
    assert.equal(manager.getMinimized().length, 1);
    assert.equal(manager.getMinimized()[0].id, 'modal-1');

    manager.restore('modal-1');
    assert.equal(notificationCount, 3);
    assert.equal(manager.getMinimized().length, 0);

    manager.close('modal-1');
    assert.equal(notificationCount, 4);
    assert.equal(manager.getWindows().length, 1);

    unsubscribe();
    manager.close('init');
    assert.equal(notificationCount, 4); // No new notification after unsubscribe
  });
});
