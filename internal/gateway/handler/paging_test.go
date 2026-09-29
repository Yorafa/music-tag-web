package handler

import (
	"strings"
	"testing"
)

// TestClampPaging pins the two bounds every list endpoint shares.
// Unclamped, page_size reaches Limit() as given, and a large page overflows
// (page-1)*pageSize into a negative offset — which GORM renders as
// "no LIMIT", i.e. the whole table.
func TestClampPaging(t *testing.T) {
	cases := []struct {
		name             string
		page, size, def  int
		wantPage, wantSz int
	}{
		{"defaults on zero", 0, 0, 50, 1, 50},
		{"negative page clamped to 1", -5, 20, 50, 1, 20},
		{"negative size uses default", 1, -1, 50, 1, 50},
		{"oversized size capped at 100", 1, 99999999, 50, 1, 100},
		{"absurd page capped", 999999999, 50, 50, maxPage, 50},
		{"in-range values pass through", 3, 25, 50, 3, 25},
		{"size exactly at cap allowed", 1, maxPageSize, 50, 1, maxPageSize},
		{"size one over cap clamped", 1, maxPageSize + 1, 50, 1, maxPageSize},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, s := clampPaging(tc.page, tc.size, tc.def)
			if p != tc.wantPage || s != tc.wantSz {
				t.Errorf("clampPaging(%d, %d, %d) = (%d, %d), want (%d, %d)",
					tc.page, tc.size, tc.def, p, s, tc.wantPage, tc.wantSz)
			}
			// The whole point: offset must never go negative, or GORM
			// drops the LIMIT entirely.
			if offset := (p - 1) * s; offset < 0 {
				t.Errorf("offset=(%d-1)*%d = %d, must stay >= 0", p, s, offset)
			}
		})
	}
}

// TestAtoiOrRejectsOverflowInput pins the digit-count bound. The old loop
// had no ceiling, so a long enough numeric string wrapped int and could
// produce a negative page.
func TestAtoiOrRejectsOverflowInput(t *testing.T) {
	const fallback = 7
	cases := []struct {
		name, in string
		want     int
	}{
		{"empty uses fallback", "", fallback},
		{"non-numeric uses fallback", "12a", fallback},
		{"negative uses fallback", "-3", fallback},
		{"normal parses", "42", 42},
		{"nine digits still parses", "999999999", 999999999},
		// 10+ digits: would overflow int on a 32-bit platform and is
		// meaningless as a page number regardless.
		{"ten digits rejected", "9999999999", fallback},
		{"absurdly long rejected", strings.Repeat("9", 64), fallback},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := atoiOr(tc.in, fallback)
			if got != tc.want {
				t.Errorf("atoiOr(%q, %d) = %d, want %d", tc.in, fallback, got, tc.want)
			}
			if got < 0 {
				t.Errorf("atoiOr(%q) produced a negative value %d", tc.in, got)
			}
		})
	}
}
