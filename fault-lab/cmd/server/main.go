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

// fault-lab 是受控故障演练应用。
//
// 端点：
//
//	/healthz /readyz          健康检查
//	/checkout                 业务接口（依赖超时注入时阻塞）
//	/inject?type=&duration=   注入故障（CHAOS_ENABLED=true 才允许）
//	/recover?type=            恢复单个故障
//	/cleanup                  恢复全部故障
//	/status                   注入状态
//	/metrics                  Prometheus 指标
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/user27c/aegisops/fault-lab/internal/faultlab"
)

var processExit = os.Exit

var (
	httpRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "faultlab_http_requests_total",
		Help: "HTTP 请求数",
	}, []string{"path", "code"})
	checkoutLatency = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "faultlab_checkout_duration_seconds",
		Help:    "/checkout 耗时",
		Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10, 30},
	})
)

type server struct {
	registry *faultlab.Registry
	config   *faultlab.ConfigInjector
	deps     *faultlab.DependencyInjector
}

func main() {
	chaosEnabled, _ := strconv.ParseBool(os.Getenv("CHAOS_ENABLED"))
	listenAddr := os.Getenv("PORT")
	if listenAddr == "" {
		listenAddr = ":8080"
	} else if !strings.HasPrefix(listenAddr, ":") {
		listenAddr = ":" + listenAddr
	}
	registry := faultlab.NewRegistry(chaosEnabled, 10*time.Minute)

	cfgInjector := &faultlab.ConfigInjector{}
	depInjector := &faultlab.DependencyInjector{}
	for _, i := range []faultlab.Injector{
		&faultlab.OOMInjector{}, &faultlab.CrashLoopInjector{}, cfgInjector,
		&faultlab.CPUInjector{}, depInjector,
	} {
		if err := registry.Register(i); err != nil {
			fmt.Fprintf(os.Stderr, "注册注入器失败: %v\n", err)
			os.Exit(1)
		}
	}

	s := &server{registry: registry, config: cfgInjector, deps: depInjector}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.healthz)
	mux.HandleFunc("/readyz", s.readyz)
	mux.HandleFunc("/checkout", s.checkout)
	mux.HandleFunc("/inject", s.inject)
	mux.HandleFunc("/recover", s.recover)
	mux.HandleFunc("/cleanup", s.cleanup)
	mux.HandleFunc("/status", s.status)
	mux.Handle("/metrics", promhttp.Handler())

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	startConfigModeWatcher(ctx, os.Getenv("FAULTLAB_CONFIG_PATH"), processExit)

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	fmt.Printf("fault-lab 启动 chaos=%v\n", chaosEnabled)

	select {
	case err := <-errCh:
		fmt.Fprintf(os.Stderr, "HTTP 服务异常退出: %v\n", err)
		os.Exit(1)
	case <-ctx.Done():
	}

	// 优雅退出前恢复全部故障。
	_ = registry.Cleanup()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func (s *server) healthz(w http.ResponseWriter, _ *http.Request) {
	httpRequests.WithLabelValues("/healthz", "200").Inc()
	_, _ = w.Write([]byte("ok"))
}

func (s *server) readyz(w http.ResponseWriter, _ *http.Request) {
	httpRequests.WithLabelValues("/readyz", "200").Inc()
	_, _ = w.Write([]byte("ok"))
}

// checkout 业务接口：配置错误时 500；依赖超时时阻塞。
func (s *server) checkout(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() {
		checkoutLatency.Observe(time.Since(start).Seconds())
	}()

	if s.config.Active() {
		httpRequests.WithLabelValues("/checkout", "500").Inc()
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "配置错误"})
		return
	}
	if lat := s.deps.Latency(); lat > 0 {
		select {
		case <-time.After(lat):
		case <-r.Context().Done():
			return
		}
	}
	httpRequests.WithLabelValues("/checkout", "200").Inc()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// inject 注入故障（需要 CHAOS_ENABLED）。
func (s *server) inject(w http.ResponseWriter, r *http.Request) {
	typ := r.URL.Query().Get("type")
	durationSec, _ := strconv.Atoi(r.URL.Query().Get("duration"))
	duration := time.Duration(durationSec) * time.Second
	if durationSec == 0 {
		duration = 30 * time.Second // 默认 30s
	}
	if err := s.registry.Inject(typ, duration); err != nil {
		if errors.Is(err, faultlab.ErrProcessTermination) {
			httpRequests.WithLabelValues("/inject", "200").Inc()
			writeJSON(w, http.StatusOK, map[string]string{"injected": typ, "duration": duration.String()})
			// Let the HTTP response leave the handler before terminating the
			// process.  This creates a real container exit for Kubernetes rather
			// than a recovered net/http handler panic.
			go func() {
				time.Sleep(50 * time.Millisecond)
				processExit(1)
			}()
			return
		}
		httpRequests.WithLabelValues("/inject", "400").Inc()
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	httpRequests.WithLabelValues("/inject", "200").Inc()
	writeJSON(w, http.StatusOK, map[string]string{"injected": typ, "duration": duration.String()})
}

// recover 恢复单个故障。
func (s *server) recover(w http.ResponseWriter, r *http.Request) {
	typ := r.URL.Query().Get("type")
	if err := s.registry.Recover(typ); err != nil {
		httpRequests.WithLabelValues("/recover", "400").Inc()
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	httpRequests.WithLabelValues("/recover", "200").Inc()
	writeJSON(w, http.StatusOK, map[string]string{"recovered": typ})
}

// cleanup 恢复全部故障。
func (s *server) cleanup(w http.ResponseWriter, _ *http.Request) {
	if err := s.registry.Cleanup(); err != nil {
		httpRequests.WithLabelValues("/cleanup", "500").Inc()
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	httpRequests.WithLabelValues("/cleanup", "200").Inc()
	writeJSON(w, http.StatusOK, map[string]string{"cleaned": "true"})
}

// status 返回注入状态。
func (s *server) status(w http.ResponseWriter, _ *http.Request) {
	httpRequests.WithLabelValues("/status", "200").Inc()
	writeJSON(w, http.StatusOK, s.registry.Status())
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
