package analysisclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// Client 是诊断服务客户端接口。
type Client interface {
	// Submit 提交异步分析任务（幂等键 key 保证重复提交返回原任务）。
	Submit(ctx context.Context, key string, req SubmitRequest) (SubmitResponse, error)
	// Get 轮询任务结果。
	Get(ctx context.Context, analysisID string) (AnalysisResponse, error)
	// PutSnapshot 保存执行前快照。
	PutSnapshot(ctx context.Context, key string, req SnapshotRequest) (SnapshotRef, error)
	// GetSnapshot 读取快照。
	GetSnapshot(ctx context.Context, id string) (Snapshot, error)
}

// TokenSource 提供访问令牌。
type TokenSource interface {
	// Token 返回当前令牌。
	Token() (string, error)
}

// StaticTokenSource 是静态令牌源。
type StaticTokenSource struct{ token string }

// NewStaticTokenSource 创建静态令牌源。
func NewStaticTokenSource(token string) *StaticTokenSource {
	return &StaticTokenSource{token: token}
}

// Token 返回令牌。
func (s *StaticTokenSource) Token() (string, error) {
	return s.token, nil
}

// Option 是客户端配置项。
type Option func(*HTTPClient)

// WithTimeout 设置请求超时。
func WithTimeout(d time.Duration) Option {
	return func(c *HTTPClient) { c.timeout = d }
}

// HTTPClient 是诊断服务 HTTP 客户端。
type HTTPClient struct {
	baseURL     string
	tokenSource TokenSource
	httpClient  *http.Client
	timeout     time.Duration
}

// maxResponseBytes 是响应体上限。
const maxResponseBytes = 1 << 20 // 1 MiB

// NewHTTPClient 创建诊断服务客户端。
func NewHTTPClient(baseURL string, tokenSource TokenSource, opts ...Option) (*HTTPClient, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("诊断服务 URL 不能为空")
	}
	c := &HTTPClient{
		baseURL:     baseURL,
		tokenSource: tokenSource,
		httpClient: &http.Client{
			Timeout:   5 * time.Second,
			Transport: http.DefaultTransport,
		},
		timeout: 5 * time.Second,
	}
	for _, opt := range opts {
		opt(c)
	}
	c.httpClient.Timeout = c.timeout
	return c, nil
}

// Submit 提交分析任务。幂等键 key 保证重复提交返回原任务。
func (c *HTTPClient) Submit(ctx context.Context, key string, req SubmitRequest) (SubmitResponse, error) {
	var out SubmitResponse
	err := c.doJSON(ctx, http.MethodPost, "/v1/analyses", req, &out, key, nil)
	return out, err
}

// Get 轮询任务结果。
func (c *HTTPClient) Get(ctx context.Context, analysisID string) (AnalysisResponse, error) {
	var out AnalysisResponse
	err := c.doJSON(ctx, http.MethodGet, "/v1/analyses/"+analysisID, nil, &out, "", nil)
	return out, err
}

// PutSnapshot 保存执行前快照。
func (c *HTTPClient) PutSnapshot(ctx context.Context, key string, req SnapshotRequest) (SnapshotRef, error) {
	var out SnapshotRef
	err := c.doJSON(ctx, http.MethodPost, "/v1/execution-snapshots", req, &out, key, nil)
	return out, err
}

// GetSnapshot 读取快照。
func (c *HTTPClient) GetSnapshot(ctx context.Context, id string) (Snapshot, error) {
	var out Snapshot
	err := c.doJSON(ctx, http.MethodGet, "/v1/execution-snapshots/"+id, nil, &out, "", nil)
	return out, err
}

// doJSON 执行 JSON 请求。POST 依赖幂等键重试。
func (c *HTTPClient) doJSON(ctx context.Context, method, path string, body, out any, idempotencyKey string, extraHeaders http.Header) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("序列化请求: %w", err)
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("构造请求: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	if c.tokenSource != nil {
		token, err := c.tokenSource.Token()
		if err != nil {
			return fmt.Errorf("读取令牌: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, vs := range extraHeaders {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("诊断服务请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("读取响应: %w", err)
	}

	if resp.StatusCode >= 400 {
		apiErr := &APIError{StatusCode: resp.StatusCode}
		// 尽力解析错误体；失败保留原始消息。
		var body struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Detail  string `json:"detail"`
		}
		if err := json.Unmarshal(raw, &body); err == nil {
			apiErr.Code = body.Code
			apiErr.Message = body.Message
			if apiErr.Message == "" {
				apiErr.Message = body.Detail
			}
		}
		if apiErr.Code == "" {
			apiErr.Code = http.StatusText(resp.StatusCode)
		}
		if apiErr.Message == "" {
			apiErr.Message = truncate(string(raw), 512)
		}
		apiErr.RetryAfter = ParseRetryAfter(resp.Header)
		return apiErr
	}

	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("解析响应: %w", err)
	}
	return nil
}

// ParseRetryAfter 解析 Retry-After 头。
func ParseRetryAfter(h http.Header) time.Duration {
	raw := h.Get("Retry-After")
	if raw == "" {
		return 0
	}
	if secs, err := strconv.Atoi(raw); err == nil {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(raw); err == nil {
		return time.Until(t)
	}
	return 0
}

func truncate(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes] + "..."
}
