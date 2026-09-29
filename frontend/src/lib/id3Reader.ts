// Server-side ID3 / Vorbis-comment / FLAC-comment reader glue.
//
// POST /api/music_id3/ → backend internal/tag.Read (dhowden/tag).
// Why backend (not browser Range + music-metadata):
//
//   - Batch hydrate after 添加音乐 can hit 100+ files. Server-side reads
//     stay on the disk host and return compact JSON; browser Range
//     would transfer ~256KB per file just to parse a few string fields.
//   - tag.Read already powers worker batch scrape / tidy / sidecars —
//     one parser, one field shape.
//   - JWT still flows via api/client axios interceptor (Authorization:
//     JWT <token>) — no manual fetch header path.
//
// Returns Partial<MusicTagInfo> so hydrateTags keeps the same
// cacheHasAnyValue contract as before.

import { getMusicId3 } from '@/api/client';
import type { MusicTagInfo } from '@/types';

/** Split a MUSIC_DIR-relative fullPath into {file_path, file_name}
 *  for the music_id3 request body.
 *    'a/b/song.flac' → { filePath: 'a/b', fileName: 'song.flac' }
 *    'song.flac'     → { filePath: '',    fileName: 'song.flac' }
 */
export function splitFullPath(fullPath: string): { filePath: string; fileName: string } {
  const parts = fullPath.split('/').filter(Boolean);
  const fileName = parts.pop() ?? fullPath;
  const filePath = parts.join('/');
  return { filePath, fileName };
}

/** Read embedded tags via POST /api/music_id3/.
 *  Throws on transport failure or result:false so caller's try/catch
 *  can toast; returns {} on empty success (lyric-only / missing tags).
 *
 *  `opts.includeArtwork` defaults to false HERE, unlike the raw
 *  getMusicId3 client, because this is the batch-hydration path. An
 *  embedded cover is a full-resolution scan: measured against real
 *  libraries, a single music_id3 response runs 3–15 MB. The worklist
 *  draws 32-pixel thumbnails, so asking for the artwork on 500 rows moved
 *  gigabytes to render thumbnails that stripHeavyFromRows then discarded
 *  before persisting. Covers for the table come from getAlbumCoverUrl,
 *  one visible row at a time.
 *
 *  A caller that genuinely needs the bytes — the tag editor, which renders
 *  the cover large — passes { includeArtwork: true } and gets the old
 *  whole-record response. */
export async function readTagsFromPath(
  fullPath: string,
  opts: { includeArtwork?: boolean } = {},
): Promise<Partial<MusicTagInfo>> {
  const { filePath, fileName } = splitFullPath(fullPath);
  const res = await getMusicId3(filePath, fileName, {
    includeArtwork: opts.includeArtwork ?? false,
  });
  if (!res || res.result === false) {
    throw new Error(res?.message || 'music_id3 failed');
  }
  const data = res.data;
  // Success(nil) serialises as [] — treat as empty tags.
  if (data == null || Array.isArray(data) || typeof data !== 'object') {
    return {};
  }
  return normalizeMusicTagInfo(data as Record<string, unknown>);
}

/** Coerce backend map (ints for year/size/duration/bit_rate) into the
 *  string-heavy MusicTagInfo shape the editor / resolveCoverSrc expect. */
function normalizeMusicTagInfo(raw: Record<string, unknown>): Partial<MusicTagInfo> {
  const out: Partial<MusicTagInfo> = {};
  const str = (k: string): string | undefined => {
    const v = raw[k];
    if (v == null || v === '') return undefined;
    return String(v);
  };
  const num = (k: string): number | undefined => {
    const v = raw[k];
    if (v == null || v === '') return undefined;
    const n = typeof v === 'number' ? v : Number(v);
    return Number.isFinite(n) ? n : undefined;
  };

  const title = str('title');
  if (title) out.title = title;
  const artist = str('artist');
  if (artist) out.artist = artist;
  const album = str('album');
  if (album) out.album = album;
  const albumartist = str('albumartist');
  if (albumartist) out.albumartist = albumartist;
  const genre = str('genre');
  if (genre) out.genre = genre;
  const language = str('language');
  if (language) out.language = language;
  const year = str('year');
  if (year && year !== '0') out.year = year;
  const lyrics = str('lyrics');
  if (lyrics) out.lyrics = lyrics;
  const comment = str('comment');
  if (comment) out.comment = comment;
  const albumType = str('album_type');
  if (albumType) out.album_type = albumType;
  const discnumber = str('discnumber');
  if (discnumber) out.discnumber = discnumber;
  const tracknumber = str('tracknumber');
  if (tracknumber) out.tracknumber = tracknumber;
  const duration = str('duration');
  if (duration && duration !== '0') out.duration = duration;
  const bitRate = str('bit_rate');
  if (bitRate && bitRate !== '0') out.bit_rate = bitRate;
  const size = str('size');
  if (size && size !== '0') out.size = size;
  const filename = str('filename');
  if (filename) out.filename = filename;

  // Cover: backend puts data-URI on `artwork`; also mirror to album_img
  // so callers that only look at album_img (scrape apply) still work.
  const artwork = str('artwork');
  if (artwork && !artwork.endsWith(',')) {
    out.artwork = artwork;
    out.album_img = artwork;
  }
  const aw = num('artwork_w');
  if (aw != null && aw > 0) out.artwork_w = aw;
  const ah = num('artwork_h');
  if (ah != null && ah > 0) out.artwork_h = ah;
  const asz = num('artwork_size');
  if (asz != null && asz > 0) out.artwork_size = asz;

  return out;
}
