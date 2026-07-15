// Package tasks — yt-dlp 参数净化 (P1.5 issue F)。
//
// 攻击面：YouTubeDownloadHandler 把 ExtraJSON 字段直接拼到
// exec.CommandContext 的 arg slice 里。若前端恶意传入
// {"format":"--exec=rm -rf /"}，yt-dlp 会执行任意命令。
//
// 修复：handler 构造 ExtraJSON 之前先校验；worker 解析完 ExtraJSON
// 之后再校验一次 (defence-in-depth，让重放 task 时也安全)。
//
// 规则：
//   - Format    : 正则 ^[a-zA-Z0-9_./+<>:=]{1,64}$，且不能以 "-" 开头
//   - OutputFormat : 限定枚举 {mp3,m4a,ogg,vorbis,wav,空}
//   - Quality   : ^[0-9]{1,4}$ (1-4 位数字)
//
// 任何违规返回 error，handler 反馈 4xx，worker 任务标记失败。
package tasks

import (
	"fmt"
	"regexp"
	"strings"
)

// ytFormatRe allows character bytes that appear in legitimate yt-dlp
// format selectors such as "bestaudio[height<=480]" or "best[ext=mp4]".
// Shell/argv-control bytes — '-', ' ', '\t', '\n', '$', '`', ';' — are
// intentionally excluded; the leading-"-" check below still applies as
// belt-and-braces.
var (
	ytFormatRe  = regexp.MustCompile(`^[a-zA-Z0-9_./+<>=,\[\]!]{1,128}$`)
	ytQualityRe = regexp.MustCompile(`^[0-9]{1,4}$`)
	allowedOut  = map[string]bool{
		"":       true,
		"mp3":    true,
		"m4a":    true,
		"ogg":    true,
		"vorbis": true,
		"wav":    true,
	}
)

// SanitizeYTDLPFormat 检查并返回安全的 format 字符串；空时返回默认。
func SanitizeYTDLPFormat(s string) (string, error) {
	if s == "" {
		return "bestaudio/best", nil
	}
	if strings.HasPrefix(s, "-") {
		return "", fmt.Errorf("yt-dlp format cannot start with '-': %q", s)
	}
	if !ytFormatRe.MatchString(s) {
		return "", fmt.Errorf("yt-dlp format rejected: %q", s)
	}
	return s, nil
}

// SanitizeYTDLPOutputFormat 检查 output_format 是否在白名单枚举中。
func SanitizeYTDLPOutputFormat(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if !allowedOut[s] {
		return "", fmt.Errorf("yt-dlp output_format rejected: %q (allowed: mp3/m4a/ogg/vorbis/wav)", s)
	}
	return s, nil
}

// SanitizeYTDLPQuality 检查 quality 是否为 1-4 位数字。
func SanitizeYTDLPQuality(s string) (string, error) {
	if s == "" {
		return "192", nil
	}
	if !ytQualityRe.MatchString(s) {
		return "", fmt.Errorf("yt-dlp quality rejected: %q (must be 1-4 digits)", s)
	}
	return s, nil
}
