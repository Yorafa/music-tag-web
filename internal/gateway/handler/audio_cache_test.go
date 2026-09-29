package handler_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/config"
	"go-music-tag/internal/gateway/handler"
)

type cacheEnvelope struct {
	Result bool            `json:"result"`
	Data   json.RawMessage `json:"data"`
}

func cacheGet(t *testing.T) cacheEnvelope {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/audio_cache/", handler.GetAudioCache)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/audio_cache/", nil))
	return decodeEnvelope(t, w.Body.String())
}

func cachePost(t *testing.T, body string) cacheEnvelope {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/audio_cache/clear/", handler.ClearAudioCache)
	req := httptest.NewRequest("POST", "/api/audio_cache/clear/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return decodeEnvelope(t, w.Body.String())
}

func decodeEnvelope(t *testing.T, body string) cacheEnvelope {
	t.Helper()
	var env cacheEnvelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	if !env.Result {
		t.Fatalf("request failed: %s", body)
	}
	return env
}

type usageBody struct {
	Root     string `json:"root"`
	Exists   bool   `json:"exists"`
	Bytes    int64  `json:"bytes"`
	Files    int    `json:"files"`
	BySource map[string]struct {
		Files int   `json:"files"`
		Bytes int64 `json:"bytes"`
	} `json:"by_source"`
	AutoPrune struct {
		Enabled       bool  `json:"enabled"`
		MaxMB         int64 `json:"max_mb"`
		MinAgeMinutes int   `json:"min_age_minutes"`
	} `json:"auto_prune"`
	MinAgeMinutes int `json:"min_age_minutes"`
}

type clearBody struct {
	Removed    int               `json:"removed"`
	FreedBytes int64             `json:"freed_bytes"`
	KeptRecent int               `json:"kept_recent"`
	Failed     map[string]string `json:"failed"`
	After      usageBody         `json:"after"`
}

func seedCache(t *testing.T, files map[string]int, ages map[string]time.Duration) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("AUDIO_CACHE_DIR", root)
	for name, size := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
		if age, ok := ages[name]; ok && age > 0 {
			ts := time.Now().Add(-age)
			if err := os.Chtimes(p, ts, ts); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

func TestGetAudioCache_ReportsUsageAndPolicy(t *testing.T) {
	seedCache(t, map[string]int{
		"youtube/a.ogg": 100,
		"youtube/b.ogg": 200,
		"migu/c.ogg":    50,
	}, nil)
	t.Setenv("AUDIO_CACHE_MAX_MB", "2048")
	t.Setenv("AUDIO_CACHE_MIN_AGE_MIN", "30")
	config.ResetSnapshotForTest()
	t.Cleanup(config.ResetSnapshotForTest)

	var u usageBody
	if err := json.Unmarshal(cacheGet(t).Data, &u); err != nil {
		t.Fatal(err)
	}
	if u.Bytes != 350 || u.Files != 3 {
		t.Fatalf("usage: got %d bytes / %d files", u.Bytes, u.Files)
	}
	if u.BySource["youtube"].Files != 2 || u.BySource["migu"].Bytes != 50 {
		t.Fatalf("by_source: got %+v", u.BySource)
	}
	// The settings page has to be able to say why the cache is the size it
	// is, so the worker's policy travels with the numbers.
	if !u.AutoPrune.Enabled || u.AutoPrune.MaxMB != 2048 || u.AutoPrune.MinAgeMinutes != 30 {
		t.Fatalf("auto_prune: got %+v", u.AutoPrune)
	}
	if u.MinAgeMinutes != 30 {
		t.Fatalf("min_age_minutes: got %d", u.MinAgeMinutes)
	}
}

// A cache that does not exist yet is a normal state on a fresh install, not
// an error the settings page should render as a failure.
func TestGetAudioCache_MissingRootReadsAsEmpty(t *testing.T) {
	t.Setenv("AUDIO_CACHE_DIR", filepath.Join(t.TempDir(), "absent"))
	config.ResetSnapshotForTest()
	t.Cleanup(config.ResetSnapshotForTest)

	var u usageBody
	if err := json.Unmarshal(cacheGet(t).Data, &u); err != nil {
		t.Fatal(err)
	}
	if u.Exists || u.Bytes != 0 || u.Files != 0 {
		t.Fatalf("expected an empty report, got %+v", u)
	}
}

func TestGetAudioCache_ReportsAutoPruneDisabled(t *testing.T) {
	seedCache(t, map[string]int{"youtube/a.ogg": 10}, nil)
	t.Setenv("AUDIO_CACHE_MAX_MB", "0")
	config.ResetSnapshotForTest()
	t.Cleanup(config.ResetSnapshotForTest)

	var u usageBody
	if err := json.Unmarshal(cacheGet(t).Data, &u); err != nil {
		t.Fatal(err)
	}
	if u.AutoPrune.Enabled {
		t.Fatal("AUDIO_CACHE_MAX_MB=0 should report the automatic prune as off")
	}
}

func TestClearAudioCache_KeepsRecentFilesByDefault(t *testing.T) {
	root := seedCache(t, map[string]int{
		"youtube/old.ogg":     100,
		"youtube/playing.ogg": 100,
	}, map[string]time.Duration{"youtube/old.ogg": 2 * time.Hour})
	t.Setenv("AUDIO_CACHE_MIN_AGE_MIN", "30")
	config.ResetSnapshotForTest()
	t.Cleanup(config.ResetSnapshotForTest)

	var c clearBody
	if err := json.Unmarshal(cachePost(t, `{}`).Data, &c); err != nil {
		t.Fatal(err)
	}
	if c.Removed != 1 || c.FreedBytes != 100 {
		t.Fatalf("clear: got %+v", c)
	}
	// The protected file is reported, not silently kept: "I removed 1 of 2"
	// and "I removed 1, kept 1 because it is in use" are different claims.
	if c.KeptRecent != 1 {
		t.Fatalf("kept_recent: got %d", c.KeptRecent)
	}
	if _, err := os.Stat(filepath.Join(root, "youtube", "old.ogg")); !os.IsNotExist(err) {
		t.Fatalf("old file should be gone, err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "youtube", "playing.ogg")); err != nil {
		t.Fatalf("recent file must survive: %v", err)
	}
	if c.After.Bytes != 100 {
		t.Fatalf("after.usage should be re-read, got %d", c.After.Bytes)
	}
}

func TestClearAudioCache_AllDropsTheRecentGuard(t *testing.T) {
	root := seedCache(t, map[string]int{
		"youtube/a.ogg": 100,
		"youtube/b.ogg": 100,
	}, nil)
	t.Setenv("AUDIO_CACHE_MIN_AGE_MIN", "30")
	config.ResetSnapshotForTest()
	t.Cleanup(config.ResetSnapshotForTest)

	var c clearBody
	if err := json.Unmarshal(cachePost(t, `{"all":true}`).Data, &c); err != nil {
		t.Fatal(err)
	}
	if c.Removed != 2 || c.KeptRecent != 0 {
		t.Fatalf("clear all: got %+v", c)
	}
	entries, err := os.ReadDir(filepath.Join(root, "youtube"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("cache dir should be empty, holds %d entries", len(entries))
	}
}

// No body at all must mean "clear everything not protected", the same as an
// empty object — the frontend has no reason to send one, and a bind failure
// here would be read as "nothing was cleared".
func TestClearAudioCache_AcceptsNoBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := seedCache(t, map[string]int{"youtube/a.ogg": 100},
		map[string]time.Duration{"youtube/a.ogg": 2 * time.Hour})
	t.Setenv("AUDIO_CACHE_MIN_AGE_MIN", "30")
	config.ResetSnapshotForTest()
	t.Cleanup(config.ResetSnapshotForTest)

	r := gin.New()
	r.POST("/api/audio_cache/clear/", handler.ClearAudioCache)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/audio_cache/clear/", nil))

	var c clearBody
	env := decodeEnvelope(t, w.Body.String())
	if err := json.Unmarshal(env.Data, &c); err != nil {
		t.Fatal(err)
	}
	if c.Removed != 1 {
		t.Fatalf("expected the aged file to go, got %+v", c)
	}
	if _, err := os.Stat(filepath.Join(root, "youtube", "a.ogg")); !os.IsNotExist(err) {
		t.Fatalf("file should be gone, err = %v", err)
	}
}

func TestClearAudioCache_EmptyCacheIsANoOp(t *testing.T) {
	seedCache(t, nil, nil)
	config.ResetSnapshotForTest()
	t.Cleanup(config.ResetSnapshotForTest)

	var c clearBody
	if err := json.Unmarshal(cachePost(t, `{}`).Data, &c); err != nil {
		t.Fatal(err)
	}
	if c.Removed != 0 || c.FreedBytes != 0 || len(c.Failed) != 0 {
		t.Fatalf("expected a clean no-op, got %+v", c)
	}
}
