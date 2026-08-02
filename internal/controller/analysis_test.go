package controller

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	"github.com/user27c/aegisops/internal/analysisclient"
)

// fakeAnalysis 是可控的诊断客户端。
type fakeAnalysis struct {
	status   analysisclient.JobStatus
	result   *analysisclient.DiagnosisResult
	err      error
	submitFn func(ctx context.Context, key string, req analysisclient.SubmitRequest) (analysisclient.SubmitResponse, error)
	// 记录最后一次 Submit 的幂等键。
	lastKey string
	// snapshots 是已保存的执行快照（按 ID）。
	snapshots map[string]analysisclient.Snapshot
}

func (f *fakeAnalysis) Submit(ctx context.Context, key string, req analysisclient.SubmitRequest) (analysisclient.SubmitResponse, error) {
	f.lastKey = key
	if f.submitFn != nil {
		return f.submitFn(ctx, key, req)
	}
	return analysisclient.SubmitResponse{AnalysisID: "a-1", EvidenceID: "e-1", Status: analysisclient.StatusQueued}, f.err
}

func (f *fakeAnalysis) Get(_ context.Context, analysisID string) (analysisclient.AnalysisResponse, error) {
	if f.err != nil {
		return analysisclient.AnalysisResponse{}, f.err
	}
	return analysisclient.AnalysisResponse{
		ID:     analysisID,
		Status: f.status,
		Result: f.result,
	}, nil
}

func (f *fakeAnalysis) PutSnapshot(_ context.Context, _ string, req analysisclient.SnapshotRequest) (analysisclient.SnapshotRef, error) {
	if f.err != nil {
		return analysisclient.SnapshotRef{}, f.err
	}
	if f.snapshots == nil {
		f.snapshots = map[string]analysisclient.Snapshot{}
	}
	// API 语义：GET 按 execution_id 查询。
	f.snapshots[req.ExecutionID] = analysisclient.Snapshot{
		ID:          "s-1",
		IncidentUID: req.IncidentUID,
		ExecutionID: req.ExecutionID,
		ActionType:  req.ActionType,
		Snapshot:    req.Snapshot,
		Hash:        "h",
	}
	return analysisclient.SnapshotRef{ID: "s-1", Hash: "h"}, nil
}

func (f *fakeAnalysis) GetSnapshot(_ context.Context, id string) (analysisclient.Snapshot, error) {
	if f.snapshots == nil {
		return analysisclient.Snapshot{}, errNotFound
	}
	snap, ok := f.snapshots[id]
	if !ok {
		return analysisclient.Snapshot{}, errNotFound
	}
	return snap, nil
}

var errNotFound = errors.New("not found")

func newDiagnosingIncident() *opsv1alpha1.AIOpsIncident {
	i := firingIncident()
	i.Finalizers = []string{FinalizerName}
	i.Status.Phase = opsv1alpha1.PhaseDiagnosing
	i.Status.Evidence = &opsv1alpha1.EvidenceSummary{Hash: "hash-1"}
	i.Status.Analysis = &opsv1alpha1.AnalysisReference{AnalysisID: "a-1"}
	return i
}

func successResult() *analysisclient.DiagnosisResult {
	params, _ := json.Marshal(map[string]any{"container": "app", "memoryLimit": "384Mi"})
	return &analysisclient.DiagnosisResult{
		Category:    "CrashLoop",
		RootCause:   "容器启动失败",
		Confidence:  0.9,
		EvidenceIDs: []string{"e1"},
		RunbookRefs: []string{"runbook://k8s-crashloop-config/v1.0.0"},
		Reviewer:    analysisclient.ReviewerResult{Verdict: "pass", Pass: true},
		Proposal: &analysisclient.ProposalDTO{
			Action:     "RestoreConfigMap",
			Parameters: params,
		},
	}
}

func TestReconcile_DiagnosingSuccess(t *testing.T) {
	incident := newDiagnosingIncident()
	analysis := &fakeAnalysis{status: analysisclient.StatusSucceeded, result: successResult()}
	r, c := newReconciler(t, nil, incident)
	r.Analysis = analysis

	res := reconcileOnce(t, r, "incident-1")
	if res.RequeueAfter > 0 {
		t.Errorf("成功后不应 requeue: %v", res.RequeueAfter)
	}

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhasePolicyChecking {
		t.Errorf("Phase 应为 PolicyChecking: %s", got.Status.Phase)
	}
	if got.Status.Diagnosis == nil || got.Status.Diagnosis.Category != "CrashLoop" {
		t.Errorf("诊断未写入: %+v", got.Status.Diagnosis)
	}
	if got.Status.Proposal == nil || got.Status.Proposal.Action != opsv1alpha1.ActionRestoreConfigMap {
		t.Errorf("方案未写入: %+v", got.Status.Proposal)
	}
	if got.Status.Diagnosis.ReviewerVerdict != "pass" {
		t.Errorf("Reviewer 结论未写入: %s", got.Status.Diagnosis.ReviewerVerdict)
	}
}

func TestReconcile_DiagnosingProcessingRequeues(t *testing.T) {
	incident := newDiagnosingIncident()
	analysis := &fakeAnalysis{status: analysisclient.StatusProcessing}
	r, _ := newReconciler(t, nil, incident)
	r.Analysis = analysis

	res := reconcileOnce(t, r, "incident-1")
	if res.RequeueAfter != 5*time.Second {
		t.Errorf("processing 应 requeue 5s: %v", res.RequeueAfter)
	}
}

func TestReconcile_DiagnosingFailedEscalates(t *testing.T) {
	incident := newDiagnosingIncident()
	analysis := &fakeAnalysis{status: analysisclient.StatusFailed}
	r, c := newReconciler(t, nil, incident)
	r.Analysis = analysis

	reconcileOnce(t, r, "incident-1")

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseEscalated {
		t.Errorf("分析失败应 Escalated: %s", got.Status.Phase)
	}
	if c := got.GetCondition("DiagnosisReady"); c == nil || c.Status != metav1.ConditionFalse {
		t.Error("应设置 DiagnosisReady=False 条件")
	}
}

func TestReconcile_DiagnosingSubmit(t *testing.T) {
	// CollectingEvidence 阶段提交分析任务。
	incident := firingIncident()
	incident.Finalizers = []string{FinalizerName}
	incident.Status.Phase = opsv1alpha1.PhaseCollectingEvidence
	collector := &fakeCollector{hash: "hash-1"}
	analysis := &fakeAnalysis{}
	r, c := newReconciler(t, collector, incident, targetDeployment())
	r.Analysis = analysis
	r.DiagnosisEnabled = true

	reconcileOnce(t, r, "incident-1")

	if analysis.lastKey == "" {
		t.Fatal("应提交分析任务")
	}
	if !strings.Contains(analysis.lastKey, string(incident.UID)) {
		t.Errorf("幂等键应含 incident UID: %s", analysis.lastKey)
	}
	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseDiagnosing {
		t.Errorf("提交后应转 Diagnosing: %s", got.Status.Phase)
	}
	if got.Status.Analysis == nil || got.Status.Analysis.AnalysisID != "a-1" {
		t.Errorf("Analysis 引用未写入: %+v", got.Status.Analysis)
	}
}
