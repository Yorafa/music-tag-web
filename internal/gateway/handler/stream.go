package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"

	"go-music-tag/internal/audioext"
	"go-music-tag/internal/plugin"
	"go-music-tag/internal/taskclient"
	"go-music-tag/internal/tasks"
)

// sanitizeLogField strips control characters (CRLF + <0x20 + 0x7f) and
// truncates an event-tag field for log lines / user-visible messages.
// Defence-in-depth so a hostile `id=` query / upstream payload can't
// smuggle CRLF into docker logs (breaking grep) or JSON envelope
// (breaking axios interceptor parsing).
//
// Truncation is at 64 bytes; song ids are short numerics so this is
// generous. Type stays string so callers don't need unquoting in
// printf verbs.
func sanitizeLogField(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if r == 0 || r == '\n' || r == '\r' || r < 0x20 || r == 0x7f {
			return '?'
		}
		return r
	}, s)
	if len(s) > max {
		s = s[:max] + "..."
	}
	return s
}

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
		// 400 — caller asked for an invalid URL; nothing else can route
		// meaningfully. FailureStatus (not Failure) so <audio> consumers
		// still see this as an error and the onerror path fires a toast.
		FailureStatus(c, http.StatusBadRequest, "missing src or id")
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
// unsafeCacheID reports whether id contains characters that would alter
// filepath.Glob semantics (`*?[`) or escape the per-source cache dir
// (`/\`). The stream + download handlers glob `<cache>/<source>/<id>.*`
// with the raw id, so a hostile id must not be able to expand the pattern
// across the whole cache dir (e.g. `id=*` → matches every cached file) or
// walk out of the source subdir. Legit ids — youtube 11-char IDs, kuwo /
// migu numeric rids — never contain these characters, so the guard is
// lossless in practice.
func unsafeCacheID(id string) bool {
	return strings.ContainsAny(id, `/\*?[]`)
}

func streamDownload(c *gin.Context, source, videoID string) {
	if unsafeCacheID(videoID) {
		// Defensive: refuse the lookup so a malicious id cannot escape
		// the cache dir via glob. 400 + envelope so <audio>'s onerror
		// path is reliably triggered (see FailureStatus comment).
		FailureStatus(c, http.StatusBadRequest, "invalid source id")
		return
	}
	cacheDir := audioCacheDir(source)
	attachment := c.Query("as_attachment") == "1"
	filename := strings.TrimSpace(c.Query("filename"))

	matches, _ := filepath.Glob(filepath.Join(cacheDir, videoID+".*"))
	matches = filterAudioMatches(matches)
	if len(matches) == 0 {
		// Prefer a concrete worker failure over another 202 loop. The
		// worker writes <id>.error next to the cache dir when yt-dlp
		// exhausts retries (signature / format / bot checks).
		if msg := tasks.ReadDownloadError(source, videoID); msg != "" {
			// 502 so waitForStreamReady stops retrying 202 and surfaces
			// the real reason (vs endless "下载中").
			FailureStatus(c, http.StatusBadGateway, "download failed: "+truncateErr(msg, 240))
			return
		}
		// Cache miss / only incomplete stubs — enqueue a generic download
		// task (deduped by asynq UniqueTTL), then long-poll until a
		// non-empty audio file lands or the long-poll budget elapses.
		if !enqueueDownloadTask(source, videoID) {
			// Enqueue failed — return 503 + envelope so <audio>.error
			// fires reliably (vs 200+JSON which browsers silently drop).
			FailureStatus(c, http.StatusServiceUnavailable, "download 异步入队失败")
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
			if msg := tasks.ReadDownloadError(source, videoID); msg != "" {
				FailureStatus(c, http.StatusBadGateway, "download failed: "+truncateErr(msg, 240))
				return
			}
			matches, _ = filepath.Glob(filepath.Join(cacheDir, videoID+".*"))
			// Ignore .error / .part / empty stubs.
			matches = filterAudioMatches(matches)
			if len(matches) > 0 {
				break
			}
		}
		if len(matches) == 0 {
			// Check once more for a late-arriving failure marker.
			if msg := tasks.ReadDownloadError(source, videoID); msg != "" {
				FailureStatus(c, http.StatusBadGateway, "download failed: "+truncateErr(msg, 240))
				return
			}
			// 10s elapsed, file still absent → 202 + Retry-After so the
			// frontend keeps showing the buffering spinner and re-issues
			// GET /api/stream?... after a short backoff. Failure-after-N
			// (N=streamDownloadRetryBudget) is enforced client-side so
			// the user eventually sees a "下载失败，请稍后重试" toast.
			c.Writer.Header().Set("Retry-After", "3")
			// We also emit the retry budget so the frontend doesn't need
			// to hardcode the ceiling independently of the backend.
			c.Writer.Header().Set("X-Download-Retry-Budget", "5")
			// charset=utf-8 so Chinese "下载中…" is not Latin-1 mojibake in
			// DevTools / any intermediate that re-decodes the body.
			c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
			c.Writer.WriteHeader(http.StatusAccepted)
			_, _ = c.Writer.Write([]byte(`{"result":false,"code":"download_pending","message":"下载中，请稍后重试"}`))
			return
		}
	}
	serveAudioFile(c, matches[0], attachment, filename)
}

// filterAudioMatches drops non-audio cache siblings (notably <id>.error
// and yt-dlp's `<id>.mp3.part` incomplete temps) and zero-byte stubs.
// Zero-byte / tiny files must not be served: ServeFile + Range bytes=0-1
// returns 416 Requested Range Not Satisfiable, which the frontend used
// to treat as a hard failure mid-download.
func filterAudioMatches(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		// audioext.IsStreamablePath is the shared whitelist (REVIEW.md
		// P0-4 / P2-1). It notably INCLUDES .flac, which this switch used
		// to omit while resolveLibraryDest accepted it — a lossless
		// download therefore landed in the cache and could never be
		// served.
		if !audioext.IsStreamablePath(p) {
			continue
		}
		// Reject incomplete / empty stubs. Real audio previews are well
		// above 1 KiB; keep a low floor so short clips still pass.
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() || fi.Size() < 1024 {
			continue
		}
		out = append(out, p)
	}
	return out
}

func truncateErr(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "..."
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
			// Default ogg so cache/preview matches 加入库 extension.
			ExtraJSON: `{"output_format":"ogg"}`,
		},
		asynq.Unique(30*time.Second),
		asynq.Queue("default"),
		asynq.MaxRetry(1),
		asynq.Timeout(30*time.Minute),
	)
	if err != nil {
		log.Printf("[stream] NewTypedTask download failed src=%s id=%s: %v",
			sanitizeLogField(source, 32), sanitizeLogField(videoID, 64), err)
		return false
	}
	if _, err := taskclient.Enqueue(t); err != nil {
		// asynq Unique: ErrDuplicateTask.Error() == "task already exists"
		// (NOT the substring "duplicate"). Treat as success so concurrent
		// play clicks keep long-polling the same in-flight download.
		if isAsynqDuplicate(err) {
			return true
		}
		log.Printf("[stream] enqueue download failed src=%s id=%s: %v",
			sanitizeLogField(source, 32), sanitizeLogField(videoID, 64), err)
		return false
	}
	return true
}

// isAsynqDuplicate reports asynq Unique-window collisions. Prefer
// errors.Is(ErrDuplicateTask); also accept historical string forms
// used by older call sites / wrapped errors.
func isAsynqDuplicate(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, asynq.ErrDuplicateTask) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "task already exists") ||
		strings.Contains(msg, "duplicate") ||
		strings.Contains(msg, "already enqueued")
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

// applyStreamUpstreamHeaders copies the inbound request's whitelist
// headers onto an upstream request we are about to send. Used for both the
// primary request and the HEAD→GET retry so the two stay identical:
//   - Range — so byte-range scrubbing from <audio> works against the
//     upstream CDN's 206 supports.
//   - User-Agent — kugou specifically 403s when UA mismatches the
//     signed-URL's recorded UA (the plugin fetched with a Mozilla UA
//     first, so we replay that fingerprint).
//   - Cookie — keeps the same session if the user's browser carried it.
//   - Referer — some upstream CDNs (migu in particular) gate signed URLs
//     on Referer; replaying the inbound value keeps the proxy consistent
//     with the plugin's direct fetch.
//
// We deliberately DO NOT forward Authorization / Origin — those break
// cross-origin hops and the JWT Authorization belongs to /api/*, not the
// public CDN.
func applyStreamUpstreamHeaders(req *http.Request, c *gin.Context) {
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
	if ref := c.GetHeader("Referer"); ref != "" {
		req.Header.Set("Referer", ref)
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
		// 400 — the caller asked for a source name we don't know about;
		// this is a client-side mistake, not a transient infrastructure
		// failure.
		FailureStatus(c, http.StatusBadRequest, "unsupported source: "+src)
		return
	}
	upstreamURL, err := ts.GetAudioURL(c.Request.Context(), id)
	if err != nil {
		// 502 — upstream plugin reported an error reaching its CDN.
		// distinct from the empty-URL case below so the front-end can
		// tease them apart in the toast if desired. Ops-side debug line
		// carries the sanitised id and the err details into docker logs
		// so a future "preview unavailable" report is greppable.
		log.Printf("[stream] 502 src=%s id=%s reason=plugin_get_audio_err err=%v",
			src, sanitizeLogField(id, 64), err)
		FailureStatus(c, http.StatusBadGateway, "preview unavailable: "+err.Error())
		return
	}
	if upstreamURL == "" {
		// 502 — most common: source is documented as a stub (netease /
		// qmusic) OR the upstream CDN returned 200 + empty data.url /
		// play_url / playUrl (kuwo Secret rotated, kg missing dfid
		// cookie, migu cohort retired). The empty URL is the plugin's
		// signal that preview cannot be served; the gateway surfaces
		// that as 502 so <audio>.error reliably fires and PlayButton's
		// onMediaError handler pushes the warning toast.
		//
		// The widened message includes `source=<src>` so a user opening
		// DevTools can read which plugin path failed (was previously
		// indistinguishable from the netease/qmusic designed stub case).
		log.Printf("[stream] 502 src=%s id=%s reason=empty_url upstream_body=%s",
			src, sanitizeLogField(id, 64), "<see plugin log line for body>")
		FailureStatus(c, http.StatusBadGateway,
			fmt.Sprintf("preview unavailable: source=%s", src))
		return
	}

	// Mirror the inbound method upstream. The frontend's waitForStreamReady
	// preflights /api/stream with HEAD before committing <audio>.src; if we
	// hardcoded GET here that preflight would pull the ENTIRE upstream body
	// from the CDN just to have net/http discard it for the HEAD response —
	// a 2×-bandwidth waste on every preview click. Forwarding HEAD as HEAD
	// makes the preflight cheap (headers only) and the subsequent GET stays
	// untouched.
	method := c.Request.Method
	if method != http.MethodGet && method != http.MethodHead {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), method, upstreamURL, nil)
	if err != nil {
		// 500 — we cannot even build the upstream request; that's a
		// bug in the handler, not an upstream failure.
		log.Printf("[stream] 500 src=%s id=%s reason=build_upstream_req err=%v",
			src, sanitizeLogField(id, 64), err)
		FailureStatus(c, http.StatusInternalServerError, "build upstream request: "+err.Error())
		return
	}
	applyStreamUpstreamHeaders(req, c)

	resp, err := streamUpstreamClient.Do(req)
	if err != nil {
		// 502 — we successfully built the upstream request but the
		// network round-trip failed. Boundary between "plugin/Kong"
		// and the source CDN; the browser sees upstream as untrusted.
		log.Printf("[stream] 502 src=%s id=%s reason=upstream_network err=%v upstream_url=%s",
			src, sanitizeLogField(id, 64), err, sanitizeLogField(upstreamURL, 128))
		FailureStatus(c, http.StatusBadGateway, "upstream error: "+err.Error())
		return
	}
	defer resp.Body.Close()

	// HEAD-preflight resilience: a few signed-URL CDNs (kuwo / kugou) reject
	// HEAD with 403/404 while answering GET fine. The frontend's
	// waitForStreamReady only falls back to GET on 405/501, so a HEAD-only
	// CDN would otherwise fail the preflight where it previously succeeded
	// (the old code always issued upstream GET). Retry once as GET and let
	// the HEAD inbound discard the body at the net/http layer — the real
	// <audio> GET still fetches the bytes. CDNs that support HEAD keep the
	// zero-body bandwidth win.
	if method == http.MethodHead && (resp.StatusCode < 200 || resp.StatusCode > 299) {
		_ = resp.Body.Close()
		retry, rerr := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, upstreamURL, nil)
		if rerr != nil {
			log.Printf("[stream] 500 src=%s id=%s reason=build_upstream_get_retry err=%v",
				src, sanitizeLogField(id, 64), rerr)
			FailureStatus(c, http.StatusInternalServerError, "build upstream request: "+rerr.Error())
			return
		}
		// Re-apply the same whitelist header forwarding as the main request
		// so the retried GET is indistinguishable from a direct user GET.
		applyStreamUpstreamHeaders(retry, c)
		retryResp, rerr := streamUpstreamClient.Do(retry)
		if rerr != nil {
			log.Printf("[stream] 502 src=%s id=%s reason=upstream_network_get_retry err=%v upstream_url=%s",
				src, sanitizeLogField(id, 64), rerr, sanitizeLogField(upstreamURL, 128))
			FailureStatus(c, http.StatusBadGateway, "upstream error: "+rerr.Error())
			return
		}
		// NOTE: the outer `defer resp.Body.Close()` already captured the
		// FIRST response's body at defer time — reassigning `resp` does NOT
		// change what it closes. Close the retried response explicitly or
		// its connection leaks from the transport pool on every HEAD
		// preflight that hits a HEAD-rejecting CDN.
		defer retryResp.Body.Close()
		resp = retryResp
		// method stays "HEAD" below: net/http discards the body writes on a
		// HEAD response, so the retried GET bytes are fetched and dropped —
		// the <audio> GET will re-fetch them with full Range support.
	}

	// The body below is capped at streamBodyCapBytes. If the upstream
	// declares a length ABOVE the cap, io.CopyN truncates mid-stream and
	// the advertised Content-Length would make the browser wait for bytes
	// that never arrive (a hung <audio>, no error event). Only in that
	// case drop Content-Length so net/http falls back to close-delimited
	// framing and the client learns the stream ended from EOF. Under-cap
	// responses (and Range / 206 partials) keep their exact byte count.
	// Content-Range is always preserved — that's what <audio> uses to seek.
	if resp.ContentLength > streamBodyCapBytes {
		resp.Header.Del("Content-Length")
	}
	for k, v := range resp.Header {
		c.Writer.Header()[k] = v
	}
	c.Writer.WriteHeader(resp.StatusCode)

	// HEAD preflight carries no body — net/http already gives us NoBody for
	// the upstream HEAD, but skip the copy explicitly so we never stream a
	// body into a HEAD response if a future upstream misbehaves.
	if method == http.MethodHead {
		return
	}

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
