// LRC / plain-text lyric parsing, shared by LyricsView (desktop popover +
// mobile NowPlaying). Kept out of the component file so the renderer can
// stay a pure component export (react-refresh) and so the parse is unit
// testable in isolation.
//
// The parse is intentionally forgiving: real-world LRC from the scrape
// plugins mixes multi-tag lines (`[00:12.00][01:30.00] repeated hook`),
// blank lines, and metadata tags (`[ti:]`, `[ar:]`, `[by:]`) we skip.

export interface LyricLine {
  /** Seconds. null for a plain-text line with no timestamp. */
  time: number | null;
  text: string;
}

// [mm:ss], [mm:ss.xx], [mm:ss.xxx] — one or more may prefix a single line.
const TIME_TAG = /\[(\d{1,2}):(\d{1,2})(?:[.:](\d{1,3}))?\]/g;
// Metadata-only tags we drop entirely: [ti:], [ar:], [al:], [by:], [offset:] …
const META_TAG = /^\[[a-z]+:.*\]$/i;

/** Parse an LRC / plain body into timed lines. When no line carries a
 *  timestamp, `synced` is false and the caller renders a static block. */
export function parseLyrics(raw: string): { lines: LyricLine[]; synced: boolean } {
  const out: LyricLine[] = [];
  let synced = false;
  for (const rawLine of raw.split(/\r?\n/)) {
    const line = rawLine.trim();
    if (!line) continue;
    // Pure metadata line with no time tag → skip.
    if (META_TAG.test(line) && !/\[\d{1,2}:\d{1,2}/.test(line)) continue;

    TIME_TAG.lastIndex = 0;
    const times: number[] = [];
    let m: RegExpExecArray | null;
    while ((m = TIME_TAG.exec(line)) !== null) {
      const min = Number(m[1]);
      const sec = Number(m[2]);
      const frac = m[3] ? Number(`0.${m[3]}`) : 0;
      times.push(min * 60 + sec + frac);
    }
    const text = line.replace(TIME_TAG, '').trim();
    if (times.length > 0) {
      synced = true;
      // A line may repeat at multiple timestamps ([00:12][01:30] hook).
      for (const t of times) out.push({ time: t, text });
    } else {
      out.push({ time: null, text });
    }
  }
  if (synced) out.sort((a, b) => (a.time ?? 0) - (b.time ?? 0));
  return { lines: out, synced };
}

/** Index of the active line for `currentTime`, or -1 before the first
 *  timestamp. Linear scan — lyric bodies are tens of lines, not thousands. */
export function activeLyricIndex(lines: LyricLine[], t: number): number {
  let idx = -1;
  for (let i = 0; i < lines.length; i++) {
    const time = lines[i].time;
    if (time === null) continue;
    if (time <= t) idx = i;
    else break;
  }
  return idx;
}
