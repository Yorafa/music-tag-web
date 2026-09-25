package tag

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

// Sample generation.
//
// Every read/write spec in this package needs *real* files in *real*
// containers. Hand-built byte stubs cannot express "does TagLib's APE parser
// cope with a real APE header", and they actively hid the bug this file
// exists to pin: a .wav with unreadable tags used to round-trip as
// {Title: "sample"} and the suite was green.
//
// testdata/gen_samples.sh produces one ~1s file per container. It runs once
// per test binary (sync.Once) into a shared temp dir that TestMain removes.
// Without ffmpeg on PATH the tests skip rather than fail: a machine that
// cannot build audio should not report a tag bug.

var (
	sampleOnce sync.Once
	sampleDir  string
	sampleErr  error
)

// buildSamples shells out to the generator script. Kept in a helper so every
// spec calls the same function and therefore shares one build.
func buildSamples(t testing.TB) string {
	t.Helper()
	sampleOnce.Do(func() {
		if _, err := exec.LookPath("ffmpeg"); err != nil {
			sampleErr = err
			return
		}
		dir, err := os.MkdirTemp("", "tag-samples-")
		if err != nil {
			sampleErr = err
			return
		}
		sampleDir = dir

		script := filepath.Join("testdata", "gen_samples.sh")
		cmd := exec.Command("sh", script, dir)
		// The generator reports per-format skips on stderr (e.g. a distro
		// ffmpeg with no APE muxer). That is expected and not an error;
		// only a non-zero exit from the script itself is.
		if out, err := cmd.CombinedOutput(); err != nil {
			sampleErr = err
			t.Logf("gen_samples.sh failed: %v\n%s", err, out)
			return
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			sampleErr = err
			return
		}
		if len(entries) == 0 {
			sampleErr = os.ErrNotExist
			return
		}
	})
	if sampleErr != nil {
		t.Skipf("audio samples unavailable (need ffmpeg on PATH): %v", sampleErr)
	}
	return sampleDir
}

// copySample copies a generated sample into the caller's temp dir so a test
// that writes to it cannot perturb the shared fixtures for later tests.
func copySample(t testing.TB, name string) string {
	t.Helper()
	src := filepath.Join(buildSamples(t), name)
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read sample %s: %v", name, err)
	}
	dst := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("write sample %s: %v", name, err)
	}
	return dst
}

// haveSample reports whether the generator produced this container. Used to
// skip individual formats rather than the whole suite.
func haveSample(t testing.TB, name string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(buildSamples(t), name))
	return err == nil
}
