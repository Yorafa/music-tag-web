package db

import (
	"path/filepath"
	"testing"

	"gorm.io/gorm"
)

// TestDedupeFolderPaths_NoTableIsNotAnError covers the fresh-install
// ordering: AutoMigrate calls the dedupe before the table exists.
func TestDedupeFolderPaths_NoTableIsNotAnError(t *testing.T) {
	gdb := openLegacyDB(t)
	if err := gdb.Migrator().DropTable(&Folder{}); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	if err := DedupeFolderPaths(gdb); err != nil {
		t.Fatalf("DedupeFolderPaths on an empty database: %v", err)
	}
}

// TestDedupeFolderPaths_CollapsesRepeatedScans is the migration's whole
// reason for existing. Three full scans of the same tree used to leave
// three rows per path; after the dedupe there must be exactly one, the
// oldest id's, and every child's parent_id must point at it.
func TestDedupeFolderPaths_CollapsesRepeatedScans(t *testing.T) {
	gdb := openLegacyDB(t)
	seedLegacyRows(t, gdb, []Folder{
		{Name: "music", Path: "/m", UID: "a", FileType: "folder", State: "scanning"},
		{Name: "music", Path: "/m", UID: "b", FileType: "folder", State: "scanning"},
		{Name: "music", Path: "/m", UID: "c", FileType: "folder", State: "scanning"},
		// A child of the SECOND copy — the row that is about to disappear.
		{Name: "song.mp3", Path: "/m/song.mp3", UID: "d", ParentID: "b", FileType: "music"},
		{Name: "song.mp3", Path: "/m/song.mp3", UID: "e", ParentID: "c", FileType: "music"},
		// Untouched path, must survive verbatim.
		{Name: "other.mp3", Path: "/other.mp3", UID: "f", FileType: "music"},
	})

	if err := DedupeFolderPaths(gdb); err != nil {
		t.Fatalf("DedupeFolderPaths: %v", err)
	}

	var rows []Folder
	if err := gdb.Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows after folding (one per distinct path), got %d: %+v", len(rows), rows)
	}
	if rows[0].Path != "/m" || rows[0].UID != "a" {
		t.Errorf("lowest id must win for /m, got uid=%q path=%q", rows[0].UID, rows[0].Path)
	}
	// No orphan: every surviving row's parent_id is either empty or a uid
	// that still exists.
	live := map[string]bool{}
	for _, r := range rows {
		live[r.UID] = true
	}
	for _, r := range rows {
		if r.ParentID != "" && !live[r.ParentID] {
			t.Errorf("row %q kept a dangling parent_id %q", r.UID, r.ParentID)
		}
	}
	var song Folder
	if err := gdb.Where("path = ?", "/m/song.mp3").First(&song).Error; err != nil {
		t.Fatalf("song row missing: %v", err)
	}
	if song.UID != "d" || song.ParentID != "a" {
		t.Errorf("song = uid %q parent %q, want uid \"d\" re-parented onto \"a\"", song.UID, song.ParentID)
	}
}

// TestAutoMigrate_UnblocksOnDuplicatedPaths is the integration point: the
// unique index is useless if AutoMigrate cannot actually install it on a
// database that already has duplicates — and both entry points abort on an
// AutoMigrate error, so that would be a self-inflicted outage.
func TestAutoMigrate_UnblocksOnDuplicatedPaths(t *testing.T) {
	gdb := openLegacyDB(t)
	seedLegacyRows(t, gdb, []Folder{
		{Name: "music", Path: "/m", UID: "a"},
		{Name: "music", Path: "/m", UID: "b"},
		{Name: "song.mp3", Path: "/m/song.mp3", UID: "c", ParentID: "b"},
	})
	if !gdb.Migrator().HasIndex(&Folder{}, "idx_music_folder_path") {
		t.Fatalf("legacy schema should carry the non-unique index")
	}

	if err := AutoMigrate(gdb); err != nil {
		t.Fatalf("AutoMigrate over duplicated paths: %v", err)
	}

	// The index is really there, and the database enforces it.
	if !gdb.Migrator().HasIndex(&Folder{}, "uni_music_folder_path") {
		t.Errorf("unique index on path was not created")
	}
	// Every deployment upgrading to the fingerprint index lands here: a
	// table that predates music_folder.duration. AutoMigrate has to add the
	// column, not assume it.
	if !gdb.Migrator().HasColumn(&Folder{}, "duration") {
		t.Error("AutoMigrate did not add the duration column to a pre-existing table")
	}
	dup := Folder{Name: "music", Path: "/m", UID: "z"}
	if err := gdb.Create(&dup).Error; err == nil {
		t.Errorf("inserting a duplicate path succeeded; the unique index is not enforced")
	}
}

// seedLegacyRows inserts rows into a music_folder that predates the current
// model, naming the columns explicitly.
//
// A plain gdb.Create(&rows) would build the INSERT from the Folder struct,
// which now carries duration — and would fail against a table that does not
// have it. That failure is the test setup breaking rather than the behaviour
// under test, and it is also the one thing these tests genuinely need to
// express: an existing installation whose table is missing the new column.
func seedLegacyRows(t *testing.T, gdb *gorm.DB, rows []Folder) {
	t.Helper()
	for _, r := range rows {
		err := gdb.Exec(
			`INSERT INTO music_folder (name, path, size, file_type, uid, parent_id, state)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			r.Name, r.Path, r.Size, r.FileType, r.UID, r.ParentID, r.State,
		).Error
		if err != nil {
			t.Fatalf("seed legacy row %s: %v", r.Path, err)
		}
	}
}

// openLegacyDB builds the music_folder table the way Django / an older Go
// build left it: identical columns, no unique index on path, only the
// non-unique idx_ one. A fresh AutoMigrate cannot produce this shape, which
// is exactly why the specs here build it by hand.
func openLegacyDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "legacy.sqlite3")
	gdb, err := Open(Config{Driver: "sqlite3", DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// The legacy DDL: the same columns as Folder minus duration, and no
	// unique index on path. duration is left out deliberately — the point is
	// a table that predates the fingerprint index, so AutoMigrate has to add
	// the column as well as install the index.
	if err := gdb.Exec(`CREATE TABLE music_folder (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT, path TEXT, size INTEGER,
		created_at DATETIME, last_scan_time DATETIME, updated_at DATETIME,
		file_type TEXT, uid CHAR(32), parent_id CHAR(32), state TEXT
	)`).Error; err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if err := gdb.Exec(`CREATE INDEX idx_music_folder_path ON music_folder(path)`).Error; err != nil {
		t.Fatalf("create legacy index: %v", err)
	}
	return gdb
}
