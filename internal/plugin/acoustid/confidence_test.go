package acoustid

import (
	"context"
	"net/http"
	"testing"

	pb "go-music-tag/api/proto/tagplugin"
)

// The confidence the API returns is the only number in the whole scrape
// pipeline that is backed by the audio rather than by the title text. It
// used to be computed, used to order this plugin's own dedup, and then
// dropped on the floor — so the strongest evidence available went out as
// indistinguishable from a guess.

// The score reaches the response.
func TestFetchId3ByTitle_CarriesTheAPIConfidence(t *testing.T) {
	body := `{"results":[{"score":0.87,"recordings":[{"id":"r1","title":"A","artists":[{"name":"x"}],"releasegroups":[{"title":"al"}]}]}]}`
	srv := newServerWithFakeFpcalc(t, "DURATION=180\nFINGERPRINT=AQAAAA==\n")
	srv.client.Transport = &acoustidRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, body), nil
	}}

	resp, err := srv.FetchId3ByTitle(context.Background(), &pb.FetchId3Request{Title: "/x"})
	if err != nil {
		t.Fatalf("FetchId3ByTitle: %v", err)
	}
	if len(resp.Songs) != 1 {
		t.Fatalf("len=%d, want 1", len(resp.Songs))
	}
	if got := resp.Songs[0].GetScore(); got != 0.87 {
		t.Errorf("Score = %v, want the API's 0.87", got)
	}
}

// Several fingerprints can point at one recording; the best one wins, and
// its score is the one that must travel with the result. Reporting a
// different fingerprint's confidence than the one the song was chosen for
// would be a number that is real but attached to the wrong answer.
func TestFetchId3ByTitle_KeepsTheBestConfidencePerRecording(t *testing.T) {
	body := `{"results":[
		{"score":0.31,"recordings":[{"id":"r1","title":"A","artists":[{"name":"x"}],"releasegroups":[{"title":"al"}]}]},
		{"score":0.94,"recordings":[{"id":"r1","title":"A","artists":[{"name":"x"}],"releasegroups":[{"title":"al"}]}]},
		{"score":0.60,"recordings":[{"id":"r2","title":"B","artists":[{"name":"y"}],"releasegroups":[{"title":"al2"}]}]}
	]}`
	srv := newServerWithFakeFpcalc(t, "DURATION=180\nFINGERPRINT=AQAAAA==\n")
	srv.client.Transport = &acoustidRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, body), nil
	}}

	resp, _ := srv.FetchId3ByTitle(context.Background(), &pb.FetchId3Request{Title: "/x"})
	if len(resp.Songs) != 2 {
		t.Fatalf("len=%d, want 2 (one row per recording)", len(resp.Songs))
	}
	byID := map[string]float64{}
	for _, s := range resp.Songs {
		byID[s.GetId()] = s.GetScore()
	}
	if byID["r1"] != 0.94 {
		t.Errorf("r1 confidence = %v, want 0.94 (the best of 0.31 / 0.94)", byID["r1"])
	}
	if byID["r2"] != 0.60 {
		t.Errorf("r2 confidence = %v, want 0.60", byID["r2"])
	}
}

// A low-confidence match is still reported, and still carries its low
// number. Rounding it up or dropping it would defeat the point: the user
// needs to see that this identification is weak.
func TestFetchId3ByTitle_LowConfidenceIsNotInflated(t *testing.T) {
	body := `{"results":[{"score":0.08,"recordings":[{"id":"r1","title":"A","artists":[{"name":"x"}],"releasegroups":[{"title":"al"}]}]}]}`
	srv := newServerWithFakeFpcalc(t, "DURATION=180\nFINGERPRINT=AQAAAA==\n")
	srv.client.Transport = &acoustidRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, body), nil
	}}

	resp, _ := srv.FetchId3ByTitle(context.Background(), &pb.FetchId3Request{Title: "/x"})
	if len(resp.Songs) != 1 {
		t.Fatalf("len=%d, want 1", len(resp.Songs))
	}
	if got := resp.Songs[0].GetScore(); got != 0.08 {
		t.Errorf("Score = %v, want 0.08 unchanged", got)
	}
}
