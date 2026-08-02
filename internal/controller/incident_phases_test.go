package controller

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	"github.com/user27c/aegisops/internal/analysisclient"
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
	r.Analysis = &fakeAnalysis{}
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

// TestReconcile_RollingBackRestoresSnapshot
// 假回滚缺陷回归:RollingBack 必须使用 Apply 前持久化的快照恢复资源原状。
func TestReconcile_RollingBackRestoresSnapshot(t *testing.T) {
	dep := execDeployment() // replicas=1
	incident := executionIncident()
	incident.Status.Proposal.Action = opsv1alpha1.ActionScaleDeployment
	incident.Status.Proposal.Parameters = apiextensionsv1.JSON{Raw: []byte(`{"replicas":4,"reason":"扩容"}`)}
	incident.Status.Proposal.PlanDigest = "sha256:" + repeatChar('b', 64)

	r, c, _ := newExecReconciler(t, incident, dep)

	// 1. Executing:Apply(replicas 1→4)并持久化快照。
	res := reconcileOnce(t, r, "incident-1")
	if res.RequeueAfter != 15*time.Second {
		t.Fatalf("Apply 后应转 Verifying: %v", res.RequeueAfter)
	}
	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseVerifying {
		t.Fatalf("应转 Verifying: %s", got.Status.Phase)
	}
	if got.Status.Execution == nil || got.Status.Execution.Reference == nil || got.Status.Execution.Reference.SnapshotID == "" {
		t.Fatal("快照 ID 未持久化到执行引用")
	}

	var depAfter appsv1.Deployment
	_ = c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: "checkout-api"}, &depAfter)
	if *depAfter.Spec.Replicas != 4 {
		t.Fatalf("Apply 后副本数应为 4: %d", *depAfter.Spec.Replicas)
	}

	// 2. 验证失败 → 超时 → RollingBack。
	rollbackBefore := got.DeepCopy()
	got.Status.Phase = opsv1alpha1.PhaseRollingBack
	_ = c.Status().Patch(context.Background(), &got, client.MergeFrom(rollbackBefore))

	// 3. RollingBack:用持久化快照回滚。
	reconcileOnce(t, r, "incident-1")

	got = opsv1alpha1.AIOpsIncident{}
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseRolledBack {
		t.Fatalf("回滚成功应 RolledBack: %s", got.Status.Phase)
	}
	var depRolled appsv1.Deployment
	_ = c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: "checkout-api"}, &depRolled)
	if *depRolled.Spec.Replicas != 1 {
		t.Errorf("回滚后副本数应恢复为 1(执行前快照): %d", *depRolled.Spec.Replicas)
	}
}

// TestReconcile_RollingBackMissingSnapshotEscalates
// 无快照时回滚必须 fail-closed(Escalated),绝不假装回滚成功。
func TestReconcile_RollingBackMissingSnapshotEscalates(t *testing.T) {
	dep := execDeployment()
	incident := executionIncident()
	incident.Status.Phase = opsv1alpha1.PhaseRollingBack
	incident.Status.Execution = &opsv1alpha1.ExecutionStatus{
		Reference: &opsv1alpha1.ExecutionReference{ExecutionID: "e-1", OperationID: "op-1"},
		// 没有 SnapshotID。
	}
	r, c, _ := newExecReconciler(t, incident, dep)

	reconcileOnce(t, r, "incident-1")

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseEscalated {
		t.Errorf("无快照回滚应 Escalated: %s", got.Status.Phase)
	}
	if c := got.GetCondition("RollbackReady"); c == nil || c.Status != metav1.ConditionFalse {
		t.Error("应标记 RollbackReady=False")
	}
}

func TestReconcile_ExecutingSnapshotPersistFailedEscalates(t *testing.T) {
	dep := execDeployment()
	incident := executionIncident()
	analysis := &fakeAnalysis{err: errNotFound} // PutSnapshot 失败
	r, c, _ := newExecReconciler(t, incident, dep)
	r.Analysis = analysis

	reconcileOnce(t, r, "incident-1")

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseEscalated {
		t.Errorf("快照保存失败应 Escalated（fail closed）: %s", got.Status.Phase)
	}
	if c := got.GetCondition("ExecutionReady"); c == nil || c.Status != metav1.ConditionFalse {
		t.Error("应标记 ExecutionReady=False")
	}
}

func TestReconcile_ExecutingNoAnalysisEscalates(t *testing.T) {
	dep := execDeployment()
	incident := executionIncident()
	r, c, _ := newExecReconciler(t, incident, dep)
	r.Analysis = nil // 快照服务不可用

	reconcileOnce(t, r, "incident-1")

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseEscalated {
		t.Errorf("无快照服务应 Escalated: %s", got.Status.Phase)
	}
}

func TestReconcile_RollingBackSnapshotReadFailed(t *testing.T) {
	dep := execDeployment()
	incident := executionIncident()
	incident.Status.Phase = opsv1alpha1.PhaseRollingBack
	incident.Status.Execution = &opsv1alpha1.ExecutionStatus{
		Reference: &opsv1alpha1.ExecutionReference{ExecutionID: "e-1", OperationID: "op-1", SnapshotID: "missing"},
	}
	r, c, _ := newExecReconciler(t, incident, dep)

	reconcileOnce(t, r, "incident-1")

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseEscalated {
		t.Errorf("快照读取失败应 Escalated: %s", got.Status.Phase)
	}
}

func TestReconcile_RollingBackSnapshotActionMismatch(t *testing.T) {
	dep := execDeployment()
	incident := executionIncident()
	incident.Status.Phase = opsv1alpha1.PhaseRollingBack
	incident.Status.Execution = &opsv1alpha1.ExecutionStatus{
		Reference: &opsv1alpha1.ExecutionReference{ExecutionID: "e-1", OperationID: "op-1", SnapshotID: "s-1"},
	}
	r, c, _ := newExecReconciler(t, incident, dep)
	// 快照动作与方案动作不一致（RestartWorkload vs 存的是 Scale）。
	analysis := &fakeAnalysis{}
	analysis.snapshots = map[string]analysisclient.Snapshot{
		"s-1": {ID: "s-1", ActionType: "ScaleDeployment", Snapshot: []byte(`{"Action":"ScaleDeployment","Payload":{"replicas":1}}`)},
	}
	r.Analysis = analysis

	reconcileOnce(t, r, "incident-1")

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseEscalated {
		t.Errorf("快照动作不匹配应 Escalated: %s", got.Status.Phase)
	}
}

func TestReconcile_RollingBackBadSnapshotJSON(t *testing.T) {
	dep := execDeployment()
	incident := executionIncident()
	incident.Status.Phase = opsv1alpha1.PhaseRollingBack
	incident.Status.Execution = &opsv1alpha1.ExecutionStatus{
		Reference: &opsv1alpha1.ExecutionReference{ExecutionID: "e-1", OperationID: "op-1", SnapshotID: "s-1"},
	}
	r, c, _ := newExecReconciler(t, incident, dep)
	analysis := &fakeAnalysis{}
	analysis.snapshots = map[string]analysisclient.Snapshot{
		"e-1": {ID: "s-1", ExecutionID: "e-1", ActionType: "RestartWorkload", Snapshot: []byte(`{bad`)},
	}
	r.Analysis = analysis

	reconcileOnce(t, r, "incident-1")

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseEscalated {
		t.Errorf("非法快照 JSON 应 Escalated: %s", got.Status.Phase)
	}
}

func TestClearPhaseEphemeralStatus_Terminal(t *testing.T) {
	i := newTestIncident(opsv1alpha1.PhaseVerifying)
	i.Status.Execution = &opsv1alpha1.ExecutionStatus{LastError: "boom"}
	ClearPhaseEphemeralStatus(i, opsv1alpha1.PhaseResolved)
	if i.Status.Execution != nil && i.Status.Execution.LastError != "" {
		t.Error("终态应清理执行错误细节")
	}
	// 回滚分支清验证明细。
	i2 := newTestIncident(opsv1alpha1.PhaseVerifying)
	i2.Status.Verification = &opsv1alpha1.VerificationSummary{Checks: []opsv1alpha1.VerificationCheck{{Name: "x"}}}
	ClearPhaseEphemeralStatus(i2, opsv1alpha1.PhaseRollingBack)
	if i2.Status.Verification != nil && len(i2.Status.Verification.Checks) != 0 {
		t.Error("RollingBack 应清验证明细")
	}
}

func TestReconcile_ExecutingPreflightFailed(t *testing.T) {
	dep := execDeployment()
	// rollout 进行中(observedGeneration != generation)使 Restart Preflight 失败。
	dep.Status.ObservedGeneration = 0
	incident := executionIncident()
	r, c, _ := newExecReconciler(t, incident, dep)

	reconcileOnce(t, r, "incident-1")

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseEscalated {
		t.Errorf("Preflight 失败应 Escalated: %s", got.Status.Phase)
	}
	if c := got.GetCondition("ExecutionReady"); c == nil || c.Status != metav1.ConditionFalse {
		t.Error("应标记 ExecutionReady=False")
	}
}

func TestReconcile_ExecutingSnapshotFailed(t *testing.T) {
	// Snapshot 失败:目标不存在。
	incident := executionIncident()
	r, c, _ := newExecReconciler(t, incident) // 无 Deployment

	reconcileOnce(t, r, "incident-1")

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseEscalated {
		t.Errorf("快照失败应 Escalated: %s", got.Status.Phase)
	}
}

func TestReconcile_ExecutingApplyFailed(t *testing.T) {
	// Scale 缺 replicas 参数 → Apply 失败。
	dep := execDeployment()
	incident := executionIncident()
	incident.Status.Proposal.Action = opsv1alpha1.ActionScaleDeployment
	incident.Status.Proposal.Parameters = apiextensionsv1.JSON{Raw: []byte(`{}`)} // 缺 replicas
	incident.Status.Proposal.PlanDigest = "sha256:" + repeatChar('e', 64)
	r, c, _ := newExecReconciler(t, incident, dep)

	reconcileOnce(t, r, "incident-1")

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseEscalated {
		t.Errorf("Apply 失败应 Escalated: %s", got.Status.Phase)
	}
}

func TestReconcile_VerifyingCheckFailed(t *testing.T) {
	// verifier 报错(registry 缺动作)→ CheckFailed 条件 + requeue。
	dep := execDeployment()
	incident := executionIncident()
	incident.Status.Phase = opsv1alpha1.PhaseVerifying
	incident.Status.Execution = &opsv1alpha1.ExecutionStatus{
		Reference: &opsv1alpha1.ExecutionReference{ExecutionID: "e-1", OperationID: "op-1"},
	}
	r, c, _ := newExecReconciler(t, incident, dep)
	r.Verifier = &failingVerifier{}

	res := reconcileOnce(t, r, "incident-1")
	if res.RequeueAfter != 15*time.Second {
		t.Errorf("CheckFailed 应 requeue 15s: %v", res.RequeueAfter)
	}
	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if c := got.GetCondition("VerificationReady"); c == nil || c.Status != metav1.ConditionFalse {
		t.Error("应标记 VerificationReady=False")
	}
}

// failingVerifier 总是返回错误。
type failingVerifier struct{}

func (f *failingVerifier) Check(context.Context, *opsv1alpha1.AIOpsIncident, *executor.Registry, logr.Logger) (executor.Verification, error) {
	return executor.Verification{}, errNotFound
}

func TestReconcile_RollingBackRollbackFailed(t *testing.T) {
	// 回滚时目标不存在 → Rollback 报错 → Escalated(绝不假装成功)。
	incident := executionIncident()
	incident.Status.Proposal.Action = opsv1alpha1.ActionScaleDeployment
	incident.Status.Proposal.Parameters = apiextensionsv1.JSON{Raw: []byte(`{"replicas":5}`)}
	incident.Status.Proposal.PlanDigest = "sha256:" + repeatChar('f', 64)
	incident.Status.Phase = opsv1alpha1.PhaseRollingBack
	incident.Status.Execution = &opsv1alpha1.ExecutionStatus{
		Reference: &opsv1alpha1.ExecutionReference{ExecutionID: "e-1", OperationID: "op-1", SnapshotID: "s-1"},
	}
	r, c, _ := newExecReconciler(t, incident) // 无 Deployment
	analysis := &fakeAnalysis{}
	analysis.snapshots = map[string]analysisclient.Snapshot{
		"e-1": {ID: "s-1", ExecutionID: "e-1", ActionType: "ScaleDeployment", Snapshot: []byte(`{"Action":"ScaleDeployment","Payload":{"replicas":3}}`)},
	}
	r.Analysis = analysis

	reconcileOnce(t, r, "incident-1")

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseEscalated {
		t.Errorf("回滚失败应 Escalated: %s", got.Status.Phase)
	}
	if c := got.GetCondition("RollbackReady"); c == nil || c.Status != metav1.ConditionFalse {
		t.Error("应标记 RollbackReady=False")
	}
}
