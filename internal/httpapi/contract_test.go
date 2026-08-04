package httpapi

import (
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// contractRoutes 是 docs/api-contracts.md 声明的 incident-api 路由（防漂移锚点）。
var contractRoutes = []string{
	"/incidents",
	"/incidents/{namespace}/{name}",
	"/incidents/{namespace}/{name}/timeline",
	"/incidents/{namespace}/{name}/evidence",
	"/incidents/{namespace}/{name}/approval",
	"/policies",
}

// TestRegisterRoutes_MatchContract 断言实际注册路由覆盖契约文档端点。
// 任何一端新增/删除端点时此测试迫使契约文档同步更新。
func TestRegisterRoutes_MatchContract(t *testing.T) {
	scheme := newTestScheme()
	c := newFakeClient(t, scheme)
	r := chi.NewRouter()
	RegisterRoutes(r, ServerDeps{
		K8s:       c,
		Now:       time.Now,
		Diagnosis: &fakeDiagnosis{},
	})

	registered := make(map[string]bool)
	for _, route := range r.Routes() {
		registered[route.Pattern] = true
	}
	for _, want := range contractRoutes {
		if !registered[want] {
			t.Errorf("契约端点未注册: %s", want)
		}
	}
	// 反向：已注册的 GET 路由应都在契约中（防遗漏）。
	// POST approval 是唯一写路由。
	for pattern := range registered {
		if pattern == "/incidents/{namespace}/{name}/approval" {
			continue
		}
		found := false
		for _, want := range contractRoutes {
			if want == pattern {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("已注册路由不在契约中（需同步 docs/api-contracts.md）: %s", pattern)
		}
	}
}

// TestContractRoutes_ServeNotFallback 契约端点必须真实路由（非 SPA fallback 的 200 HTML）。
func TestContractRoutes_ServeJSON(t *testing.T) {
	scheme := newTestScheme()
	c := newFakeClient(t, scheme, incidentWithTimeline(true))
	h, err := NewServer(ServerDeps{
		K8s:       c,
		Auth:      &testAuth{},
		Now:       time.Now,
		Diagnosis: &fakeDiagnosis{tlErr: ErrDiagnosisUnavailable, evErr: ErrDiagnosisUnavailable},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	cases := []struct {
		path string
		want int
	}{
		{"/api/v1/incidents", http.StatusOK},
		{"/api/v1/incidents/fault-lab/oom-1", http.StatusOK},
		{"/api/v1/incidents/fault-lab/oom-1/timeline", http.StatusOK},
		{"/api/v1/incidents/fault-lab/oom-1/evidence", http.StatusOK},
		{"/api/v1/policies", http.StatusOK},
	}
	for _, tc := range cases {
		rec := doRequest(t, h, http.MethodGet, tc.path)
		if rec.Code != tc.want {
			t.Errorf("%s: 期望 %d,得到 %d", tc.path, tc.want, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
			t.Errorf("%s: 期望 JSON 响应,得到 %s", tc.path, ct)
		}
	}
}
