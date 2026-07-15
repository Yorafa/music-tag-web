package utils

import (
	"os"
	"testing"
)

func TestMusicRoot_EnvWins(t *testing.T) {
	t.Setenv("MUSIC_DIR", "/srv/music")
	if got := MusicRoot(); got != "/srv/music" {
		t.Errorf("MUSIC_DIR=/srv/music → MusicRoot=%q (want /srv/music)", got)
	}
}

func TestMusicRoot_DefaultWhenUnset(t *testing.T) {
	// os.Unsetenv is safe but t.Setenv("MUSIC_DIR","") would also work
	// for downstream readers; we use Unsetenv to model the env-EMPTY case
	// specifically (Dockerfile's default /app/media is only the fallback
	// when the var is genuinely missing, not when set to "").
	prev, had := os.LookupEnv("MUSIC_DIR")
	if had {
		os.Unsetenv("MUSIC_DIR")
		t.Cleanup(func() { os.Setenv("MUSIC_DIR", prev) })
	}
	if got := MusicRoot(); got != "/app/media" {
		t.Errorf("MUSIC_DIR unset → MusicRoot=%q (want /app/media)", got)
	}
}

func TestMusicRoot_EmptyStringIsFallthrough(t *testing.T) {
	// Empty value is treated as unset → fallback. Pin this so a future
	// refactor that treats "" as a configured-empty root surfaces here.
	t.Setenv("MUSIC_DIR", "")
	if got := MusicRoot(); got != "/app/media" {
		t.Errorf("MUSIC_DIR='' → MusicRoot=%q (want /app/media)", got)
	}
}
