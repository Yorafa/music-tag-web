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

// YouTubeDownloadHandler downloads a YouTube video as audio and persists a Folder.
// Mirrors Django applications/task/services/youtube.py:
//   - extract audio via yt-dlp
//   - land under <music_root>/downloads/<video_id>.{ext}
//   - write a Folder (file_type='audio'|'youtube') and one or more TaskRecords
//
// SECURITY (P1.5 issue F): ExtraJSON is untrusted input (any client can
// POST /api/youtube_download/). The pre-P1 code path called
// fmt.Sprintf("...output_format=...%s") into the task payload and
// subsequently into exec.CommandContext's argv. Today both the gateway
// and the worker run each value through SanitizeYTDLPFormat/OutputFormat/
// Quality before constructing argv, so yt-dlp flags like `--exec` are
// refused at the boundary.
type YouTubeDownloadHandler struct {
	DB          *gorm.DB
	MusicRoot   string
	YTDLPPath   string // absolute path inside worker image; default "yt-dlp"
	Concurrency int
}

func NewYouTubeDownloadHandler(gormDB *gorm.DB, musicRoot, ytdlpPath string) *YouTubeDownloadHandler {
	if ytdlpPath == "" {
		ytdlpPath = "yt-dlp"
	}
	return &YouTubeDownloadHandler{
		DB:          gormDB,
		MusicRoot:   musicRoot,
		YTDLPPath:   ytdlpPath,
		Concurrency: 4,
	}
}

type youTubeDownloadExtra struct {
	Format       string `json:"format,omitempty"`        // e.g. "bestaudio/best"
	OutputFormat string `json:"output_format,omitempty"` // e.g. "mp3" / "m4a" / "ogg"
	Quality      string `json:"quality,omitempty"`       // e.g. "192"
}

func (h *YouTubeDownloadHandler) ProcessTask(ctx context.Context, t Task) error {
	payload, ok := t.Payload.(*YouTubeDownloadPayload)
	if !ok {
		return fmt.Errorf("youtube: invalid payload type %T", t.Payload)
	}

	extra := youTubeDownloadExtra{
		Format:       "bestaudio/best",
		OutputFormat: "mp3",
		Quality:      "192",
	}
	if payload.ExtraJSON != "" {
		if err := json.Unmarshal([]byte(payload.ExtraJSON), &extra); err != nil {
			return fmt.Errorf("youtube: parse ExtraJSON: %w", err)
		}
	}
	// Sanitize re-checks the persisted values (defence-in-depth — covers
	// task payload replay / DB tampering paths as well as gateway-vetting
	// that already happens in handler.YoutubeDownload).
	cleanFmt, err := SanitizeYTDLPFormat(extra.Format)
	if err != nil {
		return fmt.Errorf("youtube: format: %w", err)
	}
	cleanOut, err := SanitizeYTDLPOutputFormat(extra.OutputFormat)
	if err != nil {
		return fmt.Errorf("youtube: output_format: %w", err)
	}
	cleanQuality, err := SanitizeYTDLPQuality(extra.Quality)
	if err != nil {
		return fmt.Errorf("youtube: quality: %w", err)
	}

	// 1) ensure download dir
	downloadsDir := filepath.Join(h.MusicRoot, "downloads")
	if err := os.MkdirAll(downloadsDir, 0o755); err != nil {
		return fmt.Errorf("youtube: mkdir downloads: %w", err)
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
		return fmt.Errorf("youtube: stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout // merge: yt-dlp prints to stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("youtube: start %q: %w", h.YTDLPPath, err)
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
		return fmt.Errorf("youtube: %q exit: %w", h.YTDLPPath, err)
	}

	// 3) locate downloaded file (mp3 / m4a / ogg / original)
	dlFile, dlSize, err := findDownloadedFile(downloadsDir, payload.VideoID)
	if err != nil {
		return err
	}

	// 4) persist: 1 Folder + 1 TaskRecord
	now := time.Now()
	folder := db.Folder{
		UID:       payload.VideoID, // re-use YouTube id as unique folder UID
		ParentID:  "",
		Name:      filepath.Base(dlFile),
		FileType:  "youtube",
		Path:      dlFile,
		Size:      dlSize,
		UpdatedAt: now,
	}
	if err := h.DB.WithContext(ctx).Where("uid = ?", folder.UID).
		Attrs(folder).
		FirstOrCreate(&folder).Error; err != nil {
		return fmt.Errorf("youtube: upsert folder: %w", err)
	}

	rec := db.TaskRecord{
		TaskID:    payload.RequestedBy,
		Batch:     payload.Batch,
		FileName:  filepath.Base(dlFile),
		FullPath:  dlFile,
		Source:    "youtube",
		UID:       payload.VideoID,
		FileType:  "youtube",
		Status:    "completed",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := h.DB.WithContext(ctx).Create(&rec).Error; err != nil {
		return fmt.Errorf("youtube: create task record: %w", err)
	}
	return nil
}

// findDownloadedFile picks the most recently created audio file matching `<id>.{ext}`.
func findDownloadedFile(dir, videoID string) (string, int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", 0, fmt.Errorf("youtube: read downloads dir: %w", err)
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
		return "", 0, fmt.Errorf("youtube: no audio file for id=%s in %s", videoID, dir)
	}
	return bestPath, bestInfo.Size(), nil
}
