// useLibraryStore — local play mode's persistent "trial-listen" library.
//
// DESIGN.md - local play mode:
//   The user opens DirPickerDrawer -> multi-select dirs -> enqueueDirs ->
//   files appear in PlayView as a table. Persisted to localStorage so it
//   survives reloads. NOT a full music management layer — sized for
//   "the handful of folders I'm tagging this session", not the whole library.
//
// Plan B of the frontend-layout-refactor. See:
//   docs/plans/frontend-layout-refactor/Plan-B-Play-Mobile-Visual.md
//
// Directory expansion is delegated to Plan A's shared util
// `expandDirsToAudioFiles` in `src/utils/expandDirs.ts` — same audio
// extension whitelist and recursive sub-dir flattening as the scrape
// Worklist so a file appears at the same `fullPath` (and dedupes correctly)
// regardless of which mode added it. Mirrors useWorklistStore's dir-
// granularity dedupe: a re-added directory collapses onto the existing
// rows rather than producing duplicates.
//
// Persistence notes:
//   - rows + dirs go to library.v1
//   - setMusicInfo ALSO re-persists (was previously memory-only — reload
//     lost title/artist previews even though the row list survived)
//   - data-URI artwork is stripped on write to avoid quota blowups

import { create } from 'zustand';
import type { MusicTagInfo } from '@/types';
import {
  expandDirsToAudioFiles,
  type ExpandedFile,
} from '@/utils/expandDirs';
import { mergeExpandedDirs } from '@/utils/mergeExpanded';
import { readJson, writeJson, removeString } from '@/utils/persist';
import {
  needsMusicInfoRefetch,
  stripHeavyFromRows,
} from '@/utils/persistMusicInfo';
import { hydrateTagsBatched } from '@/lib/hydrateTags';

export interface LibraryRow {
  /** Stable id = the file's fullPath under MUSIC_DIR. Identical to the
   *  Plan A's WorklistRow id convention so a file row has one identity
   *  across modes (a row added in scrape mode and the same file added
   *  in play mode share `id`, even though they live in separate stores).
   *  Tied to re-expand stability: expandDirsToAudioFiles returns the
   *  same fullPath for the same file on subsequent calls, so re-adding
   *  a directory collapses to the same ids. */
  id: string;
  fullPath: string;    // relative to MUSIC_DIR — used for /media stream src
  fileName: string;
  /** Lazy tag preview populated on first read of the row's tag (optional,
   *  unused by the current PlayView rendering but reserved so a future
   *  one-tap "show id3" doesn't need a round-trip). */
  musicInfo?: Partial<MusicTagInfo>;
}

interface PersistedShape {
  rows: LibraryRow[];
  dirs: string[];
}

const STORAGE_KEY = 'library.v1';

function loadPersisted(): PersistedShape {
  const v = readJson<PersistedShape>(STORAGE_KEY);
  if (!v || !Array.isArray(v.rows) || !Array.isArray(v.dirs)) {
    return { rows: [], dirs: [] };
  }
  return v;
}

function persist(state: PersistedShape): void {
  try {
    writeJson<PersistedShape>(STORAGE_KEY, {
      rows: stripHeavyFromRows(state.rows),
      dirs: state.dirs,
    });
  } catch {
    // localStorage quota exceeded — trial-listen library is intentionally
    // small. Silently swallow; user can hit 「清空」 to free up. We avoid
    // calling useNoticeStore here to dodge a potential circular import.
  }
}

/** Map ExpandedFile[] from expandDirsToAudioFiles into LibraryRow[]. The
 *  id is the file's fullPath — same identity convention as
 *  useWorklistStore.expandedToRows so a given file has one id across
 *  both stores (see WorklistRow docstring in src/types/index.ts). */
function expandedToRows(expanded: ExpandedFile[]): LibraryRow[] {
  return expanded.map((e) => ({
    id: e.fullPath,
    fullPath: e.fullPath,
    fileName: e.file.name,
  }));
}

interface LibraryState {
  rows: LibraryRow[];
  /** Collated, deduped list of directories the library was built from.
   *  Drives the Drawer's `existingDirs` 「already-added」 badges. */
  dirs: string[];
  /** In-memory search filter applied by getFiltered(). */
  query: string;
  enqueueDirs(dirs: string[]): Promise<{ added: number; skipped: number }>;
  search(s: string): void;
  remove(id: string): void;
  clear(): void;
  getFiltered(): LibraryRow[];
  /** Lazy row-level musicInfo cache. Populated by PlayView's row
   *  click handler after a successful /api/music_id3/ fetch so
   *  subsequent clicks on the same row skip the round-trip and the
   *  editor pre-resolves the cover + first-line preview instantly.
   *  Mirrors useWorklistStore.setMusicInfo shape + indexed-by-id
   *  dedupe semantics. */
  setMusicInfo(id: string, info: Partial<MusicTagInfo>): void;
  /** Move a row to a new path after the file behind it was renamed.
   *  LibraryRow.id is fullPath too, so this mirrors the worklist store's
   *  renameRow minus the selection bookkeeping. */
  renameRow(oldPath: string, newPath: string, newFileName: string): void;
}

const boot = loadPersisted();

export const useLibraryStore = create<LibraryState>((set, get) => ({
  rows: boot.rows,
  dirs: boot.dirs,
  query: '',

  enqueueDirs: async (dirs) => {
    if (dirs.length === 0) return { added: 0, skipped: 0 };

    const expanded = await expandDirsToAudioFiles(dirs);
    if (expanded.length === 0) return { added: 0, skipped: 0 };

    // Per-file dedupe, shared with useWorklistStore so the two modes cannot
    // drift. The old rule dropped a source dir's whole batch as soon as one
    // of its files was already listed, so a directory could never gain a new
    // file — see utils/mergeExpanded.ts.
    const { fresh, addedDirs, skippedDirs, addedDirNames } = mergeExpandedDirs(
      expanded,
      get().rows.map((r) => r.fullPath),
    );
    const newRows = expandedToRows(fresh);

    // Also dedupe within the returned batch by id (a file might appear
    // twice if two input dirs share a child — pathological but cheap).
    const idSeen = new Set<string>();
    const dedupedNewRows: LibraryRow[] = [];
    for (const r of newRows) {
      if (idSeen.has(r.id)) continue;
      idSeen.add(r.id);
      dedupedNewRows.push(r);
    }

    // The set of newly-added source dirs = the input dirs that contributed at
    // least one file the library did not have. These are bookkept in `dirs`
    // for the drawer's existingDirs badges.
    const inputDirSet = new Set(dirs);
    const freshDirs = addedDirNames;

    const nextRows = [...get().rows, ...dedupedNewRows];
    // Dedupe nextDirs against existing dirs (re-adding the same dir
    // name twice should still only result in one badge).
    const existingDirSet = new Set(get().dirs);
    const nextDirs = [...get().dirs];
    for (const d of freshDirs) {
      if (!existingDirSet.has(d)) {
        existingDirSet.add(d);
        nextDirs.push(d);
      }
    }
    // Guard against the (unusual) case where dirs contains duplicates.
    void inputDirSet;

    set({ rows: nextRows, dirs: nextDirs });
    persist({ rows: nextRows, dirs: nextDirs });

    // Background-fetch /api/music_id3/ for each newly-added row so the
    // table immediately renders title/artist/album instead of showing
    // bare filenames until the user clicks each one. Fire-and-forget.
    //
    // Contract with the boot hydration below: hydrateTagsBatched
    // writes the row's musicInfo ONLY when the response has at least
    // one non-null field, so a row with genuinely empty tags stays in
    // the `needsMusicInfoRefetch` set and gets retried on the next page
    // load. If a future change to hydrateTags wrote unconditionally,
    // that retry would stop firing and the user would lose it. See
    // lib/hydrateTags.ts block comment steps 3-4 for the rationale.
    void hydrateTagsBatched(
      dedupedNewRows.map((r) => ({ id: r.id, fullPath: r.fullPath })),
      (id, info) => useLibraryStore.getState().setMusicInfo(id, info),
    );

    return {
      // Directory counts on both sides — the drawer's notice reports
      // directories, and the previous pair mixed files with dirs.
      added: addedDirs,
      skipped: skippedDirs,
    };
  },

  search: (s) => set({ query: s }),

  remove: (id) => {
    const nextRows = get().rows.filter((r) => r.id !== id);
    const next: PersistedShape = { rows: nextRows, dirs: get().dirs };
    set({ rows: nextRows });
    persist(next);
    // Note: we don't prune `dirs` here — a directory that no longer contains
    // any files in the library still counts as "added" so the drawer badge
    // stays consistent. Use clear() for a full reset.
  },

  clear: () => {
    set({ rows: [], dirs: [], query: '' });
    removeString(STORAGE_KEY);
  },

  getFiltered: () => {
    const { rows, query } = get();
    if (!query.trim()) return rows;
    const needle = query.toLowerCase();
    return rows.filter((r) => r.fileName.toLowerCase().includes(needle));
  },

  setMusicInfo: (id, info) => {
    set((s) => {
      if (!s.rows.some((r) => r.id === id)) return s;
      const nextRows = s.rows.map((r) =>
        r.id === id
          ? { ...r, musicInfo: { ...(r.musicInfo ?? {}), ...info } }
          : r,
      );
      persist({ rows: nextRows, dirs: s.dirs });
      return { rows: nextRows };
    });
  },

  renameRow: (oldPath, newPath, newFileName) => {
    set((s) => {
      if (!s.rows.some((r) => r.id === oldPath)) return s;
      if (newPath !== oldPath && s.rows.some((r) => r.id === newPath)) return s;
      const nextRows = s.rows.map((r) =>
        r.id === oldPath
          ? { ...r, id: newPath, fullPath: newPath, fileName: newFileName }
          : r,
      );
      persist({ rows: nextRows, dirs: s.dirs });
      return { rows: nextRows };
    });
  },
}));

// Boot: re-fetch tags for persisted rows that need a musicInfo refresh
// (empty cache, or lightweight tags without a usable cover after
// stripHeavy dropped data-URI artwork). Fire-and-forget.
(() => {
  const need = useLibraryStore
    .getState()
    .rows.filter((r) => needsMusicInfoRefetch(r.musicInfo))
    .map((r) => ({ id: r.id, fullPath: r.fullPath }));
  if (need.length === 0) return;
  void hydrateTagsBatched(need, (id, info) =>
    useLibraryStore.getState().setMusicInfo(id, info),
  );
})();

// Type exports for consumers.
export type { LibraryState };
