package audit_test

import (
	"context"
	"testing"

	"go-music-tag/internal/audit"
)

// TestQuery_SearchTreatsWildcardsAsLiterals
//
// The search term is user input bound into a LIKE pattern, so a `%` or `_`
// in it acted as a wildcard: searching the audit log for `100%` matched
// every row, and `_` matched any single character. The value was still a
// bound parameter, so this was never an injection — it was a filter the
// caller could not express.
func TestQuery_SearchTreatsWildcardsAsLiterals(t *testing.T) {
	ctx := context.Background()
	_ = setupTestDB(t)

	// Three rows; only the middle one contains a literal percent sign.
	audit.Log(ctx, audit.ActionUpdateID3, "track-100.mp3", "admin", audit.StatusSuccess, 1, "plain", nil)
	audit.Log(ctx, audit.ActionUpdateID3, "battery-100%.mp3", "admin", audit.StatusSuccess, 1, "plain", nil)
	audit.Log(ctx, audit.ActionUpdateID3, "unrelated.mp3", "admin", audit.StatusSuccess, 1, "plain", nil)

	rows, total, err := audit.Query(ctx, audit.QueryOptions{Page: 1, PageSize: 10, Search: "100%"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("search %q returned %d rows (total %d), want only the literal match", "100%", len(rows), total)
	}
	if rows[0].Target != "battery-100%.mp3" {
		t.Errorf("matched %q, want battery-100%%.mp3", rows[0].Target)
	}

	// `%` on its own must match the one row that literally contains a
	// percent sign, not all three rows. (A `%`-matching-everything
	// pattern is what the unescaped version produced here.)
	rows, total, err = audit.Query(ctx, audit.QueryOptions{Page: 1, PageSize: 10, Search: "%"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if total != 1 || len(rows) != 1 || rows[0].Target != "battery-100%.mp3" {
		t.Fatalf("search %q returned %d rows (total %d): %+v", "%", len(rows), total, rows)
	}

	// `_` must be a literal underscore, not "any character": neither
	// "track-100.mp3" nor "unrelated.mp3" contains one.
	_, total, err = audit.Query(ctx, audit.QueryOptions{Page: 1, PageSize: 10, Search: "_"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if total != 0 {
		t.Errorf("search %q matched %d rows; _ must be a literal", "_", total)
	}
}

// TestQuery_SearchFindsUnderscoreTarget is the positive control for the
// case above, so a Query that escaped everything into uselessness would
// fail rather than pass.
func TestQuery_SearchFindsUnderscoreTarget(t *testing.T) {
	ctx := context.Background()
	_ = setupTestDB(t)

	audit.Log(ctx, audit.ActionUpdateID3, "no_underscore_here.mp3", "admin", audit.StatusSuccess, 1, "plain", nil)
	audit.Log(ctx, audit.ActionUpdateID3, "plain.mp3", "admin", audit.StatusSuccess, 1, "plain", nil)

	rows, total, err := audit.Query(ctx, audit.QueryOptions{Page: 1, PageSize: 10, Search: "_"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if total != 1 || len(rows) != 1 || rows[0].Target != "no_underscore_here.mp3" {
		t.Fatalf("search %q returned %d rows (total %d): %+v", "_", len(rows), total, rows)
	}
}

// TestQuery_SearchEscapeCharIsLiteral covers the third case: the escape
// character itself has to be escaped, or searching for a literal `!`
// either errors or swallows the next character.
func TestQuery_SearchEscapeCharIsLiteral(t *testing.T) {
	ctx := context.Background()
	_ = setupTestDB(t)

	audit.Log(ctx, audit.ActionUpdateID3, "wow!file.mp3", "admin", audit.StatusSuccess, 1, "plain", nil)
	audit.Log(ctx, audit.ActionUpdateID3, "wowXfile.mp3", "admin", audit.StatusSuccess, 1, "plain", nil)

	rows, total, err := audit.Query(ctx, audit.QueryOptions{Page: 1, PageSize: 10, Search: "wow!"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("search %q returned %d rows (total %d), want 1", "wow!", len(rows), total)
	}
	if rows[0].Target != "wow!file.mp3" {
		t.Errorf("matched %q, want wow!file.mp3", rows[0].Target)
	}
}

// TestQuery_SearchWildcardMatchesNothing is the negative control: with no
// row containing the metacharacter, searching for it must return nothing.
// Unescaped, `%` returns the whole table and `_` returns every row with at
// least one character.
func TestQuery_SearchWildcardMatchesNothing(t *testing.T) {
	ctx := context.Background()
	_ = setupTestDB(t)

	audit.Log(ctx, audit.ActionUpdateID3, "track-100.mp3", "admin", audit.StatusSuccess, 1, "plain", nil)
	audit.Log(ctx, audit.ActionUpdateID3, "unrelated.mp3", "admin", audit.StatusSuccess, 1, "plain", nil)

	for _, term := range []string{"%", "_"} {
		_, total, err := audit.Query(ctx, audit.QueryOptions{Page: 1, PageSize: 10, Search: term})
		if err != nil {
			t.Fatalf("Query(%q): %v", term, err)
		}
		if total != 0 {
			t.Errorf("search %q matched %d of 2 rows; the metacharacter must be literal", term, total)
		}
	}
}
