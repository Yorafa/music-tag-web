package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/plugin"
)

// The client paginates its candidate list, which is only worth doing if the
// gateway is willing to return more than one page worth. It used to cut the
// fan-out at a hardcoded 15, so "查看更多" could only ever re-page the same
// fifteen rows — the pager would be a way of hiding results, not reaching
// them.

// Every existing caller omits `limit`, so an omitted field has to keep
// meaning 15 rather than "none" or "zero candidates".
func TestCandidateLimit_TreatsOmittedAndAbsurdValuesAsTheDefault(t *testing.T) {
	cases := []struct {
		name      string
		requested int
		want      int
	}{
		{"omitted", 0, DefaultSmartCandidateLimit},
		{"negative", -7, DefaultSmartCandidateLimit},
		{"explicitly small", 5, 5},
		{"at the ceiling", MaxSmartCandidateLimit, MaxSmartCandidateLimit},
		// The fan-out cost is per source, not per row, so the cap bounds a
		// client that asks for the whole result set of a broad query.
		{"beyond the ceiling", 100000, MaxSmartCandidateLimit},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := candidateLimit(c.requested); got != c.want {
				t.Errorf("candidateLimit(%d) = %d, want %d", c.requested, got, c.want)
			}
		})
	}
}

func manyMatchingSongs(n int) []plugin.Song {
	songs := make([]plugin.Song, 0, n)
	for i := 0; i < n; i++ {
		// Distinct names, so the (name, artist) dedupe keeps all of them,
		// and every name still contains the query so none is dropped as an
		// unrelated title.
		songs = append(songs, plugin.Song{
			Name:   fmt.Sprintf("Jocelyn Flores Vol %d", i),
			Artist: "XXXTENTACION",
		})
	}
	return songs
}

func TestSmartTagSearchLimit_AsksForMoreThanTheDefaultGetsMore(t *testing.T) {
	const total = 25
	installConfidenceSource(t, manyMatchingSongs(total)...)

	dflt, err := SmartTagSearch(context.Background(), "Jocelyn Flores", "")
	if err != nil {
		t.Fatalf("SmartTagSearch: %v", err)
	}
	if len(dflt) != DefaultSmartCandidateLimit {
		t.Errorf("default search returned %d, want the historical %d", len(dflt), DefaultSmartCandidateLimit)
	}

	wide, err := SmartTagSearchLimit(context.Background(), "Jocelyn Flores", "", total)
	if err != nil {
		t.Fatalf("SmartTagSearchLimit: %v", err)
	}
	if len(wide) != total {
		t.Errorf("limit=%d returned %d candidates, want %d", total, len(wide), total)
	}
}

// The limit has to arrive from the request, not just exist as a parameter:
// the field is the only way the client can ask for a deeper list.
func TestFetchID3ByTitle_HonoursTheRequestedLimit(t *testing.T) {
	// More than the ceiling, so an over-large request is clamped rather
	// than satisfied by simply running out of candidates.
	installConfidenceSource(t, manyMatchingSongs(MaxSmartCandidateLimit+10)...)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/fetch_id3_by_title/", FetchID3ByTitle)

	post := func(body string) []plugin.Song {
		t.Helper()
		req := httptest.NewRequest("POST", "/api/fetch_id3_by_title/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		var env struct {
			Data []plugin.Song `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("response is not JSON: %v (%q)", err, w.Body.String())
		}
		return env.Data
	}

	if got := len(post(`{"title":"Jocelyn Flores","resource":"smart_tag"}`)); got != DefaultSmartCandidateLimit {
		t.Errorf("without a limit: %d candidates, want %d", got, DefaultSmartCandidateLimit)
	}
	if got := len(post(`{"title":"Jocelyn Flores","resource":"smart_tag","limit":25}`)); got != 25 {
		t.Errorf("with limit=25: %d candidates, want 25", got)
	}
	if got := len(post(`{"title":"Jocelyn Flores","resource":"smart_tag","limit":9999}`)); got != MaxSmartCandidateLimit {
		t.Errorf("with limit=9999: %d candidates, want the ceiling %d", got, MaxSmartCandidateLimit)
	}
}
