package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestStaticTokenAuthenticator_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens")
	if err := os.WriteFile(path, []byte("viewer-token:viewer\napprover-token:approver,viewer\n"), 0o600); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
	a, err := NewStaticTokenAuthenticator(path)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil)
	req.Header.Set("Authorization", "Bearer viewer-token")
	p, err := a.Authenticate(req)
	if err != nil {
		t.Fatalf("认证失败: %v", err)
	}
	if !p.HasRole(RoleViewer) || p.HasRole(RoleApprover) {
		t.Errorf("角色错误: %+v", p.Roles)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("Authorization", "Bearer approver-token")
	p2, err := a.Authenticate(req2)
	if err != nil {
		t.Fatalf("approver 认证失败: %v", err)
	}
	if !p2.HasRole(RoleApprover) {
		t.Errorf("approver 角色缺失: %+v", p2.Roles)
	}
}

func TestStaticTokenAuthenticator_Invalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens")
	if err := os.WriteFile(path, []byte("viewer-token:viewer\n"), 0o600); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
	a, err := NewStaticTokenAuthenticator(path)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	cases := []struct {
		name  string
		authz string
	}{
		{"错误 token", "Bearer wrong-token"},
		{"无 Authorization", ""},
		{"非 Bearer 格式", "Basic abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.authz != "" {
				req.Header.Set("Authorization", tc.authz)
			}
			if _, err := a.Authenticate(req); err == nil {
				t.Error("应认证失败")
			}
		})
	}
}

func TestStaticTokenAuthenticator_BadFile(t *testing.T) {
	cases := []string{
		"no-colon-line\n",
		"token:unknownrole\n",
		":viewer\n",
		"# 只有注释\n",
	}
	for _, content := range cases {
		dir := t.TempDir()
		path := filepath.Join(dir, "tokens")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("写文件失败: %v", err)
		}
		if _, err := NewStaticTokenAuthenticator(path); err == nil {
			t.Errorf("内容 %q 应报错", content)
		}
	}
}

func TestTokenSubject_NeverLeaksRawToken(t *testing.T) {
	// 审计 actor / Principal.Subject 绝不能回写原始 token，否则会经时间线
	// 接口与截图泄漏凭证。tokenSubject 必须派生稳定的非敏感标识。
	if got := tokenSubject("sometoken"); got != "token-"+tokenHexSuffix("sometoken") {
		t.Fatalf("tokenSubject 派生不一致: %q", got)
	}
	if got := tokenSubject("sometoken"); len(got) != len("token-")+16 {
		t.Fatalf("tokenSubject 长度错误: %q", got)
	}
}

func TestStaticTokenAuthenticator_SubjectIsDerivedNotRawToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens")
	if err := os.WriteFile(path, []byte("viewer-token:viewer\n"), 0o600); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
	a, err := NewStaticTokenAuthenticator(path)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer viewer-token")
	p, err := a.Authenticate(req)
	if err != nil {
		t.Fatalf("认证失败: %v", err)
	}
	if p.Subject == "viewer-token" {
		t.Fatalf("Subject 泄露原始 token: %q", p.Subject)
	}
	if p.Subject != tokenSubject("viewer-token") {
		t.Fatalf("Subject 应为派生标识: %q", p.Subject)
	}
	if !subjectPattern.MatchString(p.Subject) {
		t.Fatalf("Subject 格式非法: %q", p.Subject)
	}
}

var subjectPattern = regexp.MustCompile(`^token-[0-9a-f]{16}$`)

func tokenHexSuffix(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:8])
}

func TestStaticTokenAuthenticator_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens")
	if err := os.WriteFile(path, []byte("\n\n"), 0o600); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
	if _, err := NewStaticTokenAuthenticator(path); err == nil {
		t.Error("空文件应报错")
	}
}
