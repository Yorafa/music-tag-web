// Frontend ↔ stream-proxy glue.
//
// resolveStreamUrl collapses three paths into one shape:
//
//   - `direct`: track.url was already populated by the search payload
//      (rare today — most plugins only emit metadata). Honor it verbatim.
//   - `proxy`: track.url empty AND the source's PluginInfoResponse reports
//      supports_audio_url = true. Construct `/api/stream?src=&id=` and let
//      the gateway Range-pass the upstream bytes back. This is the dominant
//      path for the 5 streaming plugins when their GetAudioURL RPC returned
//      ("", nil) (transient failure / paid track / Chinese-IP geo-block).
//      AND for any DownloadSource (e.g. youtube): the gateway StreamAudio
//      handler enqueues a download task on cache-miss and long-polls the
//      cache dir for up to 10s before responding, so frontend code no
//      longer needs to ensureDownload before committing audio.src — the
//      server does that contract for us, transparently across all sources.
//   - `none`: the source is metadata-only (musicbrainz / acoustid). The
//      caller should push a toast and NOT change playback; surfacing a
//      broken audio element does nobody any favors.
//
// sourceErrorMessage maps plugin identity to a one-line Chinese / English
// notice that's pushed via the toast store on <audio>.error. Per-source
// copy makes the failure mode legible — the user can tell whether the
// problem is kuwo's expired URL vs qmusic geo-block vs acoustid just not
// being a stream source at all.

import type { SourceInfo } from '@/types';
import type { PlayerSource } from '@/store/usePlayerStore';

export type StreamResolution =
  | { kind: 'direct'; url: string }
  | { kind: 'proxy'; url: string }
  | { kind: 'none'; reason: 'metadata-only' };

export function resolveStreamUrl(
  _trackId: string,
  source: PlayerSource,
  trackUrl: string | undefined,
  sources: SourceInfo[],
): StreamResolution {
  if (trackUrl && trackUrl.length > 0) {
    return { kind: 'direct', url: trackUrl };
  }
  if (source.kind !== 'plugin') {
    // Local-file rows always carry a populated url upstream; reaching here
    // means buildMediaUrl failed — surface as metadata-only so the caller
    // can show the same toast UI as for plugin metadata-only sources.
    return { kind: 'none', reason: 'metadata-only' };
  }
  const meta = sources.find((s) => s.name === source.source);
  if (!meta?.supports_audio_url) {
    return { kind: 'none', reason: 'metadata-only' };
  }
  // Trailing slash before the query string is intentional: gin's
  // `RedirectTrailingSlash=true` (the default) rewrites `/api/stream` →
  // `/api/stream/` with a 301, and a native `<audio>` element then has
  // to fire a SECOND GET. Worse, Axios's 401-on-logout interceptor
  // (frontend/src/api/client.ts) reads the SECOND response and could
  // spuriously drop the user's session on what is actually a same-host
  // rewrite. Asking for the trailing-slash variant up-front skips the
  // redirect round-trip and keeps the auth-header / cookie round-trip
  // stable. See docs/plugable-plugins.md §11 future-plugin authoring
  // notes for the upstream contract.
  const streamURL =
    `/api/stream/?src=${encodeURIComponent(source.source)}` +
    `&id=${encodeURIComponent(source.songId)}`;
  return { kind: 'proxy', url: streamURL };
}

/** Like resolveStreamUrl but for the "save to browser Downloads" path:
 *  same /api/stream endpoint with ?as_attachment=1, which makes the
 *  gateway set `Content-Disposition: attachment` so the browser writes
 *  the file to the user's Downloads folder instead of streaming it into
 *  the in-page <audio> element. Used by the row-level "下载到浏览器"
 *  button in CloudSearchView. Returns null when the source
 *  is metadata-only.
 *
 *  Optional `filename` becomes `?filename=` and drives Content-Disposition
 *  (e.g. "Artist - Title.ogg"). When omitted the gateway falls back to
 *  the on-disk cache basename (`<id>.ext`). */
export function resolveDownloadUrl(
  source: PlayerSource,
  trackUrl: string | undefined,
  sources: SourceInfo[],
  filename?: string,
): string | null {
  if (trackUrl && trackUrl.length > 0) return trackUrl;
  if (source.kind !== 'plugin') return null;
  const meta = sources.find((s) => s.name === source.source);
  if (!meta?.supports_audio_url) return null;
  // Same trailing-slash rationale as resolveStreamUrl above: gin's
  // RedirectTrailingSlash default turns the no-slash variant into a
  // 301 round-trip. <a download> navigation does follow 301s but the
  // resulting second request may pick up a different set of cookies
  // (depending on browser) and the file ends up with the wrong name or
  // misses the X-Download-Retry-Budget header that the no-slash path
  // wouldn't have set in the first place. Skip the redirect up-front.
  let url =
    `/api/stream/?src=${encodeURIComponent(source.source)}` +
    `&id=${encodeURIComponent(source.songId)}` +
    `&as_attachment=1`;
  const safe = (filename ?? '').trim();
  if (safe) {
    url += `&filename=${encodeURIComponent(safe)}`;
  }
  return url;
}

/** Build a filesystem-safe "Artist - Title.ogg" basename for downloads. */
export function audioDownloadBasename(
  artist: string | undefined,
  title: string | undefined,
  ext: string = 'ogg',
): string {
  const a = (artist || '未知艺术家').trim() || '未知艺术家';
  const t = (title || 'audio').trim() || 'audio';
  const base = `${a} - ${t}`
    .replace(/[\\/:*?"<>|]/g, '_')
    .replace(/\s+/g, ' ')
    .trim();
  const cleanExt = (ext || 'ogg').replace(/^\./, '').toLowerCase() || 'ogg';
  return `${base}.${cleanExt}`;
}

export function metadataOnlyMessage(): string {
  return '\u8be5\u6765\u6e90\u4ec5\u63d0\u4f9b\u5143\u6570\u636e\uff0c\u4e0d\u652f\u6301\u8bd5\u542c';
}

/** Default ceilings when gateway omits Retry-After / X-Download-Retry-Budget.
 *  Keep in lockstep with streamDownloadRetryBudget in stream.go. */
const DEFAULT_STREAM_RETRY_BUDGET = 5;
const DEFAULT_STREAM_RETRY_AFTER_SEC = 3;

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function readStreamErrorMessage(res: Response): Promise<string> {
  // Force UTF-8 decode even when Content-Type lacks charset (legacy
  // responses / proxies) so Chinese failure envelopes don't mojibake.
  try {
    const buf = await res.arrayBuffer();
    const text = new TextDecoder('utf-8').decode(buf);
    if (!text) return '';
    try {
      const body = JSON.parse(text) as { message?: string };
      if (typeof body.message === 'string' && body.message.length > 0) {
        return body.message;
      }
    } catch {
      // not JSON — fall through
    }
    return text.slice(0, 200);
  } catch {
    return '';
  }
}

/**
 * Preflight /api/stream for DownloadSource paths (youtube) where a cache
 * miss returns 202 + Retry-After until the worker finishes yt-dlp.
 *
 * Native <audio> treats a 202 JSON body as a decode failure and fires
 * `error` once — it will NOT honour Retry-After. So PlayButton must call
 * this before committing `audio.src`, then hand the same URL to <audio>
 * once the cache is warm (or after the long-poll window returns 200).
 *
 * Uses credentials so the AUTHORIZATION cookie JWTAuth fallback applies
 * (same as <audio> navigation). Prefer HEAD so we never pull audio bytes
 * twice; fall back to a tiny GET without Range if HEAD is blocked.
 * Never send Range: bytes=0-1 here — empty/incomplete cache files make
 * ServeFile answer 416 Requested Range Not Satisfiable.
 */
export async function waitForStreamReady(
  streamUrl: string,
  opts?: { signal?: AbortSignal },
): Promise<void> {
  let budget = DEFAULT_STREAM_RETRY_BUDGET;
  let attempt = 0;

  while (attempt < budget) {
    if (opts?.signal?.aborted) {
      throw new DOMException('Aborted', 'AbortError');
    }
    // HEAD first: warm-cache path only needs status/headers.
    let res = await fetch(streamUrl, {
      method: 'HEAD',
      credentials: 'same-origin',
      signal: opts?.signal,
    });
    // Some proxies/middleware reject HEAD; retry once as GET.
    if (res.status === 405 || res.status === 501) {
      try {
        await res.body?.cancel();
      } catch {
        /* ignore */
      }
      res = await fetch(streamUrl, {
        method: 'GET',
        credentials: 'same-origin',
        signal: opts?.signal,
      });
    }

    const budgetHdr = res.headers.get('X-Download-Retry-Budget');
    if (budgetHdr) {
      const n = Number(budgetHdr);
      if (Number.isFinite(n) && n > 0) budget = Math.floor(n);
    }

    if (res.status === 200 || res.status === 206) {
      // Drain/cancel body so the connection can be reused; content is
      // re-fetched by <audio> with full Range support.
      try {
        await res.body?.cancel();
      } catch {
        /* ignore */
      }
      return;
    }

    // 202 = still downloading. 416 = empty/incomplete file briefly
    // visible under an old --no-part race; treat as pending, not failure.
    if (res.status === 202 || res.status === 416) {
      const rawRa = res.headers.get('Retry-After');
      const ra = rawRa == null || rawRa === '' ? DEFAULT_STREAM_RETRY_AFTER_SEC : Number(rawRa);
      // Honour 0 (immediate retry) when the header is present; only fall
      // back to the default when the header is missing or non-numeric.
      const waitSec = Number.isFinite(ra) && ra >= 0 ? ra : DEFAULT_STREAM_RETRY_AFTER_SEC;
      try {
        await res.body?.cancel();
      } catch {
        /* ignore */
      }
      attempt += 1;
      if (attempt >= budget) {
        throw new Error(
          '\u4e0b\u8f7d\u8d85\u65f6\uff0c\u8bf7\u7a0d\u540e\u91cd\u8bd5',
        );
      }
      await sleep(waitSec * 1000);
      continue;
    }

    const msg = (await readStreamErrorMessage(res)) || `HTTP ${res.status}`;
    throw new Error(msg);
  }

  throw new Error('\u4e0b\u8f7d\u8d85\u65f6\uff0c\u8bf7\u7a0d\u540e\u91cd\u8bd5');
}

const SOURCE_ERROR_MESSAGES: Record<string, string> = {
  kuwo: '\u9177\u6211\u97f3\u4e50\u8bd5\u542c\u94fe\u63a5\u4e0d\u53ef\u7528\uff0c\u8bf7\u7a0d\u540e\u91cd\u8bd5\u6216\u5c1d\u8bd5\u522e\u635e\u540e\u518d\u8bd5\u542c',
  netease: '\u7f51\u6613\u4e91\u97f3\u4e50\u8bd5\u542c\u94fe\u63a5\u53d7\u9650\uff0c\u8bf7\u5c1d\u8bd5\u522e\u635e\u540e\u518d\u8bd5\u542c',
  kugou: '\u9177\u72d7\u97f3\u4e50\u8bd5\u542c\u94fe\u63a5\u4e0d\u53ef\u7528',
  migu: '\u54aa\u548c\u97f3\u4e50\u8bd5\u542c\u94fe\u63a5\u4e0d\u53ef\u7528\uff0c\u8bf7\u7a0d\u540e\u91cd\u8bd5',
  qmusic:
    'QQ \u97f3\u4e50\u8bd5\u542c\u53d7\u9650\uff0c\u8bf7\u5c1d\u8bd5\u522e\u635e\u540e\u518d\u8bd5\u542c',
  // YouTube preview unavailable: surfaced when PlayerBar's <audio>.error
  // fires on the active preview. Typically because the yt-dlp cache write
  // failed (anti-bot block, network blip) — see internal/plugin/youtube.
  youtube:
    'YouTube \u9884\u89c8\u4e0d\u53ef\u7528\uff0c\u8bf7\u7a0d\u540e\u91cd\u8bd5\u6216\u5c1d\u8bd5\u5176\u4ed6\u6765\u6e90',
  musicbrainz: 'MusicBrainz \u4ec5\u63d0\u4f9b\u5143\u6570\u636e\uff0c\u4e0d\u652f\u6301\u8bd5\u542c',
  acoustid: 'AcoustID \u4ec5\u63d0\u4f9b\u5143\u6570\u636e\uff0c\u4e0d\u652f\u6301\u8bd5\u542c',
};

const FALLBACK_AUDIO_ERROR =
  '\u8bd5\u542c\u94fe\u63a5\u4e0d\u53ef\u7528\uff0c\u8bf7\u7a0d\u540e\u91cd\u8bd5\u6216\u7a0d\u540e\u5c1d\u8bd5\u522e\u635e';

export function sourceErrorMessage(sourceName: string | undefined): string {
  if (!sourceName) return FALLBACK_AUDIO_ERROR;
  return SOURCE_ERROR_MESSAGES[sourceName] || FALLBACK_AUDIO_ERROR;
}
