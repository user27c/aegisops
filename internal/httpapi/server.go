// Package httpapi 提供 Web 事故控制台后端。
//
// 边界：只能读取 Incident/Policy 与创建 Approval（M4）；不得创建 Executor，
// 也不得直接修改工作负载。
package httpapi

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/user27c/aegisops/internal/observability"
)

// ServerDeps 是 HTTP 服务依赖。
type ServerDeps struct {
	// K8s 是只读 Kubernetes 客户端（Incident/Policy 查询）。
	K8s client.Client
	// Auth 是认证器。
	Auth Authenticator
	// Diagnosis 是从诊断服务读取只读详情的客户端；nil 时 /timeline /evidence 降级。
	Diagnosis DiagnosisReader
	// StaticDir 是 Web 静态文件目录；为空时不提供 SPA。
	StaticDir string
	// AllowedOrigins 是 CORS 白名单。
	AllowedOrigins []string
	// AllowedNamespaces 是 API 可读的命名空间，必须匹配其 namespaced RBAC。
	// 空值被列表端点 fail closed，避免退化为集群级 List。
	AllowedNamespaces []string
	// Now 是时钟函数（测试注入）。
	Now func() time.Time
}

// NewServer 构造完整的 HTTP 处理器（API + 静态文件 + 中间件）。
func NewServer(deps ServerDeps) (http.Handler, error) {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.K8s == nil {
		return nil, fmt.Errorf("K8s 客户端不能为空")
	}

	r := chi.NewRouter()
	// 公共端点。
	r.Get("/healthz", healthz)
	r.Get("/readyz", readyz)
	r.Get("/metrics", promhttp.Handler().ServeHTTP)

	// API 分组：全部需要认证。
	r.Route("/api/v1", func(api chi.Router) {
		api.Use(deps.Auth.Middleware)
		RegisterRoutes(api, deps)
	})

	// SPA fallback：只对非 API 的 GET 生效；未知路径回退到 index.html。
	if deps.StaticDir != "" {
		fileServer := http.FileServer(http.Dir(deps.StaticDir))
		r.Get("/*", func(w http.ResponseWriter, req *http.Request) {
			// 路径穿越防护：确保解析后的路径仍在静态目录内。
			clean := filepath.Clean(filepath.Join(deps.StaticDir, req.URL.Path))
			if !strings.HasPrefix(clean, filepath.Clean(deps.StaticDir)) {
				http.NotFound(w, req)
				return
			}
			if strings.HasPrefix(req.URL.Path, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
				w.Header().Set("Pragma", "no-cache")
				w.Header().Set("Expires", "0")
			}
			if info, err := os.Stat(clean); err == nil && !info.IsDir() {
				fileServer.ServeHTTP(w, req)
				return
			}
			// SPA fallback 到 index.html。
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
			http.ServeFile(w, req, filepath.Join(deps.StaticDir, "index.html"))
		})
	}

	// 全局中间件（从外到内）：Request ID → OTel → Recover → Security Headers → CORS。
	handler := WithRecover(r)
	handler = WithSecurityHeaders(handler)
	handler = WithCORS(handler, deps.AllowedOrigins)
	handler = observability.OTelHTTPMiddleware("incident-api")(handler)
	handler = WithRequestID(handler)
	return handler, nil
}

// RegisterRoutes 注册 /api/v1 下的全部路由。
func RegisterRoutes(r chi.Router, deps ServerDeps) {
	h := &Handlers{
		k8s:               deps.K8s,
		now:               deps.Now,
		diagnosis:         deps.Diagnosis,
		allowedNamespaces: uniqueNamespaces(deps.AllowedNamespaces),
	}
	r.Get("/incidents", h.ListIncidents)
	r.Get("/incidents/{namespace}/{name}", h.GetIncident)
	r.Get("/incidents/{namespace}/{name}/timeline", h.GetIncidentTimeline)
	r.Get("/incidents/{namespace}/{name}/evidence", h.GetIncidentEvidence)
	r.Post("/incidents/{namespace}/{name}/approval", h.ApproveIncident)
	r.Get("/policies", h.ListPolicies)
}

func uniqueNamespaces(namespaces []string) []string {
	seen := make(map[string]struct{}, len(namespaces))
	result := make([]string, 0, len(namespaces))
	for _, namespace := range namespaces {
		namespace = strings.TrimSpace(namespace)
		if namespace == "" {
			continue
		}
		if _, exists := seen[namespace]; exists {
			continue
		}
		seen[namespace] = struct{}{}
		result = append(result, namespace)
	}
	return result
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func readyz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
