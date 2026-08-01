package evidence

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPPromClient_QueryRange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" {
			t.Errorf("路径错误: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
	}))
	defer srv.Close()

	c, err := NewHTTPPromClient(srv.URL, nil)
	if err != nil {
		t.Fatalf("NewHTTPPromClient: %v", err)
	}
	raw, err := c.QueryRange(context.Background(), "up", time.Now().Add(-5*time.Minute), time.Now(), 60)
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if len(raw) == 0 {
		t.Error("响应为空")
	}
}

func TestHTTPPromClient_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"parse error"}`))
	}))
	defer srv.Close()

	c, _ := NewHTTPPromClient(srv.URL, nil)
	if _, err := c.Query(context.Background(), "bad{", time.Now()); err == nil {
		t.Error("查询错误应上报")
	}
}

func TestHTTPPromClient_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c, _ := NewHTTPPromClient(srv.URL, nil)
	if _, err := c.Query(context.Background(), "up", time.Now()); err == nil {
		t.Error("HTTP 5xx 应报错")
	}
}

func TestHTTPPromClient_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(`{"status":"success","data":{}}`))
	}))
	defer srv.Close()

	c, err := NewHTTPPromClient(srv.URL, &http.Client{Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewHTTPPromClient: %v", err)
	}
	ctx := context.Background()
	if _, err := c.Query(ctx, "up", time.Now()); err == nil {
		t.Error("超时应报错")
	}
}
