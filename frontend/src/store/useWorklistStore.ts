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
//   - row `duplicate` verdicts (a check is a point-in-time fact: the file
//     it described may since have been replaced or deleted, and a badge
//     restored from localStorage would assert a verdict nobody re-checked)
//   - collapsedGroups (Plan C.3 Open Details A: collapse state doesn't
//     survive reload; expanded-by-default per Open Details A so a fresh
//     page shows everything)
//
// Heavy data-URI artwork is stripped on write so a large batch doesn't
// blow the quota (covers re-fetch via music_id3).
//
// All mutations route through zustand's set with explicit shape updates
// (no `Object.assign` or shallow spreads on `rows`). The selection
// invariant is "selectedIds ⊆ rows.fullPath" — remove() re-validates,
// enqueueDirs appends (after dedupe), setStatus is a surgical per-row
// update.

import { create } from 'zustand';
import type { MusicTagInfo, RowDuplicate, ScrapeStatus, WorklistRow } from '@/types';
import {
  expandDirsToAudioFiles,
  type ExpandedFile,
} from '@/utils/expandDirs';
import { mergeExpandedDirs } from '@/utils/mergeExpanded';
import { getFileList } from '@/api/client';
import { hydrateTagsBatched, type HydratedTag } from '@/lib/hydrateTags';
import {
  lastPersistError,
  readJson,
  readString,
  writeJson,
  writeString,
} from '@/utils/persist';
import {
  needsMusicInfoRefetch,
  stripHeavyFromRows,
} from '@/utils/persistMusicInfo';

export type WorklistFilter = 'all' | 'pending' | 'scraped' | 'failed' | 'duplicate';

// Plan C.3 grouping dimension. Three mutually-exclusive values per the
// plan § Step 2 ("group toggle 互斥"). Default 'none' = flat list
// (i.e. legacy behaviour preserved).
export type WorklistGrouping = 'none' | 'album' | 'artist';

interface WorklistState {
  rows: WorklistRow[];
  /** Selected rows, keyed by row.fullPath (== row.id). Invariant:
   *  every selectedId must correspond to a row in `rows`. remove()
   *  re-validates this; toggleSelected only adds ids that exist on a
   *  row. */
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
  enqueueDirs: (dirs: string[]) => Promise<EnqueueResult>;

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
  remove: (ids: string[]) => void;
  /** Reconcile the queue with what's actually on disk: prune rows whose
   *  file no longer exists under its parent directory.
   *
   *  Why this exists separately from enqueueDirs: enqueueDirs is
   *  append-only and short-circuits on a dir that expands to zero files
   *  (`if (dirs.length === 0)`), so a directory the user emptied on disk
   *  leaves its stale rows in the queue forever — re-adding the dir can't
   *  clean them because it never revisits rows it isn't currently adding.
   *
   *  Method: group rows by parent dir, call getFileList(parentDir) once
   *  per dir (non-recursive — it lists exactly that dir's entries), and
   *  drop a row iff its fileName is absent from a SUCCESSFUL listing. A
   *  dir whose call throws (404/500/offline) is left entirely untouched:
   *  a transient backend failure must never wipe the queue. The empty-dir
   *  case the backend reports as success-with-`children:[]`, which is
   *  exactly the signal enqueueDirs can't act on — here it correctly
   *  prunes every row under that dir.
   *
   *  Returns the number of rows removed so the caller can surface a notice. */
  reconcile: () => Promise<{ removed: number; checkedDirs: number }>;
  setStatus: (id: string, status: ScrapeStatus) => void;
  setFilter: (f: WorklistFilter) => void;
  /** Record duplicate-check verdicts, keyed by row fullPath.
   *
   *  Applied per row rather than as a bulk replace so a partial response
   *  (the server answers per row and can skip an unreadable file) updates
   *  exactly the rows it spoke about and leaves the others alone. Ids with
   *  no matching row are dropped rather than inserted — a check result is
   *  not a reason to conjure a row. */
  setDuplicates: (
    verdicts: Array<{ fileFullPath: string } & RowDuplicate>,
  ) => void;
  /** Clear every row's verdict, e.g. after a delete so no badge outlives
   *  the file it described. */
  clearDuplicates: () => void;
  /** Lazy row-level musicInfo cache. Populated by the boot hydration
   *  below, by enqueueDirs, and by the detail dialog when it saves.
   *  Indexed by row id (== fullPath) so
   *  two rows with the same filename in different dirs stay
   *  independent. */
  setMusicInfo: (id: string, info: Partial<MusicTagInfo>) => void;
  /** Apply a whole chunk of musicInfo updates in ONE store commit.
   *
   *  This is what `hydrateTagsBatched` writes through, and the reason it
   *  exists as a separate entry point: `setMusicInfo` re-persists the
   *  entire table on every call, so a 4000-song hydration issued 4001
   *  whole-table writes totalling 2.6 GB and blocked the main thread for
   *  seconds — the page looked dead, which is what a user reported as
   *  「添加目录点了确认没反应」. Here one chunk is one `set` and one
   *  `persistRows`, so the cost is ceil(N / chunkSize) writes.
   *
   *  Same merge semantics as setMusicInfo per row (`{...old, ...new}`),
   *  ids that no longer match a row are skipped rather than inserted. */
  setMusicInfoBatch: (updates: HydratedTag[]) => void;
  /** Move a row to a new path after the file behind it was renamed.
   *
   *  A row's id IS its fullPath, so a rename is an identity change, not a
   *  field update: `id`, `fullPath` and `fileName` all move together, and
   *  `selectedIds` — which is keyed by the same path — has to follow or the
   *  checkbox desyncs from the row it was ticking. */
  renameRow: (oldPath: string, newPath: string, newFileName: string) => void;
}

interface PersistedShape {
  rows: WorklistRow[];
}

/** What one enqueueDirs call reports. Named so a caller can annotate the
 *  awaited value without re-deriving it off the store's function type
 *  (which yields the function, not its promise). */
export interface EnqueueResult {
  /** Directories newly represented in the queue. */
  added: number;
  /** Directories whose files were all already queued. */
  skipped: number;
  /** Rows actually added. */
  files: number;
  /** Whether the queue reached localStorage. False = the rows above are in
   *  memory only and a reload loses them; `persistError` says why. */
  persisted: boolean;
  /** 'quota' | 'unavailable' | null. Null when `persisted` is true. */
  persistError: string | null;
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

/** Write the queue to localStorage. Returns false when the write was
 *  refused (quota exhausted, private browsing, storage disabled).
 *
 *  It used to swallow that, which turned a real failure into a lie: the
 *  table showed a full queue that was never saved, and the user only found
 *  out when a reload emptied it. The worklist crosses the ~5 MB origin
 *  quota at roughly 24000 rows (4000 rows measure 0.89 MB, linear in row
 *  count), so this is a size the library can genuinely reach. Returning
 *  the verdict lets the batch paths report it instead. */
function persistRows(rows: WorklistRow[]): boolean {
  return writeJson<PersistedShape>(STORAGE_KEY, {
    rows: stripHeavyFromRows(rows),
  });
}

function persistGrouping(g: WorklistGrouping): boolean {
  // A failed grouping write is NOT worth a notice: the choice is one enum
  // and falls back to 'none' on the next load. The verdict is returned
  // only so this mirrors persistRows's shape.
  return writeString(GROUPING_STORAGE_KEY, g);
}

/** The wire shape the server sends for one row, minus the path key the
 *  store already owns (a row's id IS its fullPath, so re-storing it would
 *  let the two drift). */
type VerdictInput = { fileFullPath: string } & RowDuplicate;

const VERDICTS = new Set<string>([
  'unique',
  'duplicate',
  'likely_duplicate',
  'skipped',
  'error',
]);

/** Normalise a server verdict into the row field.
 *
 *  `fileFullPath` is dropped because the id is the path; an unknown verdict
 *  degrades to `error` rather than being stored verbatim, so a future
 *  server verdict cannot smuggle an unhandled string into the filter logic
 *  (which switches on these five). */
function stripVerdictEnvelope(v: VerdictInput): RowDuplicate {
  return {
    // fileFullPath is intentionally not copied through: a row's id IS its
    // fullPath, so storing it again would let the two drift apart.
    verdict: VERDICTS.has(v.verdict) ? v.verdict : 'error',
    matchField: v.matchField,
    duplicatePath: v.duplicatePath,
    reason: v.reason,
    run: v.run,
  };
}

/** Does a row pass the active filter?
 *
 *  Extracted because two places must agree exactly: the table's
 *  `filteredRows`, and `selectAll()`'s "select every visible row". When
 *  those drifted, 全选 under 「只看重复」 would have selected rows the user
 *  could not see.
 *
 *  `duplicate` is its own axis, not a scrape status: a row can be both
 *  `scraped` and `duplicate`, and 「只看重复」 must show it either way. */
export function rowMatchesFilter(row: WorklistRow, filter: WorklistFilter): boolean {
  if (filter === 'all') return true;
  if (filter === 'duplicate') return row.duplicate?.verdict === 'duplicate';
  return row.status === filter;
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
    // Nothing selected is not a failure, so it reports a clean success
    // rather than a persist verdict it never got a chance to earn.
    if (dirs.length === 0) {
      return {
        added: 0,
        skipped: 0,
        files: 0,
        persisted: true,
        persistError: null,
      };
    }
    const expanded = await expandDirsToAudioFiles(dirs);

    // Per-file dedupe, shared with useLibraryStore. The old rule dropped a
    // source dir's whole batch as soon as one of its files was already
    // queued, which meant a directory could never gain a new file — a
    // freshly downloaded track in an already-added directory stayed
    // invisible no matter how many times the user re-added it. See
    // utils/mergeExpanded.ts.
    const { fresh, addedDirs, skippedDirs } = mergeExpandedDirs(
      expanded,
      get().rows.map((r) => r.fullPath),
    );
    const newRows = expandedToRows(fresh);

    const nextRows = appendAndDedupe(get().rows, newRows);
    set({ rows: nextRows });
    const persisted = persistRows(nextRows);

    // Background-fetch /api/music_id3/ so the Worklist rows render with
    // title/artist instead of bare fileName as soon as the user drops a
    // directory.
    //
    // Contract with the boot hydration at the bottom of this file:
    // hydrateTagsBatched writes row musicInfo ONLY when the response
    // has ≥1 non-null field, so a row with genuinely empty tags stays
    // in the `needsMusicInfoRefetch` set and is retried on the next
    // page load. If hydrateTags ever wrote `{}` instead of skipping,
    // that retry would stop firing. See lib/hydrateTags.ts block
    // comment steps 3-4.
    //
    // `setMusicInfoBatch`, not `setMusicInfo`: the per-row writer
    // re-persists the whole table, so a fan-out through it is O(N²) and
    // froze the page on large batches. See setMusicInfoBatch.
    void hydrateTagsBatched(
      newRows.map((r) => ({ id: r.id, fullPath: r.fullPath })),
      (chunk) => useWorklistStore.getState().setMusicInfoBatch(chunk),
    );

    return {
      // Both counts are directories, which is what the drawer's notice
      // says it is reporting. The old pair mixed units — files for
      // `added`, dirs for `skipped` — so the two numbers could not be
      // compared against the number of directories the user ticked.
      // `files` is the row count, which is the unit a user who ticked
      // three loose files actually cares about.
      added: addedDirs,
      skipped: skippedDirs,
      files: fresh.length,
      // False means localStorage refused the write — the rows are live in
      // memory but will not survive a reload. The caller tells the user;
      // it used to be swallowed here and the queue just vanished.
      persisted,
      // 'quota' vs 'unavailable' distinguishes "too big to save, add
      // less" from "this browser will never save anything". Null on
      // success.
      persistError: persisted ? null : lastPersistError(),
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
    const visible = rows.filter((r) => rowMatchesFilter(r, filter));
    set({ selectedIds: visible.map((r) => r.id) });
    return visible.length;
  },

  clearSelected: () => set({ selectedIds: [] }),

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

  reconcile: async () => {
    const rows = get().rows;
    if (rows.length === 0) return { removed: 0, checkedDirs: 0 };

    // Group rows by parent dir so each directory is listed exactly once,
    // regardless of how many queued files sit in it. parentDir === '' is
    // the library root — a valid key the backend lists as any other dir.
    const byDir = new Map<string, WorklistRow[]>();
    for (const r of rows) {
      const i = r.fullPath.lastIndexOf('/');
      const parentDir = i === -1 ? '' : r.fullPath.slice(0, i);
      const bucket = byDir.get(parentDir);
      if (bucket) bucket.push(r);
      else byDir.set(parentDir, [r]);
    }

    // Sequential per-dir, mirroring expandOne's rationale: the backend does
    // sync DB lookups inside each /api/file_list/ call, so a serial loop
    // keeps load predictable and avoids hammering it.
    const deadIds = new Set<string>();
    let checkedDirs = 0;
    for (const [parentDir, dirRows] of byDir) {
      let res;
      try {
        res = await getFileList(parentDir);
      } catch {
        // Transient guard: a 404/500/offline call must NEVER wipe rows.
        // Leave this dir's rows entirely untouched and move on.
        continue;
      }
      // A malformed-but-non-throwing response is treated like a failure:
      // we only prune against a listing we can actually trust.
      if (!res?.result || !Array.isArray(res.data)) continue;
      checkedDirs += 1;
      // Non-recursive: data[0].children is exactly this dir's entries.
      // An emptied dir reports success with children:[] — the case
      // enqueueDirs can't act on — so `present` is empty and every row
      // under it is (correctly) pruned.
      const present = new Set<string>(
        (res.data[0]?.children ?? []).map((c: { name: string }) => c.name),
      );
      for (const r of dirRows) {
        if (!present.has(r.fileName)) deadIds.add(r.id);
      }
    }

    if (deadIds.size === 0) return { removed: 0, checkedDirs };

    set((s) => {
      const nextRows = s.rows.filter((r) => !deadIds.has(r.id));
      persistRows(nextRows);
      return {
        rows: nextRows,
        selectedIds: s.selectedIds.filter((x) => !deadIds.has(x)),
      };
    });
    return { removed: deadIds.size, checkedDirs };
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

  setDuplicates: (verdicts) => {
    if (verdicts.length === 0) return;
    set((s) => {
      // Only ids that exist as rows. The server echoes the paths it was
      // given, so a stale row id (a file renamed mid-check) is expected
      // rather than exceptional.
      const byPath = new Map(verdicts.map((v) => [v.fileFullPath, v]));
      const known = s.rows.some((r) => byPath.has(r.id));
      if (!known) return s;
      const nextRows = s.rows.map((r) => {
        const v = byPath.get(r.id);
        if (!v) return r;
        // `duplicate` is session-only (see the boot hydration below), so
        // this deliberately does NOT call persistRows.
        return { ...r, duplicate: stripVerdictEnvelope(v) };
      });
      return { rows: nextRows };
    });
  },

  clearDuplicates: () => {
    set((s) => {
      if (!s.rows.some((r) => r.duplicate !== undefined)) return s;
      return { rows: s.rows.map((r) => (r.duplicate ? { ...r, duplicate: undefined } : r)) };
    });
  },

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

  setMusicInfoBatch: (updates) => {
    if (updates.length === 0) return;
    set((s) => {
      // One id→info map for the whole chunk: the old per-row shape
      // re-checked `rows.some(...)` and re-mapped `rows` once PER FILE,
      // which is what made the hydration fan-out quadratic even before the
      // persist. Both are now once per chunk.
      const byId = new Map(updates.map((u) => [u.id, u.info]));
      let hit = false;
      for (const id of byId.keys()) {
        if (s.rows.some((r) => r.id === id)) {
          hit = true;
          break;
        }
      }
      if (!hit) return s;
      const nextRows = s.rows.map((r) => {
        const info = byId.get(r.id);
        return info ? { ...r, musicInfo: { ...(r.musicInfo ?? {}), ...info } } : r;
      });
      persistRows(nextRows);
      return { rows: nextRows };
    });
  },

  renameRow: (oldPath, newPath, newFileName) => {
    set((s) => {
      if (!s.rows.some((r) => r.id === oldPath)) return s;
      // The handler refuses a colliding rename, so this should be
      // unreachable — but merging two rows into one id would silently drop
      // a file from the queue, which is worse than ignoring the update.
      if (newPath !== oldPath && s.rows.some((r) => r.id === newPath)) return s;
      const nextRows = s.rows.map((r) =>
        r.id === oldPath
          ? { ...r, id: newPath, fullPath: newPath, fileName: newFileName }
          : r,
      );
      persistRows(nextRows);
      return {
        rows: nextRows,
        selectedIds: s.selectedIds.map((id) => (id === oldPath ? newPath : id)),
      };
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
  void hydrateTagsBatched(need, (chunk) =>
    useWorklistStore.getState().setMusicInfoBatch(chunk),
  );
})();
