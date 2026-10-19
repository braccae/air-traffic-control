import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  createTerminalSession,
  closeTerminalSession,
  setSessionMinimized,
  toggleSessionMaximized,
  updateSessionPosition,
  updateSessionSize,
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
});
