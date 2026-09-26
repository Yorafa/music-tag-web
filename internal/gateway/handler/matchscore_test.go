package handler

import (
	"testing"

	"go-music-tag/internal/plugin"
)

// The number the UI showed was `score * 20`, and `score` was a title
// similarity sum. These pin the property that makes that meaningless: when
// a scrape has no artist or album to compare — the normal case, because a
// filename is all the user has — the title heuristic produces the same
// value for candidates that are plainly not the same recording.

// A source that verified the audio keeps its own confidence.
//
// This is the one that matters. AcoustID returns a 0..1 score for a
// fingerprint match; annotateCandidate used to assign scoreMatch over it,
// so the single number in the system backed by the actual audio was
// replaced by a guess about the title text. The values below are the two
// scales the field used to carry, which is exactly why a reader could not
// tell a measured 0.87 from a heuristic 4.
func TestAnnotateCandidate_DoesNotOverwriteAFingerprintConfidence(t *testing.T) {
	for _, got := range []plugin.Song{
		annotateCandidate("Some Song", plugin.Song{Name: "Some Song", Score: 0.93}),
		annotateCandidate("Some Song", plugin.Song{Name: "Some Song (Live)", Score: 0.12}),
		annotateCandidate("Some Song", plugin.Song{Name: "Unrelated", Score: 0.5}),
	} {
		switch {
		case got.Score == 0.93, got.Score == 0.12, got.Score == 0.5:
		default:
			t.Errorf("Score = %v, want the plugin's own confidence untouched", got.Score)
		}
	}
}

// A text-only source has no confidence, and inventing one is what made
// every row read the same number. Omitted must stay omitted.
func TestAnnotateCandidate_LeavesAnUnscoredCandidateUnscored(t *testing.T) {
	got := annotateCandidate("Some Song", plugin.Song{Name: "Some Song"})
	if got.Score != 0 {
		t.Errorf("Score = %v, want 0 so the field stays absent on the wire", got.Score)
	}
}

// scoreMatch is still computed, and it is still only a ranking signal.
func TestScoreMatch_RemainsAnOrderingSignal(t *testing.T) {
	sg := plugin.Song{Name: "Some Song", Artist: "Someone"}
	if rank := scoreMatch("Some Song", "", "", sg); rank <= 0 {
		t.Errorf("rank = %v; the ordering signal should still be computed", rank)
	}
}

// The title comparison is published as a fact, not a percentage.
func TestTitleMatchLabels(t *testing.T) {
	for _, tc := range []struct {
		query, name string
		want        string
	}{
		{"Jocelyn Flores", "Jocelyn Flores", "exact"},
		{"Jocelyn Flores", "Jocelyn Flores (Explicit)", "exact"}, // parens stripped
		{"Jocelyn Flores", "jocelyn  flores", "exact"},
		{"Jocelyn Flores", "Jocelyn Flores (Remix)", "exact"},
		// A containment or shared-token match is a weaker claim and must be
		// labelled as such, not rounded up to "exact". Note that a
		// parenthetical is NOT partial — cleanForMatch strips it whole, so
		// "(Live at the Fillmore)" still normalises to the studio title.
		{"Jocelyn Flores", "Jocelyn Flores (Live at the Fillmore)", "exact"},
		{"Jocelyn Flores", "Jocelyn Flores Revisited", "partial"},
		{"Jocelyn Flores", "Jocelyn", "partial"},
		{"Jocelyn Flores", "Everybody Dies In Their Nightmares", ""},
	} {
		got := annotateCandidate(tc.query, plugin.Song{Name: tc.name}).TitleMatch
		if got != tc.want {
			t.Errorf("titleMatch(%q, %q) = %q, want %q", tc.query, tc.name, got, tc.want)
		}
	}
}

// The behaviour that made the percentage useless, stated directly: with no
// artist or album to compare, every candidate scores the same. If this ever
// stops being true, the ranking has started discriminating — which is a
// change worth noticing rather than one to discover in the UI.
func TestTitleHeuristic_IsFlatWhenOnlyTheTitleIsKnown(t *testing.T) {
	variants := []string{
		"Jocelyn Flores",
		"Jocelyn Flores (Explicit)",
		"Jocelyn Flores (Remix 3 D)",
		"Jocelyn Flores (A reversion)",
		"Jocelyn Flores (feat. Mic4shy)(Explicit)",
	}
	seen := map[float64]bool{}
	for _, v := range variants {
		sg := plugin.Song{Name: v, Artist: "Various"}
		seen[scoreMatch("Jocelyn Flores", "", "", sg)] = true
	}
	if len(seen) != 1 {
		t.Errorf("title heuristic produced %d distinct values across variants: %v",
			len(seen), seen)
	}
}

// The reason it is flat: cleaning strips parenthetical qualifiers, so a
// remix, a live cut and an explicit version all normalise to the studio
// title. Whatever label the UI shows, these are not distinguishable by
// title text, and pretending otherwise is what the old percentage did.
func TestCleanForMatch_CollapsesVersionQualifiers(t *testing.T) {
	for _, in := range []string{
		"Jocelyn Flores (Explicit)",
		"Jocelyn Flores (Remix 3 D)",
		"Jocelyn Flores (feat. Mic4shy)(Explicit)",
		"Jocelyn Flores (A reversion)",
	} {
		if got := cleanForMatch(in); got != "jocelyn flores" {
			t.Errorf("cleanForMatch(%q) = %q, want %q", in, got, "jocelyn flores")
		}
	}
}

// sortSongsByRank must still order by the heuristic, since the ranking is
// now the only thing distinguishing candidates. A stable, descending sort
// on rank is all it promises.
func TestSortSongsByRank_OrdersDescending(t *testing.T) {
	songs := []plugin.Song{
		{Source: "a", Name: "low"},
		{Source: "b", Name: "high"},
		{Source: "c", Name: "mid"},
	}
	rank := map[string]float64{
		sgKey(songs[0]): 1,
		sgKey(songs[1]): 3,
		sgKey(songs[2]): 2,
	}
	sortSongsByRank(songs, rank)
	want := []string{"high", "mid", "low"}
	for i, w := range want {
		if songs[i].Name != w {
			t.Errorf("position %d = %q, want %q (order: %v)", i, songs[i].Name, w,
				[]string{songs[0].Name, songs[1].Name, songs[2].Name})
		}
	}
}

// Two sources can return the same song id, or none at all. Keying the rank
// map on the id alone would merge them and sort part of the list by
// whichever candidate happened to be seen first.
func TestSgKey_DistinguishesSourcesAndEmptyIDs(t *testing.T) {
	a := plugin.Song{Source: "netease", ID: "1", Name: "x"}
	b := plugin.Song{Source: "kugou", ID: "1", Name: "x"}
	if sgKey(a) == sgKey(b) {
		t.Error("two sources sharing an id collapsed into one rank bucket")
	}
	noID1 := plugin.Song{Source: "netease", Name: "Song", Artist: "A"}
	noID2 := plugin.Song{Source: "netease", Name: "Song", Artist: "B"}
	if sgKey(noID1) == sgKey(noID2) {
		t.Error("two id-less candidates with different artists collapsed")
	}
	// Case must not create spurious buckets: these are the same candidate.
	upper := plugin.Song{Source: "netease", Name: "SONG", Artist: "A"}
	if sgKey(noID1) != sgKey(upper) {
		t.Error("key is case-sensitive, so the same candidate gets two ranks")
	}
}
