package httpapi

import (
	"encoding/json"
	"time"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// IncidentDTO 是对外暴露的 Incident 视图（不含原始证据与敏感字段）。
type IncidentDTO struct {
	Metadata IncidentMetadata  `json:"metadata"`
	Spec     IncidentSpecDTO   `json:"spec"`
	Status   IncidentStatusDTO `json:"status"`
}

// IncidentMetadata 是精简元数据。
type IncidentMetadata struct {
	Name              string    `json:"name"`
	Namespace         string    `json:"namespace"`
	UID               string    `json:"uid,omitempty"`
	CreationTimestamp time.Time `json:"creationTimestamp,omitempty"`
}

// IncidentSpecDTO 是对外暴露的 Spec。
type IncidentSpecDTO struct {
	Fingerprint       string            `json:"fingerprint"`
	Cluster           string            `json:"cluster"`
	AlertName         string            `json:"alertName"`
	Severity          string            `json:"severity"`
	SourceStatus      string            `json:"sourceStatus"`
	TargetRef         TargetDTO         `json:"targetRef"`
	StartedAt         time.Time         `json:"startedAt"`
	LastReceivedAt    time.Time         `json:"lastReceivedAt"`
	ResolvedAt        *time.Time        `json:"resolvedAt,omitempty"`
	CommonLabels      map[string]string `json:"commonLabels,omitempty"`
	CommonAnnotations map[string]string `json:"commonAnnotations,omitempty"`
}

// TargetDTO 是目标引用。
type TargetDTO struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
}

// IncidentStatusDTO 是对外暴露的 Status。
type IncidentStatusDTO struct {
	Phase          string               `json:"phase,omitempty"`
	Evidence       *EvidenceSummaryDTO  `json:"evidence,omitempty"`
	Diagnosis      *DiagnosisSummaryDTO `json:"diagnosis,omitempty"`
	Proposal       *ProposalDTO         `json:"proposal,omitempty"`
	PolicyDecision *PolicyDecisionDTO   `json:"policyDecision,omitempty"`
	Approval       *ApprovalDTO         `json:"approval,omitempty"`
	Verification   *VerificationDTO     `json:"verification,omitempty"`
	Timeline       []TimelineEntryDTO   `json:"timeline,omitempty"`
}

// EvidenceSummaryDTO 是证据摘要。
type EvidenceSummaryDTO struct {
	ID         string         `json:"id,omitempty"`
	Hash       string         `json:"hash,omitempty"`
	Counts     map[string]int `json:"counts,omitempty"`
	Redactions int            `json:"redactions,omitempty"`
}

// DiagnosisSummaryDTO 是诊断摘要。
type DiagnosisSummaryDTO struct {
	Category        string   `json:"category,omitempty"`
	RootCause       string   `json:"rootCause,omitempty"`
	Confidence      float64  `json:"confidence,omitempty"`
	EvidenceIDs     []string `json:"evidenceIDs,omitempty"`
	RunbookRefs     []string `json:"runbookRefs,omitempty"`
	ReviewerVerdict string   `json:"reviewerVerdict,omitempty"`
}

// ProposalDTO 是修复方案。
type ProposalDTO struct {
	Revision   int64          `json:"revision"`
	Action     string         `json:"action,omitempty"`
	Parameters map[string]any `json:"parameters,omitempty"`
	Risk       string         `json:"risk,omitempty"`
	PlanDigest string         `json:"planDigest,omitempty"`
}

// PolicyDecisionDTO 是策略判定。
type PolicyDecisionDTO struct {
	Decision    string   `json:"decision,omitempty"`
	PolicyRef   string   `json:"policyRef,omitempty"`
	ReasonCodes []string `json:"reasonCodes,omitempty"`
}

// ApprovalDTO 是审批状态。
type ApprovalDTO struct {
	Decision string `json:"decision,omitempty"`
	Actor    string `json:"actor,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// VerificationDTO 是验证状态。
type VerificationDTO struct {
	State   string `json:"state,omitempty"`
	Checks  int    `json:"checks,omitempty"`
	Healthy bool   `json:"healthy,omitempty"`
}

// TimelineEntryDTO 是时间线条目。
type TimelineEntryDTO struct {
	Time    time.Time `json:"time"`
	Type    string    `json:"type"`
	Reason  string    `json:"reason,omitempty"`
	Message string    `json:"message,omitempty"`
	Actor   string    `json:"actor,omitempty"`
	// Sequence 是审计链序号（来自诊断服务审计时间线；CR 时间线无）。
	Sequence int64 `json:"sequence,omitempty"`
	// EventHash 是审计事件哈希，默认只显示前 12 位。
	EventHash string `json:"eventHash,omitempty"`
}

// IncidentPage 是分页响应。
type IncidentPage struct {
	Items         []IncidentDTO `json:"items"`
	ContinueToken string        `json:"continueToken,omitempty"`
}

// ToIncidentDTO 转换完整 DTO。
func ToIncidentDTO(i *opsv1alpha1.AIOpsIncident) IncidentDTO {
	dto := IncidentDTO{
		Metadata: IncidentMetadata{
			Name:              i.Name,
			Namespace:         i.Namespace,
			UID:               string(i.UID),
			CreationTimestamp: i.CreationTimestamp.Time,
		},
		Spec: IncidentSpecDTO{
			Fingerprint:  i.Spec.Fingerprint,
			Cluster:      i.Spec.Cluster,
			AlertName:    i.Spec.AlertName,
			Severity:     i.Spec.Severity,
			SourceStatus: i.Spec.SourceStatus,
			TargetRef: TargetDTO{
				APIVersion: i.Spec.TargetRef.APIVersion,
				Kind:       i.Spec.TargetRef.Kind,
				Namespace:  i.Spec.TargetRef.Namespace,
				Name:       i.Spec.TargetRef.Name,
			},
			StartedAt:         i.Spec.StartedAt.Time,
			LastReceivedAt:    i.Spec.LastReceivedAt.Time,
			CommonLabels:      i.Spec.CommonLabels,
			CommonAnnotations: i.Spec.CommonAnnotations,
		},
		Status: IncidentStatusDTO{Phase: string(i.Status.Phase)},
	}
	if i.Spec.ResolvedAt != nil {
		t := i.Spec.ResolvedAt.Time
		dto.Spec.ResolvedAt = &t
	}
	if i.Status.Evidence != nil {
		dto.Status.Evidence = &EvidenceSummaryDTO{
			ID:         i.Status.Evidence.ID,
			Hash:       i.Status.Evidence.Hash,
			Counts:     i.Status.Evidence.Counts,
			Redactions: i.Status.Evidence.Redactions,
		}
	}
	if i.Status.Diagnosis != nil {
		dto.Status.Diagnosis = &DiagnosisSummaryDTO{
			Category:        i.Status.Diagnosis.Category,
			RootCause:       i.Status.Diagnosis.RootCause,
			Confidence:      i.Status.Diagnosis.Confidence,
			EvidenceIDs:     i.Status.Diagnosis.EvidenceIDs,
			RunbookRefs:     i.Status.Diagnosis.RunbookRefs,
			ReviewerVerdict: i.Status.Diagnosis.ReviewerVerdict,
		}
	}
	if i.Status.Proposal != nil {
		dto.Status.Proposal = &ProposalDTO{
			Revision:   i.Status.Proposal.Revision,
			Action:     string(i.Status.Proposal.Action),
			Parameters: jsonToMap(i.Status.Proposal.Parameters.Raw),
			Risk:       string(i.Status.Proposal.Risk),
			PlanDigest: i.Status.Proposal.PlanDigest,
		}
	}
	if i.Status.PolicyDecision != nil {
		dto.Status.PolicyDecision = &PolicyDecisionDTO{
			Decision:    i.Status.PolicyDecision.Decision,
			PolicyRef:   i.Status.PolicyDecision.PolicyRef,
			ReasonCodes: i.Status.PolicyDecision.ReasonCodes,
		}
	}
	if i.Status.Approval != nil {
		dto.Status.Approval = &ApprovalDTO{
			Decision: i.Status.Approval.Decision,
			Actor:    i.Status.Approval.Actor,
			Reason:   i.Status.Approval.Reason,
		}
	}
	if i.Status.Verification != nil {
		dto.Status.Verification = &VerificationDTO{
			State:  i.Status.Verification.State,
			Checks: len(i.Status.Verification.Checks),
		}
	}
	for _, e := range i.Status.Timeline {
		dto.Status.Timeline = append(dto.Status.Timeline, TimelineEntryDTO{
			Time:    e.Time.Time,
			Type:    e.Type,
			Reason:  e.Reason,
			Message: e.Message,
		})
	}
	return dto
}

// jsonToMap 把 JSON RawMessage 转为 map（用于 DTO 输出）。
func jsonToMap(raw []byte) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]any)
	_ = json.Unmarshal(raw, &out)
	return out
}
