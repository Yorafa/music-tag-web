import { useCallback, useRef } from 'react';

export interface ResizeHandleProps {
  onResize: (delta: number) => void;
  valueNow: number;
  min: number;
  max: number;
}

/** Vertical column resize handle. Mouse + touch + ARIA-keyboard on the same
 *  element. Used by both ScrapeView (Toolbar ↔ FileBrowser ↔ SearchResults)
 *  and LocalView (FileBrowser ↔ SearchPanel). Duplicates were the AppShell
 *  body inline copy; promote it so layout changes only happen in one place. */
export function ResizeHandle({ onResize, valueNow, min, max }: ResizeHandleProps) {
  const dragging = useRef(false);

  const onPointerDown = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    e.preventDefault();
    const target = e.currentTarget;
    try { target.setPointerCapture(e.pointerId); } catch { /* noop */ }
    dragging.current = true;
    document.body.style.userSelect = 'none';
    document.body.style.cursor = 'col-resize';

    const cleanup = () => {
      dragging.current = false;
      document.body.style.userSelect = '';
      document.body.style.cursor = '';
      document.removeEventListener('pointermove', onPointerMove);
      document.removeEventListener('pointerup', onPointerUp);
      target.removeEventListener('lostpointercapture', cleanup);
      target.removeEventListener('pointercancel', cleanup);
    };

    const onPointerMove = (ev: PointerEvent) => {
      if (!dragging.current) return;
      onResize(ev.movementX);
    };

    const onPointerUp = () => cleanup();
    document.addEventListener('pointermove', onPointerMove);
    document.addEventListener('pointerup', onPointerUp);
    target.addEventListener('lostpointercapture', cleanup);
    target.addEventListener('pointercancel', cleanup);
  }, [onResize]);

  const onKeyDown = useCallback((e: React.KeyboardEvent) => {
    const step = e.shiftKey ? 32 : 8;
    if (e.key === 'ArrowLeft') { onResize(-step); e.preventDefault(); }
    else if (e.key === 'ArrowRight') { onResize(step); e.preventDefault(); }
  }, [onResize]);

  return (
    <div
      onPointerDown={onPointerDown}
      onKeyDown={onKeyDown}
      role="separator"
      aria-orientation="vertical"
      aria-label="拖拽调节面板宽度"
      aria-valuenow={valueNow}
      aria-valuemin={min}
      aria-valuemax={max}
      tabIndex={0}
      className="w-1.5 shrink-0 cursor-col-resize bg-border hover:bg-primary/50 active:bg-primary transition-colors relative group touch-none select-none focus:outline-none focus-visible:bg-primary"
    >
      <div className="absolute inset-y-0 -left-1.5 -right-1.5" />
    </div>
  );
}
