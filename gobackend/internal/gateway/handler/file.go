package handler

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/utils"
)

// allowedAudioExts matches Python's ALLOW_TYPE.
var allowedAudioExts = map[string]bool{
	"flac": true, "mp3": true, "ape": true, "wav": true, "aiff": true,
	"wv": true, "tta": true, "m4a": true, "ogg": true, "mpc": true,
	"opus": true, "wma": true, "dsf": true, "dff": true, "wmv": true,
}

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
type FileListRequest struct {
	FilePath     string   `json:"file_path" binding:"required"`
	SortedFields []string `json:"sorted_fields" binding:"required"`
}

// FileList handles POST /api/file_list/ — lists directory contents.
//
// Security (P1.5 issue F): req.FilePath is joined into MUSIC_DIR via
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

		ext := strings.TrimPrefix(filepath.Ext(name), ".")
		if !allowedAudioExts[ext] {
			continue
		}

		baseName := strings.TrimSuffix(name, "."+ext)
		if lyricExts[ext] {
			lrcMap[baseName] = name
		}

		icon := "icon-script-file"
		if _, ok := lrcMap[baseName]; ok {
			icon = "icon-script-files"
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

// MusicID3Request ...
type MusicID3Request struct {
	FilePath string `json:"file_path" binding:"required"`
	FileName string `json:"file_name" binding:"required"`
}

// MusicID3 handles POST /api/music_id3/ — reads ID3 tags from a file.
//
// Security (P1.5 issue F): pre-P1 this handler concatenated
// `fp + "/" + req.FileName` with no validation, so req.FileName like
// "../../etc/foo.mp3" would escape the directory. We now join with
// utils.SafeJoin inside MUSIC_DIR.
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
