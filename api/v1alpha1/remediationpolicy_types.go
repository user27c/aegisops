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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TargetSelector 选择策略适用的目标命名空间与工作负载。
type TargetSelector struct {
	// NamespaceLabels 是命名空间标签选择器。
	// +optional
	NamespaceLabels map[string]string `json:"namespaceLabels,omitempty"`
	// WorkloadLabels 是工作负载标签选择器。
	// +optional
	WorkloadLabels map[string]string `json:"workloadLabels,omitempty"`
	// Kinds 是允许的目标类型列表；空表示全部（MVP 只有 Deployment）。
	// +optional
	// +kubebuilder:validation:MaxItems=8
	Kinds []string `json:"kinds,omitempty"`
}

// ActionPolicy 描述单个动作的策略。字段按动作类型区分使用。
// +kubebuilder:validation:XValidation:rule="!has(self.maxReplicaDelta) || self.maxReplicaDelta > 0",message="maxReplicaDelta 必须大于 0"
// +kubebuilder:validation:XValidation:rule="!has(self.maxReplicas) || !has(self.maxReplicaDelta) || self.maxReplicas >= self.maxReplicaDelta",message="maxReplicas 不得小于 maxReplicaDelta"
type ActionPolicy struct {
	// Enabled 是否启用该动作，默认 false（fail closed）。
	// +kubebuilder:default=false
	Enabled bool `json:"enabled"`
	// Mode 是执行模式：SuggestOnly / ApprovalRequired / Auto。
	// +kubebuilder:validation:Enum=SuggestOnly;ApprovalRequired;Auto
	// +kubebuilder:default=ApprovalRequired
	Mode PolicyMode `json:"mode,omitempty"`
	// MaxReplicaDelta 是 ScaleDeployment 单次最大副本变化量。
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxReplicaDelta *int32 `json:"maxReplicaDelta,omitempty"`
	// MaxReplicas 是 ScaleDeployment 允许的最大副本数。
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxReplicas *int32 `json:"maxReplicas,omitempty"`
	// MaxCPU 是 PatchResourceLimit 允许的 CPU limit 上限。
	// +optional
	MaxCPU *ResourceQuantity `json:"maxCPU,omitempty"`
	// MaxMemory 是 PatchResourceLimit 允许的内存 limit 上限。
	// +optional
	MaxMemory *ResourceQuantity `json:"maxMemory,omitempty"`
	// MaxIncreasePercent 是资源增幅百分比上限（0-1000）。
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1000
	// +optional
	MaxIncreasePercent *int32 `json:"maxIncreasePercent,omitempty"`
	// MaxRevisionDistance 是 RollbackDeployment 允许回退的最大 revision 距离。
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxRevisionDistance *int64 `json:"maxRevisionDistance,omitempty"`
	// AllowedNames 是 RestoreConfigMap 允许操作的 ConfigMap 名称白名单。
	// +optional
	// +kubebuilder:validation:MaxItems=32
	AllowedNames []string `json:"allowedNames,omitempty"`
	// RequireImmutableBackup 要求备份 ConfigMap 必须 immutable。
	// +kubebuilder:default=true
	// +optional
	RequireImmutableBackup *bool `json:"requireImmutableBackup,omitempty"`
}

// ResourceQuantity 是资源数量的字符串表示（如 "1Gi"），便于 CEL 校验与序列化。
type ResourceQuantity string

// RemediationPolicySpec 声明哪些命名空间、工作负载和动作可用，以及自动执行或必须审批。
// +kubebuilder:validation:XValidation:rule="self.actions.all(k, k in ['RestartWorkload','ScaleDeployment','PatchResourceLimit','RollbackDeployment','RestoreConfigMap'])",message="未知动作类型不允许出现在策略中"
// +kubebuilder:validation:XValidation:rule="self.actions.all(k, k == 'RestartWorkload' || self.actions[k].mode != 'Auto')",message="MVP 只有 RestartWorkload 允许 Auto 模式,中风险动作必须审批"
// +kubebuilder:validation:XValidation:rule="!has(self.verificationWindow) || duration(self.verificationWindow) > duration('0s')",message="verificationWindow 必须为正"
// +kubebuilder:validation:XValidation:rule="!has(self.approvalTTL) || duration(self.approvalTTL) > duration('0s')",message="approvalTTL 必须为正"
// +kubebuilder:validation:XValidation:rule="!has(self.cooldown) || duration(self.cooldown) >= duration('0s')",message="cooldown 不得为负"
type RemediationPolicySpec struct {
	// Priority 越高优先级越高；并列时 fail closed。
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=0
	Priority int32 `json:"priority,omitempty"`
	// TargetSelector 选择适用目标。
	TargetSelector TargetSelector `json:"targetSelector"`
	// Actions 是每个动作的策略，key 必须是已知动作类型。
	// +kubebuilder:validation:MaxProperties=5
	Actions map[ActionType]ActionPolicy `json:"actions"`
	// MaxAttemptsPerIncident 是每个 Incident 的最大执行尝试次数。
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=1
	MaxAttemptsPerIncident int32 `json:"maxAttemptsPerIncident,omitempty"`
	// VerificationWindow 是验证时间窗口。
	// +kubebuilder:default="2m"
	VerificationWindow *metav1.Duration `json:"verificationWindow,omitempty"`
	// ApprovalTTL 是审批有效期。
	// +kubebuilder:default="10m"
	ApprovalTTL *metav1.Duration `json:"approvalTTL,omitempty"`
	// Cooldown 是同一目标两次动作之间的冷却期。
	// +kubebuilder:default="10m"
	Cooldown *metav1.Duration `json:"cooldown,omitempty"`
	// RequireAudit 要求审计可用，默认 true。
	// +kubebuilder:default=true
	// +optional
	RequireAudit *bool `json:"requireAudit,omitempty"`
	// RollbackOnVerificationFailure 验证失败时是否回滚，默认 true。
	// +kubebuilder:default=true
	// +optional
	RollbackOnVerificationFailure *bool `json:"rollbackOnVerificationFailure,omitempty"`
}

// RemediationPolicyStatus 定义 RemediationPolicy 的观测状态。
type RemediationPolicyStatus struct {
	// ObservedGeneration 是最近一次校验的 generation。
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Conditions 是状态条件（Valid 等）。
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// LastValidatedAt 是最近一次校验时间。
	// +optional
	LastValidatedAt *metav1.Time `json:"lastValidatedAt,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Priority",type=integer,JSONPath=`.spec.priority`
// +kubebuilder:printcolumn:name="Actions",type=string,JSONPath=`.spec.actions`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// RemediationPolicy 声明哪些命名空间、工作负载和动作可用，以及自动执行或必须审批。
type RemediationPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RemediationPolicySpec   `json:"spec,omitempty"`
	Status RemediationPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RemediationPolicyList 包含 RemediationPolicy 列表。
type RemediationPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RemediationPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RemediationPolicy{}, &RemediationPolicyList{})
}

// ActionEnabled 返回动作是否启用（缺省 fail closed）。
func (p *RemediationPolicy) ActionEnabled(t ActionType) bool {
	ap, ok := p.Spec.Actions[t]
	return ok && ap.Enabled
}

// ActionMode 返回动作的配置模式；未配置时返回空（相当于拒绝）。
func (p *RemediationPolicy) ActionMode(t ActionType) PolicyMode {
	ap, ok := p.Spec.Actions[t]
	if !ok {
		return ""
	}
	if ap.Mode == "" {
		return ModeApprovalRequired
	}
	return ap.Mode
}

// RequireAuditEnabled 返回是否要求审计。
func (p *RemediationPolicy) RequireAuditEnabled() bool {
	if p.Spec.RequireAudit == nil {
		return true
	}
	return *p.Spec.RequireAudit
}
