package policy

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// Evaluator 评估方案并给出决策。
type Evaluator interface {
	Evaluate(ctx context.Context, in EvaluationInput) (Decision, error)
}

// DefaultEvaluator 是默认策略评估器。
type DefaultEvaluator struct{}

// Evaluate 按固定顺序执行 8 步判定（蓝图 13.3）。
func (e *DefaultEvaluator) Evaluate(_ context.Context, in EvaluationInput) (Decision, error) {
	i := in.Incident
	p := in.Proposal
	action := p.Action
	now := in.Now

	// 1. Action 是否已注册。
	info, ok := KnownActions[action]
	if !ok {
		return deny(ReasonActionNotRegistered, fmt.Sprintf("动作 %s 未注册", action), in), nil
	}

	// 2. 目标 UID 是否与 Incident 记录一致。
	if in.Target.UID != "" && i.Spec.TargetRef.UID != "" && in.Target.UID != i.Spec.TargetRef.UID {
		return deny(ReasonTargetChanged, "目标 UID 与 Incident 记录不一致", in), nil
	}
	// resourceVersion 一致性由 planDigest 绑定并在审批阶段完整校验（TOCTOU 防护）。

	// 3. Policy 是否匹配、是否启用 Action。
	if in.Policy == nil {
		return deny(ReasonTargetNotAllowed, "没有匹配的 RemediationPolicy", in), nil
	}
	if !in.Policy.ActionEnabled(action) {
		return deny(ReasonActionDisabled, fmt.Sprintf("动作 %s 未在策略中启用", action), in), nil
	}

	// 4. 固有风险；Policy 只能更严格。
	risk := info.IntrinsicRisk
	configuredMode := in.Policy.ActionMode(action)
	if configuredMode == "" {
		return deny(ReasonActionDisabled, "策略未配置动作模式", in), nil
	}
	if info.RequiresApproval && configuredMode == opsv1alpha1.ModeAuto {
		return deny(ReasonHighRiskDenied, fmt.Sprintf("%s 不允许 Auto 模式", action), in), nil
	}

	// 5. 参数约束。
	if err := validateParams(in); err != nil {
		return deny(ReasonConstraintsViolated, err.Error(), in), nil
	}

	// 6. 尝试次数与冷却期。
	if in.Target.Attempts >= int(in.Policy.Spec.MaxAttemptsPerIncident) && in.Policy.Spec.MaxAttemptsPerIncident > 0 {
		return deny(ReasonAttemptsExceeded, fmt.Sprintf("尝试次数 %d 已达上限", in.Target.Attempts), in), nil
	}
	if in.Target.LastActionAt != nil && in.Policy.Spec.Cooldown != nil && in.Policy.Spec.Cooldown.Duration > 0 {
		elapsed := now.Sub(*in.Target.LastActionAt)
		if elapsed < in.Policy.Spec.Cooldown.Duration {
			return deny(ReasonCooldownActive, fmt.Sprintf("冷却期未过：已过 %s，需 %s", elapsed.Round(time.Second), in.Policy.Spec.Cooldown.Duration), in), nil
		}
	}

	// 7. 审计/诊断/证据引用是否齐全。
	if in.Policy.RequireAuditEnabled() && !in.AuditAvailable {
		return deny(ReasonAuditUnavailable, "审计服务不可用", in), nil
	}
	if i.Status.Diagnosis == nil || len(i.Status.Diagnosis.EvidenceIDs) == 0 {
		return deny(ReasonEvidenceInsufficient, "诊断或证据引用缺失", in), nil
	}
	if !in.EvidenceSufficient {
		return deny(ReasonEvidenceInsufficient, "证据不足以支持修复", in), nil
	}

	constraints := effectiveConstraints(in.Policy)

	// 8. 按模式决策。
	switch configuredMode {
	case opsv1alpha1.ModeSuggestOnly:
		return Decision{
			Type:        DecisionSuggestOnly,
			Risk:        risk,
			PolicyRef:   policyRef(in.Policy),
			Constraints: constraints,
		}, nil
	case opsv1alpha1.ModeAuto:
		if risk != opsv1alpha1.RiskLow {
			return deny(ReasonHighRiskDenied, "Auto 模式只允许低风险动作", in), nil
		}
		return Decision{
			Type:        DecisionAuto,
			Risk:        risk,
			PolicyRef:   policyRef(in.Policy),
			Constraints: constraints,
		}, nil
	case opsv1alpha1.ModeApprovalRequired:
		return e.evaluateApproval(in, risk, constraints)
	default:
		return deny(ReasonActionDisabled, fmt.Sprintf("未知策略模式 %q", configuredMode), in), nil
	}
}

// evaluateApproval 校验审批对象（Incident UID + revision + planDigest + TTL）。
func (e *DefaultEvaluator) evaluateApproval(in EvaluationInput, risk opsv1alpha1.RiskLevel, constraints EffectiveConstraints) (Decision, error) {
	if in.Approval == nil {
		return Decision{
			Type:        DecisionApprovalRequired,
			Risk:        risk,
			PolicyRef:   policyRef(in.Policy),
			Reasons:     []Reason{{Code: ReasonApprovalMissing, Message: "需要人工审批"}},
			Constraints: constraints,
		}, nil
	}
	ap := in.Approval

	// 审批绑定：Incident UID + proposalRevision。
	if ap.Spec.IncidentRef.UID != in.Incident.UID {
		return deny(ReasonApprovalMismatch, "审批绑定的 Incident UID 不匹配", in), nil
	}
	if ap.Spec.IncidentRef.ProposalRevision != in.Proposal.Revision {
		return deny(ReasonApprovalMismatch, "审批绑定的方案版本不匹配", in), nil
	}
	// planDigest 完整校验：绑定目标 resourceVersion 与 Policy generation。
	// 方案摘要变化（参数/目标版本/策略版本任一变化）都会使审批失效。
	if ap.Spec.PlanDigest != "" {
		params, _ := ParseParameters(in.Proposal.Parameters)
		digestInput := DigestInput{
			IncidentUID:           in.Incident.UID,
			Target:                in.Incident.Spec.TargetRef,
			TargetResourceVersion: in.Target.ResourceVersion,
			Action:                in.Proposal.Action,
			Parameters:            params,
			PolicyUID:             in.Policy.UID,
			PolicyGeneration:      in.Policy.Generation,
		}
		if err := VerifyPlanDigest(ap.Spec.PlanDigest, digestInput); err != nil {
			return deny(ReasonApprovalMismatch, err.Error(), in), nil
		}
	}
	if ap.Spec.Decision != opsv1alpha1.ApprovalApprove {
		return deny("POLICY_APPROVAL_REJECTED", "审批被拒绝", in), nil
	}
	if in.Now.After(ap.Spec.ExpiresAt.Time) {
		return deny(ReasonApprovalExpired, "审批已过期", in), nil
	}
	return Decision{
		Type:        DecisionAuto, // 审批通过后等效放行（由调用方转 Executing）。
		Risk:        risk,
		PolicyRef:   policyRef(in.Policy),
		Constraints: constraints,
	}, nil
}

// validateParams 按动作分发参数校验。
func validateParams(in EvaluationInput) error {
	params, err := ParseParameters(in.Proposal.Parameters)
	if err != nil {
		return err
	}
	switch in.Proposal.Action {
	case opsv1alpha1.ActionRestartWorkload:
		reason, _ := params["reason"].(string)
		return ValidateRestart(RestartParams{Reason: reason}, RestartConstraints{RequireReason: true})
	case opsv1alpha1.ActionScaleDeployment:
		replicas, _ := params["replicas"].(float64)
		ap := in.Policy.Spec.Actions[opsv1alpha1.ActionScaleDeployment]
		return ValidateScale(in.Target.Replicas, ScaleParams{
			Replicas: int32(replicas),
			Reason:   strOr(params["reason"]),
		}, ScaleConstraints{
			MaxReplicas:     derefInt32(ap.MaxReplicas),
			MaxReplicaDelta: derefInt32(ap.MaxReplicaDelta),
		})
	case opsv1alpha1.ActionPatchResourceLimit:
		ap := in.Policy.Spec.Actions[opsv1alpha1.ActionPatchResourceLimit]
		return ValidateResourcePatch(nil, ResourcePatchParams{
			Container:   strOr(params["container"]),
			MemoryLimit: strOr(params["memoryLimit"]),
			CPULimit:    strOr(params["cpuLimit"]),
		}, ResourceConstraints{
			MaxMemory:          quantityOrNil(ap.MaxMemory),
			MaxCPU:             quantityOrNil(ap.MaxCPU),
			MaxIncreasePercent: derefInt32(ap.MaxIncreasePercent),
		})
	case opsv1alpha1.ActionRollbackDeployment:
		ap := in.Policy.Spec.Actions[opsv1alpha1.ActionRollbackDeployment]
		return ValidateRollback(in.Target.Revision, RollbackParams{
			TargetRevision: int64(numOr(params["targetRevision"])),
			Reason:         strOr(params["reason"]),
		}, RollbackConstraints{
			MaxRevisionDistance: derefInt64(ap.MaxRevisionDistance),
		})
	case opsv1alpha1.ActionRestoreConfigMap:
		ap := in.Policy.Spec.Actions[opsv1alpha1.ActionRestoreConfigMap]
		return ValidateConfigRestore(RestoreConfigMapParams{
			TargetConfigMap: strOr(params["targetConfigMap"]),
			BackupConfigMap: strOr(params["backupConfigMap"]),
		}, ConfigMapConstraints{
			AllowedNames:           ap.AllowedNames,
			RequireImmutableBackup: derefBool(ap.RequireImmutableBackup),
		})
	default:
		return fmt.Errorf("未知动作 %q", in.Proposal.Action)
	}
}

func deny(code, message string, in EvaluationInput) Decision {
	return Decision{
		Type:      DecisionDeny,
		Risk:      riskOf(in),
		PolicyRef: policyRef(in.Policy),
		Reasons:   []Reason{{Code: code, Message: message}},
	}
}

func riskOf(in EvaluationInput) opsv1alpha1.RiskLevel {
	risk, _ := IntrinsicRisk(in.Proposal.Action)
	return risk
}

func policyRef(p *opsv1alpha1.RemediationPolicy) string {
	if p == nil {
		return ""
	}
	return p.Namespace + "/" + p.Name
}

func effectiveConstraints(p *opsv1alpha1.RemediationPolicy) EffectiveConstraints {
	c := EffectiveConstraints{
		MaxAttemptsPerIncident:        p.Spec.MaxAttemptsPerIncident,
		RequireAudit:                  p.RequireAuditEnabled(),
		RollbackOnVerificationFailure: true,
	}
	if p.Spec.VerificationWindow != nil {
		c.VerificationWindow = p.Spec.VerificationWindow.Duration
	}
	if p.Spec.ApprovalTTL != nil {
		c.ApprovalTTL = p.Spec.ApprovalTTL.Duration
	}
	if p.Spec.Cooldown != nil {
		c.Cooldown = p.Spec.Cooldown.Duration
	}
	if p.Spec.RollbackOnVerificationFailure != nil {
		c.RollbackOnVerificationFailure = *p.Spec.RollbackOnVerificationFailure
	}
	return c
}

// quantityOrNil 把 ResourceQuantity 字符串转为 resource.Quantity。
func quantityOrNil(q *opsv1alpha1.ResourceQuantity) *resource.Quantity {
	if q == nil || *q == "" {
		return nil
	}
	parsed, err := resource.ParseQuantity(string(*q))
	if err != nil {
		return nil
	}
	return &parsed
}

func strOr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func numOr(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

func derefInt32(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func derefBool(p *bool) bool {
	if p == nil {
		return true
	}
	return *p
}
