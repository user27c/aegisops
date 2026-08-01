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

// LogLine 是单条 Loki 日志。
type LogLine struct {
	// Timestamp 是日志时间。
	Timestamp time.Time `json:"timestamp"`
	// Message 是日志内容。
	Message string `json:"message"`
}

// lokiResponse 是 Loki 响应（简化：只解析 streams 的 values）。
type lokiResponse struct {
	Status string `json:"status"`
	Data   struct {
		Result []struct {
			Stream map[string]string `json:"stream"`
			Values [][]string        `json:"values"`
		} `json:"result"`
	} `json:"data"`
	Error string `json:"error"`
}

// HTTPLokiClient 通过 HTTP API 查询 Loki。
type HTTPLokiClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewHTTPLokiClient 创建 Loki 客户端。
func NewHTTPLokiClient(baseURL string, httpClient *http.Client) (*HTTPLokiClient, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("loki URL 不能为空")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	return &HTTPLokiClient{BaseURL: baseURL, HTTPClient: httpClient}, nil
}

// QueryRange 查询区间日志。query 必须由 BuildSafeLogQL 生成。
func (l *HTTPLokiClient) QueryRange(ctx context.Context, query string, start, end time.Time, limit int) ([]LogLine, error) {
	u := l.BaseURL + "/loki/api/v1/query_range"
	q := url.Values{}
	q.Set("query", query)
	q.Set("start", fmt.Sprintf("%d", start.UnixNano()))
	q.Set("end", fmt.Sprintf("%d", end.UnixNano()))
	q.Set("limit", fmt.Sprintf("%d", limit))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := l.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("loki 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("loki HTTP %d: %s", resp.StatusCode, truncateUTF8Bytes(body, 512))
	}
	var lr lokiResponse
	if err := json.Unmarshal(body, &lr); err != nil {
		return nil, fmt.Errorf("loki 响应解析失败: %w", err)
	}
	if lr.Status != "success" {
		return nil, fmt.Errorf("loki 查询错误: %s", lr.Error)
	}

	lines := make([]LogLine, 0, limit)
	for _, stream := range lr.Data.Result {
		for _, pair := range stream.Values {
			if len(pair) != 2 {
				continue
			}
			ts, err := parseNanoTimestamp(pair[0])
			if err != nil {
				continue
			}
			lines = append(lines, LogLine{Timestamp: ts, Message: pair[1]})
		}
	}
	return lines, nil
}

// BuildSafeLogQL 构造只允许精确 namespace 与 pod 前缀的 LogQL。
// 参数必须来自 K8s API 解析结果，禁止拼接用户字符串。
func BuildSafeLogQL(namespace, podSelector string) string {
	return fmt.Sprintf(`{namespace=%q, pod=~%q}`, namespace, podSelector)
}

// LogsToEvidence 把日志行转为证据条目（去重、截断、脱敏）。
func LogsToEvidence(lines []LogLine, redactor Redactor, maxLines int, maxLineBytes int) ([]EvidenceItem, error) {
	if maxLineBytes <= 0 {
		maxLineBytes = MaxLogLineBytes
	}
	items := make([]EvidenceItem, 0, len(lines))
	seen := map[string]bool{}
	for idx, line := range lines {
		if len(items) >= maxLines {
			break
		}
		msg := line.Message
		if len(msg) > maxLineBytes {
			msg, _ = TruncateUTF8(msg, maxLineBytes)
		}
		// 去重（同消息同秒）。
		key := fmt.Sprintf("%d|%s", line.Timestamp.Unix(), msg)
		if seen[key] {
			continue
		}
		seen[key] = true

		truncated := len(msg) != len(line.Message)
		if redactor != nil {
			msg, _ = redactor.RedactString(msg)
		}
		items = append(items, EvidenceItem{
			ID:        fmt.Sprintf("log-%d", idx+1),
			Kind:      KindLogExcerpt,
			Source:    "loki",
			Timestamp: line.Timestamp,
			Summary:   msg,
			Truncated: truncated,
		})
	}
	return items, nil
}

// parseNanoTimestamp 解析纳秒时间戳。
func parseNanoTimestamp(s string) (time.Time, error) {
	n, err := time.ParseDuration(s + "ns")
	if err != nil {
		// 兼容秒/毫秒字符串。
		if ms, err2 := time.ParseDuration(s + "ms"); err2 == nil {
			return time.Unix(0, ms.Nanoseconds()), nil
		}
		return time.Time{}, err
	}
	return time.Unix(0, n.Nanoseconds()), nil
}
