package tag

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.senan.xyz/taglib"
)

// TestWritePersistsTagsInEveryFormat is the write-side half of the format
// matrix. Its existence is justified by a measured failure: a .wav/.aiff/
// .wma/.wv/.tta file whose tags could not be parsed came back from Read as
// {Title: "sample"} — the filename stem — with a nil error. The editor
// showed that fabricated title, a save reported success, and the user's real
// tags were gone with no error anywhere in the system.
//
// So: write a marker value, read it back through the public Read, and require
// the marker. Anything that round-trips to a different value is a data-loss
// bug regardless of which layer is at fault.
func TestWritePersistsTagsInEveryFormat(t *testing.T) {
	for _, tc := range matrixCases {
		t.Run(tc.file, func(t *testing.T) {
			if !haveSample(t, tc.file) {
				t.Skipf("generator produced no %s on this ffmpeg build", tc.file)
			}
			path := copySample(t, tc.file)

			const marker = "ZZMARKERZZ"
			if err := Write(path, &TagUpdate{Title: ptr(marker)}); err != nil {
				t.Fatalf("Write(%s): %v", tc.file, err)
			}

			info, err := Read(path)
			if err != nil {
				t.Fatalf("Read(%s) after Write: %v", tc.file, err)
			}
			if info.Title != marker {
				t.Fatalf("Write(%s) did not persist: Title = %q, want %q "+
					"(the save reported success, so this is silent data loss)",
					tc.file, info.Title, marker)
			}

			// A marker equal to the filename stem must still be
			// distinguishable from a fabricated title, so confirm the
			// value really came from the tag and not from the name.
			if info.Title == trimExt(tc.file) {
				t.Fatalf("Title %q looks fabricated from the filename", info.Title)
			}
		})
	}
}

// TestWritePersistsCoverArtInEveryFormat pins the embedded-artwork path.
// HandleSidecars reads the cover back out of the file to produce a
// cover-<album> sidecar, so a lost image is not cosmetic: the next save
// writes a stale sidecar.
func TestWritePersistsCoverArtInEveryFormat(t *testing.T) {
	// A real 1x1 PNG, so decoders that validate the payload do not reject it.
	png := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
		0x89, 0x00, 0x00, 0x00, 0x0A, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
		0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
		0x42, 0x60, 0x82,
	}

	for _, tc := range matrixCases {
		t.Run(tc.file, func(t *testing.T) {
			if !haveSample(t, tc.file) {
				t.Skipf("generator produced no %s on this ffmpeg build", tc.file)
			}
			path := copySample(t, tc.file)

			title := "art"
			if err := Write(path, &TagUpdate{AlbumImg: png, Title: &title}); err != nil {
				t.Fatalf("Write(%s) with artwork: %v", tc.file, err)
			}

			info, err := Read(path)
			if err != nil {
				t.Fatalf("Read(%s): %v", tc.file, err)
			}
			if info.Artwork == "" {
				t.Fatalf("Read(%s).Artwork is empty after a successful write", tc.file)
			}
			raw, mimeType, err := DecodePictureBase64(info.Artwork)
			if err != nil {
				t.Fatalf("DecodePictureBase64 for %s: %v", tc.file, err)
			}
			if !bytes.Equal(raw, png) {
				t.Errorf("%s artwork round-trip mismatch: got %d bytes, want %d",
					tc.file, len(raw), len(png))
			}
			if mimeType != "image/png" {
				t.Errorf("%s artwork mime = %q, want image/png", tc.file, mimeType)
			}
			// 1x1 PNG, so the IHDR sniff must agree.
			if info.ArtworkW != 1 || info.ArtworkH != 1 {
				t.Errorf("%s artwork dims = %dx%d, want 1x1",
					tc.file, info.ArtworkW, info.ArtworkH)
			}
		})
	}
}

// TestWriteTagsPropagatesArtworkFailure stops the silent-cover-loss
// regression at its source. writeWithTagLib used to discard the WriteImage
// error outright (`_ = err`), so a container that refused artwork reported
// success while keeping the old cover — and since HandleSidecars reads the
// cover back out to write a sidecar, the next save would copy that stale
// artwork into the sidecar file.
//
// A read-only file is the failure trigger: taglib reports "can't save file"
// for it, and unlike a non-audio target it exercises the image branch
// specifically (taglib will happily write an ID3 frame onto a text file, so
// "not audio" cannot be used to make the image write fail).
func TestWriteTagsPropagatesArtworkFailure(t *testing.T) {
	if !haveSample(t, "sample.mp3") {
		t.Skip("no sample.mp3")
	}
	png := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
		0x89, 0x00, 0x00, 0x00, 0x0A, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
		0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
		0x42, 0x60, 0x82,
	}

	src := filepath.Join(buildSamples(t), "sample.mp3")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "readonly.mp3")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	// Root ignores the write bit, which would make this spec vacuous.
	if os.Geteuid() == 0 {
		t.Skip("running as root: the read-only bit is not enforced")
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

	// Make it read-only only after setup, and pass AlbumImg alone: with no
	// text fields writeWithTagLib skips the WriteTags call entirely, so the
	// image write is the only thing that can fail. Both calls hit the same
	// permission check, so a Title would mask the branch under test.
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}

	// Sanity: the library really does fail here, so a pass below is a real
	// pass and not a test that never exercised the failure path.
	if err := taglib.WriteImage(path, png); err == nil {
		t.Skip("taglib.WriteImage unexpectedly succeeded on a read-only file")
	}

	err = writeWithTagLib(path, &TagUpdate{AlbumImg: png})
	if err == nil {
		t.Fatal("writeWithTagLib returned nil after the image write failed; " +
			"a swallowed error here means a lost cover reported as success")
	}
	if !strings.Contains(err.Error(), "write image") {
		t.Errorf("error = %v, want it to name the image write", err)
	}
}

// TestWriteRejectsNonAudioFile is the plain write-side guard: we must not
// let a text file named .mp3 be rewritten with an ID3 header and reported as
// a successful tag save. taglib.WriteTags will happily do exactly that —
// verified by prepending a well-formed ID3v2 frame to the text and
// returning nil — so the check has to live in our Write.
func TestWriteRejectsNonAudioFile(t *testing.T) {
	cases := []struct {
		name    string
		content []byte
	}{
		{"text-named-mp3.mp3", []byte("definitely not an mp3")},
		{"empty.mp3", nil},
		{"truncated.mp3", []byte{0xFF, 0xFB, 0x90, 0x00}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), c.name)
			if err := os.WriteFile(path, c.content, 0o644); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			if err := Write(path, &TagUpdate{Title: ptr("x")}); err == nil {
				t.Fatal("Write on a non-audio file returned nil")
			}

			// The file must be byte-identical: a rejected write that still
			// mutated the target would be worse than no check at all.
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Errorf("rejected write still modified the file: %d -> %d bytes",
					len(before), len(after))
			}
		})
	}
}

// TestWriteNilUpdateIsNoOp keeps the nil-update early return: callers pass
// through when the user changed nothing, and that must not touch the file.
func TestWriteNilUpdateIsNoOp(t *testing.T) {
	if !haveSample(t, "sample.mp3") {
		t.Skip("no sample.mp3")
	}
	path := copySample(t, "sample.mp3")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Write(path, nil); err != nil {
		t.Fatalf("Write(nil): %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("Write(path, nil) modified the file")
	}
}

// TestSniffImageWH pins the artwork dimension sniffer, which Read relies on
// because neither taglib nor dhowden reports pixel dimensions.
func TestSniffImageWH(t *testing.T) {
	oneByOne := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	}
	cases := []struct {
		name  string
		data  []byte
		wantW int
		wantH int
	}{
		{"png 1x1", oneByOne, 1, 1},
		{"truncated png", oneByOne[:10], 0, 0},
		{"empty", nil, 0, 0},
		{"text", []byte("hello world"), 0, 0},
		// A JPEG SOF0 header carrying 0x0140 x 0x00A0 (320x160).
		{"jpeg", []byte{
			0xFF, 0xD8, 0xFF, 0xC0, 0x00, 0x11, 0x08,
			0x00, 0xA0, 0x01, 0x40, 0x03, 0x01, 0x22, 0x00,
			0x02, 0x11, 0x01, 0x03, 0x11, 0x01, 0xFF, 0xD9,
		}, 320, 160},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, h := sniffImageWH(c.data)
			if w != c.wantW || h != c.wantH {
				t.Errorf("sniffImageWH = %dx%d, want %dx%d", w, h, c.wantW, c.wantH)
			}
		})
	}
}

func trimExt(name string) string {
	return name[:len(name)-len(filepath.Ext(name))]
}
