package controller

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// 覆盖 dispatchPhase 的 M3+ 阶段分支、stuckInterval、hasExecuted。

func TestReconcile_FuturePhasesRequeue(t *testing.T) {
	// 诊断/策略/执行等 M3+ 阶段：保持现状并延后重试（不报错、不推进）。
	for _, phase := range []opsv1alpha1.IncidentPhase{
		opsv1alpha1.PhasePolicyChecking,
		opsv1alpha1.PhaseAwaitingApproval,
		opsv1alpha1.PhaseExecuting,
		opsv1alpha1.PhaseVerifying,
		opsv1alpha1.PhaseRollingBack,
	} {
		t.Run(string(phase), func(t *testing.T) {
			incident := firingIncident()
			incident.Finalizers = []string{FinalizerName}
			incident.Status.Phase = phase
			r, c := newReconciler(t, nil, incident)

			res := reconcileOnce(t, r, "incident-1")
			if res.RequeueAfter != 30*time.Second {
				t.Errorf("M3+ 阶段应 requeue 30s: %v", res.RequeueAfter)
			}
			var got opsv1alpha1.AIOpsIncident
			_ = c.Get(context.Background(), keyIncident(), &got)
			if got.Status.Phase != phase {
				t.Errorf("Phase 不应被改变: %s", got.Status.Phase)
			}
		})
	}
}

func TestReconcile_DiagnosingWithoutAnalysis(t *testing.T) {
	// Diagnosing 已有真实 handler；Analysis 未配置时保持阶段并延后重试。
	incident := firingIncident()
	incident.Finalizers = []string{FinalizerName}
	incident.Status.Phase = opsv1alpha1.PhaseDiagnosing
	incident.Status.Analysis = &opsv1alpha1.AnalysisReference{AnalysisID: "a-1"}
	r, _ := newReconciler(t, nil, incident)

	res, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: client.ObjectKey{Namespace: "fault-lab", Name: "incident-1"},
	})
	if err == nil {
		t.Error("缺少 Analysis 客户端应报错（fail closed）")
	}
	_ = res
}

func TestReconcile_UnknownPhaseRequeue(t *testing.T) {
	incident := firingIncident()
	incident.Finalizers = []string{FinalizerName}
	incident.Status.Phase = "NotAPhase"
	r, _ := newReconciler(t, nil, incident)

	res := reconcileOnce(t, r, "incident-1")
	if res.RequeueAfter != 30*time.Second {
		t.Errorf("未知阶段应 requeue 30s: %v", res.RequeueAfter)
	}
}

func TestHasExecuted(t *testing.T) {
	i := firingIncident()
	if hasExecuted(i) {
		t.Error("未执行不应返回 true")
	}
	i.Status.Execution = &opsv1alpha1.ExecutionStatus{
		Reference: &opsv1alpha1.ExecutionReference{
			ExecutionID: "exec-1",
			OperationID: "op-1",
			StartedAt:   &metav1.Time{Time: time.Now()},
		},
	}
	if !hasExecuted(i) {
		t.Error("已执行应返回 true")
	}
	// 只有 Attempts 没有 Reference 不算执行。
	i.Status.Execution = &opsv1alpha1.ExecutionStatus{Attempts: 1}
	if hasExecuted(i) {
		t.Error("无 Reference 不算已执行")
	}
}

func TestReconcile_EvidenceWindowAndCounts(t *testing.T) {
	incident := firingIncident()
	collector := &fakeCollector{hash: "hash-2"}
	r, c := newReconciler(t, collector, incident, targetDeployment())

	reconcileOnce(t, r, "incident-1") // finalizer
	reconcileOnce(t, r, "incident-1") // Detected
	reconcileOnce(t, r, "incident-1") // CollectingEvidence

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Evidence == nil {
		t.Fatal("证据摘要为空")
	}
	if got.Status.Evidence.Window.Start.IsZero() || got.Status.Evidence.Window.End.IsZero() {
		t.Error("证据窗口未写入")
	}
	if len(got.Status.Evidence.Counts) == 0 {
		t.Error("证据 counts 未写入")
	}
	if got.Status.Evidence.ID != string(incident.UID) && got.Status.Evidence.ID != "" {
		t.Errorf("证据 ID 异常: %s", got.Status.Evidence.ID)
	}
}

func keyIncident() types.NamespacedName {
	return types.NamespacedName{Namespace: "fault-lab", Name: "incident-1"}
}
