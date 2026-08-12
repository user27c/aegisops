package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConfigModeWatcherExitsOnConfigMapMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mode")
	if err := os.WriteFile(path, []byte("healthy\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exited := make(chan int, 1)
	startConfigModeWatcher(ctx, path, func(code int) { exited <- code })

	select {
	case code := <-exited:
		t.Fatalf("healthy ConfigMap 不应触发退出: code=%d", code)
	case <-time.After(700 * time.Millisecond):
	}

	if err := os.WriteFile(path, []byte("crashloop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-exited:
		if code != 1 {
			t.Fatalf("退出码=%d, want 1", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ConfigMap crashloop 模式未触发进程退出")
	}
}

func TestReadConfigModeTrimsMountedValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mode")
	if err := os.WriteFile(path, []byte(" healthy\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mode, err := readConfigMode(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode != "healthy" {
		t.Fatalf("mode=%q, want healthy", mode)
	}
}
