package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
