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
import { ChevronDown, ChevronRight, Fingerprint, RefreshCw } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import { fieldLabel, type ApplyableField } from '@/components/workstation/scrapedInfo';
import { fetchLyricForSong } from '@/api/lyrics';
import type { SongInfo } from '@/types';

interface Props {
  candidate: SongInfo;
  /** `fields` omitted means the whole candidate. */
  onApply: (c: SongInfo, fields?: ApplyableField[]) => void;
  /** The lyric already fetched for this candidate, cached by the parent so
   *  it survives paging and tab switches (both unmount the card). `undefined`
   *  means never fetched; `''` means fetched and the source had none. */
  cachedLyric?: string;
  /** Hand a freshly fetched lyric to the parent cache. */
  onLyricFetched: (lyric: string) => void;
}

/** A field that may simply be absent, rendered as a dash rather than an
 *  empty cell so a row of dashes is visibly "this source has no such
 *  field" instead of a rendering bug.
 *
 *  `applyField` is what turns this from a read-only list into a set of
 *  decisions: it appears only on a field the candidate actually carries,
 *  because offering 「应用年份」 on a source with no year would write
 *  nothing and look broken. */
function Fact({
  label,
  value,
  mono,
  applyField,
  onApplyField,
}: {
  label: string;
  value?: string | number | null;
  mono?: boolean;
  applyField?: ApplyableField;
  onApplyField?: (field: ApplyableField) => void;
}) {
  const hasValue = value !== undefined && value !== null && value !== '';
  const text = hasValue ? String(value) : '—';
  // Only offered where there is something to apply: a 「应用年份」 on a
  // source with no year would write nothing and read as broken.
  const canApply = applyField !== undefined && hasValue && onApplyField !== undefined;
  return (
    <div className="flex items-start gap-2 text-xs min-w-0 group/fact">
      <span className="text-muted-foreground w-16 shrink-0">{label}</span>
      <span
        className={cn('text-foreground min-w-0 break-all flex-1', mono && 'font-mono text-[11px]')}
        title={text}
      >
        {text}
      </span>
      {canApply && (
        <button
          type="button"
          onClick={() => onApplyField?.(applyField)}
          className="shrink-0 text-[10px] text-primary hover:underline"
          title={`只应用这一项：${fieldLabel(applyField)}`}
        >
          应用
        </button>
      )}
    </div>
  );
}

export function CandidateCard({ candidate: c, onApply, cachedLyric, onLyricFetched }: Props) {
  const [open, setOpen] = useState(false);
  const applyOnly = (field: ApplyableField) => onApply(c, [field]);

  // Lyrics are not part of a candidate: the proto Song message has no lyric
  // field, so a search never carries one and `c.lyric`/`c.lyrics` are always
  // empty for the current backend. The lyric lives behind a separate
  // FetchLyric(song_id) call, and this candidate already holds the very
  // song_id + source that call needs — so fetch it on demand rather than
  // showing a permanent "该音源未返回歌词".
  //
  // The fetched result is cached by the parent (keyed by source+id), not held
  // in this card's own state: the card unmounts on every page turn and tab
  // switch, so local state made the user re-fetch the same lyric each time
  // they came back to 候选. `cachedLyric === undefined` means never fetched;
  // `''` means fetched and the source had none.
  const [fetchingLyric, setFetchingLyric] = useState(false);
  const lyricTried = cachedLyric !== undefined;

  const canFetchLyric = Boolean(c.id && c.source);
  // Fetch only — never auto-apply. The user asked to keep 获取 and 应用
  // separate so fetching a lyric to read it doesn't overwrite the field they
  // may have already set from another source. The fetched text is previewed;
  // the 应用 button below commits it.
  const handleFetchLyric = async () => {
    if (!canFetchLyric || fetchingLyric) return;
    setFetchingLyric(true);
    try {
      const lyric = await fetchLyricForSong(c.id, c.source ?? '');
      onLyricFetched(lyric ?? '');
    } finally {
      setFetchingLyric(false);
    }
  };

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
  // The fetched lyric wins over the (structurally always-empty) candidate
  // fields, so a card that has been fetched shows and applies the real thing.
  const effectiveLyric = cachedLyric || c.lyric || c.lyrics || '';

  return (
    <div className="p-4 rounded-xl border border-border/70 bg-surface-2/60 hover:bg-surface-2 hover:border-primary/50 transition-all space-y-3">
      <div className="flex items-start gap-2.5">
        {c.album_img ? (
          // Clicking the cover applies just it — the whole point of the
          // "封面链接 → 应用" row, but discoverable, since a cover is the one
          // field you judge by eye rather than by reading text.
          <button
            type="button"
            onClick={() => applyOnly('album_img')}
            title="应用这张封面"
            className="w-12 h-12 rounded-lg overflow-hidden bg-muted shrink-0 ring-1 ring-border/50 hover:ring-primary transition-shadow"
          >
            <img src={c.album_img} alt="" className="w-full h-full object-cover" />
          </button>
        ) : (
          <div className="w-12 h-12 rounded-lg overflow-hidden bg-muted shrink-0 ring-1 ring-border/50">
            <div className="w-full h-full flex items-center justify-center bg-primary/10 text-primary text-sm font-bold">
              {(c.name || '?').charAt(0)}
            </div>
          </div>
        )}

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
          {/* The ids first: they are the reason to look, and they are
              read-only — an id3 frame has nowhere to put a source's own
              album id, so there is no "apply" for them. */}
          <Fact label="音源 ID" value={c.id} mono />
          <Fact label="艺术家 ID" value={c.artist_id} mono />
          <Fact label="专辑 ID" value={c.album_id} mono />
          <Fact label="声纹置信" value={confidence === null ? undefined : `${confidence}%`} mono />
          <Fact label="标题比对" value={titleFact} />

          {/* The writable half, each with its own 应用. "应用此标签" takes
              everything from one source, which is right most of the time
              and wrong exactly when the user wants this source's album and
              that source's lyrics — which is most of the reason to look
              at a second candidate at all. */}
          <div className="pt-1.5 mt-1.5 border-t border-border/30 space-y-1.5">
            <Fact
              label="标题"
              value={c.name}
              applyField="title"
              onApplyField={applyOnly}
            />
            <Fact
              label="艺术家"
              value={c.artist}
              applyField="artist"
              onApplyField={applyOnly}
            />
            <Fact label="专辑" value={c.album} applyField="album" onApplyField={applyOnly} />
            <Fact
              label="专辑年份"
              value={c.year}
              mono
              applyField="year"
              onApplyField={applyOnly}
            />
            <Fact label="流派" value={c.genre} applyField="genre" onApplyField={applyOnly} />
            <Fact
              label="封面链接"
              value={c.album_img}
              mono
              applyField="album_img"
              onApplyField={applyOnly}
            />
          {/* Lyrics get their own stacked block, not a label/value/apply row
              like the other facts: the preview can be 256px tall, and when it
              was a sibling in an `items-start` row the 应用 button collapsed to
              a ~14px sliver pinned beside a huge block — unhittable on touch
              and easy to miss on desktop. Header row (label + 应用) on top,
              full-width preview below, so 应用 is always a clear target. */}
          <div className="text-xs min-w-0">
            <div className="flex items-center gap-2 min-h-6">
              <span className="text-muted-foreground w-16 shrink-0">歌词</span>
              {effectiveLyric ? (
                <button
                  type="button"
                  onClick={() => onApply({ ...c, lyric: effectiveLyric }, ['lyrics'])}
                  className="text-[11px] px-2 py-0.5 rounded text-primary hover:bg-primary/10 transition-colors"
                  title="只应用歌词"
                >
                  应用歌词
                </button>
              ) : lyricTried ? (
                <span className="text-muted-foreground">该音源未找到歌词</span>
              ) : canFetchLyric ? (
                // Not "该音源未返回歌词": a search never carries lyrics, so
                // that message was always shown and always misleading. The
                // lyric is one FetchLyric(song_id) call away — fetch it to
                // preview here; applying it is the separate 应用 button, so
                // reading a source's lyric never clobbers a field already set.
                <button
                  type="button"
                  onClick={handleFetchLyric}
                  disabled={fetchingLyric}
                  className="inline-flex items-center gap-1 text-primary hover:underline disabled:opacity-60"
                >
                  <RefreshCw className={cn('w-3 h-3', fetchingLyric && 'animate-spin')} />
                  {fetchingLyric ? '获取中…' : '获取歌词'}
                </button>
              ) : (
                <span className="text-muted-foreground">该音源不支持歌词</span>
              )}
            </div>
            {effectiveLyric && (
              <pre className="mt-1.5 max-h-64 overflow-y-auto whitespace-pre-wrap break-words font-mono text-[11px] leading-relaxed bg-muted/40 rounded p-2">
                {effectiveLyric}
              </pre>
            )}
          </div>
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
