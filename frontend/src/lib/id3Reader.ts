// Browser-side ID3 / Vorbis-comment / FLAC-comment reader.
//
// Replaces the old POST /api/music_id3/ roundtrip with a Range-fetched
// partial-buffer parse via music-metadata. Why this exists:
//
//   - Backend used to scan every requested file server-side via
//     internal/tag/reader.go::Read, paying the cost in processes,
//     CPU, and bandwidth for fetch → in-memory bytes → parsed
//     structs → JSON envelope. With 100+ files in a freshly-picked
//     library directory each auto-hydration triggers reads for
//     every row, making the gateway's worker pool hot.
//
//   - Frontend reads a ~256 KB Range chunk via fetch() and asks
//     music-metadata to parse it. 256 KB is comfortably larger than
//     the ID3v2 frame block size for MP3, the FLAC Vorbis comment
//     block, the OGG Opus identification + comment header, and the
//     M4A iTunes atom. The Range is supported by Go's stdlib
//     http.ServeFile so no infra change was needed; the gateway
//     returns 206 Partial Content with the requested slice.
//
//   - ID3v1 fallback: legacy rips (pre-ID3v2 era) only carry a
//     128-byte ID3v1 trailer. The front-chunk parse on those
//     returns `{}` even though the file IS tagged. We detect
//     no-common-tags-empty and fire a second `Range: bytes=-4096`
//     fetch into the tail to pick up ID3v1 + APEv1 tail frames before
//     declaring the file untagged. 4 KB covers both cleanly.
//
//   - JWT auth: /api/music_id3/ was a JWT-gated POST so the old axios
//     interceptor injected `Authorization: JWT <token>` automatically.
//     A raw `fetch('/media/...', { ... })` does NOT inherit the
//     interceptor — we'd hit 401 on every call. This helper reads
//     the same token from useAuthStore and adds the header manually
//     so the wire shape matches what axios did.
// 
// Returns Partial<MusicTagInfo> so the existing cacheHasAnyValue-
// guarded click paths see the same shape as the old backend getMusicId3.

import { parseBuffer } from 'music-metadata';
import type { IAudioMetadata } from 'music-metadata';
import { buildMediaUrl } from '@/lib/mediaUrl';
import { useAuthStore } from '@/store/useAuthStore';
import type { MusicTagInfo } from '@/types';

/** Bytes fetched per audio file for tag inspection. 256 KB covers
 *  ID3v2 + FLAC + OGG/Opus front-block comfortably. */
const FRONT_CHUNK = 256 * 1024;

/** Tail chunk fetched only when the front parse yields no common
 *  tags — this catches legacy ID3v1-only files whose trailer (last
 *  128 bytes) carries the only tag block. */
const TAIL_CHUNK = 4096;

/** Authenticated fetch helper — mirrors the Authorization header
 *  that api/client.ts's axios interceptor injects for /api/* calls.
 *  /media/* is JWT-gated by the gateway's authed group, so raw
 *  fetch() without this header returns 401 and the caller sees a
 *  noisy "fetch failed" toast. */
async function authedFetch(url: string, headers: Record<string, string>): Promise<Response> {
  const merged: Record<string, string> = { ...headers };
  const token = useAuthStore.getState().accessToken;
  if (token) merged.Authorization = `JWT ${token}`;
  return fetch(url, { headers: merged });
}

/** Read embedded tags from /media/<fullPath> via two-chunk parse.
 *  Throws on transport / parse failure so the caller's try/catch
 *  can fire the user-visible toast. */
export async function readTagsFromPath(
  fullPath: string,
): Promise<Partial<MusicTagInfo>> {
  const mediaUrl = buildMediaUrl(fullPath);
  const fileName = fullPath.split('/').filter(Boolean).pop() ?? fullPath;

  // 1. Front chunk for ID3v2 / FLAC vorbis comments / OGG Opus header
  //    / M4A iTunes atoms — all front-loaded.
  const meta = await parseChunk(mediaUrl, fileName, 0, FRONT_CHUNK - 1);

  // 2. If the front parse returned no common tags at all, the file
  //    is most likely a legacy ID3v1 rip. Pull the tail 4 KB and
  //    re-parse — ID3v1's 128-byte trailer + APEv1's ~30-byte tail
  //    both fit comfortably. We MERGE in non-null fields rather
  //    than replace, so a file with both ID3v2 (somehow empty for
  //    common) and ID3v1 still picks up ID3v1's contribution.
  if (!hasAnyCommonTag(meta.common)) {
    const tailRes = await authedFetch(mediaUrl, {
      Range: `bytes=-${TAIL_CHUNK}`,
    });
    // Some older files may not honour the negative Range; treat
    // 416 / non-206 silently — front already gave us the verdict.
    if (tailRes.status === 206 || tailRes.status === 200) {
      const tailBuf = new Uint8Array(await tailRes.arrayBuffer());
      try {
        const tailMeta = await parseBuffer(tailBuf, {
          path: fileName,
          size: TAIL_CHUNK,
        });
        return metaToMusicTagInfo(mergeCommon(meta.common, tailMeta.common));
      } catch {
        // Tail parse failed — return what the front gave us.
      }
    }
  }
  return metaToMusicTagInfo(meta.common);
}

async function parseChunk(
  mediaUrl: string,
  fileName: string,
  rangeStart: number,
  rangeEnd: number,
): Promise<IAudioMetadata> {
  const res = await authedFetch(mediaUrl, {
    Range: `bytes=${rangeStart}-${rangeEnd}`,
  });
  if (!(res.ok || res.status === 206)) {
    throw new Error(`fetch failed: ${res.status} ${res.statusText}`);
  }
  const buf = new Uint8Array(await res.arrayBuffer());
  return parseBuffer(buf, {
    path: fileName,
    size: buf.byteLength,
  });
}

function hasAnyCommonTag(c: IAudioMetadata['common']): boolean {
  return Boolean(c.title || c.artist || c.album || c.year != null
    || (c.genre && c.genre.length > 0)
    || (c.track?.no != null) || (c.disk?.no != null));
}

function mergeCommon(
  front: IAudioMetadata['common'],
  tail: IAudioMetadata['common'],
): IAudioMetadata['common'] {
  // Spread tail first so all of its required fields (movementIndex,
  // composer, etc.) satisfy the return-type's required-key set, then
  // overwrite only the keys front actually has — `v !== undefined`
  // matches `??` semantics we want (empty-string / empty-array in
  // front still wins; only explicit-undefined falls through to
  // tail). The cast routes through `unknown` so TS doesn't reject
  // the Record → IAudioMetadata['common'] leap; a future music-
  // metadata major bump that adds a new required key would surface
  // as a runtime missing-key — cheap to catch with vitest's
  // "title/artist populated" assertions on the resulting Common.
  const merged: Record<string, unknown> = { ...tail };
  for (const [k, v] of Object.entries(front)) {
    if (v !== undefined) merged[k] = v;
  }
  return merged as unknown as IAudioMetadata['common'];
}

function metaToMusicTagInfo(common: IAudioMetadata['common']): Partial<MusicTagInfo> {
  const out: Partial<MusicTagInfo> = {};
  if (common.title) out.title = common.title;
  if (common.artist) out.artist = common.artist;
  if (common.album) out.album = common.album;
  if (common.year != null) out.year = String(common.year);
  if (common.genre?.length) out.genre = common.genre[0];
  if (common.track?.no != null) out.tracknumber = String(common.track.no);
  if (common.disk?.no != null) out.discnumber = String(common.disk.no);
  // Cover handling: the first picture's data is a Uint8Array of the
  // embedded image (typically JPEG/PNG). Encode as data URL so the
  // existing `resolveCoverSrc` cover helper (which inspects oversize
  // + data-URI shape) picks it up without a separate fetch.
  const pic = common.picture?.[0];
  if (pic) {
    const mime = pic.format?.startsWith('image/') ? pic.format : 'image/jpeg';
    // Browser-only API: convert bytes to base64 in chunks to avoid
    // call-stack issues with large artwork. 16 KB chunks are safe.
    let binary = '';
    const chunkSize = 16 * 1024;
    for (let i = 0; i < pic.data.length; i += chunkSize) {
      const slice = pic.data.subarray(i, i + chunkSize);
      binary += String.fromCharCode(...slice);
    }
    out.album_img = `data:${mime};base64,${btoa(binary)}`;
  }
  return out;
}
