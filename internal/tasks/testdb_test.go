package tasks

import "testing"

// The pool limit is asserted rather than left to mutation testing, because
// the defect it prevents is itself intermittent: with an unlimited pool a
// second connection sometimes gets its own empty ":memory:" database, so the
// failure rate is a function of Go's scheduler and a single run cannot
// reliably reproduce it. Asserting the setting makes the guarantee
// deterministic and says what it is for.
func TestOpenTestDB_LimitsThePoolToOneConnection(t *testing.T) {
	gdb := openTestDB(t)
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("raw db: %v", err)
	}
	if got := sqlDB.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("MaxOpenConnections = %d, want 1: a second pooled connection "+
			"would see its own empty in-memory database", got)
	}
}
