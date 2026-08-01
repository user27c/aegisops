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
	"crypto/sha256"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AIOpsIncidentSpec 定义了一次事故的期望状态。创建后 Fingerprint 与 TargetRef 不可变。
type AIOpsIncidentSpec struct {
	// Fingerprint 是告警去重指纹（sha256），创建后不可变。
	// +kubebuilder:validation:MinLength=32
	// +kubebuilder:validation:MaxLength=128
	Fingerprint string `json:"fingerprint"`
	// Cluster 是集群逻辑 ID。
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	Cluster string `json:"cluster"`
	// AlertName 是来源告警名称。
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	AlertName string `json:"alertName"`
	// Severity 是告警级别。
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=32
	Severity string `json:"severity"`
	// SourceStatus 是来源告警状态：firing / resolved。
	// +kubebuilder:validation:Enum=firing;resolved
	SourceStatus string `json:"sourceStatus"`
	// TargetRef 指向目标工作负载，创建后不可变。
	TargetRef TargetReference `json:"targetRef"`
	// StartedAt 是事故开始时间。
	StartedAt metav1.Time `json:"startedAt"`
	// LastReceivedAt 是最后一次收到该事故告警的时间。
	LastReceivedAt metav1.Time `json:"lastReceivedAt"`
	// ResolvedAt 是来源告警 resolved 时间（可选）。
	ResolvedAt *metav1.Time `json:"resolvedAt,omitempty"`
	// CommonLabels 是告警公共标签（Gateway 过滤后写入，禁止任意大 Map）。
	// +kubebuilder:validation:MaxProperties=32
	// +optional
	CommonLabels map[string]string `json:"commonLabels,omitempty"`
	// CommonAnnotations 是告警公共注释（Gateway 过滤后写入）。
	// +kubebuilder:validation:MaxProperties=32
	// +optional
	CommonAnnotations map[string]string `json:"commonAnnotations,omitempty"`
}

// AnalysisReference 引用一次异步分析任务。
type AnalysisReference struct {
	// AnalysisID 是诊断服务返回的任务 ID。
	AnalysisID string `json:"analysisID,omitempty"`
	// EvidenceID 是证据包 ID。
	EvidenceID string `json:"evidenceID,omitempty"`
	// PromptVersion 是使用的 Prompt 版本。
	PromptVersion string `json:"promptVersion,omitempty"`
	// SubmittedAt 是提交时间。
	SubmittedAt *metav1.Time `json:"submittedAt,omitempty"`
	// CompletedAt 是完成时间。
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`
}

// PolicyDecisionStatus 记录 Policy Guard 的判定结果。
type PolicyDecisionStatus struct {
	// Decision 是 Auto / ApprovalRequired / SuggestOnly / Deny。
	Decision string `json:"decision,omitempty"`
	// PolicyRef 是命中的策略。
	PolicyRef string `json:"policyRef,omitempty"`
	// ReasonCodes 是拒绝/降级原因码。
	ReasonCodes []string `json:"reasonCodes,omitempty"`
	// DecidedAt 是判定时间。
	DecidedAt *metav1.Time `json:"decidedAt,omitempty"`
}

// ApprovalStatus 记录审批状态。
type ApprovalStatus struct {
	// Decision 是 Approve / Reject。
	Decision string `json:"decision,omitempty"`
	// ApprovalName 是审批 CR 名称。
	ApprovalName string `json:"approvalName,omitempty"`
	// Actor 是审批人。
	Actor string `json:"actor,omitempty"`
	// Reason 是审批理由。
	Reason string `json:"reason,omitempty"`
	// ExpiresAt 是审批过期时间。
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`
	// DecidedAt 是审批时间。
	DecidedAt *metav1.Time `json:"decidedAt,omitempty"`
}

// ExecutionStatus 记录执行状态。
type ExecutionStatus struct {
	// Reference 是执行引用。
	Reference *ExecutionReference `json:"reference,omitempty"`
	// Attempts 是已尝试次数。
	Attempts int `json:"attempts,omitempty"`
	// LastError 是最近一次错误（不含 Secret）。
	LastError string `json:"lastError,omitempty"`
}

// AIOpsIncidentStatus 定义 AIOpsIncident 的观测状态。
type AIOpsIncidentStatus struct {
	// Phase 是状态机当前阶段。
	Phase IncidentPhase `json:"phase,omitempty"`
	// ObservedGeneration 防止旧状态覆盖新 Spec。
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Conditions 是标准状态条件。
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// Evidence 是证据摘要。
	// +optional
	Evidence *EvidenceSummary `json:"evidence,omitempty"`
	// Analysis 是分析任务引用。
	// +optional
	Analysis *AnalysisReference `json:"analysis,omitempty"`
	// Diagnosis 是诊断结果摘要。
	// +optional
	Diagnosis *DiagnosisSummary `json:"diagnosis,omitempty"`
	// Proposal 是修复方案。
	// +optional
	Proposal *ActionProposal `json:"proposal,omitempty"`
	// PolicyDecision 是策略判定。
	// +optional
	PolicyDecision *PolicyDecisionStatus `json:"policyDecision,omitempty"`
	// Approval 是审批状态。
	// +optional
	Approval *ApprovalStatus `json:"approval,omitempty"`
	// Execution 是执行状态。
	// +optional
	Execution *ExecutionStatus `json:"execution,omitempty"`
	// Verification 是验证状态。
	// +optional
	Verification *VerificationSummary `json:"verification,omitempty"`
	// Timeline 是时间线条目，最多保留 20 条。
	// +optional
	// +kubebuilder:validation:MaxItems=20
	Timeline []TimelineEntry `json:"timeline,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Severity",type=string,JSONPath=`.spec.severity`
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=`.spec.targetRef.kind`
// +kubebuilder:printcolumn:name="TargetName",type=string,JSONPath=`.spec.targetRef.name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Action",type=string,JSONPath=`.status.proposal.action`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AIOpsIncident 是一次去重后的事故，也是控制循环流程状态的唯一事实源。
type AIOpsIncident struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AIOpsIncidentSpec   `json:"spec,omitempty"`
	Status AIOpsIncidentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AIOpsIncidentList 包含 AIOpsIncident 列表。
type AIOpsIncidentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AIOpsIncident `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AIOpsIncident{}, &AIOpsIncidentList{})
}

// SetCondition 设置或更新一个状态条件。
func (i *AIOpsIncident) SetCondition(condition metav1.Condition) {
	for idx := range i.Status.Conditions {
		if i.Status.Conditions[idx].Type == condition.Type {
			i.Status.Conditions[idx] = condition
			return
		}
	}
	i.Status.Conditions = append(i.Status.Conditions, condition)
}

// GetCondition 返回指定类型的状态条件，不存在时返回 nil。
func (i *AIOpsIncident) GetCondition(t string) *metav1.Condition {
	for idx := range i.Status.Conditions {
		if i.Status.Conditions[idx].Type == t {
			return &i.Status.Conditions[idx]
		}
	}
	return nil
}

// AppendTimeline 追加时间线条目并截断到最近 20 条。
func (i *AIOpsIncident) AppendTimeline(entry TimelineEntry) {
	i.Status.Timeline = append(i.Status.Timeline, entry)
	const maxTimelineEntries = 20
	if len(i.Status.Timeline) > maxTimelineEntries {
		i.Status.Timeline = i.Status.Timeline[len(i.Status.Timeline)-maxTimelineEntries:]
	}
}

// IsTerminal 判断 Incident 是否处于终端阶段。
func (i *AIOpsIncident) IsTerminal() bool {
	return i.Status.Phase.IsTerminal()
}

// ExecutionKey 生成执行幂等键：sha256(incidentUID|planDigest)。
func (i *AIOpsIncident) ExecutionKey() string {
	planDigest := ""
	if i.Status.Proposal != nil {
		planDigest = i.Status.Proposal.PlanDigest
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s", i.UID, planDigest)))
	return fmt.Sprintf("%x", sum[:])
}
