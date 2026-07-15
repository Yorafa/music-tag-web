export interface SongInfo {
  id: string;
  name: string;
  artist: string;
  artist_id: string;
  album: string;
  album_id: string;
  album_img: string;
  year: string;
  resource?: string;
  score?: number;
}

export interface FileNode {
  id: number;
  name: string;
  title: string;
  icon: string;
  state: string;
  size?: number;
  update_time?: string;
  children?: FileNode[];
  expanded?: boolean;
}

export type SortField = 'name' | 'size' | 'update_time';
export type SortDir = 'asc' | 'desc';

export interface MusicTagInfo {
  title: string;
  filename: string;
  artist: string;
  album: string;
  albumartist: string;
  genre: string;
  language: string;
  year: string;
  lyrics: string;
  comment: string;
  album_img: string;
  album_type: string;
  discnumber: string;
  tracknumber: string;
  duration: string;
  bit_rate: string;
  size: string;
  artwork: string;
  artwork_w: number;
  artwork_h: number;
  artwork_size: number;
  is_save_lyrics_file: boolean;
  is_save_album_cover: boolean;
}

export interface LyricResult {
  lyric: string;
  cover?: string;
}

export type MusicSource = 'netease' | 'qmusic' | 'kugou' | 'kuwo' | 'migu' | 'musicbrainz' | 'acoustid' | 'smart_tag';
export type SelectMode = 'simple' | 'hard';

/** Mirrors MusicSource but excludes acoustid/smart_tag which only return ID3
 *  via fingerprinting; they don't have a "search by title" UI. Used as a
 *  type-hint for places that want autocomplete on a known good string.
 *  Returned source names from the backend are plain strings — cast to
 *  SearchSource when you want autocomplete narrowing. */
export type SearchSource = 'netease' | 'qmusic' | 'kugou' | 'kuwo' | 'migu' | 'musicbrainz' | 'youtube';

/** SourceInfo mirrors GET /api/sources/ — Stage A of
 *  docs/plugable-plugins.md. The backend emits this list from the plugin
 *  registry, so adding a new built-in plugin only requires server-side
 *  registration. */
export interface SourceInfo {
  name: string;
  display_name: string;
  kind: 'tag' | 'download';
  searchable: boolean;
  lyric: boolean;
  /** Distinguishes sources that answer `FetchID3ByTitle` (which the
   *  TagEditor uses for tag-by-title scraping) from search-only /
   *  download-only sources. Always `false` for `kind: "download"`. */
  supports_id3: boolean;
  default_on: boolean;
}

export interface SearchResult {
  id: string;
  source: string; // widened from SearchSource union so a new plugin (Stage B/C)
                  // doesn't need a frontend type-checker round-trip.
  name: string;
  title?: string;
  artist: string;
  channel?: string;
  album?: string;
  cover?: string;
  duration?: string | number;
  album_id?: string;
  song_mid?: string;
  url?: string;
  thumbnail?: string;
}

export interface SearchPagination {
  pages: Record<string, number>;
  has_more: Record<string, boolean>;
}
