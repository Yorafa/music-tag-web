package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go-music-tag/internal/audiocache"
	"go-music-tag/internal/audioext"
	"go-music-tag/internal/audit"
	"go-music-tag/internal/db"
	"go-music-tag/internal/netguard"
	"go-music-tag/internal/plugin"
	"go-music-tag/internal/utils"
	"go-music-tag/internal/ytdlp"
	"gorm.io/gorm"
)

// upsertDownloadFolder records a downloaded file as a music_folder row.
//
// A download has two possible keys — the video id (its long-term identity:
// the same video fetched again must update one row, not accumulate copies)
// and the path it landed on (unique since REVIEW.md P2-5, and already
// claimed by the scanner for anything it has walked). The two can disagree,
// so all four combinations are handled explicitly:
//
//	neither known          → insert
//	uid known, path free   → move the existing row to the new path
//	uid known, path taken  → the scanner owns that path; refresh its row
//	                        and leave the video id where it is, or two
//	                        rows would claim the same uid and the folder
//	                        tree would fork
//	uid free, path taken   → adopt the scanner's row and take the video id
//
// This also replaces a FirstOrCreate that never actually updated an
// existing row (GORM's FirstOrCreate only inserts), so a re-download used
// to leave the stored size and file type stale.
func upsertDownloadFolder(tx *gorm.DB, folder db.Folder) error {
	var byUID, byPath db.Folder
	uidErr := tx.Where("uid = ?", folder.UID).First(&byUID).Error
	pathErr := tx.Where("path = ?", folder.Path).First(&byPath).Error
	if uidErr != nil && !errors.Is(uidErr, gorm.ErrRecordNotFound) {
		return fmt.Errorf("look up folder uid %q: %w", folder.UID, uidErr)
	}
	if pathErr != nil && !errors.Is(pathErr, gorm.ErrRecordNotFound) {
		return fmt.Errorf("look up folder path %q: %w", folder.Path, pathErr)
	}
	uidFound, pathFound := uidErr == nil, pathErr == nil

	var (
		target     int64
		includeUID bool
	)
	switch {
	case !uidFound && !pathFound:
		return tx.Create(&folder).Error
	case uidFound && (!pathFound || byPath.ID == byUID.ID):
		target, includeUID = byUID.ID, true
	case uidFound:
		// The video is known but the file landed somewhere the scanner has
		// already indexed. Refresh that row; the video id stays with the
		// old one.
		target = byPath.ID
	default:
		// The path is indexed but the video is new: adopt the row.
		target, includeUID = byPath.ID, true
	}

	updates := map[string]any{
		"path":       folder.Path,
		"name":       folder.Name,
		"size":       folder.Size,
		"file_type":  folder.FileType,
		"updated_at": folder.UpdatedAt,
	}
	if includeUID {
		updates["uid"] = folder.UID
	}
	return tx.Model(&db.Folder{}).Where("id = ?", target).Updates(updates).Error
}

// audioCacheDir returns the per-source staging directory downloaded audio
// files land in. The rule itself (env var, default, per-source subdir)
// lives in internal/audiocache, which also owns the size-capped pruning
// that reads this same tree — the worker and the gateway must not be able
// to disagree about where the cache is.
func audioCacheDir(source string) string {
	return audiocache.Dir(source)
}

// DownloadHandler is the unified, source-routed asynq handler for the
// `download:generic` task type. payload.Source dispatches to the
// matching download branch:
//   - DownloadSource (youtube today): the actual yt-dlp exec lives in the
//     plugin (internal/plugin/youtube) — the worker only carries the
//     yt-dlp tuning knobs over gRPC (plugin.DownloadOptions) and does the
//     cache/library copy + DB records. This keeps the worker image pure
//     Go (no python3 / yt-dlp / ffmpeg).
//   - TagSource with SupportsAudioURL (migu / kugou / kuwo): resolve a
//     short-lived audio URL via gRPC and fetch the bytes directly.
//
// Adding a new download source (e.g. soundcloud) means registering its
// DownloadSource plugin — NOT a new asynq task type and NOT a new
// endpoint.
//
// Two landing directories are involved:
//   - Cache dir (preview path): audioCacheDir(source)/<id>.<ext>
//     written by the plugin (or the audio-URL fetch). Shared with the
//     gateway's /api/stream glob so ServeFile picks the file up.
//   - DestDir (加入库 path): when non-empty, the file is copied from
//     the cache dir to SafeJoin(MusicRoot, DestDir)/<id>.<ext> AFTER
//     the download finishes, so preview cache stays short-lived and
//     the library gets a permanent copy. When empty, the worker just
//     leaves the file in cache (this is the preview-only path the
//     /api/stream proxy triggers).
//
// SECURITY: ExtraJSON is untrusted input (any client can POST
// /api/download/). The gateway, this handler, AND the plugin each run
// the values through ytdlp.SanitizeYTDLPFormat/OutputFormat/Quality
// before anything reaches argv, so yt-dlp flags like `--exec` are
// refused at every boundary (incl. replayed/tampered tasks).
type DownloadHandler struct {
	DB        *gorm.DB
	MusicRoot string
	// OnLibraryChanged fires after a download, so a track added to the
	// library gets a duration without waiting for the indexer's re-arm
	// chain to be running. See librarychanged.go.
	OnLibraryChanged LibraryChangedHook
}

func NewDownloadHandler(gormDB *gorm.DB, musicRoot string) *DownloadHandler {
	return &DownloadHandler{DB: gormDB, MusicRoot: musicRoot}
}

type downloadExtra struct {
	Format       string `json:"format,omitempty"`        // e.g. "bestaudio/best"
	OutputFormat string `json:"output_format,omitempty"` // e.g. "mp3" / "m4a" / "ogg"
	Quality      string `json:"quality,omitempty"`       // e.g. "192"
}

func (h *DownloadHandler) ProcessTask(ctx context.Context, t Task) error {
	payload, ok := t.Payload.(*DownloadPayload)
	if !ok {
		return fmt.Errorf("download: invalid payload type %T", t.Payload)
	}
	if payload.Source == "" {
		return fmt.Errorf("download: empty source")
	}
	// A download is one of the two moments the library gains a file, so
	// the index has to hear about it. Deferred, because the file is on
	// disk and its index row is written whichever branch runs below — and a
	// hook that only fires on the happy path would miss the ones that
	// fail after the copy.
	defer notifyLibraryChanged(h.OnLibraryChanged)

	// DownloadSource branch first: any source registered as a
	// DownloadSource (youtube today, soundcloud tomorrow) delegates the
	// actual exec to its plugin via gRPC.
	if ds, err := plugin.GetDownloadSource(payload.Source); err == nil {
		return h.runDownloadSource(ctx, payload, ds)
	}
	// TagSource branch: any source registered as a TagSource that
	// advertises SupportsAudioURL can be downloaded by resolving its
	// audio URL and fetching the bytes.
	//
	// Name() forces a lazy gRPC handshake so SupportsAudioURL() reads
	// the real plugin capability instead of returning false on a cold
	// adapter (g.info is nil before the first ensureConn()).
	if ts, err := plugin.GetTagSource(payload.Source); err == nil {
		_ = ts.Name()
		if ts.SupportsAudioURL() {
			return h.runTagSource(ctx, payload, ts)
		}
	}
	return fmt.Errorf("download: source %q not yet supported by worker", payload.Source)
}

// runDownloadSource is the DownloadSource branch: delegate the actual
// download exec to the plugin over gRPC (the plugin runs yt-dlp and
// lands <id>.<ext> in its workDir = AUDIO_CACHE_DIR/<source>), then do
// the worker-side persistence: library copy (DestDir) + Folder/TaskRecord
// DB rows.
//
// The plugin sanitizes the knobs again before argv; we sanitize here too
// so a replayed/tampered task fails fast with a clear error instead of
// round-tripping garbage to the plugin.
func (h *DownloadHandler) runDownloadSource(ctx context.Context, payload *DownloadPayload, ds plugin.DownloadSource) error {
	// 0) Sanitize persisted ExtraJSON (defence-in-depth — gateway already
	//    vetted, but DB-replay / tamper paths still reach the worker).
	extra := downloadExtra{
		Format:       "bestaudio/best",
		OutputFormat: "ogg",
		Quality:      "192",
	}
	if payload.ExtraJSON != "" {
		if err := json.Unmarshal([]byte(payload.ExtraJSON), &extra); err != nil {
			return fmt.Errorf("download: parse ExtraJSON: %w", err)
		}
	}
	cleanFmt, err := ytdlp.SanitizeYTDLPFormat(extra.Format)
	if err != nil {
		return fmt.Errorf("download: format: %w", err)
	}
	cleanOut, err := ytdlp.SanitizeYTDLPOutputFormat(extra.OutputFormat)
	if err != nil {
		return fmt.Errorf("download: output_format: %w", err)
	}
	cleanQuality, err := ytdlp.SanitizeYTDLPQuality(extra.Quality)
	if err != nil {
		return fmt.Errorf("download: quality: %w", err)
	}

	// 1) ensure cache dir
	downloadsDir := audioCacheDir(payload.Source)
	if err := os.MkdirAll(downloadsDir, 0o755); err != nil {
		return fmt.Errorf("download: mkdir cache: %w", err)
	}
	// Clear any previous failure marker so a successful re-download is not
	// poisoned by a stale .error from an older yt-dlp breakage.
	_ = os.Remove(downloadErrorPath(downloadsDir, payload.VideoID))

	// 2) delegate the exec to the plugin (runs yt-dlp in its own image)
	res, err := ds.Download(ctx, payload.VideoID, "", plugin.DownloadOptions{
		Format:       cleanFmt,
		OutputFormat: cleanOut,
		Quality:      cleanQuality,
	})
	if err != nil {
		return h.failDownload(downloadsDir, payload.VideoID,
			fmt.Errorf("download: plugin %q call failed: %w", payload.Source, err))
	}
	if !res.Success {
		msg := strings.TrimSpace(res.Error)
		if msg == "" {
			msg = "plugin returned failure without detail"
		}
		return h.failDownload(downloadsDir, payload.VideoID, fmt.Errorf("download: %s", msg))
	}

	// 3) locate downloaded file (mp3 / m4a / ogg / original). Prefer the
	//    plugin's reported path; fall back to scanning the shared cache
	//    dir for older plugin builds that omit FilePath.
	dlFile := strings.TrimSpace(res.FilePath)
	dlSize := int64(0)
	if dlFile != "" {
		if fi, statErr := os.Stat(dlFile); statErr == nil {
			dlSize = fi.Size()
		}
	} else {
		dlFile, dlSize, err = findDownloadedFile(downloadsDir, payload.VideoID)
		if err != nil {
			return h.failDownload(downloadsDir, payload.VideoID, err)
		}
	}

	// 4) If DestDir is supplied (加入库 path), copy the cache file into the
	//    library. DestDir is relative to MusicRoot and may be either:
	//      - a file path ending in a known audio extension
	//        (e.g. "Artist - Title.ogg" or "album/Artist - Title.ogg")
	//      - a directory (legacy / settings-only path) in which case we
	//        keep the cache basename (`<id>.ext`).
	//    Preview cache keeps its transient `<id>.ext` copy for stream.
	libraryPath := dlFile
	if payload.DestDir != "" && h.MusicRoot != "" {
		cleanDest, destErr := utils.SafeRelPath(payload.DestDir)
		if destErr != nil {
			return fmt.Errorf("download: dest_dir must be relative path under music root, got %q", payload.DestDir)
		}
		destPath, destErr := resolveLibraryDest(h.MusicRoot, cleanDest, dlFile)
		if destErr != nil {
			return fmt.Errorf("download: dest_dir %q: %w", payload.DestDir, destErr)
		}
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return fmt.Errorf("download: mkdir dest %q: %w", filepath.Dir(destPath), err)
		}
		if err := copyFile(dlFile, destPath); err != nil {
			return fmt.Errorf("download: copy to library %q: %w", destPath, err)
		}
		libraryPath = destPath
		dlFile = destPath
	}

	// 5) persist: 1 Folder + 1 TaskRecord
	now := time.Now()
	folder := db.Folder{
		UID:       payload.VideoID, // re-use video id as unique folder UID
		ParentID:  "",
		Name:      filepath.Base(dlFile),
		FileType:  audioext.FileTypeForRow(dlFile),
		Path:      dlFile,
		Size:      dlSize,
		UpdatedAt: now,
	}
	if err := upsertDownloadFolder(h.DB.WithContext(ctx), folder); err != nil {
		return fmt.Errorf("download: upsert folder: %w", err)
	}

	rec := db.TaskRecord{
		TaskID:    payload.RequestedBy,
		Batch:     payload.Batch,
		FileName:  filepath.Base(dlFile),
		FullPath:  dlFile,
		Source:    payload.Source,
		UID:       payload.VideoID,
		FileType:  audioext.FileTypeForRow(dlFile),
		Status:    "completed",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := h.DB.WithContext(ctx).Create(&rec).Error; err != nil {
		return fmt.Errorf("download: create task record: %w", err)
	}
	audit.Log(ctx, audit.ActionDownload, filepath.Base(dlFile), payload.RequestedBy, audit.StatusSuccess, 1, map[string]interface{}{
		"source":   payload.Source,
		"video_id": payload.VideoID,
		"path":     dlFile,
	}, nil)
	_ = libraryPath
	return nil
}

// downloadGuard is the SSRF guard applied to plugin-supplied audio URLs
// in runTagSource (REVIEW.md P1-2). Package-level so the resolver is
// resolved once; tests inject their own via downloadGuard.Resolver.
var downloadGuard = netguard.NewGuard()

// downloadCheckRedirect is the CheckRedirect policy for the audio-download
// client. Package-level (rather than an inline closure) so a regression test
// can exercise the per-hop SSRF re-validation directly via downloadGuard.
//
// The initial audioURL is checked once in runTagSource, but a public URL can
// 302 to http://169.254.169.254/ or an RFC 1918 host; the hop-count cap alone
// would follow that pivot. Mirrors netguard.SafeHTTPGet's CheckRedirect.
func downloadCheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return fmt.Errorf("too many redirects")
	}
	if err := downloadGuard.Validate(req.Context(), req.URL.String()); err != nil {
		return err
	}
	return nil
}

// runTagSource downloads a TagSource track by asking the plugin for a
// short-lived audio URL and saving the bytes. This lets users "加入库"
// from sources like migu / kugou / kuwo, not just YouTube.
//
// Security: audioURL is plugin-supplied, so it is SSRF-relevant input —
// the guard below rejects private / loopback / link-local targets before
// we connect (REVIEW.md P1-2). We also use an explicit outbound client
// with a timeout and a 50 MiB body cap so a malicious upstream can't
// exhaust the worker. We send a generic browser User-Agent and a
// music-platform Referer because several CDNs 403 without them.
func (h *DownloadHandler) runTagSource(ctx context.Context, payload *DownloadPayload, ts plugin.TagSource) error {
	// Resolve the upstream audio URL.
	audioURL, err := ts.GetAudioURL(ctx, payload.VideoID)
	if err != nil {
		return fmt.Errorf("download: tag source %q GetAudioURL failed: %w", payload.Source, err)
	}
	if audioURL == "" {
		return fmt.Errorf("download: tag source %q returned empty audio URL", payload.Source)
	}

	u, err := url.Parse(audioURL)
	if err != nil {
		return fmt.Errorf("download: invalid audio URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("download: unsupported audio URL scheme %q", u.Scheme)
	}

	// SSRF gate (REVIEW.md P1-2). audioURL came from the plugin's
	// GetAudioURL, so a buggy or compromised plugin could aim the worker at
	// 127.0.0.1 or a link-local metadata endpoint. Default-deny, matching
	// the gateway's stream handler and the cover-art fetch path.
	if err := downloadGuard.Validate(ctx, audioURL); err != nil {
		return fmt.Errorf("download: audio URL rejected by SSRF guard (%s): %w", payload.Source, err)
	}

	// Fetch the audio bytes. Cap redirects so a misbehaving upstream can't
	// bounce us through an open redirect chain.
	req, err := http.NewRequestWithContext(ctx, "GET", audioURL, nil)
	if err != nil {
		return fmt.Errorf("download: build request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/134.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "audio/*,*/*")

	client := &http.Client{
		Timeout:       5 * time.Minute,
		CheckRedirect: downloadCheckRedirect,
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download: fetch audio: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("download: upstream HTTP %d", resp.StatusCode)
	}

	// Determine extension. Prefer Content-Type, fall back to URL path.
	downloadsDir := audioCacheDir(payload.Source)
	if err := os.MkdirAll(downloadsDir, 0o755); err != nil {
		return fmt.Errorf("download: mkdir cache: %w", err)
	}
	cleanURL := strings.TrimSpace(u.Path)
	if cleanURL == "" {
		cleanURL = "/audio"
	}
	cacheExt := extFromResponse(resp, cleanURL)
	cachePath := filepath.Join(downloadsDir, payload.VideoID+cacheExt)

	// Write the cache file with a hard cap on body size.
	const maxBytes = 50 << 20
	if err := writeAudioFile(cachePath, resp.Body, maxBytes); err != nil {
		return fmt.Errorf("download: write cache: %w", err)
	}

	// Copy into library if requested.
	libraryPath := cachePath
	if payload.DestDir != "" && h.MusicRoot != "" {
		cleanDest, destErr := utils.SafeRelPath(payload.DestDir)
		if destErr != nil {
			return fmt.Errorf("download: dest_dir must be relative path under music root, got %q", payload.DestDir)
		}
		destPath, destErr := resolveLibraryDest(h.MusicRoot, cleanDest, cachePath)
		if destErr != nil {
			return fmt.Errorf("download: dest_dir %q: %w", payload.DestDir, destErr)
		}
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return fmt.Errorf("download: mkdir dest %q: %w", filepath.Dir(destPath), err)
		}
		if err := copyFile(cachePath, destPath); err != nil {
			return fmt.Errorf("download: copy to library %q: %w", destPath, err)
		}
		libraryPath = destPath
	}

	// Persist metadata.
	now := time.Now()
	folder := db.Folder{
		UID:       payload.VideoID,
		ParentID:  "",
		Name:      filepath.Base(libraryPath),
		FileType:  audioext.FileTypeForRow(libraryPath),
		Path:      libraryPath,
		Size:      0,
		UpdatedAt: now,
	}
	if info, err := os.Stat(libraryPath); err == nil {
		folder.Size = info.Size()
	}
	if err := upsertDownloadFolder(h.DB.WithContext(ctx), folder); err != nil {
		return fmt.Errorf("download: upsert folder: %w", err)
	}

	rec := db.TaskRecord{
		TaskID:    payload.RequestedBy,
		Batch:     payload.Batch,
		FileName:  filepath.Base(libraryPath),
		FullPath:  libraryPath,
		Source:    payload.Source,
		UID:       payload.VideoID,
		FileType:  audioext.FileTypeForRow(libraryPath),
		Status:    "completed",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := h.DB.WithContext(ctx).Create(&rec).Error; err != nil {
		return fmt.Errorf("download: create task record: %w", err)
	}
	audit.Log(ctx, audit.ActionDownload, filepath.Base(libraryPath), payload.RequestedBy, audit.StatusSuccess, 1, map[string]interface{}{
		"source":   payload.Source,
		"video_id": payload.VideoID,
		"path":     libraryPath,
	}, nil)
	return nil
}

// extFromResponse returns the file extension to store the fetched audio
// under. It prefers the response Content-Type, then the URL path extension,
// then audioext.DefaultExt.
//
// The returned extension is GUARANTEED to be a member of the shared
// streamable set, because a cache file whose extension the gateway's
// filterAudioMatches does not recognise is invisible to /api/stream — the
// download "succeeds" and playback then 202-loops until the frontend's
// retry budget is exhausted (REVIEW.md P0-4).
//
// This previously used mime.ExtensionsByType and returned exts[0], which on
// this platform yields:
//
//	audio/ogg -> .oga   (not streamable — silently broke every ogg fetch)
//	audio/mp4 -> .f4a   (not streamable — silently broke every m4a fetch)
//
// audioext.ExtForMIME is an explicit map instead, so the mapping is a
// reviewable decision rather than stdlib table ordering.
func extFromResponse(resp *http.Response, urlPath string) string {
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		// Strip parameters like "; charset=utf-8".
		if mt, _, err := mime.ParseMediaType(ct); err == nil {
			if ext := audioext.ExtForMIME(mt); ext != "" {
				return ext
			}
		}
	}
	if ext := strings.ToLower(filepath.Ext(urlPath)); audioext.IsStreamableExt(ext) {
		return ext
	}
	return audioext.DefaultExt
}

// writeAudioFile writes up to maxBytes from r to path. If the body is larger
// than maxBytes or the copy fails for any reason, the partial file is
// removed so it cannot be served by a future /api/stream glob.
func writeAudioFile(path string, r io.Reader, maxBytes int64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}

	lr := io.LimitReader(r, maxBytes+1)
	n, err := io.Copy(f, lr)
	if err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if n > maxBytes {
		// Close before removing so the partial file is not held open while
		// we delete it.
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("audio body exceeds size cap of %d bytes", maxBytes)
	}
	return f.Close()
}

// findDownloadedFile picks the most recently created audio file matching `<id>.{ext}`.
func findDownloadedFile(dir, videoID string) (string, int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", 0, fmt.Errorf("download: read cache dir: %w", err)
	}
	var (
		bestPath string
		bestInfo os.FileInfo
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
		// Shared whitelist (REVIEW.md P0-4): must agree with the
		// gateway's filterAudioMatches, or the worker reports success for
		// a file /api/stream will never serve.
		if !audioext.IsStreamablePath(name) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if bestPath == "" || info.ModTime().After(bestTime) {
			bestPath = filepath.Join(dir, name)
			bestInfo = info
			bestTime = info.ModTime()
		}
	}
	if bestPath == "" {
		return "", 0, fmt.Errorf("download: no audio file for id=%s in %s", videoID, dir)
	}
	return bestPath, bestInfo.Size(), nil
}

// downloadErrorPath is the sentinel written next to a failed cache entry so
// /api/stream can fail-closed with a real message instead of looping 202
// forever after yt-dlp exhausts retries (signature / format / bot checks).
func downloadErrorPath(dir, videoID string) string {
	return filepath.Join(dir, videoID+".error")
}

// ReadDownloadError returns the last worker-written failure for this id, if any.
// Empty string means "no recorded failure" (still pending or never attempted).
func ReadDownloadError(source, videoID string) string {
	b, err := os.ReadFile(downloadErrorPath(audioCacheDir(source), videoID))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (h *DownloadHandler) failDownload(dir, videoID string, err error) error {
	if err == nil {
		return nil
	}
	// Best-effort marker for the stream long-poll path. Ignore write errors:
	// the asynq task still fails and will surface in worker logs / archived.
	_ = os.WriteFile(downloadErrorPath(dir, videoID), []byte(err.Error()), 0o644)
	return err
}

// copyFile is a minimal byte copy for the cache→library persistence step.
// Not using io.Copy on raw os.File handles because they aren't
// seek-aware on cross-fs rename and we want a clean truncated destination.
func copyFile(src, dst string) error {
	srcF, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcF.Close()
	dstF, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstF.Close()
	_, err = io.Copy(dstF, srcF)
	return err
}

// resolveLibraryDest maps a user-supplied relative dest (under musicRoot)
// onto an absolute library path.
//
// If dest looks like a file (known audio extension on the final segment),
// it is used as the full relative path. Otherwise dest is treated as a
// directory and the cache basename is appended (legacy settings path).
// When dest carries a different extension than the actual cache file
// (e.g. frontend asks for .ogg but yt-dlp kept .opus), the real cache
// extension wins so the on-disk file stays playable.
//
// The join goes through utils.SafeJoin (REVIEW.md P3-7) so containment is
// proven where the path is built rather than inferred from a check that ran
// somewhere else on a possibly different string.
func resolveLibraryDest(musicRoot, dest, cachePath string) (string, error) {
	base := filepath.Base(cachePath)
	cacheExt := filepath.Ext(base)
	destExt := strings.ToLower(filepath.Ext(dest))
	if audioext.IsStreamableExt(destExt) {
		// File path. Prefer the real cache extension when they differ so
		// we never claim "foo.ogg" for a vorbis→.opus land, etc.
		if cacheExt != "" && !strings.EqualFold(destExt, cacheExt) {
			dest = strings.TrimSuffix(dest, filepath.Ext(dest)) + cacheExt
		}
		return utils.SafeJoin(musicRoot, dest)
	}
	// Directory (legacy settings-only path): keep the cache basename.
	return utils.SafeJoin(musicRoot, filepath.Join(dest, base))
}
