package tag

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// matrixCase describes one container the project claims to support.
type matrixCase struct {
	file string
	// wantCodec is taglib's inner codec where there is one, else its
	// container name. Checked because the old reader guessed from the
	// extension: every OggS file came back as "vorbis" even when it held
	// opus, and ALAC in an m4a came back as "mp4a".
	wantCodec string
}

// Every entry is in audioext.libraryExts, so this table is the read-side
// counterpart of that list. If a format is accepted by the file browser and
// the tag editor, it must appear here.
var matrixCases = []matrixCase{
	{"sample.mp3", "mpeg"},
	{"sample.m4a", "aac"},
	{"sample_alac.m4a", "alac"},
	{"sample.flac", "flac"},
	{"sample.ogg", "vorbis"},
	{"sample.opus", "opus"},
	{"sample.spx", "speex"},
	{"sample.wav", "pcm"},
	{"sample.aiff", "pcm"},
	{"sample.wma", "wma2"},
	{"sample.wv", "wavpack"},
	{"sample.tta", "tta"},
	// ape / mpc / dsf / dff are listed by the generator too but need
	// muxers a stock distro ffmpeg lacks, so they are probed opportunistically
	// by TestFormatMatrixOptionalFormats below.
}

// TestFormatMatrixReadsEveryLibraryFormat is the regression spec for the
// bug that motivated the taglib-primary reader: a .wav/.aiff/.wma/.wv/.tta
// file could not be parsed, so Read invented a title from the filename and
// reported success. Every format below must now return real metadata.
//
// The failure this pins is specifically Title == filename-stem on a file
// that carries no tags. Asserting Title == "" is the load-bearing part:
// it is what distinguishes "parsed, genuinely untagged" from "gave up and
// made something up".
func TestFormatMatrixReadsEveryLibraryFormat(t *testing.T) {
	for _, tc := range matrixCases {
		t.Run(tc.file, func(t *testing.T) {
			if !haveSample(t, tc.file) {
				t.Skipf("generator produced no %s on this ffmpeg build", tc.file)
			}
			path := copySample(t, tc.file)

			info, err := Read(path)
			if err != nil {
				t.Fatalf("Read(%s) failed: %v", tc.file, err)
			}

			// No fabricated title. The sample has zero tag frames, so a
			// parser that worked correctly reports an empty title; the old
			// broken path reported the filename stem ("sample").
			base := strings.TrimSuffix(tc.file, filepath.Ext(tc.file))
			if info.Title == base || strings.HasPrefix(info.Title, base) {
				t.Fatalf("Read(%s) fabricated a title from the filename: %q "+
					"(this is the bug this spec exists for)", tc.file, info.Title)
			}
			if info.Title != "" {
				t.Errorf("Read(%s).Title = %q, want \"\" for an untagged sample",
					tc.file, info.Title)
			}

			// Real properties, not per-format constants. The 1s tone
			// must come back as ~1s; the old estimateDuration computed
			// size*8/bitrate and returned 0 for every short file.
			if info.Duration < 1 || info.Duration > 2 {
				t.Errorf("Read(%s).Duration = %d, want 1..2 for a 1s sample",
					tc.file, info.Duration)
			}

			// A hardcoded-per-format bitrate is indistinguishable from a
			// real one only if it happens to be right. Assert it is in a
			// plausible range AND that distinct formats get distinct
			// values, which a switch statement cannot do.
			if info.BitRate < 8 || info.BitRate > 3200 {
				t.Errorf("Read(%s).BitRate = %d, want 8..3200", tc.file, info.BitRate)
			}

			if info.Codec != tc.wantCodec {
				t.Errorf("Read(%s).Codec = %q, want %q", tc.file, info.Codec, tc.wantCodec)
			}

			if info.Filename != tc.file {
				t.Errorf("Read(%s).Filename = %q", tc.file, info.Filename)
			}
			st, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Size != int(st.Size()) {
				t.Errorf("Read(%s).Size = %d, want %d", tc.file, info.Size, st.Size())
			}
		})
	}
}

// TestFormatMatrixBitratesAreDistinct guards the specific shape of the old
// bug: estimateBitrate returned a per-format constant, so bitrate carried no
// information. Real values must differ between a lossless and a lossy
// container.
func TestFormatMatrixBitratesAreDistinct(t *testing.T) {
	flac, ogg, wav, mp3 := "sample.flac", "sample.ogg", "sample.wav", "sample.mp3"
	for _, n := range []string{flac, ogg, wav, mp3} {
		if !haveSample(t, n) {
			t.Skipf("generator produced no %s", n)
		}
	}
	read := func(name string) int {
		info, err := Read(copySample(t, name))
		if err != nil {
			t.Fatalf("Read(%s): %v", name, err)
		}
		return info.BitRate
	}

	// sample.flac and sample_alac.m4a are the same 1s PCM source, so they
	// should land close together; wav is the same source too. mp3/ogg are
	// lossy encodings of the same tone at very different bitrates. What
	// matters is that these are measured, not looked up.
	lossless := read("sample_alac.m4a")
	lossy := read("sample.ogg")
	if lossless == lossy {
		t.Fatalf("lossless and lossy reported identical bitrate %d kbps — "+
			"that is a hardcoded constant, not a measurement", lossless)
	}
}

// TestFormatMatrixOptionalFormats covers the containers that need ffmpeg
// muxers a stock build does not ship (ape, mpc, dsf, dff). They are still
// in audioext.libraryExts, so if the local ffmpeg can produce them they must
// read correctly; otherwise the case is skipped.
func TestFormatMatrixOptionalFormats(t *testing.T) {
	optional := []string{"sample.ape", "sample.mpc", "sample.dsf", "sample.dff"}
	ran := 0
	for _, name := range optional {
		if !haveSample(t, name) {
			continue
		}
		ran++
		t.Run(name, func(t *testing.T) {
			path := copySample(t, name)
			info, err := Read(path)
			if err != nil {
				t.Fatalf("Read(%s) failed: %v", name, err)
			}
			if info.Codec == "" {
				t.Errorf("Read(%s).Codec is empty", name)
			}
			if info.Duration < 1 {
				t.Errorf("Read(%s).Duration = %d, want >= 1", name, info.Duration)
			}
		})
	}
	if ran == 0 {
		t.Skip("this ffmpeg build cannot mux ape/mpc/dsf/dff")
	}
}

// TestReadRoundTripsTagsOnEveryFormat writes a known set of tags and reads
// them back, per format. This is the write+read contract the editor depends
// on: what the user saved must be what the next openEditor shows.
//
// It also pins the multi-value artist behaviour, which dhowden got wrong in
// three different ways (see the reader.go package comment): it glued mp3
// values together, returned only the last value on flac/ogg, and returned
// binary atom headers on m4a.
func TestReadRoundTripsTagsOnEveryFormat(t *testing.T) {
	for _, tc := range matrixCases {
		t.Run(tc.file, func(t *testing.T) {
			if !haveSample(t, tc.file) {
				t.Skipf("generator produced no %s on this ffmpeg build", tc.file)
			}
			path := copySample(t, tc.file)

			// Two artists, to catch the multi-value corruption.
			want := &TagUpdate{
				Title:       ptr("标题 Title"),
				Artist:      []string{"甲 Artist", "乙 Artist"},
				Album:       ptr("专辑 Album"),
				AlbumArtist: ptr("专辑艺术家"),
				Genre:       ptr("Rock"),
				Year:        ptr("2021-05-04"),
				TrackNumber: ptr("3/12"),
				DiscNumber:  ptr("1/2"),
				Comment:     ptr("comment text"),
			}
			if err := Write(path, want); err != nil {
				t.Fatalf("Write(%s): %v", tc.file, err)
			}

			info, err := Read(path)
			if err != nil {
				t.Fatalf("Read(%s) after Write: %v", tc.file, err)
			}

			if info.Title != "标题 Title" {
				t.Errorf("Title = %q, want %q", info.Title, "标题 Title")
			}
			// Both artists must survive, separated, with no binary junk.
			if !strings.Contains(info.Artist, "甲 Artist") ||
				!strings.Contains(info.Artist, "乙 Artist") {
				t.Errorf("Artist = %q, want both %q and %q",
					info.Artist, "甲 Artist", "乙 Artist")
			}
			if strings.ContainsAny(info.Artist, "\x00") {
				t.Errorf("Artist = %q contains NUL — container bytes leaked into the tag",
					info.Artist)
			}
			if info.Album != "专辑 Album" {
				t.Errorf("Album = %q, want %q", info.Album, "专辑 Album")
			}
			if info.AlbumArtist != "专辑艺术家" {
				t.Errorf("AlbumArtist = %q, want %q", info.AlbumArtist, "专辑艺术家")
			}
			if info.Genre != "Rock" {
				t.Errorf("Genre = %q, want Rock", info.Genre)
			}
			// DATE is stored verbatim; only the year is meaningful.
			if info.Year != 2021 {
				t.Errorf("Year = %d, want 2021", info.Year)
			}
			if info.TrackNumber != "3/12" {
				t.Errorf("TrackNumber = %q, want 3/12 (dhowden dropped this on flac/ogg)",
					info.TrackNumber)
			}
			if info.DiscNumber != "1/2" {
				t.Errorf("DiscNumber = %q, want 1/2", info.DiscNumber)
			}
		})
	}
}

// TestReadUnparsableFileReturnsError is the contract change: a file we
// cannot read must produce an error, not a fabricated TagInfo.
//
// Before this, a text file named .wav came back as {Title: "song"} with a
// nil error. The editor displayed that as the track's title and a save wrote
// it over the file. Callers that want a display name fall back themselves.
func TestReadUnparsableFileReturnsError(t *testing.T) {
	tmp := t.TempDir()

	cases := []struct {
		name    string
		content []byte
	}{
		{"notaudio.wav", []byte("this is definitely not audio, just text")},
		{"empty.mp3", nil},
		{"garbage.flac", []byte("\xff\xfe\x00\x01\x02\x03not a flac stream")},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(tmp, c.name)
			if err := os.WriteFile(p, c.content, 0o644); err != nil {
				t.Fatal(err)
			}
			info, err := Read(p)
			if err == nil {
				t.Fatalf("Read(%s) = %+v, want error", c.name, info)
			}
			if info != nil {
				t.Errorf("Read(%s) returned non-nil TagInfo alongside error %v; "+
					"callers check err first, so a half-filled struct invites "+
					"exactly the fabrication this replaces", c.name, err)
			}
			if !errors.Is(err, ErrUnsupportedFormat) {
				t.Errorf("Read(%s) error = %v, want ErrUnsupportedFormat", c.name, err)
			}
		})
	}
}

// TestReadMissingFileReturnsError keeps the stat failure distinct from the
// unsupported-format failure so callers can tell "gone" from "unreadable".
func TestReadMissingFileReturnsError(t *testing.T) {
	_, err := Read(filepath.Join(t.TempDir(), "nope.mp3"))
	if err == nil {
		t.Fatal("Read on a missing file returned nil error")
	}
	if errors.Is(err, ErrUnsupportedFormat) {
		t.Errorf("missing file reported as ErrUnsupportedFormat (%v); "+
			"that is a different condition", err)
	}
}

func ptr[T any](v T) *T { return &v }
