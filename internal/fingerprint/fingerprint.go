// Package fingerprint is the only place that knows how to read Chromaprint
// output and how to decide whether two fingerprints describe the same song.
//
// It exists because that knowledge was written down three times — in the
// dedup stage, in the acoustid plugin, and in the duration indexer — each
// against a different fpcalc output mode, and the copies had already drifted
// apart. The acoustid plugin's copy was the one that mattered: it parsed
// `-json`, fell back to a text mode that fpcalc has not emitted since 1.0,
// and was never exercised end to end, so it shipped pointing at an endpoint
// that 404s with a hardcoded API key the service rejects.
//
// # The two output modes
//
// fpcalc can print a fingerprint two ways, and they are not interchangeable.
//
// `-json` returns a *compressed* fingerprint: a base64 blob whose bits have
// been packed and delta-coded, which is what the AcoustID web API accepts.
// Comparing two of those with == answers "do these files decode to the same
// audio bits", not "is this the same song", so equality is useless for
// detecting re-encodings.
//
// `-raw` returns the subfingerprint list: one 32-bit integer per ~26ms of
// audio, each a chroma-band energy bitmask. Two encodings of one track
// produce near-identical lists, which is what makes a distance comparison
// meaningful.
//
// Measured on one 120s track re-encoded seven ways:
//
//	opus 190k   0.99631     mp3 220k   0.99862
//	opus 200k   0.99624     flac       0.99997
//	vorbis 200k 0.99568     wav 16bit  0.99997
//	vorbis 190k 0.99525
//	a different track from the same library: 0.51101
//
// Same-song and different-song are therefore separated by a wide margin.
// Callers pick their own threshold; the dedup stage uses 0.90.
package fingerprint

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ErrNoBinary means fpcalc is not installed, so no comparison is possible.
//
// It is deliberately distinct from every other failure. "Cannot run" and
// "ran and found nothing" look identical in a response that carries only a
// result list, and conflating them is how a stage that never executed came
// to look like a stage that found nothing.
var ErrNoBinary = errors.New("fpcalc not available")

// minSubfingerprints is the shortest fingerprint worth comparing.
//
// Chromaprint emits roughly 7.4 subfingerprints per second, so 100 covers
// about 13 seconds of audio. Below that a handful of agreeing bits out of a
// few hundred is not evidence of anything, and a caller that treated the
// result as a score would start reporting unrelated files as the same track.
const minSubfingerprints = 100

// timeout bounds one fpcalc invocation. fpcalc decodes the whole file, so
// this scales with track length; 20s is roughly a five-minute track.
const timeout = 20 * time.Second

// Fingerprint is a file's subfingerprints plus its decoded length.
type Fingerprint struct {
	raw      []uint32
	duration int // seconds, 0 when fpcalc did not report one
}

// Duration is the decoded length in whole seconds.
func (f Fingerprint) Duration() int { return f.duration }

// Len is the number of subfingerprints, i.e. roughly duration/0.135.
func (f Fingerprint) Len() int { return len(f.raw) }

// Compressed is the packed form the AcoustID web API accepts. It cannot be
// compared for similarity — see the package comment.
type Compressed struct {
	// Data is the base64 fingerprint string, sent as the `fingerprint` form
	// field.
	Data string
	// Duration is the decoded length in whole seconds, sent as `duration`.
	Duration int
}

// LookPath resolves fpcalc on PATH, returning ErrNoBinary when it is absent.
func LookPath() (string, error) {
	p, err := exec.LookPath("fpcalc")
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNoBinary, err)
	}
	return p, nil
}

// Raw runs `fpcalc -raw` and returns the subfingerprint list.
//
// An empty fpcalcPath is ErrNoBinary rather than a failed command, so a
// caller can tell "no comparison possible" from "this file is not audio".
func Raw(ctx context.Context, fpcalcPath, audioPath string) (Fingerprint, error) {
	out, err := run(ctx, fpcalcPath, "-raw", audioPath)
	if err != nil {
		return Fingerprint{}, err
	}

	var fp Fingerprint
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "DURATION="):
			if secs, perr := strconv.ParseFloat(strings.TrimPrefix(line, "DURATION="), 64); perr == nil {
				fp.duration = int(secs)
			}
		case strings.HasPrefix(line, "FINGERPRINT="):
			fp.raw = parseSubFingerprints(strings.TrimPrefix(line, "FINGERPRINT="))
		}
	}
	if len(fp.raw) == 0 {
		return Fingerprint{}, fmt.Errorf("fpcalc: no subfingerprints in output for %s", audioPath)
	}
	return fp, nil
}

// Compress runs `fpcalc -json` and returns the packed fingerprint.
//
// There is deliberately no text-mode fallback. fpcalc has supported -json
// since 1.5 (2021) and every image here ships 1.5 or newer; the fallback
// that used to sit behind it only ever ran against a hypothetical old
// binary, and its presence is what let a broken -json path ship unnoticed.
func Compress(ctx context.Context, fpcalcPath, audioPath string) (Compressed, error) {
	out, err := run(ctx, fpcalcPath, "-json", audioPath)
	if err != nil {
		return Compressed{}, err
	}
	var parsed struct {
		Duration    float64 `json:"duration"`
		Fingerprint string  `json:"fingerprint"`
	}
	if jerr := json.Unmarshal(out, &parsed); jerr != nil {
		return Compressed{}, fmt.Errorf("fpcalc -json: %w (output=%.120q)", jerr, out)
	}
	if parsed.Fingerprint == "" {
		return Compressed{}, fmt.Errorf("fpcalc -json: empty fingerprint for %s", audioPath)
	}
	return Compressed{Data: parsed.Fingerprint, Duration: int(parsed.Duration)}, nil
}

// DurationOf returns just the length of a file, in seconds.
//
// It reads the output a line at a time and stops at DURATION=, which fpcalc
// prints before the fingerprint. For a long track the subfingerprints are
// the bulk of both the output and the work, and an indexer that only wants
// the length should not pull ~3.8KB per file through a pipe to discard it.
//
// A file fpcalc cannot decode returns an error. Callers must keep that
// distinct from a duration of 0: 0 is how an unindexed row is marked, and
// writing it for a file that failed would make the row look indexed while
// staying invisible to every query, so it would never be retried.
func DurationOf(ctx context.Context, fpcalcPath, audioPath string) (int, error) {
	if fpcalcPath == "" {
		return 0, ErrNoBinary
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// #nosec G204 -- fpcalcPath comes from LookPath, not from a request.
	cmd := exec.CommandContext(runCtx, fpcalcPath, "-raw", audioPath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	defer func() { _ = cmd.Wait() }()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "DURATION=") {
			continue
		}
		secs, perr := strconv.ParseFloat(strings.TrimPrefix(line, "DURATION="), 64)
		if perr != nil {
			return 0, fmt.Errorf("fpcalc: bad DURATION line %q: %w", line, perr)
		}
		// Closing stdout makes fpcalc see EPIPE on its next write, which is
		// fine: we already have the number, and skipping the rest is the
		// point of this function.
		if secs < 0 {
			return 0, nil
		}
		return int(secs), nil
	}
	if serr := scanner.Err(); serr != nil {
		return 0, serr
	}
	return 0, fmt.Errorf("fpcalc: no DURATION line for %s", audioPath)
}

// run executes fpcalc and returns its stdout.
func run(ctx context.Context, fpcalcPath, mode, audioPath string) ([]byte, error) {
	if fpcalcPath == "" {
		return nil, ErrNoBinary
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// #nosec G204 -- fpcalcPath comes from LookPath, not from a request.
	cmd := exec.CommandContext(runCtx, fpcalcPath, mode, audioPath)
	out, err := cmd.Output()
	if err != nil {
		// fpcalc exits non-zero on anything it cannot decode and writes the
		// reason to stdout, not stderr, so ExitError.Stderr is usually empty
		// and the useful text is lost. Re-run the message through the same
		// channel the caller would have read.
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			detail := strings.TrimSpace(string(ee.Stderr))
			if detail == "" {
				detail = "fpcalc could not decode the file"
			}
			return nil, fmt.Errorf("fpcalc %s %s: %s", mode, audioPath, detail)
		}
		return nil, fmt.Errorf("fpcalc %s %s: %w", mode, audioPath, err)
	}
	return out, nil
}

// parseSubFingerprints splits the comma-separated 32-bit list.
//
// A malformed element ends the list rather than failing the parse: fpcalc
// emits valid integers, so a bad one means the output was cut short, and a
// short list still supports a distance comparison while an empty one does
// not.
func parseSubFingerprints(s string) []uint32 {
	parts := strings.Split(s, ",")
	out := make([]uint32, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			break
		}
		out = append(out, uint32(v))
	}
	return out
}

// popcount returns the number of set bits in v.
func popcount(v uint32) int {
	// Hacker's Delight, branch-free.
	v = v - ((v >> 1) & 0x55555555)
	v = (v & 0x33333333) + ((v >> 2) & 0x33333333)
	v = (v + (v >> 4)) & 0x0F0F0F0F
	return int((v * 0x01010101) >> 24)
}

// Similarity returns the fraction of agreeing bits between two fingerprints
// over the region they overlap, and whether the comparison is meaningful.
//
// The bool is false when either side is too short to judge. Callers must
// treat that as "cannot compare" rather than as a score of zero — the two
// are different answers, and collapsing them is how an unjudgeable pair
// becomes a reported duplicate.
func Similarity(a, b Fingerprint) (float64, bool) {
	if len(a.raw) < minSubfingerprints || len(b.raw) < minSubfingerprints {
		return 0, false
	}
	n := len(a.raw)
	if len(b.raw) < n {
		n = len(b.raw)
	}
	if n < minSubfingerprints {
		return 0, false
	}

	var errBits uint64
	for i := 0; i < n; i++ {
		errBits += uint64(popcount(a.raw[i] ^ b.raw[i]))
	}
	return 1.0 - float64(errBits)/float64(uint64(n)*32), true
}
