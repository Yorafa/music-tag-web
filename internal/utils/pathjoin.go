// Package utils 路径操作通用工具。
//
// SafeJoin 是路径穿越防护 (P1.5 issue F)：调用方传入一个受信任 root
// 目录与一个 (相对 / 绝对) 路径，函数返回 resolved 后的绝对路径，
// 但仅当该路径确实落在 root 之下才返回，否则返回 error。底层依赖
// filepath.Clean + path.Join 的语义："以 / 开头的元素" 在 Join 时
// 会被当作相对元素处理，因此 SafeJoin("/app/media", "/etc/passwd")
// 安全地返回 "/app/media/etc/passwd"。
//
// 不展开 ~，不处理符号链接 (符号链接解析应该由调用方在读取文件或
// stat 之前显式调用 filepath.EvalSymlinks 决定策略)。
package utils

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// SafeJoin 将 p 解析为相对于 root 的绝对路径，并要求结果路径实际
// 落在 root 之内 (即 filepath.Clean 后通过 prefix 校验)。任何越界
// 行为都返回 ErrUnsafe。
//
// 行为细节：
//   - root 必须为非空字符串且能被 filepath.Abs 解析为绝对路径。
//   - p 为空时返回 cleanedRoot——这是与 handler/file.go::FileList
//     文档一致的"operate-on-root sentinel"语义：调用者想要
//     "list MUSIC_DIR" / "在 root 内创建/重命名" 等空路径合法操作
//     时可以直接 SafeJoin(root, "")。需要在 API 边界拒绝空路径的
//     handler 自行在 gin binding 阶段声明 binding:"required"
//     (例如 MusicID3、UpdateID3、BatchUpdateID3)，这些 handler
//     永远走不到 SafeJoin 的空路径分支。
//   - p 为绝对路径时由 filepath.Join 把前导 / 视作路径分隔符，因此
//     "/etc/passwd" 当作 "etc/passwd" 处理；这是 Go 的标准行为。
//   - 一律通过 filepath.Clean 规范化 .. 与 .，规范化后的结果必须以
//     cleanedRoot + Sep 或等于 cleanedRoot。
//
// 该函数无 fs 副作用，纯字符串操作便于单测。
func SafeJoin(root, p string) (string, error) {
	if root == "" {
		return "", errors.New("utils: SafeJoin: root is empty")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("utils: SafeJoin: abs root %q: %w", root, err)
	}
	cleanedRoot := filepath.Clean(absRoot)
	// 空路径 = 直接作用于 root 本身（list / stat / 等价于 "directory cwd"）。
	// 见上面行为细节第二点；handler/file.go::FileList 是当前唯一依赖此
	// 约定的 caller，避免在 handler 内部重复 `if FilePath == "" { root }`。
	if p == "" {
		return cleanedRoot, nil
	}
	candidate := filepath.Clean(filepath.Join(absRoot, p))
	if candidate != cleanedRoot && !strings.HasPrefix(candidate, cleanedRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("utils: SafeJoin: %q escapes root %q (resolved %q)", p, cleanedRoot, candidate)
	}
	return candidate, nil
}

// SafeAbs validates that absPath is an absolute path rooted at or under
// root. Unlike SafeJoin (which treats the second arg as relative even when
// it's absolute and would produce nonsensical double-prefixes like
// `/app/media/app/media/foo.mp3`), SafeAbs works on absolute paths the
// scanner already stores in db.Folder/d.Attachment.
//
// Refuses:
//   - empty root or absPath
//   - absPath that is not absolute (use SafeJoin for relatives)
//   - absPath whose cleaned form is not equal to or under cleanedRoot
//
// Both inputs are cleaned; trailing separators on root are normalised
// away by filepath.Clean.
func SafeAbs(root, absPath string) (string, error) {
	if root == "" {
		return "", errors.New("utils: SafeAbs: root is empty")
	}
	if absPath == "" {
		return "", errors.New("utils: SafeAbs: path is empty")
	}
	if !filepath.IsAbs(absPath) {
		return "", fmt.Errorf("utils: SafeAbs: %q is not absolute", absPath)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("utils: SafeAbs: abs root %q: %w", root, err)
	}
	cleanedRoot := filepath.Clean(absRoot)
	candidate := filepath.Clean(absPath)
	if candidate != cleanedRoot && !strings.HasPrefix(candidate, cleanedRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("utils: SafeAbs: %q escapes root %q (resolved %q)", absPath, cleanedRoot, candidate)
	}
	return candidate, nil
}

// SafeRelPath validates a caller-supplied path that is meant to be RELATIVE
// to a root, and returns it cleaned.
//
// It refuses absolute paths outright — SafeJoin deliberately treats a
// leading "/" as a separator and folds it into the root, which is the right
// default for internal callers but the wrong one for a user-supplied
// "relative under the library" field, where an absolute value means the
// caller is confused or hostile rather than that they meant a subpath.
//
// It refuses any ".." PATH SEGMENT. The segment test is the whole point:
// the check this replaces was strings.Contains(cleaned, ".."), which
// rejected the perfectly ordinary directory name "Album..Deluxe" and
// "1997..2000 Remaster" while every real traversal — "../x", "a/../../x" —
// is segment-shaped and caught either way (REVIEW.md P3-7).
//
// This validates the SHAPE of the path. Containment is proved separately by
// joining through SafeJoin at the point of use, so the check at the request
// boundary and the check where the file is written cannot drift apart.
func SafeRelPath(p string) (string, error) {
	if p == "" {
		return "", errors.New("utils: SafeRelPath: path is empty")
	}
	if filepath.IsAbs(p) {
		return "", fmt.Errorf("utils: SafeRelPath: %q is absolute, want a relative path", p)
	}
	cleaned := filepath.Clean(p)
	for _, seg := range strings.Split(cleaned, string(filepath.Separator)) {
		if seg == ".." {
			return "", fmt.Errorf("utils: SafeRelPath: %q escapes upward", p)
		}
	}
	return cleaned, nil
}

// UnderRoot reports whether p names something inside root.
//
// It exists because "does this row belong to the library?" kept getting
// answered two different ways. The answer decides whether a row is a
// duplicate-detection candidate, whether it should be fingerprinted, and
// whether it may be deleted as stale — and the wrong answer is always
// silent, because the row is simply absent from the query's point of view.
//
// A path can arrive absolute (music_folder.path stores it that way) or
// root-relative (older rows, and callers that joined before storing), so
// both are accepted. Joining an already-absolute path is the bug this
// avoids: it would produce <root>/tmp/audio_cache/... and then fail every
// containment test against a table full of correctly-stored rows.
//
// An empty root means "no root configured", which is not the same answer as
// "not inside the root". Callers use this to decide whether a feature has
// any scope at all, and refusing every row there would silently disable it
// rather than narrowing it — so an empty root admits everything, and the
// disk-walk fallbacks those callers pair it with have the same limitation.
func UnderRoot(root, p string) bool {
	if p == "" {
		return false
	}
	abs := filepath.Clean(p)
	if !filepath.IsAbs(abs) {
		if root == "" {
			return true
		}
		abs = filepath.Clean(filepath.Join(root, abs))
	}
	if root == "" {
		return true
	}
	rel, err := filepath.Rel(filepath.Clean(root), abs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
