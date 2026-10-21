import React, { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import {
  clampMenuPosition,
  estimateMenuSize,
  type ContextMenuItem,
  type MenuPosition,
  type MenuSize,
  type ViewportSize,
} from '../utils/contextMenuModel.ts';
import { emitToast } from '../utils/clipboard.ts';

function readViewport(): ViewportSize {
  if (typeof window === 'undefined') return { width: 1280, height: 800 };
  return {
    width: window.innerWidth || document.documentElement?.clientWidth || 1280,
    height: window.innerHeight || document.documentElement?.clientHeight || 800,
  };
}

export interface ContextMenuProps {
  items: ContextMenuItem[];
  /** Preferred top-left corner in viewport coordinates (event.clientX/clientY). */
  position: MenuPosition;
  onClose: () => void;
  /** Accessible name for the menu. */
  label?: string;
}

/**
 * Reusable right-click menu panel.
 *
 * - position: clamped so the panel never overflows the viewport (measured, then clamped again).
 * - dismissal: Escape, click/tap outside, page scroll and window resize all close it.
 * - keyboard: ArrowUp/ArrowDown/Home/End move focus, Enter/Space activates, Tab closes.
 */
export const ContextMenu: React.FC<ContextMenuProps> = ({ items, position, onClose, label = 'Context menu' }) => {
  const panelRef = useRef<HTMLDivElement | null>(null);
  const [coords, setCoords] = useState<MenuPosition>(() =>
    clampMenuPosition(position, estimateMenuSize(items), readViewport())
  );

  // Re-clamp with the real measured size once the panel is in the DOM.
  useLayoutEffect(() => {
    const panel = panelRef.current;
    if (!panel) return;
    const rect = panel.getBoundingClientRect();
    const size: MenuSize = { width: rect.width || 0, height: rect.height || 0 };
    const next = clampMenuPosition(position, size, readViewport());
    setCoords(prev => (prev.x === next.x && prev.y === next.y ? prev : next));
  }, [items, position]);

  // Dismissal: outside pointer, Escape, scroll, resize.
  useEffect(() => {
    if (typeof document === 'undefined') return;

    const handlePointerDown = (event: Event) => {
      const panel = panelRef.current;
      if (panel && event.target instanceof Node && panel.contains(event.target)) return;
      onClose();
    };
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        onClose();
      }
    };
    const handleViewportChange = () => onClose();

    document.addEventListener('mousedown', handlePointerDown, true);
    document.addEventListener('touchstart', handlePointerDown, true);
    document.addEventListener('keydown', handleKeyDown, true);
    window.addEventListener('scroll', handleViewportChange, true);
    window.addEventListener('resize', handleViewportChange);

    return () => {
      document.removeEventListener('mousedown', handlePointerDown, true);
      document.removeEventListener('touchstart', handlePointerDown, true);
      document.removeEventListener('keydown', handleKeyDown, true);
      window.removeEventListener('scroll', handleViewportChange, true);
      window.removeEventListener('resize', handleViewportChange);
    };
  }, [onClose]);

  // Focus the first enabled item so the menu is usable from the keyboard immediately.
  useEffect(() => {
    const panel = panelRef.current;
    if (!panel) return;
    const first = panel.querySelector<HTMLButtonElement>('button[role="menuitem"]:not(:disabled)');
    first?.focus({ preventScroll: true });
  }, []);

  const focusables = useCallback((): HTMLButtonElement[] => {
    const panel = panelRef.current;
    if (!panel) return [];
    return Array.from(panel.querySelectorAll<HTMLButtonElement>('button[role="menuitem"]:not(:disabled)'));
  }, []);

  const handleMenuKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    const nodes = focusables();
    if (nodes.length === 0) return;
    const activeIndex = nodes.indexOf(document.activeElement as HTMLButtonElement);

    if (event.key === 'ArrowDown') {
      event.preventDefault();
      nodes[(activeIndex + 1 + nodes.length) % nodes.length]?.focus();
    } else if (event.key === 'ArrowUp') {
      event.preventDefault();
      const prev = activeIndex <= 0 ? nodes.length - 1 : activeIndex - 1;
      nodes[prev]?.focus();
    } else if (event.key === 'Home') {
      event.preventDefault();
      nodes[0]?.focus();
    } else if (event.key === 'End') {
      event.preventDefault();
      nodes[nodes.length - 1]?.focus();
    } else if (event.key === 'Tab') {
      onClose();
    }
  };

  const runItem = (item: ContextMenuItem) => {
    if (item.disabled) return;
    onClose();
    try {
      const result = item.onSelect();
      if (result && typeof (result as Promise<unknown>).then === 'function') {
        (result as Promise<unknown>).catch((error: unknown) => {
          emitToast({
            type: 'error',
            title: 'Action Failed',
            message: error instanceof Error ? error.message : String(error),
          });
        });
      }
    } catch (error) {
      emitToast({
        type: 'error',
        title: 'Action Failed',
        message: error instanceof Error ? error.message : String(error),
      });
    }
  };

  if (typeof document === 'undefined') return null;

  return createPortal(
    <div
      ref={panelRef}
      role="menu"
      aria-label={label}
      data-testid="context-menu"
      onKeyDown={handleMenuKeyDown}
      onContextMenu={event => event.preventDefault()}
      style={{
        position: 'fixed',
        top: `${coords.y}px`,
        left: `${coords.x}px`,
        zIndex: 1000,
        width: '236px',
        maxHeight: 'calc(100vh - 16px)',
        overflowY: 'auto',
      }}
      className="bg-[#171a2d] border border-[#35384c] rounded-md shadow-[0_8px_24px_rgba(0,0,0,0.6)] p-1 font-mono text-xs text-[#c3c6d2]"
    >
      {items.map(item => (
        <React.Fragment key={item.id}>
          {item.separatorBefore && <div className="my-1 border-t border-[#26293c]" role="separator" />}
          <button
            type="button"
            role="menuitem"
            tabIndex={-1}
            disabled={item.disabled}
            onClick={() => runItem(item)}
            className={`w-full flex items-center gap-2 px-2 py-1.5 rounded text-left transition-colors ${
              item.disabled
                ? 'opacity-40 cursor-default'
                : item.danger
                ? 'text-[#ffb4ab] hover:bg-[#ffb4ab]/15 cursor-pointer'
                : 'hover:bg-[#26293c] hover:text-[#dfe1fa] cursor-pointer'
            }`}
          >
            {item.icon && (
              <span className="material-symbols-outlined text-[15px] shrink-0 text-current">{item.icon}</span>
            )}
            <span className="flex-1 truncate">{item.label}</span>
            {item.hint && <span className="text-[10px] text-[#8d909b] truncate max-w-[70px]">{item.hint}</span>}
          </button>
        </React.Fragment>
      ))}
    </div>,
    document.body
  );
};

export interface ContextMenuController {
  /** Open the menu for the given items at an event position. */
  open: (event: React.MouseEvent | MouseEvent, items: ContextMenuItem[]) => void;
  /** Open the menu at explicit viewport coordinates. */
  openAt: (position: MenuPosition, items: ContextMenuItem[]) => void;
  close: () => void;
  isOpen: boolean;
  /** Render this next to the view's markup. */
  menu: React.ReactNode;
}

interface MenuState {
  items: ContextMenuItem[];
  position: MenuPosition;
  key: number;
}

/**
 * Per-view context menu controller. Views attach `onContextMenu={event => ctx.open(event, items)}`
 * on a row and render `{ctx.menu}` once inside their tree.
 */
export function useContextMenu(): ContextMenuController {
  const [state, setState] = useState<MenuState | null>(null);
  const openCount = useRef(0);

  const openAt = useCallback((position: MenuPosition, items: ContextMenuItem[]) => {
    if (!items || items.length === 0) return;
    openCount.current += 1;
    setState({ items, position, key: openCount.current });
  }, []);

  const open = useCallback(
    (event: React.MouseEvent | MouseEvent, items: ContextMenuItem[]) => {
      event.preventDefault();
      event.stopPropagation();
      openAt({ x: event.clientX, y: event.clientY }, items);
    },
    [openAt]
  );

  const close = useCallback(() => setState(null), []);

  const menu = state ? (
    <ContextMenu key={state.key} items={state.items} position={state.position} onClose={close} />
  ) : null;

  return { open, openAt, close, isOpen: Boolean(state), menu };
}
