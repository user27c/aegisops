package controller

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

func approvalBase(uid types.UID) *opsv1alpha1.RemediationApproval {
	return &opsv1alpha1.RemediationApproval{
		ObjectMeta: metav1.ObjectMeta{Name: "inc-1-approval", Namespace: "fault-lab"},
		Spec: opsv1alpha1.RemediationApprovalSpec{
			IncidentRef: opsv1alpha1.IncidentReference{Name: "incident-1", UID: uid, ProposalRevision: 1},
			Decision:    opsv1alpha1.ApprovalApprove,
			PlanDigest:  "sha256:" + repeatChar('a', 64),
			Actor:       "console-approver",
			Reason:      "确认",
			ExpiresAt:   metav1.NewTime(time.Now().Add(10 * time.Minute)),
		},
	}
}

func awaitingIncident() *opsv1alpha1.AIOpsIncident {
	i := firingIncident()
	i.UID = types.UID("uid-1")
	i.Finalizers = []string{FinalizerName}
	i.Status.Phase = opsv1alpha1.PhaseAwaitingApproval
	i.Status.Proposal = &opsv1alpha1.ActionProposal{
		Revision:   1,
		Action:     opsv1alpha1.ActionRestartWorkload,
		PlanDigest: "sha256:" + repeatChar('a', 64),
	}
	return i
}

func reconcileApproval(t *testing.T, c client.Client, name string) {
	t.Helper()
	r := &ApprovalReconciler{Client: c, Clock: &testClock{now: time.Now()}}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: client.ObjectKey{Namespace: "fault-lab", Name: name},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
}

func TestApprovalReconciler_Valid(t *testing.T) {
	incident := awaitingIncident()
	approval := approvalBase(incident.UID)
	c := newFakeClientWith(t, incident, approval)
	reconcileApproval(t, c, "inc-1-approval")

	var got opsv1alpha1.RemediationApproval
	_ = c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: "inc-1-approval"}, &got)
	if cond := got.GetCondition("Valid"); cond == nil || cond.Status != metav1.ConditionTrue {
		t.Errorf("应标记 Valid: %+v", got.Status.Conditions)
	}
}

func TestApprovalReconciler_IncidentNotFound(t *testing.T) {
	approval := approvalBase(types.UID("uid-1"))
	c := newFakeClientWith(t, approval)
	reconcileApproval(t, c, "inc-1-approval")

	var got opsv1alpha1.RemediationApproval
	_ = c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: "inc-1-approval"}, &got)
	if cond := got.GetCondition("Valid"); cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "InvalidApproval" {
		t.Errorf("应 InvalidApproval: %+v", got.Status.Conditions)
	}
}

func TestApprovalReconciler_UIDMismatch(t *testing.T) {
	incident := awaitingIncident()
	approval := approvalBase(types.UID("other-uid")) // 绑定错误 UID
	c := newFakeClientWith(t, incident, approval)
	reconcileApproval(t, c, "inc-1-approval")

	var got opsv1alpha1.RemediationApproval
	_ = c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: "inc-1-approval"}, &got)
	if cond := got.GetCondition("Valid"); cond == nil || cond.Status != metav1.ConditionFalse {
		t.Errorf("UID 不匹配应 Invalid: %+v", got.Status.Conditions)
	}
}

func TestApprovalReconciler_RevisionMismatch(t *testing.T) {
	incident := awaitingIncident()
	approval := approvalBase(incident.UID)
	approval.Spec.IncidentRef.ProposalRevision = 5 // 与 proposal.Revision=1 不匹配
	c := newFakeClientWith(t, incident, approval)
	reconcileApproval(t, c, "inc-1-approval")

	var got opsv1alpha1.RemediationApproval
	_ = c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: "inc-1-approval"}, &got)
	if cond := got.GetCondition("Valid"); cond == nil || cond.Status != metav1.ConditionFalse {
		t.Errorf("版本不匹配应 Invalid: %+v", got.Status.Conditions)
	}
}

func TestApprovalReconciler_DigestMismatch(t *testing.T) {
	incident := awaitingIncident()
	approval := approvalBase(incident.UID)
	approval.Spec.PlanDigest = "sha256:" + repeatChar('b', 64) // 与 incident 方案摘要不符
	c := newFakeClientWith(t, incident, approval)
	reconcileApproval(t, c, "inc-1-approval")

	var got opsv1alpha1.RemediationApproval
	_ = c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: "inc-1-approval"}, &got)
	if cond := got.GetCondition("Valid"); cond == nil || cond.Status != metav1.ConditionFalse {
		t.Errorf("摘要不匹配应 Invalid: %+v", got.Status.Conditions)
	}
}

func TestApprovalReconciler_Expired(t *testing.T) {
	incident := awaitingIncident()
	approval := approvalBase(incident.UID)
	approval.Spec.ExpiresAt = metav1.NewTime(time.Now().Add(-time.Hour)) // 已过期
	c := newFakeClientWith(t, incident, approval)
	reconcileApproval(t, c, "inc-1-approval")

	var got opsv1alpha1.RemediationApproval
	_ = c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: "inc-1-approval"}, &got)
	if cond := got.GetCondition("Valid"); cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "Expired" {
		t.Errorf("过期应标记 Expired: %+v", got.Status.Conditions)
	}
}

func TestApprovalReconciler_NotFound(t *testing.T) {
	c := newFakeClientWith(t)
	r := &ApprovalReconciler{Client: c, Clock: &testClock{now: time.Now()}}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: client.ObjectKey{Namespace: "fault-lab", Name: "missing"},
	})
	if err != nil {
		t.Fatalf("NotFound 不应报错: %v", err)
	}
}

// newFakeClientWith 构造带 status subresource 的 fake client。
func newFakeClientWith(t *testing.T, objs ...client.Object) client.Client {
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

func TestApprovalReconciler_SetupWithManager(t *testing.T) {
	// SetupWithManager 需要真实 manager；这里只验证方法存在且不 panic（nil mgr 应报错）。
	r := &ApprovalReconciler{}
	if err := r.SetupWithManager(nil); err == nil {
		t.Error("nil manager 应报错")
	}
}
