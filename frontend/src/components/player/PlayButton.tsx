import { useState } from 'react';
import { Play, Pause, Loader2 } from 'lucide-react';
import { usePlayerStore, type PlayerTrack } from '@/store/usePlayerStore';
import { useSourceStore } from '@/store/useSourceStore';
import { useNoticeStore } from '@/store/useNoticeStore';
import { recordOperationLog } from '@/api/client';
import {
  resolveStreamUrl,
  metadataOnlyMessage,
  waitForStreamReady,
  sourceErrorMessage,
} from '@/lib/streamUrl';

interface Props {
  track: PlayerTrack;
  size?: 'sm' | 'md';
  className?: string;
}

/** Hooked selector — returns true iff the row's track is playable via
 *  either direct URL (track.url populated by search) OR the gateway
 *  /api/stream proxy (when source.supports_audio_url=true and the plugin
 *  GetAudioURL returned an empty url, which is the dominant path for the
 *  5 streaming plugins when their upstream audio endpoint hiccups AND
 *  for the YouTube source once /api/download/ has landed). */
function useHasPlayableUrl(track: PlayerTrack): boolean {
  return useSourceStore((s) => {
    const r = resolveStreamUrl(track.id, track.source, track.url, s.sources);
    return r.kind !== 'none';
  });
}

/** Inline Play/Pause toggle for any row (WorkstationTable, CloudSearchView,
 *  future playlist lists). Reads its own slice from usePlayerStore so
 *  rows outside PlayerBar get isPlaying state without prop drilling.
 *  Stops propagation so clicking "play" on a row doesn't ALSO trigger
 *  the row's primary action (open editor / expand).
 *
 *  Buffering UX: the per-track buffering signal now comes from
 *  usePlayerStore.isBuffering — flipped true by PlayerBar's <audio>
 *  onWaiting, false on onPlaying/onCanPlay/onPause. We only render the
 *  spinner when THIS row is the active track (currentTrack.id === track.id)
 *  to avoid "row 47 looks busy" confusion when only the active track is
 *  stalling. The legacy useYoutubeStore "ensureDownload on first click"
 *  gate is gone: the backend StreamAudio handler now owns the
 *  enqueue-then-long-poll contract, so PlayButton just commits audio.src
 *  and the server stalls for us until the file is on disk. */
export function PlayButton({ track, size = 'sm', className }: Props) {
  const currentTrack = usePlayerStore((s) => s.currentTrack);
  const isPlaying = usePlayerStore((s) => s.isPlaying);
  const isBuffering = usePlayerStore((s) => s.isBuffering);
  const playTrack = usePlayerStore((s) => s.playTrack);
  const togglePlay = usePlayerStore((s) => s.togglePlay);
  const setIsBuffering = usePlayerStore((s) => s.setIsBuffering);
  const pushToast = useNoticeStore((s) => s.push);
  // Local arming spinner for the preflight wait (202 download_pending
  // rounds) before currentTrack is committed — isBuffering alone only
  // covers the active track after playTrack().
  const [arming, setArming] = useState(false);

  const isThis = currentTrack?.id === track.id;
  // Only the currently-playing row reflects buffering. Other rows show
  // a clean Play icon, since they're not waiting on anything.
  const loading = arming || (isThis && isBuffering);
  const showPause = isThis && isPlaying && !arming;
  const playable = useHasPlayableUrl(track);

  const onClick = async (e: React.MouseEvent) => {
    e.stopPropagation();
    if (showPause) {
      togglePlay();
      return;
    }
    // Pull sources via getState() at click-time so we capture up-to-date
    // capabilities without forcing this row to re-render on every source
    // mutation (rare in practice — only on app boot).
    const sources = useSourceStore.getState().sources;
    const r = resolveStreamUrl(track.id, track.source, track.url, sources);
    if (r.kind === 'none') {
      pushToast(metadataOnlyMessage(), 'info');
      return;
    }

    // DownloadSource (youtube): gateway may 202 until worker lands the
    // file. Native <audio> cannot honour Retry-After, so preflight here
    // and only then commit audio.src. TagSource proxy paths usually
    // return audio immediately; waitForStreamReady is a cheap Range
    // probe in that case.
    setArming(true);
    setIsBuffering(true);
    try {
      await waitForStreamReady(r.url);
      playTrack({ ...track, url: r.url });
    } catch (err) {
      const sourceName =
        track.source.kind === 'plugin' ? track.source.source : undefined;
      const msg =
        err instanceof Error && err.message
          ? err.message
          : sourceErrorMessage(sourceName);
      pushToast(msg, 'warn');
      // Mirror the failure into the backend audit log. The /media static
      // route that 404s here has no server-side audit hook, so the browser
      // is the only place that knows the play attempt failed. Fire-and-forget
      // and swallow its own error — a failed audit POST must not surface a
      // second toast on top of the playback failure the user already saw.
      void recordOperationLog({
        action: 'playback_failed',
        target: track.id,
        status: 'failed',
        error_msg: msg,
      }).catch(() => {});
      setIsBuffering(false);
    } finally {
      setArming(false);
    }
  };

  const iconSize = size === 'md' ? 'w-5 h-5' : 'w-3.5 h-3.5';
  const boxSize = size === 'md' ? 'w-9 h-9' : 'w-7 h-7';
  const title = !playable
    ? '\u6765\u6e90\u4e0d\u652f\u6301\u8bd5\u542c'
    : loading
    ? '\u7f13\u51b2\u4e2d...'
    : showPause
    ? '\u6682\u505c'
    : '\u64ad\u653e\u9884\u89c8';

  const icon = loading
    ? <Loader2 className={`${iconSize} animate-spin`} />
    : showPause
    ? <Pause className={iconSize} />
    : <Play className={iconSize} />;

  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={loading ? '\u7f13\u51b2\u4e2d' : showPause ? '\u6682\u505c' : '\u64ad\u653e\u9884\u89c8'}
      aria-pressed={showPause}
      aria-busy={loading}
      title={title}
      disabled={!playable || loading}
      className={
        (className ?? '') +
        ' inline-flex items-center justify-center rounded-full hover:bg-accent disabled:opacity-40 disabled:cursor-not-allowed text-muted-foreground hover:text-foreground transition-colors shrink-0 ' +
        boxSize
      }
    >
      {icon}
    </button>
  );
}
