package alertmanager

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

type staticValidator struct{ token string }

func (v *staticValidator) Validate(token string) bool { return token == v.token }

// testService 构造基于 fake client 的测试服务（真实去重逻辑）。
func testService(t *testing.T, resolver TargetResolver) (*Service, *countingMetrics) {
	t.Helper()
	clk := &fakeClock{}
	metrics := &countingMetrics{}
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("添加 scheme: %v", err)
	}
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("添加 ops scheme: %v", err)
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&opsv1alpha1.AIOpsIncident{}).
		Build()
	svc := NewService("cluster-a", NewKubernetesWriter(c, clk), resolver, clk, metrics)
	return svc, metrics
}

func newTestHandler(svc *Service, auth TokenValidator) http.Handler {
	return NewHandler(svc, auth, logr.Discard(), 1<<20)
}

func validBody() string {
	return `{"version":"4","groupKey":"{}","status":"firing","alerts":[{"status":"firing","labels":{"alertname":"A","namespace":"fault-lab","workload":"checkout"},"startsAt":"2026-08-01T10:00:00Z","fingerprint":"fp"}]}`
}

func TestHandler_Accepted(t *testing.T) {
	svc, _ := testService(t, &fakeResolver{uid: "u-1"})
	h := newTestHandler(svc, &staticValidator{token: "secret"})

	req := httptest.NewRequest(http.MethodPost, "/webhooks/alertmanager", strings.NewReader(validBody()))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("期望 202，得到 %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"accepted":1`) {
		t.Errorf("响应缺少 accepted: %s", rec.Body.String())
	}
}

func TestHandler_Unauthorized(t *testing.T) {
	svc, _ := testService(t, &fakeResolver{uid: "u-1"})
	h := newTestHandler(svc, &staticValidator{token: "secret"})

	req := httptest.NewRequest(http.MethodPost, "/webhooks/alertmanager", strings.NewReader(validBody()))
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("期望 401，得到 %d", rec.Code)
	}
}

func TestHandler_MissingAuth(t *testing.T) {
	svc, _ := testService(t, &fakeResolver{uid: "u-1"})
	h := newTestHandler(svc, &staticValidator{token: "secret"})

	req := httptest.NewRequest(http.MethodPost, "/webhooks/alertmanager", strings.NewReader(validBody()))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无 Authorization 应 401，得到 %d", rec.Code)
	}
}

func TestHandler_InvalidJSON(t *testing.T) {
	svc, _ := testService(t, &fakeResolver{uid: "u-1"})
	h := newTestHandler(svc, &staticValidator{token: "secret"})

	req := httptest.NewRequest(http.MethodPost, "/webhooks/alertmanager", strings.NewReader("{bad"))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法 JSON 应 400，得到 %d", rec.Code)
	}
}

func TestHandler_BodyLimit(t *testing.T) {
	svc, _ := testService(t, &fakeResolver{uid: "u-1"})
	auth := &staticValidator{token: "secret"}
	h := NewHandler(svc, auth, logr.Discard(), 64)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/alertmanager", strings.NewReader(validBody()))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("超限请求体应 400，得到 %d", rec.Code)
	}
}

func TestHandler_WrongMethod(t *testing.T) {
	svc, _ := testService(t, &fakeResolver{uid: "u-1"})
	h := newTestHandler(svc, &staticValidator{token: "secret"})

	req := httptest.NewRequest(http.MethodGet, "/webhooks/alertmanager", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET 应 405，得到 %d", rec.Code)
	}
}

func TestHandler_PanicRecovery(t *testing.T) {
	// 构造会 panic 的服务（nil writer）。
	clk := &fakeClock{}
	svc := NewService("cluster-a", nil, &fakeResolver{uid: "u-1"}, clk, nil)
	h := newTestHandler(svc, &staticValidator{token: "secret"})

	req := httptest.NewRequest(http.MethodPost, "/webhooks/alertmanager", strings.NewReader(validBody()))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic 应恢复为 500，得到 %d", rec.Code)
	}
}

func TestFileTokenValidator(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/token"
	if err := os.WriteFile(path, []byte("line1\n"), 0o600); err != nil {
		t.Fatalf("写 token 文件失败: %v", err)
	}
	v, err := NewFileTokenValidator(path)
	if err != nil {
		t.Fatalf("创建 validator 失败: %v", err)
	}
	if !v.Validate("line1") {
		t.Error("正确 token 应通过")
	}
	if v.Validate("wrong") {
		t.Error("错误 token 应拒绝")
	}
	if v.Validate("") {
		t.Error("空 token 应拒绝")
	}
}
