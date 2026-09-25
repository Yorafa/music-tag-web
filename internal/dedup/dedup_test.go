package dedup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// music writes a file with the given content under a fresh library root and
// returns the absolute path.
func music(t *testing.T, root, rel, content string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func newChecker(t *testing.T, root string) *Checker {
	t.Helper()
	return New(nil, root)
}

// TestCheck_RejectsBadInput covers the two entry guards. Both answer
// VerdictError rather than VerdictUnique, which matters: a caller that
// treats "unique" as "safe to write" would write a file it never checked.
func TestCheck_RejectsBadInput(t *testing.T) {
	c := newChecker(t, t.TempDir())
	ctx := context.Background()

	if got := c.Check(ctx, "", Options{}); got.Verdict != VerdictError || got.Err == nil {
		t.Errorf("Check(\"\") = %+v, want VerdictError with an Err", got)
	}
	missing := filepath.Join(c.musicRoot, "nope.mp3")
	got := c.Check(ctx, missing, Options{})
	if got.Verdict != VerdictError || got.Err == nil {
		t.Errorf("Check(missing) = %+v, want VerdictError with an Err", got)
	}
	if !strings.Contains(got.Err.Error(), "stat") {
		t.Errorf("Err = %v, want it to mention the failed stat", got.Err)
	}
}

// TestCheck_SameNameInAnotherFolder is the filename stage, the cheapest and
// most decisive one. Same basename under a different album directory is the
// normal shape of a duplicate in a tagged library.
func TestCheck_SameNameInAnotherFolder(t *testing.T) {
	root := t.TempDir()
	mine := music(t, root, "Artist A/Album 1/track.mp3", "content-a")
	other := music(t, root, "Artist B/Album 2/track.mp3", "totally different bytes")

	got := newChecker(t, root).Check(context.Background(), mine, Options{})
	if got.Verdict != VerdictDuplicate {
		t.Fatalf("Verdict = %q, want %q (Reason: %s)", got.Verdict, VerdictDuplicate, got.Reason)
	}
	if got.MatchField != stageFilename {
		t.Errorf("MatchField = %q, want %q", got.MatchField, stageFilename)
	}
	// The reported path is relative to the library root, which is what the
	// UI shows and what the operator can act on.
	want := filepath.ToSlash("Artist B/Album 2/track.mp3")
	if got.DuplicatePath != want {
		t.Errorf("DuplicatePath = %q, want %q", got.DuplicatePath, want)
	}
	if got.Reason == "" {
		t.Error("Reason is empty; the UI renders this string")
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(got.DuplicatePath))); err != nil {
		t.Errorf("DuplicatePath does not resolve under the root: %v", err)
	}
	_ = other
}

// TestCheck_NeverMatchesItself is the property the whole thing hangs on: a
// file checked against its own library must not be reported as its own
// duplicate, or every tag write would be refused.
func TestCheck_NeverMatchesItself(t *testing.T) {
	root := t.TempDir()
	only := music(t, root, "Artist/Album/track.mp3", "content")

	got := newChecker(t, root).Check(context.Background(), only, Options{})
	if got.Verdict == VerdictDuplicate || got.Verdict == VerdictLikelyDuplicate {
		t.Fatalf("a file matched itself: %+v", got)
	}
	if got.Verdict != VerdictUnique {
		t.Errorf("Verdict = %q, want %q (Err: %v)", got.Verdict, VerdictUnique, got.Err)
	}
	if got.DuplicatePath != "" {
		t.Errorf("DuplicatePath = %q on a unique verdict", got.DuplicatePath)
	}
}

// TestCheck_IdenticalContentDifferentName exercises the SHA-256 stage: a
// re-encoded or re-downloaded copy that no filename check would catch.
func TestCheck_IdenticalContentDifferentName(t *testing.T) {
	root := t.TempDir()
	const content = "the exact same bytes in both files, long enough to be worth hashing twice"
	mine := music(t, root, "Artist/Album/new-name.mp3", content)
	music(t, root, "Somewhere Else/old-name.mp3", content)

	got := newChecker(t, root).Check(context.Background(), mine, Options{})
	if got.Verdict != VerdictDuplicate {
		t.Fatalf("Verdict = %q, want %q (Reason: %s)", got.Verdict, VerdictDuplicate, got.Reason)
	}
	if got.MatchField != stageHash {
		t.Errorf("MatchField = %q, want %q", got.MatchField, stageHash)
	}
	if !strings.Contains(got.DuplicatePath, "old-name.mp3") {
		t.Errorf("DuplicatePath = %q, want the identically-hashed file", got.DuplicatePath)
	}
}

// TestCheck_DifferentContentIsUnique is the negative control for the hash
// stage: same size, different bytes must NOT be a duplicate. This is the
// failure mode a size-only comparison would have.
func TestCheck_DifferentContentIsUnique(t *testing.T) {
	root := t.TempDir()
	// Exactly equal lengths, entirely different bytes.
	mine := music(t, root, "Artist/Album/a.mp3", "aaaaaaaaaa")
	music(t, root, "Artist/Album/b.mp3", "bbbbbbbbbb")

	got := newChecker(t, root).Check(context.Background(), mine, Options{})
	if got.Verdict != VerdictUnique {
		t.Errorf("Verdict = %q, want %q — equal size is not equal content (MatchField %q)",
			got.Verdict, VerdictUnique, got.MatchField)
	}
}

// TestCheck_EmptyFileIsNotHashed pins the 0-byte exemption: every empty
// file in a library has the same size, so without it every placeholder
// would be flagged as a byte-identical duplicate of every other one.
func TestCheck_EmptyFileIsNotHashed(t *testing.T) {
	root := t.TempDir()
	mine := music(t, root, "Artist/Album/empty1.mp3", "")
	music(t, root, "Artist/Album/empty2.mp3", "")

	got := newChecker(t, root).Check(context.Background(), mine, Options{})
	if got.MatchField == stageHash {
		t.Errorf("a 0-byte file was matched by content hash: %+v", got)
	}
}

// TestCheck_RecordsWhichStagesRan: Run is what the UI shows as "checked
// by", and an option that silently does nothing looks identical to one that
// worked. Each Disable* flag must remove its stage from the list.
func TestCheck_RecordsWhichStagesRan(t *testing.T) {
	root := t.TempDir()
	mine := music(t, root, "Artist/Album/track.mp3", "content")

	t.Run("filename and meta run by default", func(t *testing.T) {
		got := newChecker(t, root).Check(context.Background(), mine, Options{})
		if got.Verdict != VerdictUnique {
			t.Fatalf("Verdict = %q, want unique so every stage runs", got.Verdict)
		}
		want := []string{stageFilename, stageHash, stageMeta}
		if strings.Join(got.Run, ",") != strings.Join(want, ",") {
			t.Errorf("Run = %v, want %v", got.Run, want)
		}
	})
	t.Run("DisableHash drops the hash stage", func(t *testing.T) {
		got := newChecker(t, root).Check(context.Background(), mine, Options{DisableHash: true})
		if contains(got.Run, stageHash) {
			t.Errorf("Run = %v, want no %q", got.Run, stageHash)
		}
		if !contains(got.Run, stageFilename) {
			t.Errorf("Run = %v, want %q to still run", got.Run, stageFilename)
		}
	})
	t.Run("DisableMeta drops the meta stage", func(t *testing.T) {
		got := newChecker(t, root).Check(context.Background(), mine, Options{DisableMeta: true})
		if contains(got.Run, stageMeta) {
			t.Errorf("Run = %v, want no %q", got.Run, stageMeta)
		}
	})
	t.Run("DisableFingerprint drops the fingerprint stage", func(t *testing.T) {
		got := newChecker(t, root).Check(context.Background(), mine, Options{DisableFingerprint: true})
		if contains(got.Run, stageFingerprint) {
			t.Errorf("Run = %v, want no %q", got.Run, stageFingerprint)
		}
	})
}

// TestCheck_DisableHashActuallyDisablesDetection: a caller turning the
// expensive stage off (a large batch import) must not still be charged for
// it, and must not still be blocked by it.
func TestCheck_DisableHashActuallyDisablesDetection(t *testing.T) {
	root := t.TempDir()
	const content = "identical bytes, different names, on purpose"
	mine := music(t, root, "Artist/Album/one.mp3", content)
	music(t, root, "Elsewhere/two.mp3", content)

	withHash := newChecker(t, root).Check(context.Background(), mine, Options{})
	if withHash.MatchField != stageHash {
		t.Fatalf("setup: expected a hash match, got %+v", withHash)
	}
	without := newChecker(t, root).Check(context.Background(), mine, Options{DisableHash: true})
	if without.MatchField == stageHash {
		t.Errorf("DisableHash still reported a hash duplicate: %+v", without)
	}
}

// TestNew_DefaultsToMusicRootEnv pins the root resolution. Getting this
// wrong silently changes what "duplicate" means — relative to / instead of
// the operator's library.
func TestNew_DefaultsToMusicRootEnv(t *testing.T) {
	t.Setenv("MUSIC_DIR", "/tmp/some-library")
	if got := New(nil, "").musicRoot; got != "/tmp/some-library" {
		t.Errorf("musicRoot = %q, want /tmp/some-library", got)
	}
	t.Setenv("MUSIC_DIR", "")
	if got := New(nil, "").musicRoot; got != "/app/media" {
		t.Errorf("musicRoot = %q, want the /app/media default", got)
	}
	// An explicit root always wins over the env.
	if got := New(nil, "/explicit").musicRoot; got != "/explicit" {
		t.Errorf("musicRoot = %q, want /explicit", got)
	}
}

// ─── pure helpers ───────────────────────────────────────────────────────────

func TestMetaSimilarity(t *testing.T) {
	cases := []struct {
		name string
		a, b metaRecord
		want float64
	}{
		{
			name: "identical is 1.0",
			a:    metaRecord{Title: "t", Artist: "a", Album: "al", Duration: 100},
			b:    metaRecord{Title: "t", Artist: "a", Album: "al", Duration: 100},
			want: 1.0,
		},
		{
			name: "same duration adds its bonus",
			a:    metaRecord{Title: "t", Artist: "a", Album: "al", Duration: 100},
			b:    metaRecord{Title: "t", Artist: "a", Album: "al", Duration: 101.5},
			want: 1.0, // capped
		},
		{
			name: "title only is 0.5",
			a:    metaRecord{Title: "t", Artist: "a", Album: "al"},
			b:    metaRecord{Title: "t", Artist: "x", Album: "zz"},
			want: 0.5,
		},
		{
			name: "nothing shared is 0",
			a:    metaRecord{Title: "t", Artist: "a", Album: "al"},
			b:    metaRecord{Title: "q", Artist: "x", Album: "zz"},
			want: 0,
		},
		{
			name: "empty subject cannot match on empty fields",
			a:    metaRecord{},
			b:    metaRecord{Title: "q"},
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := metaSimilarity(tc.a, tc.b); got != tc.want {
				t.Errorf("metaSimilarity = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestMetaSimilarity_DurationIsANoOpWhenEveryFieldMatches pins the scoring
// as it actually is, because the source reads as if duration carried
// weight. It does not: the three field weights already sum to 1.0, so two
// records agreeing on title, artist AND album score 1.0 whether the
// durations are 1s or 300s apart.
//
// That means a live version and its studio cut pair up as a likely
// duplicate — which for a music library is the behaviour you want, and is
// why the "obvious" fix (subtracting on a large gap) is not applied: at
// 0.95 it would not change a single verdict. The duration term only moves
// partial matches, which is what this asserts.
func TestMetaSimilarity_DurationIsANoOpWhenEveryFieldMatches(t *testing.T) {
	near := metaSimilarity(
		metaRecord{Title: "t", Artist: "a", Album: "al", Duration: 100},
		metaRecord{Title: "t", Artist: "a", Album: "al", Duration: 101},
	)
	far := metaSimilarity(
		metaRecord{Title: "t", Artist: "a", Album: "al", Duration: 100},
		metaRecord{Title: "t", Artist: "a", Album: "al", Duration: 400},
	)
	if near != 1.0 || far != 1.0 {
		t.Errorf("full field match: near=%v far=%v, want 1.0 for both", near, far)
	}

	// Title-only: the duration agreement is the only thing that can lift it.
	titleOnlyNoDur := metaSimilarity(
		metaRecord{Title: "t", Artist: "a", Album: "al"},
		metaRecord{Title: "t", Artist: "x", Album: "zz"},
	)
	titleOnlyNear := metaSimilarity(
		metaRecord{Title: "t", Artist: "a", Album: "al", Duration: 100},
		metaRecord{Title: "t", Artist: "x", Album: "zz", Duration: 101},
	)
	if titleOnlyNear <= titleOnlyNoDur {
		t.Errorf("a matching duration did not raise a partial score: %v vs %v",
			titleOnlyNear, titleOnlyNoDur)
	}
	// And a wildly different duration on a partial match still stays under
	// the likely-duplicate threshold, so an unrelated track with a similar
	// title is not reported.
	titleOnlyFar := metaSimilarity(
		metaRecord{Title: "t", Artist: "a", Album: "al", Duration: 100},
		metaRecord{Title: "t", Artist: "x", Album: "zz", Duration: 400},
	)
	if titleOnlyFar >= 0.7 {
		t.Errorf("partial match with a 300s duration gap scored %v; it would be a likely duplicate", titleOnlyFar)
	}
}

func TestRelOrSelf(t *testing.T) {
	cases := []struct {
		full, root, want string
	}{
		{"/lib/a/b.mp3", "/lib", "a/b.mp3"},
		{"/lib/b.mp3", "/lib", "b.mp3"},
		{"/lib", "/lib", "."},
		// Outside the root after cleaning, so it is reported absolute
		// rather than as a "../" chain the UI would render as a broken
		// relative path.
		{"/elsewhere/b.mp3", "/lib", "/elsewhere/b.mp3"},
		// No root at all.
		{"/lib/b.mp3", "", "/lib/b.mp3"},
	}
	for _, tc := range cases {
		if got := relOrSelf(tc.full, tc.root); got != tc.want {
			t.Errorf("relOrSelf(%q, %q) = %q, want %q", tc.full, tc.root, got, tc.want)
		}
	}
}

func TestExtractJSONField(t *testing.T) {
	// The real fpcalc -json shape.
	s := `{"duration":187.123,"fingerprint":"AQABtMm0Xa...=="}`
	if got := extractJSONField(s, "fingerprint"); got != "AQABtMm0Xa...==" {
		t.Errorf("fingerprint = %q", got)
	}
	if got := extractJSONField(s, "duration"); got != "187.123" {
		t.Errorf("duration = %q, want 187.123", got)
	}
	if got := extractJSONField(s, "missing"); got != "" {
		t.Errorf("missing key = %q, want empty", got)
	}
	if got := extractJSONField("", "duration"); got != "" {
		t.Errorf("empty input = %q", got)
	}
	// A key that is present but has no value separator must not read past
	// the end of the document.
	if got := extractJSONField(`{"duration"`, "duration"); got != "" {
		t.Errorf("truncated input = %q, want empty", got)
	}
	// Negative and integer durations.
	if got := extractJSONField(`{"duration":-1}`, "duration"); got != "-1" {
		t.Errorf("negative duration = %q", got)
	}
}

func TestNormLower(t *testing.T) {
	if got := normLower("  MiXeD Case \t"); got != "mixed case" {
		t.Errorf("normLower = %q", got)
	}
}

// TestFindHelpers walks a small tree and pins the three filesystem scans the
// checker falls back to when there is no DB.
func TestFindHelpers(t *testing.T) {
	root := t.TempDir()
	music(t, root, "a/target.mp3", "0123456789")     // 10 bytes
	music(t, root, "b/other.mp3", "0123456789")      // 10 bytes
	music(t, root, "c/bigger.mp3", "0123456789ab")   // 12 bytes
	music(t, root, "d/target.mp3", "different body") // same name, elsewhere

	byName, err := findFilesByName(root, "target.mp3")
	if err != nil {
		t.Fatalf("findFilesByName: %v", err)
	}
	if len(byName) != 2 {
		t.Errorf("findFilesByName found %d files (%v), want both target.mp3 copies", len(byName), byName)
	}

	bySize, err := findFilesBySize(root, 10, 1024)
	if err != nil {
		t.Fatalf("findFilesBySize: %v", err)
	}
	if len(bySize) != 2 {
		t.Errorf("findFilesBySize(10) found %d files (%v), want 2", len(bySize), bySize)
	}

	byRange, err := findFilesBySizeBetween(root, 11, 12, 1024)
	if err != nil {
		t.Fatalf("findFilesBySizeBetween: %v", err)
	}
	if len(byRange) != 1 || filepath.Base(byRange[0]) != "bigger.mp3" {
		t.Errorf("findFilesBySizeBetween(11,12) = %v, want just bigger.mp3", byRange)
	}

	// The limit is honoured, so a large library cannot be turned into one
	// unbounded allocation.
	limited, err := findFilesBySize(root, 10, 1)
	if err != nil {
		t.Fatalf("findFilesBySize with a limit: %v", err)
	}
	if len(limited) != 1 {
		t.Errorf("findFilesBySize with limit 1 returned %d files", len(limited))
	}
}

// TestFindFiles_MissingRootIsAnErrorNotASilentEmpty: the checker ignores
// the error and treats "no candidates" as "no duplicate", so an unreadable
// root would otherwise downgrade to VerdictUnique with nothing logged.
func TestFindFiles_MissingRootIsAnErrorNotASilentEmpty(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := findFilesByName(missing, "x.mp3"); err == nil {
		t.Error("findFilesByName on a missing root returned no error")
	}
	if _, err := findFilesBySize(missing, 1, 10); err == nil {
		t.Error("findFilesBySize on a missing root returned no error")
	}
	if _, err := findFilesBySizeBetween(missing, 0, 1, 10); err == nil {
		t.Error("findFilesBySizeBetween on a missing root returned no error")
	}
}

func TestSHA256OfFile(t *testing.T) {
	root := t.TempDir()
	const content = "hash me"
	p := music(t, root, "x.bin", content)

	sum := sha256.Sum256([]byte(content))
	if got, err := sha256OfFile(p); err != nil || got != hex.EncodeToString(sum[:]) {
		t.Errorf("sha256OfFile = %q, %v; want %q", got, err, hex.EncodeToString(sum[:]))
	}
	if _, err := sha256OfFile(filepath.Join(root, "nope")); err == nil {
		t.Error("sha256OfFile on a missing file returned no error")
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
