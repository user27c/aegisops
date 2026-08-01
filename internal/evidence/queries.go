package evidence

import (
	"fmt"
	"regexp"
	"strings"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// SafeLabels 是查询模板的受控参数。禁止 Incident Annotation 或 LLM 提供任意 PromQL。
type SafeLabels struct {
	// Namespace 是目标命名空间（必须精确匹配）。
	Namespace string
	// Workload 是目标工作负载名（用于 pod 前缀与 deployment 匹配）。
	Workload string
}

// QuerySpec 是一个注册的查询模板。
type QuerySpec struct {
	// ID 是稳定查询 ID（同时作为证据条目 ID）。
	ID string
	// Template 是 PromQL 模板，使用 {{.Namespace}}/{{.Workload}} 占位。
	Template string
	// Description 是面向 LLM 的摘要描述。
	Description string
}

// 注册的查询模板（蓝图 11.8）。只允许这些，LLM 与告警注释都不能注入任意 PromQL。
var queryTemplates = []QuerySpec{
	{
		ID:          "container_memory_working_set",
		Template:    `sum by (pod) (container_memory_working_set_bytes{namespace="{{.Namespace}}", pod=~"{{.Workload}}-.+"})`,
		Description: "各 Pod 内存工作集（bytes）",
	},
	{
		ID:          "container_memory_limit",
		Template:    `sum by (pod) (container_memory_limit_bytes{namespace="{{.Namespace}}", pod=~"{{.Workload}}-.+"})`,
		Description: "各 Pod 内存 limit（bytes）",
	},
	{
		ID:          "container_cpu_usage",
		Template:    `sum by (pod) (rate(container_cpu_usage_seconds_total{namespace="{{.Namespace}}", pod=~"{{.Workload}}-.+"}[5m]))`,
		Description: "各 Pod CPU 使用率（cores）",
	},
	{
		ID:          "container_cpu_throttled_ratio",
		Template:    `sum by (pod) (rate(container_cpu_cfs_throttled_periods_total{namespace="{{.Namespace}}", pod=~"{{.Workload}}-.+"}[5m]) / clamp_min(rate(container_cpu_cfs_periods_total{namespace="{{.Namespace}}", pod=~"{{.Workload}}-.+"}[5m]), 0.0001))`,
		Description: "各 Pod CPU 限流比例（0-1）",
	},
	{
		ID:          "workload_ready_replicas",
		Template:    `kube_deployment_status_replicas_available{namespace="{{.Namespace}}", deployment="{{.Workload}}"} or vector(0)`,
		Description: "Deployment 可用副本数",
	},
	{
		ID:          "http_error_ratio",
		Template:    `sum by (pod) (rate(http_requests_total{namespace="{{.Namespace}}", pod=~"{{.Workload}}-.+", status=~"5.."}[5m])) / clamp_min(sum by (pod) (rate(http_requests_total{namespace="{{.Namespace}}", pod=~"{{.Workload}}-.+"}[5m])), 0.0001)`,
		Description: "各 Pod HTTP 5xx 错误率",
	},
	{
		ID:          "http_latency_p95",
		Template:    `histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket{namespace="{{.Namespace}}", pod=~"{{.Workload}}-.+"}[5m])))`,
		Description: "HTTP P95 延迟（秒）",
	},
	{
		ID:          "container_restarts_delta",
		Template:    `sum by (pod) (increase(kube_pod_container_status_restarts_total{namespace="{{.Namespace}}", pod=~"{{.Workload}}-.+"}[30m]))`,
		Description: "各 Pod 30 分钟重启增量",
	},
}

// QueriesForIncident 返回该事故适用的查询列表。
func QueriesForIncident(_ *opsv1alpha1.AIOpsIncident) ([]QuerySpec, error) {
	return queryTemplates, nil
}

// RenderQuery 渲染模板并对标签值做 regex escape。
func RenderQuery(spec QuerySpec, labels SafeLabels) (string, error) {
	namespace := regexpEscape(labels.Namespace)
	workload := regexpEscape(labels.Workload)
	if namespace == "" || workload == "" {
		return "", fmt.Errorf("查询 %s 缺少 namespace/workload 参数", spec.ID)
	}
	query := strings.ReplaceAll(spec.Template, "{{.Namespace}}", namespace)
	query = strings.ReplaceAll(query, "{{.Workload}}", workload)
	return query, nil
}

// podSelectorFor 生成 LogQL 用的 Pod 前缀 selector。
func podSelectorFor(incident *opsv1alpha1.AIOpsIncident) (string, error) {
	workload := incident.Spec.TargetRef.Name
	if workload == "" {
		return "", fmt.Errorf("目标 workload 为空")
	}
	return regexpEscape(workload) + "-.+", nil
}

// regexpEscape 转义正则特殊字符。
func regexpEscape(s string) string {
	return regexp.QuoteMeta(s)
}
