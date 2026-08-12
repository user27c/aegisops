package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
)

// Role 是用户角色。
type Role string

const (
	// RoleViewer 只读。
	RoleViewer Role = "viewer"
	// RoleApprover 可审批（M4 起使用）。
	RoleApprover Role = "approver"
)

// Principal 是认证后的主体。
type Principal struct {
	Subject     string
	DisplayName string
	Roles       []Role
}

// HasRole 判断是否拥有指定角色。
func (p Principal) HasRole(role Role) bool {
	for _, r := range p.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// Authenticator 认证 HTTP 请求。
type Authenticator interface {
	// Authenticate 返回主体；失败返回错误。
	Authenticate(r *http.Request) (Principal, error)
	// Middleware 是 chi 中间件。
	Middleware(next http.Handler) http.Handler
}

// StaticTokenAuthenticator 从静态 Token 文件认证。
//
// 文件格式每行：`token:role[,role...]`。内存只保存 SHA256，比较使用 constant-time。
type StaticTokenAuthenticator struct {
	mu      sync.RWMutex
	hashed  map[[32]byte]Principal
	enabled bool
}

// NewStaticTokenAuthenticator 从文件加载 Token 映射。
func NewStaticTokenAuthenticator(path string) (*StaticTokenAuthenticator, error) {
	a := &StaticTokenAuthenticator{hashed: map[[32]byte]Principal{}}
	if path == "" {
		return nil, fmt.Errorf("STATIC_TOKENS_FILE 不能为空")
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- 路径来自 *_FILE 配置
	if err != nil {
		return nil, fmt.Errorf("读取 Token 文件: %w", err)
	}
	for lineNo, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("token 文件第 %d 行格式错误（应为 token:role[,role...]）", lineNo+1)
		}
		token, rolesRaw := parts[0], parts[1]
		if token == "" || rolesRaw == "" {
			return nil, fmt.Errorf("token 文件第 %d 行格式错误", lineNo+1)
		}
		principal := Principal{Subject: tokenSubject(token)}
		for _, role := range strings.Split(rolesRaw, ",") {
			role = strings.TrimSpace(role)
			switch Role(role) {
			case RoleViewer, RoleApprover:
				principal.Roles = append(principal.Roles, Role(role))
			default:
				return nil, fmt.Errorf("token 文件第 %d 行含未知角色 %q", lineNo+1, role)
			}
		}
		if len(principal.Roles) == 0 {
			return nil, fmt.Errorf("token 文件第 %d 行没有有效角色", lineNo+1)
		}
		a.hashed[sha256.Sum256([]byte(token))] = principal
	}
	if len(a.hashed) == 0 {
		return nil, fmt.Errorf("token 文件为空")
	}
	a.enabled = true
	return a, nil
}

// Authenticate 校验 Bearer Token 并返回主体。
func (a *StaticTokenAuthenticator) Authenticate(r *http.Request) (Principal, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.enabled {
		return Principal{}, fmt.Errorf("认证未启用")
	}
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return Principal{}, fmt.Errorf("缺少 Bearer Token")
	}
	token := strings.TrimPrefix(header, prefix)
	sum := sha256.Sum256([]byte(token))
	for hash, principal := range a.hashed {
		if subtle.ConstantTimeCompare(sum[:], hash[:]) == 1 {
			return principal, nil
		}
	}
	return Principal{}, fmt.Errorf("token 无效")
}

// tokenSubject 派生稳定的非敏感主体标识：审计 actor 不得回写原始 token，
// 否则会经时间线接口与截图泄漏凭证。
func tokenSubject(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "token-" + hex.EncodeToString(sum[:8])
}

// Middleware 返回认证中间件。
func (a *StaticTokenAuthenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, err := a.Authenticate(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "未授权")
			return
		}
		ctx := withPrincipal(r.Context(), principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
