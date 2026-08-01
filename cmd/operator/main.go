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

// aegisops-operator 驱动 AIOpsIncident 状态机、证据采集、策略校验与类型化执行。
// 本文件只负责依赖装配，不包含业务状态机。
package main

import (
	"context"
	"fmt"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	"github.com/user27c/aegisops/internal/config"
	"github.com/user27c/aegisops/internal/observability"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(opsv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func main() {
	if err := run(context.Background()); err != nil {
		setupLog.Error(err, "Operator 启动失败")
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.LoadOperator(config.OSEnv{})
	if err != nil {
		return fmt.Errorf("加载配置: %w", err)
	}

	logger := observability.MustLogger(cfg.Common.LogLevel)
	ctrl.SetLogger(logger)
	setupLog = logger.WithName("setup")

	shutdownTracer, err := observability.InitTracer(ctx, observability.TracingConfig{
		ServiceName: "aegisops-operator",
		Endpoint:    cfg.Common.OTelEndpoint,
	})
	if err != nil {
		return fmt.Errorf("初始化 Trace: %w", err)
	}
	defer func() { _ = shutdownTracer(ctx) }()

	mgr, err := buildManager(cfg, scheme)
	if err != nil {
		return err
	}

	if err := setupControllers(mgr, Dependencies{}); err != nil {
		return err
	}

	if err := setupHealthChecks(mgr); err != nil {
		return err
	}

	setupLog.Info("Operator 启动完成", "cluster", cfg.Common.ClusterID, "watchNamespaces", cfg.WatchNamespaces)
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("manager 运行失败: %w", err)
	}
	return nil
}

// buildManager 构建 controller-runtime Manager。
// MVP 阶段 metrics 使用明文 HTTP（集群内访问），不启用证书认证。
func buildManager(cfg config.OperatorConfig, scheme *runtime.Scheme) (ctrl.Manager, error) {
	metricsServerOptions := metricsserver.Options{
		BindAddress:   ":8080",
		SecureServing: false,
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhook.NewServer(webhook.Options{}),
		HealthProbeBindAddress: ":8081",
		LeaderElection:         cfg.LeaderElect,
		LeaderElectionID:       "aegisops-operator.ops.aegis.io",
		Cache:                  cache.Options{DefaultNamespaces: namespacesToCache(cfg.WatchNamespaces)},
	})
	if err != nil {
		return nil, fmt.Errorf("创建 manager: %w", err)
	}
	return mgr, nil
}

// setupControllers 注册全部控制器。M0 阶段为空；M2 起在此注册 Incident/Approval 控制器。
func setupControllers(mgr ctrl.Manager, deps Dependencies) error {
	_ = mgr
	_ = deps
	return nil
}

// setupHealthChecks 注册健康检查端点。
func setupHealthChecks(mgr ctrl.Manager) error {
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return fmt.Errorf("设置 healthz: %w", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return fmt.Errorf("设置 readyz: %w", err)
	}
	return nil
}

// Dependencies 是控制器依赖集合，M2 阶段填充具体实现。
type Dependencies struct{}

// namespacesToCache 把 WatchNamespaces 转为 controller-runtime 的按命名空间缓存配置。
// 空列表表示缓存全部命名空间。
func namespacesToCache(namespaces []string) map[string]cache.Config {
	if len(namespaces) == 0 {
		return nil
	}
	out := make(map[string]cache.Config, len(namespaces))
	for _, ns := range namespaces {
		out[ns] = cache.Config{}
	}
	return out
}
