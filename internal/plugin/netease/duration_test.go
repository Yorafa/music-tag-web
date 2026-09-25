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
// would be off by 1000x if this divide were ever dropped.
func TestNormalize_ConvertsMillisToSeconds(t *testing.T) {
	s := &Server{}
	out := s.normalize([]map[string]interface{}{
		{
			"id":       float64(1),
			"name":     "Jocelyn Flores",
			"duration": float64(119133),
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
