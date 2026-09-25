import { useEffect, useRef, useState } from 'react';
import { Play, Pause, SkipBack, SkipForward, Volume2, VolumeX } from 'lucide-react';
import { usePlayerStore } from '@/store/usePlayerStore';
import { useNoticeStore } from '@/store/useNoticeStore';
import { sourceErrorMessage } from '@/lib/streamUrl';
import { NowPlaying } from '@/components/player/NowPlaying';

function formatTime(s: number): string {
  if (!Number.isFinite(s) || s < 0) return '0:00';
  const m = Math.floor(s / 60);
  const sec = Math.floor(s % 60);
  return `${m}:${sec.toString().padStart(2, '0')}`;
}

/** Bottom-mounted audio player. Single `<audio>` element on the page —
 *  mounted once per session, lives inside this component, mirrors
 *  `usePlayerStore` to native reality and back. Survives mode-switches
 *  because it's mode-independent (AppShell renders it once outside the
 *  mode-conditional view tree).
 *
 *  AUDIT NOTE — closure isolation per handler:
 *
 *  - `onMediaError` reads state via `usePlayerStore.getState()` rather
 *    than React-scope closure capture. Native onerror fires from the DOM,
 *    not from the React commit cycle — a fast-track-swap (A→B, B errors
 *    before React re-renders) would otherwise attribute the toast to A's
 *    source. Documented and adopted (see Round-3 fix).
 *
 *  - React onClick / onChange handlers (togglePlay, prev, next, seek,
 *    setVolume, onSeekInput) capture Zustand slice selectors (`(s) =>
 *    s.togglePlay` etc.) at render time. These references are STABLE —
 *    Zustand returns the same function pointer on every call to the
 *    store factory — so a vermicious "stale action over the new state"
 *    bug isn't possible. The render-time closures need not call
 *    `getState()` because the actions themselves read the latest store
 *    state inside the action body (Zustand getters return fresh values).
 *
 *  Conclusion: only the DOM-native `onMediaError` actually needs the
 *  getState() pattern; React onClick handlers are safe as-is. This
 *  comment exists so a future audit doesn't re-do this analysis. */
export function PlayerBar() {
  const audioRef = useRef<HTMLAudioElement>(null);
  // Mobile-only overlay open state. Owned here so the mini player bar
  // and the NowPlaying sheet share one source of truth without a UI
  // store slot. Resets whenever currentTrack goes null (so dismissing a
  // track doesn't leave the overlay primed to pop up on next play).
  const [nowPlayingOpen, setNowPlayingOpen] = useState(false);

  // Slice selectors so unrelated store writes don't re-render this bar.
  const currentTrack = usePlayerStore((s) => s.currentTrack);
  const isPlaying = usePlayerStore((s) => s.isPlaying);
  const currentTime = usePlayerStore((s) => s.currentTime);
  const duration = usePlayerStore((s) => s.duration);
  const volume = usePlayerStore((s) => s.volume);
  const togglePlay = usePlayerStore((s) => s.togglePlay);
  const seek = usePlayerStore((s) => s.seek);
  const setVolume = usePlayerStore((s) => s.setVolume);
  const next = usePlayerStore((s) => s.next);
  const prev = usePlayerStore((s) => s.prev);
  const setCurrentTime = usePlayerStore((s) => s.setCurrentTime);
  const setDuration = usePlayerStore((s) => s.setDuration);
  const setIsBuffering = usePlayerStore((s) => s.setIsBuffering);

  // Initial volume + reactive volume sync.
  useEffect(() => {
    const el = audioRef.current;
    if (!el) return;
    el.volume = volume;
  }, [volume]);

  // Track swap OR play/pause toggle: load + play / pause accordingly.
  // Combined effect (instead of two with overlapping deps) to avoid
  // double-play() calls when both keys change in the same tick.
  //
  // Dep array intentionally reads `currentTrack?.url` rather than
  // `currentTrack`: play() / pause() / src swap are driven by the URL
  // alone; re-running on title/artist mutations from the store would
  // needlessly call pause/play and clobber the listener-driven time
  // mirror. eslint can't see this so the rule is disabled inline.
  useEffect(() => {
    const el = audioRef.current;
    if (!el) return;
    if (currentTrack && currentTrack.url) {
      if (el.src !== currentTrack.url) {
        el.src = currentTrack.url;
        el.load();
      }
      if (isPlaying) {
        el.play().catch((err) => {
          console.warn('[player] playback rejected', err);
        });
      } else if (!el.paused) {
        el.pause();
      }
    } else if (el.getAttribute('src')) {
      el.pause();
      el.removeAttribute('src');
      el.load();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentTrack?.url, isPlaying]);

  // Auto-advance queue when the current track ends.
  useEffect(() => {
    const el = audioRef.current;
    if (!el) return;
    const handler = () => next();
    el.addEventListener('ended', handler);
    return () => el.removeEventListener('ended', handler);
  }, [next]);

  // Note: NowPlaying self-guards via `if (!open || !currentTrack) return null`
  // so a leftover `nowPlayingOpen=true` from a previous track is harmless —
  // When currentTrack nulls out, NowPlaying returns null anyway. The mini
  // bar's tap handler also guards on `currentTrack &&` so a stray `open=true`
  // can't pop an empty NowPlaying. The state is cleaned up the next time a
  // user manually taps "mini player tap" with a real `currentTrack` (the
  // functional setNowPlayingOpen(true) overwrites the stale value).

  // Native <audio>.error handler — distinguish per source so the user can
  // tell whether the failure came from upstream (e.g. kuwo URL expired) vs
  // the gateway /api/stream proxy (e.g. netease / qmusic geo-block). Pulled
  // out from the JSX so the inline closure stays free of fall-through state.
  //
  // Reads currentTrack via usePlayerStore.getState() rather than the React
  // closure capture: native onerror fires from the DOM, not from the React
  // commit cycle. If the user swaps track A → track B and B's network fetch
  // fails before commit, the React-scope currentTrack would still be A and
  // the toast would misattribute the error to A's source. getState() reads
  // the latest committed store value regardless of commit timing.
  const onMediaError = () => {
    const audioErr = audioRef.current?.error;
    console.warn('[player] media error', audioErr?.message);
    const track = usePlayerStore.getState().currentTrack;
    if (!track) return;
    const sourceName = track.source.kind === 'plugin' ? track.source.source : undefined;
    useNoticeStore.getState().push(sourceErrorMessage(sourceName), 'warn');
  };

  // Slider drag → push currentTime to the audio element directly. We
  // don't use a reactive seek-effect because it would loop with the
  // onTimeUpdate mirror below.
  const onSeekInput = (e: React.ChangeEvent<HTMLInputElement>) => {
    const t = Number(e.target.value);
    seek(t);
    if (audioRef.current) audioRef.current.currentTime = t;
  };

  const progress = duration > 0 ? (currentTime / duration) * 100 : 0;
  const hasUrl = !!currentTrack?.url;

  return (
    <>
      <div className="shrink-0">
      <div
        role="region"
        aria-label="播放器"
        className="border-t border-border bg-surface-1 backdrop-blur-sm shrink-0 px-3 hidden sm:flex items-center gap-3 h-16"
      >
      <audio
        ref={audioRef}
        preload="metadata"
        onLoadedMetadata={(e) => setDuration(e.currentTarget.duration || 0)}
        onTimeUpdate={(e) => setCurrentTime(e.currentTarget.currentTime)}
        onError={onMediaError}
        // Buffering signal mirrors: onWaiting fires when playback halts
        // because the next frame isn't buffered yet; onCanPlay / onPlaying
        // both indicate recovery. Layered into usePlayerStore.isBuffering
        // so PlayButton (and any future spinner overlay) can show the
        // per-track stall signal next to the row that triggered it,
        // instead of burying it in a global toast.
        onWaiting={() => setIsBuffering(true)}
        onCanPlay={() => setIsBuffering(false)}
        onPlaying={() => setIsBuffering(false)}
        onPause={() => setIsBuffering(false)}
      />

      {/* Cover */}
      <div className="w-10 h-10 rounded overflow-hidden bg-muted shrink-0">
        {currentTrack?.cover ? (
          <img
            src={currentTrack.cover}
            alt={currentTrack.title}
            className="w-full h-full object-cover"
          />
        ) : (
          <div className="w-full h-full flex items-center justify-center text-muted-foreground bg-gradient-to-br from-muted to-accent text-base">
            ♪
          </div>
        )}
      </div>

      {/* Track meta */}
      <div className="min-w-0 w-44 shrink-0">
        {currentTrack ? (
          <>
            <div className="text-sm font-medium truncate" title={currentTrack.title}>
              {currentTrack.title}
            </div>
            <div className="text-xs text-muted-foreground truncate" title={currentTrack.artist}>
              {currentTrack.artist || '未知艺术家'}
            </div>
          </>
        ) : (
          <div className="text-sm text-muted-foreground">未在播放</div>
        )}
      </div>

      {/* Transport */}
      <div className="flex items-center gap-1 shrink-0">
        <button
          onClick={prev}
          disabled={!currentTrack}
          aria-label="上一首"
          title="上一首"
          className="p-1.5 rounded hover:bg-accent disabled:opacity-40 disabled:cursor-not-allowed text-muted-foreground hover:text-foreground"
        >
          <SkipBack className="w-4 h-4" />
        </button>
        <button
          onClick={togglePlay}
          disabled={!hasUrl}
          aria-label={isPlaying ? '暂停' : '播放'}
          title={isPlaying ? '暂停' : '播放'}
          className="p-2 rounded-full bg-primary text-primary-foreground hover:bg-primary/90 disabled:opacity-40 disabled:cursor-not-allowed"
        >
          {isPlaying ? <Pause className="w-4 h-4" /> : <Play className="w-4 h-4" />}
        </button>
        <button
          onClick={next}
          disabled={!currentTrack}
          aria-label="下一首"
          title="下一首"
          className="p-1.5 rounded hover:bg-accent disabled:opacity-40 disabled:cursor-not-allowed text-muted-foreground hover:text-foreground"
        >
          <SkipForward className="w-4 h-4" />
        </button>
      </div>

      {/* Seek */}
      <div className="flex items-center gap-2 flex-1 min-w-0 max-w-[40%]">
        <span className="text-xs font-mono text-muted-foreground w-10 text-right tabular-nums">
          {formatTime(currentTime)}
        </span>
        <input
          type="range"
          min={0}
          max={duration || 0}
          step="any"
          value={currentTime}
          onChange={onSeekInput}
          disabled={!hasUrl}
          aria-label="进度"
          className="flex-1 accent-primary h-1 cursor-pointer disabled:cursor-not-allowed"
          style={{
            background: `linear-gradient(to right, var(--primary) 0% ${progress}%, var(--border) ${progress}% 100%)`,
          }}
        />
        <span className="text-xs font-mono text-muted-foreground w-10 tabular-nums">
          {formatTime(duration)}
        </span>
      </div>

      {/* Volume */}
      <div className="flex items-center gap-2 w-32 shrink-0">
        {volume <= 0 ? (
          <VolumeX className="w-4 h-4 text-muted-foreground" />
        ) : (
          <Volume2 className="w-4 h-4 text-muted-foreground" />
        )}
        <input
          type="range"
          min={0}
          max={1}
          step="any"
          value={volume}
          onChange={(e) => setVolume(Number(e.target.value))}
          aria-label="音量"
          className="flex-1 accent-primary h-1 cursor-pointer"
        />
      </div>
      </div>

      {/* Mobile mini bar (below 640px). Plan B decision #10: bottom
          h-14 strip with artist - title + thin progress bar; tap opens
          the full-screen NowPlaying overlay mounted right after this
          strip. The <audio> element lives in the desktop PlayerBar
          above; rendering it on the desktop branch means SSR / desktop
          "<noscript>" fallback still has the element in the DOM. But
          on mobile-first screens React renders BOTH branches (hidden
          handles only CSS, not React mount), so <audio> is still
          present in the DOM when the desktop branch is CSS-hidden —
          its onTimeUpdate / onError still fire, audio still plays. */}
      <div
        role="region"
        aria-label="迷你播放器"
        className="sm:hidden border-t border-border bg-surface-1 backdrop-blur-sm shrink-0 h-14 px-3 flex items-center gap-3"
        onClick={() => currentTrack && setNowPlayingOpen(true)}
      >
        {/* Cover thumbnail — tapping whole bar opens NowPlaying, so
            no nested interactive. */}
        <div className="w-9 h-9 rounded overflow-hidden shrink-0 bg-surface-2">
          {currentTrack?.cover ? (
            <img
              src={currentTrack.cover}
              alt={currentTrack.title}
              className="w-full h-full object-cover"
            />
          ) : (
            <div className="w-full h-full flex items-center justify-center text-muted-foreground">
              ♪
            </div>
          )}
        </div>
        <div className="flex-1 min-w-0 flex flex-col">
          <div className="text-xs font-medium truncate" title={currentTrack?.title}>
            {currentTrack?.title || '未在播放'}
          </div>
          <div className="text-[0.65rem] text-muted-foreground truncate" title={currentTrack?.artist}>
            {currentTrack?.artist || '点击展开播放界面'}
          </div>
        </div>
        {/* Transport — capture click so the bar tap doesn't double fire
            on a button hit. Stop propagation here. */}
        <div
          className="flex items-center gap-1 shrink-0"
          onClick={(e) => e.stopPropagation()}
        >
          <button
            onClick={prev}
            disabled={!currentTrack}
            aria-label="上一首"
            className="p-1.5 rounded hover:bg-surface-2 disabled:opacity-40"
          >
            <SkipBack className="w-4 h-4" />
          </button>
          <button
            onClick={togglePlay}
            disabled={!hasUrl}
            aria-label={isPlaying ? '暂停' : '播放'}
            className="p-2 rounded-full bg-primary text-primary-foreground disabled:opacity-40"
          >
            {isPlaying ? <Pause className="w-4 h-4" /> : <Play className="w-4 h-4" />}
          </button>
          <button
            onClick={next}
            disabled={!currentTrack}
            aria-label="下一首"
            className="p-1.5 rounded hover:bg-surface-2 disabled:opacity-40"
          >
            <SkipForward className="w-4 h-4" />
          </button>
        </div>
      </div>

      {/* Mini progress bar — renders above the mini bar itself. We
          render a 2px-tall strip fixed just above the mini bar so it
          reads as a "progress on the floor" indicator without taking
          vertical room from the controls. Same `--primary` / `--border`
          gradient recipe as the desktop seekbar. */}
      <div
        className="sm:hidden h-0.5 w-full shrink-0 bg-border pointer-events-none"
        aria-hidden
      >
        <div
          className="h-full bg-primary transition-[width]"
          style={{ width: `${progress}%` }}
        />
      </div>
      </div>

      {/* Mobile full-screen overlay. No content unless currentTrack —
          NowPlaying returns null when open && !currentTrack both guard
          so we don't double-render. */}
      <NowPlaying open={nowPlayingOpen} onClose={() => setNowPlayingOpen(false)} />
    </>
  );
}
