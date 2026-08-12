// Package verifier 做单次健康检查并返回结果（不 sleep/poll）。
package verifier

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	"github.com/user27c/aegisops/internal/executor"
)

// Checker 是单次验证执行器。
type Checker interface {
	// Check 做一次无副作用检查。
	Check(ctx context.Context, incident *opsv1alpha1.AIOpsIncident, registry *executor.Registry, logger logr.Logger) (executor.Verification, error)
}

// KubernetesChecker 基于目标 Deployment 状态做验证。
type KubernetesChecker struct {
	Client client.Client
	Now    func() time.Time
	// ScaleMetrics 在生产装配中必需；测试可显式关闭 RequireScaleMetrics。
	ScaleMetrics        ScaleMetrics
	RequireScaleMetrics bool
}

// Check 执行动作的 Verify + 基础健康检查。
func (k *KubernetesChecker) Check(
	ctx context.Context,
	incident *opsv1alpha1.AIOpsIncident,
	registry *executor.Registry,
	logger logr.Logger,
) (executor.Verification, error) {
	if incident.Status.Proposal == nil {
		return executor.Verification{}, fmt.Errorf("方案为空")
	}
	action, err := registry.Get(incident.Status.Proposal.Action)
	if err != nil {
		return executor.Verification{}, err
	}

	// 动作的 Verify 需要快照：M5 用当前状态重建轻量快照。
	snap, err := action.Snapshot(ctx, &executor.Context{
		Client:   k.Client,
		Incident: incident,
		Proposal: *incident.Status.Proposal,
		Clock:    k.Now,
		Logger:   logger,
	})
	if err != nil {
		return executor.Verification{Healthy: false, Reason: "快照失败: " + err.Error()}, err
	}
	verification, err := action.Verify(ctx, &executor.Context{
		Client:   k.Client,
		Incident: incident,
		Proposal: *incident.Status.Proposal,
		Clock:    k.Now,
		Logger:   logger,
	}, snap)
	if err != nil || !verification.Healthy || incident.Status.Proposal.Action != opsv1alpha1.ActionScaleDeployment {
		return verification, err
	}
	if k.ScaleMetrics == nil {
		if k.RequireScaleMetrics {
			return executor.Verification{Healthy: false, Reason: "ScaleDeployment 缺少指标验证器"}, nil
		}
		return verification, nil
	}
	return k.ScaleMetrics.CheckScale(ctx, incident)
}
