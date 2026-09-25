package handler

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/audioext"
	"go-music-tag/internal/utils"
)

var lyricExts = map[string]bool{"lrc": true, "txt": true}

// FileItem mirrors the Python backend's file item JSON shape.
type FileItem struct {
	ID         int        `json:"id"`
	Name       string     `json:"name"`
	Title      string     `json:"title"`
	Icon       string     `json:"icon"`
	State      string     `json:"state"`
	Children   []FileItem `json:"children"`
	Size       int64      `json:"size"`
	UpdateTime string     `json:"update_time"`
	Expanded   *bool      `json:"expanded,omitempty"`
}

func boolPtr(b bool) *bool { return &b }

// FileListRequest is the expected POST body.
//
// NOTE: FilePath is intentionally NOT marked `binding:"required"` — the
// frontend fires a mount-only `loadFiles()` with `useAppStore.filePath`
// still `”` (initial state) to list the library root. Empty FilePath is
// semantically "list MUSIC_DIR" and SafeJoin(MusicRoot, "") returns
// MusicRoot, so we accept it as the canonical root-listing contract.
// `SortedFields` likewise is optional: empty means no client-driven
// reorder (server keeps os.ReadDir's natural order).
type FileListRequest struct {
	FilePath     string   `json:"file_path"`
	SortedFields []string `json:"sorted_fields"`
}

// FileList handles POST /api/file_list/ — lists directory contents.
//
// Path-traversal guard: req.FilePath is joined into MUSIC_DIR via
// utils.SafeJoin, refusing "../" or absolute traversal that escapes the
// library root. The frontend contract is unchanged (it still receives a
// file_path of its choice); we just enforce containment server-side.
func FileList(c *gin.Context) {
	var req FileListRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Failure(c, "invalid request: "+err.Error())
		return
	}

	rooted, err := utils.SafeJoin(utils.MusicRoot(), req.FilePath)
	if err != nil {
		Failure(c, "路径不安全: "+err.Error())
		return
	}
	entries, err := os.ReadDir(rooted)
	if err != nil {
		Failure(c, "文件夹不存在")
		return
	}

	filePathParts := strings.Split(rooted, "/")
	children := make([]FileItem, 0)
	lrcMap := make(map[string]string)

	itemIdx := 1
	for _, entry := range entries {
		name := entry.Name()
		info, _ := entry.Info()
		var (
			size       int64
			updateTime string
		)
		if info != nil {
			size = info.Size()
			updateTime = info.ModTime().Format("2006-01-02 15:04:05")
		}

		if entry.IsDir() {
			children = append(children, FileItem{
				ID:         itemIdx,
				Name:       name,
				Title:      name,
				Icon:       "icon-folder",
				State:      "null",
				Children:   []FileItem{},
				Size:       size,
				UpdateTime: updateTime,
			})
			itemIdx++
			continue
		}

		// Case-insensitive via audioext (REVIEW.md P2-2): the previous
		// literal map was keyed on lowercase but compared against the raw
		// extension, so `Track.MP3` / `song.FLAC` were silently dropped from
		// the listing even though the tag editor would happily write them.
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
		if !audioext.IsLibraryExt(ext) {
			continue
		}

		// Sidecar lyric detection (REVIEW.md P2-3): the old code checked
		// `lyricExts[ext]` *after* the audio-extension guard above, so the
		// branch was unreachable (lrc/txt are not audio) and lrcMap stayed
		// empty forever — `icon-script-files` could never be emitted. Probe
		// the filesystem for a same-stem .lrc instead, which is what the
		// icon actually means: "this track has lyrics next to it".
		baseName := strings.TrimSuffix(name, "."+ext)
		icon := "icon-script-file"
		for lyricExt := range lyricExts {
			if _, err := os.Stat(filepath.Join(rooted, baseName+"."+lyricExt)); err == nil {
				icon = "icon-script-files"
				lrcMap[baseName] = name
				break
			}
		}

		children = append(children, FileItem{
			ID:         itemIdx,
			Name:       name,
			Title:      name,
			Icon:       icon,
			State:      "null",
			Size:       size,
			UpdateTime: updateTime,
		})
		itemIdx++
	}

	// Sort
	sortFields := req.SortedFields
	for _, field := range sortFields {
		switch field {
		case "name":
			sort.SliceStable(children, func(i, j int) bool {
				return strings.ToLower(children[i].Name) < strings.ToLower(children[j].Name)
			})
		case "update_time":
			sort.SliceStable(children, func(i, j int) bool {
				return children[i].UpdateTime > children[j].UpdateTime
			})
		case "size":
			sort.SliceStable(children, func(i, j int) bool {
				return children[i].Size > children[j].Size
			})
		}
	}

	result := []FileItem{{
		Name:     filePathParts[len(filePathParts)-1],
		Title:    filePathParts[len(filePathParts)-1],
		Expanded: boolPtr(true),
		ID:       0,
		Children: children,
		Icon:     "icon-folder",
	}}

	c.JSON(200, gin.H{
		"result":  true,
		"code":    "200",
		"data":    result,
		"message": "success",
	})
}

// MusicID3Request is the POST body for /api/music_id3/.
// file_path is the parent dir relative to MUSIC_DIR ("" = root);
// file_name is the basenamed audio file under that dir.
type MusicID3Request struct {
	FilePath string `json:"file_path"`
	FileName string `json:"file_name" binding:"required"`
}

// MusicID3 handles POST /api/music_id3/ — reads embedded tags server-side.
//
// Restored so batch hydrate (添加音乐) and detail openEditor go through
// backend tag.Read instead of shipping audio Range chunks to the browser.
// Path-traversal guard: both FilePath and FileName are joined via
// utils.SafeJoin under MUSIC_DIR.
func MusicID3(c *gin.Context) {
	var req MusicID3Request
	if err := c.ShouldBindJSON(&req); err != nil {
		Failure(c, "invalid request")
		return
	}

	ext := strings.TrimPrefix(filepath.Ext(req.FileName), ".")
	if lyricExts[ext] {
		Success(c, "success", nil)
		return
	}

	root := utils.MusicRoot()
	dir, err := utils.SafeJoin(root, req.FilePath)
	if err != nil {
		Failure(c, "路径不安全: "+err.Error())
		return
	}
	fullPath, err := utils.SafeJoin(dir, req.FileName)
	if err != nil {
		Failure(c, "路径不安全: "+err.Error())
		return
	}

	// Existing edge-case parity: when the directory's basename matches
	// the requested file name, treat it as a redundant request and
	// return an empty success.
	subPath := filepath.Base(dir)
	if subPath == req.FileName {
		Success(c, "success", nil)
		return
	}

	tags, err := ReadMusicTags(fullPath)
	if err != nil {
		Failure(c, err.Error())
		return
	}

	SuccessData(c, tags)
}
