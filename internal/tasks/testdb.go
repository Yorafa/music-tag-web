package tasks

import (
	"database/sql"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// openTestDB opens a throwaway in-memory SQLite for one test.
//
// The pool limit is the whole point of this helper. ":memory:" names a
// database per CONNECTION, not per process: SQLite gives a second pooled
// connection its own empty database, so a test that happens to run two
// queries concurrently sees "no such table: music_folder" on the second one.
// Nothing in these tests is concurrent, but Go's scheduler decides when the
// pool opens a second connection, not the test — so the failure arrives at
// random, roughly once in a few dozen full runs, on whichever test happened
// to be unlucky. That is a bad trade for a test suite whose value rests on
// being able to believe a red run.
//
// One connection also removes any chance of a lock contention error between
// the fp indexer's parallel workers and the test goroutine, which is what a
// larger pool would buy here: nothing, since the work is tiny.
func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	sqlDB, err := sql.Open(sqlite.DriverName, ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)

	gdb, err := gorm.Open(sqlite.Dialector{Conn: sqlDB}, &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm open: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return gdb
}
