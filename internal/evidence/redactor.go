package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// 内置脱敏模式（蓝图 11.9）：Bearer Token、AK/SK、JWT、password/api_key、PEM。
var builtinPatterns = []struct {
	name    string
	pattern *regexp.Regexp
}{
	{
		name:    "bearer-token",
		pattern: regexp.MustCompile(`(?i)(bearer\s+)[a-z0-9._~+/=-]{16,}`),
	},
	{
		name:    "jwt",
		pattern: regexp.MustCompile(`eyJ[a-zA-Z0-9_-]{10,}\.[a-zA-Z0-9_-]{10,}\.[a-zA-Z0-9_-]{10,}`),
	},
	{
		name:    "aws-ak-sk",
		pattern: regexp.MustCompile(`(AKIA|ASIA)[A-Z0-9]{16}`),
	},
	{
		name:    "private-key-field",
		pattern: regexp.MustCompile(`(?i)("?(?:password|passwd|api[_-]?key|secret|token|access[_-]?key)"?\s*[:=]\s*["']?)[^"'\s,;]{6,}`),
	},
	{
		name:    "pem-block",
		pattern: regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
	},
}

// Redaction 是一次脱敏事件。
type Redaction struct {
	// Pattern 是命中的模式名。
	Pattern string `json:"pattern"`
	// Count 是替换次数。
	Count int `json:"count"`
}

// RegexRedactor 是正则脱敏器。
type RegexRedactor struct {
	patterns []struct {
		name    string
		pattern *regexp.Regexp
	}
}

// NewRegexRedactor 创建脱敏器，可追加自定义模式。
func NewRegexRedactor(extraPatterns []*regexp.Regexp) *RegexRedactor {
	r := &RegexRedactor{}
	r.patterns = append(r.patterns, builtinPatterns...)
	for _, p := range extraPatterns {
		r.patterns = append(r.patterns, struct {
			name    string
			pattern *regexp.Regexp
		}{name: "custom", pattern: p})
	}
	return r
}

// RedactString 脱敏文本并返回脱敏事件。
func (r *RegexRedactor) RedactString(s string) (string, []Redaction) {
	out := s
	redactions := []Redaction{}
	for _, p := range r.patterns {
		count := 0
		out = p.pattern.ReplaceAllStringFunc(out, func(string) string {
			count++
			return p.name + "-REDACTED"
		})
		if count > 0 {
			redactions = append(redactions, Redaction{Pattern: p.name, Count: count})
		}
	}
	return out, redactions
}

// TruncateUTF8 按字节截断字符串，返回是否发生截断。
// 保证不截断 UTF-8 序列（含 lead byte 被截断的情况）。
func TruncateUTF8(s string, maxBytes int) (string, bool) {
	if len(s) <= maxBytes {
		return s, false
	}
	cut := s[:maxBytes]
	for !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut, true
}

// hashString 计算字符串哈希（16 hex）。
func hashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// mapToString 把 map 稳定序列化为字符串。
func mapToString(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, m[k])
	}
	return b.String()
}
