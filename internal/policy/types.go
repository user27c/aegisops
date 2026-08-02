// Package policy 实现确定性策略校验。
//
// 边界：纯逻辑，不访问集群；所有输入由调用方提供。
// 任何拒绝必须给出稳定 reason code（POLICY_*）。
package policy

import (
	"encoding/json"
	"fmt"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/types"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// DecisionType 是策略判定类型。
type DecisionType string

// 判定类型。
const (
	DecisionAuto             DecisionType = "Auto"
	DecisionApprovalRequired DecisionType = "ApprovalRequired"
	DecisionSuggestOnly      DecisionType = "SuggestOnly"
	DecisionDeny             DecisionType = "Deny"
)

// 稳定拒绝原因码。
const (
	ReasonActionNotRegistered  = "POLICY_ACTION_NOT_REGISTERED"
	ReasonTargetChanged        = "POLICY_TARGET_CHANGED"
	ReasonTargetNotAllowed     = "POLICY_TARGET_NOT_ALLOWED"
	ReasonActionDisabled       = "POLICY_ACTION_DISABLED"
	ReasonPolicyAmbiguous      = "POLICY_AMBIGUOUS"
	ReasonConstraintsViolated  = "POLICY_CONSTRAINTS_VIOLATED"
	ReasonCooldownActive       = "POLICY_COOLDOWN_ACTIVE"
	ReasonAttemptsExceeded     = "POLICY_ATTEMPTS_EXCEEDED"
	ReasonAuditUnavailable     = "POLICY_AUDIT_UNAVAILABLE"
	ReasonEvidenceInsufficient = "POLICY_EVIDENCE_INSUFFICIENT"
	ReasonApprovalMismatch     = "POLICY_APPROVAL_MISMATCH"
	ReasonApprovalExpired      = "POLICY_APPROVAL_EXPIRED"
	ReasonApprovalMissing      = "POLICY_APPROVAL_MISSING"
	ReasonHighRiskDenied       = "POLICY_HIGH_RISK_DENIED"
	ReasonVerificationUnclear  = "POLICY_VERIFICATION_UNCLEAR"
)

// Reason 是单条判定理由。
type Reason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// EffectiveConstraints 是命中策略后的生效约束。
type EffectiveConstraints struct {
	MaxAttemptsPerIncident        int32
	VerificationWindow            time.Duration
	ApprovalTTL                   time.Duration
	Cooldown                      time.Duration
	RequireAudit                  bool
	RollbackOnVerificationFailure bool
}

// Decision 是策略判定结果。
type Decision struct {
	Type        DecisionType
	Risk        opsv1alpha1.RiskLevel
	PolicyRef   string
	Reasons     []Reason
	Constraints EffectiveConstraints
}

// Denied 判断是否为拒绝。
func (d Decision) Denied() bool { return d.Type == DecisionDeny }

// Approved 判断是否允许执行（Auto）。
func (d Decision) Approved() bool { return d.Type == DecisionAuto }

// EvaluationInput 是策略评估输入。
type EvaluationInput struct {
	Incident *opsv1alpha1.AIOpsIncident
	Proposal opsv1alpha1.ActionProposal
	// Policy 是已解析的策略（nil 视为无匹配 → Deny）。
	Policy *opsv1alpha1.RemediationPolicy
	// Approval 是审批对象（ApprovalRequired 时提供）。
	Approval *opsv1alpha1.RemediationApproval
	// Target 是目标对象信息。
	Target ObjectInfo
	// Now 是评估时间。
	Now time.Time
	// AuditAvailable 标记审计服务是否可用（RequireAudit 时必填）。
	AuditAvailable bool
	// EvidenceSufficient 标记证据是否满足 Runbook 要求。
	EvidenceSufficient bool
}

// ObjectInfo 是目标对象的只读信息。
type ObjectInfo struct {
	UID             types.UID
	ResourceVersion string
	Generation      int64
	// Replicas 是当前副本数（Scale 校验用）。
	Replicas int32
	// Revision 是当前 revision（Rollback 校验用）。
	Revision int64
	// LastActionAt 是同一目标最近一次动作时间（冷却期用）。
	LastActionAt *time.Time
	// Attempts 是本次 Incident 已尝试次数。
	Attempts int
}

// ActionInfo 是动作注册信息。
type ActionInfo struct {
	// Registered 标记动作是否已知。
	Registered bool
	// IntrinsicRisk 是动作固有风险。
	IntrinsicRisk opsv1alpha1.RiskLevel
	// RequiredMode 是动作要求的策略模式。
	RequiredMode opsv1alpha1.PolicyMode
	// RequiresApproval 标记中风险动作必须审批。
	RequiresApproval bool
}

// KnownActions 是注册的动作表（与 executor 注册表一致）。
var KnownActions = map[opsv1alpha1.ActionType]ActionInfo{
	opsv1alpha1.ActionRestartWorkload:    {Registered: true, IntrinsicRisk: opsv1alpha1.RiskLow, RequiredMode: opsv1alpha1.ModeAuto, RequiresApproval: false},
	opsv1alpha1.ActionScaleDeployment:    {Registered: true, IntrinsicRisk: opsv1alpha1.RiskMedium, RequiredMode: opsv1alpha1.ModeApprovalRequired, RequiresApproval: true},
	opsv1alpha1.ActionPatchResourceLimit: {Registered: true, IntrinsicRisk: opsv1alpha1.RiskMedium, RequiredMode: opsv1alpha1.ModeApprovalRequired, RequiresApproval: true},
	opsv1alpha1.ActionRollbackDeployment: {Registered: true, IntrinsicRisk: opsv1alpha1.RiskMedium, RequiredMode: opsv1alpha1.ModeApprovalRequired, RequiresApproval: true},
	opsv1alpha1.ActionRestoreConfigMap:   {Registered: true, IntrinsicRisk: opsv1alpha1.RiskMedium, RequiredMode: opsv1alpha1.ModeApprovalRequired, RequiresApproval: true},
}

// IntrinsicRisk 返回动作固有风险。
func IntrinsicRisk(action opsv1alpha1.ActionType) (opsv1alpha1.RiskLevel, bool) {
	info, ok := KnownActions[action]
	if !ok {
		return "", false
	}
	return info.IntrinsicRisk, true
}

// ParseParameters 解析方案参数为 JSON map。
func ParseParameters(p apiextensionsv1.JSON) (map[string]any, error) {
	if len(p.Raw) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(p.Raw, &out); err != nil {
		return nil, fmt.Errorf("方案参数非法 JSON: %w", err)
	}
	return out, nil
}
