import { useState, useCallback, useRef, useLayoutEffect, useEffect } from 'react';
import { Toolbar } from '@/components/layout/Toolbar';
import { FileBrowser } from '@/components/files/FileBrowser';
import { TagEditor } from '@/components/editor/TagEditor';
import { ScrapeResults } from '@/components/editor/ScrapeResults';
import { SearchResults } from '@/components/search/SearchResults';
import { SearchPanel } from '@/components/search/SearchPanel';
import { useAppStore } from '@/store/useAppStore';
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { readNumber, readBool, writeNumber, writeBool } from '@/utils/persist';
import { Sparkles, Search } from 'lucide-react';

interface Props {
  onLoadFiles: (path?: string) => void;
}

const MIN_PANEL_WIDTH = 1;
const RIGHT_WIDTH_KEY = 'appShell.rightWidth';
const SCRAPE_RIGHT_KEY = 'appShell.scrapeRightWidth';
const TOOLBAR_COLLAPSED_KEY = 'appShell.toolbarCollapsed';
const MAIN_TAB_KEY = 'appShell.mainTab';
const DEFAULT_RIGHT_WIDTH = 360;
const DEFAULT_SCRAPE_RIGHT = 448; // 28rem

function ResizeHandle({ onResize, valueNow, min, max }: ResizeHandleProps) {
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

interface ResizeHandleProps {
  onResize: (delta: number) => void;
  valueNow: number;
  min: number;
  max: number;
}

export function AppShell({ onLoadFiles }: Props) {
  const { editorOpen, setEditorOpen, songList, updateMusicInfo } = useAppStore();
  const scrapeHasResults = songList.length > 0;
  const [mainTab, setMainTabState] = useState<'scrape' | 'search'>(() => {
    // Back-compat: a stale saved value of 'library' (pre-removal tab) falls
    // through to 'scrape' rather than recreating the removed tab.
    const saved = localStorage.getItem(MAIN_TAB_KEY);
    return saved === 'search' ? 'search' : 'scrape';
  });

  const setMainTab = (tab: 'scrape' | 'search') => {
    setMainTabState(tab);
    localStorage.setItem(MAIN_TAB_KEY, tab);
  };
  const containerRef = useRef<HTMLDivElement>(null);
  const [containerWidth, setContainerWidth] = useState(0);
  const [rightWidth, setRightWidth] = useState<number>(
    () => readNumber(RIGHT_WIDTH_KEY) ?? DEFAULT_RIGHT_WIDTH,
  );
  const [scrapeRightWidth, setScrapeRightWidth] = useState<number>(
    () => readNumber(SCRAPE_RIGHT_KEY) ?? DEFAULT_SCRAPE_RIGHT,
  );
  const [toolbarCollapsed, setToolbarCollapsed] = useState<boolean>(
    () => readBool(TOOLBAR_COLLAPSED_KEY) ?? false,
  );

  useEffect(() => {
    const timer = setTimeout(() => {
      writeNumber(RIGHT_WIDTH_KEY, rightWidth);
    }, 250);
    return () => clearTimeout(timer);
  }, [rightWidth]);

  useEffect(() => {
    const timer = setTimeout(() => {
      writeNumber(SCRAPE_RIGHT_KEY, scrapeRightWidth);
    }, 250);
    return () => clearTimeout(timer);
  }, [scrapeRightWidth]);

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
      <div ref={containerRef} className="flex-1 flex flex-col overflow-hidden">
        {/* Tab bar */}
        <div className="flex items-center border-b border-border bg-card shrink-0 h-10">
          <div className="flex-1 flex items-center justify-center gap-1">
            <button
              className={`flex items-center gap-1.5 px-4 py-1.5 text-sm font-medium rounded-t-md transition-colors
                ${mainTab === 'scrape'
                  ? 'bg-background text-foreground border border-border border-b-0 -mb-px'
                  : 'text-muted-foreground hover:text-foreground hover:bg-muted/50'
                }`}
              onClick={() => setMainTab('scrape')}
            >
              <Sparkles className="w-3.5 h-3.5" />
              刮削
            </button>
            <button
              className={`flex items-center gap-1.5 px-4 py-1.5 text-sm font-medium rounded-t-md transition-colors
                ${mainTab === 'search'
                  ? 'bg-background text-foreground border border-border border-b-0 -mb-px'
                  : 'text-muted-foreground hover:text-foreground hover:bg-muted/50'
                }`}
              onClick={() => setMainTab('search')}
            >
              <Search className="w-3.5 h-3.5" />
              搜索
            </button>
          </div>
        </div>

        {/* Main content */}
        <div className="flex-1 flex overflow-hidden">
          {mainTab === 'scrape' ? (
            <>
              <Toolbar
                collapsed={toolbarCollapsed}
                onToggle={() => setToolbarCollapsed((v) => !v)}
              />
              <div className="flex-1 overflow-hidden bg-card/30">
                <FileBrowser onLoadFiles={onLoadFiles} />
              </div>
              <ResizeHandle
                onResize={(d) =>
                  setRightWidth((w) => Math.max(MIN_PANEL_WIDTH, w - d))
                }
                valueNow={rightWidth}
                min={MIN_PANEL_WIDTH}
                max={containerWidth || rightWidth + 360}
              />
              <div
                style={{ width: rightWidth }}
                className="shrink-0 overflow-x-auto overflow-y-hidden bg-card/20"
              >
                <SearchResults />
              </div>
            </>
          ) : (
            <div className="flex-1 overflow-hidden">
              <SearchPanel />
            </div>
          )}
        </div>
      </div>

      {/* Song detail Dialog — TagEditor + ScrapeResults dual panel */}
      <Dialog open={editorOpen} onOpenChange={setEditorOpen}>
        <DialogContent
          className={`
            ${scrapeHasResults
              ? 'sm:max-w-[min(100rem,calc(100%-2rem))]'
              : 'sm:max-w-[min(64rem,calc(100%-2rem))]'
            }
            max-h-[92vh] overflow-hidden p-0
            transition-[max-width] duration-300
          `}
          style={{ maxWidth: scrapeHasResults ? 'min(100rem, calc(100% - 2rem))' : 'min(64rem, calc(100% - 2rem))' }}
        >
          <div className="flex flex-col h-[92vh] overflow-hidden">
            <div className="px-6 pt-6 pb-3">
              <DialogHeader>
                <DialogTitle>
                  {scrapeHasResults ? '歌曲详情 · 刮削结果' : '歌曲详情'}
                </DialogTitle>
              </DialogHeader>
            </div>
            <div className="flex-1 min-h-0 flex overflow-hidden">
              <div className={`flex-1 overflow-auto px-6 pb-6 ${scrapeHasResults ? 'border-r border-border' : ''}`}>
                <TagEditor onLoadFiles={onLoadFiles} />
              </div>
              {scrapeHasResults && (
                <>
                  <ResizeHandle
                    onResize={(d) =>
                      setScrapeRightWidth((w) => Math.max(200, w - d))
                    }
                    valueNow={scrapeRightWidth}
                    min={200}
                    max={800}
                  />
                  <div
                    style={{ width: scrapeRightWidth }}
                    className="shrink-0 overflow-hidden"
                  >
                    <ScrapeResults
                      onApply={(field, value) => updateMusicInfo(field, value)}
                    />
                  </div>
                </>
              )}
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
}
