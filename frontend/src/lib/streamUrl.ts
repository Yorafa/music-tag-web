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
//      AND for the YouTube source: once the backend supervisor sets
//      SourceInfo.supports_audio_url=true on the youtube download source
//      (handler/source.go), PlayButton falls through to the proxy path
//      here. The first click path actually triggers
//      useYoutubeStore.ensureDownload via PlayButton's onClick so the
//      /api/stream glob hits a freshly-written file by the time React
//      commits the new audio.src.
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
  // YouTube's proxy URL is identical in shape to the streaming plugins',
  // but PlayButton's onClick waits on useYoutubeStore.ensureDownload before
  // committing audio.src so the file is on disk by the time <audio>
  // dereferences this URL.
  const streamURL =
    `/api/stream?src=${encodeURIComponent(source.source)}` +
    `&id=${encodeURIComponent(source.songId)}`;
  return { kind: 'proxy', url: streamURL };
}

export function metadataOnlyMessage(): string {
  return '\u8be5\u6765\u6e90\u4ec5\u63d0\u4f9b\u5143\u6570\u636e\uff0c\u4e0d\u652f\u6301\u8bd5\u542c';
}

const SOURCE_ERROR_MESSAGES: Record<string, string> = {
  kuwo: '\u9177\u6211\u97f3\u4e50\u8bd5\u542c\u94fe\u63a5\u4e0d\u53ef\u7528\uff0c\u8bf7\u7a0d\u540e\u91cd\u8bd5\u6216\u5c1d\u8bd5\u522e\u635e\u540e\u518d\u8bd5\u542c',
  netease: '\u7f51\u6613\u4e91\u97f3\u4e50\u8bd5\u542c\u94fe\u63a5\u53d7\u9650\uff0c\u8bf7\u5c1d\u8bd5\u522e\u635e\u540e\u518d\u8bd5\u542c',
  kugou: '\u9177\u72d7\u97f3\u4e50\u8bd5\u542c\u94fe\u63a5\u4e0d\u53ef\u7528',
  migu: '\u54aa\u548c\u97f3\u4e50\u8bd5\u542c\u94fe\u63a5\u4e0d\u53ef\u7528\uff0c\u8bf7\u7a0d\u540e\u91cd\u8bd5',
  qmusic:
    'QQ \u97f3\u4e50\u8bd5\u542c\u53d7\u9650\uff0c\u8bf7\u5c1d\u8bd5\u522e\u635e\u540e\u518d\u8bd5\u542c',
  // YouTube preview unavailable toast: surfaced when PlayerBar's
  // <audio>.error fires for a feat.youtube_id. Typically because the
  // yt-dlp land-to-tmp path failed (anti-bot block, network blip).
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
