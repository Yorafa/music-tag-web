package netease

import (
	"testing"

	"go-music-tag/internal/plugin"
)

// TestNormalize_ConvertsMillisToSeconds pins the wiring, not just the
// helper.
//
// plugin.DurationFromMillis is unit-tested in the parent package, and
// that test still passes if this plugin stops calling it — the conversion
// is a no-op line between two tested functions and the bug only shows up
// as a 33-hour track in the UI. So assert on what normalize() produces.
//
// 119133 is the real upstream value for a 1:59 track, and NetEase is the
// one source in the fleet that reports milliseconds; every other source
// would be off by 1000x if this divide were ever dropped. The field is
// spelled `dt` on the cloudsearch endpoint the plugin actually uses.
func TestNormalize_ConvertsMillisToSeconds(t *testing.T) {
	s := &Server{}
	out := s.normalize([]map[string]interface{}{
		{
			"id":   float64(1),
			"name": "Jocelyn Flores",
			"dt":   float64(119133),
		},
	})
	if len(out) != 1 {
		t.Fatalf("normalize returned %d songs, want 1", len(out))
	}
	got := plugin.DurationFromSeconds(out[0]["duration"])
	if got < 118 || got > 120 {
		t.Errorf("duration = %v seconds, want ~119 (1:59) — the millisecond divide is missing", got)
	}
}

// The legacy /api/search/get fallback spells the same field `duration`,
// and it is the shape a unit-test fixture is most likely to be written
// from. Both spellings have to work, because the primary linux-forward
// path uses `dt` and a reader that only knows `duration` reports no
// length on every real search while its own tests stay green.
func TestNormalize_AcceptsBothDurationSpellings(t *testing.T) {
	s := &Server{}
	cases := map[string]interface{}{
		"dt":       float64(119133), // cloudsearch (primary path)
		"duration": float64(119133), // legacy /api/search/get
	}
	for key, val := range cases {
		out := s.normalize([]map[string]interface{}{
			{"id": float64(1), "name": "x", key: val},
		})
		got := plugin.DurationFromSeconds(out[0]["duration"])
		if got < 118 || got > 120 {
			t.Errorf("key %q: duration = %v, want ~119", key, got)
		}
	}
}

// A song with no length must stay 0 rather than becoming a fabricated
// value, so the row renders without a time instead of with a wrong one.
func TestNormalize_MissingDurationStaysUnknown(t *testing.T) {
	s := &Server{}
	out := s.normalize([]map[string]interface{}{{"id": float64(2), "name": "No Length"}})
	got := plugin.DurationFromSeconds(out[0]["duration"])
	if got != 0 {
		t.Errorf("duration = %v, want 0 (unknown)", got)
	}
}
