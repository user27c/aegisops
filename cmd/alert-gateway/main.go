/*
Copyright 2026 AegisOps Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// alert-gateway 接收 Alertmanager Webhook，验证输入、计算事件指纹并创建或更新 Incident。
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/user27c/aegisops/internal/config"
	"github.com/user27c/aegisops/internal/observability"
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "alert-gateway 启动失败: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.LoadGateway(config.OSEnv{})
	if err != nil {
		return fmt.Errorf("加载配置: %w", err)
	}

	logger := observability.MustLogger(cfg.Common.LogLevel)
	shutdownTracer, err := observability.InitTracer(ctx, observability.TracingConfig{
		ServiceName: "alert-gateway",
		Endpoint:    cfg.Common.OTelEndpoint,
	})
	if err != nil {
		return fmt.Errorf("初始化 Trace: %w", err)
	}
	defer func() { _ = shutdownTracer(ctx) }()

	mux := http.NewServeMux()
	// M1 阶段实现 /webhooks/alertmanager；当前先注册健康检查与指标。
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/metrics", promhttp.Handler())

	srv := newHTTPServer(cfg, mux)
	logger.Info("alert-gateway 启动完成", "addr", cfg.ListenAddr)

	stopCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("HTTP 服务异常退出: %w", err)
	case <-stopCtx.Done():
		logger.Info("收到退出信号，优雅关闭")
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("优雅关闭失败: %w", err)
	}
	return nil
}

// newHTTPServer 构造带超时约束的 HTTP 服务。
func newHTTPServer(cfg config.GatewayConfig, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}
