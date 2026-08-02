package controller

import (
	"context"
	"fmt"
	"reflect"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// ApprovalReconciler 校验 RemediationApproval 的合法性并写状态条件。
// 不做执行决策（执行由 Incident 控制器在 AwaitingApproval 阶段完成）。
type ApprovalReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Clock  clock.Clock
}

// +kubebuilder:rbac:groups=ops.aegis.io,resources=remediationapprovals,verbs=get;list;watch
// +kubebuilder:rbac:groups=ops.aegis.io,resources=remediationapprovals/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ops.aegis.io,resources=aiopsincidents,verbs=get;list;watch

// Reconcile 校验审批并写 Valid/Processed 条件。
func (r *ApprovalReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	approval := &opsv1alpha1.RemediationApproval{}
	if err := r.Get(ctx, req.NamespacedName, approval); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	now := r.Clock.Now()
	before := approval.DeepCopy()

	// 1. 引用格式校验。
	if err := r.validateApproval(ctx, approval, now); err != nil {
		setApprovalCondition(approval, "Valid", metav1.ConditionFalse, "InvalidApproval", truncateMessage(err.Error()))
	} else {
		setApprovalCondition(approval, "Valid", metav1.ConditionTrue, "Validated", "审批引用合法")
	}
	// Processed 只标记"已被 Incident 消费"由 Incident 控制器设置，这里不做。
	// 过期审批标记 Expired。
	if !now.Before(approval.Spec.ExpiresAt.Time) {
		setApprovalCondition(approval, "Valid", metav1.ConditionFalse, "Expired", "审批已过期")
	}

	if err := patchApprovalStatus(ctx, r.Client, before, approval); err != nil {
		return ctrl.Result{}, fmt.Errorf("写审批状态: %w", err)
	}
	return ctrl.Result{}, nil
}

// patchApprovalStatus 用 MergeFrom 补丁写审批 Status（与 PatchStatus 语义一致）。
func patchApprovalStatus(ctx context.Context, c client.StatusClient, before, after *opsv1alpha1.RemediationApproval) error {
	if reflect.DeepEqual(before.Status, after.Status) {
		return nil
	}
	return c.Status().Patch(ctx, after, client.MergeFrom(before))
}

// validateApproval 校验绑定关系：
// Incident 存在、UID 匹配、proposalRevision 与 planDigest 匹配 Incident 当前方案。
func (r *ApprovalReconciler) validateApproval(ctx context.Context, approval *opsv1alpha1.RemediationApproval, now time.Time) error {
	ref := approval.Spec.IncidentRef

	incident := &opsv1alpha1.AIOpsIncident{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: approval.Namespace, Name: ref.Name}, incident); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("绑定的 incident %s 不存在", ref.Name)
		}
		return fmt.Errorf("读取 Incident: %w", err)
	}
	if incident.UID != ref.UID {
		return fmt.Errorf("incident UID 不匹配: 期望 %s，实际 %s", ref.UID, incident.UID)
	}
	if incident.Status.Phase != opsv1alpha1.PhaseAwaitingApproval {
		return fmt.Errorf("incident 不在 AwaitingApproval 阶段（当前 %s）", incident.Status.Phase)
	}
	if incident.Status.Proposal == nil {
		return fmt.Errorf("incident 没有方案")
	}
	if incident.Status.Proposal.Revision != ref.ProposalRevision {
		return fmt.Errorf("方案版本不匹配: 期望 %d，当前 %d", ref.ProposalRevision, incident.Status.Proposal.Revision)
	}
	if incident.Status.Proposal.PlanDigest != "" && approval.Spec.PlanDigest != incident.Status.Proposal.PlanDigest {
		return fmt.Errorf("planDigest 不匹配")
	}
	if !now.Before(approval.Spec.ExpiresAt.Time) {
		return fmt.Errorf("审批已过期")
	}
	return nil
}

// setApprovalCondition 设置或更新审批条件（LastTransitionTime 保持语义同 SetCondition）。
func setApprovalCondition(a *opsv1alpha1.RemediationApproval, typ string, status metav1.ConditionStatus, reason, msg string) {
	cond := metav1.Condition{
		Type:               typ,
		Status:             status,
		Reason:             reason,
		Message:            truncateMessage(msg),
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: a.Generation,
	}
	for idx := range a.Status.Conditions {
		if a.Status.Conditions[idx].Type == typ {
			if a.Status.Conditions[idx].Status == status {
				cond.LastTransitionTime = a.Status.Conditions[idx].LastTransitionTime
			}
			a.Status.Conditions[idx] = cond
			return
		}
	}
	a.Status.Conditions = append(a.Status.Conditions, cond)
}

// SetupWithManager 注册审批控制器。
func (r *ApprovalReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&opsv1alpha1.RemediationApproval{}).
		Complete(r)
}
