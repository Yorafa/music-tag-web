import { useState, useCallback, useRef, useLayoutEffect } from 'react';
import { FileBrowser } from '@/components/files/FileBrowser';
import { TagEditor } from '@/components/editor/TagEditor';
import { SearchResults } from '@/components/search/SearchResults';
import { useAppStore } from '@/store/useAppStore';

interface Props {
  onLoadFiles: (path?: string) => void;
}

const MIN_PANEL_WIDTH = 1;
const HANDLE_WIDTH = 6; // matches w-1.5 (1.5 * 4px tailwind)

interface ResizeHandleProps {
  onResize: (delta: number) => void;
  /** Current panel width announced to assistive tech. */
  valueNow: number;
  /** Lower bound of width delta (0 means "can shrink to nothing"). */
  min: number;
  /** Upper bound (right edge of container width). */
  max: number;
}

function ResizeHandle({ onResize, valueNow, min, max }: ResizeHandleProps) {
  const dragging = useRef(false);

  // Use Pointer Events to unify mouse + touch + pen input.
  // movementX is the per-event delta, so cursor backtracking truly reverses
  // (avoids the cumulative-offset bug where delta = clientX - startX would
  // still grow the panel even while the user is moving leftward).
  const onPointerDown = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    e.preventDefault();
    const target = e.currentTarget;
    // Capture the pointer so we keep receiving moves even if the cursor
    // leaves the handle. Try/catch: throws on Safari <13 and iOS when the
    // pointer id is invalid mid-teardown.
    try { target.setPointerCapture(e.pointerId); } catch { /* noop */ }
    dragging.current = true;
    // Lock body so child elements with their own cursor / selectable text
    // don't glitch mid-drag. We always restore to default ('') on release,
    // so a missed mouseup can't strand us in drag mode.
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
    // Safety nets for browser interrupts where the OS steals focus before
    // pointerup fires (alt-tab, dialog, pointer ID becoming invalid).
    target.addEventListener('lostpointercapture', cleanup);
    target.addEventListener('pointercancel', cleanup);
  }, [onResize]);

  // Keyboard support: 8px per arrow, 32px with Shift.
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
      {/* Wider invisible hit region so the handle is grabbable on touch */}
      <div className="absolute inset-y-0 -left-1.5 -right-1.5" />
    </div>
  );
}

export function AppShell({ onLoadFiles }: Props) {
  const { fadeShowDetail, songList } = useAppStore();
  const containerRef = useRef<HTMLDivElement>(null);
  const [containerWidth, setContainerWidth] = useState(0);
  const [leftWidth, setLeftWidth] = useState(320);
  const [rightWidth, setRightWidth] = useState(360);

  // Track container width so middle panel can fill remaining space.
  useLayoutEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    setContainerWidth(el.clientWidth);
    const observer = new ResizeObserver(() => setContainerWidth(el.clientWidth));
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  // Middle panel absorbs whatever is left; floor at 1 so it's never negative.
  const midWidth = Math.max(
    MIN_PANEL_WIDTH,
    containerWidth - leftWidth - rightWidth - HANDLE_WIDTH * 2
  );

  return (
    <div ref={containerRef} className="flex-1 flex overflow-hidden">
      {/* Panel 1: File Browser — resize by left handle */}
      <div
        style={{ width: leftWidth }}
        className="shrink-0 overflow-auto bg-card/30"
      >
        <FileBrowser onLoadFiles={onLoadFiles} />
      </div>

      <ResizeHandle
        onResize={(d) =>
          setLeftWidth((w) => Math.max(MIN_PANEL_WIDTH, w + d))
        }
        valueNow={leftWidth}
        min={MIN_PANEL_WIDTH}
        max={containerWidth || leftWidth + midWidth + rightWidth + HANDLE_WIDTH * 2}
      />

      {/* Panel 2: Tag Editor — fills remaining space, auto-sized */}
      <div
        style={{ width: midWidth }}
        className="shrink-0 overflow-auto bg-card/20"
      >
        <TagEditor onLoadFiles={onLoadFiles} />
      </div>

      <ResizeHandle
        // Convention: handle is the right edge of the LEFT-adjacent panel.
        // For handle2, that's Panel 2 (middle). Dragging right shrinks
        // rightWidth so that midWidth (computed) grows by the same amount.
        onResize={(d) =>
          setRightWidth((w) => Math.max(MIN_PANEL_WIDTH, w - d))
        }
        valueNow={rightWidth}
        min={MIN_PANEL_WIDTH}
        max={containerWidth || leftWidth + midWidth + rightWidth + HANDLE_WIDTH * 2}
      />

      {/* Panel 3: Search Results — resize by right handle */}
      <div
        style={{ width: rightWidth }}
        className="shrink-0 overflow-auto"
      >
        {fadeShowDetail && songList.length > 0 ? (
          <SearchResults />
        ) : (
          <div className="flex items-center justify-center h-full text-muted-foreground">
            <div className="text-center">
              <div className="text-4xl mb-4">🎧</div>
              <p className="text-sm">选择文件后在标题栏点击搜索</p>
              <p className="text-xs mt-1 text-muted-foreground/60">或使用智能刮削自动匹配</p>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
