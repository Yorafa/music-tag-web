// Unified modern app shell. Supports 5 core navigation sections:
//   - library:  Local library tracks, playback, filter, instant editor
//   - scraper:  Worklist queue, auto-scraping, filename parser, folder tidy
//   - search:   Cloud multi-source search, preview stream & download
//   - audit:    Operation history logs, action/status filters & details
//   - settings: General paths, music sources manager & hot reload
//
// Hosts global Dialog for TagEditor/ScrapeResults, bottom PlayerBar,
// mobile navigation sheet, and ToastHost.

import { useEffect, useState, useCallback, useRef } from 'react';
import { TagEditor } from '@/components/editor/TagEditor';
import { ScrapeResults } from '@/components/editor/ScrapeResults';
import { WorkstationView } from '@/components/workstation/WorkstationView';
import { PlayView } from '@/components/play/PlayView';
import { CloudSearchView } from '@/components/search/CloudSearchView';
import { AuditLogView } from '@/components/audit/AuditLogView';
import { SettingsView } from '@/components/settings/SettingsView';
import { PlayerBar } from '@/components/player/PlayerBar';
import { ToastHost } from '@/components/common/ToastHost';
import { Sidebar, type NavSection } from '@/components/layout/Sidebar';
import { TopHeader } from '@/components/layout/TopHeader';
import { useEditorStore, editorActions } from '@/store/useEditorStore';
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { ResizeHandle } from '@/components/layout/ResizeHandle';
import { readNumber, writeNumber, readBool, writeBool } from '@/utils/persist';
import {
  SCRAPE_RIGHT_MIN,
  applyResize,
  resolveSplitLayout,
  sanitizeStoredWidth,
} from '@/components/layout/splitLayout';
import { cn } from '@/lib/utils';
import { X } from 'lucide-react';

export type AppSection = NavSection;
export type AppMode = 'play' | 'scrape'; // Backward compat

interface Props {
  initialSection?: AppSection;
}

const SECTION_KEY = 'appShell.section';
const SIDEBAR_COLLAPSED_KEY = 'appShell.sidebarCollapsed';
const SCRAPE_RIGHT_KEY = 'appShell.scrapeRightWidth';

function readStoredSection(): AppSection {
  try {
    const s = localStorage.getItem(SECTION_KEY);
    if (s === 'library' || s === 'scraper' || s === 'search' || s === 'audit' || s === 'settings') {
      return s;
    }
    // Backward compatibility for mode
    const mode = localStorage.getItem('appShell.mode');
    if (mode === 'scrape') return 'scraper';
    if (mode === 'play') return 'library';
  } catch {
    /* ignore */
  }
  return 'scraper';
}

export function AppShell({ initialSection }: Props) {
  const [section, setSectionState] = useState<AppSection>(() => initialSection ?? readStoredSection());
  const [sidebarCollapsed, setSidebarCollapsed] = useState<boolean>(() =>
    readBool(SIDEBAR_COLLAPSED_KEY) ?? false,
  );
  const [mobileNavOpen, setMobileNavOpen] = useState(false);

  const editorOpen = useEditorStore((s) => s.editorOpen);
  const songList = useEditorStore((s) => s.songList);
  const scrapeHasResults = songList.length > 0;

  const setSection = useCallback((next: AppSection) => {
    setSectionState(next);
    setMobileNavOpen(false);
    try {
      localStorage.setItem(SECTION_KEY, next);
      localStorage.setItem('appShell.mode', next === 'scraper' ? 'scrape' : 'play');
    } catch {
      /* ignore */
    }
  }, []);

  const toggleSidebar = useCallback(() => {
    setSidebarCollapsed((prev) => {
      const next = !prev;
      writeBool(SIDEBAR_COLLAPSED_KEY, next);
      return next;
    });
  }, []);

  const [scrapeRightWidth, setScrapeRightWidth] = useState<number>(() =>
    sanitizeStoredWidth(readNumber(SCRAPE_RIGHT_KEY)),
  );
  useEffect(() => {
    const timer = setTimeout(
      () => writeNumber(SCRAPE_RIGHT_KEY, scrapeRightWidth),
      250,
    );
    return () => clearTimeout(timer);
  }, [scrapeRightWidth]);

  // Measure the dialog's split row so the panel width can be capped
  // against what is actually available, not just against an absolute
  // maximum. Without this, shrinking the window (or opening the dialog
  // on a smaller display) left a 448px panel inside a 400px dialog and
  // the editor column collapsed to nothing.
  const splitRef = useRef<HTMLDivElement>(null);
  const [splitWidth, setSplitWidth] = useState(0);
  useEffect(() => {
    const el = splitRef.current;
    if (!el) return;
    const measure = () => setSplitWidth(el.clientWidth);
    measure();
    if (typeof ResizeObserver === 'undefined') return;
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [editorOpen, scrapeHasResults]);

  const split = resolveSplitLayout(splitWidth, scrapeRightWidth);

  return (
    <div className="flex h-screen w-screen overflow-hidden bg-background text-foreground">
      {/* Desktop Navigation Sidebar */}
      <div className="hidden md:flex shrink-0 h-full">
        <Sidebar
          activeSection={section}
          onSelectSection={setSection}
          collapsed={sidebarCollapsed}
          onToggleCollapse={toggleSidebar}
        />
      </div>

      {/* Mobile Navigation Drawer Overlay */}
      {mobileNavOpen && (
        <div className="fixed inset-0 z-50 md:hidden flex">
          <div
            className="fixed inset-0 bg-black/60 backdrop-blur-xs transition-opacity"
            onClick={() => setMobileNavOpen(false)}
          />
          <div className="relative w-64 h-full z-50 flex flex-col shadow-2xl animate-in slide-in-from-left duration-200">
            <button
              type="button"
              onClick={() => setMobileNavOpen(false)}
              className="absolute right-2.5 top-3 z-50 w-7 h-7 rounded-full bg-muted/80 text-muted-foreground hover:text-foreground flex items-center justify-center"
              aria-label="关闭导航"
            >
              <X className="w-4 h-4" />
            </button>
            <Sidebar
              activeSection={section}
              onSelectSection={setSection}
              collapsed={false}
              onToggleCollapse={() => setMobileNavOpen(false)}
            />
          </div>
        </div>
      )}

      {/* Main Content Workspace Container */}
      <div className="flex-1 flex flex-col min-w-0 h-full overflow-hidden">
        {/* Top Header */}
        <TopHeader
          activeSection={section}
          onOpenMobileNav={() => setMobileNavOpen(true)}
        />

        {/* Dynamic Section Viewport */}
        <div className="flex-1 flex flex-col min-h-0 overflow-hidden relative">
          {section === 'library' && <PlayView />}
          {section === 'scraper' && <WorkstationView />}
          {section === 'search' && <CloudSearchView />}
          {section === 'audit' && <AuditLogView />}
          {section === 'settings' && <SettingsView />}
        </div>

        {/* Persistent Bottom Player Bar */}
        <PlayerBar />
      </div>

      {/* Song detail Dialog — TagEditor + ScrapeResults dual panel */}
      <Dialog open={editorOpen} onOpenChange={editorActions.setEditorOpen}>
        <DialogContent
          className={cn(
            scrapeHasResults
              ? 'sm:max-w-[min(100rem,calc(100%-2rem))]'
              : 'sm:max-w-[min(64rem,calc(100%-2rem))]',
            'max-h-[92vh] overflow-hidden p-0 transition-[max-width] duration-300',
          )}
          style={{
            maxWidth: scrapeHasResults
              ? 'min(100rem, calc(100% - 2rem))'
              : 'min(64rem, calc(100% - 2rem))',
          }}
        >
          <div className="flex flex-col h-[92vh] overflow-hidden">
            <div className="px-6 pt-6 pb-3">
              <DialogHeader>
                <DialogTitle>
                  {scrapeHasResults ? '歌曲详情 · 刮削结果' : '歌曲详情'}
                </DialogTitle>
              </DialogHeader>
            </div>
            <div
              ref={splitRef}
              className={cn(
                'flex-1 min-h-0 flex overflow-hidden',
                // Stacked: editor on top, scrape results underneath. The
                // row layout is impossible below SCRAPE_STACK_BELOW
                // because the two minimums do not fit side by side.
                split.stacked ? 'flex-col' : 'flex-row',
              )}
            >
              <div
                className={cn(
                  'flex-1 min-h-0 overflow-auto px-6 pb-6',
                  scrapeHasResults &&
                    (split.stacked
                      ? 'border-b border-border'
                      : 'border-r border-border'),
                )}
              >
                <TagEditor />
              </div>
              {scrapeHasResults && (
                <>
                  {!split.stacked && (
                    <ResizeHandle
                      onResize={(d) =>
                        setScrapeRightWidth((w) =>
                          applyResize(w, d, resolveSplitLayout(splitWidth, w)),
                        )
                      }
                      valueNow={split.right}
                      min={SCRAPE_RIGHT_MIN}
                      max={split.maxRight}
                    />
                  )}
                  <div
                    style={
                      split.stacked ? undefined : { width: split.right }
                    }
                    className={cn(
                      'shrink-0 min-h-0 overflow-hidden',
                      split.stacked &&
                        'w-full basis-[45%] border-t border-border',
                    )}
                  >
                    <ScrapeResults
                      onApply={(field, value) =>
                        editorActions.updateMusicInfo(field, value)
                      }
                    />
                  </div>
                </>
              )}
            </div>
          </div>
        </DialogContent>
      </Dialog>

      {/* Global Toast Host */}
      <ToastHost />
    </div>
  );
}
