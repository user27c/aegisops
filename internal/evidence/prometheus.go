package evidence

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// promResponse 是 Prometheus HTTP API 响应。
type promResponse struct {
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data"`
	Error  string          `json:"error"`
}

// HTTPPromClient 通过 HTTP API 查询 Prometheus。
type HTTPPromClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewHTTPPromClient 创建 Prometheus 客户端。
func NewHTTPPromClient(baseURL string, httpClient *http.Client) (*HTTPPromClient, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("prometheus URL 不能为空")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	return &HTTPPromClient{BaseURL: baseURL, HTTPClient: httpClient}, nil
}

// Query 执行瞬时查询。
func (p *HTTPPromClient) Query(ctx context.Context, promQL string, ts time.Time) (json.RawMessage, error) {
	u := p.baseURL() + "/api/v1/query"
	q := url.Values{}
	q.Set("query", promQL)
	q.Set("time", fmt.Sprintf("%d", ts.Unix()))
	return p.do(ctx, u, q)
}

// QueryRange 执行区间查询。
func (p *HTTPPromClient) QueryRange(ctx context.Context, promQL string, start, end time.Time, stepSeconds int) (json.RawMessage, error) {
	u := p.baseURL() + "/api/v1/query_range"
	q := url.Values{}
	q.Set("query", promQL)
	q.Set("start", fmt.Sprintf("%d", start.Unix()))
	q.Set("end", fmt.Sprintf("%d", end.Unix()))
	q.Set("step", fmt.Sprintf("%ds", stepSeconds))
	return p.do(ctx, u, q)
}

func (p *HTTPPromClient) baseURL() string {
	return p.BaseURL
}

func (p *HTTPPromClient) do(ctx context.Context, endpoint string, q url.Values) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prometheus 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prometheus HTTP %d: %s", resp.StatusCode, truncateUTF8Bytes(body, 512))
	}
	var pr promResponse
	if err := json.Unmarshal(body, &pr); err != nil {
		return nil, fmt.Errorf("prometheus 响应解析失败: %w", err)
	}
	if pr.Status != "success" {
		return nil, fmt.Errorf("prometheus 查询错误: %s", pr.Error)
	}
	return pr.Data, nil
}

// truncateUTF8Bytes 按字节截断。
func truncateUTF8Bytes(b []byte, maxBytes int) string {
	if len(b) <= maxBytes {
		return string(b)
	}
	return string(b[:maxBytes]) + "..."
}
