import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  createTerminalSession,
  closeTerminalSession,
  setSessionMinimized,
  toggleSessionMaximized,
  updateSessionPosition,
  updateSessionSize,
  formatSshTarget,
  TERMINAL_QUICK_COMMANDS,
} from './terminalSessionManager.ts';

describe('TerminalSessionManager', () => {
  it('creates a new terminal session with staggered coordinates', () => {
    const s1 = createTerminalSession('node-1', 'likes-it-cold', '100.105.71.49', undefined, 0);
    const s2 = createTerminalSession('node-2', 'ping', '100.73.87.87', undefined, 1);

    assert.equal(s1.nodeName, 'likes-it-cold');
    assert.equal(s1.isMinimized, false);
    assert.equal(s1.position.x, 80);
    assert.equal(s1.position.y, 60);

    assert.equal(s2.nodeName, 'ping');
    assert.equal(s2.position.x, 110);
    assert.equal(s2.position.y, 90);
  });

  it('closes a session and selects the next active session', () => {
    const s1 = createTerminalSession('n1', 'host1', '10.0.0.1');
    const s2 = createTerminalSession('n2', 'host2', '10.0.0.2');
    const list = [s1, s2];

    const { sessions, nextActiveId } = closeTerminalSession(list, s1.id, s1.id);
    assert.equal(sessions.length, 1);
    assert.equal(sessions[0].id, s2.id);
    assert.equal(nextActiveId, s2.id);

    const closedAll = closeTerminalSession(sessions, s2.id, s2.id);
    assert.equal(closedAll.sessions.length, 0);
    assert.equal(closedAll.nextActiveId, null);
  });

  it('minimizes and restores sessions', () => {
    const s = createTerminalSession('n1', 'host1', '10.0.0.1');
    const minimized = setSessionMinimized([s], s.id, true);
    assert.equal(minimized[0].isMinimized, true);

    const restored = setSessionMinimized(minimized, s.id, false);
    assert.equal(restored[0].isMinimized, false);
  });

  it('toggles maximized state', () => {
    const s = createTerminalSession('n1', 'host1', '10.0.0.1');
    const max = toggleSessionMaximized([s], s.id);
    assert.equal(max[0].isMaximized, true);

    const normal = toggleSessionMaximized(max, s.id);
    assert.equal(normal[0].isMaximized, false);
  });

  it('updates position and size', () => {
    const s = createTerminalSession('n1', 'host1', '10.0.0.1');
    const moved = updateSessionPosition([s], s.id, { x: 350, y: 220 });
    assert.deepEqual(moved[0].position, { x: 350, y: 220 });

    const resized = updateSessionSize(moved, s.id, { width: 1024, height: 768 });
    assert.deepEqual(resized[0].size, { width: 1024, height: 768 });
  });

  it('maintains independent isolation between multiple terminal sessions', () => {
    const s1 = createTerminalSession('n1', 'host1', '100.64.0.1', undefined, 0);
    const s2 = createTerminalSession('n2', 'host2', '100.64.0.2', undefined, 1);
    const s3 = createTerminalSession('n3', 'host3', '100.64.0.3', undefined, 2);
    let list = [s1, s2, s3];

    // Mutate position of session 2 only
    list = updateSessionPosition(list, s2.id, { x: 500, y: 400 });
    assert.deepEqual(list[0].position, s1.position);
    assert.deepEqual(list[1].position, { x: 500, y: 400 });
    assert.deepEqual(list[2].position, s3.position);

    // Mutate size of session 1 only
    list = updateSessionSize(list, s1.id, { width: 1200, height: 800 });
    assert.deepEqual(list[0].size, { width: 1200, height: 800 });
    assert.deepEqual(list[1].size, s2.size);
    assert.deepEqual(list[2].size, s3.size);

    // Minimize session 2 only
    list = setSessionMinimized(list, s2.id, true);
    assert.equal(list[0].isMinimized, false);
    assert.equal(list[1].isMinimized, true);
    assert.equal(list[2].isMinimized, false);

    // Maximize session 3 only
    list = toggleSessionMaximized(list, s3.id);
    assert.equal(list[0].isMaximized, false);
    assert.equal(list[1].isMaximized, false);
    assert.equal(list[2].isMaximized, true);
  });

  it('formats SSH target correctly with user fallback and node attributes', () => {
    // With explicit sshUser and tailscaleHost
    const target1 = formatSshTarget({
      sshUser: 'admin',
      tailscaleHost: 'node-alpha.leo-banjo.ts.net',
      ip: '192.168.1.100',
    });
    assert.equal(target1, 'admin@node-alpha.leo-banjo.ts.net');

    // With fallback sshUser ('core') and tailscaleIp
    const target2 = formatSshTarget({
      tailscaleIp: '100.80.90.10',
    });
    assert.equal(target2, 'core@100.80.90.10');

    // With session fallback host if node properties are missing
    const target3 = formatSshTarget(undefined, '100.70.60.50');
    assert.equal(target3, 'core@100.70.60.50');

    // Default localhost fallback
    const target4 = formatSshTarget();
    assert.equal(target4, 'core@localhost');
  });

  it('provides predefined quick commands for cockpit operators', () => {
    assert(TERMINAL_QUICK_COMMANDS.length >= 4);
    const labels = TERMINAL_QUICK_COMMANDS.map((qc) => qc.label);
    assert(labels.includes('htop'));
    assert(labels.includes('podman ps'));
    assert(labels.includes('journalctl'));
    assert(labels.includes('uptime'));

    for (const qc of TERMINAL_QUICK_COMMANDS) {
      assert(typeof qc.label === 'string' && qc.label.length > 0);
      assert(typeof qc.cmd === 'string' && qc.cmd.length > 0);
    }
  });
});
