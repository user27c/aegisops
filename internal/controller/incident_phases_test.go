package controller

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	"github.com/user27c/aegisops/internal/executor"
	"github.com/user27c/aegisops/internal/verifier"
)

// 覆盖 dispatchPhase 的 M3+ 阶段分支、stuckInterval、hasExecuted。

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

func executionIncident() *opsv1alpha1.AIOpsIncident {
	i := firingIncident()
	i.UID = types.UID("uid-1")
	i.Finalizers = []string{FinalizerName}
	i.Status.Phase = opsv1alpha1.PhaseExecuting
	i.Status.Proposal = &opsv1alpha1.ActionProposal{
		Revision:   1,
		Action:     opsv1alpha1.ActionRestartWorkload,
		Parameters: apiextensionsv1.JSON{Raw: []byte(`{"reason":"CrashLoopBackOff 持续"}`)},
		PlanDigest: "sha256:" + repeatChar('a', 64),
	}
	return i
}

func newExecReconciler(t *testing.T, objs ...client.Object) (*IncidentReconciler, client.Client, *executor.Registry) {
	t.Helper()
	registry, err := executor.DefaultRegistry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	r, c := newReconciler(t, nil, objs...)
	r.Executor = registry
	r.Verifier = &verifier.KubernetesChecker{Client: c}
	return r, c, registry
}

func TestReconcile_ExecutingAppliesAndVerifies(t *testing.T) {
	dep := execDeployment()
	incident := executionIncident()
	r, c, _ := newExecReconciler(t, incident, dep)

	// 第一次:Apply → Verifying。
	res := reconcileOnce(t, r, "incident-1")
	if res.RequeueAfter != 15*time.Second {
		t.Errorf("应 requeue 15s: %v", res.RequeueAfter)
	}
	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseVerifying {
		t.Fatalf("Apply 后应转 Verifying: %s", got.Status.Phase)
	}
	if got.Status.Execution == nil || got.Status.Execution.Reference == nil {
		t.Fatal("执行引用未写入")
	}

	// 目标被写入 restart 注解。
	var depGot appsv1.Deployment
	_ = c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: "checkout-api"}, &depGot)
	if depGot.Spec.Template.Annotations[executor.RestartAnnotationKey] == "" {
		t.Error("restart 注解未写入")
	}

	// 第二次:幂等(Execution.Reference 存在)→ 不重复 Apply → 直接 Verifying。
	res = reconcileOnce(t, r, "incident-1")
	if res.RequeueAfter != 15*time.Second {
		t.Errorf("Verifying 应 requeue 15s: %v", res.RequeueAfter)
	}
	got = opsv1alpha1.AIOpsIncident{}
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseVerifying {
		t.Errorf("幂等路径应保持 Verifying: %s", got.Status.Phase)
	}
}

func TestReconcile_VerifyingResolvesAfterTwoSuccess(t *testing.T) {
	dep := execDeployment()
	incident := executionIncident()
	incident.Status.Phase = opsv1alpha1.PhaseVerifying
	incident.Status.Execution = &opsv1alpha1.ExecutionStatus{
		Reference: &opsv1alpha1.ExecutionReference{ExecutionID: "e-1", OperationID: "op-1"},
	}
	r, c, _ := newExecReconciler(t, incident, dep)

	reconcileOnce(t, r, "incident-1") // 第一次成功(1/2)
	reconcileOnce(t, r, "incident-1") // 第二次成功(2/2) → Resolved

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseResolved {
		t.Errorf("连续两次成功应 Resolved: %s", got.Status.Phase)
	}
	if got.Status.Verification == nil || got.Status.Verification.ConsecutiveSuccesses != 2 {
		t.Errorf("验证计数错误: %+v", got.Status.Verification)
	}
}

func TestReconcile_VerifyingUnhealthyResetsCounter(t *testing.T) {
	dep := execDeployment()
	incident := executionIncident()
	incident.Status.Phase = opsv1alpha1.PhaseVerifying
	incident.Status.Execution = &opsv1alpha1.ExecutionStatus{
		Reference: &opsv1alpha1.ExecutionReference{ExecutionID: "e-1", OperationID: "op-1"},
	}
	r, c, _ := newExecReconciler(t, incident, dep)

	reconcileOnce(t, r, "incident-1") // 成功(1/2)
	// 目标变坏。
	var depLatest appsv1.Deployment
	_ = c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: "checkout-api"}, &depLatest)
	before := depLatest.DeepCopy()
	depLatest.Status.UnavailableReplicas = 1
	_ = c.Status().Patch(context.Background(), &depLatest, client.MergeFrom(before))
	reconcileOnce(t, r, "incident-1") // 失败 → 计数清零

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Verification == nil || got.Status.Verification.ConsecutiveSuccesses != 0 {
		t.Errorf("失败应清零计数: %+v", got.Status.Verification)
	}
	if got.Status.Phase != opsv1alpha1.PhaseVerifying {
		t.Errorf("失败不应改变阶段: %s", got.Status.Phase)
	}
}

func TestReconcile_VerifyingTimeoutRollsBack(t *testing.T) {
	dep := execDeployment()
	incident := executionIncident()
	incident.Status.Phase = opsv1alpha1.PhaseVerifying
	incident.Status.Execution = &opsv1alpha1.ExecutionStatus{
		Reference: &opsv1alpha1.ExecutionReference{ExecutionID: "e-1", OperationID: "op-1"},
	}
	// 验证已过期（相对 reconciler 的固定时钟）。
	incident.Status.Verification = &opsv1alpha1.VerificationSummary{
		Deadline: &metav1.Time{Time: time.Date(2026, 8, 1, 9, 59, 0, 0, time.UTC)},
	}
	r, c, _ := newExecReconciler(t, incident, dep)

	reconcileOnce(t, r, "incident-1")

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseRollingBack {
		t.Errorf("验证超时应转 RollingBack: %s", got.Status.Phase)
	}
}

func TestReconcile_RollingBackRestartUnsupportedEscalates(t *testing.T) {
	dep := execDeployment()
	incident := executionIncident()
	incident.Status.Phase = opsv1alpha1.PhaseRollingBack
	r, c, _ := newExecReconciler(t, incident, dep)

	reconcileOnce(t, r, "incident-1")

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseEscalated {
		t.Errorf("Restart 不支持回滚应 Escalated: %s", got.Status.Phase)
	}
}

func TestReconcile_ExecutingUnregisteredAction(t *testing.T) {
	dep := execDeployment()
	incident := executionIncident()
	incident.Status.Proposal.Action = "UnknownAction"
	r, c, _ := newExecReconciler(t, incident, dep)

	reconcileOnce(t, r, "incident-1")

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseEscalated {
		t.Errorf("未注册动作应 Escalated: %s", got.Status.Phase)
	}
}

func execDeployment() *appsv1.Deployment {
	replicas := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "checkout-api", Namespace: "fault-lab", UID: "dep-uid-1", Generation: 1,
			Annotations: map[string]string{},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "checkout"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "checkout"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app", Image: "registry/checkout:v1"}},
				},
			},
		},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 1,
			AvailableReplicas:  1,
		},
	}
}
