package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// testAuth 是固定放行的认证器。
type testAuth struct{}

func (t *testAuth) Authenticate(_ *http.Request) (Principal, error) {
	return Principal{Subject: "test", Roles: []Role{RoleViewer, RoleApprover}}, nil
}
func (t *testAuth) Middleware(next http.Handler) http.Handler { return next }

func newTestServer(t *testing.T, objs ...client.Object) http.Handler {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("scheme: %v", err)
	}
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("ops scheme: %v", err)
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&opsv1alpha1.AIOpsIncident{}).
		WithObjects(objs...).
		Build()
	h, err := NewServer(ServerDeps{
		K8s:  c,
		Auth: &testAuth{},
		Now:  func() time.Time { return time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return h
}

func sampleIncident(name, ns, phase, severity string) *opsv1alpha1.AIOpsIncident {
	return &opsv1alpha1.AIOpsIncident{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: opsv1alpha1.AIOpsIncidentSpec{
			Fingerprint:    "sha256:" + strings.Repeat("a", 64),
			Cluster:        "local-k3s",
			AlertName:      "ContainerOOMKilled",
			Severity:       severity,
			SourceStatus:   "firing",
			TargetRef:      opsv1alpha1.TargetReference{APIVersion: "apps/v1", Kind: "Deployment", Namespace: ns, Name: "checkout"},
			StartedAt:      metav1.NewTime(time.Now()),
			LastReceivedAt: metav1.NewTime(time.Now()),
		},
		Status: opsv1alpha1.AIOpsIncidentStatus{Phase: opsv1alpha1.IncidentPhase(phase)},
	}
}

func doRequest(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestListIncidents(t *testing.T) {
	h := newTestServer(t,
		sampleIncident("oom-1", "fault-lab", "Detected", "critical"),
		sampleIncident("oom-2", "fault-lab", "Resolved", "warning"),
		sampleIncident("oom-3", "other-ns", "Detected", "critical"),
	)

	rec := doRequest(t, h, http.MethodGet, "/api/v1/incidents")
	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200，得到 %d: %s", rec.Code, rec.Body.String())
	}
	var page IncidentPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(page.Items) != 3 {
		t.Errorf("应返回 3 条，得到 %d", len(page.Items))
	}
}

func TestListIncidents_Filters(t *testing.T) {
	h := newTestServer(t,
		sampleIncident("oom-1", "fault-lab", "Detected", "critical"),
		sampleIncident("oom-2", "fault-lab", "Resolved", "warning"),
	)

	// namespace 过滤。
	rec := doRequest(t, h, http.MethodGet, "/api/v1/incidents?namespace=fault-lab")
	var page IncidentPage
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if len(page.Items) != 2 {
		t.Errorf("namespace 过滤应 2 条: %d", len(page.Items))
	}

	// phase 过滤。
	rec = doRequest(t, h, http.MethodGet, "/api/v1/incidents?phase=Resolved")
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if len(page.Items) != 1 || page.Items[0].Metadata.Name != "oom-2" {
		t.Errorf("phase 过滤错误: %d", len(page.Items))
	}

	// severity 过滤。
	rec = doRequest(t, h, http.MethodGet, "/api/v1/incidents?severity=critical")
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if len(page.Items) != 1 || page.Items[0].Metadata.Name != "oom-1" {
		t.Errorf("severity 过滤错误: %d", len(page.Items))
	}
}

func TestListIncidents_InvalidLimit(t *testing.T) {
	h := newTestServer(t)
	rec := doRequest(t, h, http.MethodGet, "/api/v1/incidents?limit=99999")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("非法 limit 应 400: %d", rec.Code)
	}
	rec = doRequest(t, h, http.MethodGet, "/api/v1/incidents?limit=abc")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("非数字 limit 应 400: %d", rec.Code)
	}
}

func TestGetIncident(t *testing.T) {
	h := newTestServer(t, sampleIncident("oom-1", "fault-lab", "Detected", "critical"))

	rec := doRequest(t, h, http.MethodGet, "/api/v1/incidents/fault-lab/oom-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200: %d", rec.Code)
	}
	var dto IncidentDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if dto.Metadata.Name != "oom-1" || dto.Status.Phase != "Detected" {
		t.Errorf("DTO 内容错误: %+v", dto)
	}
}

func TestGetIncident_NotFound(t *testing.T) {
	h := newTestServer(t)
	rec := doRequest(t, h, http.MethodGet, "/api/v1/incidents/fault-lab/missing")
	if rec.Code != http.StatusNotFound {
		t.Errorf("不存在应 404: %d", rec.Code)
	}
}

func TestListPolicies(t *testing.T) {
	policy := &opsv1alpha1.RemediationPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "fault-lab-default", Namespace: "fault-lab"},
		Spec: opsv1alpha1.RemediationPolicySpec{
			TargetSelector: opsv1alpha1.TargetSelector{Kinds: []string{"Deployment"}},
			Actions:        map[opsv1alpha1.ActionType]opsv1alpha1.ActionPolicy{},
		},
	}
	h := newTestServer(t, policy)
	rec := doRequest(t, h, http.MethodGet, "/api/v1/policies")
	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "fault-lab-default") {
		t.Errorf("响应缺少策略: %s", rec.Body.String())
	}
}

func TestHealthz(t *testing.T) {
	h := newTestServer(t)
	rec := doRequest(t, h, http.MethodGet, "/healthz")
	if rec.Code != http.StatusOK {
		t.Errorf("healthz 应 200: %d", rec.Code)
	}
}

func TestSPAFallback(t *testing.T) {
	// 无 StaticDir 时 API 之外的路由应 404（不提供 SPA）。
	h := newTestServer(t)
	rec := doRequest(t, h, http.MethodGet, "/some/spa/route")
	if rec.Code != http.StatusNotFound {
		t.Errorf("无 StaticDir 时非 API 路由应 404: %d", rec.Code)
	}

	// 有 StaticDir 时提供 SPA（index.html 兜底）。
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>console</html>"), 0o600); err != nil {
		t.Fatalf("写 index: %v", err)
	}
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = opsv1alpha1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	h2, err := NewServer(ServerDeps{
		K8s:       c,
		Auth:      &testAuth{},
		StaticDir: dir,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	rec = doRequest(t, h2, http.MethodGet, "/")
	if rec.Code != http.StatusOK {
		t.Errorf("有 StaticDir 时根路径应 200: %d", rec.Code)
	}
}
