/**
 * Clipboard helper used by context menus.
 *
 * Reports honestly: resolves true only when the browser confirmed the write, and
 * emits the app-wide toast event on failure so the operator sees real feedback
 * instead of a silent no-op.
 */

/** Canonical in-app toast event. Dispatched once; App listens for it. */
export const TOAST_EVENT_NAME = 'air_traffic_control_toast';

export interface ToastPayload {
  type: 'success' | 'error' | 'info' | 'warning';
  title: string;
  message?: string;
}

export function emitToast(payload: ToastPayload): void {
  if (typeof window === 'undefined' || typeof window.dispatchEvent !== 'function') return;
  try {
    window.dispatchEvent(new CustomEvent(TOAST_EVENT_NAME, { detail: payload }));
  } catch {
    // CustomEvent unavailable — nothing else to do.
  }
}

function fallbackCopy(text: string): boolean {
  if (typeof document === 'undefined') return false;
  try {
    const area = document.createElement('textarea');
    area.value = text;
    area.setAttribute('readonly', 'true');
    area.style.position = 'fixed';
    area.style.top = '-1000px';
    area.style.opacity = '0';
    document.body.appendChild(area);
    area.select();
    const ok = typeof document.execCommand === 'function' ? document.execCommand('copy') : false;
    document.body.removeChild(area);
    return ok;
  } catch {
    return false;
  }
}

/**
 * Copy `text` to the clipboard. `label` is used in the toast message.
 * Returns true only when the copy actually succeeded.
 */
export async function copyTextToClipboard(text: string, label = 'Value'): Promise<boolean> {
  if (!text) {
    emitToast({ type: 'warning', title: 'Nothing to copy', message: `${label} is empty.` });
    return false;
  }

  const clipboard = typeof navigator !== 'undefined' ? navigator.clipboard : undefined;

  if (clipboard && typeof clipboard.writeText === 'function') {
    try {
      await clipboard.writeText(text);
      emitToast({ type: 'success', title: 'Copied', message: `${label}: ${text}` });
      return true;
    } catch {
      // fall through to the legacy path below
    }
  }

  if (fallbackCopy(text)) {
    emitToast({ type: 'success', title: 'Copied', message: `${label}: ${text}` });
    return true;
  }

  emitToast({
    type: 'error',
    title: 'Copy Failed',
    message: `The browser refused clipboard access for ${label}.`,
  });
  return false;
}
