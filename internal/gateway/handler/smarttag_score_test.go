package handler

import (
	"context"
	"testing"

	"go-music-tag/internal/plugin"
)

// A source that actually listened to the audio is the only thing in the
// system that can produce a real confidence, and this file is what keeps
// the gateway from throwing it away.
//
// The bug it pins: SmartTagSearch assigned scoreMatch(title, artist, album,
// sg) — a 0..6 title-similarity sum — to plugin.Song.Score, and the
// frontend rendered `score * 20` as a percentage. A scrape of an untagged
// file has no artist or album to compare, so that sum was 2 for every
// candidate and every row in the picker read a flat "40%", live versions
// and remixes included. Any source that had put a measured confidence in
// Score had it replaced by the guess.

// confidenceSource behaves like AcoustID: it hands back a measured 0..1
// confidence in the audio. It is deliberately NOT named "acoustid",
// because smartTagSources() excludes that name and the property under test
// is name-independent — whatever a source is called, the gateway must not
// rewrite its number.
type confidenceSource struct {
	songs []plugin.Song
}

func (c confidenceSource) Name() string           { return "confidenceprobe" }
func (c confidenceSource) DisplayName() string    { return "confidence probe" }
func (c confidenceSource) SupportsSearch() bool   { return true }
func (c confidenceSource) SupportsLyric() bool    { return false }
func (c confidenceSource) SupportsId3() bool      { return true }
func (c confidenceSource) SupportsAudioURL() bool { return false }
func (c confidenceSource) FetchLyric(context.Context, string) (string, error) {
	return "", nil
}

func (c confidenceSource) GetAudioURL(context.Context, string) (string, error) {
	return "", nil
}

func (c confidenceSource) Search(context.Context, string, int, int) (*plugin.SearchResult, error) {
	return &plugin.SearchResult{}, nil
}

func (c confidenceSource) FetchID3ByTitle(context.Context, string) ([]plugin.Song, error) {
	return c.songs, nil
}

// installConfidenceSource registers the probe for one test and removes it
// on cleanup. It goes through InstallMockTagSource rather than an init()
// because other tests in this package call plugin.ResetForTesting(), which
// wipes the whole registry — an init()-registered source disappears
// mid-suite and the test then passes or fails for reasons that have
// nothing to do with the property it is checking.
func installConfidenceSource(t *testing.T, songs ...plugin.Song) {
	t.Helper()
	plugin.InstallMockTagSource("confidenceprobe", confidenceSource{songs: songs})
	t.Cleanup(func() { plugin.UninstallMockTagSource("confidenceprobe") })
}

// The measured values must survive the fan-out verbatim. The old code
// replaced them with a 0..6 sum, and the UI then read that as a
// percentage — so a genuine 0.87 and a guessed 4 became the same display.
func TestSmartTagSearch_CarriesTheRealConfidenceThrough(t *testing.T) {
	installConfidenceSource(t,
		plugin.Song{Name: "Jocelyn Flores (Live)", Artist: "XXXTENTACION", Score: 0.87},
		plugin.Song{Name: "Jocelyn Flores (Remix 3 D)", Artist: "XXXTENTACION", Score: 0.31},
	)

	songs, err := SmartTagSearch(context.Background(), "Jocelyn Flores", "")
	if err != nil {
		t.Fatalf("SmartTagSearch: %v", err)
	}
	if len(songs) != 2 {
		t.Fatalf("got %d candidates, want 2: %+v", len(songs), songs)
	}

	want := map[string]float64{
		"Jocelyn Flores (Live)":      0.87,
		"Jocelyn Flores (Remix 3 D)": 0.31,
	}
	for _, s := range songs {
		w, ok := want[s.Name]
		if !ok {
			t.Fatalf("unexpected candidate %q", s.Name)
		}
		if s.Score != w {
			t.Errorf("%q: Score = %v, want the plugin's measured %v", s.Name, s.Score, w)
		}
	}
}

// A text-only source has no confidence, and the field must stay empty so
// the UI says "not verified against the audio" rather than inventing a
// number. The old frontend fallback was a hardcoded 85%.
func TestSmartTagSearch_LeavesAnUnverifiedCandidateWithoutAScore(t *testing.T) {
	installConfidenceSource(t,
		plugin.Song{Name: "Jocelyn Flores", Artist: "XXXTENTACION"},
	)

	songs, err := SmartTagSearch(context.Background(), "Jocelyn Flores", "")
	if err != nil {
		t.Fatalf("SmartTagSearch: %v", err)
	}
	if len(songs) != 1 {
		t.Fatalf("got %d candidates, want 1", len(songs))
	}
	if songs[0].Score != 0 {
		t.Errorf("Score = %v, want 0 so the field stays absent on the wire", songs[0].Score)
	}
}

// The title comparison is published alongside, as a fact. A text match is
// a different and much weaker claim than an acoustic one, so it gets its
// own field rather than being folded into a single number.
func TestSmartTagSearch_ReportsTheTitleComparisonSeparately(t *testing.T) {
	installConfidenceSource(t,
		plugin.Song{Name: "Jocelyn Flores (Live)", Artist: "XXXTENTACION", Score: 0.87},
		plugin.Song{Name: "Jocelyn Flores", Artist: "XXXTENTACION", Score: 0.91},
	)

	songs, err := SmartTagSearch(context.Background(), "Jocelyn Flores", "")
	if err != nil {
		t.Fatalf("SmartTagSearch: %v", err)
	}
	for _, s := range songs {
		if s.TitleMatch != "exact" {
			t.Errorf("%q: TitleMatch = %q, want \"exact\" (parens are stripped before comparing)",
				s.Name, s.TitleMatch)
		}
	}
}
