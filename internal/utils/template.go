// Package utils 通用工具：模板渲染、其他小工具。
package utils

import (
	"regexp"
	"strings"
)

// 匹配 ${key}，key 不能含 ${ } #（与 Python TEMPLATE_PATTERN = re.compile(r"\${[^${}#]+}") 等价）
var templatePattern = regexp.MustCompile(`\$\{([^{}#$]+)\}`)

// RenderTemplate 把 ${key} 占位符替换为 value_map 中对应值。
// 找不到的 key 降级为原占位符 (与 Python ConstantTemplate 返回原 template)。
func RenderTemplate(tmpl string, vars map[string]string) string {
	if !strings.Contains(tmpl, "${") {
		return tmpl
	}
	return templatePattern.ReplaceAllStringFunc(tmpl, func(match string) string {
		// 去外壳 ${}
		key := strings.TrimSpace(match[2 : len(match)-1])
		if v, ok := vars[key]; ok {
			return v
		}
		return match
	})
}

// SanitizePath 把字符串清理为可用作文件名的形式（保留中英文/数字/常用分隔符，
// 其余替换为下划线）。与 Python sanitize_filename 等价的 Go 版本。
func SanitizePath(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	bad := map[rune]bool{
		'/': true, '\\': true, ':': true, '*': true,
		'?': true, '"': true, '<': true, '>': true,
		'|': true, '\n': true, '\r': true, '\t': true,
	}
	runes := []rune(s)
	for i, r := range runes {
		if bad[r] {
			runes[i] = '_'
		}
	}
	return string(runes)
}
