// Full-screen NowPlaying overlay for mobile (below 640px). The mini
// PlayerBar at the bottom of the screen mounts this overlay and owns the
// open state; NowPlaying itself reads transport state from
// usePlayerStore and writes through to the same store — no queue area
// per Plan B decision #10 (user picked "no queue, swipe down to dismiss").
//
// Layout (Plan B B4 / Navidrome-style minimal):
//   - Drag-down handle at top center (a short cold-gray bar). User can
//     drag the whole sheet down past 80px and release to dismiss.
//     Pointer events (pointerdown → pointermove → pointerup) over the
//     whole sheet, but only vertical downward motion accumulates
//     displacement; upward motion snaps back to 0 immediately.
//   - Album cover, square, sized to min(80vw, 320px) — caps on tablets
//     so it doesn't dominate a 768 viewport.
//   - Title + artist text rows.
//   - Seekbar: same range-input pattern PlayerBar uses; the store does
//     the heavy lifting, NowPlaying just mirrors.
//   - Transport row: Prev | Play/Pause | Next — icon-only, large tap
//     targets (~56px diameter).
//   - NO queue / library list.
//
// The overlay never blocks the audio element. The mini PlayerBar keeps
// the actual <audio> mounted; this component reads the same store and
// only writes transport commands (seek / togglePlay / next / prev).

import { useCallback, useEffect, useRef, useState } from 'react';
import { Play, Pause, SkipBack, SkipForward, ChevronDown } from 'lucide-react';
import { usePlayerStore } from '@/store/usePlayerStore';

function formatTime(s: number): string {
  if (!Number.isFinite(s) || s < 0) return '0:00';
  const m = Math.floor(s / 60);
  const sec = Math.floor(s % 60);
  return `${m}:${sec.toString().padStart(2, '0')}`;
}

interface Props {
  open: boolean;
  onClose: () => void;
}

const DISMISS_THRESHOLD_PX = 80;

export function NowPlaying({ open, onClose }: Props) {
  const currentTrack = usePlayerStore((s) => s.currentTrack);
  const isPlaying = usePlayerStore((s) => s.isPlaying);
  const currentTime = usePlayerStore((s) => s.currentTime);
  const duration = usePlayerStore((s) => s.duration);
  const togglePlay = usePlayerStore((s) => s.togglePlay);
  const seek = usePlayerStore((s) => s.seek);
  const next = usePlayerStore((s) => s.next);
  const prev = usePlayerStore((s) => s.prev);

  // Drag-down state — vertical displacement (px) the sheet has been
  // dragged down. Committed to 0 on every pointer up below threshold AND
  // when the sheet is dismissed (see onPointerUp). No effect-based reset
  // on open because upstream onClose invocation already returns the
  // value to 0 — last committed value of a closed sheet is always 0.
  const [dragY, setDragY] = useState(0);
  const dragOriginY = useRef<number | null>(null);

  // Lock body scroll while the overlay is open — the NowPlaying sheet
  // is fixed inset-0 and any background page scroll underneath would
  // show through the surface-1 background as a jitter. Mini-button at
  // <head> style would break the page; `overflow: hidden` is enough.
  useEffect(() => {
    if (!open) return;
    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      document.body.style.overflow = prevOverflow;
    };
  }, [open]);

  const onPointerDown = useCallback((e: React.PointerEvent) => {
    // Only the drag handle OR the handle bar should originate drags —
    // but we also accept drags started anywhere on the upper 64px so
    // the user doesn't have to be pixel-accurate on the handle.
    if (e.clientY > 80) return;
    dragOriginY.current = e.clientY;
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
  }, []);

  const onPointerMove = useCallback((e: React.PointerEvent) => {
    if (dragOriginY.current === null) return;
    const dy = e.clientY - dragOriginY.current;
    // Clamp to ≥0 — no upward lift, only downward dismiss intent.
    setDragY(Math.max(0, dy));
  }, []);

  const onPointerUp = useCallback(
    (e: React.PointerEvent) => {
      if (dragOriginY.current === null) return;
      const dy = e.clientY - dragOriginY.current;
      dragOriginY.current = null;
      try {
        (e.currentTarget as HTMLElement).releasePointerCapture(e.pointerId);
      } catch {
        /* pointer already released */
      }
      if (dy > DISMISS_THRESHOLD_PX) {
        // Reset displacement so the (now hidden) sheet doesn't render
        // translated after the parent reopens the overlay.
        setDragY(0);
        onClose();
      } else {
        setDragY(0);
      }
    },
    [onClose],
  );

  const onSeekInput = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const t = Number(e.target.value);
      seek(t);
    },
    [seek],
  );

  const progress = duration > 0 ? (currentTime / duration) * 100 : 0;
  const hasUrl = !!currentTrack?.url;

  if (!open || !currentTrack) return null;

  return (
    <div
      role="dialog"
      aria-label="正在播放 - 全屏视图"
      aria-modal="true"
      className="fixed inset-0 z-50 flex flex-col items-stretch px-4 pt-3 pb-6 select-none bg-surface-1 text-surface-1-foreground"
      style={{ transform: `translateY(${dragY}px)` }}
      // Drag is started from the top 80px (handle area). Transport and
      // seek inputs below it don't initiate a drag — they get the touch
      // event to themselves.
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onPointerCancel={onPointerUp}
    >
      {/* Drag handle */}
      <div className="flex justify-center shrink-0 touch-none">
        <button
          type="button"
          onPointerDown={onPointerDown}
          onClick={onClose}
          aria-label="关闭"
          title="下拉关闭"
          className="flex flex-col items-center gap-1 w-full max-w-[6rem] pt-1 pb-2 cursor-grab active:cursor-grabbing"
        >
          <div className="w-10 h-1 rounded-full bg-foreground/30" />
          <ChevronDown className="w-4 h-4 text-muted-foreground" />
        </button>
      </div>

      {/* Cover */}
      <div className="flex-1 min-h-0 flex items-center justify-center">
        <div className="w-[min(80vw,22rem)] aspect-square rounded-xl overflow-hidden bg-surface-2 shadow-2xl">
          {currentTrack.cover ? (
            <img
              src={currentTrack.cover}
              alt={currentTrack.title}
              className="w-full h-full object-cover"
            />
          ) : (
            <div className="w-full h-full flex items-center justify-center text-6xl text-muted-foreground/60">
              ♪
            </div>
          )}
        </div>
      </div>

      {/* Title + artist */}
      <div className="text-center mt-4 shrink-0 space-y-1">
        <h2 className="text-lg font-semibold truncate px-4">
          {currentTrack.title}
        </h2>
        <p className="text-sm text-muted-foreground truncate px-4">
          {currentTrack.artist || '未知艺术家'}
        </p>
      </div>

      {/* Seekbar */}
      <div className="flex items-center gap-2 mt-4 shrink-0 px-2">
        <span className="text-xs font-mono text-muted-foreground tabular-nums w-10 text-right">
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
          className="flex-1 accent-primary h-1.5 cursor-pointer disabled:cursor-not-allowed touch-none"
          style={{
            background: `linear-gradient(to right, var(--primary) 0% ${progress}%, var(--border) ${progress}% 100%)`,
          }}
        />
        <span className="text-xs font-mono text-muted-foreground tabular-nums w-10">
          {formatTime(duration)}
        </span>
      </div>

      {/* Transport */}
      <div className="flex items-center justify-center gap-8 mt-3 shrink-0">
        <button
          onClick={prev}
          disabled={!currentTrack}
          aria-label="上一首"
          className="p-3 rounded-full hover:bg-surface-2 disabled:opacity-40 disabled:cursor-not-allowed"
        >
          <SkipBack className="w-7 h-7" />
        </button>
        <button
          onClick={togglePlay}
          disabled={!hasUrl}
          aria-label={isPlaying ? '暂停' : '播放'}
          className="p-5 rounded-full bg-primary text-primary-foreground hover:bg-primary/90 disabled:opacity-40 disabled:cursor-not-allowed"
        >
          {isPlaying ? <Pause className="w-8 h-8" /> : <Play className="w-8 h-8" />}
        </button>
        <button
          onClick={next}
          disabled={!currentTrack}
          aria-label="下一首"
          className="p-3 rounded-full hover:bg-surface-2 disabled:opacity-40 disabled:cursor-not-allowed"
        >
          <SkipForward className="w-7 h-7" />
        </button>
      </div>
    </div>
  );
}
