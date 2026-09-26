// Package utils: filename ⇒ tags parser for the 解析文件名 flow.
//
// # What this is for
//
// Files that arrive from a download have a well-formed name and no tags at
// all, which leaves the scraper nothing to match on. So the name is read for
// whatever fields it happens to carry, and those become the starting point
// the later scrape works from.
//
// # Two modes
//
// Without a pattern (the default), the name is split on a separator class
// and the position decides: first part is the artist, the rest is the
// title. That is the right guess for `Artist - Title.flac` and it is
// unchanged from what shipped.
//
// With a pattern, the caller says which capture is which field:
//
//	^(?P<artist>.+?) - (?P<album>.+?) - (?P<tracknumber>\d+) - (?P<title>.+)$
//
// Position is not a field. A library that names files
// `Artist - Album - 01 - Title` cannot be served by the default split, and
// the alternative — guessing that the third part is an album — is how a
// track ends up tagged with its own album name. A pattern makes the caller
// state it instead, which is the only version of this feature that writes
// the right thing without being told.
//
// # Named groups
//
// The field names a pattern may use are exactly:
//
//	title  artist  album  albumartist  genre  year  tracknumber  discnumber
//
// A group named anything else is an ERROR, not an unmatched row. A typo
// (`(?P<albumartist>...)` written as `(?P<album_artist>...)`) that is
// silently ignored produces a batch where every row parses fine and writes
// nothing, and nothing in the response says why.
//
// A pattern with no named groups keeps the older meaning: groups 1 and 2 are
// the artist and the title. That path exists because it is what shipped, not
// because it is recommended.
//
// # The algorithm (default mode)
//
//  1. NFC-normalise the basename (unicode/norm.NFC.String) — macOS emits
//     NFD CJK filenames.
//  2. Strip the file extension (everything after the LAST '.'). A basename
//     starting with '.' has no stem and is unparsable.
//  3. Split the stem on `\s*[-_/\\|·]\s*` (or opts.Separator), trim each
//     part, drop empties:
//     - 0 or 1 non-empty part → unparsable
//     - 2 parts               → ok (artist + title)
//     - 3+ parts              → ambiguous (first = artist, rest re-joined
//     with " - " = title, so the modal can show what the name looked like)
//
// # Status
//
// `ambiguous` is a default-mode concept only: a pattern states its own
// meaning, so a matching pattern is `ok` and a non-matching one is
// `unparsable`. Callers must consult Status before reading any field.
package utils

import (
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Status values. A single string (vs two booleans) eliminates invalid
// combos like ok=true AND ambiguous=true and round-trips over JSON.
const (
	StatusOK         = "ok"
	StatusAmbiguous  = "ambiguous"
	StatusUnparsable = "unparsable"
)

// defaultSeparator accepts any of {-, _, /, \, |, ·} with optional
// whitespace. NB `·` is U+00B7 MIDDLE DOT, not U+2022 BULLET; a library
// likely uses one or both.
var defaultSeparator = regexp.MustCompile(`\s*[-_/\\|·]\s*`)

// PatternFieldNames are the group names a pattern may use, in the order the
// UI shows them. It doubles as the allow-list: a group outside this set is
// rejected at compile time, which is what turns a typo into a 400 instead
// of a batch that quietly writes nothing.
var PatternFieldNames = []string{
	"title", "artist", "album", "albumartist",
	"genre", "year", "tracknumber", "discnumber",
}

var patternFieldSet = func() map[string]bool {
	m := make(map[string]bool, len(PatternFieldNames))
	for _, n := range PatternFieldNames {
		m[n] = true
	}
	return m
}()

// ParseOptions overrides the defaults. Empty fields fall back to default.
type ParseOptions struct {
	// Separator replaces defaultSeparator when non-empty, for libraries
	// that split on something the default class does not cover.
	Separator string

	// Pattern, when non-empty, replaces the positional split entirely.
	// Capture groups are named with the Go syntax `(?P<field>...)` and
	// must use a name from PatternFieldNames:
	//
	//	^(?P<artist>.+?) - (?P<album>.+?) - (?P<tracknumber>\d+) - (?P<title>.+)$
	//
	// A pattern with no named groups is read the old way — groups 1 and 2
	// are the artist and the title — which is what this field did before
	// it took a name.
	//
	// An invalid pattern is a hard error. The previous behaviour compiled
	// it per file and turned a compile failure into "unparsable", on the
	// reasoning that a typo should not fail the whole batch; the cost was
	// that a typo produced a preview where every row was unparsable and
	// nothing said why. A pattern is now a user-facing field with a
	// default that works, so a wrong one is a mistake worth reporting.
	Pattern string
}

// ParsedFilename is the per-call return shape. Status is the only field
// callers MUST consult before reading the rest: an unparsable row's fields
// are guaranteed empty.
type ParsedFilename struct {
	Title       string
	Artist      string
	Album       string
	AlbumArtist string
	Genre       string
	Year        string
	TrackNumber string
	DiscNumber  string
	Status      string
}

// Empty reports whether nothing at all was extracted. The apply path treats
// this as a no-op row rather than a failure.
func (p ParsedFilename) Empty() bool {
	return p.Title == "" && p.Artist == "" && p.Album == "" &&
		p.AlbumArtist == "" && p.Genre == "" && p.Year == "" &&
		p.TrackNumber == "" && p.DiscNumber == ""
}

// CompilePattern validates a pattern once, up front, and returns the
// compiled form. Callers that have a whole batch to run should compile
// here rather than letting each row discover the problem: the point of
// rejecting a bad pattern is to say so once, to the person who typed it.
func CompilePattern(pattern string) (*regexp.Regexp, error) {
	if strings.TrimSpace(pattern) == "" {
		return nil, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("模式无效: %w", err)
	}
	for _, name := range re.SubexpNames() {
		if name == "" {
			continue // unnamed group, or the whole match
		}
		if !patternFieldSet[name] {
			return nil, fmt.Errorf(
				"未知的字段名 %q；可用字段：%s",
				name, strings.Join(PatternFieldNames, " / "))
		}
	}
	return re, nil
}

// PortParseFilename extracts tags from `name` (basename; the caller strips
// the parent dir). See the package comment for the algorithm.
func PortParseFilename(name string, opts ParseOptions) ParsedFilename {
	return PortParseFilenameCompiled(name, opts, nil)
}

// PortParseFilenameCompiled is PortParseFilename for a caller that already
// compiled the pattern with CompilePattern. A non-nil `re` takes precedence
// over opts.Pattern, so the pattern is compiled once per request rather than
// once per file.
func PortParseFilenameCompiled(name string, opts ParseOptions, re *regexp.Regexp) ParsedFilename {
	// NFC normalisation. macOS Finder uploads frequently emit NFD-encoded
	// CJK filenames; without canonicalisation the regex would match a
	// different set of codepoints or fail outright.
	name = norm.NFC.String(name)

	stem := stripExt(name)
	if stem == "" {
		return ParsedFilename{Status: StatusUnparsable}
	}

	if re == nil && strings.TrimSpace(opts.Pattern) != "" {
		// Not pre-compiled (a direct caller of the simple API). A pattern
		// that does not compile is treated as "no pattern" here rather
		// than panicking; the HTTP layer compiles up front and reports,
		// so this path is only reachable from tests and internal callers.
		re, _ = CompilePattern(opts.Pattern)
	}
	if re != nil {
		return parseWithPattern(stem, re)
	}

	// Default separator split.
	pat := opts.Separator
	if pat == "" {
		pat = defaultSeparator.String()
	}
	sepRe, err := regexp.Compile(pat)
	if err != nil {
		// Invalid custom separator from the caller — degrade to
		// unparsable rather than 500 the whole preview batch.
		return ParsedFilename{Status: StatusUnparsable}
	}

	// Split, trim, drop empties.
	raw := sepRe.Split(stem, -1)
	parts := make([]string, 0, len(raw))
	for _, p := range raw {
		if t := strings.TrimSpace(p); t != "" {
			parts = append(parts, t)
		}
	}
	switch len(parts) {
	case 0, 1:
		return ParsedFilename{Status: StatusUnparsable}
	case 2:
		return ParsedFilename{Artist: parts[0], Title: parts[1], Status: StatusOK}
	default:
		// First = artist; the rest re-joined with " - " preserves the
		// separator, so the modal can show the name as the user typed it
		// if they accept the split.
		return ParsedFilename{
			Artist: parts[0],
			Title:  strings.Join(parts[1:], " - "),
			Status: StatusAmbiguous,
		}
	}
}

// parseWithPattern applies a caller-supplied pattern.
//
// Named groups land in their own field. Unnamed groups keep the old
// two-group reading (1 = artist, 2 = title) ONLY when the pattern declares
// no names at all; a pattern that names some groups and leaves others
// anonymous is read as "the named ones are what I asked for", because
// guessing which anonymous group was meant is the thing this whole mode
// exists to avoid.
//
// A group that matched empty is left empty rather than written as "" — the
// difference between "the pattern did not capture an album" and "the album
// is now empty" is the same one the tag write contract draws.
func parseWithPattern(stem string, re *regexp.Regexp) ParsedFilename {
	match := re.FindStringSubmatch(stem)
	if match == nil {
		return ParsedFilename{Status: StatusUnparsable}
	}

	var out ParsedFilename
	hasNames := false
	for i, name := range re.SubexpNames() {
		if i == 0 || name == "" {
			continue // group 0 is the whole match
		}
		hasNames = true
		if i >= len(match) {
			continue
		}
		// A group that matched empty stays empty. "The pattern did not
		// capture an album" and "the album is now empty" are different
		// statements, and collapsing them is the mistake this module
		// spends its comments avoiding elsewhere.
		if v := strings.TrimSpace(match[i]); v != "" {
			assignPatternField(&out, name, v)
		}
	}

	if !hasNames {
		// The pre-pattern reading, kept because it is what this field did
		// before it took a name: two unnamed groups, artist then title.
		if len(match) >= 3 {
			if v := strings.TrimSpace(match[1]); v != "" {
				out.Artist = v
			}
			if v := strings.TrimSpace(match[2]); v != "" {
				out.Title = v
			}
		}
	}
	return finishPattern(out)
}

// assignPatternField routes one captured value to its field. Split out so
// the loop above stays a straight scan over SubexpNames and this stays a
// table: adding a field is one line here plus one in PatternFieldNames.
func assignPatternField(out *ParsedFilename, name, v string) {
	switch name {
	case "title":
		out.Title = v
	case "artist":
		out.Artist = v
	case "album":
		out.Album = v
	case "albumartist":
		out.AlbumArtist = v
	case "genre":
		out.Genre = v
	case "year":
		out.Year = v
	case "tracknumber":
		out.TrackNumber = v
	case "discnumber":
		out.DiscNumber = v
	}
}

// finishPattern assigns the status. A pattern that matched but captured
// nothing usable is unparsable, not ok — "ok" with every field empty is a
// row the apply step will silently skip, and the preview said it parsed.
func finishPattern(p ParsedFilename) ParsedFilename {
	if p.Empty() {
		return ParsedFilename{Status: StatusUnparsable}
	}
	p.Status = StatusOK
	return p
}

// stripExt returns everything before the LAST '.' (so "Mr. A - B - C.mp3"
// keeps its dots). Empty if the basename starts with '.' or has no '.'.
func stripExt(name string) string {
	idx := strings.LastIndex(name, ".")
	if idx <= 0 {
		return ""
	}
	return name[:idx]
}
