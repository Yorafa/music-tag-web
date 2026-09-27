import { useEffect, useRef, useState } from 'react';
import { Play, Pause, Volume2, VolumeX, Mic2 } from 'lucide-react';
import { usePlayerStore } from '@/store/usePlayerStore';
import { useNoticeStore } from '@/store/useNoticeStore';
import { sourceErrorMessage } from '@/lib/streamUrl';
import { NowPlaying } from '@/components/player/NowPlaying';
import { LyricsView } from '@/components/player/LyricsView';
import { Popover, PopoverTrigger, PopoverContent } from '@/components/ui/popover';

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
 *  - React onClick / onChange handlers (togglePlay, seek,
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
  // The track URL we last assigned to <audio>.src, kept verbatim from the
  // store. We compare against THIS, not el.src: the DOM resolves el.src to an
  // absolute URL, so `el.src !== currentTrack.url` (a relative "/api/stream/…")
  // is always true — every pause/resume would then re-assign src and call
  // load(), which resets currentTime to 0. Comparing the store value to itself
  // makes the src swap fire only on a genuine track change.
  const loadedUrlRef = useRef<string | null>(null);
  // VBR MP3s report <audio>.duration = Infinity at loadedmetadata until the
  // browser scans to the real end. We force that scan by seeking far past the
  // end (see onLoadedMetadata); this flag suppresses the currentTime mirror
  // during the throwaway seek and records that we owe a reset back to 0 once
  // durationchange delivers the true finite length.
  const vbrScanRef = useRef(false);
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
  const pause = usePlayerStore((s) => s.pause);
  const seek = usePlayerStore((s) => s.seek);
  const setVolume = usePlayerStore((s) => s.setVolume);
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
      if (loadedUrlRef.current !== currentTrack.url) {
        loadedUrlRef.current = currentTrack.url;
        // New source: any pending VBR end-scan belonged to the old track.
        vbrScanRef.current = false;
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
    } else if (loadedUrlRef.current !== null) {
      loadedUrlRef.current = null;
      el.pause();
      el.removeAttribute('src');
      el.load();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentTrack?.url, isPlaying]);

  // Single-track player (no queue): when the track ends, stop playback so
  // the transport reflects reality and a tap on play restarts from 0.
  useEffect(() => {
    const el = audioRef.current;
    if (!el) return;
    const handler = () => pause();
    el.addEventListener('ended', handler);
    return () => el.removeEventListener('ended', handler);
  }, [pause]);

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

  // Duration mirror. VBR MP3s (e.g. "Jocelyn Flores") report duration =
  // Infinity at loadedmetadata because the header carries no frame count;
  // the browser only learns the true length after it scans to the end.
  // formatTime(Infinity) renders "0:00", and without a recovery path the
  // stale value from the previous track lingers. So: commit only finite
  // durations, and when we see Infinity, force the scan by seeking past the
  // end. That fires durationchange with the real finite value, which we then
  // commit and rewind back to 0.
  const commitDuration = (el: HTMLAudioElement) => {
    if (Number.isFinite(el.duration) && el.duration > 0) {
      setDuration(el.duration);
      // If a VBR scan was in flight, the seek left currentTime at the end.
      // Rewind so playback (and the progress bar) start from the top.
      if (vbrScanRef.current) {
        vbrScanRef.current = false;
        try {
          el.currentTime = 0;
        } catch {
          /* seeking may throw if not seekable yet; timeupdate will re-sync */
        }
        setCurrentTime(0);
      }
      return;
    }
    // Non-finite duration: kick off a one-shot end-scan. Seeking to a huge
    // time is the canonical trick to make the browser compute a VBR length.
    if (!vbrScanRef.current) {
      vbrScanRef.current = true;
      try {
        el.currentTime = 1e101;
      } catch {
        vbrScanRef.current = false;
      }
    }
  };

  const onTimeUpdate = (el: HTMLAudioElement) => {
    // Swallow the throwaway seek position during a VBR end-scan; committing
    // it would flash the slider to the track's end.
    //
    // The suppression is conditional on the duration STILL being unknown, not
    // just on the latch being set. Some media (a stalled fetch, a live stream)
    // never delivers the durationchange that clears the latch, and a bare
    // boolean would then silence the time mirror for the rest of the session
    // on this track — a progress bar frozen at 0:00. Tying the suppression to
    // the actual condition means the moment a finite duration shows up, the
    // mirror resumes whether or not the rewind in commitDuration ran.
    if (vbrScanRef.current && !Number.isFinite(el.duration)) return;
    setCurrentTime(el.currentTime);
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
        onLoadedMetadata={(e) => commitDuration(e.currentTarget)}
        onDurationChange={(e) => commitDuration(e.currentTarget)}
        onTimeUpdate={(e) => onTimeUpdate(e.currentTarget)}
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

      {/* Transport — single-track player, so just play/pause (no queue). */}
      <div className="flex items-center gap-1 shrink-0">
        <button
          onClick={togglePlay}
          disabled={!hasUrl}
          aria-label={isPlaying ? '暂停' : '播放'}
          title={isPlaying ? '暂停' : '播放'}
          className="p-2 rounded-full bg-primary text-primary-foreground hover:bg-primary/90 disabled:opacity-40 disabled:cursor-not-allowed"
        >
          {isPlaying ? <Pause className="w-4 h-4" /> : <Play className="w-4 h-4" />}
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

      {/* Lyrics — popover panel anchored to the bar so the thin strip
          doesn't have to grow. Disabled when the current track carries no
          lyric body (most streaming rows). */}
      <Popover>
        <PopoverTrigger
          render={
            <button
              type="button"
              disabled={!currentTrack?.lyrics}
              aria-label="歌词"
              title={currentTrack?.lyrics ? '歌词' : '暂无歌词'}
              className="p-1.5 rounded hover:bg-accent disabled:opacity-40 disabled:cursor-not-allowed text-muted-foreground hover:text-foreground shrink-0"
            >
              <Mic2 className="w-4 h-4" />
            </button>
          }
        />
        <PopoverContent align="end" sideOffset={8} className="w-80">
          <div className="mb-2 min-w-0">
            <div className="text-sm font-medium truncate">{currentTrack?.title}</div>
            <div className="text-xs text-muted-foreground truncate">
              {currentTrack?.artist || '未知艺术家'}
            </div>
          </div>
          <LyricsView
            lyrics={currentTrack?.lyrics}
            currentTime={currentTime}
            variant="compact"
            className="max-h-72"
          />
        </PopoverContent>
      </Popover>
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
            onClick={togglePlay}
            disabled={!hasUrl}
            aria-label={isPlaying ? '暂停' : '播放'}
            className="p-2 rounded-full bg-primary text-primary-foreground disabled:opacity-40"
          >
            {isPlaying ? <Pause className="w-4 h-4" /> : <Play className="w-4 h-4" />}
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
