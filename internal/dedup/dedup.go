// Package dedup 提供刮削时的重复文件检查。
//
// 入口：Check(path, opts)。Check 以三级漏斗逐层判定，匹配越早越便宜。
//
//  1. 文件名校验 ── 与库内已收录（Task 表 success 行 + 磁盘真实存在）的同名
//     音频做精确比较；命中即返回 Duplicate，跳过昂贵的 hash/fingerprint。
//
//  2. 数字指纹 ── 计算 SHA-256(audio-body) 与库内所有 fileSize 命中阈值的
//     文件交叉比对；同一密文 → 不同文件名但内容完全一致 → 重复。
//
//  3. 声纹校验 ── 仅在前两阶无果且库内已有同曲（同 title+同 duration±2s 名）
//     时启用 fpcalc，把两份音频都打 fingerprint 再做相同字符串比较；既昂贵
//     又是兜底（与 smart_tag 也用 acoustid 的 codepath 一致）。
//
//  4. 元数据对比 ── title/artist/album/duration 归一化后比对，弱信号 → 仅
//     标记「疑似重复」（LikelyDuplicate result），不打断写入流程。
//
// 设计目标：让 applyFileUpdate 与 batchtag.tagOne 在调用 tag.Write 前可按需
// 跳过整张文件而无需重复实现这套漏斗；服务端可选不阻塞前端 UX。
package dedup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"go-music-tag/internal/tag"
)

// gormDBWrapper 是历史扩散名；实际类型就是 *gorm.DB。用 alias 让 struct
// 字段不清空；为 future-proof。
type gormDBWrapper = gorm.DB

// Result 描述一次 Check 的判定结果。handler/helper 据此决定写入与否和 UI 反馈。
type Result struct {
	// Verdict ∈ {Unique, Duplicate, LikelyDuplicate, Skipped, Error}。
	Verdict string `json:"verdict"`
	// Run 用以指示触发 / 未启用某漏斗层（filename / hash / fingerprint / meta）。
	Run []string `json:"run"`
	// DuplicatePath 命中时的对端文件相对 MUSIC_DIR 路径（不需要时为空）。
	DuplicatePath string `json:"duplicate_path,omitempty"`
	// MatchField 标记第一次命中的字段：filename / sha256 / fingerprint / meta。
	MatchField string `json:"match_field,omitempty"`
	// Reason 给 UI 展示的人话：刻意写得“给用户看”，不带技术埋点。
	Reason string `json:"reason,omitempty"`
	// Err Verdict==Error 时携带底层错误。
	Err error `json:"-"`
}

const (
	VerdictUnique          = "unique"
	VerdictDuplicate       = "duplicate"
	VerdictLikelyDuplicate = "likely_duplicate"
	VerdictSkipped         = "skipped"
	VerdictError           = "error"

	stageFilename    = "filename"
	stageHash        = "hash"
	stageFingerprint = "fingerprint"
	stageMeta        = "meta"
)

// Options 给 caller 单层开关。零值 ⇒ 全启用；可关闭价格贵的两层。
type Options struct {
	// DisableHash true 时跳过 SHA-256 层（使主要用作磁盘/网络压力场景的 opt-out）。
	DisableHash bool
	// DisableFingerprint true 时跳过 fpcalc 层（避免 spawn 子进程；环境无 fpcalc 时自动 skip）。
	DisableFingerprint bool
	// DisableMeta true 时跳过元数据比对（仅以强证据判定）。
	DisableMeta bool
	// MusicRoot 必须是库根目录；唯一参与 fingerprint hash 比对时的相对路径基准。
	// 为空时默认走 utils.MusicRoot()。
	MusicRoot string
}

// Checker 持有可复用的 fpcalc 句柄与 fs 缓存，避免每次 Check 重新发现 fpcalc。
type Checker struct {
	mu sync.Mutex

	db        *gorm.DB // 可为 nil；nil 则降级到纯 FS 比对
	musicRoot string

	// fpcalc path ""; 未查到二进制时 disabled=true 会跳过 fingerprint 层。
	fpcalcPath     string
	fpcalcOnce     sync.Once
	fpcalcDisabled bool
}

// New 构造一个全局复用的 Checker；DB 为空时仅文件系统层生效。
func New(database *gorm.DB, musicRoot string) *Checker {
	c := &Checker{db: database}
	if musicRoot != "" {
		c.musicRoot = musicRoot
	} else {
		c.musicRoot = defaultRoot()
	}
	return c
}

func defaultRoot() string {
	if v := os.Getenv("MUSIC_DIR"); v != "" {
		return v
	}
	return "/app/media"
}

// Check 对 path 执行去重判定。预期调用约定：path 必须是绝对物理路径，且
// caller 已通过 utils.SafeJoin 校验过它落在 MUSIC_DIR 之内。
//
// DB 不为 nil 会查 Task 表（含 /music_track 触及表）以避免磁盘扫描；
// 为 nil 时 fall back 到 os.ReadDir 的全库扫描（O(n) 但内存占用极低）。
//
// 超时由 caller 通过 ctx 控制；本函数内部不再 wrap 第二层 timeout，
// 以便 caller 用统一 deadline 调度 hash + fingerprint（hash 一个文件通常 < 100ms）。
func (c *Checker) Check(ctx context.Context, path string, opts Options) Result {
	if path == "" {
		return Result{Verdict: VerdictError, Err: errors.New("empty path")}
	}
	if _, err := os.Stat(path); err != nil {
		return Result{Verdict: VerdictError, Err: fmt.Errorf("stat %s: %w", path, err)}
	}
	run := []string{}
	res := Result{Verdict: VerdictUnique, Run: run}

	// Stage 1: filename
	if dup, ok := c.checkFilename(ctx, path, opts); ok {
		res.Verdict = VerdictDuplicate
		res.Run = append(res.Run, stageFilename)
		res.MatchField = stageFilename
		res.DuplicatePath = dup
		res.Reason = "库内已存在同名音频"
		return res
	}
	res.Run = append(res.Run, stageFilename)

	// Stage 2: hash
	if !opts.DisableHash {
		if dup, ok, err := c.checkHash(ctx, path, opts); err == nil && ok {
			res.Verdict = VerdictDuplicate
			res.Run = append(res.Run, stageHash)
			res.MatchField = stageHash
			res.DuplicatePath = dup
			res.Reason = "音频内容指纹（SHA-256）与库内文件完全一致"
			return res
		}
		res.Run = append(res.Run, stageHash)
	}

	// Stage 4 (pre): read meta + size for fingerprint/metabits
	fi, _ := os.Stat(path)
	_ = fi

	// Stage 3: fingerprint (deferred; only when a possible pair exists)
	if !opts.DisableFingerprint && c.fpcalcAvailable() {
		if dup, ok, err := c.checkFingerprint(ctx, path, opts); err == nil && ok {
			res.Verdict = VerdictDuplicate
			res.Run = append(res.Run, stageFingerprint)
			res.MatchField = stageFingerprint
			res.DuplicatePath = dup
			res.Reason = "声纹（acoustic fingerprint）比对一致"
			return res
		}
		res.Run = append(res.Run, stageFingerprint)
	}

	// Stage 4: metadata weak match
	if !opts.DisableMeta {
		if dup, score := c.checkMeta(ctx, path, opts); dup != "" && score >= 0.7 {
			res.Verdict = VerdictLikelyDuplicate
			res.Run = append(res.Run, stageMeta)
			res.MatchField = stageMeta
			res.DuplicatePath = dup
			res.Reason = fmt.Sprintf("音乐元数据相似度 %.0f%%，疑似重复", score*100)
			return res
		}
		res.Run = append(res.Run, stageMeta)
	}
	return res
}

// ─── Stage 1: filename ──────────────────────────────────────────────────────

// checkFilename 在库内追同名音频文件（不含自身），命中返回 dup 的相对路径。
// 当 DB 可用时，直接命中 Task 表 + sqlite unique path == name 比对；
// DB 为空时走 os.ReadDir 父目录扫描一次（限制 O(dir)）。
func (c *Checker) checkFilename(_ context.Context, path string, _ Options) (string, bool) {
	base := filepath.Base(path)
	if base == "" {
		return "", false
	}
	root := c.musicRoot
	if root == "" {
		return "", false
	}

	// 自己不算重复。
	abs := filepath.Clean(path)
	// 优先：同一曲子可能在不同 album dir 下重名；扫一遍同名候选。
	// DB 有则查 Task 表。
	if c.db != nil {
		var samepaths []string
		// Task 表里 success 行可能不止一个（多目录同名）；用 filename 直接查。
		err := c.db.Raw(
			"SELECT full_path FROM task_task WHERE filename = ? AND state = ?",
			base, "success",
		).Scan(&samepaths).Error
		if err == nil {
			for _, sp := range samepaths {
				if filepath.Clean(sp) == abs {
					continue
				}
				if _, statErr := os.Stat(sp); statErr == nil {
					return relOrSelf(sp, root), true
				}
			}
			// 退一步：查 music_track 表
			var tracks []string
			err = c.db.Table("music_track").
				Where("suffix = ? AND name LIKE ?", filepath.Ext(base)[1:], "%"+base).
				Limit(50).Pluck("path", &tracks).Error
			if err == nil {
				for _, sp := range tracks {
					if sp == "" {
						continue
					}
					full := filepath.Join(root, sp)
					if filepath.Clean(full) == abs {
						continue
					}
					if _, statErr := os.Stat(full); statErr == nil {
						return sp, true
					}
				}
			}
		}
	}

	// Fallback（DB 缺失）：直接扫 MUSIC_DIR 全卷一次，找同名。
	// 这条路径仅在初次或测试场景出现；生产请保持 db != nil。
	candidates, _ := findFilesByName(root, base)
	for _, c2 := range candidates {
		if filepath.Clean(c2) == abs {
			continue
		}
		return relOrSelf(c2, root), true
	}
	return "", false
}

// errLimitReached stops a filepath.Walk once a helper has collected enough
// candidates.
//
// The idiom is a sentinel error, NOT filepath.SkipDir: SkipDir only takes
// effect when returned for a *directory*, so returning it from the
// per-file callback skipped that one file's remaining siblings at best and
// did nothing at all in the general case — the documented `limit` on all
// three scan helpers was simply not enforced.
var errLimitReached = errors.New("dedup: scan limit reached")

// walkLimit collects from walkFn until limit candidates are found, then
// stops. A limit below 1 means "no limit".
func walkLimit(root string, limit int, walkFn func(path string, size int64) bool) ([]string, error) {
	out := make([]string, 0, 16)
	err := filepath.Walk(root, func(p string, fi os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		if fi.IsDir() {
			return nil
		}
		if !walkFn(p, fi.Size()) {
			return nil
		}
		out = append(out, p)
		if limit > 0 && len(out) >= limit {
			return errLimitReached
		}
		return nil
	})
	if errors.Is(err, errLimitReached) {
		return out, nil
	}
	return out, err
}

// findFilesByName 在 root 下递归查找同名文件，最多返回 limit 个（<=0 不限）。
// 返回绝对路径。
func findFilesByName(root, name string) ([]string, error) {
	return walkLimit(root, 8, func(p string, _ int64) bool {
		return filepath.Base(p) == name
	})
}

// ─── Stage 2: SHA-256 hash ──────────────────────────────────────────────────

// checkHash 计算 path 的 SHA-256，并在库内filesize 接近的候选文件上做相同
// 计算；只要 hash 命中即判定为重复内容（数字层面完全相同）。
//
// 优化限制：
//   - fileSize 完全相等才转 hash（避免给 1000 首 5MB 文件逐个做 SHA-256）。
//   - DB 有则按 size 取候选；DB 为空时退化为遍历 root 找同 size 文件。
func (c *Checker) checkHash(ctx context.Context, path string, _ Options) (string, bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", false, err
	}
	target := fi.Size()
	if target == 0 {
		return "", false, nil // 0 字节视为不参与。
	}
	myHash, err := sha256OfFile(path)
	if err != nil {
		return "", false, err
	}

	root := c.musicRoot
	// DB 路径：查 music_track 拿 size + path，或者扫 Task 表 full_path。
	if c.db != nil {
		var paths []string
		// 走 Track 表（有 size 字段可索引）。
		err = c.db.Table("music_track").
			Where("size = ?", target).
			Limit(512).
			Pluck("path", &paths).Error
		if err == nil {
			for _, rel := range paths {
				if rel == "" {
					continue
				}
				full := filepath.Join(root, rel)
				if filepath.Clean(full) == filepath.Clean(path) {
					continue
				}
				if _, statErr := os.Stat(full); statErr != nil {
					continue
				}
				h, e := sha256OfFile(full)
				if e == nil && h == myHash {
					return rel, true, nil
				}
				if ctx.Err() != nil {
					return "", false, ctx.Err()
				}
			}
		}
	}

	// Fallback：DB 缺失时遍历 root 全盘，但仅查看同 size 文件（极少）。
	cands, _ := findFilesBySize(root, target, 1024)
	for _, c2 := range cands {
		if filepath.Clean(c2) == filepath.Clean(path) {
			continue
		}
		h, e := sha256OfFile(c2)
		if e == nil && h == myHash {
			return relOrSelf(c2, root), true, nil
		}
		if ctx.Err() != nil {
			return "", false, ctx.Err()
		}
	}
	return "", false, nil
}

// findFilesBySize 在 root 下递归查找大小为 size 字节的文件（≤ limit 条）。
func findFilesBySize(root string, size int64, limit int) ([]string, error) {
	return walkLimit(root, limit, func(_ string, sz int64) bool {
		return sz == size
	})
}

// sha256OfFile 计算 path 内容的 SHA-256，返回 hex 编码字符串。
// 大音频文件按 1 MiB chunk 流式读取，内存 footprint 在 KB 级。
func sha256OfFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 1<<20) // 1 MiB
	if _, err := io.CopyBuffer(h, f, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ─── Stage 3: fpcalc fingerprint ────────────────────────────────────────────

// checkFingerprint 仅在有潜在 duplicate 候选时启动：
// 我们已经在 stage1/2 过滤掉了「同名 + 同 hash」；剩下还有一种场景即
// 「不同名 / 不同 hash / 但音频解码后 wave 一样」（重编码重复）。这种情况
// 用 chromaprint FINGERPRINT 字段比对就能识别。
//
// 当前实现：在 DB / FS 里找同 duration (±2s) 的候选，逐个跑 fpcalc，比
// fingerprint 字符串相等即判重。fpcalc 在机器上的概率也不高，所以联系
// acoustid service 的 LookPath 一致；缺失则跳过本层即可。
func (c *Checker) checkFingerprint(ctx context.Context, path string, opts Options) (string, bool, error) {
	if !c.fpcalcAvailable() {
		return "", false, nil
	}
	myFP, myDur, err := c.runFPCalc(path)
	if err != nil || myFP == "" {
		return "", false, err
	}
	root := c.musicRoot

	// 候选集合：同 duration ±2s 的库内 Tracks。
	// 这里采用：DB 查 size 范围（duration 没有直接列；duration 在 music_track
	// 的字段是真实秒数；同一曲复压 size 可能差几 MB 但 duration 几乎一致）。
	// Fallback：DB 缺失时，按 size 在 [target*0.8, target*1.2] 区间找候选。
	fi, _ := os.Stat(path)
	target := int64(0)
	if fi != nil {
		target = fi.Size()
	}
	candidates := []string{}

	if c.db != nil {
		var rels []string
		if target > 0 {
			lo := target * 8 / 10
			hi := target * 12 / 10
			_ = c.db.Table("music_track").
				Where("size BETWEEN ? AND ?", lo, hi).
				Limit(64).
				Pluck("path", &rels).Error
		} else {
			_ = c.db.Table("music_track").
				Where("duration BETWEEN ? AND ?", float64(myDur)-2, float64(myDur)+2).
				Limit(64).
				Pluck("path", &rels).Error
		}
		for _, rel := range rels {
			if rel == "" {
				continue
			}
			full := filepath.Join(root, rel)
			if filepath.Clean(full) == filepath.Clean(path) {
				continue
			}
			if _, err := os.Stat(full); err == nil {
				candidates = append(candidates, full)
			}
			if ctx.Err() != nil {
				return "", false, ctx.Err()
			}
		}
	}
	if len(candidates) == 0 && root != "" {
		// 退一步：size 大区间扫盘。
		lo := target * 7 / 10
		hi := target * 13 / 10
		fsCands, _ := findFilesBySizeBetween(root, lo, hi, 64)
		for _, c2 := range fsCands {
			if filepath.Clean(c2) == filepath.Clean(path) {
				continue
			}
			candidates = append(candidates, c2)
		}
	}

	for _, cand := range candidates {
		select {
		case <-ctx.Done():
			return "", false, ctx.Err()
		default:
		}
		ifcand, dur, err := c.runFPCalc(cand)
		if err != nil || ifcand == "" {
			continue
		}
		if myDur > 0 && dur > 0 && absi(myDur-dur) > 5 {
			continue // ±5s 误差宽松阈值（fpcalc 解码差异）
		}
		if ifcand == myFP {
			return relOrSelf(cand, root), true, nil
		}
	}
	return "", false, nil
}

// fpcalcAvailable 懒求一次：超时无 fpcalc 时以后永远跳过本层。
func (c *Checker) fpcalcAvailable() bool {
	c.fpcalcOnce.Do(func() {
		if p, err := exec.LookPath("fpcalc"); err == nil {
			c.fpcalcPath = p
		} else {
			c.fpcalcDisabled = true
		}
	})
	return !c.fpcalcDisabled
}

// runFPCalc 跑 fpcalc -json audioPath 取 fingerprint + duration。
// 这代码与 internal/plugin/acoustid/server.go runFPCalc 等价（不抽公共，避免
// 循环依赖）；出错一律返回 "" 不影响主流程。
func (c *Checker) runFPCalc(audioPath string) (string, int, error) {
	if c.fpcalcDisabled || c.fpcalcPath == "" {
		return "", 0, errors.New("fpcalc disabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.fpcalcPath, "-json", audioPath)
	out, err := cmd.Output()
	if err != nil {
		return "", 0, err
	}
	// 简化：直接 strings.Cut 取 fingerprint/duration 字段（避免引 json 包）。
	fp := extractJSONField(string(out), "fingerprint")
	dur := extractJSONField(string(out), "duration")
	d := 0
	fmt.Sscanf(dur, "%d", &d)
	return fp, d, nil
}

// extractJSONField 极简字符串抽取：fpcalc -json 输出形如
// {"duration":187.123,"fingerprint":"AQAB..."}
func extractJSONField(s, key string) string {
	k := "\"" + key + "\":"
	i := strings.Index(s, k)
	if i < 0 {
		return ""
	}
	s = s[i+len(k):]
	// 数字类型
	if s != "" && (s[0] >= '0' && s[0] <= '9' || s[0] == '-') {
		j := 0
		if s[0] == '-' {
			j = 1
		}
		for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == '.') {
			j++
		}
		return s[:j]
	}
	// 字符串类型
	if strings.HasPrefix(s, "\"") {
		s = s[1:]
		j := strings.IndexByte(s, '"')
		if j < 0 {
			return ""
		}
		return s[:j]
	}
	return ""
}

// findFilesBySizeBetween 在 root 下递归查找 size 在 [lo, hi] 的文件，
// 上限 limit。lo/hi 是 int64 字节数。
func findFilesBySizeBetween(root string, lo, hi int64, limit int) ([]string, error) {
	return walkLimit(root, limit, func(_ string, sz int64) bool {
		return sz >= lo && sz <= hi
	})
}

// ─── Stage 4: metadata weak check ───────────────────────────────────────────

// checkMeta 读 path 与每个候选的 tag.Title/Artist/Album + 时长做归一化比较，
// 得一个 0..1 相似度；>=0.7 即判定为 LikelyDuplicate。这一步不写磁盘、不
// 进 spawn。
//
// 由于「无 hash 命中 + 怕漏放」的兜底性质，候选集限定同 artist 同曲名。
func (c *Checker) checkMeta(ctx context.Context, path string, _ Options) (string, float64) {
	// 候选集合：music_track 表中 name LIKE + artist 命中（DB 路径）。
	// DB 缺失时这一层直接返回空（避免扫盘时的 tag.Read 数千次）。
	if c.db == nil {
		return "", 0
	}
	my := readTransposed(path)
	if my.Title == "" && my.Artist == "" {
		return "", 0 // 缺元数据无法比对
	}

	var rels []string
	// 简单 %name% 模糊找同名 track。
	like := strings.Replace(strings.ToLower(my.Title), "%", "\\%", -1)
	if like == "" {
		return "", 0
	}
	_ = c.db.Table("music_track").
		Where("LOWER(name) LIKE ?", "%"+like+"%").
		Limit(64).
		Pluck("path", &rels).Error

	bestScore := 0.0
	bestRel := ""
	root := c.musicRoot
	for _, rel := range rels {
		if rel == "" {
			continue
		}
		full := filepath.Join(root, rel)
		if filepath.Clean(full) == filepath.Clean(path) {
			continue
		}
		if _, err := os.Stat(full); err != nil {
			continue
		}
		meta := readTransposed(full)
		score := metaSimilarity(my, meta)
		if score > bestScore {
			bestScore = score
			bestRel = rel
		}
		if ctx.Err() != nil {
			break
		}
	}
	return bestRel, bestScore
}

// metaRecord 是 tag 读回后给 meta 对比用的简化结构。
type metaRecord struct {
	Title    string
	Artist   string
	Album    string
	Duration float64
}

func readTransposed(path string) metaRecord {
	if info, err := readTag(path); err == nil {
		return metaRecord{
			Title:    normLower(info.Title),
			Artist:   normLower(info.Artist),
			Album:    normLower(info.Album),
			Duration: float64(info.Duration),
		}
	}
	return metaRecord{}
}

// readTag 是 internal/tag.Read 的薄封装；放在本文件里避免在多 os/arch
// 构建环境下抽出 dedup_tag.go，并使本文件单一文件可被独立测试。
// tag.Read 返回的 *tag.TagInfo 已含常见字段 (Title/Artist/Album/Duration)。
func readTag(path string) (*tag.TagInfo, error) {
	return tag.Read(path)
}

// metaSimilarity 给出两份归一化元数据的近似匹配分。全等 1.0，部分贡献
// 0.5；空字段不贡献（但若两边都为空则全等权重不变）。
func metaSimilarity(a, b metaRecord) float64 {
	var w float64 = 0
	var total float64 = 0
	const wTitle = 0.5
	const wArtist = 0.3
	const wAlbum = 0.2
	if a.Title != "" && a.Title == b.Title {
		w += wTitle
	}
	total += wTitle
	if a.Artist != "" && a.Artist == b.Artist {
		w += wArtist
	}
	total += wArtist
	if a.Album != "" && a.Album == b.Album {
		w += wAlbum
	}
	total += wAlbum
	if total == 0 {
		return 0
	}
	// Duration 相近 +0.05 cap 至 1.0
	// Duration 相近 +0.05，cap 至 1.0。
	//
	// Note this term can only ever be a no-op in practice: the three field
	// weights already sum to exactly 1.0, so a record matching on title,
	// artist AND album scores 1.0 whether the durations line up or not.
	// It still moves the needle for partial matches (title-only is 0.5, and
	// a duration agreement takes it to 0.55). Making a large duration gap
	// *subtract* would be a scoring change with no effect on any verdict,
	// since 0.95 is comfortably over the 0.7 likely-duplicate threshold —
	// so a live version and its studio cut still pair up, which is the
	// behaviour a music library wants.
	if a.Duration > 0 && b.Duration > 0 {
		d := mathAbs(a.Duration - b.Duration)
		if d <= 2 {
			w += 0.05
		}
		if w > 1 {
			w = 1
		}
	}
	return w / total
}

// ─── utils ──────────────────────────────────────────────────────────────────

func relOrSelf(full, root string) string {
	if root == "" {
		return full
	}
	rel, err := filepath.Rel(root, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return full
	}
	return rel
}

func absi(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func mathAbs(n float64) float64 {
	if n < 0 {
		return -n
	}
	return n
}

func normLower(s string) string {
	return strings.TrimSpace(strings.ToLower(s))
}
