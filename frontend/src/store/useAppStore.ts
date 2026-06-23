import { create } from 'zustand';
import type { SongInfo, MusicTagInfo, MusicSource, FileNode, SortField, SortDir } from '@/types';
import { readJson, readString, writeJson, writeString } from '@/utils/persist';

interface AppState {
  // File browser
  filePath: string;
  treeData: FileNode[];
  selectedFile: string | null;
  fullPath: string;
  checkedIds: number[];

  // Tag editor
  musicInfo: Partial<MusicTagInfo>;
  musicInfoManual: Partial<MusicTagInfo>;
  showFields: string[];

  // Search
  resource: MusicSource;
  songList: SongInfo[];
  fadeShowDetail: boolean;

  // Batch
  selectAutoMode: 'hard' | 'simple';
  sourceList: string[];
  tidyFormData: { root_path: string; first_dir: string; second_dir: string };

  // UI
  isLoading: boolean;
  editorOpen: boolean;

  // Sort (shared globally — FileBrowser dropdown drives it; SearchResults reflects it).
  sortField: SortField;
  sortDir: SortDir;

  // Per-file id3 cache keyed by FileNode.id. SearchResults hydrates lazily;
  // remounts read from this cache instead of refetching.
  id3Cache: Record<string, Partial<MusicTagInfo>>;

  // Actions
  setFilePath: (path: string) => void;
  setTreeData: (data: FileNode[]) => void;
  setSelectedFile: (file: string | null) => void;
  setFullPath: (path: string) => void;
  setCheckedIds: (ids: number[]) => void;
  setMusicInfo: (info: Partial<MusicTagInfo>) => void;
  updateMusicInfo: (key: string, value: unknown) => void;
  setShowFields: (fields: string[]) => void;
  setResource: (r: MusicSource) => void;
  setSongList: (songs: SongInfo[]) => void;
  setFadeShowDetail: (v: boolean) => void;
  setSelectAutoMode: (m: 'hard' | 'simple') => void;
  setSourceList: (l: string[]) => void;
  setTidyFormData: (d: { root_path: string; first_dir: string; second_dir: string }) => void;
  setIsLoading: (v: boolean) => void;
  setEditorOpen: (v: boolean) => void;
  setSortField: (field: SortField) => void;
  setSortDir: (dir: SortDir | ((prev: SortDir) => SortDir)) => void;
  /** Batch update — single localStorage write for field+dir together. */
  setSort: (field: SortField, dir: SortDir) => void;
  setId3CacheEntry: (key: string, info: Partial<MusicTagInfo>) => void;
}

const defaultMusicInfo: Partial<MusicTagInfo> = {
  genre: '流行',
  is_save_lyrics_file: false,
  is_save_album_cover: false,
};

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

// Compute initial sort synchronously so first paint already matches the saved
// preference — no flicker between mount and the post-hydration re-render.
const INITIAL_SORT: SortPref = (() => {
  const saved = readJson<unknown>(SORT_STORAGE_KEY);
  return isValidSortPref(saved) ? saved : { field: 'name', dir: 'desc' };
})();

// One-time cleanup of the previous per-directory sort map so it doesn't
// accumulate orphan entries for users who upgraded.
// One-time cleanup of the previous per-directory sort map so it doesn't
// accumulate orphan entries for users who upgraded. The check is gated on a
// non-empty value — `''` already satisfies `!== null`, so without a truthiness
// guard the cleanup would re-fire on every HMR module reload.
if (typeof window !== 'undefined') {
  const legacyValue = readString(LEGACY_SORT_KEY);
  if (legacyValue && legacyValue !== '') {
    writeString(LEGACY_SORT_KEY, '');
  }
}

export const useAppStore = create<AppState>((set) => ({
  filePath: '/app/media/',
  treeData: [],
  selectedFile: null,
  fullPath: '',
  checkedIds: [],

  musicInfo: { ...defaultMusicInfo },
  musicInfoManual: { ...defaultMusicInfo },
  showFields: ['filename', 'artist', 'album', 'albumartist', 'genre', 'year', 'lyrics', 'comment', 'album_img'],

  resource: (localStorage.getItem('resource') as MusicSource) || 'netease',
  songList: [],
  fadeShowDetail: false,

  selectAutoMode: 'hard',
  sourceList: [],
  tidyFormData: { root_path: '/app/media/', first_dir: 'artist', second_dir: '' },

  isLoading: false,
  editorOpen: false,

  sortField: INITIAL_SORT.field,
  sortDir: INITIAL_SORT.dir,

  id3Cache: {},

  setFilePath: (path) => set({ filePath: path }),
  setTreeData: (data) => set({ treeData: data }),
  setSelectedFile: (file) => set({ selectedFile: file }),
  setFullPath: (path) => set({ fullPath: path }),
  setCheckedIds: (ids) => set({ checkedIds: ids }),
  setMusicInfo: (info) => set({ musicInfo: info }),
  updateMusicInfo: (key, value) => set((s) => ({ musicInfo: { ...s.musicInfo, [key]: value } })),
  setShowFields: (fields) => set({ showFields: fields }),
  setResource: (r) => { localStorage.setItem('resource', r); set({ resource: r }); },
  setSongList: (songs) => set({ songList: songs }),
  setFadeShowDetail: (v) => set({ fadeShowDetail: v }),
  setSelectAutoMode: (m) => set({ selectAutoMode: m }),
  setSourceList: (l) => set({ sourceList: l }),
  setTidyFormData: (d) => set({ tidyFormData: d }),
  setIsLoading: (v) => set({ isLoading: v }),
  setEditorOpen: (v) => set({ editorOpen: v }),
  setSortField: (field) => {
    set((state) => {
      writeJson<SortPref>(SORT_STORAGE_KEY, { field, dir: state.sortDir });
      return { sortField: field };
    });
  },
  setSortDir: (updater) => {
    set((state) => {
      const dir = typeof updater === 'function' ? updater(state.sortDir) : updater;
      writeJson<SortPref>(SORT_STORAGE_KEY, { field: state.sortField, dir });
      return { sortDir: dir };
    });
  },
  setSort: (field, dir) => {
    set(() => {
      writeJson<SortPref>(SORT_STORAGE_KEY, { field, dir });
      return { sortField: field, sortDir: dir };
    });
  },
  setId3CacheEntry: (key, info) => {
    set((state) => ({ id3Cache: { ...state.id3Cache, [key]: info } }));
  },
}));
