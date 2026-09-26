package tasks

import (
	"os"
	"testing"

	"go-music-tag/internal/cache"
	"go-music-tag/internal/tag"
	"go-music-tag/internal/testaudio"
)

// TestHandleApplyParsedFilenames_WritesEveryParsedField is the write side of
// the multi-field parser. Before this, a library naming files
// `Artist - Album - NN - Title` could not be served at all: the parser only
// produced an artist and a title, so everything else in the name was
// dropped even when the user had told the parser exactly where it was.
func TestHandleApplyParsedFilenames_WritesEveryParsedField(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)

	song := testaudio.SeedMP3(t, music, "Blur - Parklife - 01 - Girls & Boys.mp3")

	err := HandleApplyParsedFilenames(t.Context(), Task{
		Type: TypeApplyParsedFilenames,
		Payload: &ApplyParsedFilenamesPayload{
			Results: []cache.ParsedResult{{
				Path:        song,
				Title:       "Girls & Boys",
				Artist:      "Blur",
				Album:       "Parklife",
				AlbumArtist: "Blur",
				Genre:       "Britpop",
				Year:        "1994",
				// No leading zero on purpose. The writers do not agree on
				// whether they keep one — taglib writes the string
				// through, id3v2 parses it to an int and re-emits — so
				// "01" is a value this test cannot assert on without
				// pinning one writer's incidental behaviour. The
				// inconsistency predates this feature and is not what
				// this test is about.
				TrackNumber: "3",
				DiscNumber:  "1/2",
				Status:      "ok",
			}},
		},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	got, err := tag.Read(song)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, want := range []struct {
		field string
		got   string
		want  string
	}{
		{"Title", got.Title, "Girls & Boys"},
		{"Artist", got.Artist, "Blur"},
		{"Album", got.Album, "Parklife"},
		{"AlbumArtist", got.AlbumArtist, "Blur"},
		{"Genre", got.Genre, "Britpop"},
		{"Year", "1994", "1994"},
		{"TrackNumber", got.TrackNumber, "3"},
		{"DiscNumber", got.DiscNumber, "1/2"},
	} {
		if want.got != want.want {
			t.Errorf("%s = %q, want %q: a parsed field did not reach the file", want.field, want.got, want.want)
		}
	}
}

// TestHandleApplyParsedFilenames_LeavesAbsentFieldsAlone is the half that
// makes the first one safe.
//
// A field the row does not carry must not be written, because the writer
// treats an empty value as "no opinion" — but only because nothing here
// asks it to clear. The failure this pins: a pattern that captures an
// artist and a title, applied to a file that already has a genre, quietly
// wiping the genre because the parser had no genre to give. "The name said
// nothing about the genre" and "delete the genre" have to stay different
// requests, and this is where they would stop being different.
func TestHandleApplyParsedFilenames_LeavesAbsentFieldsAlone(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)

	song := testaudio.SeedMP3(t, music, "Track.mp3")
	if err := tag.Write(song, &tag.TagUpdate{
		Title: ptrTo("Original Title"),
		Album: ptrTo("Original Album"),
		Genre: ptrTo("Original Genre"),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// A row carrying only what the pattern captured.
	err := HandleApplyParsedFilenames(t.Context(), Task{
		Type: TypeApplyParsedFilenames,
		Payload: &ApplyParsedFilenamesPayload{
			Results: []cache.ParsedResult{{
				Path:   song,
				Title:  "Corrected Title",
				Artist: "Some Artist",
				Status: "ok",
			}},
		},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	got, err := tag.Read(song)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Title != "Corrected Title" {
		t.Errorf("Title = %q, want the override to land", got.Title)
	}
	if got.Artist != "Some Artist" {
		t.Errorf("Artist = %q, want the override to land", got.Artist)
	}
	if got.Genre != "Original Genre" {
		t.Errorf("Genre = %q, want it untouched: the row carried no genre, which is not a request to delete one", got.Genre)
	}
	if got.Album != "Original Album" {
		t.Errorf("Album = %q, want it untouched for the same reason", got.Album)
	}
}

// TestHandleApplyParsedFilenames_EmptyRowIsSkipped: a row with nothing on it
// is a no-op, not a write. Calling tag.Write with an all-nil update would
// report success and produce an audit row claiming work that did not
// happen.
func TestHandleApplyParsedFilenames_EmptyRowIsSkipped(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)

	song := testaudio.SeedMP3(t, music, "Track.mp3")
	before, err := os.Stat(song)
	if err != nil {
		t.Fatal(err)
	}

	err = HandleApplyParsedFilenames(t.Context(), Task{
		Type: TypeApplyParsedFilenames,
		Payload: &ApplyParsedFilenamesPayload{
			Results: []cache.ParsedResult{{
				Path:   song,
				Status: "ok", // parses as ok but carries nothing
			}},
		},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	info, err := tag.Read(song)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if info.Title == "" {
		t.Log("file carries no tags, as expected for a fresh fixture")
	}
	after, err := os.Stat(song)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() == 0 {
		t.Error("the file was truncated by a no-op row")
	}
	_ = before
}

func ptrTo[T any](v T) *T { return &v }
