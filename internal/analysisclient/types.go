// Package analysisclient 是 Go 访问 Diagnosis API 的唯一入口。
//
// 边界：所有异步分析任务的提交、轮询、审计与快照读写都经过本包；
// 其他包不得直接构造诊断服务 HTTP 请求。
package analysisclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/types"

	"github.com/user27c/aegisops/internal/evidence"
)

// JobStatus 是分析任务状态（与 Python API 同步）。
type JobStatus string

// 任务状态枚举。
const (
	StatusQueued     JobStatus = "queued"
	StatusProcessing JobStatus = "processing"
	StatusSucceeded  JobStatus = "succeeded"
	StatusFailed     JobStatus = "failed"
)

// UnmarshalJSON 校验状态值；未知值返回错误，不能悄悄降级。
func (s *JobStatus) UnmarshalJSON(b []byte) error {
	var raw string
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	switch JobStatus(raw) {
	case StatusQueued, StatusProcessing, StatusSucceeded, StatusFailed:
		*s = JobStatus(raw)
		return nil
	default:
		return fmt.Errorf("未知任务状态 %q", raw)
	}
}

// IncidentDTO 是提交给诊断服务的 Incident 摘要。
type IncidentDTO struct {
	UID          string `json:"uid"`
	Namespace    string `json:"namespace"`
	Name         string `json:"name"`
	CategoryHint string `json:"category_hint,omitempty"`
	Severity     string `json:"severity"`
	Target       struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Name       string `json:"name"`
	} `json:"target"`
}

// SubmitRequest 是分析任务提交请求。
type SubmitRequest struct {
	Incident       IncidentDTO           `json:"incident"`
	Evidence       evidence.EvidencePack `json:"evidence"`
	RequestedModel string                `json:"requested_model,omitempty"`
	PromptVersion  string                `json:"prompt_version"`
}

// SubmitResponse 是提交响应（202）。
type SubmitResponse struct {
	AnalysisID string    `json:"analysis_id"`
	Status     JobStatus `json:"status"`
	EvidenceID string    `json:"evidence_id"`
}

// APIError 是诊断服务返回的错误。
type APIError struct {
	StatusCode int
	Code       string
	Message    string
	// RetryAfter 是 429/503 时的重试等待。
	RetryAfter time.Duration
}

// Error 实现 error 接口。
func (e *APIError) Error() string {
	return fmt.Sprintf("diagnosis API %d %s: %s", e.StatusCode, e.Code, e.Message)
}

// IsRetryable 判断错误是否可重试。
func IsRetryable(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 429, 502, 503, 504:
			return true
		default:
			return false
		}
	}
	// 网络错误可重试（POST 重试依赖幂等键）。
	return true
}

// RetryAfterOf 返回建议的重试等待。
func RetryAfterOf(err error) time.Duration {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.RetryAfter
	}
	return 0
}

// ReviewerResult 是 Reviewer 审查结论。
type ReviewerResult struct {
	Verdict string   `json:"verdict"`
	Issues  []string `json:"issues,omitempty"`
	Pass    bool     `json:"pass"`
}

// ProposalDTO 是修复方案（与 Python discriminated union 同步）。
type ProposalDTO struct {
	Action     string          `json:"action"`
	Parameters json.RawMessage `json:"parameters,omitempty"`
}

// DiagnosisResult 是完整诊断结果。
type DiagnosisResult struct {
	Category    string         `json:"category"`
	RootCause   string         `json:"root_cause"`
	Confidence  float64        `json:"confidence"`
	EvidenceIDs []string       `json:"evidence_ids"`
	RunbookRefs []string       `json:"runbook_refs"`
	Reviewer    ReviewerResult `json:"reviewer"`
	Proposal    *ProposalDTO   `json:"proposal,omitempty"`
}

// AnalysisResponse 是轮询响应。
type AnalysisResponse struct {
	ID                string           `json:"id"`
	Status            JobStatus        `json:"status"`
	RetryAfterSeconds int              `json:"retry_after_seconds"`
	Result            *DiagnosisResult `json:"result,omitempty"`
	ErrorCode         string           `json:"error_code,omitempty"`
	ErrorMessage      string           `json:"error_message,omitempty"`
	InputTokens       int              `json:"input_tokens,omitempty"`
	OutputTokens      int              `json:"output_tokens,omitempty"`
}

// SnapshotRequest 是执行前快照保存请求。
type SnapshotRequest struct {
	IncidentUID types.UID       `json:"incident_uid"`
	ExecutionID string          `json:"execution_id"`
	ActionType  string          `json:"action_type"`
	Snapshot    json.RawMessage `json:"snapshot"`
}

// SnapshotRef 是快照保存响应。
type SnapshotRef struct {
	ID        string `json:"id"`
	Hash      string `json:"sha256"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// AuditEventRequest 是审计事件写入请求。
type AuditEventRequest struct {
	IncidentUID string         `json:"incident_uid"`
	Component   string         `json:"component"`
	EventType   string         `json:"event_type"`
	Actor       string         `json:"actor,omitempty"`
	Payload     map[string]any `json:"payload,omitempty"`
}

// AuditEventResponse 是审计写入响应（含 hash chain 信息）。
type AuditEventResponse struct {
	ID           string `json:"id"`
	Sequence     int64  `json:"sequence"`
	PreviousHash string `json:"previous_hash"`
	EventHash    string `json:"event_hash"`
}

// Snapshot 是读取到的快照。
type Snapshot struct {
	ID          string          `json:"id"`
	IncidentUID types.UID       `json:"incident_uid"`
	ExecutionID string          `json:"execution_id"`
	ActionType  string          `json:"action_type"`
	Snapshot    json.RawMessage `json:"snapshot"`
	Hash        string          `json:"sha256"`
}
