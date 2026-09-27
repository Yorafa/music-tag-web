package tag

import (
	"testing"
)

// The predicates behind "a .lrc whose track is gone is residue". They are
// pinned separately from the pruner because the direction of their errors
// differs: HasTrack saying yes when the answer is no leaves a file lying
// around, and saying no when the answer is yes hands somebody's lyrics to the
// delete pass. The second one is the one worth a test table.

// The naming convention has to agree with the writer's, or the pruner looks
// for a file the app never wrote.
func TestLyricsSidecarName_MatchesWhatTheWriterWrites(t *testing.T) {
	cases := map[string]string{
		"/music/Artist/Album/song.ogg":      "/music/Artist/Album/song.lrc",
		"/music/Artist/Album/song.flac":     "/music/Artist/Album/song.lrc",
		"/music/Artist/Album/01 - Song.mp3": "/music/Artist/Album/01 - Song.lrc",
		"/music/Artist/Album/song.lrc":      "/music/Artist/Album/song.lrc",
	}
	for audio, want := range cases {
		if got := LyricsSidecarName(audio); got != want {
			t.Errorf("LyricsSidecarName(%q) = %q, want %q", audio, got, want)
		}
	}
}

func TestLyricsSidecarFor(t *testing.T) {
	yes := map[string]string{
		"song.lrc":   "song",
		"song.LRC":   "song",
		"01 - A.lrc": "01 - A",
	}
	for name, wantBase := range yes {
		base, ok := LyricsSidecarFor(name)
		if !ok {
			t.Errorf("LyricsSidecarFor(%q) said no", name)
			continue
		}
		if base != wantBase {
			t.Errorf("LyricsSidecarFor(%q) base = %q, want %q", name, base, wantBase)
		}
	}
	// Nothing here describes a track, so nothing here may be treated as an
	// orphan and handed to the pruner.
	for _, name := range []string{
		"album.nfo", "cover.jpg", ".lrc", ".hidden.lrc",
		"song.mp3", "notes.txt", "lrc", "song.lrc.bak", "sub/song.lrc",
	} {
		if base, ok := LyricsSidecarFor(name); ok {
			t.Errorf("LyricsSidecarFor(%q) = %q, want no match", name, base)
		}
	}
}
