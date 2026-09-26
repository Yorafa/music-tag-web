package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	// Register the image decoders used by the cover-art validation below.
	//
	// image.DecodeConfig only understands formats whose package has been
	// linked in via one of these blank imports; the stdlib `image` package
	// itself ships no codecs. Without them EVERY image is rejected with
	// "image: unknown format" — which is exactly what fetchRemoteBytes was
	// doing in production, silently breaking every remote cover fetch.
	// Pinned to the formats taglib writes (JPEG is what tag/reader.go
	// assumes when it has to pick a mime type).
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

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
		res, err := applyFileUpdate(filePath, info)
		if err != nil {
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
		report.addDone(rawPath, res.RenamedTo)
		for _, m := range res.FailedSidecars() {
			report.addSidecarWarning(rawPath, m)
			log.Printf("[update_id3] sidecar %s -> %s did not follow %s: %v",
				filepath.Base(m.From), filepath.Base(m.To), rawPath, m.Err)
		}
		audit.Log(c.Request.Context(), audit.ActionUpdateID3, rawPath, "admin", audit.StatusSuccess, 1, info, nil)
	}
	SuccessData(c, report.toJSON())
}

// updateBatchReport 是 BatchUpdateID3 / UpdateID3 共用的「哪些写成功、哪些
// 因重复被跳过」回包包体。给前端 toast / 状态 badge 渲染。
type updateBatchReport struct {
	done     []map[string]interface{}
	skipped  []map[string]interface{}
	warnings []map[string]interface{}
}

// addSidecarWarning records a sidecar that could not follow its audio
// file. The write itself succeeded, so this is informational: the client
// shows it and moves on rather than treating the save as failed.
func (r *updateBatchReport) addSidecarWarning(path string, m tag.SidecarMove) {
	r.warnings = append(r.warnings, map[string]interface{}{
		"file_full_path": path,
		"sidecar":        filepath.Base(m.From),
		"target":         filepath.Base(m.To),
		"reason":         m.Err.Error(),
	})
}

// addDone records one successful write. newFileName is non-empty only
// when the call also renamed the file — the frontend keys its rows by
// path, so without this it would keep pointing at a name that no longer
// exists after the user renames from the detail dialog.
func (r *updateBatchReport) addDone(path, newFileName string) {
	entry := map[string]interface{}{
		"file_full_path": path,
		"status":         "updated",
	}
	if newFileName != "" {
		entry["new_file_name"] = newFileName
	}
	r.done = append(r.done, entry)
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
	if r.warnings == nil {
		r.warnings = []map[string]interface{}{}
	}
	return map[string]interface{}{
		"done":     r.done,
		"skipped":  r.skipped,
		"warnings": r.warnings,
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
		// Action picks the audit.Action to record. Only the two batch
		// actions are accepted; anything else falls back to
		// ActionBatchUpdateID3 so a stray value cannot invent an
		// unrenderable action in the audit log.
		//
		// The auto-scrape path sends ActionAutoScrape because it rides
		// this endpoint but is a different thing from a hand-made
		// uniform edit: without it a 50-track scrape is
		// indistinguishable in 操作审计 from 50 rows of "set these four
		// fields on these files".
		Action string `json:"action"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Failure(c, "invalid request")
		return
	}

	action := audit.ActionBatchUpdateID3
	if req.Action == audit.ActionAutoScrape {
		action = audit.ActionAutoScrape
	}

	// perEntry resolves which tags one select_data row gets. The original
	// contract is a single shared music_info applied to every selection —
	// "set these four fields on these forty files" — and that is still the
	// default. A row may now carry its own music_info, which is what makes
	// this endpoint usable for auto-scrape: each scraped track has its own
	// title/artist/album, and folding those into one shared map would write
	// track A's tags onto track B.
	perEntry := func(sel map[string]interface{}) map[string]interface{} {
		if own, ok := sel["music_info"].(map[string]interface{}); ok {
			return own
		}
		return req.MusicInfo
	}
	hasPerEntry := false
	for _, sel := range req.SelectData {
		if _, ok := sel["music_info"].(map[string]interface{}); ok {
			hasPerEntry = true
			break
		}
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
				merged := mergeInfo(perEntry(sel), map[string]interface{}{
					"file_full_path": leaf,
					"filename":       e.Name(),
				})
				res, err := applyFileUpdate(stringValue(merged["file_full_path"]), merged)
				if err != nil {
					if dupErr, ok := err.(ErrDuplicateSkipped); ok {
						report.addSkipped(relToMusicRoot(leaf), dupErr.Dup)
						continue
					}
					Failure(c, err.Error())
					return
				}
				report.addDone(relToMusicRoot(leaf), res.RenamedTo)
				for _, m := range res.FailedSidecars() {
					report.addSidecarWarning(relToMusicRoot(leaf), m)
					log.Printf("[batch_update_id3] sidecar %s -> %s did not follow: %v",
						filepath.Base(m.From), filepath.Base(m.To), m.Err)
				}
			}
			continue
		}
		leaf, err := utils.SafeJoin(baseDir, name)
		if err != nil {
			Failure(c, "路径不安全: "+err.Error())
			return
		}
		merged := mergeInfo(perEntry(sel), map[string]interface{}{
			"file_full_path": leaf,
		})
		res, err := applyFileUpdate(stringValue(merged["file_full_path"]), merged)
		if err != nil {
			if dupErr, ok := err.(ErrDuplicateSkipped); ok {
				report.addSkipped(relToMusicRoot(leaf), dupErr.Dup)
				continue
			}
			Failure(c, err.Error())
			return
		}
		report.addDone(relToMusicRoot(leaf), res.RenamedTo)
		for _, m := range res.FailedSidecars() {
			report.addSidecarWarning(relToMusicRoot(leaf), m)
			log.Printf("[batch_update_id3] sidecar %s -> %s did not follow: %v",
				filepath.Base(m.From), filepath.Base(m.To), m.Err)
		}
	}
	status := audit.StatusSuccess
	if len(report.skipped) > 0 && len(report.done) > 0 {
		status = audit.StatusPartial
	} else if len(report.done) == 0 && len(report.skipped) > 0 {
		status = audit.StatusSkipped
	}
	details := map[string]interface{}{
		"file_full_path": req.FileFullPath,
		"select_count":   len(req.SelectData),
		"done_count":     len(report.done),
		"skipped_count":  len(report.skipped),
	}
	if hasPerEntry {
		// req.MusicInfo is only a placeholder once rows carry their own
		// tags, so recording it would describe a write that never happened.
		// What each row actually got is in the response report.
		details["per_entry_music_info"] = true
	} else {
		details["music_info"] = req.MusicInfo
	}
	audit.Log(c.Request.Context(), action, req.FileFullPath, "admin", status, len(report.done)+len(report.skipped), details, nil)
	SuccessData(c, report.toJSON())
}

// 注: BatchAutoUpdateID3 / TidyFolder 在 task.go 里已经接入 asynq，这里保持空。
// UploadImage handles POST /api/upload_image/ — base64 returns (无 data URI 前缀)。
// maxUploadImageBytes caps an uploaded cover. 20 MiB matches the cap
// fetchRemoteBytes applies to a downloaded one, so both entry points into
// the tag pipeline agree on the ceiling.
const maxUploadImageBytes = 20 << 20

func UploadImage(c *gin.Context) {
	file, header, err := c.Request.FormFile("upload_file")
	if err != nil {
		Failure(c, "no file uploaded")
		return
	}
	defer file.Close()

	// Size first: read at most one byte past the cap so an oversized upload
	// is rejected without buffering the whole thing. The extra byte is what
	// distinguishes "exactly at the cap" from "over it".
	data, err := io.ReadAll(io.LimitReader(file, maxUploadImageBytes+1))
	if err != nil {
		Failure(c, "read error")
		return
	}
	if len(data) > maxUploadImageBytes {
		Failure(c, fmt.Sprintf("image too large (max %d MB)", maxUploadImageBytes>>20))
		return
	}
	if len(data) == 0 {
		Failure(c, "empty file")
		return
	}

	// Content-shape validation (REVIEW.md P2-12). The bytes go straight into
	// the ID3/APIC frame, so a non-image body would be written to the tag
	// and every player would then fail on that track. fetchRemoteBytes
	// already does this for the download path; uploads did not, which made
	// the two entry points into the same tag inconsistent.
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		Failure(c, "invalid image: not a decodable image")
		return
	}
	// A decompression-bomb guard: DecodeConfig reads only the header, so a
	// tiny file can declare enormous dimensions and blow up any decoder that
	// later materialises the pixels (both ours and the players').
	const maxCoverPixels = 50_000_000 // e.g. 10000x5000
	if cfg.Width <= 0 || cfg.Height <= 0 {
		Failure(c, "invalid image: zero dimension")
		return
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxCoverPixels {
		Failure(c, fmt.Sprintf("image too large (%dx%d)", cfg.Width, cfg.Height))
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
		"format":   format,
		"width":    cfg.Width,
		"height":   cfg.Height,
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

// relToMusicRoot converts a path under MUSIC_DIR back into the relative form
// this API speaks everywhere else (REVIEW.md P3-9).
//
// UpdateID3 echoed back the relative path the client had sent, while
// BatchUpdateID3 echoed the absolute leaf it built internally from
// SafeJoin — so `done[].file_full_path` meant two different things
// depending on which endpoint answered, and a caller that round-tripped
// the value into the other endpoint got a path rejected for being
// absolute. Nothing consumed the field, which is why it went unnoticed.
func relToMusicRoot(abs string) string {
	root := utils.MusicRoot()
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		// Different volumes / unresolvable root: the absolute value is
		// still more useful than an error the caller cannot act on.
		return abs
	}
	return rel
}

// applyResult reports what applyFileUpdate actually did to one file.
type applyResult struct {
	// RenamedTo is the new BASE name when the file was renamed, and empty
	// otherwise. Only the base name, because the rename target is always
	// in the same parent directory, so the caller can rebuild the
	// relative path from the one it already has.
	RenamedTo string
	// Sidecars records every sidecar that was asked to follow the file.
	// An entry with a non-nil Err did not make it; the audio is renamed
	// regardless, so these become warnings rather than failures.
	Sidecars []tag.SidecarMove
}

// FailedSidecars returns only the sidecars that did not land.
func (r applyResult) FailedSidecars() []tag.SidecarMove {
	var out []tag.SidecarMove
	for _, m := range r.Sidecars {
		if m.Err != nil {
			out = append(out, m)
		}
	}
	return out
}

// applyFileUpdate 每个文件按 MusicIDS 流：模板 → 写 tag → sidecar → 文件名模板 → 改名。
//
// Renamed targets are computed via utils.SafeAbs so callers cannot drive
// the rename into an arbitrary parent directory.
func applyFileUpdate(filePath string, info map[string]interface{}) (applyResult, error) {
	var res applyResult
	if !isAudioFile(filePath) {
		return res, nil
	}
	// 去重前置检查：caller 通过 info["check_duplicate"]=true 开启；
	// 命中 Duplicate 即跳过整张文件的写入。
	if err := runDedupCheck(context.Background(), filePath, info); err != nil {
		return res, err
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

	// Resolve and validate the rename target BEFORE any mutation.
	//
	// The collision check used to run at the very end of this function,
	// after tag.Write and HandleSidecars. That made a doomed rename a
	// partial one: the file's tags were rewritten and its .lrc / cover
	// sidecars dropped, and only then did the function report "rename
	// target already exists" and change nothing about the name. The user
	// sees a failure but the file has already been modified.
	//
	// It runs here, after `upd` is built but before tag.Write, because
	// the template has to be expanded against the tags this request is
	// about to write — see renameTemplateVars.
	//
	// The rename itself still happens last, because the sidecars are
	// written next to filePath and the target is always in the same
	// parent directory.
	var (
		renameTarget string
		renameWanted bool
	)
	if v := stringValue(info["filename"]); v != "" {
		newName := applyTemplate(v, renameTemplateVars(tmplVars, upd))
		if !strings.HasSuffix(strings.ToLower(newName), strings.ToLower(filepath.Ext(filePath))) {
			newName = newName + filepath.Ext(filePath)
		}
		newName = utils.SanitizePath(newName)
		parent := filepath.Dir(filePath)
		target := filepath.Join(parent, newName)
		// SafeAbs, not SafeJoin(TrimPrefix(...)) (REVIEW.md P2-6).
		// TrimPrefix returns its input unchanged when the prefix does not
		// match — which is exactly what happens when MUSIC_DIR is a
		// symlink, a relative path, or differs in case. SafeJoin then
		// treats the resulting absolute path as relative and produces
		// `/app/media/app/media/foo.mp3` without raising anything.
		safeTarget, sErr := utils.SafeAbs(utils.MusicRoot(), target)
		if sErr != nil {
			return res, fmt.Errorf("rename target unsafe: %w", sErr)
		}
		if safeTarget != filePath {
			// os.Rename silently REPLACES an existing destination. Two
			// tracks whose filename templates render to the same name
			// would lose one file with no error and no trace, so the
			// collision has to be caught before the rename.
			if _, statErr := os.Stat(safeTarget); statErr == nil {
				return res, fmt.Errorf("rename target already exists: %s", filepath.Base(safeTarget))
			} else if !os.IsNotExist(statErr) {
				// A permission error or I/O failure on the destination is
				// not evidence that it is free.
				return res, fmt.Errorf("rename target stat: %w", statErr)
			}
			renameTarget = safeTarget
			renameWanted = true
		}
	}

	if err := tag.Write(filePath, upd); err != nil {
		return res, fmt.Errorf("write tags: %w", err)
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
		return res, fmt.Errorf("sidecar: %w", err)
	}

	// 文件名模板 + rename
	//
	// The target was resolved and collision-checked above, before any
	// write, so all that is left here is the rename itself.
	if renameWanted {
		if err := os.Rename(filePath, renameTarget); err != nil {
			return res, fmt.Errorf("rename: %w", err)
		}
		res.RenamedTo = filepath.Base(renameTarget)
		// The sidecars written above are named after the OLD path, so
		// they have to travel too or they are orphaned under a name the
		// library no longer lists. Best-effort: the audio is already
		// renamed, and refusing the whole request would leave the client
		// unable to trust the row it just wrote.
		res.Sidecars = tag.MoveSidecars(filePath, renameTarget)
	}
	return res, nil
}

func applyTemplate(tmpl string, vars map[string]string) string {
	return utils.RenderTemplate(tmpl, vars)
}

// renameTemplateVars returns the template variables for the NEW filename,
// with the tags this request is about to write laid over the ones read off
// disk.
//
// The detail dialog submits the whole form at once — the corrected title
// AND the requested filename — so expanding `filename` against the
// on-disk tags names the file after the value the user just replaced. In
// the common case (fix a typo, ask for `${artist} - ${title}`) the
// rendered name then equals the file's current path, the handler sees a
// no-op, and the rename silently does nothing.
//
// Fields the request leaves empty are absent from upd and so keep the
// on-disk value, matching how tag.Write treats them: an empty string
// means "leave this tag alone", not "clear it".
//
// `filename` is deliberately not overlaid — $filename means the file's
// CURRENT name, which is what readFileContext already holds.
func renameTemplateVars(onDisk map[string]string, upd *tag.TagUpdate) map[string]string {
	vars := make(map[string]string, len(onDisk)+6)
	for k, v := range onDisk {
		vars[k] = v
	}
	if upd.Title != nil {
		vars["title"] = *upd.Title
	}
	if upd.Artist != nil {
		vars["artist"] = strings.Join(upd.Artist, ", ")
	}
	if upd.Album != nil {
		vars["album"] = *upd.Album
	}
	if upd.AlbumArtist != nil {
		vars["albumartist"] = *upd.AlbumArtist
	}
	if upd.DiscNumber != nil {
		vars["discnumber"] = firstBeforeSlash(*upd.DiscNumber)
	}
	if upd.TrackNumber != nil {
		vars["tracknumber"] = firstBeforeSlash(*upd.TrackNumber)
	}
	return vars
}

// readFileContext 与 Django MusicIDS.var_dict() 字段对齐。
func readFileContext(path string) map[string]string {
	info, err := tag.Read(path)
	if err != nil {
		return map[string]string{}
	}
	return map[string]string{
		"title":       info.Title,
		"artist":      info.Artist,
		"albumartist": info.AlbumArtist,
		"album":       info.Album,
		"filename":    info.Filename,
		"discnumber":  firstBeforeSlash(info.DiscNumber),
		"tracknumber": firstBeforeSlash(info.TrackNumber),
	}
}

// firstBeforeSlash 取 "3/12" 里的 "3"：Vorbis 的 disc/track 编号常写成
// "总数/本盘" 形式，模板里要的是前者。
func firstBeforeSlash(s string) string {
	if i := strings.Index(s, "/"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
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
