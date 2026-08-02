package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	"github.com/user27c/aegisops/internal/policy"
)

// handlePolicyChecking：解析策略并评估方案。
// Auto → Executing；ApprovalRequired → AwaitingApproval；
// SuggestOnly → 保持并记录建议；Deny → Escalated。
func (r *IncidentReconciler) handlePolicyChecking(ctx context.Context, i *opsv1alpha1.AIOpsIncident) (ctrl.Result, error) {
	now := r.Clock.Now()

	if i.Status.Proposal == nil {
		SetCondition(i, "PolicyChecked", metav1.ConditionFalse, "NoProposal", "诊断未给出方案")
		if err := Terminalize(i, opsv1alpha1.PhaseRecoveredNoAction, "无方案可执行", now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	targetInfo, err := r.buildTargetInfo(ctx, i)
	if err != nil {
		// 目标缺失/不可读 → fail closed。
		SetCondition(i, "PolicyChecked", metav1.ConditionFalse, "TargetUnavailable", truncateMessage(err.Error()))
		if err := Terminalize(i, opsv1alpha1.PhaseEscalated, "目标不可用: "+err.Error(), now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	resolvedPolicy, err := r.resolvePolicy(ctx, i)
	if err != nil {
		// POLICY_AMBIGUOUS 等 → fail closed。
		SetCondition(i, "PolicyChecked", metav1.ConditionFalse, "PolicyResolveFailed", truncateMessage(err.Error()))
		if err := Terminalize(i, opsv1alpha1.PhaseEscalated, "策略解析失败: "+err.Error(), now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// 计算方案摘要（绑定当前目标版本与策略版本）。
	digest, digestErr := buildDigestFor(i, resolvedPolicy, targetInfo)
	if digestErr == nil && digest != "" {
		i.Status.Proposal.PlanDigest = digest
	}

	decision, err := (&policy.DefaultEvaluator{}).Evaluate(ctx, policy.EvaluationInput{
		Incident:           i,
		Proposal:           *i.Status.Proposal,
		Policy:             resolvedPolicy,
		Target:             targetInfo,
		Now:                now,
		AuditAvailable:     true, // M6 接审计探活
		EvidenceSufficient: i.Status.Evidence != nil && i.Status.Diagnosis != nil,
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("策略评估失败: %w", err)
	}

	writePolicyDecision(i, decision, resolvedPolicy)

	switch decision.Type {
	case policy.DecisionAuto:
		if err := Transition(i, opsv1alpha1.PhaseExecuting, "PolicyApproved", "低风险自动放行", now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	case policy.DecisionApprovalRequired:
		if err := Transition(i, opsv1alpha1.PhaseAwaitingApproval, "ApprovalRequired", "等待人工审批", now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	case policy.DecisionSuggestOnly:
		// 只给建议不执行：保持阶段并记录。
		SetCondition(i, "PolicyChecked", metav1.ConditionTrue, "SuggestOnly", "策略仅建议，不自动执行")
		return ctrl.Result{RequeueAfter: r.stuckInterval()}, nil
	case policy.DecisionDeny:
		SetCondition(i, "PolicyChecked", metav1.ConditionFalse, "PolicyDenied", decision.Reasons[0].Message)
		if err := Terminalize(i, opsv1alpha1.PhaseEscalated, "策略拒绝: "+decision.Reasons[0].Code, now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	default:
		return ctrl.Result{}, fmt.Errorf("未知决策 %q", decision.Type)
	}
}

// handleAwaitingApproval：轮询审批 CR；有效审批 → Executing；拒绝 → Escalated。
func (r *IncidentReconciler) handleAwaitingApproval(ctx context.Context, i *opsv1alpha1.AIOpsIncident) (ctrl.Result, error) {
	now := r.Clock.Now()

	if i.Status.Proposal == nil || i.Status.Proposal.PlanDigest == "" {
		SetCondition(i, "ApprovalReady", metav1.ConditionFalse, "NoPlanDigest", "方案缺少摘要")
		if err := Terminalize(i, opsv1alpha1.PhaseEscalated, "方案摘要缺失", now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	targetInfo, err := r.buildTargetInfo(ctx, i)
	if err != nil {
		return ctrl.Result{}, err
	}
	resolvedPolicy, err := r.resolvePolicy(ctx, i)
	if err != nil {
		SetCondition(i, "ApprovalReady", metav1.ConditionFalse, "PolicyResolveFailed", truncateMessage(err.Error()))
		if err := Terminalize(i, opsv1alpha1.PhaseEscalated, "策略解析失败: "+err.Error(), now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	approval, err := r.findApproval(ctx, i)
	if err != nil {
		return ctrl.Result{}, err
	}
	if approval == nil {
		// 尚无审批：保持等待。
		SetCondition(i, "ApprovalReady", metav1.ConditionFalse, "AwaitingDecision", "等待审批人操作")
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	decision, err := (&policy.DefaultEvaluator{}).Evaluate(ctx, policy.EvaluationInput{
		Incident:           i,
		Proposal:           *i.Status.Proposal,
		Policy:             resolvedPolicy,
		Approval:           approval,
		Target:             targetInfo,
		Now:                now,
		AuditAvailable:     true,
		EvidenceSufficient: true,
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("审批评估失败: %w", err)
	}

	writePolicyDecision(i, decision, resolvedPolicy)

	switch {
	case decision.Denied():
		// 审批人明确拒绝 → Escalated；无效审批（摘要不匹配/过期）→ 保持等待新审批。
		if approval.Spec.Decision == opsv1alpha1.ApprovalReject {
			i.Status.Approval = &opsv1alpha1.ApprovalStatus{
				Decision:     string(approval.Spec.Decision),
				ApprovalName: approval.Name,
				Actor:        approval.Spec.Actor,
				Reason:       approval.Spec.Reason,
				DecidedAt:    &metav1.Time{Time: now},
			}
			reason := "审批人拒绝: " + approval.Spec.Reason
			// 条件随终态更新，避免残留旧 ApprovalInvalid。
			SetCondition(i, "ApprovalReady", metav1.ConditionFalse, "Rejected", reason)
			if err := Terminalize(i, opsv1alpha1.PhaseEscalated, reason, now); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		// 摘要不匹配/过期/绑定错误：不执行，等待新的有效审批。
		//
		// 恢复路径：审批等待期目标 RV 变化会使摘要永久不匹配（TOCTOU 防护，
		// fail-closed ✓）。这里用当前目标状态刷新 Proposal.PlanDigest，
		// 旧审批（绑定旧摘要）自动失效，审批人基于新摘要重新审批。
		// 刷新后摘要基于当前 RV，下次 reconcile 不会再进入刷新分支（防循环）。
		if decision.Reasons[0].Code == policy.ReasonApprovalMismatch ||
			decision.Reasons[0].Code == policy.ReasonApprovalExpired {
			if refreshed, ok := r.refreshPlanDigest(ctx, i, resolvedPolicy, targetInfo); ok {
				SetCondition(i, "ApprovalReady", metav1.ConditionFalse, "ProposalRefreshed",
					"目标已变化，方案摘要已刷新，请重新审批")
				_ = refreshed
				return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
			}
		}
		SetCondition(i, "ApprovalReady", metav1.ConditionFalse, "ApprovalInvalid", decision.Reasons[0].Message)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	case decision.Approved():
		i.Status.Approval = &opsv1alpha1.ApprovalStatus{
			Decision:     string(approval.Spec.Decision),
			ApprovalName: approval.Name,
			Actor:        approval.Spec.Actor,
			Reason:       approval.Spec.Reason,
			ExpiresAt:    &approval.Spec.ExpiresAt,
			DecidedAt:    &metav1.Time{Time: now},
		}
		// 条件随阶段推进更新，避免终态残留旧 ApprovalInvalid。
		SetCondition(i, "ApprovalReady", metav1.ConditionTrue, "ApprovalGranted", "审批通过")
		if err := Transition(i, opsv1alpha1.PhaseExecuting, "ApprovalGranted", "审批通过，开始执行", now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	default:
		// ApprovalRequired 但审批无效（过期/不匹配）→ 继续等新审批。
		SetCondition(i, "ApprovalReady", metav1.ConditionFalse, "ApprovalInvalid", decision.Reasons[0].Message)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}
}

// buildTargetInfo 读取目标 Deployment 的当前状态。
func (r *IncidentReconciler) buildTargetInfo(ctx context.Context, i *opsv1alpha1.AIOpsIncident) (policy.ObjectInfo, error) {
	var dep appsv1.Deployment
	err := r.Get(ctx, client.ObjectKey{Namespace: i.Spec.TargetRef.Namespace, Name: i.Spec.TargetRef.Name}, &dep)
	if apierrors.IsNotFound(err) {
		return policy.ObjectInfo{}, fmt.Errorf("目标 Deployment 不存在")
	}
	if err != nil {
		return policy.ObjectInfo{}, err
	}
	info := policy.ObjectInfo{
		UID:             dep.UID,
		ResourceVersion: dep.ResourceVersion,
		Generation:      dep.Generation,
		Replicas:        derefReplicas(dep.Spec.Replicas),
		Revision:        latestRevisionFromDeployment(&dep),
	}
	if ts := dep.Annotations["ops.aegis.io/last-action-at"]; ts != "" {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			info.LastActionAt = &t
		}
	}
	return info, nil
}

// resolvePolicy 解析命中的策略（读取 namespace 标签用于 selector 匹配）。
func (r *IncidentReconciler) resolvePolicy(ctx context.Context, i *opsv1alpha1.AIOpsIncident) (*opsv1alpha1.RemediationPolicy, error) {
	ns := &corev1.Namespace{}
	if err := r.Get(ctx, client.ObjectKey{Name: i.Namespace}, ns); err != nil {
		return nil, fmt.Errorf("读取 namespace: %w", err)
	}
	// 目标工作负载标签（Deployment 的 labels）。
	var dep appsv1.Deployment
	workloadLabels := map[string]string{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: i.Spec.TargetRef.Namespace, Name: i.Spec.TargetRef.Name}, &dep); err == nil {
		workloadLabels = dep.Labels
	}

	var list opsv1alpha1.RemediationPolicyList
	if err := r.List(ctx, &list, client.InNamespace(i.Namespace)); err != nil {
		return nil, fmt.Errorf("列出策略: %w", err)
	}
	candidates := make([]opsv1alpha1.RemediationPolicy, 0, len(list.Items))
	for idx := range list.Items {
		p := &list.Items[idx]
		ok, err := policy.Matches(p, ns.Labels, workloadLabels, i.Spec.TargetRef.Kind)
		if err != nil {
			return nil, err
		}
		if ok {
			candidates = append(candidates, *p)
		}
	}
	selected, err := policy.SelectHighestPriority(candidates)
	if err != nil {
		return nil, err
	}
	return selected, nil
}

// findApproval 查找该 Incident 的审批 CR。
func (r *IncidentReconciler) findApproval(ctx context.Context, i *opsv1alpha1.AIOpsIncident) (*opsv1alpha1.RemediationApproval, error) {
	var list opsv1alpha1.RemediationApprovalList
	if err := r.List(ctx, &list, client.InNamespace(i.Namespace)); err != nil {
		return nil, fmt.Errorf("列出审批: %w", err)
	}
	// 优先取最近创建的 Approve 审批。
	var latest *opsv1alpha1.RemediationApproval
	for idx := range list.Items {
		ap := &list.Items[idx]
		if ap.Spec.IncidentRef.UID != i.UID {
			continue
		}
		if latest == nil || ap.CreationTimestamp.After(latest.CreationTimestamp.Time) {
			latest = ap
		}
	}
	return latest, nil
}

// buildDigestFor 计算方案摘要。
func buildDigestFor(i *opsv1alpha1.AIOpsIncident, p *opsv1alpha1.RemediationPolicy, target policy.ObjectInfo) (string, error) {
	if p == nil {
		return "", nil
	}
	params, err := policy.ParseParameters(i.Status.Proposal.Parameters)
	if err != nil {
		return "", err
	}
	return policy.BuildPlanDigest(policy.DigestInput{
		IncidentUID:           i.UID,
		Target:                i.Spec.TargetRef,
		TargetResourceVersion: target.ResourceVersion,
		Action:                i.Status.Proposal.Action,
		Parameters:            params,
		PolicyUID:             p.UID,
		PolicyGeneration:      p.Generation,
	})
}

// refreshPlanDigest 用当前目标状态刷新方案摘要。
// 返回 (新摘要, true) 表示确实刷新了；摘要未变化或失败返回 false。
func (r *IncidentReconciler) refreshPlanDigest(
	_ context.Context,
	i *opsv1alpha1.AIOpsIncident,
	p *opsv1alpha1.RemediationPolicy,
	target policy.ObjectInfo,
) (string, bool) {
	if i.Status.Proposal == nil || p == nil {
		return "", false
	}
	digest, err := buildDigestFor(i, p, target)
	if err != nil || digest == "" || digest == i.Status.Proposal.PlanDigest {
		return "", false
	}
	i.Status.Proposal.PlanDigest = digest
	return digest, true
}

// writePolicyDecision 写策略判定摘要。
func writePolicyDecision(i *opsv1alpha1.AIOpsIncident, d policy.Decision, p *opsv1alpha1.RemediationPolicy) {
	codes := make([]string, 0, len(d.Reasons))
	for _, r := range d.Reasons {
		codes = append(codes, r.Code)
	}
	i.Status.PolicyDecision = &opsv1alpha1.PolicyDecisionStatus{
		Decision:    string(d.Type),
		PolicyRef:   d.PolicyRef,
		ReasonCodes: codes,
		DecidedAt:   &metav1.Time{Time: i.Status.Proposal.GeneratedAt.Time},
	}
	_ = p
}

// derefReplicas 安全解引用副本数。
func derefReplicas(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

// latestRevisionFromDeployment 从 annotations 读取当前 revision。
func latestRevisionFromDeployment(dep *appsv1.Deployment) int64 {
	// Deployment 自身不直接存 revision；从 ReplicaSet 注解读取由 M5 executor 处理。
	// 这里用 rollout revision 注解（若存在）。
	var rev int64
	_, _ = fmt.Sscanf(dep.Annotations["deployment.kubernetes.io/revision"], "%d", &rev)
	return rev
}
