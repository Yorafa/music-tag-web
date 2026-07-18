// Zustand store owning the song-detail editor's state. Mode-agnostic —
// it lives at AppShell root level via its consumer components, so a
// mode switch (play ↔ scrape) does NOT clear its fields. The detail
// Dialog is rendered above whichever mode body is active, so user-
// visible state persists across mode toggles (DESIGN.md §A1 pick).
//
// State shape matches the union of fields useAppStore.ts used to
// expose on the editor slice, minus the bits that no longer fit
// (checkedIds / selectAutoMode / tidyFormData / sourceList) — those
// moved to useWorklistStore (scrape-side) or are dropped entirely
// (smoke from the abandoned Django `tracks.album_cover` fields).
//
// `resource` is the user's preferred scrape tag-source (the dropdown
// in TagEditor's "标签源" picker). Persists to the SAME localStorage
// key the old useAppStore.setResource wrote to (`"resource"` directly,
// not under the typed helpers). Don't migrate that key — any existing
// user's choice carries over, and a future server-side preference
// store can supersede it without a localStorage collision.

import { create } from 'zustand';
import type {
  MusicSource,
  MusicTagInfo,
  SongInfo,
} from '@/types';

const defaultMusicInfo: Partial<MusicTagInfo> = {
  genre: '流行',
  is_save_lyrics_file: false,
  is_save_album_cover: false,
};

/** The persisted "标签源" selection lives in localStorage under
 *  the literal key "resource" (matching the historical useAppStore
 *  setResource() contract). The read path narrows to MusicSource
 *  on a known value; unknown / corrupt values fall through to the
 *  default netease. */
function readStoredResource(): MusicSource {
  if (typeof window === 'undefined') return 'netease';
  const raw = localStorage.getItem('resource');
  const allowed: MusicSource[] = [
    'netease', 'qmusic', 'kugou', 'kuwo', 'migu',
    'musicbrainz', 'acoustid', 'smart_tag',
  ];
  return (allowed.includes(raw as MusicSource)
    ? (raw as MusicSource)
    : 'netease');
}

interface EditorState {
  /** filename (no path) of the currently-edited file. Distinct from
   *  fullPath so the editor can show the basename in titles while
   *  keeping the relative path for backend calls. */
  selectedFile: string | null;
  /** Relative path under MUSIC_DIR: `'foo/bar/song.flac'`. Drives
   *  /api/music_id3/, /api/update_id3/, /api/fetch_id3_by_title/. */
  fullPath: string;
  musicInfo: Partial<MusicTagInfo>;
  /** Manual-edit copy of musicInfo — Diff between this and musicInfo
   *  powers "user-edited vs scraped" hints in TagEditor. Field
   *  precision is field-by-field — callers can read the merge by
   *  inspecting which keys differ. */
  musicInfoManual: Partial<MusicTagInfo>;
  /** Field show-list — TagEditor iterates this in its renderField
   *  switch. Default covers the canonical set; users can toggle
   *  fields in a future settings panel. */
  showFields: string[];
  /** Search results from /api/fetch_id3_by_title/. Surfaces in the
   *  detail Dialog through ScrapeResults; cleared on every new
   *  editor session so a stale list doesn't leak across files. */
  songList: SongInfo[];
  /** Controls the wide-narrow Dialog toggle (AppShell reads it to
   *  switch between 64rem and 100rem max-width). TagEditor sets it
   *  true once /api/fetch_id3_by_title/ resolves with at least one
   *  candidate. */
  fadeShowDetail: boolean;
  editorOpen: boolean;
  /** The user's preferred scrape tag-source (`'netease'` etc.).
   *  Drives both:
   *     - TagEditor's <Select> shown value
   *     - the `resource` param passed to /api/fetch_id3_by_title/
   *  Persists to localStorage so the next session remembers. */
  resource: MusicSource;

  setSelectedFile: (file: string | null) => void;
  setFullPath: (path: string) => void;
  setMusicInfo: (info: Partial<MusicTagInfo>) => void;
  updateMusicInfo: (key: string, value: unknown) => void;
  setShowFields: (fields: string[]) => void;
  setSongList: (songs: SongInfo[]) => void;
  setFadeShowDetail: (v: boolean) => void;
  setEditorOpen: (v: boolean) => void;
  setResource: (r: MusicSource) => void;
}

export const useEditorStore = create<EditorState>((set) => ({
  selectedFile: null,
  fullPath: '',
  musicInfo: { ...defaultMusicInfo },
  musicInfoManual: { ...defaultMusicInfo },
  showFields: [
    'filename',
    'artist',
    'album',
    'albumartist',
    'genre',
    'year',
    'lyrics',
    'comment',
    'album_img',
  ],
  songList: [],
  fadeShowDetail: false,
  editorOpen: false,
  resource: readStoredResource(),

  setSelectedFile: (file) => set({ selectedFile: file }),
  setFullPath: (path) => set({ fullPath: path }),

  setMusicInfo: (info) => set({ musicInfo: info }),

  updateMusicInfo: (key, value) =>
    set((s) => ({ musicInfo: { ...s.musicInfo, [key]: value } })),

  setShowFields: (fields) => set({ showFields: fields }),

  setSongList: (songs) => set({ songList: songs }),

  setFadeShowDetail: (v) => set({ fadeShowDetail: v }),

  setEditorOpen: (v) => set({ editorOpen: v }),

  setResource: (r) => {
    if (typeof window !== 'undefined') {
      localStorage.setItem('resource', r);
    }
    set({ resource: r });
  },
}));

/** Imperative action setters exposed as a plain object alongside
 *  the hook. Call-site pattern:
 *
 *     const songList = useEditorStore(s => s.songList);
 *     // …
 *     editorActions.setSongList([]);  // no subscription churn
 *
 *  Routes every write through `useEditorStore.getState().<action>`
 *  so the action body — including the resource-localStorage write
 *  inside setResource — runs with the existing semantics. Keeps the
 *  component import surface small (`{ useEditorStore, editorActions }`)
 *  while still letting consumers that need a one-shot set stay
 *  outside the subscription tree. */
export const editorActions = {
  setSelectedFile: (file: string | null): void =>
    useEditorStore.getState().setSelectedFile(file),
  setFullPath: (path: string): void =>
    useEditorStore.getState().setFullPath(path),
  setMusicInfo: (info: Partial<MusicTagInfo>): void =>
    useEditorStore.getState().setMusicInfo(info),
  updateMusicInfo: (key: string, value: unknown): void =>
    useEditorStore.getState().updateMusicInfo(key, value),
  setShowFields: (fields: string[]): void =>
    useEditorStore.getState().setShowFields(fields),
  setSongList: (songs: SongInfo[]): void =>
    useEditorStore.getState().setSongList(songs),
  setFadeShowDetail: (v: boolean): void =>
    useEditorStore.getState().setFadeShowDetail(v),
  setEditorOpen: (v: boolean): void =>
    useEditorStore.getState().setEditorOpen(v),
  setResource: (r: MusicSource): void =>
    useEditorStore.getState().setResource(r),
};
