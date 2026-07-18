// Independent store for the scrape Worklist.
//
// State shape carries one row per file-to-scrape, plus selection +
// filter state for the active view. Session-scoped — NO localStorage
// persistence (DESIGN.md §LLM design pick: "Worklist is session-scoped,
// cleared on reload is fine for trial-scrape work"). The whole store
// resets to its default shape on a hard reload, which matches the
// expected UX of trial-scrape sessions: user adds dirs, works through
// them, reloads, starts fresh.
//
// All mutations route through zustand's set with explicit shape updates
// (no `Object.assign` or shallow spreads on `rows`). The selection
// invariant is "selectedIds ⊆ rows.fullPath" — remove() and clear()
// re-validate, enqueueDirs appends (after dedupe), setStatus is a
// surgical per-row update.

import { create } from 'zustand';
import type { MusicTagInfo, ScrapeStatus, WorklistRow } from '@/types';
import {
  expandDirsToAudioFiles,
  type ExpandedFile,
} from '@/utils/expandDirs';
import { hydrateTagsBatched } from '@/lib/hydrateTags';

export type WorklistFilter = 'all' | 'pending' | 'scraped' | 'failed';

interface WorklistState {
  rows: WorklistRow[];
  /** Selected rows, keyed by row.fullPath (== row.id). Invariant:
   *  every selectedId must correspond to a row in `rows`. remove() and
   *  clear() re-validate this; toggleSelected only adds ids that
   *  exist on a row. */
  selectedIds: string[];
  filter: WorklistFilter;

  /** Add rows from a list of user-selected top-level directories.
   *  Dedupe-at-dir granularity per DESIGN.md §B pick: any subdir whose
   *  ExpandedFile set overlaps already-existing rows drops the whole
   *  re-add as a no-op (returns `skipped` count > 0). Returns the
   *  count of newly-added rows AND the count of dirs that were
   *  skipped, so the caller (DirPickerDrawer) can render a summary
   *  like "已收录 5 个新增、跳过 2 个已存在". */
  enqueueDirs: (
    dirs: string[],
  ) => Promise<{ added: number; skipped: number }>;

  /** Toggle one row in/out of selection. Idempotent; the caller may
   *  pass any row fullPath — the store resolves to a no-op if the id
   *  is unknown. */
  toggleSelected: (id: string) => void;
  /** Selects every visible row that passes the current filter (not
   *  every row — makes the button contextually meaningful: clicking
   *  it after switching to "失败" picks just the failed rows).
   *  Returns the new selectedIds count for the badge. */
  selectAll: () => number;
  /** Drop the in-filter selection. Symmetric to selectAll. */
  clearSelected: () => void;
  /** Empty the worklist AND selection. Used by a future "清空 Worklist"
   *  affordance. Today nothing calls it, but the store owns it for
   *  nothing-else-to-know reasons. */
  clear: () => void;
  remove: (ids: string[]) => void;
  setStatus: (id: string, status: ScrapeStatus) => void;
  setFilter: (f: WorklistFilter) => void;
  /** Lazy row-level musicInfo cache. Used by TagEditor when it
   *  hydrates from a row click. Indexed by row id (== fullPath) so
   *  two rows with the same filename in different dirs stay
   *  independent. */
  setMusicInfo: (id: string, info: Partial<MusicTagInfo>) => void;
}

/** Pure-row merge: take existing rows, append new ones, dedupe by id.
 *  Returns the merged array. Used by enqueueDirs. */
function appendAndDedupe(
  existing: WorklistRow[],
  additions: WorklistRow[],
): WorklistRow[] {
  if (existing.length === 0) return additions;
  const idSet = new Set(existing.map((r) => r.id));
  const next: WorklistRow[] = [...existing];
  for (const r of additions) {
    if (idSet.has(r.id)) continue;
    idSet.add(r.id);
    next.push(r);
  }
  return next;
}

/** Map ExpandedFile[] from expandDirsToAudioFiles into WorklistRow[].
 *  The id/fullPath are identical (see WorklistRow doc) so the row
 *  is stable across reloads of the same dir — re-adding `/music/foo`
 *  twice collapses onto the same id. */
function expandedToRows(expanded: ExpandedFile[]): WorklistRow[] {
  return expanded.map((e) => ({
    id: e.fullPath,
    fullPath: e.fullPath,
    fileName: e.file.name,
    status: 'pending' as const,
  }));
}

export const useWorklistStore = create<WorklistState>((set, get) => ({
  rows: [],
  selectedIds: [],
  filter: 'all',

  enqueueDirs: async (dirs) => {
    if (dirs.length === 0) return { added: 0, skipped: 0 };
    const expanded = await expandDirsToAudioFiles(dirs);

    // Dedupe at directory granularity: any expanded file whose
    // fullPath already exists in rows implies its source dir is
    // already collected — drop the whole re-add.
    const existingPaths = new Set(get().rows.map((r) => r.fullPath));
    const dirHasOverlap = new Set<string>();
    for (const e of expanded) {
      if (existingPaths.has(e.fullPath)) {
        dirHasOverlap.add(e.sourceDir);
      }
    }

    const fresh = expanded.filter(
      (e) => !dirHasOverlap.has(e.sourceDir),
    );
    const newRows = expandedToRows(fresh);

    set((s) => ({
      rows: appendAndDedupe(s.rows, newRows),
    }));

    // Mirror of useLibraryStore.enqueueDirs: background-fetch
    // /api/music_id3/ so the Worklist rows render with title/artist
    // instead of bare fileName as soon as the user drops a directory.
    //
    // IMPORTANT — contract with the click path: hydrateTagsBatched
    // writes row musicInfo ONLY when the response has ≥1 non-null
    // field. WorklistRowView.openEditor's `cacheHasAnyValue` guard
    // relies on that — if hydrateTags ever writes `{}` instead of
    // skipping, this row's click will silently skip the refetch on
    // truly-empty tags. See lib/hydrateTags.ts block §3.
    void hydrateTagsBatched(
      newRows.map((r) => ({ id: r.id, fullPath: r.fullPath })),
      (id, info) => useWorklistStore.getState().setMusicInfo(id, info),
    );

    return {
      added: newRows.length,
      skipped: dirHasOverlap.size,
    };
  },

  toggleSelected: (id) => {
    set((s) => {
      const has = s.selectedIds.includes(id);
      if (has) {
        return { selectedIds: s.selectedIds.filter((x) => x !== id) };
      }
      // Defensive: refuse to add ids that aren't bound to a row.
      // Avoids the bug where a stale id would be silently tracked
      // and visible-but-unselectable in the UI.
      if (!s.rows.some((r) => r.id === id)) return s;
      return { selectedIds: [...s.selectedIds, id] };
    });
  },

  selectAll: () => {
    const { rows, filter } = get();
    const visible = rows.filter((r) =>
      filter === 'all' ? true : r.status === filter,
    );
    set({ selectedIds: visible.map((r) => r.id) });
    return visible.length;
  },

  clearSelected: () => set({ selectedIds: [] }),

  clear: () => set({ rows: [], selectedIds: [] }),

  remove: (ids) => {
    if (ids.length === 0) return;
    const drop = new Set(ids);
    set((s) => ({
      rows: s.rows.filter((r) => !drop.has(r.id)),
      selectedIds: s.selectedIds.filter((x) => !drop.has(x)),
    }));
  },

  setStatus: (id, status) => {
    set((s) => {
      // Cheap path: nothing to update if the id doesn't exist.
      if (!s.rows.some((r) => r.id === id)) return s;
      return {
        rows: s.rows.map((r) => (r.id === id ? { ...r, status } : r)),
      };
    });
  },

  setFilter: (f) => set({ filter: f }),

  setMusicInfo: (id, info) => {
    set((s) => ({
      rows: s.rows.map((r) =>
        r.id === id ? { ...r, musicInfo: { ...(r.musicInfo ?? {}), ...info } } : r,
      ),
    }));
  },
}));
