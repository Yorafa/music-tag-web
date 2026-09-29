// Shared lyrics renderer for both player surfaces (desktop PlayerBar
// popover + mobile NowPlaying overlay). Takes a raw lyric body — plain
// text OR LRC with `[mm:ss.xx]` time tags — and renders it. When the
// body carries time tags AND the caller passes the current playback
// position, the active line is highlighted and auto-scrolled into view
// (Navidrome-style karaoke follow). Without time tags it degrades to a
// plain scrollable text block.
//
// The LRC / plain parse lives in @/lib/lyrics so this file stays a pure
// component export (react-refresh) and the parse is unit-testable.

import { useEffect, useMemo, useRef } from 'react';
import { parseLyrics, activeLyricIndex } from '@/lib/lyrics';
import { cn } from '@/lib/utils';

interface Props {
  lyrics?: string;
  currentTime: number;
  /** Compact desktop-popover styling vs. roomy full-screen mobile. */
  variant?: 'compact' | 'full';
  /** Seconds to shift the playhead by when picking the active line. A
   *  positive value makes lines light up earlier (the track's lyrics run
   *  ahead of the audio), negative later. Only affects synced LRC. */
  offset?: number;
  /** Seek to a line's timestamp when the listener taps it. Only wired for
   *  synced LRC lines (a plain-text line has no time to jump to). The
   *  passed time is the raw line timestamp; the caller (player) applies the
   *  same `offset` correction on playback so tap-to-jump lands where the
   *  highlight is. */
  onSeek?: (time: number) => void;
  className?: string;
}

export function LyricsView({ lyrics, currentTime, variant = 'full', offset = 0, onSeek, className }: Props) {
  const { lines, synced } = useMemo(() => parseLyrics(lyrics ?? ''), [lyrics]);
  const activeIdx = synced ? activeLyricIndex(lines, currentTime + offset) : -1;
  const scrollRef = useRef<HTMLDivElement>(null);
  const activeRef = useRef<HTMLParagraphElement>(null);

  // Karaoke follow: keep the active line centered. Only runs when the
  // active index changes (not on every timeupdate tick) so the scroll
  // stays smooth and doesn't fight the user scrolling manually between
  // line changes.
  useEffect(() => {
    if (!synced || activeIdx < 0) return;
    const el = activeRef.current;
    const container = scrollRef.current;
    if (!el || !container) return;
    const top = el.offsetTop - container.clientHeight / 2 + el.clientHeight / 2;
    container.scrollTo({ top: Math.max(0, top), behavior: 'smooth' });
  }, [activeIdx, synced]);

  if (lines.length === 0) {
    return (
      <div
        className={cn(
          'flex items-center justify-center text-muted-foreground text-sm',
          className,
        )}
      >
        暂无歌词
      </div>
    );
  }

  const isCompact = variant === 'compact';

  return (
    <div
      ref={scrollRef}
      className={cn(
        'overflow-y-auto',
        isCompact ? 'text-sm leading-7' : 'text-center text-base leading-9',
        className,
      )}
    >
      {lines.map((line, i) => {
        const isActive = synced && i === activeIdx;
        // A synced line carries a timestamp, so tapping it can jump the
        // playhead there. Plain-text lines (time === null) and the case
        // where no onSeek handler is wired stay non-interactive.
        const seekable = onSeek != null && line.time !== null;
        return (
          <p
            key={i}
            ref={isActive ? activeRef : undefined}
            onClick={seekable ? () => onSeek(line.time as number) : undefined}
            role={seekable ? 'button' : undefined}
            tabIndex={seekable ? 0 : undefined}
            onKeyDown={
              seekable
                ? (e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault();
                      onSeek(line.time as number);
                    }
                  }
                : undefined
            }
            className={cn(
              'transition-colors duration-300',
              synced
                ? isActive
                  ? 'text-foreground font-medium'
                  : 'text-muted-foreground/50'
                : 'text-foreground/80',
              seekable && 'cursor-pointer hover:text-foreground',
              !line.text && 'h-4', // keep blank lines as spacing
            )}
          >
            {line.text || ' '}
          </p>
        );
      })}
    </div>
  );
}
