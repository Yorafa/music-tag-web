package audioext

import "testing"

// TestAudioMIMEToExt_AllStreamable pins the invariant that makes this package
// worth having: every extension we would ever WRITE for a downloaded file must
// also be an extension the stream handler will SERVE.
//
// The pre-fix code violated this via mime.ExtensionsByType (audio/ogg → ".oga",
// audio/mp4 → ".f4a"), so files downloaded fine and then could never be played
// — the failure surfaced to users as a bogus "download timeout". A regression
// here reintroduces exactly that class of silent breakage.
func TestAudioMIMEToExt_AllStreamable(t *testing.T) {
	for mt, ext := range audioMIMEToExt {
		if !IsStreamableExt(ext) {
			t.Errorf("audioMIMEToExt[%q] = %q, which is NOT in streamableExts; "+
				"a file written with this extension would be filtered out of "+
				"the stream cache and appear to the user as a download timeout", mt, ext)
		}
	}
	if !IsStreamableExt(DefaultExt) {
		t.Errorf("DefaultExt = %q is not streamable", DefaultExt)
	}
}

// TestExtForMIME covers the concrete Content-Type values the five streaming
// plugins' CDNs return, including the two that the stdlib got wrong.
func TestExtForMIME(t *testing.T) {
	cases := []struct{ mediaType, want string }{
		// The two stdlib regressions this table replaces.
		{"audio/ogg", ".ogg"}, // stdlib picked .oga
		{"audio/mp4", ".m4a"}, // stdlib picked .f4a
		// Correct-by-luck under the stdlib; pinned so it stays correct.
		{"audio/mpeg", ".mp3"},
		// Common aliases seen from migu / kugou / kuwo.
		{"audio/x-m4a", ".m4a"},
		{"audio/aac", ".m4a"},
		{"audio/flac", ".flac"},
		{"audio/x-flac", ".flac"},
		{"audio/x-wav", ".wav"},
		{"application/ogg", ".ogg"},
		{"audio/opus", ".opus"},
		// Case + whitespace tolerance: header values are not normalised.
		{"AUDIO/MPEG", ".mp3"},
		{"  audio/ogg  ", ".ogg"},
		// Unknown → "" so the caller falls back to the URL path extension.
		{"application/octet-stream", ""},
		{"text/html", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := ExtForMIME(c.mediaType); got != c.want {
			t.Errorf("ExtForMIME(%q) = %q, want %q", c.mediaType, got, c.want)
		}
	}
}

// TestIsLibraryExt_CaseInsensitive is the P2-2 regression guard: an uppercase
// extension was invisible in the file browser (case-sensitive map lookup) even
// though the tag editor would happily write to it.
func TestIsLibraryExt_CaseInsensitive(t *testing.T) {
	for _, in := range []string{"mp3", "MP3", "Mp3", ".mp3", ".MP3", "flac", "FLAC", ".FLAC"} {
		if !IsLibraryExt(in) {
			t.Errorf("IsLibraryExt(%q) = false, want true", in)
		}
	}
	for _, in := range []string{"", "txt", "lrc", "jpg", "wmv", "exe"} {
		if IsLibraryExt(in) {
			t.Errorf("IsLibraryExt(%q) = true, want false", in)
		}
	}
}

func TestIsLibraryPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/app/media/17/track.mp3", true},
		{"/app/media/17/Track.MP3", true},
		{"/app/media/17/song.FLAC", true},
		{"relative/name.m4a", true},
		{"noext", false},
		{"/app/media/cover.jpg", false},
		{"/app/media/lyrics.lrc", false},
		// A dot in a directory name must not be mistaken for an extension.
		{"/app/media/album.2019/track.ogg", true},
	}
	for _, c := range cases {
		if got := IsLibraryPath(c.path); got != c.want {
			t.Errorf("IsLibraryPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// TestIsStreamableExt_AcceptsBothForms lets callers pass either the dotted
// form from filepath.Ext or a bare extension, in any case.
func TestIsStreamableExt_AcceptsBothForms(t *testing.T) {
	for _, in := range []string{".mp3", "mp3", ".MP3", "MP3", ".ogg", "ogg", ".flac", "flac"} {
		if !IsStreamableExt(in) {
			t.Errorf("IsStreamableExt(%q) = false, want true", in)
		}
	}
	// The stdlib's wrong answers must stay rejected — if either of these
	// flips to true, the mapping table has regressed rather than the filter.
	for _, in := range []string{".oga", ".f4a", ".mpga", ".error", ".part", "", ".txt"} {
		if IsStreamableExt(in) {
			t.Errorf("IsStreamableExt(%q) = true, want false", in)
		}
	}
}

// TestStreamableSupersetOfDownloadable documents the one-way relationship
// between the two sets: every *download-produced* library format must be
// streamable, but the library set legitimately contains exotic formats
// (ape/tta/wv/dsf/dff) that no download source yields and that
// http.ServeFile has no business handing to an <audio> element.
func TestStreamableSupersetOfDownloadable(t *testing.T) {
	// Formats a DownloadSource / TagSource CDN can actually produce.
	downloadable := []string{"mp3", "m4a", "ogg", "opus", "wav", "flac"}
	for _, bare := range downloadable {
		if !IsLibraryExt(bare) {
			t.Errorf("%q is downloadable but not a library ext", bare)
		}
		if !IsStreamableExt(bare) {
			t.Errorf("%q is downloadable but not streamable", bare)
		}
	}
	// Container formats are streamable but deliberately not library formats.
	for _, bare := range []string{"webm", "mkv", "mp4"} {
		if !IsStreamableExt(bare) {
			t.Errorf("%q should be streamable (yt-dlp yields it without transcode)", bare)
		}
		if IsLibraryExt(bare) {
			t.Errorf("%q should NOT be a library ext (video container)", bare)
		}
	}
}
