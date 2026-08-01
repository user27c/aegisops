package evidence

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func lokiResponseBody() string {
	return `{"status":"success","data":{"resultType":"streams","result":[
		{"stream":{"pod":"checkout-api-abc"},"values":[
			["1754035200000000000","level=error msg=boom token=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.x"],
			["1754035200000000000","level=error msg=boom"],
			["1754035140000000000","level=info msg=ok"]
		]}
	]}}`
}

func TestHTTPLokiClient_QueryRange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/loki/api/v1/query_range" {
			t.Errorf("路径错误: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(lokiResponseBody()))
	}))
	defer srv.Close()

	c, err := NewHTTPLokiClient(srv.URL, nil)
	if err != nil {
		t.Fatalf("NewHTTPLokiClient: %v", err)
	}
	lines, err := c.QueryRange(context.Background(), `{namespace="fault-lab"}`, time.Now().Add(-5*time.Minute), time.Now(), 100)
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	// 3 个 values 但第 1、2 条同消息同秒 → 去重后 2 条。
	if len(lines) != 3 {
		t.Errorf("应有 3 条日志，得到 %d", len(lines))
	}
}

func TestHTTPLokiClient_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer srv.Close()

	c, _ := NewHTTPLokiClient(srv.URL, nil)
	if _, err := c.QueryRange(context.Background(), "{}", time.Now(), time.Now(), 10); err == nil {
		t.Error("HTTP 5xx 应报错")
	}
}

func TestBuildSafeLogQL_NoInjection(t *testing.T) {
	// 即使参数含引号也会被 %q 转义，不会逃逸出字符串字面量。
	q := BuildSafeLogQL(`x" OR 1=1 --`, `a"b`)
	if !strings.Contains(q, `\"`) {
		t.Errorf("引号应被转义: %s", q)
	}
	// 转义后不会出现"裸引号闭合后拼接新表达式"的模式。
	if strings.Contains(q, `namespace="x" OR`) {
		t.Errorf("LogQL 注入: %s", q)
	}
}

func TestLogsToEvidence_RedactAndLimit(t *testing.T) {
	redactor := NewRegexRedactor(nil)
	lines := []LogLine{
		{Timestamp: time.Unix(100, 0), Message: "error token=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.x secret"},
		{Timestamp: time.Unix(101, 0), Message: strings.Repeat("y", 10000)}, // 超 8KiB
	}
	items, err := LogsToEvidence(lines, redactor, 200, MaxLogLineBytes)
	if err != nil {
		t.Fatalf("LogsToEvidence: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("应有 2 条: %d", len(items))
	}
	if strings.Contains(items[0].Summary, "eyJ") {
		t.Errorf("JWT 未脱敏: %s", items[0].Summary)
	}
	if len(items[1].Summary) > MaxLogLineBytes {
		t.Errorf("日志未截断: %d", len(items[1].Summary))
	}
	if !items[1].Truncated {
		t.Error("应标记截断")
	}
}

func TestLogsToEvidence_Dedup(t *testing.T) {
	lines := []LogLine{
		{Timestamp: time.Unix(100, 0), Message: "same"},
		{Timestamp: time.Unix(100, 0), Message: "same"},
		{Timestamp: time.Unix(101, 0), Message: "same"},
	}
	items, err := LogsToEvidence(lines, nil, 200, MaxLogLineBytes)
	if err != nil {
		t.Fatalf("LogsToEvidence: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("同秒同消息应去重: %d", len(items))
	}
}

func TestParseNanoTimestamp(t *testing.T) {
	ts, err := parseNanoTimestamp("1754035200000000000")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if ts.Unix() != 1754035200 {
		t.Errorf("时间戳错误: %v", ts)
	}
	if _, err := parseNanoTimestamp("not-a-time"); err == nil {
		t.Error("非法时间戳应报错")
	}
}
