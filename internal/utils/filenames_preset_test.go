package utils

import "testing"

// The preset chips in the dialog are a claim about what the parser can
// do: "艺术家 - 专辑 - 音轨 - 标题" is offered because a pattern of that
// shape is believed to work. These tests run the Go parser — the side
// that actually executes the pattern — over every advertised shape, so
// the claim is checked where it is load-bearing rather than only in the
// frontend's own re-implementation.
//
// A preset whose example fails here means the chip is lying to the user,
// and the fix is the pattern generator, not this test.

// presetShape mirrors one entry of PATTERN_PRESETS on the TS side.
type presetShape struct {
	name    string
	pattern string
	example string
	want    map[string]string
}

func presetShapes() []presetShape {
	return []presetShape{
		{
			name:    "艺术家 - 标题",
			pattern: `^(?P<artist>.+?) - (?P<title>.+)$`,
			example: "周杰倫 - 晴天.flac",
			want:    map[string]string{"artist": "周杰倫", "title": "晴天"},
		},
		{
			name:    "音轨 - 艺术家 - 标题",
			pattern: `^(?P<tracknumber>\d+) - (?P<artist>.+?) - (?P<title>.+)$`,
			example: "01 - 周杰倫 - 晴天.flac",
			want: map[string]string{
				"tracknumber": "01", "artist": "周杰倫", "title": "晴天",
			},
		},
		{
			name:    "艺术家 - 专辑 - 标题",
			pattern: `^(?P<artist>.+?) - (?P<album>.+?) - (?P<title>.+)$`,
			example: "周杰伦 - 叶惠美 - 晴天.flac",
			want:    map[string]string{"artist": "周杰伦", "album": "叶惠美", "title": "晴天"},
		},
		{
			name:    "艺术家 - 专辑 - 音轨 - 标题",
			pattern: `^(?P<artist>.+?) - (?P<album>.+?) - (?P<tracknumber>\d+) - (?P<title>.+)$`,
			example: "Radiohead - OK Computer - 01 - Airbag.flac",
			want: map[string]string{
				"artist": "Radiohead", "album": "OK Computer",
				"tracknumber": "01", "title": "Airbag",
			},
		},
		{
			name:    "艺术家 - 年份 - 标题",
			pattern: `^(?P<artist>.+?) - (?P<year>\d{4}) - (?P<title>.+)$`,
			example: "窦唯 - 1994 - 黑梦.flac",
			want:    map[string]string{"artist": "窦唯", "year": "1994", "title": "黑梦"},
		},
		{
			name:    "艺术家 - 碟片 - 音轨 - 标题",
			pattern: `^(?P<artist>.+?) - (?P<discnumber>\d+) - (?P<tracknumber>\d+) - (?P<title>.+)$`,
			example: "Beatles - 1 - 03 - Something.flac",
			want: map[string]string{
				"artist": "Beatles", "discnumber": "1",
				"tracknumber": "03", "title": "Something",
			},
		},
	}
}

func TestPresetShapes_RunOnTheGoParser(t *testing.T) {
	for _, p := range presetShapes() {
		t.Run(p.name, func(t *testing.T) {
			re, err := CompilePattern(p.pattern)
			if err != nil {
				t.Fatalf("preset pattern does not compile: %v", err)
			}
			got := PortParseFilenameCompiled(p.example, ParseOptions{}, re)
			if got.Status != StatusOK {
				t.Fatalf("status = %q, want %q (parsed %+v)", got.Status, StatusOK, got)
			}
			// Compare through the JSON shape so the test reads the same
			// way the map is written, and a renamed field shows up as a
			// missing key rather than a zero value.
			if got.Title != p.want["title"] {
				t.Errorf("title = %q, want %q", got.Title, p.want["title"])
			}
			if got.Artist != p.want["artist"] {
				t.Errorf("artist = %q, want %q", got.Artist, p.want["artist"])
			}
			if got.Album != p.want["album"] {
				t.Errorf("album = %q, want %q", got.Album, p.want["album"])
			}
			if got.Year != p.want["year"] {
				t.Errorf("year = %q, want %q", got.Year, p.want["year"])
			}
			if got.TrackNumber != p.want["tracknumber"] {
				t.Errorf("tracknumber = %q, want %q", got.TrackNumber, p.want["tracknumber"])
			}
			if got.DiscNumber != p.want["discnumber"] {
				t.Errorf("discnumber = %q, want %q", got.DiscNumber, p.want["discnumber"])
			}
		})
	}
}

// TestPresetShapes_RejectWrongArity is the other half of the promise: a
// three-field pattern must NOT quietly swallow a two-field name, because
// the whole reason the chips exist is that the user can tell which shape
// their library uses and pick it deliberately.
func TestPresetShapes_RejectWrongArity(t *testing.T) {
	re, err := CompilePattern(`^(?P<artist>.+?) - (?P<album>.+?) - (?P<title>.+)$`)
	if err != nil {
		t.Fatal(err)
	}
	// Two segments, three fields expected.
	got := PortParseFilenameCompiled("周杰倫 - 晴天.mp3", ParseOptions{}, re)
	if got.Status != StatusUnparsable {
		t.Errorf("a 2-segment name under a 3-field pattern = %q, want unparsable", got.Status)
	}
	if !got.Empty() {
		t.Errorf("an unparsable row must carry no fields, got %+v", got)
	}
}

// TestStripExt_ServerAndClientAgree pins the rule the frontend's
// tryPattern re-implements. It is a real trap: cutting at the FIRST dot
// turns "Mr. A - B - C.mp3" into the stem "Mr" and the whole parse
// collapses to unparsable.
func TestStripExt_ServerAndClientAgree(t *testing.T) {
	cases := map[string]string{
		"周杰倫 - 晴天.mp3":      "周杰倫 - 晴天",
		"Mr. A - B - C.mp3": "Mr. A - B - C",
		"Mr. A - B":         "Mr",
		"A - B":             "",
		".hidden":           "",
	}
	for in, want := range cases {
		if got := stripExt(in); got != want {
			t.Errorf("stripExt(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCompilePattern_RejectsRE2Unsupported is the server half of the
// client-side check. The dialog refuses lookahead and backreferences
// before spending a request; this asserts the refusal is correct rather
// than superstition, so the two stay in step.
func TestCompilePattern_RejectsRE2Unsupported(t *testing.T) {
	for _, p := range []string{
		`^(?P<title>.+?)(?= - )`,
		`^(?!x)(?P<title>.+)$`,
		`(?<=a)(?P<title>.+)$`,
		`^(?P<title>.+)\1$`,
	} {
		if _, err := CompilePattern(p); err == nil {
			t.Errorf("CompilePattern(%q) accepted a construct RE2 does not support", p)
		}
	}
	// And the constructs it DOES support must keep working, or the
	// client check would be rejecting legitimate patterns.
	for _, p := range []string{
		`^(?P<title>.+?)\s-\s(?P<artist>.+)$`,
		`^(?P<title>[\p{L}]+)$`,
		`^(?P<artist>.+?) - (?P<year>\d{4})$`,
	} {
		if _, err := CompilePattern(p); err != nil {
			t.Errorf("CompilePattern(%q) rejected a valid RE2 pattern: %v", p, err)
		}
	}
}
