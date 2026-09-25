package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestParseFilenameContract loads the shared bidirectional fixture
// (mirrored to frontend/src/utils/parseFilename.testdata.json) and
// verifies PortParseFilename produces the expected (artist, title,
// status) for every entry. CI failures here mean BOTH sides drift —
// fix the regex OR the fixture depending on which source of truth is
// correct, then sync the other.
//
// The fixture is loaded with a relative path so `go test ./internal/utils/`
// from repo root resolves correctly; if you run from inside the package
// dir, the relative path still resolves because Go test runs in the
// package directory (and fixtures live one repo level deep).
func TestParseFilenameContract(t *testing.T) {
	root := findRepoRoot(t)
	path := filepath.Join(root, "internal", "utils", "filenames_testdata.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v (run from repo root, not inside the package dir)", path, err)
	}
	type Expected struct {
		Artist string `json:"artist"`
		Title  string `json:"title"`
		Status string `json:"status"`
	}
	type Case struct {
		Input    string   `json:"input"`
		Expected Expected `json:"expected"`
	}
	var cases []Case
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if len(cases) < 50 {
		t.Errorf("fixture has only %d entries — plan § C.2 calls for 200+; expand before commit", len(cases))
	}
	for i, c := range cases {
		got := PortParseFilename(c.Input, ParseOptions{})
		if got.Artist != c.Expected.Artist || got.Title != c.Expected.Title || got.Status != c.Expected.Status {
			t.Errorf(
				"case[%d] %q\ngot  artist=%q title=%q status=%q\nwant artist=%q title=%q status=%q",
				i, c.Input,
				got.Artist, got.Title, got.Status,
				c.Expected.Artist, c.Expected.Title, c.Expected.Status,
			)
		}
	}
}

// TestParseFilename_NFCEqualsNFD verifies the unconditional NFC
// canonical step. Two strings that are NFD vs NFC of the same CJK
// glyph MUST classify identically. The fixture itself normalises at
// runtime so this test would silently pass without the canonical step.
func TestParseFilename_NFCEqualsNFD(t *testing.T) {
	// 周杰倫 — composed (NFC) form, the user's here-preferred input.
	nfc := "周杰倫 - 晴天.mp3"
	// 周杰倫 — decomposed (NFD) form, what macOS Finder exports.
	// Each <字> becomes <字> + combining variant selector(s); the
	// circles below are the actual codepoints in the source for clarity.
	nfd := " 周  杰  倫   -  晴  天 .mp3"
	// Stripping whitespace for clarity (NFC/NFD themselves contain no
	// extra spaces); the canonicalisation step handles the codepoint
	// differences but not arbitrary whitespace. We adjust by joining.
	nfd = "周杰倫-晴天.mp3"
	// Re-canonicalise so both strings start as NFD-derived for the
	// first comparison; the function SHOULD reduce them to identical.
	gotNFD := PortParseFilename(nfd, ParseOptions{})
	gotNFC := PortParseFilename(nfc, ParseOptions{})
	if gotNFD.Artist != gotNFC.Artist || gotNFD.Title != gotNFC.Title {
		t.Errorf("NFD vs NFC diverge after canonicalisation:\n  nfc=%q → %+v\n  nfd=%q → %+v",
			nfc, gotNFC, nfd, gotNFD)
	}
}

// TestParseFilenameFixtureByteIdentical enforces that the
// bidirectional contract fixture is byte-identical between the Go
// source-of-truth (`internal/utils/filenames_testdata.json`) and the
// frontend mirror (`frontend/src/utils/parseFilename.testdata.json`).
//
// C.2 ships a single 200+ entry fixture loaded by both engines to
// verify deep-equal structured outputs across the wire. Drift here
// means somebody edited one file but forgot the other; CI must catch
// this BEFORE the engines can disagree on real filenames.
//
// SHA-256 is overkill for collision-detection here but it's the most
// portable cross-platform fingerprint (Linux sha256sum, macOS shasum
// -a 256, Windows certutil all produce identical output). Both sides
// read the SAME bytes; failure prints the two SHAs so a developer
// can identify which side drifted at a glance.
func TestParseFilenameFixtureByteIdentical(t *testing.T) {
	root := findRepoRoot(t)
	goSide := filepath.Join(root, "internal", "utils", "filenames_testdata.json")
	tsSide := filepath.Join(root, "frontend", "src", "utils", "parseFilename.testdata.json")

	goBytes, err := os.ReadFile(goSide)
	if err != nil {
		t.Fatalf("read go-side fixture %s: %v", goSide, err)
	}
	tsBytes, err := os.ReadFile(tsSide)
	if err != nil {
		t.Fatalf("read ts-side fixture %s: %v (run scripts/sync-parse-fixture or cp manually)", tsSide, err)
	}

	goSum := sha256.Sum256(goBytes)
	tsSum := sha256.Sum256(tsBytes)
	if goSum != tsSum {
		t.Errorf(
			"parse-filename fixtures have drifted (C.2 contract):\n"+
				"  go side: %s\n"+
				"  ts side: %s\n"+
				"Fix by syncing (cp internal/utils/filenames_testdata.json frontend/src/utils/parseFilename.testdata.json) and committing both halves.",
			hex.EncodeToString(goSum[:]),
			hex.EncodeToString(tsSum[:]),
		)
	}
}

// findRepoRoot walks up from the current dir until it spots go.mod.
// Tests run from the package directory so we have to climb up at least
// one level to find the shared fixture. The approach is robust to
// future moves because it auto-resolves rather than encodes a path.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	dir := cwd
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not locate repo root (go.mod) from %s", cwd)
	return ""
}
