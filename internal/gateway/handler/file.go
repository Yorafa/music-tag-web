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

		// 扩展名先转小写再交给 audioext 判定：`Track.MP3` / `song.FLAC` 这类
		// 大写扩展名标签编辑器写得进去，不能在列表里看不见。
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
		if !audioext.IsLibraryExt(ext) {
			continue
		}

		// 歌词图标探的是磁盘上有没有同名的 .lrc，而不是拿 lrc/txt 扩展名去猜：
		// 这个图标的含义是「这首歌旁边放着歌词」，而 lrc/txt 本身不是音频，
		// 走上面那道音频扩展名守卫的话这个分支永远进不去。
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
	// IncludeArtwork controls whether the embedded cover comes back inline
	// as a base64 data URI. Defaults to true when omitted.
	//
	// Why this exists: the batch hydrate path calls this once per file, and
	// a reported-real library returns 3–15 MB per response because the
	// embedded art is a full-resolution scan. Paying that for 500 files to
	// paint 32-pixel thumbnails moved over a gigabyte, and the frontend
	// discarded every byte of it in stripHeavyFromRows before persisting.
	//
	// The batch path sends false and fetches covers separately via
	// /api/album_cover/ for the rows actually on screen. The tag editor
	// leaves it true, because there the cover IS the point — it renders
	// large, and one file at a time.
	//
	// A *bool so "absent" is distinguishable from "explicitly false":
	// existing clients that never send the field keep the old behaviour
	// without a version bump.
	IncludeArtwork *bool `json:"include_artwork"`
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

	// The reader has already paid for the artwork by this point — it is
	// extracted during the tag parse, not lazily on access — so omitting it
	// here saves the base64 encoding and the transfer, not the disk read.
	// That is still the dominant cost at 3–15 MB per response, but worth
	// being precise about: this flag does not make the read cheaper, it
	// makes the RESPONSE cheaper.
	if req.IncludeArtwork != nil && !*req.IncludeArtwork {
		delete(tags, "artwork")
	}

	SuccessData(c, tags)
}
