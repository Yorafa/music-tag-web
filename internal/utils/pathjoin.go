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
//   - p 为空时返回 ErrUnsafeEmpty，避免返回 root 本身造成误用。
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
	if p == "" {
		return "", errors.New("utils: SafeJoin: path is empty")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("utils: SafeJoin: abs root %q: %w", root, err)
	}
	cleanedRoot := filepath.Clean(absRoot)
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
