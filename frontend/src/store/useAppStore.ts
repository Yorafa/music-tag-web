import { create } from 'zustand';
import type { SongInfo, MusicTagInfo, MusicSource, FileNode } from '@/types';

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
  reloadImg: boolean;

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

  // Actions
  setFilePath: (path: string) => void;
  setTreeData: (data: FileNode[]) => void;
  setSelectedFile: (file: string | null) => void;
  setFullPath: (path: string) => void;
  setCheckedIds: (ids: number[]) => void;
  setMusicInfo: (info: Partial<MusicTagInfo>) => void;
  updateMusicInfo: (key: string, value: unknown) => void;
  setShowFields: (fields: string[]) => void;
  setReloadImg: (v: boolean) => void;
  setResource: (r: MusicSource) => void;
  setSongList: (songs: SongInfo[]) => void;
  setFadeShowDetail: (v: boolean) => void;
  setSelectAutoMode: (m: 'hard' | 'simple') => void;
  setSourceList: (l: string[]) => void;
  setTidyFormData: (d: { root_path: string; first_dir: string; second_dir: string }) => void;
  setIsLoading: (v: boolean) => void;
}

const defaultMusicInfo: Partial<MusicTagInfo> = {
  genre: '流行',
  is_save_lyrics_file: false,
  is_save_album_cover: false,
};

export const useAppStore = create<AppState>((set) => ({
  filePath: '/app/media/',
  treeData: [],
  selectedFile: null,
  fullPath: '',
  checkedIds: [],

  musicInfo: { ...defaultMusicInfo },
  musicInfoManual: { ...defaultMusicInfo },
  showFields: ['filename', 'artist', 'album', 'albumartist', 'genre', 'year', 'lyrics', 'comment', 'album_img'],
  reloadImg: true,

  resource: (localStorage.getItem('resource') as MusicSource) || 'netease',
  songList: [],
  fadeShowDetail: false,

  selectAutoMode: 'hard',
  sourceList: [],
  tidyFormData: { root_path: '/app/media/', first_dir: 'artist', second_dir: '' },

  isLoading: false,

  setFilePath: (path) => set({ filePath: path }),
  setTreeData: (data) => set({ treeData: data }),
  setSelectedFile: (file) => set({ selectedFile: file }),
  setFullPath: (path) => set({ fullPath: path }),
  setCheckedIds: (ids) => set({ checkedIds: ids }),
  setMusicInfo: (info) => set({ musicInfo: info }),
  updateMusicInfo: (key, value) => set((s) => ({ musicInfo: { ...s.musicInfo, [key]: value } })),
  setShowFields: (fields) => set({ showFields: fields }),
  setReloadImg: (v) => set({ reloadImg: v }),
  setResource: (r) => { localStorage.setItem('resource', r); set({ resource: r }); },
  setSongList: (songs) => set({ songList: songs }),
  setFadeShowDetail: (v) => set({ fadeShowDetail: v }),
  setSelectAutoMode: (m) => set({ selectAutoMode: m }),
  setSourceList: (l) => set({ sourceList: l }),
  setTidyFormData: (d) => set({ tidyFormData: d }),
  setIsLoading: (v) => set({ isLoading: v }),
}));
