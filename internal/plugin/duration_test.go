package plugin

import (
	"encoding/json"
	"math"
	"testing"
)

// closeEnough compares durations at a tolerance. Dividing by 1000 is not
// exact in binary floating point (119.133/1000 = 0.11913299999999999), so
// an equality assertion here would be testing the FPU rather than the
// conversion. 1e-9 seconds is a nanosecond — far below anything a track
// length can meaningfully differ by.
func closeEnough(got, want float64) bool {
	return math.Abs(got-want) < 1e-9
}

// TestMillisToSeconds pins the one conversion that is genuinely easy to
// get wrong. NetEase reports track length in milliseconds while every
// other source reports seconds; feeding a millisecond value through
// unconverted renders a 2-minute song as 33 hours, which does not look
// like a units bug — it looks like a broken library.
func TestMillisToSeconds(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{
		{119133, 119.133}, // the real NetEase value for a 1:59 track
		{0, 0},
		{-1, 0}, // upstreams use negatives for "no length"
		{500, 0.5},
	}
	for _, c := range cases {
		if got := MillisToSeconds(c.in); !closeEnough(got, c.want) {
			t.Errorf("MillisToSeconds(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestDurationReaders covers the encodings a duration arrives in. The
// upstreams are inconsistent about sending 119 versus "119", and a strict
// float64 decode would drop the time on the rows that use the other one —
// or worse, fail the entire search.
func TestDurationReaders(t *testing.T) {
	cases := []struct {
		name string
		in   interface{}
		sec  float64 // expected from DurationFromSeconds
		msec float64 // expected from DurationFromMillis
	}{
		{"json number", float64(119), 119, 0.119},
		{"string number", "119", 119, 0.119},
		{"string with space", " 119 ", 119, 0.119},
		{"float from json", 119.133, 119.133, 0.119133},
		{"int", 119, 119, 0.119},
		{"json.Number", json.Number("119"), 119, 0.119},
		{"zero", float64(0), 0, 0},
		{"nil", nil, 0, 0},
		{"unparseable string", "n/a", 0, 0},
		{"bool", true, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DurationFromSeconds(c.in); !closeEnough(got, c.sec) {
				t.Errorf("DurationFromSeconds(%#v) = %v, want %v", c.in, got, c.sec)
			}
			if got := DurationFromMillis(c.in); !closeEnough(got, c.msec) {
				t.Errorf("DurationFromMillis(%#v) = %v, want %v", c.in, got, c.msec)
			}
		})
	}
}

// TestDurationReadersSurviveRealUpstreamShapes feeds the actual JSON
// encodings the live APIs emit, decoded the way a plugin decodes them, to
// confirm the units line up across sources for one real track.
func TestDurationReadersSurviveRealUpstreamShapes(t *testing.T) {
	// Jocelyn Flores is 1:59. NetEase sends milliseconds, everyone else
	// seconds — the two must agree after conversion, which is the whole
	// reason normalization happens at the plugin edge.
	var netease struct {
		Duration float64 `json:"duration"`
	}
	if err := json.Unmarshal([]byte(`{"duration":119133}`), &netease); err != nil {
		t.Fatal(err)
	}
	if got := DurationFromMillis(netease.Duration); got < 118 || got > 120 {
		t.Errorf("netease duration = %v seconds, want ~119 (1:59)", got)
	}

	var kugou struct {
		Duration float64 `json:"duration"`
	}
	if err := json.Unmarshal([]byte(`{"duration":119}`), &kugou); err != nil {
		t.Fatal(err)
	}
	if got := DurationFromSeconds(kugou.Duration); got < 118 || got > 120 {
		t.Errorf("kugou duration = %v seconds, want ~119 (1:59)", got)
	}

	// The two must not be 1000x apart — that is the whole bug class.
	net, _ := DurationFromMillis(netease.Duration), 0.0
	kg := DurationFromSeconds(kugou.Duration)
	if ratio := net / kg; ratio < 0.9 || ratio > 1.1 {
		t.Errorf("netease/kugou duration ratio = %v, want ~1.0 (a ms/s mix-up would be ~1000)", ratio)
	}
}

// TestDurationZeroMeansUnknown pins the "no length" contract. MusicBrainz
// search returns no length at all, and that has to stay 0 rather than
// becoming a fabricated value or an error the fan-out would surface.
func TestDurationZeroMeansUnknown(t *testing.T) {
	if got := DurationFromSeconds(nil); got != 0 {
		t.Errorf("missing duration = %v, want 0 (unknown)", got)
	}
	// And the JSON tag must omit a zero so the field simply does not
	// appear, rather than shipping `"duration":0` for every MusicBrainz row.
	b, err := json.Marshal(Song{ID: "x", Name: "y"})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"id":"x","name":"y","artist":"","artist_id":"","album":"","album_id":"","album_img":"","year":"","source":""}` {
		t.Errorf("Song JSON = %s", b)
	}
}
