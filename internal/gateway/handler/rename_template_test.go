package handler

import (
	"os"
	"testing"

	"go-music-tag/internal/tag"
	"go-music-tag/internal/testaudio"
)

// seedTags writes title/artist/album onto a fixture so a later
// applyFileUpdate has a non-empty on-disk baseline to fall back to.
// testaudio.MP3Bytes deliberately ships untagged.
func seedTags(t *testing.T, music, name, title, artist, album string) string {
	t.Helper()
	path := testaudio.SeedMP3(t, music, name)
	if err := applyFileUpdate(path, map[string]interface{}{
		"title":  title,
		"artist": artist,
		"album":  album,
	}, nil); err != nil {
		t.Fatalf("seed tags: %v", err)
	}
	return path
}

// TestApplyFileUpdate_FilenameTemplateSeesPendingTags pins the template
// source for the rename.
//
// The detail dialog submits the whole form in one request: the new title
// AND the new filename. If `filename` is expanded against the tags read
// off disk, the two disagree — the user fixes a typo in the title, types
// `${artist} - ${title}` in the 文件名 row to match, and gets a file named
// after the typo they just corrected. Worse, the rename then resolves to
// the path the file already occupies, so the handler treats it as a
// no-op, reports no new name, and the row silently keeps the old name.
//
// So the values being written in THIS request must win over the values
// already on disk.
func TestApplyFileUpdate_FilenameTemplateSeesPendingTags(t *testing.T) {
	t.Run("title set in the same request is used", func(t *testing.T) {
		music := t.TempDir()
		t.Setenv("MUSIC_DIR", music)
		src := seedTags(t, music, "stale.mp3", "Old Title", "Seed Artist", "Seed Album")

		var renamedTo string
		if err := applyFileUpdate(src, map[string]interface{}{
			"title":    "BrandNewTitle",
			"filename": "${title}",
		}, &renamedTo); err != nil {
			t.Fatalf("applyFileUpdate: %v", err)
		}
		if renamedTo != "BrandNewTitle.mp3" {
			t.Errorf("renamedTo = %q, want %q — the template rendered against the stale on-disk title", renamedTo, "BrandNewTitle.mp3")
		}
	})

	t.Run("artist set in the same request is used", func(t *testing.T) {
		music := t.TempDir()
		t.Setenv("MUSIC_DIR", music)
		src := seedTags(t, music, "stale2.mp3", "Seed Title", "Old Artist", "Seed Album")

		var renamedTo string
		if err := applyFileUpdate(src, map[string]interface{}{
			"artist":   "NewArtist",
			"filename": "${artist} - ${title}",
		}, &renamedTo); err != nil {
			t.Fatalf("applyFileUpdate: %v", err)
		}
		// The title was NOT in this request, so it must fall back to the
		// on-disk value rather than rendering empty.
		if want := "NewArtist - Seed Title.mp3"; renamedTo != want {
			t.Errorf("renamedTo = %q, want %q", renamedTo, want)
		}
	})

	t.Run("album set in the same request is used", func(t *testing.T) {
		music := t.TempDir()
		t.Setenv("MUSIC_DIR", music)
		src := seedTags(t, music, "stale3.mp3", "Seed Title", "Seed Artist", "Old Album")

		var renamedTo string
		if err := applyFileUpdate(src, map[string]interface{}{
			"album":    "NewAlbum",
			"filename": "${album} - ${title}",
		}, &renamedTo); err != nil {
			t.Fatalf("applyFileUpdate: %v", err)
		}
		if want := "NewAlbum - Seed Title.mp3"; renamedTo != want {
			t.Errorf("renamedTo = %q, want %q", renamedTo, want)
		}
	})

	t.Run("unset fields still fall back to the on-disk tags", func(t *testing.T) {
		music := t.TempDir()
		t.Setenv("MUSIC_DIR", music)
		src := seedTags(t, music, "stale4.mp3", "Seed Title", "Seed Artist", "Seed Album")

		// Only the filename is supplied. Overlaying the pending tags must
		// not blank out the ones the request says nothing about.
		var renamedTo string
		if err := applyFileUpdate(src, map[string]interface{}{
			"filename": "${artist} - ${title}",
		}, &renamedTo); err != nil {
			t.Fatalf("applyFileUpdate: %v", err)
		}
		if want := "Seed Artist - Seed Title.mp3"; renamedTo != want {
			t.Errorf("renamedTo = %q, want %q", renamedTo, want)
		}
	})

	t.Run("an empty submitted value does not blank the template", func(t *testing.T) {
		music := t.TempDir()
		t.Setenv("MUSIC_DIR", music)
		src := seedTags(t, music, "stale5.mp3", "Seed Title", "Seed Artist", "Seed Album")

		// The dialog sends `title: ""` for a field the user cleared. The
		// tag write treats empty as "leave alone", so the template must
		// agree — otherwise the name is built from a value the file does
		// not have either.
		var renamedTo string
		if err := applyFileUpdate(src, map[string]interface{}{
			"title":    "",
			"filename": "${title}",
		}, &renamedTo); err != nil {
			t.Fatalf("applyFileUpdate: %v", err)
		}
		if want := "Seed Title.mp3"; renamedTo != want {
			t.Errorf("renamedTo = %q, want %q", renamedTo, want)
		}
	})
}

// TestApplyFileUpdate_CollidingRenameStillWritesNothing guards the fix
// against regressing db622e6: learning the pending tags before resolving
// the rename must NOT move the resolution after the writes. A refused
// rename has to leave both the name and the tags untouched.
func TestApplyFileUpdate_CollidingRenameStillWritesNothing(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)
	src := seedTags(t, music, "source.mp3", "Seed Title", "Seed Artist", "Seed Album")
	taken := seedTags(t, music, "NewAlbum - Seed Title.mp3", "Other", "Other", "Other")

	err := applyFileUpdate(src, map[string]interface{}{
		"album":    "NewAlbum",
		"filename": "${album} - ${title}",
	}, nil)
	if err == nil {
		t.Fatal("applyFileUpdate succeeded — expected a collision error")
	}
	if _, statErr := os.Stat(src); statErr != nil {
		t.Errorf("source file vanished despite the refused rename: %v", statErr)
	}
	if _, statErr := os.Stat(taken); statErr != nil {
		t.Errorf("collision target was overwritten: %v", statErr)
	}

	// The tags must not have been written either — that was the whole
	// point of hoisting the collision check.
	info, readErr := tag.Read(src)
	if readErr != nil {
		t.Fatalf("re-read source: %v", readErr)
	}
	if info.Album != "Seed Album" {
		t.Errorf("source album = %q, want %q — tags were written despite the refused rename", info.Album, "Seed Album")
	}
}
