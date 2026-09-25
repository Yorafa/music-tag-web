package handler

import (
	"os"
	"path/filepath"
	"testing"

	"go-music-tag/internal/testaudio"
	"go-music-tag/internal/utils"
)

// TestApplyFileUpdate_ReportsRename pins the out-param contract the
// detail dialog's 文件名 row depends on.
//
// The frontend keys every row by its path — `WorklistRow.id ==
// fullPath`, and so is `LibraryRow.id`. A rename performed behind
// update_id3/ therefore invalidates the row that asked for it: the table
// would keep a path that no longer resolves, and every later operation
// on that row (stream, re-edit, save again) would fail against a file
// that is not there. The client cannot derive the new name itself — the
// backend appends the extension, sanitises the result, expands $artist /
// $title templates and refuses collisions — so it has to be told.
//
// Three cases, because the interesting one is the negative: a caller must
// be able to pass a pointer and learn that NOTHING was renamed, without
// the field being cleared or a stale value leaking through.
func TestApplyFileUpdate_ReportsRename(t *testing.T) {
	t.Run("reports the new base name when it renames", func(t *testing.T) {
		music := t.TempDir()
		t.Setenv("MUSIC_DIR", music)
		src := testaudio.SeedMP3(t, music, "before.mp3")

		// No extension in the requested name on purpose: the handler
		// appends the source extension, so the reported name must include
		// it. A client that trusted its own input here would store a path
		// that does not exist.
		res, err := applyFileUpdate(src, map[string]interface{}{
			"title":    "Some Title",
			"filename": "after",
		})
		if err != nil {
			t.Fatalf("applyFileUpdate: %v", err)
		}

		if res.RenamedTo != "after.mp3" {
			t.Errorf("res.RenamedTo = %q, want %q", res.RenamedTo, "after.mp3")
		}
		if _, err := os.Stat(filepath.Join(music, res.RenamedTo)); err != nil {
			t.Errorf("reported name does not resolve on disk: %v", err)
		}
		if _, err := os.Stat(src); !os.IsNotExist(err) {
			t.Errorf("original still present at %s — the rename did not happen", src)
		}
	})

	t.Run("leaves the field untouched when nothing was renamed", func(t *testing.T) {
		music := t.TempDir()
		t.Setenv("MUSIC_DIR", music)
		src := testaudio.SeedMP3(t, music, "same.mp3")

		// No "filename" key at all: a plain tag write.
		res, err := applyFileUpdate(src, map[string]interface{}{
			"title": "Just A Title",
		})
		if err != nil {
			t.Fatalf("applyFileUpdate: %v", err)
		}
		if res.RenamedTo != "" {
			t.Errorf("res.RenamedTo = %q, want empty — a client would rewrite the row for a file that did not move", res.RenamedTo)
		}
	})

	t.Run("reports nothing when the requested name is the current one", func(t *testing.T) {
		music := t.TempDir()
		t.Setenv("MUSIC_DIR", music)
		src := testaudio.SeedMP3(t, music, "unchanged.mp3")

		// The common case: the dialog pre-fills the field with the current
		// filename and the user edits only the tags. The handler resolves
		// the target, finds it equals the source, and skips the rename —
		// so there is nothing to report and the row must not move.
		res, err := applyFileUpdate(src, map[string]interface{}{
			"filename": "unchanged.mp3",
		})
		if err != nil {
			t.Fatalf("applyFileUpdate: %v", err)
		}
		if res.RenamedTo != "" {
			t.Errorf("res.RenamedTo = %q, want empty for a no-op rename", res.RenamedTo)
		}
		if _, err := os.Stat(src); err != nil {
			t.Errorf("file vanished on a no-op rename: %v", err)
		}
	})

	t.Run("reports nothing when the rename is refused", func(t *testing.T) {
		music := t.TempDir()
		t.Setenv("MUSIC_DIR", music)
		src := testaudio.SeedMP3(t, music, "source.mp3")
		testaudio.SeedMP3(t, music, "taken.mp3")

		// The collision is detected before any write, so the caller must
		// not be handed a new name for a file that stayed put.
		res, err := applyFileUpdate(src, map[string]interface{}{
			"filename": "taken",
		})
		if err == nil {
			t.Fatal("applyFileUpdate succeeded — expected a collision error")
		}
		if res.RenamedTo != "" {
			t.Errorf("res.RenamedTo = %q, want empty on a refused rename", res.RenamedTo)
		}
	})
}

// TestAddDone_OmitsNewNameUnlessRenamed pins the report shape. A
// `new_file_name: ""` would be read by a client as "renamed to the empty
// string" and could rewrite the row to the directory itself, so the key
// is absent rather than empty.
func TestAddDone_OmitsNewNameUnlessRenamed(t *testing.T) {
	var r updateBatchReport
	r.addDone("a/b.mp3", "")
	got, ok := r.done[0]["new_file_name"]
	if ok {
		t.Errorf("new_file_name present with no rename: %v", got)
	}
	if r.done[0]["file_full_path"] != "a/b.mp3" {
		t.Errorf("file_full_path = %v", r.done[0]["file_full_path"])
	}

	r.addDone("a/c.mp3", "c.mp3")
	if got := r.done[1]["new_file_name"]; got != "c.mp3" {
		t.Errorf("new_file_name = %v, want %q", got, "c.mp3")
	}
}

// TestRenderedNewPath documents how the client turns the reported base
// name back into the relative path it stores. The rename target is always
// in the same parent directory, so this is a Dir+Base join and nothing
// more — asserted here because getting it wrong silently points the row
// at a sibling directory.
func TestRenderedNewPath(t *testing.T) {
	cases := []struct{ oldRel, newBase, want string }{
		{"Artist/Album/song.mp3", "renamed.mp3", "Artist/Album/renamed.mp3"},
		{"song.mp3", "renamed.mp3", "renamed.mp3"},
	}
	for _, c := range cases {
		got := filepath.ToSlash(filepath.Join(filepath.Dir(c.oldRel), c.newBase))
		if got != c.want {
			t.Errorf("join(%q, %q) = %q, want %q", c.oldRel, c.newBase, got, c.want)
		}
	}
	_ = utils.SanitizePath
}
