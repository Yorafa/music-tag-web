// Package utils: filename ⇒ (artist, title) parser for the C.2
// Filename Parse backend round-trip. The frontend mirror at
// `frontend/src/utils/parseFilename.ts` and `parseFilename.testdata.json`
// MUST produce identical classified results for every fixture entry; this
// file is the source of truth.
//
// Algorithm (chosen by the C.2 architecture round):
//  1. NFC-normalise the basename (Go: unicode/norm.NFC.String, TS: name.normalize('NFC')).
//  2. Strip the file extension (everything after the LAST '.').
//     Special cases: ".hidden" → empty stem → unparsable.
//  3. If `opts.FallbackRegex` is set, run a FindStringSubmatch and
//     unwrap groups 1/2 (artist/title) — used when the operator supplies
//     a project-specific capture pattern ("DISC-N TRACK-N ..." etc.).
//  4. Otherwise, split the stem on the DEFAULT separator regex
//     `\s*[-_/\\|·]\s*` (or a custom pattern from opts.Separator):
//     - 0 or 1 non-empty part → "unparsable"
//     - 2 non-empty parts     → "ok" (artist+title)
//     - 3+ non-empty parts    → "ambiguous" (first = artist;
//     rest joined with " - " = title)
//  5. Trim each part's whitespace before counting / assembling so
//     leading/trailing separators don't generate ghost empty parts.
//
// Each non-empty part is also trimmed before being returned — the
// final-form (Artist, Title) is therefore the same in both engines.
//
// Updates to the algorithm MUST land in BOTH `internal/utils/filenames.go`
// AND `frontend/src/utils/parseFilename.ts` (and the fixture file pair).
// CI verifies the contract via deep-equality of structured outputs.
package utils

import (
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Status values mirror the frontend enum for PortParseFilename. A
// single string (vs two booleans) eliminates invalid combos like ok=true
// AND ambiguous=true and trivially round-trips over JSON.
const (
	StatusOK         = "ok"
	StatusAmbiguous  = "ambiguous"
	StatusUnparsable = "unparsable"
)

// defaultSeparator is the C.2 Step 1 default pattern. The character
// class accepts any of {-, _, /, \, |, ·} surrounded by optional
// whitespace on either side. Trailing / leading separators tolerated.
//
// NB: `·` is the U+00B7 MIDDLE DOT, NOT the U+2022 BULLET (the
// user's music library likely uses one or both — we accept both via
// the fronted TS regex too).
var defaultSeparator = regexp.MustCompile(`\s*[-_/\\|·]\s*`)

// ParseOptions overrides the defaults. Empty fields fall back to default.
type ParseOptions struct {
	// Separator replaces defaultSeparator when non-empty. Operators can
	// configure a project-specific split character (e.g. a literal "  -  "
	// if all their files use a wider dash WITH surrounding spaces, which
	// defaultSeparator tolerates but a stricter pattern would reject).
	Separator string
	// FallbackRegex, when non-empty, fully bypasses the separator split
	// and runs a 2-group capture on the stem instead. Use case: a label
	// project that orders "TRACK-N ARTIST - TITLE" and wants to skip
	// the separator split. Compile-failure on the regex is treated as
	// "unparsable" rather than a 4xx so a typo in field doesn't break
	// the whole preview.
	FallbackRegex string
}

// ParsedFilename is the per-call return shape. Status is the only field
// that callers MUST consult before reading Artist / Title — an
// "unparsable" row's Artist/Title are guaranteed empty.
type ParsedFilename struct {
	Artist string
	Title  string
	Status string
}

// PortParseFilename extracts Artist + Title from `name` (basename; the
// caller is responsible for stripping the parent dir before calling).
// See algorithm block at the top of the file.
func PortParseFilename(name string, opts ParseOptions) ParsedFilename {
	// NFC normalisation. macOS Finder uploads frequently emit NFD-encoded
	// CJK filenames; without canonicalisation the regex would either match
	// a different set of codepoints or fail outright. Same call site in
	// the TS mirror (`name.normalize('NFC')`).
	name = norm.NFC.String(name)

	stem := stripExt(name)
	if stem == "" {
		return ParsedFilename{Status: StatusUnparsable}
	}

	// FallbackRegex bypass path (full-match capture).
	if opts.FallbackRegex != "" {
		re, err := regexp.Compile(opts.FallbackRegex)
		if err == nil && re.MatchString(stem) {
			m := re.FindStringSubmatch(stem)
			if len(m) >= 3 {
				return ParsedFilename{
					Artist: strings.TrimSpace(m[1]),
					Title:  strings.TrimSpace(m[2]),
					Status: StatusOK,
				}
			}
		}
		return ParsedFilename{Status: StatusUnparsable}
	}

	// Default separator split.
	pat := opts.Separator
	if pat == "" {
		pat = defaultSeparator.String()
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		// Invalid custom regex from caller — degrade gracefully rather
		// than 500 the whole preview batch.
		return ParsedFilename{Status: StatusUnparsable}
	}

	// Split, trim, drop empties.
	raw := re.Split(stem, -1)
	nonempty := make([]string, 0, len(raw))
	for _, p := range raw {
		if t := strings.TrimSpace(p); t != "" {
			nonempty = append(nonempty, t)
		}
	}
	switch len(nonempty) {
	case 0, 1:
		return ParsedFilename{Status: StatusUnparsable}
	case 2:
		return ParsedFilename{
			Artist: nonempty[0],
			Title:  nonempty[1],
			Status: StatusOK,
		}
	default:
		// First = artist; remaining re-joined with " - " preserves the
		// separator for round-trip display fidelity ("A - B - C" → artist
		// "A" + title "B - C"). The composed title is what the user's
		// label looked like originally, so the modal can show what they
		// typed IF they accept the ambiguous split.
		return ParsedFilename{
			Artist: nonempty[0],
			Title:  strings.Join(nonempty[1:], " - "),
			Status: StatusAmbiguous,
		}
	}
}

// stripExt returns everything before the LAST '.' (preserves dots in
// titles like "Mr. A - B - C.mp3"). Empty if the basename starts with
// '.' OR has no '.' at all.
func stripExt(name string) string {
	idx := strings.LastIndex(name, ".")
	if idx <= 0 {
		return ""
	}
	return name[:idx]
}
