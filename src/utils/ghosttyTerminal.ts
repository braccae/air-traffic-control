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

export interface GhosttyThemeDefinition {
  background: string;
  foreground: string;
  cursor: string;
  cursorAccent?: string;
  selectionBackground?: string;
  selectionForeground?: string;
  black?: string;
  red?: string;
  green?: string;
  yellow?: string;
  blue?: string;
  magenta?: string;
  cyan?: string;
  white?: string;
  brightBlack?: string;
  brightRed?: string;
  brightGreen?: string;
  brightYellow?: string;
  brightBlue?: string;
  brightMagenta?: string;
  brightCyan?: string;
  brightWhite?: string;
}

export const CATPPUCCIN_MACCHIATO_GHOSTTY: GhosttyThemeDefinition = {
  background: '#181926', // crust/surface lowest matching manager dark modal body
  foreground: '#cad3f5', // text
  cursor: '#8aadf4',     // blue
  cursorAccent: '#181926',
  selectionBackground: '#494d64', // surface1
  selectionForeground: '#cad3f5',
  black: '#494d64',
  red: '#ed8796',
  green: '#a6da95',
  yellow: '#eed49f',
  blue: '#8aadf4',
  magenta: '#c6a0f6',
  cyan: '#8bd5ca',
  white: '#b8c0e0',
  brightBlack: '#5b6078',
  brightRed: '#ed8796',
  brightGreen: '#a6da95',
  brightYellow: '#eed49f',
  brightBlue: '#8aadf4',
  brightMagenta: '#c6a0f6',
  brightCyan: '#8bd5ca',
  brightWhite: '#cad3f5',
};

export const CATPPUCCIN_LATTE_GHOSTTY: GhosttyThemeDefinition = {
  background: '#eff1f5', // base
  foreground: '#4c4f69', // text
  cursor: '#1e66f5',     // blue
  cursorAccent: '#eff1f5',
  selectionBackground: '#bcc0cc', // surface1
  selectionForeground: '#4c4f69',
  black: '#5c5f77',
  red: '#d20f39',
  green: '#40a02b',
  yellow: '#df8e1d',
  blue: '#1e66f5',
  magenta: '#8839ef',
  cyan: '#179299',
  white: '#acb0be',
  brightBlack: '#6c6f85',
  brightRed: '#d20f39',
  brightGreen: '#40a02b',
  brightYellow: '#df8e1d',
  brightBlue: '#1e66f5',
  brightMagenta: '#8839ef',
  brightCyan: '#179299',
  brightWhite: '#4c4f69',
};

export function getGhosttyTheme(resolvedTheme?: 'macchiato' | 'latte' | string | null): GhosttyThemeDefinition {
  return resolvedTheme === 'latte' ? CATPPUCCIN_LATTE_GHOSTTY : CATPPUCCIN_MACCHIATO_GHOSTTY;
}

