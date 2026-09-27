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
  className?: string;
}

export function LyricsView({ lyrics, currentTime, variant = 'full', className }: Props) {
  const { lines, synced } = useMemo(() => parseLyrics(lyrics ?? ''), [lyrics]);
  const activeIdx = synced ? activeLyricIndex(lines, currentTime) : -1;
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
        return (
          <p
            key={i}
            ref={isActive ? activeRef : undefined}
            className={cn(
              'transition-colors duration-300',
              synced
                ? isActive
                  ? 'text-foreground font-medium'
                  : 'text-muted-foreground/50'
                : 'text-foreground/80',
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
