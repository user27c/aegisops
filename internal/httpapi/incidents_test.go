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

func TestListIncidents_ScanAcrossFilteredPage(t *testing.T) {
	// 第一页(按 List 顺序)全被 phase 过滤掉,第二页有匹配 —— 服务端必须跨页扫描。
	h := newTestServer(t,
		sampleIncident("oom-1", "fault-lab", "Resolved", "critical"),
		sampleIncident("oom-2", "fault-lab", "Resolved", "critical"),
		sampleIncident("oom-3", "fault-lab", "Detected", "critical"),
	)

	rec := doRequest(t, h, http.MethodGet, "/api/v1/incidents?namespace=fault-lab&phase=Detected&limit=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200: %d %s", rec.Code, rec.Body.String())
	}
	var page IncidentPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("应返回 1 条匹配(跨过被过滤项): %d", len(page.Items))
	}
	if page.Items[0].Metadata.Name != "oom-3" {
		t.Errorf("应命中 oom-3,得到 %s", page.Items[0].Metadata.Name)
	}
}

func TestListIncidents_NoMatchReturnsEmpty(t *testing.T) {
	h := newTestServer(t,
		sampleIncident("oom-1", "fault-lab", "Resolved", "critical"),
	)
	rec := doRequest(t, h, http.MethodGet, "/api/v1/incidents?phase=Escalated")
	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200: %d", rec.Code)
	}
	var page IncidentPage
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if page.Items == nil || len(page.Items) != 0 {
		t.Errorf("空结果应为 [] 而非 null: %v", page.Items)
	}
	if page.ContinueToken != "" {
		t.Errorf("无更多数据不应有游标: %s", page.ContinueToken)
	}
}

func TestListIncidents_CursorFilterChanged(t *testing.T) {
	h := newTestServer(t,
		sampleIncident("oom-1", "fault-lab", "Detected", "critical"),
		sampleIncident("oom-2", "fault-lab", "Resolved", "warning"),
	)

	// 先拿一个 phase=Detected 的游标,再用 phase=Resolved 请求 → 400。
	cur := encodeCursor(listCursor{
		Version:    cursorVersion,
		Namespace:  "fault-lab",
		Phase:      "Detected",
		Continue:   "opaque-k8s-token",
		FilterHash: filterHashOf("fault-lab", "Detected", ""),
	})
	rec := doRequest(t, h, http.MethodGet, "/api/v1/incidents?namespace=fault-lab&phase=Resolved&continue="+cur)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("过滤条件变化应 400: %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["code"] != "FILTER_CHANGED" {
		t.Errorf("错误码应为 FILTER_CHANGED: %v", body)
	}
}

func TestListIncidents_InvalidCursor(t *testing.T) {
	h := newTestServer(t)
	rec := doRequest(t, h, http.MethodGet, "/api/v1/incidents?continue=not-base64")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法游标应 400: %d", rec.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["code"] != "INVALID_CURSOR" {
		t.Errorf("错误码应为 INVALID_CURSOR: %v", body)
	}

	// 合法 base64 但结构错误。
	rec = doRequest(t, h, http.MethodGet, "/api/v1/incidents?continue="+encodeCursor(listCursor{Version: 999}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("版本不符应 400: %d", rec.Code)
	}
}

func TestListIncidents_OverflowPageNoDataLoss(t *testing.T) {
	// 页内过滤后匹配项超过剩余容量时,游标必须重扫该页并跳过已消费匹配,
	// 不能直接跳到整页之后(否则页内剩余匹配项丢失)。
	h := newTestServer(t,
		sampleIncident("det-1", "fault-lab", "Detected", "critical"),
		sampleIncident("det-2", "fault-lab", "Detected", "critical"),
		sampleIncident("det-3", "fault-lab", "Detected", "critical"),
		sampleIncident("exec-1", "fault-lab", "Executing", "critical"),
		sampleIncident("exec-2", "fault-lab", "Executing", "critical"),
		sampleIncident("exec-3", "fault-lab", "Executing", "critical"),
		sampleIncident("exec-4", "fault-lab", "Executing", "critical"),
		sampleIncident("exec-5", "fault-lab", "Executing", "critical"),
	)

	var got []string
	cont := ""
	pages := 0
	for {
		u := "/api/v1/incidents?namespace=fault-lab&phase=Executing&limit=2"
		if cont != "" {
			u += "&continue=" + cont
		}
		rec := doRequest(t, h, http.MethodGet, u)
		if rec.Code != http.StatusOK {
			t.Fatalf("页 %d 期望 200: %d %s", pages+1, rec.Code, rec.Body.String())
		}
		var page IncidentPage
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
			t.Fatalf("解析失败: %v", err)
		}
		pages++
		for _, it := range page.Items {
			got = append(got, it.Metadata.Name)
		}
		if page.ContinueToken == "" {
			break
		}
		cont = page.ContinueToken
		if pages > 10 {
			t.Fatal("分页未收敛")
		}
	}

	if len(got) != 5 {
		t.Fatalf("5 个匹配项应全部返回(无丢失),得到 %d: %v", len(got), got)
	}
	seen := map[string]bool{}
	for _, n := range got {
		if seen[n] {
			t.Fatalf("重复项: %s", n)
		}
		seen[n] = true
	}
	for _, want := range []string{"exec-1", "exec-2", "exec-3", "exec-4", "exec-5"} {
		if !seen[want] {
			t.Errorf("缺失匹配项: %s", want)
		}
	}
}
