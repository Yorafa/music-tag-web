// Zustand store owning browser-related state — directory browsing state
// + sort prefs + per-file id3 cache. Once Plan A's tree migration
// completes this is the SOLE home for these fields; useAppStore.ts
// (which today owns them) is deleted in Step 9 of the plan.
//
// Persistence behaviour matches the old useAppStore contract verbatim:
//   - sort pref (field + dir) is round-tripped to localStorage under
//     SORT_STORAGE_KEY so a page reload keeps the user's choice.
//   - the legacy per-directory sort key (LEGACY_SORT_KEY) is one-time
//     emptied at module-load so upgrade-era users don't accumulate
//     orphan entries. Both behaviors copy what useAppStore did and
//     are intentionally kept here so the refactor is behavioural-zero.
//
// field validation reuses the same Set membership as the old
// implementation; new SORT values are gated through VALID_FIELDS.

import { create } from 'zustand';
import { readJson, readString, writeJson, writeString } from '@/utils/persist';
import type { FileNode, MusicTagInfo, SortField, SortDir } from '@/types';

interface BrowserState {
  /** Relative path under MUSIC_DIR: '' represents the root, 'foo/bar'
   *  is a subdirectory. Reads from / writes to /api/file_list/, /api/
   *  music_id3/, and the /media/<path> static route all flow through
   *  `utils.SafeJoin(MUSIC_DIR, filePath)`. Kept relative everywhere
   *  so the absolute path on disk is a single source of truth. */
  filePath: string;
  /** Children of the directory currently shown — populated by the
   *  /api/file_list/ fetch in `AppShell.onLoadFiles`. */
  treeData: FileNode[];
  /** True while a /api/file_list/ request is in flight. UI surfaces
   *  this on browser-side affordances (refresh button). */
  isLoading: boolean;
  sortField: SortField;
  sortDir: SortDir;
  /** Per-file id3 cache — key is `${filePath}|${file.name}` (NOT
   *  FileNode.id, since FileNode.id is assigned sequentially per
   *  /api/file_list/ response and collides across directories). */
  id3Cache: Record<string, Partial<MusicTagInfo>>;

  setFilePath: (path: string) => void;
  setTreeData: (data: FileNode[]) => void;
  setIsLoading: (v: boolean) => void;
  setSort: (field: SortField, dir: SortDir) => void;
  setSortField: (field: SortField) => void;
  setSortDir: (
    updater: SortDir | ((prev: SortDir) => SortDir),
  ) => void;
  setId3CacheEntry: (key: string, info: Partial<MusicTagInfo>) => void;
}

const SORT_STORAGE_KEY = 'fileBrowser.sort';
const LEGACY_SORT_KEY = 'fileBrowser.sortByPath';
const VALID_FIELDS: ReadonlyArray<SortField> = ['name', 'size', 'update_time'];
const VALID_DIRS: ReadonlyArray<SortDir> = ['asc', 'desc'];

type SortPref = { field: SortField; dir: SortDir };
const isValidSortPref = (v: unknown): v is SortPref =>
  !!v &&
  typeof v === 'object' &&
  VALID_FIELDS.includes((v as SortPref).field) &&
  VALID_DIRS.includes((v as SortPref).dir);

/** Compute initial sort synchronously so first paint already matches
 *  the saved preference — no flicker between mount and the post-
 *  hydration re-render. Mirrors the original useAppStore. */
const INITIAL_SORT: SortPref = (() => {
  const saved = readJson<unknown>(SORT_STORAGE_KEY);
  return isValidSortPref(saved) ? saved : { field: 'name', dir: 'desc' };
})();

/** One-time cleanup of the legacy per-directory sort map. Wrapped in
 *  `typeof window` guard so SSR or document-less contexts skip the
 *  side effect. */
if (typeof window !== 'undefined') {
  const legacyValue = readString(LEGACY_SORT_KEY);
  if (legacyValue && legacyValue !== '') {
    writeString(LEGACY_SORT_KEY, '');
  }
}

/** Helper: persist the field+dir pair to localStorage. Same write
 *  happens inside setSort (atomic) and inside the individual
 *  setSortField / setSortDir setters (each commits the new pref
 *  using the OTHER side's current value). The asymmetric behaviour
 *  is intentional — keeping the pref in sync across any flapping of
 *  either side. */
function persistSort(field: SortField, dir: SortDir): void {
  writeJson<SortPref>(SORT_STORAGE_KEY, { field, dir });
}

export const useBrowserStore = create<BrowserState>((set, get) => ({
  filePath: '',
  treeData: [],
  isLoading: false,
  sortField: INITIAL_SORT.field,
  sortDir: INITIAL_SORT.dir,
  id3Cache: {},

  setFilePath: (path) => set({ filePath: path }),

  setTreeData: (data) => set({ treeData: data }),

  setIsLoading: (v) => set({ isLoading: v }),

  setSort: (field, dir) => {
    persistSort(field, dir);
    set({ sortField: field, sortDir: dir });
  },

  setSortField: (field) => {
    persistSort(field, get().sortDir);
    set({ sortField: field });
  },

  setSortDir: (updater) => {
    const current = get().sortDir;
    const dir = typeof updater === 'function' ? updater(current) : updater;
    persistSort(get().sortField, dir);
    set({ sortDir: dir });
  },

  setId3CacheEntry: (key, info) =>
    set((state) => ({ id3Cache: { ...state.id3Cache, [key]: info } })),
}));

/** Imperative action setters exposed as a plain object alongside
 *  the hook. Routes every write through `useBrowserStore.getState()
 *  .<action>` so the embedded persistence logic in `setSort*` runs
 *  unchanged — see the inline docs on those setters for why the
 *  sort prefs commit through `persistSort(...)`. Keep consumers on
 *  this object when they need a one-shot write that doesn't need to
 *  re-render anything (e.g. dispatching the tree fetch result from
 *  a then-callback). */
export const browserActions = {
  setFilePath: (path: string): void =>
    useBrowserStore.getState().setFilePath(path),
  setTreeData: (data: FileNode[]): void =>
    useBrowserStore.getState().setTreeData(data),
  setIsLoading: (v: boolean): void =>
    useBrowserStore.getState().setIsLoading(v),
  setSort: (field: SortField, dir: SortDir): void =>
    useBrowserStore.getState().setSort(field, dir),
  setSortField: (field: SortField): void =>
    useBrowserStore.getState().setSortField(field),
  setSortDir: (
    updater: SortDir | ((prev: SortDir) => SortDir),
  ): void =>
    useBrowserStore.getState().setSortDir(updater),
  setId3CacheEntry: (key: string, info: Partial<MusicTagInfo>): void =>
    useBrowserStore.getState().setId3CacheEntry(key, info),
};
