package verifier

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	"github.com/user27c/aegisops/internal/evidence"
	"github.com/user27c/aegisops/internal/executor"
)

// ScaleMetrics 单次检查扩缩容后的业务指标；不得在实现中轮询或 sleep。
type ScaleMetrics interface {
	CheckScale(ctx context.Context, incident *opsv1alpha1.AIOpsIncident) (executor.Verification, error)
}

// PrometheusScaleMetrics 以容器 CPU 限流比例验证扩容是否实际恢复。
type PrometheusScaleMetrics struct {
	Prom              evidence.PromClient
	MaxThrottledRatio float64
	Now               func() time.Time
}

// CheckScale performs one Prometheus query and fails closed when its result is unavailable.
func (p *PrometheusScaleMetrics) CheckScale(ctx context.Context, incident *opsv1alpha1.AIOpsIncident) (executor.Verification, error) {
	if p.Prom == nil {
		return executor.Verification{Healthy: false, Reason: "ScaleDeployment 验证需要 Prometheus"}, nil
	}
	if p.MaxThrottledRatio < 0 || p.MaxThrottledRatio > 1 {
		return executor.Verification{}, fmt.Errorf("ScaleDeployment 限流阈值非法")
	}
	now := time.Now()
	if p.Now != nil {
		now = p.Now()
	}
	target := incident.Spec.TargetRef
	// container!="" 过滤掉 cAdvisor 的 pod 级聚合序列(无 container 标签),
	// 否则 sum by (pod) 会把 pod 级与 container 级的限流比例重复相加,
	// 得到 >1 的越界值而误判。
	query := fmt.Sprintf(`max(sum by (pod) (rate(container_cpu_cfs_throttled_periods_total{namespace=%q,pod=~%q,container!=""}[1m]) / clamp_min(rate(container_cpu_cfs_periods_total{namespace=%q,pod=~%q,container!=""}[1m]), 0.0001)))`, target.Namespace, target.Name+"-.+", target.Namespace, target.Name+"-.+")
	raw, err := p.Prom.Query(ctx, query, now)
	if err != nil {
		return executor.Verification{Healthy: false, Reason: fmt.Sprintf("Prometheus CPU 限流查询失败 (%T)", err)}, nil
	}
	var data struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Value []json.RawMessage `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return executor.Verification{}, fmt.Errorf("prometheus CPU 限流响应非法: %w", err)
	}
	if data.ResultType != "vector" || len(data.Result) != 1 || len(data.Result[0].Value) != 2 {
		return executor.Verification{Healthy: false, Reason: "Prometheus CPU 限流指标缺失"}, nil
	}
	var text string
	if err := json.Unmarshal(data.Result[0].Value[1], &text); err != nil {
		return executor.Verification{}, fmt.Errorf("prometheus CPU 限流值非法: %w", err)
	}
	ratio, err := strconv.ParseFloat(text, 64)
	if err != nil || ratio < 0 || ratio > 1 {
		return executor.Verification{}, fmt.Errorf("prometheus CPU 限流值越界: %q", text)
	}
	if ratio > p.MaxThrottledRatio {
		return executor.Verification{Healthy: false, Reason: fmt.Sprintf("CPU 限流比例 %.3f 高于阈值 %.3f", ratio, p.MaxThrottledRatio)}, nil
	}
	return executor.Verification{Healthy: true, Reason: fmt.Sprintf("CPU 限流比例 %.3f 已恢复", ratio)}, nil
}
