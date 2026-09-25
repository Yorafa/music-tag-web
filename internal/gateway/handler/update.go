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

	"go-music-tag/internal/audioext"
	"go-music-tag/internal/audit"
	"go-music-tag/internal/dedup"
	"go-music-tag/internal/netguard"
	"go-music-tag/internal/tag"
	"go-music-tag/internal/utils"
)

// remoteGuard 是本地默认 SSRF 防护实例；fetchRemoteBytes 每次都会
// 调用其 Validate。在生产中通过环境变量调整白名单并不现实 (这是
// 给内网出站连接用的)，所以默认 deny private/loopback 即可。
var remoteGuard = netguard.NewGuard()

// dedupChecker 是跨 handler 复用的 dedup.Checker 实例（按需懒构造）。
// 它绑定当前 gateway 进程的 GORM DB 与 MUSIC_DIR，在第一次刮削请求时
// 完成初始化；后续重入直接复用以省 fpcalc LookPath 等 syscall。
//
// nil ⇒ dedup 检查被禁用（操作员可在配置中显式 opt-out 时不挂上）。
var dedupChecker *dedup.Checker

// SetDedupChecker 由 main 在 db.Open 之后调用注入；handler 不主动 new。
func SetDedupChecker(c *dedup.Checker) { dedupChecker = c }

// UpdateID3 handles POST /api/update_id3/ — 写入单条文件标签。
//
// Security (P1.5 issue F): every file_full_path is SafeJoined against
// MUSIC_DIR before being touched; previously the handler trusted the
// path verbatim, which let any caller rename tags outside the library.
//
// Dedup optionally: each MusicID3Info 行携带 check_duplicate=true/缺省 → 在
// tag.Write 前调用 dedup.Checker；命中 Duplicate 时跳过该条并附 skip 信息
// 一起回包，给前端 toast / 状态 badge 用。
func UpdateID3(c *gin.Context) {
	var req struct {
		MusicID3Info []map[string]interface{} `json:"music_id3_info" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Failure(c, "invalid request")
		return
	}
	root := utils.MusicRoot()
	report := updateBatchReport{}
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
			if dupErr, ok := err.(ErrDuplicateSkipped); ok {
				// 记录跳过信息但 handler 整体仍返回成功，避免前端给一行「重复」扔 4xx。
				report.addSkipped(rawPath, dupErr.Dup)
				audit.Log(c.Request.Context(), audit.ActionUpdateID3, rawPath, "admin", audit.StatusSkipped, 1, dupErr.Dup, nil)
				continue
			}
			audit.Log(c.Request.Context(), audit.ActionUpdateID3, rawPath, "admin", audit.StatusFailed, 1, info, err)
			Failure(c, fmt.Sprintf("update %s: %v", filepath.Base(filePath), err))
			return
		}
		report.addDone(rawPath)
		audit.Log(c.Request.Context(), audit.ActionUpdateID3, rawPath, "admin", audit.StatusSuccess, 1, info, nil)
	}
	SuccessData(c, report.toJSON())
}

// updateBatchReport 是 BatchUpdateID3 / UpdateID3 共用的「哪些写成功、哪些
// 因重复被跳过」回包包体。给前端 toast / 状态 badge 渲染。
type updateBatchReport struct {
	done    []map[string]interface{}
	skipped []map[string]interface{}
}

func (r *updateBatchReport) addDone(path string) {
	r.done = append(r.done, map[string]interface{}{
		"file_full_path": path,
		"status":         "updated",
	})
}
func (r *updateBatchReport) addSkipped(path string, dup dedup.Result) {
	r.skipped = append(r.skipped, map[string]interface{}{
		"file_full_path": path,
		"status":         "duplicate",
		"verdict":        dup.Verdict,
		"match_field":    dup.MatchField,
		"duplicate_path": dup.DuplicatePath,
		"reason":         dup.Reason,
	})
}

// toJSON 把 done/skipped 合并回一个稳定的 dict 结构，避免返回 nil slice 给
// 前端造成 JSON null（types 约定是数组）。
func (r *updateBatchReport) toJSON() map[string]interface{} {
	if r.done == nil {
		r.done = []map[string]interface{}{}
	}
	if r.skipped == nil {
		r.skipped = []map[string]interface{}{}
	}
	return map[string]interface{}{
		"done":    r.done,
		"skipped": r.skipped,
	}
}

// BatchUpdateID3 handles POST /api/batch_update_id3/ — folder 展开 + 多文件写入。
//
// Security (P1.5 issue F): each leaf path is SafeJoined under MUSIC_DIR;
// folder recursion only descends into directories that resolved inside
// the root.
//
// Dedup: 若 req.MusicInfo["check_duplicate"]==true，则该标志会随 mergeInfo
// 透传到每个 leaf 的 applyFileUpdate 调用，命中 Duplicate 的文件计入
// report.skipped。失败 = 老语义的 Failure；整批没有 Failure 即 200 + report。
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

	root := utils.MusicRoot()
	baseDir, err := utils.SafeJoin(root, req.FileFullPath)
	if err != nil {
		Failure(c, "路径不安全: "+err.Error())
		return
	}

	report := updateBatchReport{}

	// 把每张 leaf 的写入错误拆开，duplicate 走 report.skipped，
	// 真实写错误走 Failure（同老语义：单条炸就报错并返回）。
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
				if !audioext.IsLibraryPath(e.Name()) {
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
					if dupErr, ok := err.(ErrDuplicateSkipped); ok {
						report.addSkipped(leaf, dupErr.Dup)
						continue
					}
					Failure(c, err.Error())
					return
				}
				report.addDone(leaf)
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
			if dupErr, ok := err.(ErrDuplicateSkipped); ok {
				report.addSkipped(leaf, dupErr.Dup)
				continue
			}
			Failure(c, err.Error())
			return
		}
		report.addDone(leaf)
	}
	status := audit.StatusSuccess
	if len(report.skipped) > 0 && len(report.done) > 0 {
		status = audit.StatusPartial
	} else if len(report.done) == 0 && len(report.skipped) > 0 {
		status = audit.StatusSkipped
	}
	audit.Log(c.Request.Context(), audit.ActionBatchUpdateID3, req.FileFullPath, "admin", status, len(report.done)+len(report.skipped), map[string]interface{}{
		"file_full_path": req.FileFullPath,
		"music_info":     req.MusicInfo,
		"select_count":   len(req.SelectData),
		"done_count":     len(report.done),
		"skipped_count":  len(report.skipped),
	}, nil)
	SuccessData(c, report.toJSON())
}

// 注: BatchAutoUpdateID3 / TidyFolder 在 task.go 里已经接入 asynq，这里保持空。
// UploadImage handles POST /api/upload_image/ — base64 returns (无 data URI 前缀)。
func UploadImage(c *gin.Context) {
	file, header, err := c.Request.FormFile("upload_file")
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
	filename := "cover.jpg"
	if header != nil && header.Filename != "" {
		filename = header.Filename
	}
	b64 := base64.StdEncoding.EncodeToString(data)
	audit.Log(c.Request.Context(), audit.ActionUploadCover, filename, "admin", audit.StatusSuccess, 1, map[string]interface{}{
		"filename": filename,
		"size":     len(data),
	}, nil)
	SuccessData(c, b64)
}

// skipDuplicateCheck 返回 true 表示 info 标记了跳过去重。
// JSON 入参约定: {"...":..., "check_duplicate": true}；显式传 false / 缺省都视为
// 不开，保持旧行为（向后兼容，不强迫老前端必须升级才能用）。
func shouldRunDedup(info map[string]interface{}) bool {
	v, ok := info["check_duplicate"]
	if !ok || v == nil {
		return false
	}
	return truthy(v)
}

// runDedupCheck 在 applyFileUpdate 写入前执行一次去重检查；命中 Duplicate
// 时跳过整个写入并返回一个 sentinel 错误，由 caller 把信息透传给前端。
// LikelyDuplicate 仅给 caller 参考并不阻断写入，符合「兜底提示」语义。
func runDedupCheck(ctx context.Context, filePath string, info map[string]interface{}) error {
	if !shouldRunDedup(info) || dedupChecker == nil {
		return nil
	}
	r := dedupChecker.Check(ctx, filePath, dedup.Options{MusicRoot: utils.MusicRoot()})
	switch r.Verdict {
	case dedup.VerdictDuplicate, dedup.VerdictLikelyDuplicate:
		return ErrDuplicateSkipped{Dup: r}
	case dedup.VerdictError:
		// dedup 错误本身不阻塞写流程（保守策略：工具失败仍允许写入）。
		return nil
	}
	return nil
}

// ErrDuplicateSkipped 是 applyFileUpdate 在去重命中时返回的 sentinel 错误，
// 让 caller 判断该不该把信息回写到前端（区分写失败与主动跳过）。
type ErrDuplicateSkipped struct{ Dup dedup.Result }

func (e ErrDuplicateSkipped) Error() string {
	if e.Dup.DuplicatePath != "" {
		return fmt.Sprintf("duplicate: %s", e.Dup.DuplicatePath)
	}
	return "duplicate"
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
	// 去重前置检查：caller 通过 info["check_duplicate"]=true 开启；
	// 命中 Duplicate 即跳过整张文件的写入。
	if err := runDedupCheck(context.Background(), filePath, info); err != nil {
		return err
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

// isAudioFile reports whether path is a library audio container we can
// tag-write. Delegates to audioext so this predicate can never drift from
// the file browser's listing filter again (REVIEW.md P2-1).
func isAudioFile(path string) bool {
	return audioext.IsLibraryPath(path)
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
//
//	(a) validates the URL up front (scheme + resolved IPs),
//	(b) re-validates every redirect hop (so a public → private pivot
//	    via 302 is refused),
//	(c) caps the body at 20MB.
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
