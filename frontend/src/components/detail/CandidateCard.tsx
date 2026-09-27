// One candidate in the 候选 tab.
//
// Split out of TrackInspector because this is where requirement "let me see
// what a candidate actually is" lives, and it is the part with the most
// state (the expand toggle, one per candidate). The card shows the two or
// three facts that identify a record at a glance; everything else — ids,
// year, genre, cover URL, whether the source even sent lyrics — is behind
// 「详情」, because the alternative was a card so tall that five candidates
// filled the tab.
//
// The expanded facts are what the user needs in order to CHOOSE: two
// candidates with the same title and artist differ by release year, by
// album, and by whether one of them is the live version. Showing only the
// title left the choice to "apply this one and see if the tags look right",
// which writes to the file to find out.

import { useState } from 'react';
import { ChevronDown, ChevronRight, Fingerprint } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import type { SongInfo } from '@/types';

interface Props {
  candidate: SongInfo;
  onApply: (c: SongInfo) => void;
}

/** A field that may simply be absent, rendered as a dash rather than an
 *  empty cell so a row of dashes is visibly "this source has no such
 *  field" instead of a rendering bug. */
function Fact({
  label,
  value,
  mono,
}: {
  label: string;
  value?: string | number | null;
  mono?: boolean;
}) {
  const text = value === undefined || value === null || value === '' ? '—' : String(value);
  return (
    <div className="flex items-start gap-2 text-xs min-w-0">
      <span className="text-muted-foreground w-16 shrink-0">{label}</span>
      <span
        className={cn('text-foreground min-w-0 break-all', mono && 'font-mono text-[11px]')}
        title={text}
      >
        {text}
      </span>
    </div>
  );
}

export function CandidateCard({ candidate: c, onApply }: Props) {
  const [open, setOpen] = useState(false);

  // Two different claims, never merged into one number. `score` is a
  // confidence in the audio and only a source that listened to the file
  // sets it; the old code multiplied a title-similarity sum by 20 to
  // manufacture a percentage, which displayed a flat 40% for every
  // candidate of a normal scrape.
  const confidence =
    typeof c.score === 'number' && c.score > 0 ? Math.round(c.score * 100) : null;
  const titleFact =
    c.title_match === 'exact'
      ? '标题完全匹配'
      : c.title_match === 'partial'
        ? '标题部分匹配'
        : null;
  const lyric = c.lyric || c.lyrics || '';

  return (
    <div className="p-4 rounded-xl border border-border/70 bg-surface-2/60 hover:bg-surface-2 hover:border-primary/50 transition-all space-y-3">
      <div className="flex items-start gap-2.5">
        <div className="w-12 h-12 rounded-lg overflow-hidden bg-muted shrink-0 ring-1 ring-border/50">
          {c.album_img ? (
            <img src={c.album_img} alt="" className="w-full h-full object-cover" />
          ) : (
            <div className="w-full h-full flex items-center justify-center bg-primary/10 text-primary text-sm font-bold">
              {(c.name || '?').charAt(0)}
            </div>
          )}
        </div>

        <div className="flex-1 min-w-0 space-y-0.5">
          <div className="flex items-center justify-between gap-1">
            <span className="text-sm font-semibold text-foreground truncate">{c.name}</span>
            <Badge variant="outline" className="text-[10px] px-1.5 h-4 uppercase font-mono shrink-0">
              {c.source || 'cloud'}
            </Badge>
          </div>
          <p className="text-xs text-muted-foreground truncate">
            {c.artist || '未知'} · {c.album || '单曲'}
          </p>
          {/* The one extra line that costs nothing and disambiguates the
              commonest pair of look-alikes: a remaster, a live cut, a
              different year. */}
          {(c.year || c.genre) && (
            <p className="text-[11px] text-muted-foreground/80 truncate">
              {[c.year, c.genre].filter(Boolean).join(' · ')}
            </p>
          )}
        </div>
      </div>

      {open && (
        <div className="space-y-1.5 pt-2 border-t border-border/40">
          <Fact label="音源 ID" value={c.id} mono />
          <Fact label="艺术家 ID" value={c.artist_id} mono />
          <Fact label="专辑 ID" value={c.album_id} mono />
          <Fact label="专辑年份" value={c.year} mono />
          <Fact label="流派" value={c.genre} />
          <Fact label="封面链接" value={c.album_img} mono />
          <Fact label="声纹置信" value={confidence === null ? undefined : `${confidence}%`} mono />
          <Fact label="标题比对" value={titleFact} />
          <div className="flex items-start gap-2 text-xs min-w-0">
            <span className="text-muted-foreground w-16 shrink-0">歌词</span>
            <span className="text-foreground min-w-0 flex-1">
              {lyric ? (
                <pre className="max-h-32 overflow-y-auto whitespace-pre-wrap break-words font-mono text-[11px] leading-relaxed bg-muted/40 rounded p-2">
                  {lyric}
                </pre>
              ) : (
                <span className="text-muted-foreground">该音源未返回歌词</span>
              )}
            </span>
          </div>
        </div>
      )}

      <div className="flex items-center justify-between pt-2 border-t border-border/40">
        <div className="flex items-center gap-1.5 text-xs text-muted-foreground min-w-0">
          {confidence ? (
            <>
              <Fingerprint className="w-3 h-3 shrink-0" />
              <span className="shrink-0">声纹匹配</span>
              <span className="font-mono font-bold text-foreground">{confidence}%</span>
            </>
          ) : titleFact ? (
            <span>{titleFact}</span>
          ) : (
            <span className="italic">未按音频校验</span>
          )}
        </div>
        <div className="flex items-center gap-1.5 shrink-0">
          <Button
            variant="ghost"
            onClick={() => setOpen((v) => !v)}
            className="h-8 px-2 text-xs text-muted-foreground hover:text-foreground"
            aria-expanded={open}
          >
            {open ? <ChevronDown className="w-3.5 h-3.5" /> : <ChevronRight className="w-3.5 h-3.5" />}
            {open ? '收起详情' : '详情'}
          </Button>
          <Button
            variant="secondary"
            onClick={() => onApply(c)}
            className="h-8 px-3 text-xs text-primary hover:bg-primary hover:text-primary-foreground font-semibold"
          >
            应用此标签
          </Button>
        </div>
      </div>
    </div>
  );
}
