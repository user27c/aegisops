package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	// ErrDiagnosisUnavailable 是诊断服务不可用（网络/超时/5xx）。
	ErrDiagnosisUnavailable = errors.New("diagnosis 服务不可用")
	// ErrDiagnosisNotFound 是诊断服务返回 404。
	ErrDiagnosisNotFound = errors.New("diagnosis 服务未找到资源")
)

// DiagnosisReader 是从诊断服务读取只读详情（证据/审计时间线）的接口。
// 边界：只读；诊断服务不可用时调用方必须降级（detailsUnavailable）而非 500。
type DiagnosisReader interface {
	// GetEvidence 读取脱敏证据详情。
	GetEvidence(ctx context.Context, evidenceID string) (*EvidenceDetail, error)
	// GetTimeline 读取事故的审计时间线。
	GetTimeline(ctx context.Context, incidentUID string) ([]TimelineEntryDTO, error)
}

// EvidenceItemDetail 是脱敏证据条目（只暴露安全字段）。
type EvidenceItemDetail struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Source    string    `json:"source"`
	Timestamp time.Time `json:"timestamp"`
	Summary   string    `json:"summary"`
}

// EvidenceDetail 是脱敏证据详情。
type EvidenceDetail struct {
	ID             string               `json:"id"`
	Hash           string               `json:"hash"`
	SchemaVersion  string               `json:"schemaVersion,omitempty"`
	WindowStart    time.Time            `json:"windowStart"`
	WindowEnd      time.Time            `json:"windowEnd"`
	Partial        bool                 `json:"partial"`
	MissingSources []string             `json:"missingSources,omitempty"`
	Redactions     int                  `json:"redactions,omitempty"`
	Items          []EvidenceItemDetail `json:"items"`
}

// diagnosisHTTPClient 是 DiagnosisReader 的 HTTP 实现。
type diagnosisHTTPClient struct {
	baseURL string
	token   string
	client  *http.Client
}

// NewDiagnosisClient 构造诊断服务只读客户端。
// timeout 是单请求超时（建议 3s）；token 为空时请求不带 Authorization。
func NewDiagnosisClient(baseURL, token string, timeout time.Duration) DiagnosisReader {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &diagnosisHTTPClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		client:  &http.Client{Timeout: timeout},
	}
}

const maxDiagnosisResponse = 1 << 20 // 1MiB

func (c *diagnosisHTTPClient) do(ctx context.Context, method, path string, out any) error {
	u := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDiagnosisUnavailable, err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDiagnosisUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDiagnosisResponse+1))
	if err != nil {
		return fmt.Errorf("%w: 读取响应失败", ErrDiagnosisUnavailable)
	}
	if len(body) > maxDiagnosisResponse {
		return fmt.Errorf("%w: 响应超过 1MiB", ErrDiagnosisUnavailable)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrDiagnosisNotFound, path)
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("%w: 响应解析失败", ErrDiagnosisUnavailable)
		}
		return nil
	default:
		return fmt.Errorf("%w: status=%d", ErrDiagnosisUnavailable, resp.StatusCode)
	}
}

// diagnosisEvidenceResp 是 /v1/evidence/{id} 的响应（诊断服务字段子集）。
type diagnosisEvidenceResp struct {
	ID            string          `json:"id"`
	ContentHash   string          `json:"content_hash"`
	SchemaVersion string          `json:"schema_version"`
	Payload       json.RawMessage `json:"payload"`
}

// evidencePackWire 是 evidence pack 的 JSON 字段（脱敏输出只取安全子集）。
type evidencePackWire struct {
	WindowStart    time.Time          `json:"windowStart"`
	WindowEnd      time.Time          `json:"windowEnd"`
	Partial        bool               `json:"partial"`
	MissingSources []string           `json:"missingSources"`
	Redactions     []map[string]any   `json:"redactions"`
	Items          []evidenceItemWire `json:"items"`
	Hash           string             `json:"hash"`
}

type evidenceItemWire struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Source    string    `json:"source"`
	Timestamp time.Time `json:"timestamp"`
	Summary   string    `json:"summary"`
}

func (c *diagnosisHTTPClient) GetEvidence(ctx context.Context, evidenceID string) (*EvidenceDetail, error) {
	if evidenceID == "" {
		return nil, fmt.Errorf("evidenceID 不能为空")
	}
	var resp diagnosisEvidenceResp
	if err := c.do(ctx, http.MethodGet, "/v1/evidence/"+url.PathEscape(evidenceID), &resp); err != nil {
		return nil, err
	}
	detail := &EvidenceDetail{
		ID:            resp.ID,
		Hash:          resp.ContentHash,
		SchemaVersion: resp.SchemaVersion,
		Items:         []EvidenceItemDetail{},
	}
	if len(resp.Payload) > 0 {
		var pack evidencePackWire
		if err := json.Unmarshal(resp.Payload, &pack); err != nil {
			return nil, fmt.Errorf("%w: 证据 payload 解析失败", ErrDiagnosisUnavailable)
		}
		detail.WindowStart = pack.WindowStart
		detail.WindowEnd = pack.WindowEnd
		detail.Partial = pack.Partial
		detail.MissingSources = pack.MissingSources
		detail.Redactions = len(pack.Redactions)
		if detail.Hash == "" {
			detail.Hash = pack.Hash
		}
		for _, it := range pack.Items {
			detail.Items = append(detail.Items, EvidenceItemDetail(it))
		}
	}
	return detail, nil
}

// diagnosisTimelineEntry 是 /v1/incidents/{uid}/timeline 响应条目。
type diagnosisTimelineEntry struct {
	Time      time.Time `json:"time"`
	Type      string    `json:"type"`
	Reason    string    `json:"reason"`
	Message   string    `json:"message"`
	Actor     string    `json:"actor"`
	Sequence  int64     `json:"sequence"`
	EventHash string    `json:"event_hash"`
}

func (c *diagnosisHTTPClient) GetTimeline(ctx context.Context, incidentUID string) ([]TimelineEntryDTO, error) {
	if incidentUID == "" {
		return nil, fmt.Errorf("incidentUID 不能为空")
	}
	var entries []diagnosisTimelineEntry
	if err := c.do(ctx, http.MethodGet, "/v1/incidents/"+url.PathEscape(incidentUID)+"/timeline", &entries); err != nil {
		return nil, err
	}
	out := make([]TimelineEntryDTO, 0, len(entries))
	for _, e := range entries {
		hash := e.EventHash
		if len(hash) > 12 {
			hash = hash[:12]
		}
		out = append(out, TimelineEntryDTO{
			Time:      e.Time,
			Type:      e.Type,
			Reason:    e.Reason,
			Message:   e.Message,
			Actor:     e.Actor,
			Sequence:  e.Sequence,
			EventHash: hash,
		})
	}
	return out, nil
}
