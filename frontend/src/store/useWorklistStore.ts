// Independent store for the scrape Worklist.
//
// State shape carries one row per file-to-scrape, plus selection +
// filter state for the active view.
//
// Persistence layers (Plan C.3 Open Details E):
//   - `worklist.v1`             → rows + lightweight musicInfo
//   - `worklist.grouping.v1`    → grouping choice (none / album / artist)
//
// What is session-only:
//   - selectedIds
//   - filter
//   - collapsedGroups (Plan C.3 Open Details A: collapse state doesn't
//     survive reload; expanded-by-default per Open Details A so a fresh
//     page shows everything)
//
// Heavy data-URI artwork is stripped on write so a large batch doesn't
// blow the quota (covers re-fetch via music_id3).
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
import { readJson, readString, writeJson, writeString, removeString } from '@/utils/persist';
import {
  needsMusicInfoRefetch,
  stripHeavyFromRows,
} from '@/utils/persistMusicInfo';

export type WorklistFilter = 'all' | 'pending' | 'scraped' | 'failed';

// Plan C.3 grouping dimension. Three mutually-exclusive values per the
// plan § Step 2 ("group toggle 互斥"). Default 'none' = flat list
// (i.e. legacy behaviour preserved).
export type WorklistGrouping = 'none' | 'album' | 'artist';

interface WorklistState {
  rows: WorklistRow[];
  /** Selected rows, keyed by row.fullPath (== row.id). Invariant:
   *  every selectedId must correspond to a row in `rows`. remove() and
   *  clear() re-validate this; toggleSelected only adds ids that
   *  exist on a row. */
  selectedIds: string[];
  filter: WorklistFilter;

  // ───── C.3 grouping plane ──────────────────────────────────────────
  /** Active grouping axis; persisted to `worklist.grouping.v1` so the
   *  choice survives reload. Default 'none' = legacy flat list. */
  grouping: WorklistGrouping;
  /** Session-only Set of `<groupKey>:hash` strings. Plan C.3 § Step 2
   *  Open Details A — collapsed state does NOT persist across reloads;
   *  it's re-initialized to empty (treated as "all groups expanded")
   *  on every page load so first impressions aren't "where did my
   *  albums go?". */
  collapsedGroups: Set<string>;
  setGrouping: (g: WorklistGrouping) => void;
  /** Cycle: 'none' → 'album' → 'artist' → 'none'. Symmetric is the
   *  chip row's "互斥" pattern; cycle is the keyboard-shortcut /
   *  context-menu path. */
  toggleGrouping: () => void;
  /** Toggle one group between expanded (default) and collapsed. No-op
   *  if grouping === 'none' (caller is responsible for suppress). */
  toggleGroupCollapsed: (groupKey: string) => void;

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

interface PersistedShape {
  rows: WorklistRow[];
}

const STORAGE_KEY = 'worklist.v1';
const GROUPING_STORAGE_KEY = 'worklist.grouping.v1';

const VALID_STATUS = new Set<ScrapeStatus>(['pending', 'scraped', 'failed']);
const VALID_GROUPING = new Set<WorklistGrouping>(['none', 'album', 'artist']);
/** Order matters for `toggleGrouping()` cycle step. */
const GROUPING_CYCLE: WorklistGrouping[] = ['none', 'album', 'artist'];

function loadPersisted(): WorklistRow[] {
  const v = readJson<PersistedShape>(STORAGE_KEY);
  if (!v || !Array.isArray(v.rows)) return [];
  return v.rows
    .filter(
      (r): r is WorklistRow =>
        !!r &&
        typeof r.id === 'string' &&
        typeof r.fullPath === 'string' &&
        typeof r.fileName === 'string' &&
        VALID_STATUS.has(r.status as ScrapeStatus),
    )
    .map((r) => ({
      id: r.id,
      fullPath: r.fullPath,
      fileName: r.fileName,
      status: r.status as ScrapeStatus,
      musicInfo: r.musicInfo,
    }));
}

/** Hydrate grouping from the dedicated key. Strings only — Set shape
 *  is intentionally NOT persisted (Plan C.3 Open Details A). */
function loadGrouping(): WorklistGrouping {
  const v = readString(GROUPING_STORAGE_KEY);
  if (v && VALID_GROUPING.has(v as WorklistGrouping)) {
    return v as WorklistGrouping;
  }
  return 'none';
}

function persistRows(rows: WorklistRow[]): void {
  try {
    writeJson<PersistedShape>(STORAGE_KEY, {
      rows: stripHeavyFromRows(rows),
    });
  } catch {
    // quota / private mode — swallow; list still lives in memory
  }
}

function persistGrouping(g: WorklistGrouping): void {
  try {
    writeString(GROUPING_STORAGE_KEY, g);
  } catch {
    // swallow per persistRows rationale; grouping choice is recoverable
    // on next reload via the default 'none'.
  }
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
  rows: loadPersisted(),
  selectedIds: [],
  filter: 'all',
  grouping: loadGrouping(),
  collapsedGroups: new Set(),

  setGrouping: (g) => {
    // Coerce unknown values to 'none' to mirror loadGrouping's
    // defensive default (e.g. a stale localStorage with a v2 schema).
    const safe: WorklistGrouping = VALID_GROUPING.has(g) ? g : 'none';
    set({ grouping: safe });
    persistGrouping(safe);
  },

  toggleGrouping: () => {
    const current = get().grouping;
    const idx = GROUPING_CYCLE.indexOf(current);
    const next = GROUPING_CYCLE[(idx + 1) % GROUPING_CYCLE.length];
    set({ grouping: next });
    persistGrouping(next);
  },

  toggleGroupCollapsed: (groupKey) => {
    set((s) => {
      // Always copy before mutation so React/zustand sees a new
      // reference (Set mutations in-place don't trigger renders).
      const next = new Set(s.collapsedGroups);
      if (next.has(groupKey)) next.delete(groupKey);
      else next.add(groupKey);
      return { collapsedGroups: next };
    });
  },

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

    const nextRows = appendAndDedupe(get().rows, newRows);
    set({ rows: nextRows });
    persistRows(nextRows);

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

  clear: () => {
    set({ rows: [], selectedIds: [] });
    removeString(STORAGE_KEY);
  },

  remove: (ids) => {
    if (ids.length === 0) return;
    const drop = new Set(ids);
    set((s) => {
      const nextRows = s.rows.filter((r) => !drop.has(r.id));
      persistRows(nextRows);
      return {
        rows: nextRows,
        selectedIds: s.selectedIds.filter((x) => !drop.has(x)),
      };
    });
  },

  setStatus: (id, status) => {
    set((s) => {
      // Cheap path: nothing to update if the id doesn't exist.
      if (!s.rows.some((r) => r.id === id)) return s;
      const nextRows = s.rows.map((r) =>
        r.id === id ? { ...r, status } : r,
      );
      persistRows(nextRows);
      return { rows: nextRows };
    });
  },

  setFilter: (f) => set({ filter: f }),

  setMusicInfo: (id, info) => {
    set((s) => {
      if (!s.rows.some((r) => r.id === id)) return s;
      const nextRows = s.rows.map((r) =>
        r.id === id
          ? { ...r, musicInfo: { ...(r.musicInfo ?? {}), ...info } }
          : r,
      );
      persistRows(nextRows);
      return { rows: nextRows };
    });
  },
}));

// Boot: re-fetch tags for persisted rows that need a musicInfo refresh
// (empty cache, or lightweight tags without a usable cover after
// stripHeavy dropped data-URI artwork). Fire-and-forget.
(() => {
  const need = useWorklistStore
    .getState()
    .rows.filter((r) => needsMusicInfoRefetch(r.musicInfo))
    .map((r) => ({ id: r.id, fullPath: r.fullPath }));
  if (need.length === 0) return;
  void hydrateTagsBatched(need, (id, info) =>
    useWorklistStore.getState().setMusicInfo(id, info),
  );
})();
