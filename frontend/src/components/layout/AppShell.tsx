// Two-mode app shell. `mode` is owned by HomePage (persisted under
// `appShell.mode` in localStorage); AppShell just routes between the
// mode body and renders a single PlayerBar + the song-detail Dialog
// that are mode-independent.
//
// Migration notes (Plan A):
//   - Reads/writes for editor-side state moved from useAppStore to
//     useEditorStore + editorActions (the slice-level split the plan
//     formalized).
//   - The scrape branch renders `<ScrapeMode/>` (full vertical-layout
//     replacement for the old Toolbar+FileBrowser+SearchResults
//     three-column `ScrapeView`). Play branch shows a placeholder —
//     Plan B's PR3 introduces <PlayView> for the local library
//     surfaces (TBD there) so this is the smallest viable bridge.
//   - FileBrowser / Toolbar / ScrapeView / SearchResults / LocalView
//     all deleted. SearchPanel still lives (Plan B owns it) but is
//     not currently rendered; future PlayView will mount it.
//
// Pitfall #6 reminder: never nest a Dialog in a Dialog. The Tidy
// folder Dialog lives in ScrapeTopBar (inside ScrapeMode) and the
// song-detail Dialog lives here at AppShell root — they are
// siblings, both portal to document.body.

import { useEffect, useState } from 'react';
import { TagEditor } from '@/components/editor/TagEditor';
import { ScrapeResults } from '@/components/editor/ScrapeResults';
import { ScrapeMode } from '@/components/scraper/ScrapeMode';
import { PlayView } from '@/components/play/PlayView';
import { PlayerBar } from '@/components/player/PlayerBar';
import { ToastHost } from '@/components/common/ToastHost';
import { useEditorStore, editorActions } from '@/store/useEditorStore';
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { ResizeHandle } from '@/components/layout/ResizeHandle';
import { readNumber, writeNumber } from '@/utils/persist';

export type AppMode = 'play' | 'scrape';

interface Props {
  mode: AppMode;
}

const SCRAPE_RIGHT_KEY = 'appShell.scrapeRightWidth';
const DEFAULT_SCRAPE_RIGHT = 448; // 28rem — width of the in-dialog ScrapeResults

export function AppShell({ mode }: Props) {
  const editorOpen = useEditorStore((s) => s.editorOpen);
  const songList = useEditorStore((s) => s.songList);
  const scrapeHasResults = songList.length > 0;

  // Width of the in-dialog ScrapeResults sidebar (TagEditor ↔
  // ScrapeResults inside the song-detail Dialog). Independent of any
  // body view because the Dialog survives mode toggles.
  const [scrapeRightWidth, setScrapeRightWidth] = useState<number>(
    () => readNumber(SCRAPE_RIGHT_KEY) ?? DEFAULT_SCRAPE_RIGHT,
  );
  useEffect(() => {
    const timer = setTimeout(
      () => writeNumber(SCRAPE_RIGHT_KEY, scrapeRightWidth),
      250,
    );
    return () => clearTimeout(timer);
  }, [scrapeRightWidth]);

  return (
    <>
      <div className="flex-1 flex flex-col overflow-hidden">
        {/* Mode routing. Plan A owns the scrape branch (ScrapeMode).
            Plan B's PlayView renders the play branch: PlayTopBar +
            track table / empty state + (stub AddMusicDrawer pending
            Plan A's real DirPickerDrawer on the base branch). */}
        {mode === 'scrape' ? (
          <ScrapeMode />
        ) : (
          <PlayView />
        )}
        {/* PlayerBar sits below the body view in both modes — one
            audio element, mode-independent, persisted across tab naps. */}
        <PlayerBar />
      </div>

      {/* Song detail Dialog — TagEditor + ScrapeResults dual panel.
          Mode-independent; opens from any view via editorOpen. */}
      <Dialog open={editorOpen} onOpenChange={editorActions.setEditorOpen}>
        <DialogContent
          className={`${
            scrapeHasResults
              ? 'sm:max-w-[min(100rem,calc(100%-2rem))]'
              : 'sm:max-w-[min(64rem,calc(100%-2rem))]'
          } max-h-[92vh] overflow-hidden p-0 transition-[max-width] duration-300`}
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
            <div className="flex-1 min-h-0 flex overflow-hidden">
              <div
                className={`flex-1 overflow-auto px-6 pb-6 ${
                  scrapeHasResults ? 'border-r border-border' : ''
                }`}
              >
                <TagEditor />
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

      {/* Toast surface — pinned bottom-right; auto-dismisses each
          message after ~4s. Mounted at AppShell root so anywhere in
          the tree (ScrapeTopBar, DirPickerDrawer, future plan-B
          PlayView) can broadcast without prop drilling. */}
      <ToastHost />
    </>
  );
}
