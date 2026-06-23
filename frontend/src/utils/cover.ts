/**
 * Cover-rendering helpers shared by the row-level `CoverThumb`
 * (SearchResults.tsx) and the detail-level `DetailCover` (TagEditor.tsx).
 *
 * A typical mutagen base64 cover is well under 100 KB; payloads above
 * ~300 KB chars (~225 KB binary after base64 padding) almost always
 * come from a runaway high-res scan and can lock the browser on decode,
 * silently unmounting the React tree. We cap inline renders at
 * `MAX_INLINE_COVER_CHARS` and bail to the gradient placeholder beyond
 * that point.
 */

/** Stable six-entry palette — same on row + detail so the same song's
 *  gradient matches across both views. */
export const COVER_PLACEHOLDER_GRADIENTS: ReadonlyArray<string> = [
  'linear-gradient(135deg,#fb7185,#fb923c)',
  'linear-gradient(135deg,#f59e0b,#ec4899)',
  'linear-gradient(135deg,#10b981,#06b6d4)',
  'linear-gradient(135deg,#a78bfa,#6366f1)',
  'linear-gradient(135deg,#22d3ee,#3b82f6)',
  'linear-gradient(135deg,#f472b6,#a855f7)',
];

/** Hard cap on inline `<img src=data:image/…>` payload size. */
export const MAX_INLINE_COVER_CHARS = 300_000;

/** Pick the best cover source available on an id3 record. Prefers the
 *  embedded base64 `artwork` over the HTTP `album_img`; rejects the
 *  empty-payload sentinel `"data:image/...,"` that mutagen returns when
 *  no cover is embedded. Returns undefined when neither field carries
 *  a usable URL. Defends against a nullish record. */
export function resolveCoverSrc(
  info: { artwork?: string; album_img?: string } | null | undefined,
): string | undefined {
  if (!info) return undefined;
  const artwork = info.artwork;
  if (artwork && !artwork.endsWith(',')) return artwork;
  const albumImg = info.album_img;
  if (albumImg && !albumImg.endsWith(',')) return albumImg;
  return undefined;
}

/** Inspect a cover URL and return the booleans a cover component wants:
 *  `isEmptyPayload` (mutagen sentinels `"data:image/...,"`) and
 *  `isOversized` (over `MAX_INLINE_COVER_CHARS`). Caller decides
 *  `showImg` itself by AND-ing with `imgFailed` — keeps the helper
 *  stateless so `imgFailed` (which flips on `<img>.onError`) stays in
 *  component scope. */
export function inspectCoverSrc(src: string | undefined): {
  isEmptyPayload: boolean;
  isOversized: boolean;
  tooltip: string;
} {
  const isEmptyPayload = !src || src.endsWith(',');
  const isOversized = !!src && src.length > MAX_INLINE_COVER_CHARS;
  const tooltip = isOversized
    ? '封面过大，仅显示占位'
    : isEmptyPayload
    ? '暂无封面'
    : '封面';
  return { isEmptyPayload, isOversized, tooltip };
}

/** Deterministic integer id from a string. The detail header derives the
 *  same gradient slot the row thumb would have picked for the same file
 *  (via `file.id`), so a song's color matches across row + detail. */
export function stableIdFromString(s: string): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) | 0;
  return Math.abs(h);
}
