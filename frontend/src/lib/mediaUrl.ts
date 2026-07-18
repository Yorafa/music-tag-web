// Media-stream URL builder.
//
// Local-file playback in BOTH modes (scrape mode Worklist rows and play mode
// PlayView rows) needs the same URL shape: `/media/<encoded-segments>` so
// the gateway's static file server hands back the byte stream. Extracted
// here so the two modes don't each roll their own per-segment encoder.
//
// Cross-plan contract (frozen — Plan A & Plan B both import from here):
//   - input  = a "relative-to-MUSIC_DIR" path. '' is the root; 'foo/bar/baz.flac'
//     is a file under it. The slash is the only path separator.
//   - output = `/media/` + each segment URL-encoded, joined by '/'. The
//     leading slash is literal so the SPA <audio src> resolves against the
//     gateway origin.
//   - encodeURIComponent is applied PER-SEGMENT so '/'s are preserved but
//     unicode / spaces / special chars in a filename don't break the URL.
//
// See also: src/lib/streamUrl.ts — resolveStreamUrl pulls the `url` we
// compute here and treats any non-empty url as the 'direct' branch, so the
// <audio> element just loads this URL with no /api/stream proxy detour.

/** Build the `/media/<encoded-rel-path>` URL for a local audio file.
 *  Example: 'foo/中文 bar.flac' → '/media/foo/%E4%B8%AD%E6%96%87%20bar.flac' */
export function buildMediaUrl(fullPath: string): string {
  const parts = fullPath.split('/').map((s) => encodeURIComponent(s));
  return `/media/${parts.join('/')}`;
}
