import { useState, useCallback, useRef, useEffect } from 'react';

export interface DragPosition {
  x: number;
  y: number;
}

export interface UseDraggableOptions {
  initialPosition?: DragPosition;
  minX?: number;
  minY?: number;
  maxX?: number;
  maxY?: number;
  disabled?: boolean;
  onDragEnd?: (pos: DragPosition) => void;
}

export function clampPosition(
  pos: DragPosition,
  minX = 0,
  minY = 0,
  maxX = typeof window !== 'undefined' ? window.innerWidth - 100 : 1800,
  maxY = typeof window !== 'undefined' ? window.innerHeight - 50 : 1000
): DragPosition {
  return {
    x: Math.max(minX, Math.min(maxX, pos.x)),
    y: Math.max(minY, Math.min(maxY, pos.y)),
  };
}

export function useDraggable(options: UseDraggableOptions = {}) {
  const {
    initialPosition = { x: 100, y: 80 },
    minX = 0,
    minY = 0,
    disabled = false,
    onDragEnd,
  } = options;

  const [position, setPosition] = useState<DragPosition>(initialPosition);
  const [isDragging, setIsDragging] = useState(false);
  const dragStartRef = useRef<{ startX: number; startY: number; posX: number; posY: number }>({
    startX: 0,
    startY: 0,
    posX: initialPosition.x,
    posY: initialPosition.y,
  });

  const handlePointerDown = useCallback(
    (e: React.PointerEvent) => {
      if (disabled) return;
      // Don't drag if clicking buttons, inputs, or interactive controls inside header
      const target = e.target as HTMLElement;
      if (
        target.tagName === 'BUTTON' ||
        target.tagName === 'INPUT' ||
        target.tagName === 'SELECT' ||
        target.closest('button') ||
        target.closest('[data-no-drag]')
      ) {
        return;
      }

      setIsDragging(true);
      dragStartRef.current = {
        startX: e.clientX,
        startY: e.clientY,
        posX: position.x,
        posY: position.y,
      };

      try {
        (e.target as HTMLElement).setPointerCapture(e.pointerId);
      } catch {
        // pointer capture fallback
      }
      e.stopPropagation();
    },
    [disabled, position]
  );

  const handlePointerMove = useCallback(
    (e: React.PointerEvent) => {
      if (!isDragging || disabled) return;
      const dx = e.clientX - dragStartRef.current.startX;
      const dy = e.clientY - dragStartRef.current.startY;

      const newPos = clampPosition(
        {
          x: dragStartRef.current.posX + dx,
          y: dragStartRef.current.posY + dy,
        },
        minX,
        minY,
        typeof window !== 'undefined' ? window.innerWidth - 150 : 1800,
        typeof window !== 'undefined' ? window.innerHeight - 80 : 1000
      );

      setPosition(newPos);
    },
    [isDragging, disabled, minX, minY]
  );

  const handlePointerUp = useCallback(
    (e: React.PointerEvent) => {
      if (!isDragging) return;
      setIsDragging(false);
      try {
        (e.target as HTMLElement).releasePointerCapture(e.pointerId);
      } catch {
        // fallback
      }
      onDragEnd?.(position);
    },
    [isDragging, onDragEnd, position]
  );

  return {
    position,
    setPosition,
    isDragging,
    dragProps: {
      onPointerDown: handlePointerDown,
      onPointerMove: handlePointerMove,
      onPointerUp: handlePointerUp,
    },
  };
}
