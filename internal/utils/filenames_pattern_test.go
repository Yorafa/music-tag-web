package utils

import (
	"strings"
	"testing"
)

// TestPortParseFilename_NamedGroupsFillTheirOwnField is the core of the
// pattern mode: a group named `album` lands in Album, not wherever it
// happens to sit.
//
// The failure this pins is the one positional inference would cause. A
// library naming files `Artist - Album - NN - Title` fed to the default
// split yields artist="Artist", title="Album - 01 - Title" — every track in
// the album carrying the album's name as its title. The pattern exists so
// the caller states the mapping instead of the parser guessing.
func TestPortParseFilename_NamedGroupsFillTheirOwnField(t *testing.T) {
	pattern := `^(?P<artist>.+?) - (?P<album>.+?) - (?P<tracknumber>\d+) - (?P<title>.+)$`
	got := PortParseFilename("Blur - Parklife - 01 - Girls & Boys.flac", ParseOptions{Pattern: pattern})

	for _, want := range []struct {
		field string
		got   string
	}{
		{"Artist", got.Artist},
		{"Album", got.Album},
		{"TrackNumber", got.TrackNumber},
		{"Title", got.Title},
	} {
		if want.got == "" {
			t.Errorf("%s is empty; the named group was not read into its own field", want.field)
		}
	}
	if got.Status != StatusOK {
		t.Errorf("Status = %q, want %q", got.Status, StatusOK)
	}
}

// TestPortParseFilename_EveryFieldNameRoutesSomewhere walks the whole
// allow-list. Each name is a branch in assignPatternField, and a name that
// compiles but routes nowhere produces a row that parses "ok" and writes
// nothing — so the list is the test.
func TestPortParseFilename_EveryFieldNameRoutesSomewhere(t *testing.T) {
	// A pattern naming every field, each fed a distinct sentinel value.
	sentinels := map[string]string{
		"title":       "TITLEVALUE",
		"artist":      "ARTISTVALUE",
		"album":       "ALBUMVALUE",
		"albumartist": "ALBUMARTISTVALUE",
		"genre":       "GENREVALUE",
		"year":        "1994",
		"tracknumber": "03",
		"discnumber":  "01/02",
	}
	pattern := "^" + strings.Join([]string{
		`(?P<title>` + sentinels["title"] + `)`,
		`(?P<artist>` + sentinels["artist"] + `)`,
		`(?P<album>` + sentinels["album"] + `)`,
		`(?P<albumartist>` + sentinels["albumartist"] + `)`,
		`(?P<genre>` + sentinels["genre"] + `)`,
		`(?P<year>` + sentinels["year"] + `)`,
		`(?P<tracknumber>` + sentinels["tracknumber"] + `)`,
		`(?P<discnumber>` + sentinels["discnumber"] + `)`,
	}, "-") + "$"

	got := PortParseFilename(strings.Join([]string{
		sentinels["title"], sentinels["artist"], sentinels["album"],
		sentinels["albumartist"], sentinels["genre"], sentinels["year"],
		sentinels["tracknumber"], sentinels["discnumber"],
	}, "-")+".flac", ParseOptions{Pattern: pattern})

	for name, want := range sentinels {
		var have string
		switch name {
		case "title":
			have = got.Title
		case "artist":
			have = got.Artist
		case "album":
			have = got.Album
		case "albumartist":
			have = got.AlbumArtist
		case "genre":
			have = got.Genre
		case "year":
			have = got.Year
		case "tracknumber":
			have = got.TrackNumber
		case "discnumber":
			have = got.DiscNumber
		}
		if have != want {
			t.Errorf("%s = %q, want %q", name, have, want)
		}
	}
}

// TestCompilePattern_RejectsUnknownGroupNames is why the pattern is compiled
// up front and validated.
//
// A group named `album_artist` instead of `albumartist` is the kind of typo
// that, ignored, produces a preview where every row parses fine and writes
// nothing — the response says "ok" and the user's tags do not move, with
// nothing anywhere saying why. An error naming the allowed fields costs the
// user one read of the message.
func TestCompilePattern_RejectsUnknownGroupNames(t *testing.T) {
	_, err := CompilePattern(`^(?P<artist>.+?) - (?P<album_artist>.+?) - (?P<title>.+)$`)
	if err == nil {
		t.Fatal("a pattern with an unknown group name compiled; the typo would be silently ignored")
	}
	// The message has to be usable: it must name the offending group and
	// list what is allowed.
	if !strings.Contains(err.Error(), "album_artist") {
		t.Errorf("error does not name the offending group: %v", err)
	}
	for _, allowed := range PatternFieldNames {
		if !strings.Contains(err.Error(), allowed) {
			t.Errorf("error does not list the allowed field %q: %v", allowed, err)
		}
	}
}

// TestCompilePattern_RejectsInvalidRegex keeps the previous "a typo must not
// 500 the batch" intent while making it an error rather than a silent blank:
// the difference is that this is reported to the person who typed it, once,
// instead of appearing as 5000 unparsable rows.
func TestCompilePattern_RejectsInvalidRegex(t *testing.T) {
	if _, err := CompilePattern(`^(?P<artist>.+`); err == nil {
		t.Error("an unbalanced group compiled")
	}
}

// TestCompilePattern_EmptyIsNoPattern: the zero value must mean "use the
// default split", not "compile the empty regex and match everything".
func TestCompilePattern_EmptyIsNoPattern(t *testing.T) {
	for _, blank := range []string{"", "   "} {
		re, err := CompilePattern(blank)
		if err != nil {
			t.Errorf("CompilePattern(%q) errored: %v", blank, err)
		}
		if re != nil {
			t.Errorf("CompilePattern(%q) returned a regexp; a blank pattern must mean no pattern", blank)
		}
	}
}

// TestPortParseFilename_UnnamedGroupsKeepTheOldTwoFieldReading: a pattern
// with no names is what this field did before it took one, and someone may
// still be relying on it.
func TestPortParseFilename_UnnamedGroupsKeepTheOldTwoFieldReading(t *testing.T) {
	got := PortParseFilename("Ryuichi Sakamoto - Merry Christmas Mr. Lawrence.flac",
		ParseOptions{Pattern: `^(.+) - (.+)$`})
	if got.Artist != "Ryuichi Sakamoto" || got.Title != "Merry Christmas Mr. Lawrence" {
		t.Errorf("artist=%q title=%q, want the two-group reading", got.Artist, got.Title)
	}
	if got.Status != StatusOK {
		t.Errorf("Status = %q, want %q", got.Status, StatusOK)
	}
}

// TestPortParseFilename_DefaultModeIsUntouchedByThePatternFeature guards the
// regression that matters most to existing libraries: adding a pattern must
// not change how a plain name reads. Same input, no options, before and
// after this feature.
func TestPortParseFilename_DefaultModeIsUntouchedByThePatternFeature(t *testing.T) {
	cases := []struct {
		in     string
		artist string
		title  string
		status string
	}{
		{"Taylor Swift - Blank Space.flac", "Taylor Swift", "Blank Space", StatusOK},
		{"A - B - C.flac", "A", "B - C", StatusAmbiguous},
		{"single.flac", "", "", StatusUnparsable},
		{".hidden.flac", "", "", StatusUnparsable},
	}
	for _, tc := range cases {
		got := PortParseFilename(tc.in, ParseOptions{})
		if got.Artist != tc.artist || got.Title != tc.title || got.Status != tc.status {
			t.Errorf("%q → artist=%q title=%q status=%q; want %q / %q / %q",
				tc.in, got.Artist, got.Title, got.Status, tc.artist, tc.title, tc.status)
		}
	}
}

// TestPortParseFilename_PatternMatchWithNothingCapturedIsUnparsable: a
// pattern whose groups are all optional matches an empty string, and
// reporting that as "ok" would hand the apply step a row that writes
// nothing while the preview claims it parsed.
func TestPortParseFilename_PatternMatchWithNothingCapturedIsUnparsable(t *testing.T) {
	// The group requires a literal it cannot find, so it does not
	// participate and the match carries nothing. (`.*` would have been
	// greedy and captured the whole name, which is a different case.)
	got := PortParseFilename("anything.flac", ParseOptions{Pattern: `^(?:(?P<title>ONLY-IF-PRESENT))?$`})
	if got.Status != StatusUnparsable {
		t.Errorf("Status = %q, want %q", got.Status, StatusUnparsable)
	}
	if !got.Empty() {
		t.Errorf("fields are populated on an unparsable row: %+v", got)
	}
}

// TestPortParseFilename_OptionalGroupThatDidNotParticipateStaysEmpty
// distinguishes "the pattern did not capture an album" from "the album is
// now empty". Only the apply step can delete a tag, and it does so by
// being told to; a preview that reported an empty capture as a value would
// make the two indistinguishable.
func TestPortParseFilename_OptionalGroupThatDidNotParticipateStaysEmpty(t *testing.T) {
	pattern := `^(?P<artist>.+?) - (?P<title>.+?)(?: \[(?P<genre>.+)\])?$`

	withoutGenre := PortParseFilename("Aphex Twin - Xtal.flac", ParseOptions{Pattern: pattern})
	if withoutGenre.Genre != "" {
		t.Errorf("Genre = %q when the optional group did not participate, want empty", withoutGenre.Genre)
	}
	if withoutGenre.Status != StatusOK {
		t.Errorf("Status = %q, want %q: an unmatched optional group is not a parse failure", withoutGenre.Status, StatusOK)
	}

	withGenre := PortParseFilename("Aphex Twin - Xtal [IDM].flac", ParseOptions{Pattern: pattern})
	if withGenre.Genre != "IDM" {
		t.Errorf("Genre = %q, want IDM", withGenre.Genre)
	}
}
