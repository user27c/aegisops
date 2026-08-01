package controller

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

func newFakeStatusClient(t *testing.T) (client.Client, *opsv1alpha1.AIOpsIncident) {
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
		Build()
	i := &opsv1alpha1.AIOpsIncident{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "fault-lab"},
	}
	if err := c.Create(context.Background(), i); err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	return c, i
}

func TestPatchStatus_SkipNoChange(t *testing.T) {
	c, i := newFakeStatusClient(t)
	before := i.DeepCopy()
	after := i.DeepCopy()
	if err := PatchStatus(context.Background(), c, before, after); err != nil {
		t.Fatalf("PatchStatus 失败: %v", err)
	}
	// 无变化时不应产生任何写操作（fake client 也验证无错误）。
}

func TestPatchStatus_WritesPhase(t *testing.T) {
	c, i := newFakeStatusClient(t)
	before := i.DeepCopy()
	i.Status.Phase = opsv1alpha1.PhaseDetected
	if err := PatchStatus(context.Background(), c, before, i); err != nil {
		t.Fatalf("PatchStatus 失败: %v", err)
	}

	var got opsv1alpha1.AIOpsIncident
	if err := c.Get(context.Background(), client.ObjectKey{Name: "test", Namespace: "fault-lab"}, &got); err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.PhaseDetected {
		t.Errorf("Phase 未写入: %s", got.Status.Phase)
	}
}

func TestPatchStatus_ConflictRetry(t *testing.T) {
	// 模拟并发修改：先外部更新 resourceVersion，再 PatchStatus 应产生 conflict。
	// fake client 不做 CAS，这里验证 PatchStatus 本身不 panic 且能成功。
	c, i := newFakeStatusClient(t)

	var other opsv1alpha1.AIOpsIncident
	if err := c.Get(context.Background(), client.ObjectKey{Name: "test", Namespace: "fault-lab"}, &other); err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	other.Spec.Severity = "critical"
	if err := c.Update(context.Background(), &other); err != nil {
		t.Fatalf("Update 失败: %v", err)
	}

	before := i.DeepCopy()
	i.Status.Phase = opsv1alpha1.PhaseDetected
	if err := PatchStatus(context.Background(), c, before, i); err != nil {
		t.Fatalf("PatchStatus 冲突后应成功: %v", err)
	}
}

func TestSetCondition_PreservesTransitionTime(t *testing.T) {
	i := newTestIncident(opsv1alpha1.PhaseDetected)
	SetCondition(i, "Ready", metav1.ConditionTrue, "Test", "第一次")
	first := i.GetCondition("Ready")
	if first == nil || first.Status != metav1.ConditionTrue {
		t.Fatal("条件未设置")
	}
	firstTime := first.LastTransitionTime

	SetCondition(i, "Ready", metav1.ConditionTrue, "Test", "第二次")
	second := i.GetCondition("Ready")
	if !second.LastTransitionTime.Equal(&firstTime) {
		t.Error("状态未变化时不应更新 LastTransitionTime")
	}

	SetCondition(i, "Ready", metav1.ConditionFalse, "Test", "失败")
	third := i.GetCondition("Ready")
	if third.LastTransitionTime.Equal(&firstTime) {
		t.Error("状态变化时应更新 LastTransitionTime")
	}
}

func TestSetCondition_TruncatesMessage(t *testing.T) {
	i := newTestIncident(opsv1alpha1.PhaseDetected)
	longMsg := strings.Repeat("密", 2000)
	SetCondition(i, "Ready", metav1.ConditionTrue, "Test", longMsg)
	cond := i.GetCondition("Ready")
	if len(cond.Message) > maxStatusMessageBytes {
		t.Errorf("message 未截断: %d", len(cond.Message))
	}
}

func TestClearPhaseEphemeralStatus(t *testing.T) {
	i := newTestIncident(opsv1alpha1.PhaseExecuting)
	i.Status.Approval = &opsv1alpha1.ApprovalStatus{Actor: "x"}
	i.Status.Execution = &opsv1alpha1.ExecutionStatus{LastError: "boom"}

	ClearPhaseEphemeralStatus(i, opsv1alpha1.PhaseVerifying)
	if i.Status.Approval != nil {
		t.Error("进入 Verifying 应清理 Approval")
	}
	if i.Status.Execution != nil && i.Status.Execution.LastError != "" {
		t.Error("进入 Verifying 应清理执行错误细节")
	}
}
