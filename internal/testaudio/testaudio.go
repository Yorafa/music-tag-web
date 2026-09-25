// Package testaudio builds small, genuinely decodable audio fixtures for
// tests.
//
// # Why this exists
//
// Several specs used to seed a "music file" with a handful of literal bytes
// — []byte("payload"), []byte("ID3\x03\x00..."), []byte("stub"). That was
// fine while tag.Read was dhowden-based, because dhowden's Identify()
// dispatches on the file EXTENSION and its ID3 parser accepts a bare
// "ID3" magic with no frames.
//
// It stopped being fine for two reasons, both of which are now load-bearing
// behaviour under test:
//
//  1. taglib.WriteTags will prepend a well-formed ID3v2 header to a text
//     file and return nil, so a fake fixture let a test assert a
//     "successful" tag write on a file with no audio in it.
//  2. tag.Write now refuses to touch a file with no decodable audio
//     stream (see internal/tag.ensureAudioFile), which is exactly the
//     guard that stops a non-audio file being silently corrupted. Fake
//     fixtures cannot satisfy that guard.
//
// So the fixtures have to be real. Committing real encoded audio is the
// obvious answer and the wrong one: a multi-KB binary blob in git cannot be
// reviewed in a diff, and a 1s sample per format is ~300 KB of the repo.
//
// Instead we synthesise the smallest thing TagLib will accept as a valid
// MPEG-1 Layer III stream: a run of frame headers with zeroed payload.
// TagLib walks the frame chain to compute duration, so it must be able to
// find real frames — but it does not care that the audio data inside them
// is silence, because that is literally what an encoder emits for a silent
// frame.
//
// This keeps every fixture a few lines of code, works without ffmpeg (so CI
// needs no extra tooling), and produces files that pass the same
// readability check as a real download.
//
// Only test files should import this package; nothing under cmd/ or the
// runtime path does, so it is not linked into the shipped binaries.
package testaudio

import (
	"os"
	"path/filepath"
	"testing"
)

// MPEG-1 Layer III, 128 kbit/s, 44.1 kHz, joint stereo, no CRC, no padding.
//
//	0xFF 0xFB  sync (11 bits) + MPEG-1 (11) + Layer III (01) + no CRC (1)
//	0x90      bitrate index 9 (=128 kbit/s for MPEG-1 LIII) + sample index
//	          0 (=44100 Hz) + padding 0 + private 0
//	0x00      channel mode 00 (stereo) + mode ext 00 + copyright 0 +
//	          original 0 + emphasis 00
const (
	mpegHeader0 = 0xFF
	mpegHeader1 = 0xFB
	mpegHeader2 = 0x90
	mpegHeader3 = 0x00

	// Frame length in bytes = floor(144 * bitrate / samplerate) + padding
	//                         = floor(144 * 128000 / 44100) = 417.
	mpegFrameLen = 417

	// mpegFrameSamples is the number of PCM samples one Layer III frame
	// decodes to (1152, since 144*8=1152 > 576). 20 frames is ~0.52 s,
	// comfortably past the ">0 duration" readability threshold, and only
	// 8.3 KB on disk.
	mpegFrameSamples = 1152
	mpegSampleRate   = 44100
	mpegFrames       = 20
)

// MP3Bytes returns a valid, decodable MPEG-1 Layer III file: mpegFrames
// zeroed frames behind a real frame header.
//
// The result has no ID3 tags, which is usually what a test wants (it can
// then assert on tags it wrote itself, or on the absence of tags).
func MP3Bytes() []byte {
	out := make([]byte, 0, mpegFrameLen*mpegFrames)
	frame := make([]byte, mpegFrameLen)
	frame[0] = mpegHeader0
	frame[1] = mpegHeader1
	frame[2] = mpegHeader2
	frame[3] = mpegHeader3
	for i := 0; i < mpegFrames; i++ {
		out = append(out, frame...)
	}
	return out
}

// MP3DurationSeconds is the playback length of MP3Bytes, useful for
// asserting a decoded duration without hard-coding it.
func MP3DurationSeconds() float64 {
	return float64(mpegFrames*mpegFrameSamples) / mpegSampleRate
}

// WriteMP3 writes MP3Bytes to path, creating parent directories as needed,
// and returns the path.
//
// Tests should prefer this over os.WriteFile for any fixture that will pass
// through internal/tag: a fixture that is not real audio will now be
// rejected by design, and the failure will point at the fixture rather than
// at the behaviour under test.
func WriteMP3(tb testing.TB, path string) string {
	tb.Helper()
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			tb.Fatalf("create fixture dir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(path, MP3Bytes(), 0o644); err != nil {
		tb.Fatalf("write mp3 fixture %s: %v", path, err)
	}
	return path
}

// SeedMP3 writes an MP3 fixture at filepath.Join(dir, name) and returns the
// full path.
func SeedMP3(tb testing.TB, dir, name string) string {
	tb.Helper()
	return WriteMP3(tb, filepath.Join(dir, name))
}
