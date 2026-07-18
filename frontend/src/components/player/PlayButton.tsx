import { useState } from 'react';
import { Play, Pause, Loader2 } from 'lucide-react';
import { usePlayerStore, type PlayerTrack } from '@/store/usePlayerStore';
import { useSourceStore } from '@/store/useSourceStore';
import { useNoticeStore } from '@/store/useNoticeStore';
import { useYoutubeStore } from '@/store/useYoutubeStore';
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
 *  for the YouTube source once /api/youtube_download/ has landed). */
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
 *  YouTube-source preview path:
 *   - First click per session → POST /api/youtube_download/ via
 *     useYoutubeStore.ensureDownload, wait for the 202 envelope, then
 *     commit audio.src = /api/stream?src=youtube&id=. Subsequent clicks
 *     short-circuit and skip the POST so the gateway streams the
 *     already-landed file.
 *   - During the in-flight POST we render a Loader spinner + aria-busy
 *     so the row stays interactive feedback-wise but doesn't accept
 *     double-clicks. */
export function PlayButton({ track, size = 'sm', className }: Props) {
  const currentTrack = usePlayerStore((s) => s.currentTrack);
  const isPlaying = usePlayerStore((s) => s.isPlaying);
  const playTrack = usePlayerStore((s) => s.playTrack);
  const togglePlay = usePlayerStore((s) => s.togglePlay);
  const pushToast = useNoticeStore((s) => s.push);
  const [loading, setLoading] = useState(false);

  const isThis = currentTrack?.id === track.id;
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

    // YouTube-specific gate: ensure the file is on disk before assigning
    // audio.src. ensureDownload is idempotent within the page session,
    // so the second click onward does an early-return and we go straight
    // to playTrack(...).
    if (
      track.source.kind === 'plugin' &&
      track.source.source === 'youtube' &&
      !useYoutubeStore.getState().isDownloaded(track.source.songId)
    ) {
      setLoading(true);
      try {
        await useYoutubeStore
          .getState()
          .ensureDownload(track.source.songId);
      } catch {
        pushToast('\u89c6\u9891\u4e0b\u8f7d\u5931\u8d25\uff0c\u8bf7\u7a0d\u540e\u91cd\u8bd5', 'warn');
        setLoading(false);
        return;
      }
      setLoading(false);
    }

    playTrack({ ...track, url: r.url });
  };

  const iconSize = size === 'md' ? 'w-5 h-5' : 'w-3.5 h-3.5';
  const boxSize = size === 'md' ? 'w-9 h-9' : 'w-7 h-7';
  const title = !playable
    ? '\u6765\u6e90\u4e0d\u652f\u6301\u8bd5\u542c'
    : loading
    ? '\u6b63\u5728\u4e0b\u8f7d\u9884\u89c8...'
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
      aria-label={loading ? '\u4e0b\u8f7d\u4e2d' : showPause ? '\u6682\u505c' : '\u64ad\u653e\u9884\u89c8'}
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
