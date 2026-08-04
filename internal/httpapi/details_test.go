package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

func newTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = opsv1alpha1.AddToScheme(scheme)
	return scheme
}

func newFakeClient(t *testing.T, scheme *runtime.Scheme, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&opsv1alpha1.AIOpsIncident{}).
		WithObjects(objs...).
		Build()
}

// fakeDiagnosis 是 DiagnosisReader 的测试替身。
type fakeDiagnosis struct {
	evidence *EvidenceDetail
	timeline []TimelineEntryDTO
	evErr    error
	tlErr    error
}

func (f *fakeDiagnosis) GetEvidence(_ context.Context, _ string) (*EvidenceDetail, error) {
	return f.evidence, f.evErr
}

func (f *fakeDiagnosis) GetTimeline(_ context.Context, _ string) ([]TimelineEntryDTO, error) {
	return f.timeline, f.tlErr
}

func incidentWithTimeline(hasEvidence bool) *opsv1alpha1.AIOpsIncident {
	i := sampleIncident("oom-1", "fault-lab", "Detected", "critical")
	i.UID = "uid-123456"
	i.Status.Timeline = []opsv1alpha1.TimelineEntry{
		{Time: metav1.NewTime(time.Now()), Type: "PhaseTransition", Reason: "Detected", Message: "进入收集"},
	}
	if hasEvidence {
		i.Status.Evidence = &opsv1alpha1.EvidenceSummary{
			ID:   "evidence-uuid-1",
			Hash: "sha256:abcdef",
		}
	}
	return i
}

func TestGetIncidentTimeline_AuditSource(t *testing.T) {
	diag := &fakeDiagnosis{
		timeline: []TimelineEntryDTO{
			{Time: time.Now(), Type: "ExecutionStarted", Reason: "apply", Actor: "operator", Sequence: 7, EventHash: "a1b2c3d4e5f6a7b8c9d0"},
		},
	}
	// 用真实 Server 构造（带 Diagnosis）。
	scheme := newTestScheme()
	c := newFakeClient(t, scheme, incidentWithTimeline(false))
	h, err := NewServer(ServerDeps{K8s: c, Auth: &testAuth{}, Diagnosis: diag, Now: time.Now})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	rec := doRequest(t, h, http.MethodGet, "/api/v1/incidents/fault-lab/oom-1/timeline")
	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200: %d %s", rec.Code, rec.Body.String())
	}
	var resp TimelineResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if resp.Source != "audit" {
		t.Errorf("应来自 audit: %s", resp.Source)
	}
	if resp.DetailsUnavailable {
		t.Error("不应标记不可用")
	}
	if len(resp.Items) != 1 || resp.Items[0].Actor != "operator" || resp.Items[0].Sequence != 7 {
		t.Errorf("审计时间线字段错误: %+v", resp.Items)
	}
	if resp.Items[0].EventHash != "a1b2c3d4e5f6" {
		t.Errorf("eventHash 应截断前 12 位: %q", resp.Items[0].EventHash)
	}
}

func TestGetIncidentTimeline_DegradesToCR(t *testing.T) {
	diag := &fakeDiagnosis{tlErr: ErrDiagnosisUnavailable}
	scheme := newTestScheme()
	c := newFakeClient(t, scheme, incidentWithTimeline(false))
	h, err := NewServer(ServerDeps{K8s: c, Auth: &testAuth{}, Diagnosis: diag, Now: time.Now})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	rec := doRequest(t, h, http.MethodGet, "/api/v1/incidents/fault-lab/oom-1/timeline")
	if rec.Code != http.StatusOK {
		t.Fatalf("诊断不可用不应 500: %d", rec.Code)
	}
	var resp TimelineResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if !resp.DetailsUnavailable {
		t.Error("应标记 detailsUnavailable")
	}
	if resp.Source != "cr" {
		t.Errorf("应回退 CR 时间线: %s", resp.Source)
	}
	if len(resp.Items) != 1 || resp.Items[0].Type != "PhaseTransition" {
		t.Errorf("应含 CR 时间线条目: %+v", resp.Items)
	}
}

func TestGetIncidentEvidence_Success(t *testing.T) {
	diag := &fakeDiagnosis{
		evidence: &EvidenceDetail{
			ID: "evidence-uuid-1", Hash: "sha256:abcdef",
			WindowStart: time.Now().Add(-time.Hour), WindowEnd: time.Now(),
			Partial: false, Redactions: 2,
			Items: []EvidenceItemDetail{
				{ID: "k8s-1", Kind: "KubernetesEvent", Source: "kubernetes/events", Summary: "container restart", Timestamp: time.Now()},
			},
		},
	}
	scheme := newTestScheme()
	c := newFakeClient(t, scheme, incidentWithTimeline(true))
	h, err := NewServer(ServerDeps{K8s: c, Auth: &testAuth{}, Diagnosis: diag, Now: time.Now})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	rec := doRequest(t, h, http.MethodGet, "/api/v1/incidents/fault-lab/oom-1/evidence")
	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200: %d %s", rec.Code, rec.Body.String())
	}
	var resp EvidenceDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if resp.ID != "evidence-uuid-1" || resp.Redactions != 2 {
		t.Errorf("证据详情字段错误: %+v", resp)
	}
	if len(resp.Items) != 1 || resp.Items[0].Summary != "container restart" {
		t.Errorf("items 应只含脱敏字段: %+v", resp.Items)
	}
}

func TestGetIncidentEvidence_UnavailableDegrades(t *testing.T) {
	diag := &fakeDiagnosis{evErr: ErrDiagnosisUnavailable}
	scheme := newTestScheme()
	c := newFakeClient(t, scheme, incidentWithTimeline(true))
	h, err := NewServer(ServerDeps{K8s: c, Auth: &testAuth{}, Diagnosis: diag, Now: time.Now})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	rec := doRequest(t, h, http.MethodGet, "/api/v1/incidents/fault-lab/oom-1/evidence")
	if rec.Code != http.StatusOK {
		t.Fatalf("诊断不可用不应 500: %d", rec.Code)
	}
	var resp EvidenceResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if !resp.DetailsUnavailable {
		t.Error("应标记 detailsUnavailable")
	}
	if resp.Hash != "sha256:abcdef" {
		t.Errorf("降级响应应含 CR 证据哈希: %+v", resp)
	}
}

func TestGetIncidentEvidence_NotFound(t *testing.T) {
	diag := &fakeDiagnosis{evErr: ErrDiagnosisNotFound}
	scheme := newTestScheme()
	c := newFakeClient(t, scheme, incidentWithTimeline(true))
	h, err := NewServer(ServerDeps{K8s: c, Auth: &testAuth{}, Diagnosis: diag, Now: time.Now})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	rec := doRequest(t, h, http.MethodGet, "/api/v1/incidents/fault-lab/oom-1/evidence")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("证据不存在应 404: %d", rec.Code)
	}
}

func TestGetIncidentTimeline_NotFound(t *testing.T) {
	scheme := newTestScheme()
	c := newFakeClient(t, scheme)
	h, err := NewServer(ServerDeps{K8s: c, Auth: &testAuth{}, Diagnosis: &fakeDiagnosis{}, Now: time.Now})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	rec := doRequest(t, h, http.MethodGet, "/api/v1/incidents/fault-lab/nonexistent/timeline")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("不存在事故应 404: %d", rec.Code)
	}
}
