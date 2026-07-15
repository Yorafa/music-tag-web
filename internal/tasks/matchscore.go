// Package tasks match_score + match_artist 简化移植。
// 与 applications/task/utils.py 对齐；P1 暂不引入 zhconv 繁简转换。
package tasks

import (
	"regexp"
	"strings"
)

var (
	reBracket     = regexp.MustCompile(`[\(\[][^)\]]*[\)\]]`)
	reFeat        = regexp.MustCompile(`(?i)\b(feat|ft)\.?\s*\S*`)
	reNonWordChar = regexp.MustCompile(`[^\w]+`)
)

// cleanForMatch 与 Python `_clean_for_match`：lower + 去括号 + 去 feat + 去非 \w。
//
// 注意 Go 的 \p{L}\p{N} 比 \w 更宽，覆盖中文等非 ASCII 字母；Python 用 [^\w]（Unicode
// 模式）保留中文字符；这里改成 re 非 word 序列拆 token，等价语义。
func cleanForMatch(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = reBracket.ReplaceAllString(s, "")
	s = reFeat.ReplaceAllString(s, "")
	// \W 用 Unicode property
	s = regexp.MustCompile(`[^\p{L}\p{N}]+`).ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// matchScore 与 Python match_score 一致：相同=2；包含=1；token 重叠>=一半=1；不沾=0。
func matchScore(myValue, uValue string) int {
	if myValue == "" || uValue == "" {
		return 0
	}
	myClean := cleanForMatch(myValue)
	uClean := cleanForMatch(uValue)
	if myClean == "" || uClean == "" {
		return 0
	}
	if myClean == uClean {
		return 2
	}
	if strings.Contains(uClean, myClean) || strings.Contains(myClean, uClean) {
		return 1
	}
	myTokens := strings.Fields(myClean)
	uTokens := strings.Fields(uClean)
	if len(myTokens) == 0 || len(uTokens) == 0 {
		return 0
	}
	setU := map[string]bool{}
	for _, t := range uTokens {
		setU[t] = true
	}
	hits := 0
	for _, t := range myTokens {
		if setU[t] {
			hits++
		}
	}
	if hits == len(myTokens) {
		return 2
	}
	if hits >= len(myTokens)/2 {
		return 1
	}
	return 0
}

// matchArtist 对齐 Python match_artist。u 含逗号拆两个分别打分。
func matchArtist(myValue, uValue string) int {
	if strings.Contains(uValue, ",") {
		parts := strings.SplitN(uValue, ",", 2)
		return matchScore(myValue, strings.ReplaceAll(parts[0], " ", "")) +
			matchScore(myValue, strings.ReplaceAll(parts[1], " ", ""))
	}
	return matchScore(myValue, uValue)
}
