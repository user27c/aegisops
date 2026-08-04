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

// incident-api 提供 Web 事故控制台后端：Incident/Policy 只读接口、审批创建与静态文件。
// 它不得创建 Executor，也不得直接修改工作负载。
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	"github.com/user27c/aegisops/internal/config"
	"github.com/user27c/aegisops/internal/httpapi"
	"github.com/user27c/aegisops/internal/observability"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(opsv1alpha1.AddToScheme(scheme))
}

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "incident-api 启动失败: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.LoadAPI(config.OSEnv{})
	if err != nil {
		return fmt.Errorf("加载配置: %w", err)
	}

	logger := observability.MustLogger(cfg.Common.LogLevel)
	shutdownTracer, err := observability.InitTracer(ctx, observability.TracingConfig{
		ServiceName: "incident-api",
		Endpoint:    cfg.Common.OTelEndpoint,
	})
	if err != nil {
		return fmt.Errorf("初始化 Trace: %w", err)
	}
	defer func() { _ = shutdownTracer(ctx) }()

	// 只读 K8s 客户端。
	k8sClient, err := client.New(ctrl.GetConfigOrDie(), client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("创建 K8s 客户端: %w", err)
	}

	// 认证。
	var auth httpapi.Authenticator
	switch cfg.AuthMode {
	case config.AuthModeDisabled:
		logger.Info("AUTH_MODE=disabled 仅用于本地开发")
		auth = &disabledAuthenticator{}
	case config.AuthModeStaticTokens:
		sa, err := httpapi.NewStaticTokenAuthenticator(cfg.StaticTokensFile)
		if err != nil {
			return fmt.Errorf("加载静态 Token: %w", err)
		}
		auth = sa
	default:
		return fmt.Errorf("未知 AUTH_MODE %q", cfg.AuthMode)
	}

	var diagnosis httpapi.DiagnosisReader
	if cfg.DiagnosisURL != "" {
		token, err := readTokenFile(cfg.DiagnosisTokenFile)
		if err != nil {
			return fmt.Errorf("读取诊断服务 Token: %w", err)
		}
		diagnosis = httpapi.NewDiagnosisClient(cfg.DiagnosisURL, token, 3*time.Second)
		logger.Info("诊断服务代理已启用", "url", cfg.DiagnosisURL)
	}

	handler, err := httpapi.NewServer(httpapi.ServerDeps{
		K8s:            k8sClient,
		Auth:           auth,
		StaticDir:      cfg.WebDistDir,
		AllowedOrigins: cfg.AllowedOrigins,
		Diagnosis:      diagnosis,
	})
	if err != nil {
		return fmt.Errorf("创建 HTTP 服务: %w", err)
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	logger.Info("incident-api 启动完成", "addr", cfg.ListenAddr, "authMode", cfg.AuthMode)

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

// disabledAuthenticator 仅本地开发：接受所有请求。
type disabledAuthenticator struct{}

func (d *disabledAuthenticator) Authenticate(_ *http.Request) (httpapi.Principal, error) {
	return httpapi.Principal{Subject: "local-dev", Roles: []httpapi.Role{httpapi.RoleViewer, httpapi.RoleApprover}}, nil
}

func (d *disabledAuthenticator) Middleware(next http.Handler) http.Handler {
	return next
}

// readTokenFile 读取单行 Token 文件（忽略空行与首尾空白），空文件视为错误。
func readTokenFile(path string) (string, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- 路径来自运维环境变量
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("token 文件 %s 内容为空", path)
	}
	return token, nil
}
