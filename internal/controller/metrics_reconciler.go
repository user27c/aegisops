package controller

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	"github.com/user27c/aegisops/internal/observability"
)

// IncidentMetricsReconciler 每 30 秒整体重算活跃事故 gauge。
// 不做增量加减,避免 Reconcile 重放造成 Gauge 漂移。
type IncidentMetricsReconciler struct {
	Client   client.Client
	Metrics  *observability.Metrics
	Logger   logr.Logger
	Interval time.Duration
}

// Start 实现 manager.Runnable。
func (r *IncidentMetricsReconciler) Start(ctx context.Context) error {
	interval := r.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	r.recompute(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			r.recompute(ctx)
		}
	}
}

func (r *IncidentMetricsReconciler) recompute(ctx context.Context) {
	if r.Metrics == nil {
		return
	}
	incidents := &opsv1alpha1.AIOpsIncidentList{}
	if err := r.Client.List(ctx, incidents); err != nil {
		r.Logger.Error(err, "重算活跃事故指标失败")
		return
	}
	r.Metrics.ResetActiveIncidents()
	now := time.Now()
	// oldest 按 phase 聚合。
	oldest := map[string]time.Time{}
	oldestSev := map[string]string{}
	for i := range incidents.Items {
		inc := &incidents.Items[i]
		if inc.IsTerminal() {
			continue
		}
		phase := string(inc.Status.Phase)
		if phase == "" {
			phase = string(opsv1alpha1.PhaseDetected)
		}
		severity := string(inc.Spec.Severity)
		r.Metrics.ObserveActiveIncident(phase, severity, 1)
		// 该 phase 最近一次 timeline transition 时间(非 creationTimestamp)。
		phaseStart := inc.CreationTimestamp.Time
		if n := len(inc.Status.Timeline); n > 0 {
			phaseStart = inc.Status.Timeline[n-1].Time.Time
		}
		if old, ok := oldest[phase]; !ok || phaseStart.Before(old) {
			oldest[phase] = phaseStart
			oldestSev[phase] = severity
		}
	}
	for phase, start := range oldest {
		r.Metrics.ObserveOldestIncidentAge(phase, oldestSev[phase], now.Sub(start).Seconds())
	}
}
