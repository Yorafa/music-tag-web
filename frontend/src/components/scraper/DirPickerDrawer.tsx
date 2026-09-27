// Shared directory-picker drawer.
//
// Frozen contract with Plan B (β-side):
//   destination: 'library' | 'worklist'
//     - 'worklist' → useWorklistStore.getState().enqueueDirs(selected)
//       (this file owns that store, so the static import is safe)
//     - 'library'  → useLibraryStore.getState().enqueueDirs(selected)
//       Originally a dynamic import with `/* @vite-ignore */` so Plan A
//       could ship on a base branch before Plan B's store existed. Now
//       that Plan B is merged the static import is safe AND necessary:
//       the `@/` path alias is resolved by the Vite/dev build but NOT by
//       the browser at runtime, so a production dynamic import('@/store/
//       useLibraryStore') would fail path resolution, fall into the
//       catch branch, and silently drop the user's selection.
//
// UI: right-side slide-in on desktop, bottom-sheet on mobile via the
// Tailwind `sm:` breakpoint (matches the rest of the app's mobile
// pivots — see AppShell.tsx). The drawer renders the current directory's
// folder children; clicking a folder navigates into it; checking marks
// it for collection. Selected folder paths show as chips above the
// footer; an ✕ button on each chip removes that selection.

import { useEffect, useState, useCallback, useMemo } from 'react';
import {
  Folder,
  FolderTree,
  ChevronRight,
  Home,
  ArrowLeft,
  Music,
  X as XIcon,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { ScrollArea } from '@/components/ui/scroll-area';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { getFileList } from '@/api/client';
import { selectionKind } from '@/utils/expandDirs';
import { useWorklistStore } from '@/store/useWorklistStore';
import { useLibraryStore } from '@/store/useLibraryStore';
import { useNoticeStore } from '@/store/useNoticeStore';
import {
  PATH_ALIAS,
  formatDisplayPath,
  joinParts,
} from '@/utils/path';
import {
  appendToPath,
  directAudioNames,
  fileRowsOf,
  folderRowsOf,
  parentOf,
  wholeDirLabel,
  wholeDirSelectable,
} from '@/components/scraper/dirPicker';
import { cn } from '@/lib/utils';
import { formatBytes } from '@/utils/formatBytes';
import type { FileNode } from '@/types';

interface DirPickerDrawerProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  destination: 'library' | 'worklist';
  /** Optional badge list — preserved on the contract so Plan B's
   *  PlayView can pass it without a re-coordinated signature change.
   *  Today we don't render "already added" chrome; this prop is a
   *  forward-compat slot, intentionally unused. */
  existingDirs?: string[];
}

/** Map from absolute path under MUSIC_DIR (e.g. 'foo/bar') to the
 *  FileNode[] the backend returned for that directory's /api/file_list/
 *  call. Persisted in component state so navigating back doesn't
 *  re-fetch. */
type TreeCache = Record<string, FileNode[] | undefined>;

export function DirPickerDrawer({
  open,
  onOpenChange,
  destination,
}: DirPickerDrawerProps) {
  // We deliberately don't subscribe to `useBrowserStore.filePath` here —
  // the picker maintains its own current-dir navigation stack so the
  // outer store's filePath (used by FileBrowser) is unaffected by what
  // the user is browsing in the drawer.

  const [currentDir, setCurrentDir] = useState<string>('');
  const [treeCache, setTreeCache] = useState<TreeCache>({});
  const [selected, setSelected] = useState<Set<string>>(() => new Set());
  const [fetching, setFetching] = useState(false);

  // Combined (re-)hydrate + refetch effect. The two transitions we
  // care about are (a) the drawer just opened (re-hydrate: clear
  // selection, reset to root, fetch root) and (b) the user navigated
  // into a subfolder (fetch that subfolder). Both reduce to "fetch
  // currentDir" once the open gate is satisfied, so a single effect
  // with `[open, currentDir]` deps is enough — no callbacks to
  // re-declare. setFetching / setTreeCache are deliberately placed
  // INSIDE the async IIFE so React-19's
  // `react-hooks/set-state-in-effect` rule doesn't flag them as
  // cascading-render side effects.
  useEffect(() => {
    if (!open) return;
    let active = true;
    void (async () => {
      setFetching(true);
      try {
        const res = await getFileList(currentDir);
        if (!active) return;
        if (res?.result && Array.isArray(res.data)) {
          setTreeCache((prev) => ({ ...prev, [currentDir]: res.data }));
        } else {
          setTreeCache((prev) => ({ ...prev, [currentDir]: [] }));
        }
      } catch {
        if (!active) return;
        setTreeCache((prev) => ({ ...prev, [currentDir]: [] }));
      } finally {
        if (active) setFetching(false);
      }
    })();
    return () => {
      active = false;
    };
  }, [open, currentDir]);

  const rowsForCurrent = useMemo(
    () => folderRowsOf(treeCache[currentDir], currentDir),
    [treeCache, currentDir],
  );

  // The files sitting in this directory, audio first. They are selectable
  // on their own now, which is the point: the user who knows they want one
  // track should not have to take the whole folder to get it.
  const fileRows = useMemo(
    () => fileRowsOf(treeCache[currentDir], currentDir),
    [treeCache, currentDir],
  );

  // The directory being browsed is itself selectable, which is the only
  // way to reach audio sitting directly in it (see dirPicker.ts). Hidden
  // when it would enqueue nothing, so an inert checkbox never shows up.
  const canPickWholeDir = useMemo(
    () => wholeDirSelectable(treeCache[currentDir], currentDir),
    [treeCache, currentDir],
  );
  const directAudio = useMemo(
    () => directAudioNames(treeCache[currentDir]),
    [treeCache, currentDir],
  );

  const toggleSelected = (relPath: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(relPath)) next.delete(relPath);
      else next.add(relPath);
      return next;
    });
  };

  const removeChip = (relPath: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      next.delete(relPath);
      return next;
    });
  };

  const navigate = (relPath: string) => {
    setCurrentDir(relPath);
  };

  const breadcrumbParts = currentDir.split('/').filter(Boolean);
  const isRoot = currentDir === '';

  const onConfirm = useCallback(async () => {
    const selectedArr = Array.from(selected);
    if (selectedArr.length === 0) {
      useNoticeStore
        .getState()
        .push('没有选中任何目录', 'warn');
      return;
    }

    const result =
      destination === 'worklist'
        ? await useWorklistStore.getState().enqueueDirs(selectedArr)
        : await useLibraryStore.getState().enqueueDirs(selectedArr);

    // Two units, because the selection can hold both. "已收录 1 个新增目录"
    // after ticking three files is true and useless; the file count is what
    // the user can check against the checkbox column.
    const onlyFiles = selectedArr.every((p) => selectionKind(p) === 'file');
    useNoticeStore.getState().push(
      onlyFiles
        ? `已收录 ${result.files} 个文件`
        : `已收录 ${result.files} 个文件（${result.added} 个新增目录）、跳过 ${result.skipped} 个重复目录`,
      'info',
    );
    setSelected(new Set());
    onOpenChange(false);
  }, [selected, destination, onOpenChange]);

  // Compose submit-disabled state. We keep the button rather than hiding
  // it so the affordance is discoverable; disabled state is friendlier
  // for screen-reader users than disappearance.
  const submitDisabled = selected.size === 0;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className={cn(
          // Mobile = bottom sheet pinned to bottom-stretch; SM+ flips it
          // to a right-side drawer sized at sm:max-w-md.
          'p-0 gap-0',
          'left-0 right-0 bottom-0 top-auto translate-x-0 translate-y-0',
          'rounded-b-none rounded-t-xl max-w-none',
          'sm:left-auto sm:right-0 sm:top-0 sm:bottom-0 sm:max-w-md sm:h-screen sm:rounded-l-xl sm:rounded-r-none sm:max-h-screen sm:translate-x-0 sm:translate-y-0',
          'data-open:slide-in-from-bottom sm:data-open:slide-in-from-right',
          'data-closed:slide-out-to-bottom sm:data-closed:slide-out-to-right',
        )}
      >
        {/* min-w-0 is load-bearing, not tidiness. DialogContent is a
            `grid`, and a grid track sized `auto` will not shrink below its
            content's min-content width — one long filename (this library
            has a 60-character one) pushed the track to 748px inside a
            448px drawer and moved 确认 off the right edge of the screen,
            so the drawer's only submit button could not be clicked. */}
        <div className="flex flex-col h-[85vh] sm:h-screen min-w-0">
          <DialogHeader className="px-4 pt-4 pb-3 border-b border-border">
            <DialogTitle>选择目录</DialogTitle>
            {/* Breadcrumb. Clicking any segment OR the home icon jumps
                directly there without rebuilding the cache. The
                data-testid attributes parallel FileBrowser's so future
                e2e tests can find these affordances by stable id. */}
            <div className="flex items-center gap-1 text-xs flex-wrap mt-2">
              <button
                type="button"
                onClick={() => navigate('')}
                title="返回根目录"
                data-testid="dirpicker-home"
                className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded hover:bg-accent text-muted-foreground hover:text-foreground transition-colors"
              >
                <Home className="w-3 h-3" />
                <span>{PATH_ALIAS}</span>
              </button>
              {breadcrumbParts.map((part, idx) => {
                const isLast = idx === breadcrumbParts.length - 1;
                const segPath = joinParts(breadcrumbParts.slice(0, idx + 1));
                return (
                  <div key={`${part}-${idx}`} className="flex items-center gap-1 min-w-0">
                    <ChevronRight className="w-3 h-3 text-muted-foreground/50 shrink-0" />
                    {isLast ? (
                      <span
                        className="font-medium text-foreground px-1.5 py-0.5 truncate max-w-[160px]"
                        title={part}
                      >
                        {part}
                      </span>
                    ) : (
                      <button
                        type="button"
                        onClick={() => navigate(segPath)}
                        title={`跳转到 ${part}`}
                        className="px-1.5 py-0.5 rounded hover:bg-accent text-muted-foreground hover:text-foreground transition-colors truncate max-w-[160px]"
                      >
                        {part}
                      </button>
                    )}
                  </div>
                );
              })}
              <button
                type="button"
                onClick={() => setCurrentDir(parentOf(currentDir))}
                disabled={isRoot}
                title="返回上级目录"
                aria-label="返回上级目录"
                className="p-1 hover:bg-accent rounded transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
              >
                <ArrowLeft className="w-4 h-4 text-muted-foreground" />
              </button>
            </div>
          </DialogHeader>

          <ScrollArea className="flex-1 min-h-0">
            <div className="p-2">
              {fetching && rowsForCurrent.length === 0 ? (
                <div className="text-center py-8 text-xs text-muted-foreground">
                  加载中…
                </div>
              ) : rowsForCurrent.length === 0 && !canPickWholeDir ? (
                <div className="text-center py-8 text-xs text-muted-foreground">
                  当前目录下没有子目录，也没有可收录的音频
                </div>
              ) : (
                <>
                {canPickWholeDir && (
                  <label
                    className={cn(
                      'flex items-center gap-2 px-2 py-1.5 rounded-md transition-colors text-sm mb-1 cursor-pointer min-w-0',
                      selected.has(currentDir)
                        ? 'bg-primary/10 text-foreground'
                        : 'hover:bg-accent',
                    )}
                  >
                    <input
                      type="checkbox"
                      checked={selected.has(currentDir)}
                      onChange={() => toggleSelected(currentDir)}
                      className="w-3.5 h-3.5 rounded border-border accent-primary shrink-0 cursor-pointer"
                    />
                    <FolderTree className="w-4 h-4 text-primary shrink-0" />
                    <span className="truncate min-w-0">{wholeDirLabel(currentDir)}</span>
                    {directAudio.length > 0 && (
                      <span className="ml-auto shrink-0 text-[10px] text-muted-foreground">
                        本层 {directAudio.length} 个音频
                      </span>
                    )}
                  </label>
                )}
                {rowsForCurrent.map((row) => {
                  const isSelected = selected.has(row.relPath);
                  return (
                    <div
                      key={row.relPath}
                      className={cn(
                        'flex items-center gap-2 px-2 py-1.5 rounded-md transition-colors text-sm min-w-0',
                        isSelected
                          ? 'bg-primary/10 text-foreground'
                          : 'hover:bg-accent',
                      )}
                    >
                      <input
                        type="checkbox"
                        checked={isSelected}
                        onChange={() => toggleSelected(row.relPath)}
                        onClick={(e) => e.stopPropagation()}
                        aria-label={`选中 ${row.name}`}
                        className="w-3.5 h-3.5 rounded border-border accent-primary shrink-0 cursor-pointer"
                      />
                      <button
                        type="button"
                        onClick={() => navigate(row.relPath)}
                        className="flex items-center gap-2 flex-1 min-w-0 cursor-pointer bg-transparent border-0 p-0 text-left font-normal text-inherit hover:underline focus-visible:outline-none focus-visible:underline"
                        title={`进入 ${row.name}`}
                      >
                        <Folder className="w-4 h-4 text-amber-400 shrink-0" />
                        <span className="truncate">{row.name}</span>
                      </button>
                      <button
                        type="button"
                        onClick={() =>
                          navigate(appendToPath(currentDir, row.name))
                        }
                        title="进入子目录"
                        className="inline-flex items-center justify-center w-6 h-6 rounded hover:bg-accent text-muted-foreground hover:text-foreground transition-colors shrink-0"
                      >
                        <ChevronRight className="w-3.5 h-3.5" />
                      </button>
                    </div>
                  );
                })}

                {/* The directory's own files. The list used to stop at the
                    subdirectories, so a folder of loose tracks looked
                    empty: the user could see there were files only by
                    selecting the whole directory and getting all of them,
                    which is not the same as choosing one. Non-audio files
                    are listed but not tickable — a checkbox that adds
                    nothing is worse than no checkbox. */}
                {fileRows.length > 0 && (
                  <div className="mt-2 pt-2 border-t border-border/40">
                    {fileRows.map((row) => {
                      const isSelected = selected.has(row.relPath);
                      return (
                        <div
                          key={row.relPath}
                          className={cn(
                            'flex items-center gap-2 px-2 py-1.5 rounded-md transition-colors text-sm min-w-0',
                            row.selectable
                              ? isSelected
                                ? 'bg-primary/10 text-foreground'
                                : 'hover:bg-accent'
                              : 'opacity-60',
                          )}
                        >
                          {row.selectable ? (
                            <input
                              type="checkbox"
                              checked={isSelected}
                              onChange={() => toggleSelected(row.relPath)}
                              aria-label={`选中 ${row.name}`}
                              className="w-3.5 h-3.5 rounded border-border accent-primary shrink-0 cursor-pointer"
                            />
                          ) : (
                            <span
                              className="w-3.5 shrink-0"
                              title="非音频文件，不会被收录"
                            />
                          )}
                          <Music
                            className={cn(
                              'w-4 h-4 shrink-0',
                              row.selectable
                                ? 'text-primary'
                                : 'text-muted-foreground',
                            )}
                          />
                          <span
                            className="truncate flex-1 min-w-0"
                            title={row.relPath}
                          >
                            {row.name}
                          </span>
                          {typeof row.size === 'number' && row.size > 0 && (
                            <span className="text-[10px] text-muted-foreground font-mono shrink-0">
                              {formatBytes(row.size)}
                            </span>
                          )}
                        </div>
                      );
                    })}
                  </div>
                )}
                </>
              )}
            </div>
          </ScrollArea>

          {/* Selected paths as removable chips. */}
          {selected.size > 0 && (
            <div className="border-t border-border px-3 py-2 max-h-32 overflow-y-auto bg-surface-2">
              <div className="text-[10px] uppercase tracking-wide text-muted-foreground mb-1.5">
                已选 {selected.size} 项
              </div>
              <div className="flex flex-wrap gap-1">
                {Array.from(selected).map((relPath) => (
                  <Badge
                    key={relPath}
                    variant="secondary"
                    className="gap-1 pr-1 font-mono"
                  >
                    <span className="truncate max-w-[180px]">{formatDisplayPath(relPath)}</span>
                    <button
                      type="button"
                      onClick={() => removeChip(relPath)}
                      className="inline-flex items-center justify-center w-3.5 h-3.5 rounded hover:bg-foreground/10 transition-colors"
                      title="移除此目录"
                      aria-label="移除此目录"
                    >
                      <XIcon className="w-3 h-3" />
                    </button>
                  </Badge>
                ))}
              </div>
            </div>
          )}

          {/* `mx-0 mb-0` overrides the primitive's `-mx-4 -mb-4`. Those
              negatives exist to cancel DialogContent's own `p-4`; this
              drawer sets `p-0 gap-0` instead, so nothing cancels them and
              the footer hangs 16px off BOTH edges — invisible on a desktop
              panel, 32px wider than the screen on a phone. */}
          <DialogFooter className="mx-0 mb-0 px-4 py-3 sm:px-4">
            <Button
              variant="outline"
              onClick={() => onOpenChange(false)}
            >
              取消
            </Button>
            <Button
              onClick={onConfirm}
              disabled={submitDisabled}
              title={
                submitDisabled ? '请至少选中一个目录' : '把选中的目录加入队列'
              }
            >
              确认
            </Button>
          </DialogFooter>
        </div>
      </DialogContent>
    </Dialog>
  );
}
