package testaudio

import (
	"os"
	"path/filepath"
	"testing"

	"go.senan.xyz/taglib"
)

// TestMP3BytesIsDecodable is the whole point of this package: if the
// synthetic fixture ever stops being a real MPEG stream, every spec that
// depends on it becomes a false negative waiting to happen.
func TestMP3BytesIsDecodable(t *testing.T) {
	path := WriteMP3(t, filepath.Join(t.TempDir(), "fixture.mp3"))

	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := mpegFrameLen * mpegFrames; int(st.Size()) != want {
		t.Errorf("size = %d, want %d", st.Size(), want)
	}

	props, err := taglib.ReadProperties(path)
	if err != nil {
		t.Fatalf("ReadProperties: %v", err)
	}
	if props.Format != "mpeg" {
		t.Errorf("Format = %q, want mpeg", props.Format)
	}
	if props.Length <= 0 {
		t.Fatalf("Length = %v, want > 0 — a zero duration means TagLib "+
			"cannot find a frame chain and the fixture is not real audio", props.Length)
	}
	if got := props.Length.Seconds(); got < 0.2 || got > 1.5 {
		t.Errorf("Length = %v, want roughly 0.52s", props.Length)
	}
	if props.BitRate != 128 {
		t.Errorf("BitRate = %d, want 128", props.BitRate)
	}
}

func TestSeedMP3(t *testing.T) {
	dir := t.TempDir()
	p := SeedMP3(t, dir, "Artist/Album/01 Song.mp3")
	if filepath.Dir(p) != filepath.Join(dir, "Artist", "Album") {
		t.Errorf("SeedMP3 created %s, want nested dirs under %s", p, dir)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("SeedMP3 did not create the file: %v", err)
	}
}
