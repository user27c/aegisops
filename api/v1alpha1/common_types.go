/*
Copyright 2026 AegisOps Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"fmt"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// IncidentPhase 表示 AIOpsIncident 状态机的当前阶段。
type IncidentPhase string

// 状态机全部阶段。终端阶段为 Resolved / RolledBack / Escalated。
const (
	PhaseDetected           IncidentPhase = "Detected"
	PhaseCollectingEvidence IncidentPhase = "CollectingEvidence"
	PhaseDiagnosing         IncidentPhase = "Diagnosing"
	PhasePolicyChecking     IncidentPhase = "PolicyChecking"
	PhaseAwaitingApproval   IncidentPhase = "AwaitingApproval"
	PhaseExecuting          IncidentPhase = "Executing"
	PhaseVerifying          IncidentPhase = "Verifying"
	PhaseRollingBack        IncidentPhase = "RollingBack"
	PhaseResolved           IncidentPhase = "Resolved"
	PhaseRolledBack         IncidentPhase = "RolledBack"
	PhaseEscalated          IncidentPhase = "Escalated"
	PhaseRecoveredNoAction  IncidentPhase = "RecoveredWithoutAction"
)

// ActionType 表示受支持的类型化修复动作。任何未在此列出的动作都不得执行。
type ActionType string

// 全部受支持动作。MVP 只有这五种，注册表之外的任何动作一律拒绝。
const (
	ActionRestartWorkload    ActionType = "RestartWorkload"
	ActionScaleDeployment    ActionType = "ScaleDeployment"
	ActionPatchResourceLimit ActionType = "PatchResourceLimit"
	ActionRollbackDeployment ActionType = "RollbackDeployment"
	ActionRestoreConfigMap   ActionType = "RestoreConfigMap"
)

// RiskLevel 表示动作的固有风险等级。
type RiskLevel string

const (
	// RiskLow 是低风险（可自动执行）。
	RiskLow RiskLevel = "low"
	// RiskMedium 是中等风险（必须审批）。
	RiskMedium RiskLevel = "medium"
	// RiskHigh 是高风险（永久拒绝）。
	RiskHigh RiskLevel = "high"
)

// PolicyMode 表示动作在 RemediationPolicy 中的执行模式。
type PolicyMode string

const (
	// ModeSuggestOnly 只给出建议，不执行。
	ModeSuggestOnly PolicyMode = "SuggestOnly"
	// ModeApprovalRequired 必须人工审批。
	ModeApprovalRequired PolicyMode = "ApprovalRequired"
	// ModeAuto 低风险自动执行。
	ModeAuto PolicyMode = "Auto"
)

// ApprovalDecision 表示审批结论。
type ApprovalDecision string

const (
	// ApprovalApprove 批准。
	ApprovalApprove ApprovalDecision = "Approve"
	// ApprovalReject 拒绝。
	ApprovalReject ApprovalDecision = "Reject"
)

// TargetReference 指向被诊断/修复的目标工作负载。
type TargetReference struct {
	// APIVersion 是目标资源的 API 版本，例如 apps/v1。
	APIVersion string `json:"apiVersion"`
	// Kind 是目标资源类型，MVP 只支持 Deployment。
	Kind string `json:"kind"`
	// Namespace 是目标所在命名空间。
	Namespace string `json:"namespace"`
	// Name 是目标资源名称。
	Name string `json:"name"`
	// UID 是目标资源 UID，创建后不可变。
	UID types.UID `json:"uid,omitempty"`
}

// ObjectRevision 记录观测到的目标资源版本信息。
type ObjectRevision struct {
	// Generation 是目标资源 generation。
	Generation int64 `json:"generation,omitempty"`
	// ResourceVersion 是目标资源 resourceVersion。
	ResourceVersion string `json:"resourceVersion,omitempty"`
	// ObservedAt 是观测时间。
	ObservedAt *metav1.Time `json:"observedAt,omitempty"`
}

// EvidenceSummary 是证据包在 Incident Status 中的摘要，原始证据存 PostgreSQL。
type EvidenceSummary struct {
	// ID 是证据包 ID（PostgreSQL evidence_snapshots.id）。
	ID string `json:"id"`
	// Hash 是证据包内容哈希。
	Hash string `json:"hash"`
	// Window 是证据时间窗口。
	Window TimeWindow `json:"window,omitempty"`
	// Counts 按证据类型统计条目数。
	Counts map[string]int `json:"counts,omitempty"`
	// Redactions 是脱敏事件摘要。
	Redactions int `json:"redactions,omitempty"`
}

// TimeWindow 表示证据时间窗口。
type TimeWindow struct {
	Start metav1.Time `json:"start"`
	End   metav1.Time `json:"end"`
}

// DiagnosisSummary 是诊断结果摘要。
type DiagnosisSummary struct {
	// Category 是根因分类，例如 OOMKilled。
	Category string `json:"category,omitempty"`
	// RootCause 是根因描述。
	RootCause string `json:"rootCause,omitempty"`
	// Confidence 是模型置信度 0..1。注意：置信度绝不作为安全授权条件。
	Confidence float64 `json:"confidence,omitempty"`
	// EvidenceIDs 是引用的证据条目 ID。
	EvidenceIDs []string `json:"evidenceIDs,omitempty"`
	// RunbookRefs 是引用的 Runbook 引用（runbook://...）。
	RunbookRefs []string `json:"runbookRefs,omitempty"`
	// ReviewerVerdict 是 Reviewer 审查结论。
	ReviewerVerdict string `json:"reviewerVerdict,omitempty"`
}

// ActionProposal 是类型化修复方案。只有方案语义变化才递增 Revision。
type ActionProposal struct {
	// Revision 是方案版本，从 1 开始递增。
	Revision int64 `json:"revision"`
	// Action 是动作类型。
	Action ActionType `json:"action"`
	// Parameters 是动作参数（具体 schema 由各动作定义）。
	Parameters apiextensionsv1.JSON `json:"parameters,omitempty"`
	// Risk 是风险等级（由 Policy Guard 判定，模型不可自报）。
	Risk RiskLevel `json:"risk,omitempty"`
	// PlanDigest 是方案摘要，绑定 Incident UID、目标 resourceVersion 与 Policy generation。
	PlanDigest string `json:"planDigest,omitempty"`
	// GeneratedAt 是方案生成时间。
	GeneratedAt metav1.Time `json:"generatedAt,omitempty"`
}

// ExecutionReference 记录一次执行。
type ExecutionReference struct {
	// ExecutionID 是执行 ID。
	ExecutionID string `json:"executionID,omitempty"`
	// SnapshotID 是执行前快照 ID。
	SnapshotID string `json:"snapshotID,omitempty"`
	// OperationID 是写入目标资源的幂等 operation-id annotation。
	OperationID string `json:"operationID,omitempty"`
	// StartedAt / FinishedAt 是执行起止时间。
	StartedAt  *metav1.Time `json:"startedAt,omitempty"`
	FinishedAt *metav1.Time `json:"finishedAt,omitempty"`
}

// VerificationSummary 是健康验证结果摘要。
type VerificationSummary struct {
	// State 是 Pending / Healthy / Unhealthy。
	State string `json:"state,omitempty"`
	// Checks 是检查项及结果。
	Checks []VerificationCheck `json:"checks,omitempty"`
	// Deadline 是验证截止时间。
	Deadline *metav1.Time `json:"deadline,omitempty"`
	// LastCheckedAt 是上次检查时间。
	LastCheckedAt *metav1.Time `json:"lastCheckedAt,omitempty"`
	// ConsecutiveSuccesses 是连续成功次数，存于 Status 而非进程内存。
	ConsecutiveSuccesses int `json:"consecutiveSuccesses,omitempty"`
}

// VerificationCheck 是单次检查结果。
type VerificationCheck struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// TimelineEntry 是 Incident 时间线条目。Status 只保留最近 20 条，完整历史存审计。
type TimelineEntry struct {
	Time    metav1.Time `json:"time"`
	Type    string      `json:"type"`
	Reason  string      `json:"reason,omitempty"`
	Message string      `json:"message,omitempty"`
}

// IsTerminal 判断阶段是否为终端阶段。
func (p IncidentPhase) IsTerminal() bool {
	switch p {
	case PhaseResolved, PhaseRolledBack, PhaseEscalated, PhaseRecoveredNoAction:
		return true
	default:
		return false
	}
}

// IsKnown 判断动作类型是否在支持列表中。
func (a ActionType) IsKnown() bool {
	switch a {
	case ActionRestartWorkload, ActionScaleDeployment, ActionPatchResourceLimit,
		ActionRollbackDeployment, ActionRestoreConfigMap:
		return true
	default:
		return false
	}
}

// Valid 判断风险等级是否合法。
func (r RiskLevel) Valid() bool {
	switch r {
	case RiskLow, RiskMedium, RiskHigh:
		return true
	default:
		return false
	}
}

// Valid 判断审批结论是否合法。
func (d ApprovalDecision) Valid() bool {
	switch d {
	case ApprovalApprove, ApprovalReject:
		return true
	default:
		return false
	}
}

// CanonicalTargetKey 生成目标资源的稳定标识键，用于去重与锁。
func CanonicalTargetKey(ref TargetReference) string {
	return fmt.Sprintf("%s/%s/%s/%s", ref.APIVersion, ref.Kind, ref.Namespace, ref.Name)
}
