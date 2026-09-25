package plugin

import (
	"testing"

	pb "go-music-tag/api/proto/tagplugin"
)

// TestPbToSong_CarriesDuration pins the gRPC boundary for the track
// length.
//
// Every plugin is a separate process that answers over gRPC, so a field
// that exists on both sides but is missing from the conversion is
// silently dropped in transit — the plugin populates it, the gateway
// receives zero, and the UI shows no time. Nothing in either package's
// tests notices, because each one only ever sees its own half.
func TestPbToSong_CarriesDuration(t *testing.T) {
	got := pbToSong(&pb.Song{
		Id:       "1",
		Name:     "Jocelyn Flores",
		Duration: 119.133,
	})
	if got.Duration < 118 || got.Duration > 120 {
		t.Errorf("Duration = %v, want ~119 — the adapter drops the field", got.Duration)
	}
}

// A source with no length crosses the boundary as 0 and must stay 0
// rather than picking up a default.
func TestPbToSong_ZeroDurationStaysZero(t *testing.T) {
	if got := pbToSong(&pb.Song{Id: "1", Name: "MusicBrainz row"}).Duration; got != 0 {
		t.Errorf("Duration = %v, want 0", got)
	}
}
