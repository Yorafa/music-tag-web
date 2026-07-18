// Package youtube implements the YouTube gRPC DownloadSource plugin by
// shelling out to yt-dlp. The plugin is intentionally thin: all the
// search/download heavy lifting is delegated to the yt-dlp binary that's
// already a runtime dependency for the worker side at
// internal/tasks/yt_dl.go.
//
// Why yt-dlp shell-outs:
//   - the worker already uses yt-dlp via exec.CommandContext (see yt_dl.go);
//     reusing the same binary keeps cookie / extract-audio config drift-free
//     between worker and plugin.
//   - bypassing yt-dlp would mean reimplementing YouTube's search + audio
//     extraction logic in pure Go. Not worth the maintenance cost.
//
// SECURITY: every free-form input that reaches yt-dlp argv goes through
// either a charset whitelist (validYouTubeID), a length cap (query),
// or a clamping (max_results) before being assembled into argv. The argv
// always separates options from positional arguments with `--`, so even
// if a future input slipped through the whitelists, yt-dlp would still
// treat it as a search term / URL, not as a flag.
package youtube

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	pb "go-music-tag/api/proto/tagplugin"
)

const (
	// defaultYTDLPPath is resolved via $PATH; apk installs yt-dlp to
	// /usr/bin/yt-dlp in the plugin image (see Dockerfile.plugin). Tests
	// and dev rigs can override via WithYTDLPPath.
	defaultYTDLPPath = "yt-dlp"

	// maxStderrCapture caps how many of yt-dlp's stderr bytes we keep
	// in memory per call. yt-dlp under failure modes (rate-limit
	// retries, geo-block cascades, --verbose flag leaks) can emit
	// megabytes of stderr; capping at 64 KiB bounds memory while
	// keeping enough for a meaningful head-line diagnostic. The drain
	// itself is full (see cappedWriter + goroutine) — the cap is on
	// what we RETAIN, not on what we drain.
	maxStderrCapture = 64 * 1024
)

// cappedWriter is an io.Writer that captures the first `max` bytes
// written to it and silently discards the rest. It ALWAYS returns
// (len(p), nil) so callers like io.Copy can drain a pipe to completion
// without the writer blocking the producer. Without this contract an
// `io.ReadAll(io.LimitReader(pipe, max))` would orphan the pipe after
// max bytes — yt-dlp would then block on stderr write.
type cappedWriter struct {
	out []byte
	max int
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	if rem := w.max - len(w.out); rem > 0 {
		n := len(p)
		if n > rem {
			n = rem
		}
		w.out = append(w.out, p[:n]...)
	}
	return len(p), nil
}

// drainStderr starts a goroutine that fully drains stderrR into a
// cappedWriter and signals completion via done. Using a goroutine is
// mandatory: yt-dlp writes to stderr AND stdout concurrently, and if
// either pipe hits the kernel pipe-buffer (~64 KiB on Linux) without a
// reader, the cmd deadlocks. We must read both pipes concurrently —
// stderr in a goroutine here, stdout (scanner or io.Discard) on the
// main goroutine.
//
// Returns the buffer (caller reads .out only after <-done). done must
// be received from before touching cw.out under -race.
func drainStderr(stderrR io.ReadCloser) (cw *cappedWriter, done <-chan struct{}) {
	cw = &cappedWriter{max: maxStderrCapture}
	d := make(chan struct{})
	go func() {
		_, _ = io.Copy(cw, stderrR) // drain fully; bounded retention in cw
		close(d)
	}()
	return cw, d
}

// Server implements the YouTube gRPC DownloadSource service by shelling
// out to yt-dlp. Embedding `pb.UnimplementedDownloadSourceServer` keeps
// the stub forward-compatible if the proto grows new RPCs.
type Server struct {
	pb.UnimplementedDownloadSourceServer

	ytdlpPath string
	// workDir is the on-disk staging dir Download() lands files in.
	// Fixed at boot via YT_TMP_DIR (defaults to /tmp/youtube_audio) so
	// the gateway's /api/stream handler can find them without a second
	// hop.
	workDir         string
	searchTimeout   time.Duration
	downloadTimeout time.Duration
}

// Option overrides Server defaults at construction.
type Option func(*Server)

// WithYTDLPPath overrides the yt-dlp binary path. Default "yt-dlp"
// (resolves via $PATH).
func WithYTDLPPath(p string) Option { return func(s *Server) { s.ytdlpPath = p } }

// WithWorkDir overrides the directory Download() writes into. Default
// YT_TMP_DIR=/tmp/youtube_audio — must stay in lockstep with
// internal/tasks/yt_dl.go::youtubeTmpDir.
func WithWorkDir(d string) Option { return func(s *Server) { s.workDir = d } }

// NewServer constructs a Server. It MkdirAll's workDir at startup so
// Download() never races against a missing staging directory mid-stream.
func NewServer(opts ...Option) (*Server, error) {
	s := &Server{
		ytdlpPath:       defaultYTDLPPath,
		workDir:         tmpDir(),
		searchTimeout:   30 * time.Second,
		downloadTimeout: 30 * time.Minute,
	}
	for _, opt := range opts {
		opt(s)
	}
	if err := os.MkdirAll(s.workDir, 0o755); err != nil {
		return nil, fmt.Errorf("youtube: mkdir work dir %q: %w", s.workDir, err)
	}
	return s, nil
}

// tmpDir mirrors internal/tasks/yt_dl.go::youtubeTmpDir. Kept private
// here (instead of importing tasks) to keep the gRPC plugin code decoupled
// from the worker's taskqueue machinery — they share a runtime directory
// by convention, not by import.
func tmpDir() string {
	if v := os.Getenv("YT_TMP_DIR"); v != "" {
		return v
	}
	return "/tmp/youtube_audio"
}

// GetPluginInfo is the gRPC handshake — the gateway calls this once
// during connection setup to surface the source's name + display in
// /api/sources/.
func (s *Server) GetPluginInfo(_ context.Context, _ *pb.DownloadPluginInfoRequest) (*pb.DownloadPluginInfoResponse, error) {
	return &pb.DownloadPluginInfoResponse{
		Name:        "youtube",
		DisplayName: "YouTube",
	}, nil
}

// Search wraps `yt-dlp --no-playlist --flat-playlist --dump-json 'ytsearchN:QUERY'`.
// `--flat-playlist` keeps each entry as a flat metadata blob (no full
// resolution), which bounds response time at the cost of slightly
// older thumbnail URLs.
func (s *Server) Search(ctx context.Context, req *pb.DownloadSearchRequest) (*pb.DownloadSearchResponse, error) {
	query := strings.TrimSpace(req.GetQuery())
	if query == "" {
		return nil, fmt.Errorf("youtube: empty query")
	}
	if strings.ContainsAny(query, "\n\r") {
		return nil, fmt.Errorf("youtube: query contains newline")
	}
	// Bound query length so a hostile caller can't blow out yt-dlp's
	// argv buffer. 1 KiB is generous; real search queries are <512 bytes.
	if len(query) > 1024 {
		return nil, fmt.Errorf("youtube: query too long (%d)", len(query))
	}

	max := int(req.GetMaxResults())
	if max <= 0 {
		max = 10
	}
	if max > 20 {
		max = 20
	}

	args := []string{
		"--no-playlist",
		"--flat-playlist",
		"--no-warnings",
		"--dump-json",
		fmt.Sprintf("ytsearch%d:%s", max, query),
	}

	cctx, cancel := context.WithTimeout(ctx, s.searchTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, s.ytdlpPath, args...)
	cmd.Env = append(os.Environ(),
		"PYTHONUNBUFFERED=1",
		"LC_ALL=C.UTF-8",
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("youtube: stdout pipe: %w", err)
	}
	stderrR, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("youtube: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("youtube: start %q: %w", s.ytdlpPath, err)
	}

	// Concurrent pipe drain: stderr → goroutine (capped), stdout →
	// main goroutine (scanner). Crucial — sequential drain would
	// deadlock whenever yt-dlp emits enough data to whichever pipe we
	// aren't currently reading to fill the kernel pipe buffer.
	stderrBuf, stderrDone := drainStderr(stderrR)
	// Reap the cmd on EVERY exit path. The explicit cmd.Wait() further
	// down captures the exit status for the success/failure branch;
	// this defer handles the error paths (scanner.Err etc.) that
	// return before we reach the explicit Wait. The double-Wait returns
	// "exec: Wait was already called" which we discard.
	defer func() {
		<-stderrDone
		_ = cmd.Wait()
	}()

	items := make([]*pb.DownloadItem, 0, max)
	scanner := bufio.NewScanner(stdout)
	// 64 KiB initial, 4 MiB max — yt-dlp's per-entry JSON is usually 1-5
	// KiB but very long titles + descriptions can balloon a single line
	// out of the default 64 KiB max without warning. 4 MiB is the upper
	// bound we'd expect, without committing to unbounded memory on a
	// hostile caller.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry struct {
			ID        string  `json:"id"`
			Title     string  `json:"title"`
			Duration  float64 `json:"duration"`
			URL       string  `json:"url"`
			Channel   string  `json:"channel"`
			Thumbnail string  `json:"thumbnail"`
		}
		if err := json.Unmarshal(line, &entry); err != nil {
			// yt-dlp occasionally intersperses metadata lines (e.g.
			// "[generic] Extracting URL...") with JSON. Skip them.
			continue
		}
		if entry.ID == "" {
			continue
		}
		items = append(items, &pb.DownloadItem{
			Id:        entry.ID,
			Title:     entry.Title,
			Duration:  entry.Duration,
			Url:       entry.URL,
			Channel:   entry.Channel,
			Thumbnail: entry.Thumbnail,
		})
	}
	if scanErr := scanner.Err(); scanErr != nil {
		// Defer runs <-stderrDone + cmd.Wait() so the goroutine ends
		// and the cmd is reaped before we return.
		return nil, fmt.Errorf("youtube: read search output: %w", scanErr)
	}

	// cmd has exited (stdout EOF ⇒ cmd close ⇒ scanner EOF). cmd.Wait()
	// returns the exit status, which we want for the success/failure
	// branch below.
	waitErr := cmd.Wait()
	// Always wait on stderrDone before reading cw.out — the goroutine's
	// last write happens-before its close(done), and the receive
	// happens-before any subsequent read of cw.out.
	<-stderrDone
	stderr := strings.TrimSpace(string(stderrBuf.out))

	if waitErr != nil {
		// yt-dlp typically exits 0 on a successful zero-result search
		// and non-zero only on hard failures (network down, format
		// unavailable, geo-restriction on every entry). If we recovered
		// any items, surface them — the UI is happier with "5 results,
		// the rest were unavailable" than with a hard error. Log the
		// stderr head line so ops can still correlate the partial
		// failure.
		if stderr != "" {
			log.Printf("[youtube] search partial: %s", firstLine(stderr))
		}
		if len(items) == 0 {
			if stderr != "" {
				return nil, fmt.Errorf("youtube: yt-dlp: %s", firstLine(stderr))
			}
			return nil, fmt.Errorf("youtube: yt-dlp exit: %w", waitErr)
		}
	}
	return &pb.DownloadSearchResponse{Items: items}, nil
}

// Download lands the audio track for video_id into workDir and returns
// the file path the gateway can serve. Mirrors internal/tasks/yt_dl.go's
// filenames — YouTube (id).(ext) is the same on both sides — so a
// handler that points at YT_TMP_DIR can serve either result
// interchangeably.
func (s *Server) Download(ctx context.Context, req *pb.DownloadRequest) (*pb.DownloadResponse, error) {
	videoID := strings.TrimSpace(req.GetVideoId())
	if videoID == "" {
		return &pb.DownloadResponse{Success: false, Error: "empty video_id"}, nil
	}
	if !validYouTubeID(videoID) {
		return &pb.DownloadResponse{
			Success: false,
			Error:   fmt.Sprintf("youtube: invalid video_id %q", videoID),
		}, nil
	}

	// The proto lets callers specify an optional download_dir, but the
	// existing gateway handler at handler/youtube.go::YoutubeDownload
	// never sets it today. Pin the override to workDir anyway: allowing
	// the caller to point into arbitrary filesystem locations would
	// let a compromised gateway bushwack files outside workDir.
	// filepath.Clean normalises trailing slashes and /./ so e.g.
	// /tmp/youtube_audio/ and /tmp/youtube_audio/./ compare equal to
	// /tmp/youtube_audio.
	if override := strings.TrimSpace(req.GetDownloadDir()); override != "" {
		if filepath.Clean(override) != filepath.Clean(s.workDir) {
			return &pb.DownloadResponse{
				Success: false,
				Error:   fmt.Sprintf("youtube: download_dir override not allowed (workDir is %q)", s.workDir),
			}, nil
		}
	}
	dir := s.workDir

	outTpl := filepath.Join(dir, "%(id)s.%(ext)s")
	args := []string{
		"--no-playlist",
		"--no-progress",
		"--newline",
		"-f", "bestaudio/best",
		"-o", outTpl,
		"--no-part",
		"--", // separates opts from positional URL; defence-in-depth.
		"https://www.youtube.com/watch?v=" + videoID,
	}

	cctx, cancel := context.WithTimeout(ctx, s.downloadTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, s.ytdlpPath, args...)
	cmd.Env = append(os.Environ(),
		"PYTHONUNBUFFERED=1",
		"LC_ALL=C.UTF-8",
	)

	// Same concurrent-drain contract as Search. We don't care about
	// stdout content here (download doesn't emit JSON), so drain it to
	// /dev/null rather than parsing it.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("youtube: stdout pipe: %w", err)
	}
	stderrR, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("youtube: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("youtube: start %q: %w", s.ytdlpPath, err)
	}

	stderrBuf, stderrDone := drainStderr(stderrR)
	stdoutDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, stdout)
		close(stdoutDone)
	}()

	waitErr := cmd.Wait()
	<-stderrDone // happens-before for cw.out reads below
	<-stdoutDone

	if waitErr != nil {
		snippet := firstLine(strings.TrimSpace(string(stderrBuf.out)))
		if snippet == "" {
			snippet = waitErr.Error()
		}
		return &pb.DownloadResponse{
			Success: false,
			Error:   fmt.Sprintf("yt-dlp: %s", snippet),
		}, nil
	}

	fpath, fname, err := findLandedFile(dir, videoID)
	if err != nil {
		return &pb.DownloadResponse{Success: false, Error: err.Error()}, nil
	}
	return &pb.DownloadResponse{
		Success:  true,
		FilePath: fpath,
		FileName: fname,
	}, nil
}

// validYouTubeID is a charset whitelist for video_id: alphanumeric and
// [_-], length 6..32. Real YouTube IDs are 11 chars; the range tolerates
// future YouTube ID schemes (Shorts/Live etc.) and avoids over-fitting
// to today's URL pattern.
func validYouTubeID(id string) bool {
	if len(id) < 6 || len(id) > 32 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}

// findLandedFile picks the most-recently-created audio file matching
// `<id>.{ext}` in dir. Mirrors internal/tasks/yt_dl.go::findDownloadedFile
// but kept private here so the plugin doesn't import the worker package
// (the dependency direction would be awkward; the duplication is small
// enough to live in both).
func findLandedFile(dir, videoID string) (string, string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", fmt.Errorf("youtube: read dir %q: %w", dir, err)
	}
	var (
		bestPath string
		bname    string
		bestTime time.Time
	)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, videoID+".") {
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		switch ext {
		case ".mp3", ".m4a", ".ogg", ".opus", ".wav", ".webm", ".mkv", ".mp4":
		default:
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if bestPath == "" || info.ModTime().After(bestTime) {
			bestPath = filepath.Join(dir, name)
			bname = name
			bestTime = info.ModTime()
		}
	}
	if bestPath == "" {
		return "", "", fmt.Errorf("youtube: no audio file for id=%s in %s", videoID, dir)
	}
	return bestPath, bname, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
