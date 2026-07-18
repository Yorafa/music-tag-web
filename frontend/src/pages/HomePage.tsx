// Top-level page after login. Header shows app title, mode toggle,
// and a contextual "已选 N 项" counter that drives off the scrape
// Worklist selection today (Plan A scope). Plan B's PlayView will
// supply a parallel "已收 录 N 个目录" counter for the library branch.
//
// Migrated off useAppStore during Plan A. The header's selection
// counter is now the WorklistStore's selectedIds count rather than
// the legacy FileBrowser checkbox list — same UX surface, different
// backing source. loadFiles reads filePath from useBrowserStore and
// writes treeData back via browserActions.

import { useEffect, useState, useCallback } from 'react';
import { AppShell, type AppMode } from '@/components/layout/AppShell';
import { ThemeToggle } from '@/components/ThemeToggle';
import { SettingsButton } from '@/components/settings/SettingsModal';
import { NoticeCenterButton } from '@/components/notice/NoticeCenterButton';
import { useBrowserStore, browserActions } from '@/store/useBrowserStore';
import { editorActions } from '@/store/useEditorStore';
import { useWorklistStore } from '@/store/useWorklistStore';
import { useAuthStore } from '@/store/useAuthStore';
import { getFileList } from '@/api/client';
import { LogOut, Headphones, Sparkles } from 'lucide-react';
import { resolveBrowsePath } from '@/utils/path';
import { cn } from '@/lib/utils';

const MODE_KEY = 'appShell.mode';

function readStoredMode(): AppMode {
  // New key wins; fall through to legacy appShell.mainTab with mapping:
  //   'search' / 'library' → 'play'  (search was folded into play mode)
  //   'scrape'             → 'scrape'
  // Default → 'play'.
  try {
    const direct = localStorage.getItem(MODE_KEY);
    if (direct === 'play' || direct === 'scrape') return direct;
    const legacy = localStorage.getItem('appShell.mainTab');
    if (legacy === 'scrape') return 'scrape';
    // 'search', 'library', missing, garbage → 'play'.
  } catch {
    /* SSR / locked storage */
  }
  return 'play';
}

export function HomePage() {
  const selectedIds = useWorklistStore((s) => s.selectedIds);
  const logout = useAuthStore((s) => s.logout);
  const [mode, setModeState] = useState<AppMode>(readStoredMode);

  const setMode = useCallback((next: AppMode) => {
    setModeState(next);
    try {
      localStorage.setItem(MODE_KEY, next);
      // Drop the legacy key once we know the user has migrated.
      localStorage.removeItem('appShell.mainTab');
    } catch {
      /* noop */
    }
  }, []);

  // `resolveBrowsePath` 显式保留空串作为「相对 MUSIC_DIR 根」哨兵。
  // 切勿用 `||` 替代 `??`：home 按钮 / 内部 fetch 调用方传 '' 表示根，
  // || 会被 coerce 成 stale filePath，导致「回退不刷新」。
  const loadFiles = async (path?: string) => {
    const p = resolveBrowsePath(path, useBrowserStore.getState().filePath);
    editorActions.setFadeShowDetail(false);
    try {
      const res = await getFileList(p);
      if (res.result) {
        browserActions.setTreeData(res.data);
      }  } catch {
    // silently fail (401 will auto-logout)
  }
  };

  // Mount-only: load the directory tree once on entry. The
  // exhaustive-deps rule wants `loadFiles` listed, but doing so
  // would re-fire the fetch every render because the function reads
  // `filePath` from the store at call time. ESLint's exhaustive-deps
  // is disabled at the file level for this reason — see eslint
  // config override for `src/pages/HomePage.tsx`.
  useEffect(() => {
    void loadFiles();
  }, []);

  return (
    <div className="h-screen flex flex-col bg-background text-foreground">
      <header className="h-12 border-b border-border flex items-center px-4 shrink-0 bg-card gap-3">
        <h1 className="text-sm font-semibold tracking-tight">
          🎵 音乐标签 Web 版
        </h1>
        <span className="text-xs text-muted-foreground">
          {/* Selection counter: scrape-mode = Worklist selection; play-
              mode = empty (Plan B will supply its own counter on
              PlayView). Reads the slice via subscription so swapping
              rows in/out updates the chip without any explicit
              binding. */}
          {mode === 'scrape' && selectedIds.length > 0
            ? `已选 ${selectedIds.length} 项`
            : ''}
        </span>
        {/* Mode toggle (segmented control). 'play' = 音乐库 + 全局搜
            索同屏；'scrape' = Worklist + 工具行. Lives at top-right
            per plan. */}
        <div
          role="group"
          aria-label="界面模式"
          className="ml-auto flex items-center gap-1"
        >
          <div className="inline-flex rounded-md border border-border bg-muted/40 p-0.5 mr-1">
            <button
              type="button"
              onClick={() => setMode('play')}
              aria-pressed={mode === 'play'}
              className={cn(
                'h-7 px-2.5 inline-flex items-center gap-1 rounded text-xs font-medium transition-colors',
                mode === 'play'
                  ? 'bg-background shadow-sm text-foreground'
                  : 'text-muted-foreground hover:text-foreground',
              )}
              title="本地播放模式：音乐库 + 全局搜索同屏"
            >
              <Headphones className="w-3.5 h-3.5" />
              <span>本地</span>
            </button>
            <button
              type="button"
              onClick={() => setMode('scrape')}
              aria-pressed={mode === 'scrape'}
              className={cn(
                'h-7 px-2.5 inline-flex items-center gap-1 rounded text-xs font-medium transition-colors',
                mode === 'scrape'
                  ? 'bg-background shadow-sm text-foreground'
                  : 'text-muted-foreground hover:text-foreground',
              )}
              title="刮削模式：Worklist 队列 + 批处理工具行"
            >
              <Sparkles className="w-3.5 h-3.5" />
              <span>刮削</span>
            </button>
          </div>
          <SettingsButton />
          <ThemeToggle />
          <NoticeCenterButton />
          <button
            type="button"
            onClick={logout}
            title="退出登录"
            aria-label="退出登录"
            className="inline-flex items-center justify-center w-9 h-9 rounded-md hover:bg-accent text-muted-foreground hover:text-foreground transition-colors"
          >
            <LogOut className="w-4 h-4" />
          </button>
        </div>
      </header>
      <AppShell mode={mode} />
    </div>
  );
}
