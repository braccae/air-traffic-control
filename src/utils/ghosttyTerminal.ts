import type { NodeItem } from '../types/index.ts';

export interface TerminalInputState {
  buffer: string;
  history: string[];
  historyIndex: number;
}

export interface TerminalInputResult {
  nextState: TerminalInputState;
  echo?: string;
  submitCommand?: string;
  clearScreen?: boolean;
}

export function formatGhosttyBanner(node: NodeItem): string {
  const sshUser = node.sshUser || 'core';
  const lines = [
    `\x1b[1;36mAir Traffic Control v8.2-ansible\x1b[0m | \x1b[1;32mlibghostty-vt WASM Terminal Engine\x1b[0m`,
    `\x1b[38;5;250mTarget Host:\x1b[0m \x1b[1;37m${sshUser}@${node.tailscaleHost}\x1b[0m (${node.ip} / ${node.tailscaleIp})`,
    `\x1b[38;5;250mDistribution:\x1b[0m ${node.osDescription} | \x1b[38;5;250mAuth:\x1b[0m ${node.authMethod || 'ssh-ed25519 (/run/user/1000/ssh-agent.socket)'}`,
    `\x1b[38;5;244mType any shell command directly or click a Quick action above.\x1b[0m`,
    '',
  ];
  return lines.join('\r\n');
}

export function formatGhosttyPrompt(node: NodeItem): string {
  const sshUser = node.sshUser || 'core';
  return `\x1b[1;36m[${sshUser}@${node.shortName} ~]\x1b[0m$ `;
}

export function formatCommandOutputForGhostty(output: string): string {
  if (!output) return '';
  const normalized = output.replace(/\r?\n/g, '\r\n');
  return normalized.endsWith('\r\n') ? normalized : `${normalized}\r\n`;
}

export function applyTerminalInputKey(
  state: TerminalInputState,
  data: string
): TerminalInputResult {
  // Ctrl+C
  if (data === '\x03') {
    return {
      nextState: { ...state, buffer: '', historyIndex: -1 },
      echo: '^C\r\n',
      submitCommand: '',
    };
  }

  // Ctrl+L (clear)
  if (data === '\x0c') {
    return {
      nextState: { ...state, buffer: '', historyIndex: -1 },
      clearScreen: true,
    };
  }

  // Enter / Return
  if (data === '\r' || data === '\n') {
    const trimmed = state.buffer.trim();
    const nextHistory = trimmed ? [...state.history, trimmed] : state.history;
    return {
      nextState: {
        buffer: '',
        history: nextHistory,
        historyIndex: -1,
      },
      echo: '\r\n',
      submitCommand: trimmed,
    };
  }

  // Backspace (DEL \x7f or BS \x08)
  if (data === '\x7f' || data === '\b') {
    if (state.buffer.length === 0) {
      return { nextState: state };
    }
    return {
      nextState: {
        ...state,
        buffer: state.buffer.slice(0, -1),
      },
      echo: '\b \b',
    };
  }

  // Up Arrow (\x1b[A)
  if (data === '\x1b[A') {
    if (state.history.length === 0) return { nextState: state };
    const nextIdx =
      state.historyIndex === -1
        ? state.history.length - 1
        : Math.max(0, state.historyIndex - 1);
    const nextBuffer = state.history[nextIdx] || '';
    const eraseOld = '\b \b'.repeat(state.buffer.length);
    return {
      nextState: {
        ...state,
        buffer: nextBuffer,
        historyIndex: nextIdx,
      },
      echo: `${eraseOld}${nextBuffer}`,
    };
  }

  // Down Arrow (\x1b[B)
  if (data === '\x1b[B') {
    if (state.historyIndex === -1) return { nextState: state };
    const eraseOld = '\b \b'.repeat(state.buffer.length);
    if (state.historyIndex >= state.history.length - 1) {
      return {
        nextState: {
          ...state,
          buffer: '',
          historyIndex: -1,
        },
        echo: eraseOld,
      };
    }
    const nextIdx = state.historyIndex + 1;
    const nextBuffer = state.history[nextIdx] || '';
    return {
      nextState: {
        ...state,
        buffer: nextBuffer,
        historyIndex: nextIdx,
      },
      echo: `${eraseOld}${nextBuffer}`,
    };
  }

  // Ignore other escape sequences
  if (data.startsWith('\x1b')) {
    return { nextState: state };
  }

  // Printable characters (including pasted text without newlines)
  const clean = data.replace(/[\r\n]+/g, ' ');
  return {
    nextState: {
      ...state,
      buffer: state.buffer + clean,
    },
    echo: clean,
  };
}
