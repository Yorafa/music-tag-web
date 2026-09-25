package handler

import (
	"fmt"
	"io"
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
	"go-music-tag/internal/ytdlp"
)

// Download handles POST /api/download/ — enqueue (or short-circuit from
// cache) a download task for any registered DownloadSource OR any
// registered TagSource that advertises SupportsAudioURL (migu / kugou /
// kuwo today; netease / qmusic are stubs).
//
// Mirrors the design of /api/search_music/ (which fans out across TagSource
// + DownloadSource plugins): the client supplies `source` and the handler
// routes to the matching DownloadSource's worker branch.
//
// Body:
//
//	{
//	  source: "youtube",            // registered DownloadSource name
//	  video_id: "...",              // per-source identity (yt id / soundcloud id / ...)
//	  download_path: "Artist - Title.ogg", // optional server-side destination
//	                                 // relative to MUSIC_DIR. When it ends in a
//	                                 // known audio extension it is treated as a
//	                                 // FILE path (joined with optional settings
//	                                 // subdir by the frontend). When empty the
//	                                 // file lives only in the transient cache
//	                                 // dir, NOT the library — that's the
//	                                 // "preview-only" path triggered
//	                                 // automatically by /api/stream on miss.
//	  extra_audio_format?: "ogg"    // default: ogg (vorbis)
//	}
//
// Cache short-circuit (β2): before enqueue, the handler globs the per-source
// cache dir (`audioCacheDir(source)/<id>.*`). If a file already exists (fetched
// previously by a /api/stream long-poll, or by an earlier enqueue), the
// handler copies it to SafeJoin(MUSIC_DIR, download_path) directly and
// returns 200 with `{skipped:true, dest:"..."}`. No task is enqueued.
// Only a cache miss triggers the async download task. This means:
//   - Re-clicking "加入库" 30s after a preview-listen is a single cp syscall
//     (~zero kubectl hits), not a full re-download.
//   - Each long-poll download in /api/stream pre-populates the cache so a
//     subsequent "加入库" succeeds without re-enqueue'ing.
//   - The "反复下载就反复覆盖" contract is preserved: cp overwrites the
//     library copy every time, even on cache-hit short-circuits.
//
// Today youtube is the only DownloadSource, but the handler is generic –
// adding a new download source (e.g. soundcloud) only requires registering
// its plugin server-side; no new HTTP endpoint and no new asynq task type
// (only a new `run<Source>` branch in tasks.DownloadHandler).
//
// SECURITY: ExtraAudioFmt and download_path are both user-controlled and
// must stay sanitized:
//   - ExtraAudioFmt passes through ytdlp.SanitizeYTDLPOutputFormat before
//     being injected into the yt-dlp argv (refusal of `--exec` etc).
//   - download_path must be a relative path under MUSIC_DIR; absolute paths
//     and `..` traversal are refused with a Failure envelope.
func Download(c *gin.Context) {
	var req struct {
		Source        string `json:"source" binding:"required"`
		VideoID       string `json:"video_id" binding:"required"`
		DestDir       string `json:"download_path"`
		ExtraAudioFmt string `json:"extra_audio_format"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Failure(c, "invalid request")
		return
	}

	// Validate the source is either a registered DownloadSource OR a
	// registered TagSource that advertises audio URLs. Refuse anything
	// else fail-closed.
	//
	// NOTE: for gRPC-backed TagSources, SupportsAudioURL() reads cached
	// plugin info that is only populated after the first ensureConn().
	// Calling Name() first forces that handshake, so we don't reject a
	// valid source just because the lazy dial hasn't happened yet.
	downloadOK := false
	if _, err := plugin.GetDownloadSource(req.Source); err == nil {
		downloadOK = true
	} else if ts, err := plugin.GetTagSource(req.Source); err == nil {
		_ = ts.Name()
		if ts.SupportsAudioURL() {
			downloadOK = true
		}
	}
	if !downloadOK {
		Failure(c, "download source not available: "+req.Source)
		return
	}

	// Glob-metacharacter / path-separator guard on the cache id (shared
	// with the /api/stream handler). The cache short-circuit below globs
	// `<cache>/<source>/<id>.*`; a hostile id like `*` would otherwise
	// match every cached file and could be copied into the library.
	if unsafeCacheID(req.VideoID) {
		Failure(c, "invalid video_id")
		return
	}

	// Sanitize download_path BEFORE we use it; absolute or `..` traversal
	// is refused outright so a malicious `download_path` can't write
	// outside MUSIC_DIR. Empty is fine (cache-only path, no library cp).
	if req.DestDir != "" {
		cleaned := filepath.Clean(req.DestDir)
		if filepath.IsAbs(req.DestDir) || strings.Contains(cleaned, "..") {
			Failure(c, "download_path must be a relative path under the music library root")
			return
		}
		req.DestDir = cleaned
	}

	// Extra JSON phase: build the sanitized extra block (format/quality).
	// Default to ogg when the client omits extra_audio_format so stream
	// preview and 加入库 land the same container.
	extraJSON := `{"output_format":"ogg"}`
	if req.ExtraAudioFmt != "" {
		fmt2, err := ytdlp.SanitizeYTDLPOutputFormat(req.ExtraAudioFmt)
		if err != nil {
			Failure(c, err.Error())
			return
		}
		if fmt2 != "" {
			extraJSON = `{"output_format":"` + fmt2 + `"}`
		}
	}

	// Cache short-circuit (β2): glob the per-source cache dir first. If
	// a file is already there, copy to MUSIC_DIR/<download_path> directly
	// and return Success without enqueue'ing — saves a ~30s yt-dlp run
	// when the user has already previewed the same track via /api/stream.
	//
	// The glob pattern `<id>.*` also matches worker-written siblings that
	// are NOT audio: `<id>.error` (a failed-download marker written by
	// failDownload) and yt-dlp's `<id>.<ext>.part` incomplete temps. We
	// reuse the same filterAudioMatches guard as the /api/stream handler
	// so a stale failure marker can never be short-circuited as a cache
	// hit and copied into the library as if it were audio.
	cacheDir := audioCacheDir(req.Source)
	matches, _ := filepath.Glob(filepath.Join(cacheDir, req.VideoID+".*"))
	matches = filterAudioMatches(matches)
	if len(matches) > 0 {
		// Cache hit. If DestDir is empty, we treat this as already-cached
		// but transient: pure no-op enqueue-equivalent (Success with
		// skipped=true, dest pointing at the cache file). The frontend
		// uses this for "preview already on disk" UX.
		if req.DestDir == "" {
			Success(c, "已经在缓存中，可直接试听", gin.H{
				"skipped": true,
				"dest":    matches[0],
			})
			return
		}
		// DestDir > "" refuses absolute/`..` (done above); now copy.
		destPath, err := persistCacheToLibrary(matches[0], req.DestDir)
		if err != nil {
			Failure(c, err.Error())
			return
		}
		Success(c, "已从缓存复制到本地库（加档库不需要重新下载）", gin.H{
			"skipped": true,
			"dest":    destPath,
		})
		return
	}

	// Cache miss → enqueue a generic download task. The worker will write
	// to (a) the cache dir (so the next /api/stream?src=&id= immediately
	// ServeFile's it) AND (b) MUSIC_DIR/<download_path>/ (if DestDir != "")
	// in one shot.
	taskclient.Init()
	t, err := tasks.NewTypedTask(tasks.TypeDownloadGeneric,
		&tasks.DownloadPayload{
			Source:    req.Source,
			VideoID:   req.VideoID,
			DestDir:   req.DestDir,
			ExtraJSON: extraJSON,
			// RequestedBy left empty: middleware doesn't propagate user_id
			// yet. Worker logs the owner in db.TaskRecord when this is wired.
		},
		// 30s asynq.Unique so a rapid double-click on "加入库" or
		// back-to-back enqueues from different sessions dedupes to a
		// single task. After 30s the same id's enqueue is allowed again
		// (= the "反复下载就反复覆盖" semantic the user requested).
		asynq.Unique(30*time.Second),
		asynq.Queue("default"),
		asynq.MaxRetry(1),
		asynq.Timeout(30*time.Minute),
	)
	if err != nil {
		Failure(c, err.Error())
		return
	}
	info, err := taskclient.Enqueue(t)
	if err != nil {
		// asynq Unique → ErrDuplicateTask ("task already exists").
		// Shared helper lives next to stream enqueue.
		if isAsynqDuplicate(err) {
			Success(c, "下载任务已提交（去重窗口内）", gin.H{
				"skipped": true,
				"reason":  "dedup",
			})
			return
		}
		Failure(c, "enqueue: "+err.Error())
		return
	}
	Success(c, "下载任务已提交，请稍后刷新文件列表查看", gin.H{
		"task_id": info.ID,
		"type":    info.Type,
	})
}

// persistCacheToLibrary copies the cache hit into MUSIC_DIR under
// downloadPath, returning the absolute destination path. The caller is
// responsible for validating downloadPath (no absolute paths, no `..`
// traversal).
//
// downloadPath may be:
//   - a file path ending in a known audio extension
//     (e.g. "Artist - Title.ogg" or "subdir/Artist - Title.ogg")
//   - a directory (legacy settings-only path): cache basename is appended
//
// The library copy always overwrites (matches the "反复下载就反复覆盖"
// semantic). If the parent directory doesn't exist, we MkdirAll it.
// When the requested extension differs from the real cache extension,
// the cache extension wins so the file stays playable.
func persistCacheToLibrary(cachePath, downloadPath string) (string, error) {
	musicRoot := os.Getenv("MUSIC_DIR")
	if musicRoot == "" {
		// Worker and gateway share env; absence means the operator forgot
		// to wire MUSIC_DIR — refuse the cp path so the user knows.
		return "", fmt.Errorf("MUSIC_DIR env not set; add-to-library requires it")
	}
	destPath := resolveLibraryDestPath(musicRoot, downloadPath, cachePath)
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return "", fmt.Errorf("mkdir %q: %w", filepath.Dir(destPath), err)
	}
	srcF, err := os.Open(cachePath)
	if err != nil {
		return "", fmt.Errorf("open cache %q: %w", cachePath, err)
	}
	defer srcF.Close()
	dstF, err := os.Create(destPath)
	if err != nil {
		return "", fmt.Errorf("create library %q: %w", destPath, err)
	}
	defer dstF.Close()
	if _, err := io.Copy(dstF, srcF); err != nil {
		return "", fmt.Errorf("copy cache → library: %w", err)
	}
	return destPath, nil
}

// resolveLibraryDestPath is the gateway twin of tasks.resolveLibraryDest:
// file-path vs directory semantics for download_path. Kept local so the
// gateway package does not need to export the worker helper.
func resolveLibraryDestPath(musicRoot, dest, cachePath string) string {
	base := filepath.Base(cachePath)
	cacheExt := filepath.Ext(base)
	destExt := strings.ToLower(filepath.Ext(dest))
	if audioext.IsStreamableExt(destExt) {
		if cacheExt != "" && !strings.EqualFold(destExt, cacheExt) {
			dest = strings.TrimSuffix(dest, filepath.Ext(dest)) + cacheExt
		}
		return filepath.Join(musicRoot, dest)
	}
	// Directory (legacy settings-only path): keep the cache basename.
	return filepath.Join(musicRoot, dest, base)
}
