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
// 设计目标：让 applyFileUpdate 在调用 tag.Write 前可按需跳过整张文件而无需
// 重复实现这套漏斗；服务端可选不阻塞前端 UX。
package dedup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gorm.io/gorm"

	"go-music-tag/internal/fingerprint"
	"go-music-tag/internal/tag"
)

// gormDBWrapper 是历史扩散名；实际类型就是 *gorm.DB。用 alias 让 struct
// 字段不清空；为 future-proof。
type gormDBWrapper = gorm.DB

// Result 描述一次 Check 的判定结果。handler/helper 据此决定写入与否和 UI 反馈。
type Result struct {
	// Verdict ∈ {Unique, Duplicate, LikelyDuplicate, Skipped, Error}。
	//
	// Duplicate means content-level evidence (SHA-256 or fpcalc) and is
	// the only verdict that may justify refusing a write. A name clash and
	// a metadata-similarity hit both come back LikelyDuplicate, which is a
	// warning only.
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

// SetMusicRoot repoints an existing Checker at a different library root.
//
// The gateway builds one Checker and reuses it for the life of the process,
// so a root captured at construction goes stale the moment MUSIC_DIR is not
// literally the value it had at boot — which is exactly what a test with
// t.Setenv does, and it made dedup verdicts depend on test ordering. Reading
// the root per call is cheap; the genuinely expensive state (fpcalc
// discovery) is behind the Checker's own sync.Once and survives this.
func (c *Checker) SetMusicRoot(root string) {
	if root == "" {
		return
	}
	c.mu.Lock()
	c.musicRoot = root
	c.mu.Unlock()
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
// 漏斗分两类证据。filename 与 meta 两级只是弱信号，一律返回
// VerdictLikelyDuplicate（警告）；只有 hash / fingerprint 两级——内容
// 本身相同——才返回 VerdictDuplicate，而那是唯一可以拒绝写入的判定。
//
// 有索引时先查 music_folder（扫描器唯一写入的表）以避免磁盘扫描；
// 索引里查不到时 fall back 到有界的全库 walk（O(n) 但内存占用极低）。
// 每个阶段都保留兜底，所以索引缺失只损失速度，不影响判定。
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
	//
	// A name clash is NOT strong evidence. "track01.mp3", "01 - Song.mp3"
	// and the same title under two albums all collide here while being
	// completely different recordings, and the caller treats
	// VerdictDuplicate as "skip the write". Content evidence — SHA-256 in
	// stage 2, fpcalc in stage 3 — is what justifies blocking; a filename
	// only earns a warning. MatchField still says "filename" so the UI can
	// say why.
	if dup, ok := c.checkFilename(ctx, path, opts); ok {
		res.Verdict = VerdictLikelyDuplicate
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
	//
	// The index is the fast path. It used to query task_task (written by
	// nothing since the worker auto-scrape chain was deleted) and then
	// music_track (never written by anything), so both branches returned
	// nothing and every name clash was found by the full-volume walk below.
	// music_folder is the table the scanners actually populate, with one row
	// per audio file under file_type='music'.
	for _, sp := range c.libraryFiles(indexCandidateLimit, "name = ?", base) {
		if isSelf(sp, abs) {
			continue
		}
		if _, statErr := os.Stat(sp); statErr == nil {
			return relOrSelf(sp, root), true
		}
	}

	// Fallback（索引里没有）：扫 MUSIC_DIR 全卷一次，找同名。
	// 一条 walk，代价与库大小成正比，所以索引命中时是可观的提速。
	candidates, _ := findFilesByName(root, base)
	for _, c2 := range candidates {
		if isSelf(c2, abs) {
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
	// 索引路径：按 size 从 music_folder 取候选。这张表此前查的是
	// music_track，而那张表从没有人写入，所以这里永远返回空，真正干活的
	// 一直是下面那条全盘 walk。size 由扫描器写入（见 tasks.fileSize）。
	for _, full := range c.libraryFiles(hashCandidateLimit, "size = ?", target) {
		if isSelf(full, path) {
			continue
		}
		if _, statErr := os.Stat(full); statErr != nil {
			continue
		}
		h, e := sha256OfFile(full)
		if e == nil && h == myHash {
			return relOrSelf(full, root), true, nil
		}
		if ctx.Err() != nil {
			return "", false, ctx.Err()
		}
	}

	// Fallback：索引里没有时遍历 root 全盘，但仅查看同 size 文件（极少）。
	cands, _ := findFilesBySize(root, target, 1024)
	for _, c2 := range cands {
		if isSelf(c2, path) {
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

// checkFingerprint 找「同一首歌的不同编码」：不同名、不同 hash，但解码后
// 声纹一致。
//
// 候选集按**时长**而不是文件大小选。同一首歌换编码，大小可以差 25 倍（120
// 秒的歌在 96k opus 下约 1.2MB，flac 下约 30MB），而时长不变。所以旧版
// 的 size ±20% 窗口恰好漏掉了最典型的无损转有损重复：实测里只有 vorbis /
// opus 200k 落进窗口，flac、mp3、wav 全部在窗口外。
//
// 时长读自 music_folder.duration，由索引任务预先算好（见
// internal/tasks/fpindex.go）。没有索引时退回按大小扫盘，并明确标注这条
// 路径仍然会漏掉大幅变小的重编码。
//
// 比较用子指纹位距离而不是压缩指纹字符串全等，原因见 fingerprint.go。
func (c *Checker) checkFingerprint(ctx context.Context, path string, opts Options) (string, bool, error) {
	if !c.fpcalcAvailable() {
		return "", false, nil
	}
	myFP, err := subFingerprint(ctx, c.fpcalcPath, path)
	if err != nil {
		// 解不出来（不是音频、被截断、fpcalc 缺失）都只是「本层没结论」，
		// 不是「无重复」。
		return "", false, err
	}
	root := c.musicRoot

	candidates := c.durationCandidates(myFP.Duration(), path)
	if len(candidates) == 0 && root != "" && myFP.Duration() <= 0 {
		// 没有时长索引（或本文件时长读不出来）时的退路。窗口比旧的 ±20%
		// 宽，因为后面还要过声纹比对这一关，误纳的候选只是多花 0.4s。
		if fi, serr := os.Stat(path); serr == nil && fi.Size() > 0 {
			lo := fi.Size() / 2
			hi := fi.Size() * 2
			fsCands, _ := findFilesBySizeBetween(root, lo, hi, 64)
			for _, p := range fsCands {
				if !isSelf(p, path) {
					candidates = append(candidates, p)
				}
			}
		}
	}

	for _, cand := range candidates {
		select {
		case <-ctx.Done():
			return "", false, ctx.Err()
		default:
		}
		if isSelf(cand, path) {
			continue
		}
		candFP, cerr := subFingerprint(ctx, c.fpcalcPath, cand)
		if cerr != nil {
			continue // 候选解不出来：不是匹配，跳过
		}
		if sameTrack(myFP, candFP) {
			return relOrSelf(cand, root), true, nil
		}
	}
	return "", false, nil
}

// durationToleranceSeconds is how far apart two durations may be and still
// be considered the same track. fpcalc reports the decoded length, which
// encoders round differently (a track can gain or lose a fraction of a
// second, and some containers pad), so exact equality is too strict.
const durationToleranceSeconds = 5

// durationCandidates returns library audio files whose indexed duration is
// within durationToleranceSeconds of target. Returns nil when target is
// unknown or the index has no durations yet — the caller then falls back.
func (c *Checker) durationCandidates(target int, _ string) []string {
	if target <= 0 || c.db == nil {
		return nil
	}
	lo := int64(target - durationToleranceSeconds)
	hi := int64(target + durationToleranceSeconds)
	var paths []string
	err := c.db.Table("music_folder").
		Where("file_type = ?", musicFileType).
		// duration > 0 excludes rows the index task has not reached yet.
		// Without it every unindexed row is duration=0, and a 0-0 window
		// would match the entire library for any file under 5s long.
		Where("duration > 0 AND duration BETWEEN ? AND ?", lo, hi).
		Limit(hashCandidateLimit).
		Pluck("path", &paths).Error
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if abs := resolveUnderRoot(c.musicRoot, p); abs != "" {
			out = append(out, abs)
		}
	}
	return out
}

// fpcalcAvailable 懒求一次：超时无 fpcalc 时以后永远跳过本层。
func (c *Checker) fpcalcAvailable() bool {
	c.fpcalcOnce.Do(func() {
		p, err := fingerprint.LookPath()
		if err != nil {
			c.fpcalcDisabled = true
			// Logged, because a skipped stage and a stage that found
			// nothing produce the same Result, and the first one used to be
			// invisible.
			log.Printf("[dedup] fingerprint stage disabled: %v", err)
			return
		}
		c.fpcalcPath = p
	})
	return !c.fpcalcDisabled
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
	my := readTransposed(path)
	if my.Title == "" && my.Artist == "" {
		return "", 0 // 缺元数据无法比对
	}
	// 候选集合：索引里曲名模糊命中的音频。
	//
	// The old version queried music_track, a table nothing has ever written,
	// and had no fallback at all: an empty result was taken as the answer.
	// Since that table is permanently empty, this stage could never produce a
	// verdict — the one weak-signal stage that exists to catch re-tagged
	// copies was inert. Unlike the other three stages there is nothing to
	// degrade to, so a missing index silently meant "no duplicates".
	//
	// The artist comparison is not part of the query: music_folder has no
	// artist column, and filtering on it here would be a second reason for
	// the stage to come up empty. metaSimilarity weights it instead, and a
	// wrong artist simply scores lower rather than disappearing.
	like := strings.Replace(strings.ToLower(my.Title), "%", "\\%", -1)
	if like == "" {
		return "", 0
	}
	candidates := c.libraryFiles(indexCandidateLimit, "LOWER(name) LIKE ?", "%"+like+"%")

	// Fallback（索引里没有）：按曲名走一遍全盘，但只 stat 匹配的 basename。
	// 比 collect-all-then-read-tags 便宜得多 —— tag.Read 仍然只对最终的候选
	// 调用，最多 indexCandidateLimit 次。
	if len(candidates) == 0 && c.musicRoot != "" {
		fsCands, _ := walkLimit(c.musicRoot, indexCandidateLimit,
			func(p string, _ int64) bool {
				return strings.Contains(strings.ToLower(filepath.Base(p)), like)
			})
		candidates = append(candidates, fsCands...)
	}

	bestScore := 0.0
	bestRel := ""
	root := c.musicRoot
	for _, full := range candidates {
		if full == "" || isSelf(full, path) {
			continue
		}
		if _, err := os.Stat(full); err != nil {
			continue
		}
		score := metaSimilarity(my, readTransposed(full))
		if score > bestScore {
			bestScore = score
			bestRel = relOrSelf(full, root)
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

// ─── library index ──────────────────────────────────────────────────────────

// Candidate bounds per stage. These stages are a funnel, not an exhaustive
// comparison: each answers "is there a plausible duplicate", and a bound
// keeps a pathological library from turning one Check() into thousands of
// hashes or tag reads. The hash stage gets a larger budget because an exact
// size match is a precise filter — a library of 5000 same-length tracks
// should still be searchable — while hashing is far cheaper per candidate
// than reading and comparing tags.
const (
	// indexCandidateLimit is the default for name, size-range and metadata
	// lookups.
	indexCandidateLimit = 64
	// hashCandidateLimit is the exact-size-match budget.
	hashCandidateLimit = 512
)

// musicFileType is the file_type the scanners assign to audio files. Rows
// with any other value — directories, cover images, and the youtube download
// cache — are not library audio and must not be offered as duplicate
// candidates.
const musicFileType = "music"

// libraryFiles returns absolute paths of indexed audio files matching
// `cond`, or nil when there is no index to consult.
//
// Why music_folder and not music_track: the scanners record every path they
// see — files and folders alike — into music_folder, distinguished by
// file_type. Nothing has ever inserted a music_track row, so that table is
// permanently empty and a query against it returns nothing forever. Because
// every stage also has a filesystem-walk fallback, the effect was invisible:
// the stages kept producing correct verdicts, just by walking the disk, and
// the dead queries read as "no matches found".
//
// Why the results are absolute: music_folder.path stores the real path, so
// callers must NOT re-join the music root onto it. resolveUnderRoot accepts
// both shapes because a caller that did re-join produced
// <root>/tmp/.../song.mp3, which fails every os.Stat and reports "unique"
// against a table full of exact duplicates.
func (c *Checker) libraryFiles(limit int, cond string, args ...interface{}) []string {
	if c.db == nil {
		return nil
	}
	if limit <= 0 {
		limit = indexCandidateLimit
	}
	var paths []string
	err := c.db.Table("music_folder").
		Where("file_type = ?", musicFileType).
		Where(cond, args...).
		Limit(limit).
		Pluck("path", &paths).Error
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if abs := resolveUnderRoot(c.musicRoot, p); abs != "" {
			out = append(out, abs)
		}
	}
	return out
}

// resolveUnderRoot turns an index-sourced path into an absolute one.
//
// Index rows are stored absolute today, but the same value can arrive
// root-relative from other sources, so both are accepted: an absolute path is
// returned cleaned and otherwise untouched, and only a relative one is joined
// onto root. Joining an already-absolute path is the bug this avoids.
func resolveUnderRoot(root, p string) string {
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	if root == "" {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(root, p))
}

// isSelf reports whether candidate is the file under test, which must never
// be reported as its own duplicate.
func isSelf(candidate, path string) bool {
	return filepath.Clean(candidate) == filepath.Clean(path)
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
