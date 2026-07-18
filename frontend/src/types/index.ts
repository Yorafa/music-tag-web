export interface SongInfo {
  id: string;
  name: string;
  artist: string;
  artist_id: string;
  album: string;
  album_id: string;
  album_img: string;
  year: string;
  source?: string;
  score?: number;
  /** Lyric body for a scrape candidate — populated whenever the scrape
   *  call answered. Both keys are accepted because upstream is mixed: some
   *  plugins emit `lyric` (singular, e.g. Kuwo's [cover] appendix), others
   *  emit `lyrics`. ScrapeResults normalises across both with a fallback
   *  read in the per-card apply-all handler. */
  lyric?: string;
  lyrics?: string;
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

// backend tag.go::FetchLyric returns SuccessData(c, lyric) where `lyric` is
// a plain string (may include Chinese \"未找到歌词\" prefix on the not-found
// path). The audit flagged a stale {lyric, cover?} object shape that no
// caller ever used. Keep this as a string alias so call sites can still
// import a named type for self-documentation without breaking wire shape.
export type LyricText = string;

/** @deprecated use `LyricText`. Kept here so any straggler import still
 *  compiles; safe to delete once a repo-wide grep confirms zero
 *  remaining `LyricResult` references. */
export type LyricResult = LyricText;

/** Which resource to use for the next scrape from /api/fetch_id3_by_title/.
 *  Mirrors the union of: every tag-source plugin (see SearchSource) plus
 *  the synthetic `smart_tag` resource. `smart_tag` is NOT a registered
 *  plugin and will never appear in /api/sources/, but it IS a valid
 *  fetch_id3_by_title request value that triggers the backend's
 *  SmartTagSearch logic; TagEditor exposes it as '智能刮削' and
 *  useAppStore.resource persists the user's choice. Don't reuse this
 *  type for plugin-only contexts — use a stricter PluginSource type
 *  there. */
export type MusicSource = 'netease' | 'qmusic' | 'kugou' | 'kuwo' | 'migu' | 'musicbrainz' | 'acoustid' | 'smart_tag';
export type SelectMode = 'simple' | 'hard';

/** Mirrors MusicSource but excludes acoustid/smart_tag which only return ID3
 *  via fingerprinting; they don't have a "search by title" UI. Also
 *  excludes `youtube` — YouTube is a DownloadSource on the backend, not a
 *  TagSource, so /api/search_music/ would silently fan out to nothing for
 *  that name. The audit flagged adding `youtube` here as a BROKEN entry:
 *  user-visible empty-results with no error. Use youtube results through
 *  /api/youtube_search/ directly (no frontend wrapper today; that's a
 *  separate feature). Returned source names from the backend are plain
 *  strings — cast to SearchSource when you want autocomplete narrowing. */
export type SearchSource = 'netease' | 'qmusic' | 'kugou' | 'kuwo' | 'migu' | 'musicbrainz';

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
  /** Mirror of proto PluginInfoResponse.supports_audio_url — true if the
   *  source can answer `GetAudioURL` with a playable upstream URL. Musicbrainz
   *  + AcoustID report false; the 5 streaming plugins (kuwo / netease /
   *  kugou / migu / qmusic) report true once their server-side GetAudioURL
   *  RPC is wired through `/api/sources/`. Always `false` for `kind:
   *  "download"`. PlayButton reads this to decide between direct `<audio>`
   *  playback (when the per-song url is already populated) and a
   *  `/api/stream?src=&id=` fallback (when the plugin returned empty url
   *  on its GetAudioURL call). */
  supports_audio_url?: boolean;
  default_on: boolean;
}

export interface SearchResult {
  id: string;
  source: string; // widened from SearchSource union so a new plugin (Stage B/C)
                  // doesn't need a frontend type-checker round-trip.
  name: string;
  title?: string;
  artist: string;
  album?: string;
  cover?: string;
  duration?: string | number;
  album_id?: string;
  // Wire-shape match against `internal/plugin/interface.go::Song.Mid`
  // (json tag "mid"). The old `song_mid?` name had no readers; this
  // rename is purely payload-shape, not behavioural.
  mid?: string;
  url?: string;
  thumbnail?: string;
}

export interface SearchPagination {
  pages: Record<string, number>;
  has_more: Record<string, boolean>;
}

/** Scrape-side lifecycle for a single Worklist row.
 *
 *  - `pending`: freshly added via DirPickerDrawer; not yet scraped.
 *  - `scraped`: at least one successful tag-write (batch auto OR manual
 *    candidate apply) flipped the row to this state.
 *  - `failed`: a batch auto-scrape returned failure for this row.
 *
 *  Future non-breaking extension: append `'scraping'` once the asynq
 *  scraper emits in-flight updates. Don't reorder — old values keep
 *  filtering correctly.
 *
 *  Frozen contract with Plan B (β-side). See docs/plans/frontend-layout-
 *  refactor/Plan-A-Scrape-Workflow.md §Handshake. */
export type ScrapeStatus = 'pending' | 'scraped' | 'failed';

/** One row in the scrape Worklist. The worklist replaces the old
 *  three-column ScrapeView layout (Toolbar + FileBrowser + SearchResults)
 *  with a flat list of files-to-scrape plus a per-row status badge.
 *
 *  `id` carries the row's fullPath (relative to MUSIC_DIR, including the
 *  filename) so two re-adds of the same directory collapse onto the same
 *  row even though the backend assigns short-lived FileNode.id values per
 *  `/api/file_list/` response — those IDs are sequential within a
 *  request and collide across directories (see expandDirs.ts notes).
 *
 *  `fullPath` is duplicated for convenience: callers that already hold
 *  the id can derive it; callers iterating rows for display read it
 *  directly.
 *
 *  `musicInfo` is a lazy cache of the most-recent `/api/music_id3/`
 *  fetch for this file. Populated when the row's tag editor dialog is
 *  opened; reused on remount so a per-row thumbnail + first-line
 *  preview paint instantly instead of refetching. */
export interface WorklistRow {
  id: string;
  fullPath: string;
  fileName: string;
  status: ScrapeStatus;
  musicInfo?: Partial<MusicTagInfo>;
}
