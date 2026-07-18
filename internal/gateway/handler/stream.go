package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"

	"go-music-tag/internal/plugin"
	"go-music-tag/internal/taskclient"
	"go-music-tag/internal/tasks"
)

// streamBodyCapBytes caps how many bytes we'll proxy from an upstream audio
// URL in a single response. 50 MiB is comfortably above any preview-length
// streaming track the 5 plugins expose; if the upstream serves a longer
// file the browser will see a truncated stream (acceptable for preview)
// and the gateway won't OOM under replay attacks.
//
// NOT applied to the ServeFile branch — that one is a plain file on disk
// served via http.ServeFile and the OS handles its own limits.
const streamBodyCapBytes int64 = 50 << 20

// streamUpstreamTimeout bounds how long streamFromPlugin's HTTP request
// to the upstream CDN may take end-to-end. 15s is well above the responsive
// path for any of the 5 plugin audio CDNs but short enough that a hung
// upstream connection can't pile up gateway goroutines.
//
// http.DefaultClient.Timeout is 0 ("no timeout") by default — never use
// it for an inbound-driven proxy connection.
const streamUpstreamTimeout = 15 * time.Second

// streamDownloadLongPollDur bounds how long the download-source branch of
// StreamAudio will hold the inbound request open while polling the cache
// dir for the downloaded file. 10s is a compromise:
//   - It's well above the common yt-dlp median finish time (~3-8s for a
//     3-minute song on a warm CDN), so the dominant path returns 200 in
//     one round-trip and the browser's <audio> starts Range-playing
//     without ever knowing a download was in flight.
//   - It's below Chrome's default network idle timeout (~10s) so the
//     browser doesn't cancel the request mid-poll.
//   - On timeout we return 202 + Retry-After so the frontend can keep
//     showing the loading spinner through usePlayerStore.isBuffering
//     and fire another GET /api/stream?... in 3-5s. The f3 failure
//     ceiling (5 × 10s = 50s) lets the user see a "下载失败" toast.
const streamDownloadLongPollDur = 10 * time.Second

// streamDownloadLongPollInterval is how often we re-glob the cache dir
// during a long-poll. 500ms keeps per-request syscall count bounded
// (~20 globs per long-poll round) while staying snappy enough once a
// worker finishes writing the file.
const streamDownloadLongPollInterval = 500 * time.Millisecond

// streamDownloadRetryBudget is the frontend-facing retry ceiling. The
// handler emits this in the Retry-After hint regardless of the per-round
// long-poll duration; when failure-after-N-rounds is wired into the
// frontend (l2 path), the user gets a toast once they exhaust this many
// attempts. Documented here so the handler and the frontend share the
// same number rather than two magic copies.
const streamDownloadRetryBudget = 5

// audioCacheDir returns the per-source staging directory the worker is
// expected to write downloaded audio files to (`<cache_root>/<source>`).
// Read from env on every call so a docker-compose operator can hot-reload
// by setting AUDIO_CACHE_DIR without restarting the gateway (test seams
// over `os.Setenv` work the same way).
//
// MUST stay in lockstep with the worker's choice of the same directory:
// see internal/tasks/yt_dl.go::audioCacheDir.
//
// Per-source subdirectories prevent video_id collisions across sources
// (e.g. numeric IDs from migu / soundcloud vs youtube's 11-char IDs).
func audioCacheDir(source string) string {
	root := os.Getenv("AUDIO_CACHE_DIR")
	if root == "" {
		root = "/tmp/audio_cache"
	}
	return filepath.Join(root, source)
}

// streamUpstreamClient is the http.Client used by streamFromPlugin.
// Constructed once at package init so per-call allocation stays cheap;
// http.Client is concurrency-safe and re-uses the underlying Transport.
//
// Default-Client has no Timeout and no CheckRedirect, both dangerous
// for an inbound-driven audio proxy. We:
//   - bound Timeouts at streamUpstreamTimeout to keep a stuck upstream
//     from leaking goroutines,
//   - cap redirects at 5 hops so a malicious sign-via-redirect-upstream
//     can't bounce us through internal IPs without perimeter netguard
//     protection.
var streamUpstreamClient = &http.Client{
	Timeout: streamUpstreamTimeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("upstream too many redirects")
		}
		return nil
	},
}

// fallbackUserAgent is the User-Agent we attach to the upstream GET when
// the inbound browser didn't send one. The plugin binaries fetched their
// audio URLs with a Mozilla-class UA, so we replay that fingerprint
// here — kugou specifically 403s when the upstream CDN sees a UA that
// doesn't match the signed-URL's recorded UA.
const fallbackUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/134.0.0.0 Safari/537.36"

// StreamAudio handles GET /api/stream?src=<plugin>&id=<song_id> — the
// dynamic audio-stream proxy the frontend PlayButton falls back to when
// a search result has empty `track.url` AND the source advertises
// `supports_audio_url`.
//
// Three dispatch paths, chosen by which plugin registry recognises `src`:
//
//  1. src is a registered DownloadSource (e.g. "youtube") — file-on-disk
//     path. The worker is expected to have written `<cache>/<src>/<id>.<ext>`.
//     We glob for that pattern and ServeFile it (native Range support,
//     so the bottom <audio>'s seek bar scrubs without a second request).
//     If the file is absent we enqueue a generic download task
//     (TypeDownloadGeneric, payload {source, video_id}; asynq dedups via
//     UniqueTTL(30s)) and long-poll the cache directory for up to 10s;
//     file appears → 200 + ServeFile; 10s elapsed → 202 + Retry-After so
//     the frontend (with usePlayerStore.isBuffering active) fires another
//     /api/stream GET after a short backoff. After streamDownloadRetryBudget
//     consecutive 202s the frontend decides the download failed and
//     surfaces a toast.
//
//  2. ?as_attachment=1 (download-to-browser path; see e1 row button "下载
//     到浏览器"). Same glob/long-poll logic as (1) plus an explicit
//     `Content-Disposition: attachment; filename="<filename>"` header so
//     the browser saves the file to Downloads/ instead of inline-playing.
//     <filename> may be supplied via ?filename=, sanitized to strip path
//     separators; otherwise we fall back to the on-disk basename `<id>.<ext>`.
//
//  3. src is a registered TagSource (e.g. {netease, qmusic, kuwo, kugou,
//     migu}) — fall back to the plugin's GetAudioURL gRPC call. If it
//     returns a non-empty URL, GET it with the request's Range / User-Agent
//     / Cookie headers passed through (kugou specifically 403s without
//     them) and stream the bytes back, capped at streamBodyCapBytes so a
//     hostile upstream can't OOM us.
//
// Both DownloadSource branches are enqueue-on-miss + long-poll so the
// frontend code doesn't special-case "download source" vs "tag source"
// — both look like "GET /api/stream?src=&id= returns the audio bytes".
// JWT-gated via the authed group; GET-only so BodyLimit doesn't bound here.
func StreamAudio(c *gin.Context) {
	src := strings.TrimSpace(c.Query("src"))
	id := strings.TrimSpace(c.Query("id"))
	if src == "" || id == "" {
		Failure(c, "missing src or id")
		return
	}

	// Try the DownloadSource registry first — youtube and any future
	// download-style plugin land here. We don't branch on a hardcoded
	// source-name list; the registry IS the source of truth.
	if _, err := plugin.GetDownloadSource(src); err == nil {
		streamDownload(c, src, id)
		return
	}

	// Otherwise: TagSource path.
	streamFromPlugin(c, src, id)
}

// streamDownload serves `<cache_root>/<source>/<id>.{mp3,m4a,ogg,opus,...}`
// with native Range support if present. The glob is broad on purpose so
// we don't have to know yt-dlp's resolved <ext> in advance.
//
// On miss the handler enqueues a generic download task (deduped by
// asynq UniqueTTL(30s)) and long-polls the cache directory for up to
// streamDownloadLongPollDur. If the file lands within that window, the
// inbound request returns 200 + ServeFile in a single round-trip; if
// not, we return 202 + Retry-After so the frontend re-issues the GET
// after a short backoff (and the user sees the buffering spinner via
// usePlayerStore.isBuffering through audio event listeners in PlayerBar).
//
// ?as_attachment=1 adds a `Content-Disposition: attachment` header so
// the browser saves the file to Downloads/ (the user's "下载到浏览器"
// row button path). The attachment filename is taken from ?filename=
// (sanitized) or, when absent, falls back to the on-disk basename.
func streamDownload(c *gin.Context, source, videoID string) {
	if strings.ContainsAny(videoID, "/\\") {
		// Defensive: a video id should never contain a path separator.
		// If one does, refuse the lookup so a malicious id cannot
		// escape the cache dir via glob.
		Failure(c, "invalid source id")
		return
	}
	cacheDir := audioCacheDir(source)
	attachment := c.Query("as_attachment") == "1"
	filename := strings.TrimSpace(c.Query("filename"))

	matches, err := filepath.Glob(filepath.Join(cacheDir, videoID+".*"))
	if err != nil || len(matches) == 0 {
		// Cache miss — enqueue a generic download task (deduped by asynq
		// UniqueTTL; fine-grained UniqueTTL set in handler.Download), then
		// long-poll until the file lands or the long-poll budget elapses.
		if !enqueueDownloadTask(source, videoID) {
			// Enqueue failed — return 503 without body so the frontend
			// shows the generic "试听失败" toast via the <audio>.error path.
			Failure(c, "download 异步入队失败")
			return
		}
		// Long-poll: re-glob every streamDownloadLongPollInterval until
		// streamDownloadLongPollDur elapses or we find at least one match.
		deadline := time.Now().Add(streamDownloadLongPollDur)
		for time.Now().Before(deadline) {
			if _, err := SleepCtx(c.Request.Context(), streamDownloadLongPollInterval); err != nil {
				// Client disconnected — stop polling free the goroutine.
				return
			}
			matches, _ = filepath.Glob(filepath.Join(cacheDir, videoID+".*"))
			if len(matches) > 0 {
				break
			}
		}
		if len(matches) == 0 {
			// 10s elapsed, file still absent → 202 + Retry-After so the
			// frontend keeps showing the buffering spinner and re-issues
			// GET /api/stream?... after a short backoff. Failure-after-N
			// (N=streamDownloadRetryBudget) is enforced client-side so
			// the user eventually sees a "下载失败，请稍后重试" toast.
			c.Writer.Header().Set("Retry-After", "3")
			// We also emit the retry budget so the frontend doesn't need
			// to hardcode the ceiling independently of the backend.
			c.Writer.Header().Set("X-Download-Retry-Budget", "5")
			c.Writer.WriteHeader(http.StatusAccepted)
			_, _ = c.Writer.Write([]byte(`{"result":false,"code":"download_pending","message":"下载中，请稍后重试"}`))
			return
		}
	}
	serveAudioFile(c, matches[0], attachment, filename)
}

// serveAudioFile wraps http.ServeFile with an optional attachment header.
// When attachment=true and filename is non-empty (already sanitized by the
// caller), we set Content-Disposition: attachment; filename="<filename>"
// before ServeFile. Go's http.ServeFile sets its own Content-Disposition
// for "type=attachment" when given a non-empty filename argument via
// the request URL — but we override this explicitly so the ?filename= query
// (rather than the on-disk basename) drives the saved filename when the
// user wants a human-readable download.
func serveAudioFile(c *gin.Context, absPath string, attachment bool, filename string) {
	if attachment {
		// Sanitize: strip path separators / control chars so a malicious
		// `?filename=` can't smuggle a path or inject CRLF into headers.
		safe := strings.Map(func(r rune) rune {
			if r == '/' || r == '\\' || r == ':' || r < 0x20 || r == 0x7f {
				return -1
			}
			return r
		}, filename)
		if safe == "" {
			safe = filepath.Base(absPath)
		}
		// RFC 6266: filename*=UTF-8''<percent-encoded> for non-ASCII; the
		// simple `filename="<ascii>"` form is enough here because users
		// type their own titles and we already strip control chars.
		c.Writer.Header().Set("Content-Disposition",
			`attachment; filename="`+safe+`"`)
	}
	http.ServeFile(c.Writer, c.Request, absPath)
}

// enqueueDownloadTask is the inline trigger used when stream cannot find
// the file in cache. It uses asynq.UniqueTTL(30s) so concurrent stream
// requests for the same (task type, video_id) dedup — the asynq server
// coordinates the 30s window across multiple gateway replicas if needed.
// Returns true iff the task was successfully enqueued (or already pending
// in the 30s window); false on asynq error so the caller can fail-closed.
//
// The unique key is the (TaskType, payload) tuple; asynq uses the full
// payload JSON as part of the dedup hash so a different source / video_id
// yields a different key. 30s lets the user click "play" 4x in a second
// and still only enqueue ONCE; the next song-switch triggers a fresh
// download once 30s elapsed (matches "反复下载就反复覆盖" semantics).
func enqueueDownloadTask(source, videoID string) bool {
	taskclient.Init()
	t, err := tasks.NewTypedTask(tasks.TypeDownloadGeneric,
		&tasks.DownloadPayload{
			Source:  source,
			VideoID: videoID,
			// DestDir empty: preview/cache path. "加入库" (server-side
			// persist) path goes through POST /api/download with its
			// own dest_dir carry-and-copy; this long-poll trigger is
			// ONLY for transient playback file.
		},
		asynq.Unique(30*time.Second),
		asynq.Queue("default"),
		asynq.MaxRetry(1),
		asynq.Timeout(30*60*1e9),
	)
	if err != nil {
		return false
	}
	if _, err := taskclient.Enqueue(t); err != nil {
		// asynq returns an error if a task with the same uniqueness key is
		// already pending — we treat that as success (dedup did its job).
		return err == nil || strings.Contains(err.Error(), "duplicate")
	}
	return true
}

// SleepCtx is a context-aware time.Sleep. It returns the duration it
// actually slept (best-effort) and the context error if any. We use this
// for the long-poll loop so an inbound client disconnect frees the
// gateway goroutine immediately instead of spinning out the long-poll.
func SleepCtx(ctx context.Context, dur time.Duration) (time.Duration, error) {
	select {
	case <-time.After(dur):
		return dur, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// streamFromPlugin delegates the audio-URL fetch to the registered
// Tag Source plugin, then proxies the upstream bytes back. The empty-url
// from GRPCTagSource (per the SupportsAudioURL() contract) is treated
// as "preview unavailable" — frontend gets a Failure envelope and shows
// the per-source toast.
//
// Headers forwarded upstream (whitelist-only):
//   - Range — so byte-range scrubbing from <audio> works against the
//     upstream CDN's 206 supports.
//   - User-Agent — kugou specifically 403s when UA mismatches the
//     signed-URL's recorded UA (the plugin fetched with a Mozilla UA
//     first, so we replay that fingerprint).
//   - Cookie — keeps the same session if the user's browser carried it.
//
// We deliberately DO NOT forward Authorization / Origin / Referer —
// those break cross-origin hops and the JWT Authorization belongs to
// /api/*, not the public CDN.
func streamFromPlugin(c *gin.Context, src, id string) {
	ts, err := plugin.GetTagSource(src)
	if err != nil {
		Failure(c, "unsupported source: "+src)
		return
	}
	upstreamURL, err := ts.GetAudioURL(c.Request.Context(), id)
	if err != nil {
		Failure(c, "preview unavailable: "+err.Error())
		return
	}
	if upstreamURL == "" {
		Failure(c, "preview unavailable")
		return
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), "GET", upstreamURL, nil)
	if err != nil {
		Failure(c, "build upstream request: "+err.Error())
		return
	}
	if r := c.GetHeader("Range"); r != "" {
		req.Header.Set("Range", r)
	}
	if ua := c.GetHeader("User-Agent"); ua != "" {
		req.Header.Set("User-Agent", ua)
	} else {
		req.Header.Set("User-Agent", fallbackUserAgent)
	}
	if ck := c.GetHeader("Cookie"); ck != "" {
		req.Header.Set("Cookie", ck)
	}

	resp, err := streamUpstreamClient.Do(req)
	if err != nil {
		Failure(c, "upstream error: "+err.Error())
		return
	}
	defer resp.Body.Close()

	for k, v := range resp.Header {
		c.Writer.Header()[k] = v
	}
	c.Writer.WriteHeader(resp.StatusCode)

	// Cap the proxy body so an upstream returning a multi-GB blob can't
	// exhaust gateway memory. io.CopyN caps writes at exactly n bytes;
	// if the upstream has less, it returns early without error.
	if _, err := io.CopyN(c.Writer, resp.Body, streamBodyCapBytes); err != nil && err != io.EOF {
		// Bytes already written to the wire — best we can do is log.
		// Returning Failure after a partial write would corrupt the
		// Range response on the client; browsers tolerate a short
		// truncated stream silently better than a mid-stream status flip.
		_ = err
	}
}
