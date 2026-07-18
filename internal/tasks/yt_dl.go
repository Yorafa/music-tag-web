package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go-music-tag/internal/db"
	"gorm.io/gorm"
)

// AudioCacheRoot returns the per-source staging directory downloaded audio
// files land in (shared with the gateway /api/stream handler's glob lookup).
//
// Each registered DownloadSource gets its own subdirectory under
// /tmp/audio_cache/<source>/ so cross-source video_id collisions (e.g.
// numeric IDs from migu / soundcloud vs youtube's 11-char) can't collide.
// MUST stay in lockstep with internal/gateway/handler/stream.go's
// audioCacheDir (same env var, same default).
func audioCacheDir(source string) string {
	root := os.Getenv("AUDIO_CACHE_DIR")
	if root == "" {
		root = "/tmp/audio_cache"
	}
	return filepath.Join(root, source)
}

// DownloadHandler is the unified, source-routed asynq handler for the
// `download:generic` task type. payload.Source dispatches to the
// matching download branch. Today only the youtube/yt-dlp branch is
// implemented; adding a new download source (e.g. soundcloud) means
// adding a case here plus a side-effecting exec path for that source,
// NOT a new asynq task type and NOT a new endpoint.
//
// Two landing directories are involved:
//   - Cache dir (preview path): audioCacheDir(source)/<id>.<ext>
//     written by the source-specific exec call. Shared with the
//     gateway's /api/stream glob so ServeFile picks the file up.
//   - DestDir (加入库 path): when non-empty, the file is copied from
//     the cache dir to SafeJoin(MusicRoot, DestDir)/<id>.<ext> AFTER
//     the download finishes, so preview cache stays short-lived and
//     the library gets a permanent copy. When empty, the worker just
//     leaves the file in cache (this is the preview-only path the
//     /api/stream proxy triggers).
//
// SECURITY: ExtraJSON is untrusted input (any client can POST
// /api/download/). The previous code path called
// fmt.Sprintf("...output_format=...%s") into the task payload and
// subsequently into exec.CommandContext's argv. Today both the gateway
// and the worker run each value through SanitizeYTDLPFormat/OutputFormat/
// Quality before constructing argv, so yt-dlp flags like `--exec` are
// refused at the boundary.
type DownloadHandler struct {
	DB        *gorm.DB
	MusicRoot string
	YTDLPPath string // absolute path inside worker image; default "yt-dlp". Currently only used by the youtube branch.
}

func NewDownloadHandler(gormDB *gorm.DB, musicRoot, ytdlpPath string) *DownloadHandler {
	if ytdlpPath == "" {
		ytdlpPath = "yt-dlp"
	}
	return &DownloadHandler{DB: gormDB, MusicRoot: musicRoot, YTDLPPath: ytdlpPath}
}

type youTubeDownloadExtra struct {
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
	switch payload.Source {
	case "youtube":
		return h.runYouTube(ctx, payload)
	default:
		return fmt.Errorf("download: source %q not yet supported by worker", payload.Source)
	}
}

// runYouTube is the youtube-specific branch: fork yt-dlp into the cache dir.
// Other branches (soundcloud, etc) would each have their own run<Source>
// helper here once those download sources are registered. Until branch
// generalization happens at the plugin interface level (yt-dlp exec is
// the only one implemented today), we dispatch on payload.Source here,
// not via plugin.GetDownloadSource — that helper is for the gateway
// handler (which never runs yt-dlp itself), not the worker (which runs
// the actual binary).
func (h *DownloadHandler) runYouTube(ctx context.Context, payload *DownloadPayload) error {
	// 0) Sanitize persisted ExtraJSON (defence-in-depth — gateway already
	//    vetted, but DB-replay / tamper paths still reach the worker).
	extra := youTubeDownloadExtra{
		Format:       "bestaudio/best",
		OutputFormat: "mp3",
		Quality:      "192",
	}
	if payload.ExtraJSON != "" {
		if err := json.Unmarshal([]byte(payload.ExtraJSON), &extra); err != nil {
			return fmt.Errorf("download: parse ExtraJSON: %w", err)
		}
	}
	cleanFmt, err := SanitizeYTDLPFormat(extra.Format)
	if err != nil {
		return fmt.Errorf("download: format: %w", err)
	}
	cleanOut, err := SanitizeYTDLPOutputFormat(extra.OutputFormat)
	if err != nil {
		return fmt.Errorf("download: output_format: %w", err)
	}
	cleanQuality, err := SanitizeYTDLPQuality(extra.Quality)
	if err != nil {
		return fmt.Errorf("download: quality: %w", err)
	}

	// 1) ensure cache dir
	downloadsDir := audioCacheDir("youtube")
	if err := os.MkdirAll(downloadsDir, 0o755); err != nil {
		return fmt.Errorf("download: mkdir cache: %w", err)
	}

	// 2) build yt-dlp output template: <id>.%(ext)s
	outTpl := filepath.Join(downloadsDir, "%(id)s.%(ext)s")

	args := []string{
		"--no-playlist",
		"--no-progress",
		"--newline",
		"-f", cleanFmt,
		"-o", outTpl,
		"--no-part",
	}
	switch cleanOut {
	case "mp3":
		args = append(args,
			"--extract-audio",
			"--audio-format", "mp3",
			"--audio-quality", cleanQuality+"K",
		)
	case "m4a":
		args = append(args,
			"--extract-audio",
			"--audio-format", "m4a",
			"--audio-quality", cleanQuality+"K",
		)
	case "ogg", "vorbis":
		args = append(args,
			"--extract-audio",
			"--audio-format", "vorbis",
			"--audio-quality", cleanQuality+"K",
		)
	case "wav":
		args = append(args, "--extract-audio", "--audio-format", "wav")
	default:
		// bestaudio fallback: keep original container
	}

	// `--` 分隔符先行于 URL，让 argv 解析明确把 URL 当作 positional 而不是
	// option。这是防御性 coding：即使将来某条 sanitize 漏掉了带前导 "-" 的
	// 危险字符串，yt-dlp 也不会把它当作 flag 执行。
	args = append(args,
		"--",
		"https://www.youtube.com/watch?v="+payload.VideoID,
	)

	cmd := exec.CommandContext(ctx, h.YTDLPPath, args...)
	cmd.Env = append(os.Environ(),
		"PYTHONUNBUFFERED=1",
		"LC_ALL=C.UTF-8",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("download: stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout // merge: yt-dlp prints to stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("download: start %q: %w", h.YTDLPPath, err)
	}

	// Log progress to stdout (cheap signals for ops; no buffering).
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				fmt.Printf("[yt-dlp][%s] %s", payload.VideoID, string(buf[:n]))
			}
			if err != nil {
				if err != io.EOF {
					// suppress noise
				}
				return
			}
		}
	}()

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("download: %q exit: %w", h.YTDLPPath, err)
	}

	// 3) locate downloaded file (mp3 / m4a / ogg / original)
	dlFile, dlSize, err := findDownloadedFile(downloadsDir, payload.VideoID)
	if err != nil {
		return err
	}

	// 4) If DestDir is supplied (加入库 path), copy the cache file into
	//    SafeJoin(MusicRoot, DestDir) so the user gets a permanent library
	//    file; preview cache keeps its transient copy. Failing to persist
	//    is non-fatal for the play path — we surface the error and let
	//    asynq retry once; the cache copy is already valid for preview.
	libraryPath := dlFile
	if payload.DestDir != "" && h.MusicRoot != "" {
		destDir := filepath.Join(h.MusicRoot, filepath.Clean(payload.DestDir))
		// SafeJoin-ish: refuse absolute / `..` traversal. This path came
		// from the user's `settings.downloadPath` localStorage (personal
		// deployment) so we don't expect abuse, but the boundary guard
		// stays for defence-in-depth.
		if strings.Contains(filepath.Clean(payload.DestDir), "..") ||
			filepath.IsAbs(payload.DestDir) {
			return fmt.Errorf("download: dest_dir must be relative path under music root, got %q", payload.DestDir)
		}
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			return fmt.Errorf("download: mkdir dest %q: %w", destDir, err)
		}
		destPath := filepath.Join(destDir, filepath.Base(dlFile))
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
		FileType:  payload.Source,
		Path:      dlFile,
		Size:      dlSize,
		UpdatedAt: now,
	}
	if err := h.DB.WithContext(ctx).Where("uid = ?", folder.UID).
		Attrs(folder).
		FirstOrCreate(&folder).Error; err != nil {
		return fmt.Errorf("download: upsert folder: %w", err)
	}

	rec := db.TaskRecord{
		TaskID:    payload.RequestedBy,
		Batch:     payload.Batch,
		FileName:  filepath.Base(dlFile),
		FullPath:  dlFile,
		Source:    payload.Source,
		UID:       payload.VideoID,
		FileType:  payload.Source,
		Status:    "completed",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := h.DB.WithContext(ctx).Create(&rec).Error; err != nil {
		return fmt.Errorf("download: create task record: %w", err)
	}
	_ = libraryPath
	return nil
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
			bestInfo = info
			bestTime = info.ModTime()
		}
	}
	if bestPath == "" {
		return "", 0, fmt.Errorf("download: no audio file for id=%s in %s", videoID, dir)
	}
	return bestPath, bestInfo.Size(), nil
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
