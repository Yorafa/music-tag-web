package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/netguard"
	"go-music-tag/internal/tag"
	"go-music-tag/internal/utils"
)

// remoteGuard 是本地默认 SSRF 防护实例；fetchRemoteBytes 每次都会
// 调用其 Validate。在生产中通过环境变量调整白名单并不现实 (这是
// 给内网出站连接用的)，所以默认 deny private/loopback 即可。
var remoteGuard = netguard.NewGuard()

// UpdateID3 handles POST /api/update_id3/ — 写入单条文件标签。
//
// Security (P1.5 issue F): every file_full_path is SafeJoined against
// MUSIC_DIR before being touched; previously the handler trusted the
// path verbatim, which let any caller rename tags outside the library.
func UpdateID3(c *gin.Context) {
	var req struct {
		MusicID3Info []map[string]interface{} `json:"music_id3_info" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Failure(c, "invalid request")
		return
	}
	root := utils.MusicRoot()
	for _, info := range req.MusicID3Info {
		rawPath := stringValue(info["file_full_path"])
		if rawPath == "" {
			continue
		}
		filePath, err := utils.SafeJoin(root, rawPath)
		if err != nil {
			Failure(c, "路径不安全: "+err.Error())
			return
		}
		if err := applyFileUpdate(filePath, info); err != nil {
			Failure(c, fmt.Sprintf("update %s: %v", filepath.Base(filePath), err))
			return
		}
	}
	Success(c, "success", nil)
}

// BatchUpdateID3 handles POST /api/batch_update_id3/ — folder 展开 + 多文件写入。
//
// Security (P1.5 issue F): each leaf path is SafeJoined under MUSIC_DIR;
// folder recursion only descends into directories that resolved inside
// the root.
func BatchUpdateID3(c *gin.Context) {
	var req struct {
		FileFullPath string                   `json:"file_full_path" binding:"required"`
		MusicInfo    map[string]interface{}   `json:"music_info" binding:"required"`
		SelectData   []map[string]interface{} `json:"select_data" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Failure(c, "invalid request")
		return
	}

	allowedExt := map[string]bool{
		"flac": true, "mp3": true, "ape": true, "wav": true, "aiff": true,
		"wv": true, "tta": true, "m4a": true, "ogg": true, "mpc": true,
		"opus": true, "wma": true, "dsf": true, "dff": true,
	}

	root := utils.MusicRoot()
	baseDir, err := utils.SafeJoin(root, req.FileFullPath)
	if err != nil {
		Failure(c, "路径不安全: "+err.Error())
		return
	}

	for _, sel := range req.SelectData {
		name := stringValue(sel["name"])
		if name == "" {
			continue
		}
		if stringValue(sel["icon"]) == "icon-folder" {
			dirPath, err := utils.SafeJoin(baseDir, name)
			if err != nil {
				Failure(c, "路径不安全: "+err.Error())
				return
			}
			entries, err := os.ReadDir(dirPath)
			if err != nil {
				continue
			}
			for _, e := range entries {
				ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(e.Name()), "."))
				if !allowedExt[ext] {
					continue
				}
				leaf, err := utils.SafeJoin(dirPath, e.Name())
				if err != nil {
					continue // skip — name overflowed under a different root (defensive)
				}
				merged := mergeInfo(req.MusicInfo, map[string]interface{}{
					"file_full_path": leaf,
					"filename":       e.Name(),
				})
				if err := applyFileUpdate(stringValue(merged["file_full_path"]), merged); err != nil {
					Failure(c, err.Error())
					return
				}
			}
			continue
		}
		leaf, err := utils.SafeJoin(baseDir, name)
		if err != nil {
			Failure(c, "路径不安全: "+err.Error())
			return
		}
		merged := mergeInfo(req.MusicInfo, map[string]interface{}{
			"file_full_path": leaf,
		})
		if err := applyFileUpdate(stringValue(merged["file_full_path"]), merged); err != nil {
			Failure(c, err.Error())
			return
		}
	}
	Success(c, "success", nil)
}

// 注: BatchAutoUpdateID3 / TidyFolder 在 task.go 里已经接入 asynq，这里保持空。
// UploadImage handles POST /api/upload_image/ — base64 returns (无 data URI 前缀)。
func UploadImage(c *gin.Context) {
	file, _, err := c.Request.FormFile("upload_file")
	if err != nil {
		Failure(c, "no file uploaded")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		Failure(c, "read error")
		return
	}
	b64 := base64.StdEncoding.EncodeToString(data)
	SuccessData(c, b64)
}

// ─── internal ─────────────────────────────────────────────────────────────

// applyFileUpdate 每个文件按 MusicIDS 流：模板 → 写 tag → sidecar → 文件名模板 → 改名。
//
// Renamed targets are computed via utils.SafeJoin so callers cannot drive
// the rename into an arbitrary parent directory.
func applyFileUpdate(filePath string, info map[string]interface{}) error {
	if !isAudioFile(filePath) {
		return nil
	}
	tmplVars := readFileContext(filePath)

	upd := &tag.TagUpdate{}
	if v := stringValue(info["title"]); v != "" {
		v = applyTemplate(v, tmplVars)
		upd.Title = &v
	}
	if v := stringValue(info["artist"]); v != "" {
		arr := strings.Split(v, ",")
		upd.Artist = make([]string, len(arr))
		for i := range arr {
			upd.Artist[i] = strings.TrimSpace(arr[i])
		}
	}
	if v := stringValue(info["album"]); v != "" {
		v = applyTemplate(v, tmplVars)
		upd.Album = &v
	}
	if v := stringValue(info["albumartist"]); v != "" {
		v = applyTemplate(v, tmplVars)
		upd.AlbumArtist = &v
	}
	if v := stringValue(info["discnumber"]); v != "" {
		upd.DiscNumber = &v
	}
	if v := stringValue(info["tracknumber"]); v != "" {
		upd.TrackNumber = &v
	}
	if v := stringValue(info["genre"]); v != "" {
		upd.Genre = &v
	}
	if v := stringValue(info["year"]); v != "" {
		upd.Year = &v
	}
	if rawLyrics, present := info["lyrics"]; present {
		if rawLyrics == nil {
			upd.ClearLyrics = true
		} else if v := stringValue(rawLyrics); v != "" {
			upd.Lyrics = &v
		}
	}
	if v := stringValue(info["comment"]); v != "" {
		upd.Comment = &v
	}
	if v := stringValue(info["album_img"]); v != "" {
		raw, _, _ := tag.DecodePictureBase64(v)
		if len(raw) == 0 && strings.HasPrefix(strings.TrimSpace(v), "http") {
			if fetched, err := fetchRemoteBytes(v); err == nil {
				raw = fetched
			}
		}
		if len(raw) > 0 {
			upd.AlbumImg = raw
		}
	}

	if err := tag.Write(filePath, upd); err != nil {
		return fmt.Errorf("write tags: %w", err)
	}

	sc := &tag.WriteSidecar{
		SaveLrcFile:    truthy(info["is_save_lyrics_file"]),
		SaveAlbumCover: truthy(info["is_save_album_cover"]),
	}
	if upd.Album != nil {
		sc.Album = *upd.Album
	} else if v := stringValue(info["album"]); v != "" {
		sc.Album = v
	}
	if _, err := tag.HandleSidecars(filePath, upd, sc); err != nil {
		return fmt.Errorf("sidecar: %w", err)
	}

	// 文件名模板 + rename
	if v := stringValue(info["filename"]); v != "" {
		newName := applyTemplate(v, tmplVars)
		if !strings.HasSuffix(strings.ToLower(newName), strings.ToLower(filepath.Ext(filePath))) {
			newName = newName + filepath.Ext(filePath)
		}
		newName = utils.SanitizePath(newName)
		parent := filepath.Dir(filePath)
		target := filepath.Join(parent, newName)
		// Single-arm SafeJoin: refuse the rename if the resolved target
		// escapes MUSIC_DIR (defence-in-depth against any future change to
		// upstream SanitizePath). No fallback to the unsafe target.
		safeTarget, sErr := utils.SafeJoin(utils.MusicRoot(), strings.TrimPrefix(target, utils.MusicRoot()))
		if sErr != nil {
			return fmt.Errorf("rename target unsafe: %w", sErr)
		}
		if safeTarget != filePath {
			if err := os.Rename(filePath, safeTarget); err != nil {
				return fmt.Errorf("rename: %w", err)
			}
		}
	}
	return nil
}

func applyTemplate(tmpl string, vars map[string]string) string {
	return utils.RenderTemplate(tmpl, vars)
}

// readFileContext 与 Django MusicIDS.var_dict() 字段对齐。
func readFileContext(path string) map[string]string {
	info, err := tag.Read(path)
	if err != nil {
		return map[string]string{}
	}
	idx := func(s string) string {
		if i := strings.Index(s, "/"); i >= 0 {
			return strings.TrimSpace(s[:i])
		}
		return strings.TrimSpace(s)
	}
	return map[string]string{
		"title":       info.Title,
		"artist":      info.Artist,
		"albumartist": info.AlbumArtist,
		"album":       info.Album,
		"filename":    info.Filename,
		"discnumber":  idx(info.DiscNumber),
		"tracknumber": idx(info.TrackNumber),
	}
}

func mergeInfo(base, override map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(base))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

// stringValue 把 interface{} 适配成 string，兼容 JSON 数字、bool、nil 输入。
func stringValue(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return ""
	case float64:
		if x == float64(int(x)) {
			return fmt.Sprintf("%d", int(x))
		}
		return fmt.Sprintf("%v", x)
	case int:
		return fmt.Sprintf("%d", x)
	case int64:
		return fmt.Sprintf("%d", x)
	case json.Number:
		return string(x)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func isAudioFile(path string) bool {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	switch ext {
	case "flac", "mp3", "ape", "wav", "aiff", "wv", "tta", "m4a", "ogg", "mpc",
		"opus", "wma", "dsf", "dff":
		return true
	}
	return false
}

func truthy(v interface{}) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		s := strings.ToLower(strings.TrimSpace(x))
		return s == "true" || s == "1" || s == "yes"
	case float64:
		return x != 0
	}
	return false
}

// fetchRemoteBytes GET 远程封面。20MB 上限；返回 bytes 前用 image.DecodeConfig
// 校验完整性，避免写入半截 JPG 让前端 data URI 渲染失败。
//
// Security (P1.5 issue F): delegates to netguard.SafeHTTPGet which
//   (a) validates the URL up front (scheme + resolved IPs),
//   (b) re-validates every redirect hop (so a public → private pivot
//       via 302 is refused),
//   (c) caps the body at 20MB.
//
// Content-shape validation is the handler's responsibility: we run
// image.DecodeConfig here so a non-image body cannot poison a tag.
func fetchRemoteBytes(rawURL string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	buf, err := remoteGuard.SafeHTTPGet(ctx, rawURL, 20<<20)
	if err != nil {
		return nil, fmt.Errorf("remote url not allowed: %w", err)
	}
	if _, _, err := image.DecodeConfig(bytes.NewReader(buf)); err != nil {
		return nil, fmt.Errorf("invalid image: %w", err)
	}
	return buf, nil
}

// _ 保留 import encoding/json 入口以备 stringValue 的 json.Number 分支使用
var _ = json.Marshal
