package evidence

import (
	"regexp"
	"strings"
	"testing"
)

func TestRedactor_Secrets(t *testing.T) {
	r := NewRegexRedactor(nil)
	cases := []struct {
		name string
		in   string
	}{
		{"bearer", "Authorization: Bearer abcdefghijklmnopqrstuvwxyz123456"},
		{"jwt", "token=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"},
		{"aws", "key=AKIAIOSFODNN7EXAMPLE"},
		{"password", `"password": "hunter2hunter2hunter2"`},
		{"api_key", `api_key=hunter2hunter2hunter2`},
		{"pem", "-----BEGIN RSA PRIVATE KEY-----\nMIIEpQIBAAKCAQEA\n-----END RSA PRIVATE KEY-----"},
		{"ip-address", "Failed to pull image: dial tcp 10.96.0.12:443; public 8.8.8.8"},
	}
	for _, tc := range cases {
		out, redactions := r.RedactString(tc.in)
		if len(redactions) == 0 {
			t.Errorf("%s: 未脱敏: %q", tc.name, tc.in)
		}
		if strings.Contains(out, "hunter2") || strings.Contains(out, "AKIA") || strings.Contains(out, "10.96.0.12") || strings.Contains(out, "8.8.8.8") ||
			strings.Contains(out, "eyJ") || strings.Contains(out, "PRIVATE KEY-----") {
			t.Errorf("%s: 泄露: %q", tc.name, out)
		}
	}
}

func TestRedactor_CustomPattern(t *testing.T) {
	r := NewRegexRedactor([]*regexp.Regexp{
		regexp.MustCompile(`(?i)internal-secret-\d+`),
	})
	out, redactions := r.RedactString("found internal-secret-42 here")
	if strings.Contains(out, "internal-secret-42") {
		t.Errorf("自定义模式未生效: %q", out)
	}
	if len(redactions) != 1 || redactions[0].Pattern != "custom" {
		t.Errorf("自定义脱敏事件错误: %+v", redactions)
	}
}

func TestRedactor_FalsePositiveBaseline(t *testing.T) {
	// 普通业务文本不应误报。
	r := NewRegexRedactor(nil)
	normal := []string{
		"container restarted 3 times, exit code 137",
		"GET /api/v1/incidents 200 OK",
		"namespace=fault-lab deployment=checkout-api",
	}
	for _, s := range normal {
		out, redactions := r.RedactString(s)
		if len(redactions) > 0 {
			t.Errorf("误报 %q → %q (%+v)", s, out, redactions)
		}
	}
}

func TestTruncateUTF8(t *testing.T) {
	s := strings.Repeat("密", 100)
	cut, truncated := TruncateUTF8(s, 10)
	if !truncated {
		t.Error("应标记截断")
	}
	if len(cut) > 10 {
		t.Errorf("截断超限: %d", len(cut))
	}
	if !strings.HasSuffix(cut, "密") && len(cut) > 0 {
		t.Errorf("UTF-8 截断破坏字符: %q", cut)
	}

	short, truncated := TruncateUTF8("abc", 10)
	if truncated || short != "abc" {
		t.Error("短文本不应截断")
	}
}
