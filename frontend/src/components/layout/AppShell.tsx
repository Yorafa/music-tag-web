import { useState, useCallback, useRef, useLayoutEffect, useEffect } from 'react';
import { Toolbar } from '@/components/layout/Toolbar';
import { FileBrowser } from '@/components/files/FileBrowser';
import { TagEditor } from '@/components/editor/TagEditor';
import { ScrapeResults } from '@/components/editor/ScrapeResults';
import { SearchResults } from '@/components/search/SearchResults';
import { useAppStore } from '@/store/useAppStore';
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { readNumber, readBool, writeNumber, writeBool } from '@/utils/persist';

interface Props {
  onLoadFiles: (path?: string) => void;
}

const MIN_PANEL_WIDTH = 1;
const HANDLE_WIDTH = 6; // matches w-1.5 (1.5 * 4px tailwind)
const RIGHT_WIDTH_KEY = 'appShell.rightWidth';
const TOOLBAR_COLLAPSED_KEY = 'appShell.toolbarCollapsed';
const DEFAULT_RIGHT_WIDTH = 360;

function ResizeHandle({ onResize, valueNow, min, max }: ResizeHandleProps) {
  const dragging = useRef(false);

  // Use Pointer Events to unify mouse + touch + pen input.
  // movementX is the per-event delta, so cursor backtracking truly reverses.
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

interface ResizeHandleProps {
  onResize: (delta: number) => void;
  valueNow: number;
  min: number;
  max: number;
}

export function AppShell({ onLoadFiles }: Props) {
  const { editorOpen, setEditorOpen, songList, updateMusicInfo } = useAppStore();
  const scrapeOpen = songList.length > 0;
  const containerRef = useRef<HTMLDivElement>(null);
  const [containerWidth, setContainerWidth] = useState(0);
  // Lazy-init from localStorage so first paint already matches the user's saved layout.
  const [rightWidth, setRightWidth] = useState<number>(
    () => readNumber(RIGHT_WIDTH_KEY) ?? DEFAULT_RIGHT_WIDTH,
  );
  const [toolbarCollapsed, setToolbarCollapsed] = useState<boolean>(
    () => readBool(TOOLBAR_COLLAPSED_KEY) ?? false,
  );

  // Debounce rightWidth persistence so a drag (which fires setState every
  // pixel) writes to localStorage only once at the trailing edge — avoids
  // hundreds of disk writes per second.
  useEffect(() => {
    const timer = setTimeout(() => {
      writeNumber(RIGHT_WIDTH_KEY, rightWidth);
    }, 250);
    return () => clearTimeout(timer);
  }, [rightWidth]);

  useEffect(() => {
    writeBool(TOOLBAR_COLLAPSED_KEY, toolbarCollapsed);
  }, [toolbarCollapsed]);

  useLayoutEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    setContainerWidth(el.clientWidth);
    const observer = new ResizeObserver(() => setContainerWidth(el.clientWidth));
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  return (
    <>
      <div ref={containerRef} className="flex-1 flex overflow-hidden">
        {/* Toolbar: fixed-width, collapsible (48 ↔ 180). No resize handle — width toggles via button. */}
        <Toolbar
          collapsed={toolbarCollapsed}
          onToggle={() => setToolbarCollapsed((v) => !v)}
        />

        {/* FileBrowser — fills remaining space */}
        <div className="flex-1 overflow-hidden bg-card/30">
          <FileBrowser onLoadFiles={onLoadFiles} />
        </div>

        {/* Single draggable handle — between FileBrowser (left of handle) and SearchResults (right of handle).
            Convention: drag right grows LEFT panel (FileBrowser). This shrinks rightWidth; midWidth is auto. */}
        <ResizeHandle
          onResize={(d) =>
            setRightWidth((w) => Math.max(MIN_PANEL_WIDTH, w - d))
          }
          valueNow={rightWidth}
          min={MIN_PANEL_WIDTH}
          max={containerWidth || rightWidth + 360}
        />

        {/* SearchResults — always-on current-directory song list; self-handles empty state. */}
        {/* Allow horizontal scroll if the user resizes narrower than the table's minimum width */}
        <div
          style={{ width: rightWidth }}
          className="shrink-0 overflow-x-auto overflow-y-hidden bg-card/20"
        >
          <SearchResults />
        </div>
      </div>

      <Dialog open={editorOpen} onOpenChange={setEditorOpen}>
        <DialogContent
          className={`
            ${scrapeOpen
              ? 'sm:max-w-[min(100rem,calc(100%-2rem))]'
              : 'sm:max-w-[min(64rem,calc(100%-2rem))]'
            }
            max-h-[92vh] overflow-hidden p-0
            transition-[max-width] duration-300
          `}
          style={{ maxWidth: scrapeOpen ? 'min(100rem, calc(100% - 2rem))' : 'min(64rem, calc(100% - 2rem))' }}
        >
          <div className="flex flex-col h-[92vh] overflow-hidden">
            <div className="px-6 pt-6 pb-3">
              <DialogHeader>
                <DialogTitle>
                  {scrapeOpen ? '歌曲详情 · 刮削结果' : '歌曲详情'}
                </DialogTitle>
              </DialogHeader>
            </div>
            <div className="flex-1 min-h-0 flex overflow-hidden">
              {/* Left: Tag Editor */}
              <div className={`flex-1 overflow-auto px-6 pb-6 ${scrapeOpen ? 'border-r border-border' : ''}`}>
                <TagEditor onLoadFiles={onLoadFiles} />
              </div>

              {/* Right: Scrape Results (conditional) */}
              {scrapeOpen && (
                <div className="w-[28rem] shrink-0 overflow-hidden">
                  <ScrapeResults
                    onApply={(field, value) => updateMusicInfo(field, value)}
                  />
                </div>
              )}
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
}
