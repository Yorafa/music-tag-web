package handler

import "testing"

// contentTypeFor is unexported and has no business being exported for a test,
// so this is an in-package test — the package already carries several.
//
// The exceptions to audio/<ext> are the entire reason the table exists, so a
// test that only checked .ogg would pass with the map deleted. The casing and
// unknown-extension rows are here because the callers hand it whatever the
// filesystem gave them.
func TestContentTypeFor(t *testing.T) {
	for name, want := range map[string]string{
		// the exceptions
		"a.mp3":  "audio/mpeg",
		"a.m4a":  "audio/mp4",
		"a.opus": "audio/ogg",
		"a.wv":   "audio/x-wavpack",
		"a.wma":  "audio/x-ms-wma",
		// the spelled-the-obvious-way cases
		"a.ogg":  "audio/ogg",
		"a.flac": "audio/flac",
		"a.wav":  "audio/wav",
		// not audio at all
		"cover.jpg":      "image/jpg",
		"cover.PNG":      "image/png",
		"album.nfo":      "text/plain; charset=utf-8",
		"lyrics.lrc":     "text/plain; charset=utf-8",
		"notes.xyz":      "application/octet-stream",
		"no-extension":   "application/octet-stream",
		"archive.tar.gz": "application/octet-stream",
	} {
		if got := contentTypeFor(name); got != want {
			t.Errorf("contentTypeFor(%q) = %q, want %q", name, got, want)
		}
	}
}
