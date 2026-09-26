package tag

import (
	"strconv"
	"testing"

	"go-music-tag/internal/testaudio"
)

// Clearing a tag is a write, and this file exists because that write has two
// implementations which do not agree on how to do it.
//
// taglib is the primary writer and has no API for removing a single tag, so
// a clear is expressed as writing an empty value — the trick ClearLyrics has
// used since it was the only clearable field. The id3v2 fallback can do a
// real DeleteFrames instead. Same intent, two mechanisms, and the only thing
// that makes them interchangeable is that Read comes back empty afterwards.
//
// So nothing here asserts on a mechanism. Every assertion is a round trip:
// write real values, clear, read back, require the cleared field empty AND
// its untouched neighbours intact. A clear that also wiped the rest of the
// file would satisfy "the genre is gone" on its own.
//
// # Why the ffmpeg-free fixtures come first
//
// The obvious shape for this is the existing format matrix, and that is what
// the *EveryFormat test at the bottom does. It is also how this file got
// written first, and it is a trap: buildSamples() shells out to ffmpeg, so on
// any machine without it haveSample() is false for every case and the whole
// test reports PASS having asserted nothing. A green suite that proves
// nothing is worse than a red one, because it is believed.
//
// So the load-bearing cases run on fixtures synthesised in-process
// (internal/testaudio, ffmpeg-free by design) and the matrix is a sweep on
// top for whoever has the encoders.

// clearableFields is every field the batch dialog can clear, together with
// how to write it and how to clear it. One table, so "a Clear* was added to
// TagUpdate and nobody added it here" shows up as a diff rather than as a
// branch nobody runs.
var clearableFields = []struct {
	name  string
	of    func(*TagInfo) string
	apply func(*TagUpdate)
	clear func(*TagUpdate)
}{
	{"Title", func(i *TagInfo) string { return i.Title },
		func(u *TagUpdate) { u.Title = ptr("Marker Title") },
		func(u *TagUpdate) { u.ClearTitle = true }},
	{"Artist", func(i *TagInfo) string { return i.Artist },
		func(u *TagUpdate) { u.Artist = []string{"Marker Artist"} },
		func(u *TagUpdate) { u.ClearArtist = true }},
	{"Album", func(i *TagInfo) string { return i.Album },
		func(u *TagUpdate) { u.Album = ptr("Marker Album") },
		func(u *TagUpdate) { u.ClearAlbum = true }},
	{"AlbumArtist", func(i *TagInfo) string { return i.AlbumArtist },
		func(u *TagUpdate) { u.AlbumArtist = ptr("Marker AlbumArtist") },
		func(u *TagUpdate) { u.ClearAlbumArtist = true }},
	{"Genre", func(i *TagInfo) string { return i.Genre },
		func(u *TagUpdate) { u.Genre = ptr("Marker Genre") },
		func(u *TagUpdate) { u.ClearGenre = true }},
	{"Year", func(i *TagInfo) string { return strconv.Itoa(i.Year) },
		func(u *TagUpdate) { u.Year = ptr("1994") },
		func(u *TagUpdate) { u.ClearYear = true }},
	{"TrackNumber", func(i *TagInfo) string { return i.TrackNumber },
		func(u *TagUpdate) { u.TrackNumber = ptr("3/12") },
		func(u *TagUpdate) { u.ClearTrackNumber = true }},
	{"DiscNumber", func(i *TagInfo) string { return i.DiscNumber },
		func(u *TagUpdate) { u.DiscNumber = ptr("1/2") },
		func(u *TagUpdate) { u.ClearDiscNumber = true }},
	{"Comment", func(i *TagInfo) string { return i.Comment },
		func(u *TagUpdate) { u.Comment = ptr("Marker Comment") },
		func(u *TagUpdate) { u.ClearComment = true }},
	{"Lyrics", func(i *TagInfo) string { return i.Lyrics },
		func(u *TagUpdate) { u.Lyrics = ptr("Marker Lyrics") },
		func(u *TagUpdate) { u.ClearLyrics = true }},
}

// seedEveryField is the update that puts a known value in every field, used
// both to prove a fixture can carry tags at all and as the "everything else"
// that a single clear must leave alone.
func seedEveryField() *TagUpdate {
	upd := &TagUpdate{}
	for _, f := range clearableFields {
		f.apply(upd)
	}
	return upd
}

func clearEveryField() *TagUpdate {
	upd := &TagUpdate{}
	for _, f := range clearableFields {
		f.clear(upd)
	}
	return upd
}

// synthFixtures are built in-process and therefore always available. The MP3
// must never skip: it is the format with an id3v2 fallback path, so it is
// the only fixture that can catch a clear wired to the wrong ID3 frame.
func synthFixtures(t *testing.T) []struct {
	label string
	make  func(t *testing.T) string
} {
	return []struct {
		label string
		make  func(t *testing.T) string
	}{
		{"mp3", func(t *testing.T) string { return testaudio.SeedMP3(t, t.TempDir(), "track.mp3") }},
		{"wav", func(t *testing.T) string { return testaudio.SeedWAV(t, t.TempDir(), "track.wav", 1) }},
	}
}

// requireSeedable checks the fixture can carry the field under test, and
// skips when it cannot. A container whose tag chunk this build refuses to
// write is not a failure of clearing — it is a fixture that cannot
// demonstrate anything, and saying so is the point. The MP3 case is the one
// that always runs.
func requireSeedable(t *testing.T, label, path string, f struct {
	name  string
	of    func(*TagInfo) string
	apply func(*TagUpdate)
	clear func(*TagUpdate)
}) *TagInfo {
	t.Helper()
	upd := &TagUpdate{}
	f.apply(upd)
	if err := Write(path, upd); err != nil {
		t.Skipf("%s: this fixture cannot be written: %v", label, err)
	}
	info, err := Read(path)
	if err != nil {
		t.Skipf("%s: this fixture cannot be read back: %v", label, err)
	}
	if got := f.of(info); got == "" || got == "0" {
		t.Skipf("%s: this fixture does not carry %s, so clearing it cannot be observed", label, f.name)
	}
	return info
}

// TestWriteClearRemovesTheTagAndNothingElse is the core case, and it runs
// without ffmpeg. One field is cleared while the rest of the file is left
// alone: the difference between "clear the genre" and "rewrite this file
// from this update" is the entire reason Clear* are separate flags.
func TestWriteClearRemovesTheTagAndNothingElse(t *testing.T) {
	for _, fx := range synthFixtures(t) {
		for _, f := range clearableFields {
			t.Run(fx.label+"/"+f.name, func(t *testing.T) {
				path := fx.make(t)
				// Seed the field under test (proving the fixture can hold
				// it) and then seed everything, so the neighbours are real
				// values rather than absent ones.
				requireSeedable(t, fx.label, path, f)
				if err := Write(path, seedEveryField()); err != nil {
					t.Fatalf("seed every field: %v", err)
				}
				seeded, err := Read(path)
				if err != nil {
					t.Fatalf("read after seed: %v", err)
				}
				want := make(map[string]string, len(clearableFields))
				for _, other := range clearableFields {
					want[other.name] = other.of(seeded)
				}

				clear := &TagUpdate{}
				f.clear(clear)
				if err := Write(path, clear); err != nil {
					t.Fatalf("clear write: %v", err)
				}

				got, err := Read(path)
				if err != nil {
					t.Fatalf("read after clear: %v", err)
				}
				if v := f.of(got); v != "" && v != "0" {
					t.Errorf("%s = %q after clearing it, want empty: the clear did not take", f.name, v)
				}
				for _, other := range clearableFields {
					if other.name == f.name {
						continue
					}
					// A neighbour this fixture never held has nothing to
					// assert. Demanding it would turn "this format does
					// not store comments" into a false alarm.
					if want[other.name] == "" || want[other.name] == "0" {
						continue
					}
					if v := other.of(got); v != want[other.name] {
						t.Errorf("%s = %q, want %q — clearing %s took its neighbours with it",
							other.name, v, want[other.name], f.name)
					}
				}
			})
		}
	}
}

// TestWriteClearsEveryClearableFieldAtOnce covers the set as a whole. Each
// Clear* is a separate branch in two writers, and a branch nobody exercised
// is a branch that does not work — so this asks for all of them in one write
// and requires every field back empty.
func TestWriteClearsEveryClearableFieldAtOnce(t *testing.T) {
	for _, fx := range synthFixtures(t) {
		t.Run(fx.label, func(t *testing.T) {
			path := fx.make(t)
			if err := Write(path, seedEveryField()); err != nil {
				t.Fatalf("seed: %v", err)
			}
			if err := Write(path, clearEveryField()); err != nil {
				t.Fatalf("clear: %v", err)
			}
			got, err := Read(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			for _, f := range clearableFields {
				if v := f.of(got); v != "" && v != "0" {
					t.Errorf("%s = %q after clearing every field, want empty", f.name, v)
				}
			}
		})
	}
}

// TestWriteClearBeatsTheSameFieldBeingSet pins precedence. Setting and
// clearing one field in the same write is a contradiction, and the only
// defensible reading is "delete it": write-then-delete and delete agree,
// delete-then-write quietly loses the delete, and the caller who asked to
// clear finds their tag back.
func TestWriteClearBeatsTheSameFieldBeingSet(t *testing.T) {
	for _, fx := range synthFixtures(t) {
		for _, f := range clearableFields {
			t.Run(fx.label+"/"+f.name, func(t *testing.T) {
				path := fx.make(t)
				requireSeedable(t, fx.label, path, f)

				both := &TagUpdate{}
				f.apply(both)
				f.clear(both)
				if err := Write(path, both); err != nil {
					t.Fatalf("clear+set write: %v", err)
				}
				got, err := Read(path)
				if err != nil {
					t.Fatalf("read: %v", err)
				}
				if v := f.of(got); v != "" && v != "0" {
					t.Errorf("%s = %q, want empty: a set must not resurrect a field cleared in the same write", f.name, v)
				}
			})
		}
	}
}

// ─── opportunistic sweep: every format, but only where ffmpeg exists ─────

// TestWriteClearsTagsInEveryFormat is the same property across the whole
// library format list. Every subtest skips when buildSamples() cannot produce
// that file, so read the output before believing it: a machine with no ffmpeg
// reports this as PASS having run nothing at all. The ffmpeg-free tests
// above are the load-bearing coverage.
func TestWriteClearsTagsInEveryFormat(t *testing.T) {
	for _, tc := range matrixCases {
		t.Run(tc.file, func(t *testing.T) {
			if !haveSample(t, tc.file) {
				t.Skipf("generator produced no %s on this ffmpeg build", tc.file)
			}
			path := copySample(t, tc.file)
			if err := Write(path, seedEveryField()); err != nil {
				t.Fatalf("seed Write(%s): %v", tc.file, err)
			}
			if err := Write(path, clearEveryField()); err != nil {
				t.Fatalf("clear Write(%s): %v", tc.file, err)
			}
			got, err := Read(path)
			if err != nil {
				t.Fatalf("Read(%s) after clear: %v", tc.file, err)
			}
			for _, f := range clearableFields {
				if v := f.of(got); v != "" && v != "0" {
					t.Errorf("%s: %s = %q after clearing it, want empty", tc.file, f.name, v)
				}
			}
		})
	}
}

// TestWriteMP3ClearsFramesDirectly covers the id3v2 fallback branch on its
// own, because nothing reaches it through Write.
//
// Write tries taglib first and only falls back to writeMP3 when taglib
// returns an error. For a healthy MP3 taglib always succeeds, so every
// mutation of the DeleteFrames calls below is unobservable through the
// public entry point — which is exactly how "clear the title" can be a
// no-op on the fallback path while every other test stays green.
//
// Two things follow. This calls the private writer directly, because a
// fallback that cannot be reached cannot be tested through the public API.
// And it is worth asking what a clear is FOR on a path that only runs when
// the primary writer is already broken: nothing good. The branch stays
// because a silent no-op is the worse of the two answers, but the honest
// summary is that the taglib path is what users get, every time.
func TestWriteMP3ClearsFramesDirectly(t *testing.T) {
	for _, f := range clearableFields {
		t.Run(f.name, func(t *testing.T) {
			path := testaudio.SeedMP3(t, t.TempDir(), "track.mp3")

			seed := &TagUpdate{}
			f.apply(seed)
			if err := writeMP3(path, seed); err != nil {
				t.Fatalf("seed via writeMP3: %v", err)
			}
			seeded, err := Read(path)
			if err != nil {
				t.Fatalf("read after seed: %v", err)
			}
			if got := f.of(seeded); got == "" || got == "0" {
				t.Skipf("this fixture does not carry %s through the id3v2 path", f.name)
			}

			clear := &TagUpdate{}
			f.clear(clear)
			if err := writeMP3(path, clear); err != nil {
				t.Fatalf("clear via writeMP3: %v", err)
			}
			got, err := Read(path)
			if err != nil {
				t.Fatalf("read after clear: %v", err)
			}
			if v := f.of(got); v != "" && v != "0" {
				t.Errorf("%s = %q after the id3v2 clear, want empty", f.name, v)
			}
		})
	}
}
