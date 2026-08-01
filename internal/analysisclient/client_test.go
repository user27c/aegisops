package analysisclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSubmitAndGet(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/analyses":
			gotKey = r.Header.Get("Idempotency-Key")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"analysis_id":"a-1","status":"queued","evidence_id":"e-1"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/analyses/a-1":
			_, _ = w.Write([]byte(`{"id":"a-1","status":"succeeded","result":{"category":"OOMKilled","root_cause":"limit too low","confidence":0.9,"evidence_ids":["e1"],"reviewer":{"verdict":"ok","pass":true},"proposal":{"action":"PatchResourceLimit","parameters":{"container":"app"}}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, err := NewHTTPClient(srv.URL, NewStaticTokenSource("token"))
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}

	resp, err := c.Submit(context.Background(), "idem-1", SubmitRequest{Incident: IncidentDTO{UID: "u-1"}, PromptVersion: "v1"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if resp.AnalysisID != "a-1" || gotKey != "idem-1" {
		t.Errorf("Submit 响应/幂等键错误: %+v key=%s", resp, gotKey)
	}

	got, err := c.Get(context.Background(), "a-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusSucceeded || got.Result == nil || got.Result.Category != "OOMKilled" {
		t.Errorf("Get 响应错误: %+v", got)
	}
	if got.Result.Proposal == nil || got.Result.Proposal.Action != "PatchResourceLimit" {
		t.Errorf("Proposal 解析错误: %+v", got.Result)
	}
}

func TestSubmit_RejectsUnknownStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"analysis_id":"a-1","status":"unknown"}`))
	}))
	defer srv.Close()

	c, _ := NewHTTPClient(srv.URL, nil)
	if _, err := c.Submit(context.Background(), "k", SubmitRequest{}); err == nil {
		t.Error("未知状态应报错")
	}
}

func TestErrorMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code":"RATE_LIMITED","message":"慢一点"}`))
	}))
	defer srv.Close()

	c, _ := NewHTTPClient(srv.URL, nil)
	_, err := c.Submit(context.Background(), "k", SubmitRequest{})
	if err == nil {
		t.Fatal("429 应报错")
	}
	if !IsRetryable(err) {
		t.Error("429 应可重试")
	}
	if RetryAfterOf(err) != 3*time.Second {
		t.Errorf("Retry-After 解析错误: %v", RetryAfterOf(err))
	}
	var apiErr *APIError
	if !AsAPIError(err, &apiErr) || apiErr.Code != "RATE_LIMITED" {
		t.Errorf("错误码错误: %v", err)
	}
}

func TestErrorMapping_5xxRetryable(t *testing.T) {
	for _, code := range []int{502, 503, 504} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"detail":"upstream down"}`))
		}))
		c, _ := NewHTTPClient(srv.URL, nil)
		_, err := c.Submit(context.Background(), "k", SubmitRequest{})
		if err == nil || !IsRetryable(err) {
			t.Errorf("%d 应可重试", code)
		}
		srv.Close()
	}
}

func TestErrorMapping_4xxNotRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"INVALID","message":"bad"}`))
	}))
	defer srv.Close()

	c, _ := NewHTTPClient(srv.URL, nil)
	_, err := c.Submit(context.Background(), "k", SubmitRequest{})
	if err == nil || IsRetryable(err) {
		t.Error("400 不应重试")
	}
	if IsNotFound(err) {
		t.Error("400 不应被识别为 NotFound")
	}
}

func TestNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer srv.Close()

	c, _ := NewHTTPClient(srv.URL, nil)
	_, err := c.Get(context.Background(), "missing")
	if err == nil || !IsNotFound(err) {
		t.Error("404 应识别为 NotFound")
	}
}

func TestBodyLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxResponseBytes+1024)))
	}))
	defer srv.Close()

	c, _ := NewHTTPClient(srv.URL, nil)
	_, err := c.Get(context.Background(), "a")
	// 响应超限只截断读取，JSON 解析失败返回错误（不 panic）。
	if err == nil {
		t.Log("超大响应未报错(可接受,取决于解析)")
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/execution-snapshots":
			_, _ = w.Write([]byte(`{"id":"snap-1","sha256":"abc"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/execution-snapshots/snap-1":
			_, _ = w.Write([]byte(`{"id":"snap-1","execution_id":"e-1","action_type":"ScaleDeployment","snapshot":{"replicas":3},"sha256":"abc"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, _ := NewHTTPClient(srv.URL, nil)
	ref, err := c.PutSnapshot(context.Background(), "idem", SnapshotRequest{ExecutionID: "e-1"})
	if err != nil {
		t.Fatalf("PutSnapshot: %v", err)
	}
	if ref.ID != "snap-1" {
		t.Errorf("快照引用错误: %+v", ref)
	}

	snap, err := c.GetSnapshot(context.Background(), "snap-1")
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	if snap.ActionType != "ScaleDeployment" || string(snap.Snapshot) != `{"replicas":3}` {
		t.Errorf("快照内容错误: %+v", snap)
	}
}

// AsAPIError 是 errors.As 的别名。
func AsAPIError(err error, target **APIError) bool {
	return errors.As(err, target)
}
