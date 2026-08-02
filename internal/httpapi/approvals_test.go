package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// approverAuth 是 approver 角色认证器。
type approverAuth struct{}

func (t *approverAuth) Authenticate(_ *http.Request) (Principal, error) {
	return Principal{Subject: "approver-1", Roles: []Role{RoleViewer, RoleApprover}}, nil
}
func (t *approverAuth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := t.Authenticate(r)
		next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), p)))
	})
}

// viewerAuth 只有 viewer 角色。
type viewerAuth struct{}

func (t *viewerAuth) Authenticate(_ *http.Request) (Principal, error) {
	return Principal{Subject: "viewer-1", Roles: []Role{RoleViewer}}, nil
}
func (t *viewerAuth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := t.Authenticate(r)
		next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), p)))
	})
}

func newApprovalServer(t *testing.T, auth Authenticator, objs ...client.Object) (http.Handler, client.Client) {
	t.Helper()
	c := newHTTPFakeClient(t, objs...)
	h, err := NewServer(ServerDeps{
		K8s:  c,
		Auth: auth,
		Now:  func() time.Time { return time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return h, c
}

func awaitingApprovalIncident() *opsv1alpha1.AIOpsIncident {
	i := sampleIncident("inc-1", "fault-lab", "AwaitingApproval", "warning")
	i.UID = types.UID("uid-1")
	i.Status.Proposal = &opsv1alpha1.ActionProposal{
		Revision:   1,
		Action:     opsv1alpha1.ActionRestartWorkload,
		PlanDigest: "sha256:" + strings.Repeat("a", 64),
	}
	return i
}

func approvalRequest(decision, reason string) *strings.Reader {
	body, _ := json.Marshal(map[string]string{"decision": decision, "reason": reason})
	return strings.NewReader(string(body))
}

func TestApproveIncident_OK(t *testing.T) {
	h, c := newApprovalServer(t, &approverAuth{}, awaitingApprovalIncident())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/fault-lab/inc-1/approval", approvalRequest("Approve", "确认修复"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("期望 201: %d %s", rec.Code, rec.Body.String())
	}
	// 审批对象已创建且字段正确。
	var list opsv1alpha1.RemediationApprovalList
	_ = c.List(context.Background(), &list, client.InNamespace("fault-lab"))
	if len(list.Items) != 1 {
		t.Fatalf("应有 1 个审批: %d", len(list.Items))
	}
	ap := list.Items[0]
	if ap.Spec.IncidentRef.UID != "uid-1" || ap.Spec.IncidentRef.ProposalRevision != 1 {
		t.Errorf("审批绑定错误: %+v", ap.Spec.IncidentRef)
	}
	if ap.Spec.PlanDigest != "sha256:"+strings.Repeat("a", 64) {
		t.Error("planDigest 应从 Status 复制")
	}
	if ap.Spec.Actor != "approver-1" {
		t.Errorf("actor 错误: %s", ap.Spec.Actor)
	}
	if ap.Spec.Decision != opsv1alpha1.ApprovalApprove {
		t.Errorf("decision 错误: %s", ap.Spec.Decision)
	}
}

func TestApproveIncident_Reject(t *testing.T) {
	h, _ := newApprovalServer(t, &approverAuth{}, awaitingApprovalIncident())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/fault-lab/inc-1/approval", approvalRequest("Reject", "风险不可接受"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("Reject 也应创建审批: %d", rec.Code)
	}
}

func TestApproveIncident_ViewerForbidden(t *testing.T) {
	h, _ := newApprovalServer(t, &viewerAuth{}, awaitingApprovalIncident())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/fault-lab/inc-1/approval", approvalRequest("Approve", "确认"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer 应 403: %d", rec.Code)
	}
}

func TestApproveIncident_PhaseConflict(t *testing.T) {
	incident := awaitingApprovalIncident()
	incident.Status.Phase = "Executing" // 不在待审批阶段
	h, _ := newApprovalServer(t, &approverAuth{}, incident)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/fault-lab/inc-1/approval", approvalRequest("Approve", "确认修复"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("阶段不符应 409: %d", rec.Code)
	}
}

func TestApproveIncident_NoProposal(t *testing.T) {
	incident := awaitingApprovalIncident()
	incident.Status.Proposal = nil
	h, _ := newApprovalServer(t, &approverAuth{}, incident)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/fault-lab/inc-1/approval", approvalRequest("Approve", "确认修复"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("无方案应 409: %d", rec.Code)
	}
}

func TestApproveIncident_InvalidBody(t *testing.T) {
	h, _ := newApprovalServer(t, &approverAuth{}, awaitingApprovalIncident())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/fault-lab/inc-1/approval", strings.NewReader("{bad"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法体应 400: %d", rec.Code)
	}
}

func TestApproveIncident_InvalidDecision(t *testing.T) {
	h, _ := newApprovalServer(t, &approverAuth{}, awaitingApprovalIncident())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/fault-lab/inc-1/approval", approvalRequest("Maybe", "确认修复"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法 decision 应 400: %d", rec.Code)
	}
}

func TestApproveIncident_ShortReason(t *testing.T) {
	h, _ := newApprovalServer(t, &approverAuth{}, awaitingApprovalIncident())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/fault-lab/inc-1/approval", approvalRequest("Approve", "ok"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("短 reason 应 400: %d", rec.Code)
	}
}

func TestApproveIncident_NotFound(t *testing.T) {
	h, _ := newApprovalServer(t, &approverAuth{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/fault-lab/missing/approval", approvalRequest("Approve", "确认修复"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("不存在应 404: %d", rec.Code)
	}
}

func TestApproveIncident_ClientCannotForgeDigest(t *testing.T) {
	// 客户端提交的 body 不包含 planDigest（schema 固定），服务端从 Status 复制。
	h, c := newApprovalServer(t, &approverAuth{}, awaitingApprovalIncident())
	body := `{"decision":"Approve","reason":"确认修复","planDigest":"sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/fault-lab/inc-1/approval", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("应 201: %d", rec.Code)
	}
	var list opsv1alpha1.RemediationApprovalList
	_ = c.List(context.Background(), &list, client.InNamespace("fault-lab"))
	if len(list.Items) == 0 {
		t.Fatal("审批未创建")
	}
	if list.Items[0].Spec.PlanDigest == "sha256:"+strings.Repeat("f", 64) {
		t.Error("客户端伪造的 digest 不应生效")
	}
}

// newHTTPFakeClient 构造带 status subresource 的 fake client。
func newHTTPFakeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("scheme: %v", err)
	}
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("ops scheme: %v", err)
	}
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&opsv1alpha1.AIOpsIncident{}, &opsv1alpha1.RemediationApproval{}).
		WithObjects(objs...).
		Build()
}
