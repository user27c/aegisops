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
	"k8s.io/apimachinery/pkg/types"
)

// IncidentReference 绑定审批与 Incident 及其方案版本。
type IncidentReference struct {
	// Name 是 Incident 名称。
	Name string `json:"name"`
	// UID 是 Incident UID，防止名称复用导致误绑。
	UID types.UID `json:"uid"`
	// ProposalRevision 是审批针对的方案版本。
	// +kubebuilder:validation:Minimum=1
	ProposalRevision int64 `json:"proposalRevision"`
}

// RemediationApprovalSpec 定义一次审批。Spec 整体不可变，只能重新创建。
// +kubebuilder:validation:XValidation:rule=`self == oldSelf`,message="审批对象不可修改，只能重新创建"
type RemediationApprovalSpec struct {
	// IncidentRef 绑定 Incident UID 与 ProposalRevision。
	IncidentRef IncidentReference `json:"incidentRef"`
	// Decision 是 Approve / Reject。
	// +kubebuilder:validation:Enum=Approve;Reject
	Decision ApprovalDecision `json:"decision"`
	// PlanDigest 是方案摘要，必须匹配 sha256:<64 hex>。
	// +kubebuilder:validation:Pattern=`^sha256:[0-9a-f]{64}$`
	PlanDigest string `json:"planDigest"`
	// Actor 是审批人，由 Incident API 从认证上下文写入。
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	Actor string `json:"actor"`
	// Reason 是审批理由，必填。
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	Reason string `json:"reason"`
	// ExpiresAt 是审批过期时间（RFC3339）。是否晚于创建时间由 Approval Controller 用服务器时钟判断。
	ExpiresAt metav1.Time `json:"expiresAt"`
}

// RemediationApprovalStatus 定义 RemediationApproval 的观测状态。
type RemediationApprovalStatus struct {
	// Conditions 是状态条件（Valid / Processed）。
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// ProcessedAt 是处理时间。
	// +optional
	ProcessedAt *metav1.Time `json:"processedAt,omitempty"`
	// InvalidReason 是校验失败原因。
	// +optional
	InvalidReason string `json:"invalidReason,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Incident",type=string,JSONPath=`.spec.incidentRef.name`
// +kubebuilder:printcolumn:name="Decision",type=string,JSONPath=`.spec.decision`
// +kubebuilder:printcolumn:name="Actor",type=string,JSONPath=`.spec.actor`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// RemediationApproval 是绑定 Incident UID、proposalRevision 与 planDigest 的不可变审批对象。
type RemediationApproval struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RemediationApprovalSpec   `json:"spec,omitempty"`
	Status RemediationApprovalStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RemediationApprovalList 包含 RemediationApproval 列表。
type RemediationApprovalList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RemediationApproval `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RemediationApproval{}, &RemediationApprovalList{})
}
