import { useState, useCallback, useRef } from 'react';
import type { DragPosition } from './useDraggable.ts';

export interface WindowSize {
  width: number;
  height: number;
}

export type ResizeDirection = 'n' | 's' | 'e' | 'w' | 'ne' | 'nw' | 'se' | 'sw';

export interface UseResizableOptions {
  initialSize?: WindowSize;
  minWidth?: number;
  minHeight?: number;
  maxWidth?: number;
  maxHeight?: number;
  onResizeEnd?: (size: WindowSize) => void;
  onPositionChange?: (pos: DragPosition) => void;
}

export function useResizable(options: UseResizableOptions = {}) {
  const {
    initialSize = { width: 840, height: 560 },
    minWidth = 520,
    minHeight = 320,
    maxWidth = typeof window !== 'undefined' ? window.innerWidth - 40 : 2560,
    maxHeight = typeof window !== 'undefined' ? window.innerHeight - 40 : 1440,
    onResizeEnd,
    onPositionChange,
  } = options;

  const [size, setSize] = useState<WindowSize>(initialSize);
  const [isResizing, setIsResizing] = useState(false);
  const resizeStateRef = useRef<{
    dir: ResizeDirection;
    startX: number;
    startY: number;
    startWidth: number;
    startHeight: number;
    startPosX: number;
    startPosY: number;
  }>({
    dir: 'se',
    startX: 0,
    startY: 0,
    startWidth: initialSize.width,
    startHeight: initialSize.height,
    startPosX: 0,
    startPosY: 0,
  });

  const startResize = useCallback(
    (
      dir: ResizeDirection,
      e: React.PointerEvent,
      currentPos: DragPosition
    ) => {
      setIsResizing(true);
      resizeStateRef.current = {
        dir,
        startX: e.clientX,
        startY: e.clientY,
        startWidth: size.width,
        startHeight: size.height,
        startPosX: currentPos.x,
        startPosY: currentPos.y,
      };

      try {
        (e.target as HTMLElement).setPointerCapture(e.pointerId);
      } catch {
        // fallback
      }
      e.stopPropagation();
      e.preventDefault();
    },
    [size]
  );

  const handlePointerMove = useCallback(
    (e: React.PointerEvent) => {
      if (!isResizing) return;
      const { dir, startX, startY, startWidth, startHeight, startPosX, startPosY } =
        resizeStateRef.current;
      const dx = e.clientX - startX;
      const dy = e.clientY - startY;

      let newWidth = startWidth;
      let newHeight = startHeight;
      let newPosX = startPosX;
      let newPosY = startPosY;

      if (dir.includes('e')) {
        newWidth = Math.max(minWidth, Math.min(maxWidth, startWidth + dx));
      }
      if (dir.includes('s')) {
        newHeight = Math.max(minHeight, Math.min(maxHeight, startHeight + dy));
      }
      if (dir.includes('w')) {
        const potentialWidth = startWidth - dx;
        if (potentialWidth >= minWidth && potentialWidth <= maxWidth) {
          newWidth = potentialWidth;
          newPosX = startPosX + dx;
        }
      }
      if (dir.includes('n')) {
        const potentialHeight = startHeight - dy;
        if (potentialHeight >= minHeight && potentialHeight <= maxHeight) {
          newHeight = potentialHeight;
          newPosY = startPosY + dy;
        }
      }

      setSize({ width: newWidth, height: newHeight });
      if (newPosX !== startPosX || newPosY !== startPosY) {
        onPositionChange?.({ x: newPosX, y: newPosY });
      }
    },
    [isResizing, minWidth, maxWidth, minHeight, maxHeight, onPositionChange]
  );

  const handlePointerUp = useCallback(
    (e: React.PointerEvent) => {
      if (!isResizing) return;
      setIsResizing(false);
      try {
        (e.target as HTMLElement).releasePointerCapture(e.pointerId);
      } catch {
        // fallback
      }
      onResizeEnd?.(size);
    },
    [isResizing, onResizeEnd, size]
  );

  return {
    size,
    setSize,
    isResizing,
    startResize,
    resizeProps: {
      onPointerMove: handlePointerMove,
      onPointerUp: handlePointerUp,
    },
  };
}
