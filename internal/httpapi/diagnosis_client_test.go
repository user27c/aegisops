package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestGetEvidence_WindowNestedContract 验证诊断服务 evidence payload 的窗口
// 字段为嵌套对象 window:{start,end}(与 schemas.py EvidencePackModel 一致),
// 修复前 Go 读顶层 windowStart/windowEnd 导致窗口零值。
func TestGetEvidence_WindowNestedContract(t *testing.T) {
	payload := map[string]any{
		"schemaVersion": "v1",
		"window": map[string]string{
			"start": "2026-08-02T10:00:00Z",
			"end":   "2026-08-02T10:30:00Z",
		},
		"partial":        false,
		"missingSources": []string{},
		"redactions":     []map[string]any{{"pattern": "jwt"}},
		"hash":           "sha256:packhash",
		"items": []map[string]any{
			{"id": "k8s-1", "kind": "ContainerState", "source": "kubernetes",
				"timestamp": "2026-08-02T10:05:00Z", "summary": "OOMKilled exit 137"},
		},
	}
	raw, _ := json.Marshal(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/evidence/ev-1" {
			t.Errorf("意外路径: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer tkn" {
			t.Errorf("缺少 Bearer: %q", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "ev-1", "content_hash": "sha256:content", "schema_version": "v1",
			"payload": json.RawMessage(raw),
		})
	}))
	defer srv.Close()

	c := NewDiagnosisClient(srv.URL, "tkn", 3*time.Second)
	d, err := c.GetEvidence(context.Background(), "ev-1")
	if err != nil {
		t.Fatalf("GetEvidence 失败: %v", err)
	}
	wantStart := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 8, 2, 10, 30, 0, 0, time.UTC)
	if !d.WindowStart.Equal(wantStart) {
		t.Errorf("WindowStart 应为 %v(嵌套 window.start),得到 %v", wantStart, d.WindowStart)
	}
	if !d.WindowEnd.Equal(wantEnd) {
		t.Errorf("WindowEnd 应为 %v(嵌套 window.end),得到 %v", wantEnd, d.WindowEnd)
	}
	if d.Hash != "sha256:content" {
		t.Errorf("Hash 应为 content_hash: %s", d.Hash)
	}
	if d.Redactions != 1 {
		t.Errorf("Redactions 应为 1(脱敏事件数): %d", d.Redactions)
	}
	if len(d.Items) != 1 || d.Items[0].Summary != "OOMKilled exit 137" {
		t.Errorf("items 解析失败: %+v", d.Items)
	}
}

// TestGetEvidence_WindowTopLevelFallback 验证旧格式(顶层 windowStart/windowEnd)仍可解析。
func TestGetEvidence_WindowTopLevelFallback(t *testing.T) {
	payload := map[string]any{
		"windowStart": "2026-08-02T10:00:00Z",
		"windowEnd":   "2026-08-02T10:30:00Z",
		"items":       []map[string]any{},
		"redactions":  []map[string]any{},
	}
	raw, _ := json.Marshal(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "ev-2", "payload": json.RawMessage(raw)})
	}))
	defer srv.Close()

	c := NewDiagnosisClient(srv.URL, "", 3*time.Second)
	d, err := c.GetEvidence(context.Background(), "ev-2")
	if err != nil {
		t.Fatalf("GetEvidence 失败: %v", err)
	}
	if !d.WindowStart.Equal(time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("回退顶层 windowStart 失败: %v", d.WindowStart)
	}
}

// TestGetEvidence_OverSizeRejected 验证 >1MiB 响应被拒绝(ErrDiagnosisUnavailable)。
func TestGetEvidence_OverSizeRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"x","payload":"` + strings.Repeat("a", 1<<20+10) + `"}`))
	}))
	defer srv.Close()

	c := NewDiagnosisClient(srv.URL, "", 3*time.Second)
	if _, err := c.GetEvidence(context.Background(), "ev-x"); err == nil {
		t.Fatal("超大响应应被拒绝")
	}
}
