import { Play, Pause, Loader2 } from 'lucide-react';
import { usePlayerStore, type PlayerTrack } from '@/store/usePlayerStore';
import { useSourceStore } from '@/store/useSourceStore';
import { useNoticeStore } from '@/store/useNoticeStore';
import { resolveStreamUrl, metadataOnlyMessage } from '@/lib/streamUrl';

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

/** Inline Play/Pause toggle for any row (SearchPanel, SearchResults,
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
  const pushToast = useNoticeStore((s) => s.push);

  const isThis = currentTrack?.id === track.id;
  // Only the currently-playing row reflects buffering. Other rows show
  // a clean Play icon, since they're not waiting on anything.
  const loading = isThis && isBuffering;
  const showPause = isThis && isPlaying;
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

    // No per-source gate (legacy youtube ensureDownload) — the gateway
    // StreamAudio handler enqueues a download task on cache-miss and
    // long-polls the cache dir for up to 10s before returning. If the
    // file still isn't there, the response goes 202 + Retry-After or
    // a Failure envelope, and <audio>.error → onMediaError surfaces
    // the per-source toast via sourceErrorMessage(track.source.source).
    playTrack({ ...track, url: r.url });
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
