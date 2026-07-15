package tasks

import (
	"strings"
	"testing"
)

// TestSanitizeTidySeg covers P1.5 issue F — H2: the helper that gates
// tag-derived directory segments used by TidyFolder onto os.Rename. Any
// attacker-controlled album/artist/genre that survives the literal
// bad-char strip AND collapses to "." / ".." / leading-dot must be
// refused so the rename cannot escape p.RootPath.
func TestSanitizeTidySeg(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		want     string
		wantErr  bool
		errMatch string
	}{
		{"normal album", "Album Name", "Album Name", false, ""},
		{"slash replaced", "a/b", "a_b", false, ""},
		{"backslash replaced", `a\b`, "a_b", false, ""},
		// Both literal "../" inputs land as "..___.." after the bad-char
		// strip (slash → underscore). filepath.Base keeps the trailing
		// segment, which still starts with "." — the leading-dot guard
		// catches them and refuses. This is the actual H2 defence: real
		// traversal cannot escape through os.Rename.
		{"deep traversal refused", "../../etc", "", true, "unsafe"},
		{"deeper traversal refused", "../../../etc/passwd", "", true, "unsafe"},
		{"empty after trim", "", "", true, "unsafe"},
		{"literal dot", ".", "", true, "unsafe"},
		{"literal dotdot", "..", "", true, "unsafe"},
		{"leading dotfile", ".hidden", "", true, "hidden"},
		// NUL byte is *not* in the bad-char list — it survives sanitisation
		// and would later trip os.WriteFile on EINVAL. We accept the
		// segment from sanitizeTidySeg's point of view (no leading dot,
		// no parent reference) so the rename-side defence can fail clean
		// downstream. Pin the behavioural contract explicitly.
		{"raw nul passes sanitizer", "\x00evil", "\x00evil", false, ""},
		{"raw colon replaced", "A:B", "A_B", false, ""},
		// "foo/../bar" goes through the bad-char strip: every "/" is
		// replaced by "_". Result: "foo" + "_" + ".." + "_" + "bar" =
		// "foo_.._bar" (one underscore between each `/` boundary; the
		// middle `..` is preserved as text). filepath.Base keeps that
		// as a single segment — no leading dot, no parent ref. The
		// rename-side (SafeJoin on the final dst) remains the last stand
		// for any pathological case the sanitizer might miss.
		{"intra-path dotdot collapses to segment", "foo/../bar", "foo_.._bar", false, ""},
	}
	for _, c := range cases {
		got, err := sanitizeTidySeg("test", c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("%s: sanitizeTidySeg(%q) expected error, got %q", c.name, c.in, got)
				continue
			}
			if c.errMatch != "" && !strings.Contains(err.Error(), c.errMatch) {
				t.Errorf("%s: error %v must contain %q", c.name, err, c.errMatch)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: sanitizeTidySeg(%q) unexpected error %v", c.name, c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: sanitizeTidySeg(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}
