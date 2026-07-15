package tasks

import (
	"context"
	"fmt"

	"go-music-tag/internal/db"
	"gorm.io/gorm"
)

// ClearMusicHandler hard-deletes scanned data, mirroring Django `applications/task/tasks.py::clear_music`:
//
//   1) Delete all TaskRecord rows
//   2) Delete all Track rows
//   3) Delete all Folder rows (cascading to Album/Artist via FK in Django — GORM
//      deletes in whichever order matches the FK constraints)
//   4) Vacuum for sqlite; OPTIMIZE TABLE for mysql (best-effort, ignored on failure)
//
// We never touch user accounts / settings / music_folder (the root config) —
// Django's `music_folder` table holds the configured library paths and is preserved.
type ClearMusicHandler struct {
	DB        *gorm.DB
	DBDriver  string // "sqlite" | "mysql" — used for VACUUM/OPTIMIZE post-cleanup
}

func NewClearMusicHandler(gormDB *gorm.DB, driver string) *ClearMusicHandler {
	return &ClearMusicHandler{DB: gormDB, DBDriver: driver}
}

func (h *ClearMusicHandler) ProcessTask(ctx context.Context, _ Task) error {
	if h.DB == nil {
		return fmt.Errorf("clear: DB not initialized")
	}

	// Delete in FK-safe order. Django has:
	//   track.album  → album
	//   track.artist → artist
	//   track.genre  → genre
	//   taskrecord.task → task
	// Album/Artist/Genre records are naturally orphaned after Track delete.
	steps := []struct {
		name  string
		model interface{}
	}{
		// "task-progress + tag-action-history" rows first
		{"TaskRecord", &db.TaskRecord{}},
		{"Task", &db.Task{}},
		// Then audio metadata cascade
		{"Track", &db.Track{}},
		{"Album", &db.Album{}},
		{"Artist", &db.Artist{}},
		{"Genre", &db.Genre{}},
		{"Attachment", &db.Attachment{}},
		// Folder last (tracker's parent key)
		{"Folder", &db.Folder{}},
	}

	for _, s := range steps {
		if err := h.DB.WithContext(ctx).
			Session(&gorm.Session{AllowGlobalUpdate: true}).
			Unscoped().
			Delete(s.model).Error; err != nil {
			return fmt.Errorf("clear: delete %s: %w", s.name, err)
		}
	}

	// Best-effort DB scrub. Failures here are non-fatal.
	switch h.DBDriver {
	case "sqlite":
		if err := h.DB.WithContext(ctx).Exec("VACUUM").Error; err != nil {
			fmt.Printf("[clear] sqlite VACUUM failed (ignored): %v\n", err)
		}
	case "mysql":
		for _, table := range []string{
			"task_record", "track", "album", "artist", "genre",
			"track_attachment", "music_folder",
		} {
			if err := h.DB.WithContext(ctx).Exec("OPTIMIZE TABLE " + table).Error; err != nil {
				fmt.Printf("[clear] mysql OPTIMIZE %s failed (ignored): %v\n", table, err)
			}
		}
	}

	return nil
}
