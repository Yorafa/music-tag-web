// Frontend mirror of internal/utils/filenames.go — must produce
// identical (artist, title, status) over every fixture case.
//
// Algorithm:
//   1. NFC-normalise the basename.
//   2. Strip the file extension (everything after the LAST dot).
//      A hidden basename OR a basename with no dot yields an empty
//      stem, which classifies as unparsable.
//   3. If opts.fallbackRegex is set, run a 2-group capture on the
//      stem (artist = group 1, title = group 2).
//   4. Otherwise, split the stem on the DEFAULT separator regex
//      and decide:
//        - 0 or 1 non-empty part  -> unparsable, both fields empty.
//        - 2 non-empty parts      -> ok, both fields populated.
//        - 3+ non-empty parts     -> ambiguous, first = artist,
//                                    remainder joined with " - " = title.
//
// UPDATES TO THE ALGORITHM MUST LAND IN BOTH
// internal/utils/filenames.go AND this file. CI verifies the contract
// via deep-equality of structured outputs over the 200+ shared fixture.

export type ParseStatus = 'ok' | 'ambiguous' | 'unparsable';

export interface ParseOptions {
  separator?: string;
  fallbackRegex?: string;
}

export interface ParsedFilename {
  artist: string;
  title: string;
  status: ParseStatus;
}

// Default separator pattern. Accepts any of "- _ / \ | ·" surrounded
// by optional whitespace. The middle dot is the U+00B7 MIDDLE DOT.
export const DEFAULT_SEPARATOR = /\s*[-_/\\|·]\s*/;

// Status enum copies for ergonomic import sites.
export const StatusOK: ParseStatus = 'ok';
export const StatusAmbiguous: ParseStatus = 'ambiguous';
export const StatusUnparsable: ParseStatus = 'unparsable';

// Convenience empty / unparsable result shared by every exit path.
const EMPTY: ParsedFilename = {
  artist: '',
  title: '',
  status: StatusUnparsable,
};

export function parseFilename(
  name: string,
  opts: ParseOptions = {},
): ParsedFilename {
  if (!name) return EMPTY;

  // 1. NFC canonicalisation. macOS Finder uploads frequently emit
  //    NFD-encoded CJK filenames; without canonicalisation the regex
  //    either matches a different codepoint set or fails outright.
  const nfc: string = name.normalize('NFC');

  // 2. Strip the extension (everything after the LAST dot). Preserves
  //    dots inside titles like "Mr. A - B - C.mp3".
  const lastDot: number = nfc.lastIndexOf('.');
  if (lastDot <= 0) {
    // Leading-dot hidden OR no dot at all -> no stem to parse.
    return EMPTY;
  }
  const stem: string = nfc.substring(0, lastDot);
  if (stem === '') return EMPTY;

  // 3. FallbackRegex bypass (2-group capture).
  if (opts.fallbackRegex) {
    try {
      const re = new RegExp(opts.fallbackRegex);
      const m = re.exec(stem);
      if (m && m.length >= 3 && m[1] !== undefined && m[2] !== undefined) {
        return {
          artist: m[1].trim(),
          title: m[2].trim(),
          status: StatusOK,
        };
      }
      return EMPTY;
    } catch {
      return EMPTY;
    }
  }

  // 4. Default separator split.
  let parts: string[];
  try {
    const re = opts.separator
      ? new RegExp(opts.separator)
      : DEFAULT_SEPARATOR;
    parts = stem.split(re);
  } catch {
    return EMPTY;
  }

  // Trim each part, drop empties.
  const nonempty: string[] = [];
  for (const p of parts) {
    const t = p.trim();
    if (t.length > 0) nonempty.push(t);
  }

  switch (nonempty.length) {
    case 0:
    case 1:
      return EMPTY;
    case 2: {
      const a: string = nonempty[0] ?? '';
      const t: string = nonempty[1] ?? '';
      return { artist: a, title: t, status: StatusOK };
    }
    default: {
      // 3+ parts: first = artist; remainder joined with " - ".
      const a: string = nonempty[0] ?? '';
      const t: string = nonempty.slice(1).join(' - ');
      return { artist: a, title: t, status: StatusAmbiguous };
    }
  }
}
