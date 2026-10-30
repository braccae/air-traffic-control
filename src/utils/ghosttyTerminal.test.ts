import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  formatGhosttyBanner,
  formatGhosttyPrompt,
  formatCommandOutputForGhostty,
  applyTerminalInputKey,
  getGhosttyTheme,
  CATPPUCCIN_MACCHIATO_GHOSTTY,
  CATPPUCCIN_LATTE_GHOSTTY,
  evaluateTerminalWSMessage,
  buildTerminalExecPayload,
} from './ghosttyTerminal.ts';
import type { NodeItem } from '../types/index.ts';

const sampleNode: NodeItem = {
  id: 'node-ping',
  name: 'ping',
  shortName: 'ping',
  group: 'tag:host',
  datacenter: 'leo-banjo.ts.net',
  nodeType: 'linux',
  badge: 'SSH',
  badgeColor: 'text-[#8cd6cb]',
  ip: '100.73.87.87',
  port: 22,
  sshUser: 'core',
  authMethod: 'ssh-ed25519 (/run/user/1000/ssh-agent.socket)',
  tags: ['tag:host'],
  sshConnected: true,
  rttMs: '2.1ms',
  tailscaleIp: '100.73.87.87',
  tailscaleHost: 'ping.leo-banjo.ts.net',
  osDescription: 'AlmaLinux 10.2 (Lavender Lion)',
  status: 'online',
  uptime: '27d 14h',
  hasGlances: true,
  hasCockpit: true,
  hasPodman: true,
  hasRestic: true,
  specs: {
    cpu: 'Intel(R) Core(TM) i5-6500 CPU @ 3.20GHz',
    vcpus: 4,
    ramGiB: 16,
    motherboard: 'Dell OptiPlex 7040',
    bios: 'UEFI',
    ipmi: '100.73.87.87',
    ecc: 'N/A',
    secureBoot: 'Enabled',
    iommu: 'Enabled',
  },
};

describe('libghostty (ghostty-web) terminal formatting and input state', () => {
  it('formats ANSI welcome banner and prompt for a target host', () => {
    const banner = formatGhosttyBanner(sampleNode);
    assert.ok(banner.includes('libghostty-vt'));
    assert.ok(banner.includes('core@ping.leo-banjo.ts.net'));
    assert.ok(banner.includes('\r\n'));

    const prompt = formatGhosttyPrompt(sampleNode);
    assert.ok(prompt.includes('core@ping'));
    assert.ok(prompt.endsWith('$ '));
  });

  it('normalizes LF to CRLF for libghostty VT sequences', () => {
    const formatted = formatCommandOutputForGhostty('line1\nline2\r\nline3');
    assert.equal(formatted, 'line1\r\nline2\r\nline3\r\n');
  });

  it('handles interactive line editing, backspace, history navigation, and enter submission', () => {
    let state = {
      buffer: '',
      history: ['uname -a', 'uptime'],
      historyIndex: -1,
    };

    // Type 'id'
    let step = applyTerminalInputKey(state, 'i');
    state = step.nextState;
    assert.equal(state.buffer, 'i');
    assert.equal(step.echo, 'i');

    step = applyTerminalInputKey(state, 'd');
    state = step.nextState;
    assert.equal(state.buffer, 'id');

    // Backspace (\x7f)
    step = applyTerminalInputKey(state, '\x7f');
    state = step.nextState;
    assert.equal(state.buffer, 'i');
    assert.equal(step.echo, '\b \b');

    // Complete 'id' and press Enter (\r)
    state = applyTerminalInputKey(state, 'd').nextState;
    step = applyTerminalInputKey(state, '\r');
    assert.equal(step.submitCommand, 'id');
    assert.equal(step.nextState.buffer, '');
    assert.deepEqual(step.nextState.history, ['uname -a', 'uptime', 'id']);
  });

  it('provides Catppuccin theme definitions for Ghostty matching app color scheme', () => {
    assert.equal(getGhosttyTheme('macchiato').background, '#181926');
    assert.equal(getGhosttyTheme('macchiato').foreground, '#cad3f5');
    assert.equal(getGhosttyTheme('latte').background, '#eff1f5');
    assert.equal(getGhosttyTheme('latte').foreground, '#4c4f69');
    assert.deepEqual(getGhosttyTheme(), CATPPUCCIN_MACCHIATO_GHOSTTY);
    assert.deepEqual(getGhosttyTheme('latte'), CATPPUCCIN_LATTE_GHOSTTY);
  });

  it('tracks PTY liveness across WebSocket frames and builds /api/terminal/exec payloads', () => {
    const okMsg = evaluateTerminalWSMessage(
      { type: 'terminal_data', sessionId: 'sess-1', data: 'core@dev-pc:~$ ' },
      'sess-1',
      false
    );
    assert.equal(okMsg.matchedSession, true);
    assert.equal(okMsg.ptyActive, true);
    assert.equal(okMsg.writeData, 'core@dev-pc:~$ ');
    assert.equal(okMsg.reprompt, false);

    // terminal_output is accepted as an alias for terminal_data.
    const aliasMsg = evaluateTerminalWSMessage(
      { type: 'terminal_output', sessionId: 'sess-1', data: 'ok' },
      'sess-1',
      true
    );
    assert.equal(aliasMsg.matchedSession, true);
    assert.equal(aliasMsg.writeData, 'ok');

    // A spawn error arrives as ordinary output but means the PTY is dead.
    const errMsg = evaluateTerminalWSMessage(
      { type: 'terminal_data', sessionId: 'sess-1', data: '\r\n[pty spawn error: dial tcp refused]\r\n' },
      'sess-1',
      true
    );
    assert.equal(errMsg.matchedSession, true);
    assert.equal(errMsg.ptyActive, false);
    assert.equal(errMsg.reprompt, true);

    const exitMsg = evaluateTerminalWSMessage({ type: 'terminal_exit', sessionId: 'sess-1', code: 1 }, 'sess-1', true);
    assert.equal(exitMsg.ptyActive, false);
    assert.equal(exitMsg.reprompt, true);

    // Frames for another session are ignored and never change local state.
    const otherSess = evaluateTerminalWSMessage({ type: 'terminal_exit', sessionId: 'sess-2', code: 0 }, 'sess-1', true);
    assert.equal(otherSess.matchedSession, false);
    assert.equal(otherSess.ptyActive, true);

    assert.deepEqual(buildTerminalExecPayload('  uname -a  ', 'node-dev-pc'), {
      command: 'uname -a',
      host: 'node-dev-pc',
      nodeId: 'node-dev-pc',
    });
  });
});
