package tasks

import "testing"

func TestSanitizeYTDLPFormat(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		// allowed (matching real yt-dlp selectors)
		{"", "bestaudio/best", true},
		{"bestaudio/best", "bestaudio/best", true},
		{"bestaudio[height<=480]", "bestaudio[height<=480]", true},
		{"best[ext=mp4]", "best[ext=mp4]", true},
		// rejected: shell/flag/argv injection
		{"--exec=rm -rf /", "", false},
		{"-exec=something", "", false},
		{"-f", "", false},
		// rejected: bad characters
		{"bestaudio;rm", "", false},
		{"bestaudio audio", "", false},   // space → not single argv token
		{"a${X}b", "", false},            // template-bypass
		{"a\nb", "", false},              // newline
		{"a`b`", "", false},              // backticks
	}
	for _, c := range cases {
		got, err := SanitizeYTDLPFormat(c.in)
		if c.ok {
			if err != nil {
				t.Errorf("SanitizeYTDLPFormat(%q): unexpected err %v", c.in, err)
				continue
			}
			if got != c.want {
				t.Errorf("SanitizeYTDLPFormat(%q)=%q, want %q", c.in, got, c.want)
			}
		} else {
			if err == nil {
				t.Errorf("SanitizeYTDLPFormat(%q): expected err, got %q", c.in, got)
			}
		}
	}
}

func TestSanitizeYTDLPOutputFormat(t *testing.T) {
	allowed := []string{"", "mp3", "m4a", "ogg", "vorbis", "wav", "MP3", "  Ogg "}
	for _, in := range allowed {
		if _, err := SanitizeYTDLPOutputFormat(in); err != nil {
			t.Errorf("SanitizeYTDLPOutputFormat(%q): unexpected err %v", in, err)
		}
	}
	rejected := []string{"mp4", "opus", "exec", "raw", "json"}
	for _, in := range rejected {
		if _, err := SanitizeYTDLPOutputFormat(in); err == nil {
			t.Errorf("SanitizeYTDLPOutputFormat(%q): expected err", in)
		}
	}
}

func TestSanitizeYTDLPQuality(t *testing.T) {
	allowed := []string{"", "0", "1", "192", "320", "9999"}
	for _, in := range allowed {
		if _, err := SanitizeYTDLPQuality(in); err != nil {
			t.Errorf("SanitizeYTDLPQuality(%q): unexpected err %v", in, err)
		}
	}
	rejected := []string{"abc", "-1", "12abc", string(make([]byte, 5)) + "12345"} // 5 digits
	for _, in := range rejected {
		if _, err := SanitizeYTDLPQuality(in); err == nil {
			t.Errorf("SanitizeYTDLPQuality(%q): expected err", in)
		}
	}
}
