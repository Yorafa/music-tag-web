package handler

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-music-tag/internal/tag"
	"go-music-tag/internal/testaudio"
	"go-music-tag/internal/utils"
)

// TestApplyFileUpdate_RefusesToOverwrite pins REVIEW.md P2-6.
//
// os.Rename silently replaces its destination. Two tracks whose filename
// templates render to the same name — which happens as soon as the template
// omits a discriminating field, e.g. two "Unknown Artist" files in one
// directory — meant one file vanished with no error anywhere.
//
// The contract here is refuse, not disambiguate: silently writing
// "song (2).mp3" would put a file on disk whose name no longer matches
// anything the template or the operator asked for.
func TestApplyFileUpdate_RefusesToOverwrite(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)

	// Both sides are real decodable MP3s. The bystander additionally
	// carries a marker tag so we can assert the whole file survives the
	// refused update, not just that it still exists.
	src := testaudio.SeedMP3(t, music, "source.mp3")
	dst := testaudio.SeedMP3(t, music, "target.mp3")
	const precious = "PRECIOUS-DO-NOT-LOSE"
	if err := tag.Write(dst, &tag.TagUpdate{Title: &[]string{precious}[0]}); err != nil {
		t.Fatalf("mark the bystander: %v", err)
	}
	srcBefore, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}

	err = applyFileUpdate(src, map[string]interface{}{
		"title":    "Renamed Title",
		"filename": "target",
	})
	if err == nil {
		t.Fatal("applyFileUpdate succeeded — the existing file was silently replaced")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("err = %v, want an 'already exists' conflict error", err)
	}

	// The decisive assertion: the bystander is intact.
	info, readErr := tag.Read(dst)
	if readErr != nil {
		t.Fatalf("target vanished or became unreadable: %v", readErr)
	}
	if info.Title != precious {
		t.Errorf("target title = %q, want %q — a file was modified", info.Title, precious)
	}

	// A refused update must also leave the source byte-identical. The
	// conflict check used to run after tag.Write, so the source was
	// already rewritten before the function reported the conflict.
	srcAfter, readErr := os.ReadFile(src)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(srcBefore, srcAfter) {
		t.Errorf("source was modified by a refused update (%d -> %d bytes)",
			len(srcBefore), len(srcAfter))
	}
}

// TestApplyFileUpdate_AllowsFreeTarget is the other half: the conflict
// check must not block a rename that genuinely has a free destination.
func TestApplyFileUpdate_AllowsFreeTarget(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)

	src := testaudio.SeedMP3(t, music, "before.mp3")

	if err := applyFileUpdate(src, map[string]interface{}{
		"filename": "after",
	}); err != nil {
		t.Fatalf("applyFileUpdate: %v", err)
	}

	want := filepath.Join(music, "after.mp3")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("rename did not happen: %v", err)
	}
	if _, err := os.Stat(src); err == nil {
		t.Error("source still present after rename")
	}
}

// TestApplyFileUpdate_RenamesOntoItselfIsANoOp guards the case where the
// template renders the current name: the conflict check must not fire
// against the file we are renaming.
func TestApplyFileUpdate_RenamesOntoItselfIsANoOp(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)

	src := testaudio.SeedMP3(t, music, "same.mp3")

	if err := applyFileUpdate(src, map[string]interface{}{
		"filename": "same",
	}); err != nil {
		t.Fatalf("applyFileUpdate on an unchanged name: %v", err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("file disappeared: %v", err)
	}
}

// TestApplyFileUpdate_KeepsResultUnderMusicRoot pins the actual invariant
// for a traversing template. The contract is NOT "reject" — SanitizePath
// strips the separators, so "../../etc/passwd" becomes
// ".._.._etc_passwd" and lands harmlessly inside MUSIC_DIR. The contract
// is that the result never escapes MUSIC_DIR, and the source never
// disappears without a target.
func TestApplyFileUpdate_KeepsResultUnderMusicRoot(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)

	src := filepath.Join(music, "song.mp3")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Use a sentinel filename that cannot pre-exist anywhere, so the
	// assertion is about what the rename CREATED rather than about what
	// happens to be on the machine. (/etc/passwd is a bad choice here: on
	// a normal host it already exists, so its presence proves nothing.)
	sentinel := "zzz-escape-sentinel-9f3a.mp3"
	err := applyFileUpdate(src, map[string]interface{}{
		"filename": "../../etc/" + sentinel,
	})

	// Nothing named after the sentinel may exist anywhere — not directly
	// above the music root, not two levels up, not in /etc.
	candidates := []string{
		filepath.Join(music, sentinel),
		filepath.Join(filepath.Dir(music), sentinel),
		filepath.Join(filepath.Dir(filepath.Dir(music)), sentinel),
		filepath.Join("/etc", sentinel),
	}
	for _, c := range candidates {
		if _, statErr := os.Stat(c); statErr == nil {
			t.Fatalf("rename escaped MUSIC_DIR and created %s", c)
		}
	}

	// The file is either still at src, or was renamed to something inside
	// music — never lost.
	if _, statErr := os.Stat(src); statErr == nil {
		return // unchanged, fine
	}
	entries, readErr := os.ReadDir(music)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) == 0 {
		t.Errorf("err=%v — source vanished and no target exists", err)
	}
}

// TestSafeAbsNeverDoublesThePrefix documents the exact failure the old
// TrimPrefix+SafeJoin pattern produced, and asserts the property that
// actually matters: SafeAbs validates the path it is given rather than
// re-deriving one, so it cannot manufacture a doubled prefix.
func TestSafeAbsNeverDoublesThePrefix(t *testing.T) {
	root := "/app/media"
	abs := "/app/media/foo.mp3"

	// Sanity: the old pattern. TrimPrefix is a no-op on a prefix mismatch,
	// so the absolute path reached SafeJoin, which treated it as relative.
	rel := strings.TrimPrefix(abs, root)
	doubled, joinErr := utils.SafeJoin(root, rel)
	if joinErr == nil && strings.Count(doubled, "/app/media") > 1 {
		t.Logf("old pattern would have produced %q", doubled)
	}

	// SafeAbs validates rather than re-derives: a genuine in-root path
	// passes through unchanged, and nothing outside root is accepted.
	got, err := utils.SafeAbs(root, abs)
	if err != nil {
		t.Fatalf("SafeAbs rejected a legitimate in-root path: %v", err)
	}
	if got != abs {
		t.Errorf("SafeAbs(%q, %q) = %q, want it unchanged", root, abs, got)
	}
	if _, err := utils.SafeAbs(root, "/etc/passwd"); err == nil {
		t.Error("SafeAbs accepted a path outside root")
	}
}
