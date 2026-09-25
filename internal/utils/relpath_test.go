package utils

import (
	"strings"
	"testing"
)

// TestSafeRelPath_RejectsTraversal pins the shape check that replaced
// `strings.Contains(cleaned, "..")` in the download handlers (REVIEW.md
// P3-7).
func TestSafeRelPath_RejectsTraversal(t *testing.T) {
	bad := []string{
		"..",
		"../etc/passwd",
		"a/../../etc/passwd",
		"a/b/../../../x",
		"./../x",
		"/etc/passwd",
		"/a/b",
		"",
	}
	for _, p := range bad {
		if got, err := SafeRelPath(p); err == nil {
			t.Errorf("SafeRelPath(%q) = %q, want an error", p, got)
		}
	}
}

// TestSafeRelPath_AllowsDoubleDotInNames is the false positive the old
// substring check produced: "Album..Deluxe" and "1997..2000" are ordinary
// directory names, and a library containing them could not be downloaded
// into at all.
func TestSafeRelPath_AllowsDoubleDotInNames(t *testing.T) {
	good := map[string]string{
		"Album..Deluxe/x.ogg":           "Album..Deluxe/x.ogg",
		"1997..2000 Remaster/track.mp3": "1997..2000 Remaster/track.mp3",
		"a..b..c":                       "a..b..c",
		"dots.../track.mp3":             "dots.../track.mp3",
		"..hidden/track.mp3":            "..hidden/track.mp3",
		"Artist/Album../track.mp3":      "Artist/Album../track.mp3",
		"Album..Deluxe/..hidden/x.ogg":  "Album..Deluxe/..hidden/x.ogg",
		"Artist/Album/01 - Song.mp3":    "Artist/Album/01 - Song.mp3",
		"./Artist/track.mp3":            "Artist/track.mp3",
		"Artist//Album/track.mp3":       "Artist/Album/track.mp3",
		"Artist/./Album/track.mp3":      "Artist/Album/track.mp3",
		// A `..` segment that stays inside the tree is resolved away by
		// Clean before the segment loop ever runs, so these are accepted
		// and normalised. The old substring check refused both.
		"a/b/../c/track.mp3": "a/c/track.mp3",
		// "album/.." collapses to the root itself, which is a legitimate
		// destination directory.
		"album/..": ".",
	}
	for p, want := range good {
		got, err := SafeRelPath(p)
		if err != nil {
			t.Errorf("SafeRelPath(%q) = error %v, want %q", p, err, want)
			continue
		}
		if got != want {
			t.Errorf("SafeRelPath(%q) = %q, want %q", p, got, want)
		}
	}
}

// TestSafeRelPath_ThenSafeJoin is the pair the download path actually uses:
// the request-boundary shape check, then the containment proof where the
// path is built. If a future change ever let an upward path past the first
// check, the second still has to stop it — that redundancy is the point of
// routing the join through SafeJoin.
func TestSafeRelPath_ThenSafeJoin(t *testing.T) {
	const root = "/app/media"
	cleaned, err := SafeRelPath("Album..Deluxe/../../etc/passwd")
	if err == nil {
		if _, joinErr := SafeJoin(root, cleaned); joinErr == nil {
			t.Errorf("upward path %q survived both SafeRelPath and SafeJoin", cleaned)
		}
		return
	}
	if !strings.Contains(err.Error(), "escapes upward") {
		t.Errorf("SafeRelPath error = %v, want an upward-escape rejection", err)
	}
}
