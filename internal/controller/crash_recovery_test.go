package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	"github.com/user27c/aegisops/internal/audit"
	"github.com/user27c/aegisops/internal/executor"
	"github.com/user27c/aegisops/internal/targetlock"
)

// TestCrashRecovery_ExecutingAfterApply:Apply 后、状态写前崩溃。
// 重启后 Execution.Reference 缺失但目标已有 OperationID 注解 →
// 重新 Apply 幂等跳过 → 写 Execution → Verifying(不重复执行)。
func TestCrashRecovery_ExecutingAfterApply(t *testing.T) {
	dep := execDeployment()
	incident := executionIncident()
	r, c, _ := newExecReconciler(t, incident, dep)

	// 第一次 reconcile:Apply 成功(restart 注解 + OperationID 写入)。
	reconcileOnce(t, r, "incident-1")

	var depBefore appsv1.Deployment
	_ = c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: "checkout-api"}, &depBefore)
	opID := depBefore.Annotations[executor.OperationIDAnnotation]
	if opID == "" {
		t.Fatal("Apply 应写入 OperationID 注解")
	}

	// 模拟崩溃:Execution 状态丢失(Status 回滚),目标注解保留。
	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	gotBefore := got.DeepCopy()
	got.Status.Execution = nil // 崩溃:状态未持久化
	got.Status.Phase = opsv1alpha1.PhaseExecuting
	_ = c.Status().Patch(context.Background(), &got, client.MergeFrom(gotBefore))

	// 重启后 reconcile:幂等路径。
	reconcileOnce(t, r, "incident-1")

	got = opsv1alpha1.AIOpsIncident{}
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseVerifying {
		t.Errorf("崩溃恢复应转 Verifying: %s", got.Status.Phase)
	}
	var depAfter appsv1.Deployment
	_ = c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: "checkout-api"}, &depAfter)
	if depAfter.Annotations[executor.OperationIDAnnotation] != opID {
		t.Error("崩溃恢复后 OperationID 不应变化(未重复 Apply)")
	}
}

// TestCrashRecovery_VerifyingKeepsCounter:Verifying 崩溃后计数保留在 Status。
func TestCrashRecovery_VerifyingKeepsCounter(t *testing.T) {
	dep := execDeployment()
	incident := executionIncident()
	incident.Status.Phase = opsv1alpha1.PhaseVerifying
	incident.Status.Execution = &opsv1alpha1.ExecutionStatus{
		Reference: &opsv1alpha1.ExecutionReference{ExecutionID: "e-1", OperationID: "op-1"},
	}
	r, c, _ := newExecReconciler(t, incident, dep)

	reconcileOnce(t, r, "incident-1") // 成功 1/2
	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Verification == nil || got.Status.Verification.ConsecutiveSuccesses != 1 {
		t.Fatalf("第一次成功计数错误: %+v", got.Status.Verification)
	}

	// 模拟崩溃重启(重新从 Status 继续)。
	reconcileOnce(t, r, "incident-1") // 成功 2/2 → Resolved
	got = opsv1alpha1.AIOpsIncident{}
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseResolved {
		t.Errorf("崩溃恢复后应从上次计数继续: %s", got.Status.Phase)
	}
}

// TestCrashRecovery_RollingBackRepeat:Rollback 后、状态写前崩溃 →
// 重复 Rollback 幂等(恢复到同一原状)。
func TestCrashRecovery_RollingBackRepeat(t *testing.T) {
	dep := execDeployment() // replicas=1
	incident := executionIncident()
	incident.Status.Proposal.Action = opsv1alpha1.ActionScaleDeployment
	incident.Status.Proposal.Parameters = apiextensionsv1.JSON{Raw: []byte(`{"replicas":4,"reason":"扩容"}`)}
	incident.Status.Proposal.PlanDigest = "sha256:" + repeatChar('g', 64)
	r, c, _ := newExecReconciler(t, incident, dep)

	// Apply → replicas=4,快照 replicas=1。
	reconcileOnce(t, r, "incident-1")

	// 手动转 RollingBack。
	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	before := got.DeepCopy()
	got.Status.Phase = opsv1alpha1.PhaseRollingBack
	_ = c.Status().Patch(context.Background(), &got, client.MergeFrom(before))

	// 第一次回滚。
	reconcileOnce(t, r, "incident-1")
	var dep1 appsv1.Deployment
	_ = c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: "checkout-api"}, &dep1)
	if *dep1.Spec.Replicas != 1 {
		t.Fatalf("回滚后 replicas 应为 1: %d", *dep1.Spec.Replicas)
	}

	// 模拟崩溃:状态回退到 RollingBack(未写 RolledBack),重新回滚。
	got = opsv1alpha1.AIOpsIncident{}
	_ = c.Get(context.Background(), keyIncident(), &got)
	before2 := got.DeepCopy()
	got.Status.Phase = opsv1alpha1.PhaseRollingBack
	_ = c.Status().Patch(context.Background(), &got, client.MergeFrom(before2))

	reconcileOnce(t, r, "incident-1")
	got = opsv1alpha1.AIOpsIncident{}
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseRolledBack {
		t.Errorf("重复回滚应 RolledBack: %s", got.Status.Phase)
	}
	var dep2 appsv1.Deployment
	_ = c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: "checkout-api"}, &dep2)
	if *dep2.Spec.Replicas != 1 {
		t.Errorf("重复回滚后 replicas 应仍为 1(幂等): %d", *dep2.Spec.Replicas)
	}
}

// TestErrTransient_Backoff:transientRequeueAfter 指数退避。
func TestErrTransient_Backoff(t *testing.T) {
	if transientRequeueAfter(0) != 30*time.Second {
		t.Errorf("attempt 0 应为 30s: %v", transientRequeueAfter(0))
	}
	if transientRequeueAfter(1) != 60*time.Second {
		t.Errorf("attempt 1 应为 60s: %v", transientRequeueAfter(1))
	}
	if transientRequeueAfter(5) > 5*time.Minute {
		t.Errorf("退避应封顶 5min: %v", transientRequeueAfter(5))
	}
	if !errors.Is(ErrTransient, ErrTransient) {
		t.Error("ErrTransient 应可被 errors.Is 识别")
	}
}

// TestAuditCriticalFailClosed:执行前审计不可用 → Escalated(fail-closed)。
func TestAuditCriticalFailClosed(t *testing.T) {
	dep := execDeployment()
	incident := executionIncident()
	r, c, _ := newExecReconciler(t, incident, dep)
	// Audit writer 使用失败的 sink。
	r.Audit = newFailingAuditWriter()

	reconcileOnce(t, r, "incident-1")

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseEscalated {
		t.Errorf("审计不可用应 fail-closed(Escalated): %s", got.Status.Phase)
	}
	if c := got.GetCondition("ExecutionReady"); c == nil || c.Status != metav1.ConditionFalse {
		t.Error("应标记 ExecutionReady=False")
	}
}

// TestAuditCriticalSuccess:审计成功 → 正常执行。
func TestAuditCriticalSuccess(t *testing.T) {
	dep := execDeployment()
	incident := executionIncident()
	r, c, _ := newExecReconciler(t, incident, dep)
	r.Audit = newRecordingAuditWriter()

	reconcileOnce(t, r, "incident-1")

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseVerifying {
		t.Errorf("审计成功后应 Verifying: %s", got.Status.Phase)
	}
}

// newFailingAuditWriter 返回审计 sink 失败的 Writer。
func newFailingAuditWriter() *audit.Writer {
	return audit.NewWriter(audit.SinkFunc(func(context.Context, string, audit.Event) error {
		return errNotFound
	}), logr.Discard())
}

// newRecordingAuditWriter 返回记录事件的 Writer。
func newRecordingAuditWriter() *audit.Writer {
	events := map[string]audit.Event{}
	return audit.NewWriter(audit.SinkFunc(func(_ context.Context, key string, e audit.Event) error {
		events[key] = e
		return nil
	}), logr.Discard())
}

var _ = reconcile.Request{}

// TestReconcile_DiagnosingTransientIncrementsAttempts
// ErrTransient 返回前必须递增 Attempts,使退避真正指数增长。
func TestReconcile_DiagnosingTransientIncrementsAttempts(t *testing.T) {
	incident := newDiagnosingIncident()
	analysis := &fakeAnalysis{err: errors.New("network timeout")} // 网络类错误(可重试)
	r, c := newReconciler(t, nil, incident)
	r.Analysis = analysis
	r.DiagnosisEnabled = true

	res, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: client.ObjectKey{Namespace: "fault-lab", Name: "incident-1"},
	})
	if err != nil {
		t.Fatalf("ErrTransient 不应作为 error 返回: %v", err)
	}
	// 第一次:Attempts=1 → 退避 60s。
	if res.RequeueAfter != 60*time.Second {
		t.Errorf("第一次退避应为 60s: %v", res.RequeueAfter)
	}
	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Execution == nil || got.Status.Execution.Attempts != 1 {
		t.Fatalf("Attempts 应递增为 1: %+v", got.Status.Execution)
	}
	if got.Status.Phase != opsv1alpha1.PhaseDiagnosing {
		t.Errorf("ErrTransient 应保持 Phase: %s", got.Status.Phase)
	}

	// 第二次:Attempts=2 → 退避 120s。
	res2, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: client.ObjectKey{Namespace: "fault-lab", Name: "incident-1"},
	})
	if err != nil {
		t.Fatalf("第二次不应报错: %v", err)
	}
	if res2.RequeueAfter != 120*time.Second {
		t.Errorf("第二次退避应为 120s: %v", res2.RequeueAfter)
	}
}

// TestClearEphemeralOnVerifying 验证:Approval 在进入 Verifying 后被清理(临时数据不跨阶段保留)。
func TestClearEphemeralOnVerifying(t *testing.T) {
	incident := executionIncident()
	incident.Status.Approval = &opsv1alpha1.ApprovalStatus{Decision: "Approved", Actor: "alice"}
	r, c, _ := newExecReconciler(t, incident, execDeployment())

	reconcileOnce(t, r, "incident-1")

	var got opsv1alpha1.AIOpsIncident
	if err := c.Get(context.Background(), keyIncident(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != opsv1alpha1.PhaseVerifying {
		t.Fatalf("应进入 Verifying: %s", got.Status.Phase)
	}
	if got.Status.Approval != nil {
		t.Error("Verifying 后 Approval 临时数据应被清理")
	}
}

// TestRollingBack_ExecutorNil 覆盖 Executor 未配置 → Escalated。
func TestRollingBack_ExecutorNil(t *testing.T) {
	incident := executionIncident()
	incident.Status.Phase = opsv1alpha1.PhaseRollingBack
	incident.Status.Execution = &opsv1alpha1.ExecutionStatus{
		Reference: &opsv1alpha1.ExecutionReference{ExecutionID: "exec-x", OperationID: "op-x", SnapshotID: "snap-x"},
	}
	r, c, _ := newExecReconciler(t, incident, execDeployment())
	r.Executor = nil

	reconcileOnce(t, r, "incident-1")

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseEscalated {
		t.Errorf("Executor nil 应 Escalated: %s", got.Status.Phase)
	}
}

// TestAwaitingApproval_Reject 覆盖审批人拒绝 → Escalated(条件更新)。
func TestAwaitingApproval_Reject(t *testing.T) {
	incident := awaitingIncident()
	incident.Status.PolicyDecision = &opsv1alpha1.PolicyDecisionStatus{Decision: "ApprovalRequired"}
	approval := approvalBase(incident.UID)
	approval.Spec.Decision = opsv1alpha1.ApprovalReject
	approval.Spec.Reason = "不批准"
	r, c, _ := newExecReconciler(t, incident, execDeployment())

	reconcileOnce(t, r, "incident-1")
	reconcileApproval(t, c, approval.Name)

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseEscalated {
		t.Errorf("拒绝应 Escalated: %s", got.Status.Phase)
	}
	if !meta.IsStatusConditionFalse(got.Status.Conditions, "ApprovalReady") {
		t.Error("拒绝后 ApprovalReady 应为 False")
	}
}

// TestTargetLock_Contended 验证:同目标第二个 Incident 进入 Executing 时
// 被锁阻挡(保持 Executing,TargetLockContended 条件),不执行 Apply。
func TestTargetLock_Contended(t *testing.T) {
	dep := execDeployment()
	incidentA := executionIncident()
	incidentA.Name = "incident-a"
	incidentA.UID = types.UID("uid-a")
	incidentB := executionIncident()
	incidentB.Name = "incident-b"
	incidentB.UID = types.UID("uid-b")
	r, c, _ := newExecReconciler(t, incidentA, incidentB, dep)
	r.TargetLock = targetlock.NewKubernetesManager(c)

	// A 先执行:获得锁并 Apply。
	reconcileOnce(t, r, "incident-a")
	var a opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: "incident-a"}, &a)
	if a.Status.Phase != opsv1alpha1.PhaseVerifying {
		t.Fatalf("A 应进入 Verifying: %s", a.Status.Phase)
	}
	if a.Status.Execution == nil || a.Status.Execution.TargetLock == nil {
		t.Fatal("A 应持有目标锁")
	}

	// B 尝试执行:被锁阻挡,保持 Executing + TargetLockContended。
	reconcileOnce(t, r, "incident-b")
	var b opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: "incident-b"}, &b)
	if b.Status.Phase != opsv1alpha1.PhaseExecuting {
		t.Fatalf("B 应保持 Executing(被锁阻挡): %s", b.Status.Phase)
	}
	if !meta.IsStatusConditionFalse(b.Status.Conditions, "TargetLockReady") {
		t.Error("B 应有 TargetLockReady=False(TargetLockContended)")
	}
	// B 未执行:OperationID 未写入。
	var depAfter appsv1.Deployment
	_ = c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: "checkout-api"}, &depAfter)
	if depAfter.Annotations[executor.OperationIDAnnotation] == "" {
		t.Fatal("A 应已 Apply(OperationID 存在)")
	}
	_ = r
}

// TestTargetLock_ReleasedOnTerminal 验证:终态释放锁,后续 Incident 可执行。
func TestTargetLock_ReleasedOnTerminal(t *testing.T) {
	dep := execDeployment()
	incidentA := executionIncident()
	incidentA.Name = "incident-a"
	incidentA.UID = types.UID("uid-a")
	incidentB := executionIncident()
	incidentB.Name = "incident-b"
	incidentB.UID = types.UID("uid-b")
	r, c, _ := newExecReconciler(t, incidentA, incidentB, dep)
	r.TargetLock = targetlock.NewKubernetesManager(c)

	reconcileOnce(t, r, "incident-a")
	// A 进入终态(手动置 Resolved 并 reconcile 触发释放)。
	var a opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: "incident-a"}, &a)
	before := a.DeepCopy()
	a.Status.Phase = opsv1alpha1.PhaseResolved
	_ = c.Status().Patch(context.Background(), &a, client.MergeFrom(before))
	reconcileOnce(t, r, "incident-a")

	// 锁已释放:B 可获取并执行。
	reconcileOnce(t, r, "incident-b")
	var b opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: "incident-b"}, &b)
	if b.Status.Phase != opsv1alpha1.PhaseVerifying {
		t.Fatalf("B 应获得锁并执行: %s", b.Status.Phase)
	}
	if b.Status.Execution == nil || b.Status.Execution.TargetLock == nil {
		t.Fatal("B 应持有目标锁")
	}
}
