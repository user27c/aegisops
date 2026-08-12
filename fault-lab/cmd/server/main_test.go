package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/user27c/aegisops/fault-lab/internal/faultlab"
)

func TestInjectCrashLoopSchedulesProcessExit(t *testing.T) {
	registry := faultlab.NewRegistry(true, time.Minute)
	if err := registry.Register(&faultlab.CrashLoopInjector{}); err != nil {
		t.Fatal(err)
	}
	server := &server{registry: registry}
	original := processExit
	t.Cleanup(func() { processExit = original })
	exited := make(chan int, 1)
	processExit = func(code int) { exited <- code }

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/inject?type=crashloop&duration=30", nil)
	server.inject(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	select {
	case code := <-exited:
		if code != 1 {
			t.Fatalf("exit code=%d, want 1", code)
		}
	case <-time.After(time.Second):
		t.Fatal("crashloop 未调度 host process exit")
	}
}
