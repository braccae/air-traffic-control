import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  DEFAULT_QUICK_COMMANDS,
  QUICK_COMMAND_STORAGE_KEY,
  QUICK_COMMAND_EVENT,
  loadQuickCommands,
  saveQuickCommands,
  addQuickCommand,
  updateQuickCommand,
  deleteQuickCommand,
  resetQuickCommands,
  type QuickCommandItem,
} from './quickCommandsManager.ts';

class MockStorage {
  private store = new Map<string, string>();

  getItem(key: string): string | null {
    return this.store.get(key) ?? null;
  }

  setItem(key: string, value: string): void {
    this.store.set(key, value);
  }

  removeItem(key: string): void {
    this.store.delete(key);
  }

  clear(): void {
    this.store.clear();
  }
}

class MockEventTarget {
  public dispatchedEvents: Array<{ type: string; detail?: unknown }> = [];

  dispatchEvent(event: Event): boolean {
    const detail = (event as CustomEvent).detail;
    this.dispatchedEvents.push({ type: event.type, detail });
    return true;
  }
}

describe('quickCommandsManager', () => {
  it('loads default quick commands when storage is empty', () => {
    const storage = new MockStorage();
    const commands = loadQuickCommands(storage);

    assert.equal(commands.length, DEFAULT_QUICK_COMMANDS.length);
    assert.equal(commands[0].label, 'htop');
    assert.equal(commands[0].cmd, 'htop');
    assert.equal(commands[1].label, 'podman ps');
    assert.equal(commands[1].cmd, 'podman ps');
    assert(commands.some((c) => c.label === 'ip a'));
    assert(commands.some((c) => c.label === 'df -h'));
  });

  it('loads saved commands from localStorage', () => {
    const storage = new MockStorage();
    const customList: QuickCommandItem[] = [
      {
        id: 'cmd-custom-1',
        label: 'zpool status',
        cmd: 'zpool status -v',
        description: 'Check ZFS storage pool health',
        category: 'storage',
      },
      {
        id: 'cmd-custom-2',
        label: 'tailscale status',
        cmd: 'tailscale status',
        description: 'Tailnet mesh node connectivity',
        category: 'network',
      },
    ];
    storage.setItem(QUICK_COMMAND_STORAGE_KEY, JSON.stringify(customList));

    const loaded = loadQuickCommands(storage);
    assert.equal(loaded.length, 2);
    assert.equal(loaded[0].label, 'zpool status');
    assert.equal(loaded[0].cmd, 'zpool status -v');
    assert.equal(loaded[0].category, 'storage');
    assert.equal(loaded[1].label, 'tailscale status');
  });

  it('falls back to defaults if stored data is corrupted JSON', () => {
    const storage = new MockStorage();
    storage.setItem(QUICK_COMMAND_STORAGE_KEY, '{not-valid-json');

    const loaded = loadQuickCommands(storage);
    assert.equal(loaded.length, DEFAULT_QUICK_COMMANDS.length);
    assert.equal(loaded[0].label, 'htop');
  });

  it('falls back to defaults if stored data is an empty array', () => {
    const storage = new MockStorage();
    storage.setItem(QUICK_COMMAND_STORAGE_KEY, '[]');

    const loaded = loadQuickCommands(storage);
    assert.equal(loaded.length, DEFAULT_QUICK_COMMANDS.length);
  });

  it('saves quick commands to storage and broadcasts custom event', () => {
    const storage = new MockStorage();
    const eventTarget = new MockEventTarget();

    const sample: QuickCommandItem[] = [
      { id: '1', label: 'uptime', cmd: 'uptime', category: 'system' },
    ];

    saveQuickCommands(sample, storage, eventTarget);

    assert.equal(
      storage.getItem(QUICK_COMMAND_STORAGE_KEY),
      JSON.stringify(sample)
    );
    assert.equal(eventTarget.dispatchedEvents.length, 1);
    assert.equal(eventTarget.dispatchedEvents[0].type, QUICK_COMMAND_EVENT);
    assert.deepEqual(eventTarget.dispatchedEvents[0].detail, sample);
  });

  it('adds a quick command with a generated unique ID', () => {
    const storage = new MockStorage();
    const eventTarget = new MockEventTarget();

    const updated = addQuickCommand(
      {
        label: 'docker ps',
        cmd: 'docker ps -a',
        description: 'Show all docker containers',
        category: 'containers',
      },
      storage,
      eventTarget
    );

    assert.equal(updated.length, DEFAULT_QUICK_COMMANDS.length + 1);
    const added = updated[updated.length - 1];
    assert.equal(added.label, 'docker ps');
    assert.equal(added.cmd, 'docker ps -a');
    assert.equal(added.description, 'Show all docker containers');
    assert.equal(added.category, 'containers');
    assert(typeof added.id === 'string' && added.id.length > 0);

    // Verify persisted
    const reloaded = loadQuickCommands(storage);
    assert.equal(reloaded.length, updated.length);
    assert.equal(reloaded[reloaded.length - 1].label, 'docker ps');
  });

  it('updates an existing quick command by id', () => {
    const storage = new MockStorage();
    const eventTarget = new MockEventTarget();

    // Start with defaults
    const initial = loadQuickCommands(storage);
    const targetId = initial[0].id; // 'qc-htop'

    const updated = updateQuickCommand(
      targetId,
      {
        label: 'htop (filtered)',
        cmd: 'htop -u root',
        description: 'View root processes only',
      },
      storage,
      eventTarget
    );

    assert.equal(updated.length, initial.length);
    const modified = updated.find((c) => c.id === targetId);
    assert(modified);
    assert.equal(modified.label, 'htop (filtered)');
    assert.equal(modified.cmd, 'htop -u root');
    assert.equal(modified.description, 'View root processes only');
    assert.equal(modified.category, 'system'); // Preserved original category

    // Verify stored
    const reloaded = loadQuickCommands(storage);
    const reloadedItem = reloaded.find((c) => c.id === targetId);
    assert.equal(reloadedItem?.cmd, 'htop -u root');
  });

  it('deletes a quick command by id', () => {
    const storage = new MockStorage();
    const eventTarget = new MockEventTarget();

    const initial = loadQuickCommands(storage);
    const targetId = initial[0].id;

    const updated = deleteQuickCommand(targetId, storage, eventTarget);
    assert.equal(updated.length, initial.length - 1);
    assert(!updated.some((c) => c.id === targetId));

    // Verify stored
    const reloaded = loadQuickCommands(storage);
    assert.equal(reloaded.length, initial.length - 1);
    assert(!reloaded.some((c) => c.id === targetId));
  });

  it('resets commands to homelab defaults', () => {
    const storage = new MockStorage();
    const eventTarget = new MockEventTarget();

    // Add a custom command first
    addQuickCommand(
      { label: 'custom', cmd: 'echo custom', category: 'custom' },
      storage,
      eventTarget
    );
    assert.equal(loadQuickCommands(storage).length, DEFAULT_QUICK_COMMANDS.length + 1);

    // Now reset
    const reset = resetQuickCommands(storage, eventTarget);
    assert.equal(reset.length, DEFAULT_QUICK_COMMANDS.length);
    assert.equal(reset[0].label, 'htop');

    // Reload check
    const reloaded = loadQuickCommands(storage);
    assert.equal(reloaded.length, DEFAULT_QUICK_COMMANDS.length);
  });
});
