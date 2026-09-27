package utils

import (
	"path/filepath"
	"testing"
)

// ResolveUnderRoot is the resolver TidyFolder was missing: the client sends
// root-relative worklist paths, and the worker had demanded absolute ones,
// so every file was refused. These tests pin both accepted forms AND the
// containment guarantees that must survive the widening.
func TestResolveUnderRoot_AcceptsBothForms(t *testing.T) {
	root := "/app/media"

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"root-relative (what the frontend sends)", "17/song.ogg", "/app/media/17/song.ogg"},
		{"root-relative, single segment", "song.ogg", "/app/media/song.ogg"},
		{"absolute inside the root", "/app/media/17/song.ogg", "/app/media/17/song.ogg"},
		{"absolute, the root itself", "/app/media", "/app/media"},
		{"empty means the root", "", "/app/media"},
		{"dot segments are cleaned", "./17/./song.ogg", "/app/media/17/song.ogg"},
	}
	for _, c := range cases {
		got, err := ResolveUnderRoot(root, c.in)
		if err != nil {
			t.Errorf("%s: ResolveUnderRoot(%q) errored: %v", c.name, c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: ResolveUnderRoot(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// The absolute form must NOT be re-prefixed. Routing both forms through
// SafeJoin would produce /app/media/app/media/... here, which is the exact
// double-prefix bug UnderRoot's comment warns about — and it would still
// pass every containment test, so it has to be pinned explicitly.
func TestResolveUnderRoot_DoesNotDoublePrefixAnAbsolutePath(t *testing.T) {
	got, err := ResolveUnderRoot("/app/media", "/app/media/17/song.ogg")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/app/media/17/song.ogg" {
		t.Fatalf("double-prefixed: %q", got)
	}
	if filepath.Base(got) != "song.ogg" {
		t.Errorf("basename mangled: %q", got)
	}
}

// Widening the accepted INPUTS must not widen what is REACHABLE. Every
// escape is still refused, in both forms.
func TestResolveUnderRoot_RefusesEscapes(t *testing.T) {
	root := "/app/media"
	for _, in := range []string{
		"../etc/passwd",
		"17/../../etc/passwd",
		"../../..",
		"/etc/passwd",
		"/app/media-backup/song.ogg", // prefix-sharing sibling
		"/app/mediaother/song.ogg",
		"/",
	} {
		if got, err := ResolveUnderRoot(root, in); err == nil {
			t.Errorf("ResolveUnderRoot(%q) = %q, want an error", in, got)
		}
	}
}

func TestResolveUnderRoot_RefusesEmptyRoot(t *testing.T) {
	// An empty root must refuse, not admit: unlike UnderRoot (a predicate
	// whose callers degrade to a narrower answer), this one MOVES files and
	// has no way to know what is in bounds. There is deliberately no branch
	// for it in ResolveUnderRoot — the primitive reached below refuses on
	// its own, and mutation testing showed an explicit check was equivalent.
	for _, in := range []string{"", "17/song.ogg", "/app/media/17/song.ogg"} {
		if got, err := ResolveUnderRoot("", in); err == nil {
			t.Errorf("ResolveUnderRoot(\"\", %q) = %q, want an error", in, got)
		}
	}
}

// Agreement with the two primitives it composes — ResolveUnderRoot must not
// be a third, laxer opinion about containment.
func TestResolveUnderRoot_AgreesWithThePrimitives(t *testing.T) {
	root := "/app/media"
	for _, in := range []string{"17/song.ogg", "../x", "/etc/passwd", "/app/media/a/b"} {
		got, rErr := ResolveUnderRoot(root, in)

		var want string
		var wErr error
		if filepath.IsAbs(in) {
			want, wErr = SafeAbs(root, in)
		} else {
			want, wErr = SafeJoin(root, in)
		}
		if (rErr == nil) != (wErr == nil) {
			t.Errorf("%q: ResolveUnderRoot err=%v but primitive err=%v", in, rErr, wErr)
			continue
		}
		if rErr == nil && got != want {
			t.Errorf("%q: ResolveUnderRoot = %q, primitive = %q", in, got, want)
		}
	}
}
