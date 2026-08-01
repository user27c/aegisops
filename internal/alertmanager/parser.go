package alertmanager

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// 标签与注释的允许项。
const (
	// LabelAlertName 是必填告警名称标签。
	LabelAlertName = "alertname"
	// LabelNamespace 是必填命名空间标签。
	LabelNamespace = "namespace"
	// LabelWorkload 是工作负载名称标签（deployment/workload 二选一）。
	LabelWorkload = "workload"
	// LabelDeployment 是 Deployment 名称标签（兼容写法）。
	LabelDeployment = "deployment"
	// LabelSeverity 是严重级别标签。
	LabelSeverity = "severity"
	// LabelCluster 是集群标签（可选，用于多集群告警）。
	LabelCluster = "cluster"

	// 注释允许项。
	AnnotationSummary     = "summary"
	AnnotationDescription = "description"

	// 长度限制。
	maxLabelValueLength      = 128
	maxAnnotationValueLength = 1024
	maxAnnotationCount       = 8
	maxLabelCount            = 24

	// StatusFiring / StatusResolved 是告警状态。
	StatusFiring   = "firing"
	StatusResolved = "resolved"
)

// DecodeWebhook 从请求体解码 Alertmanager Webhook，限制最大字节数。
func DecodeWebhook(r io.Reader, maxBytes int64) (Webhook, error) {
	var hook Webhook
	dec := json.NewDecoder(io.LimitReader(r, maxBytes))
	if err := dec.Decode(&hook); err != nil {
		return Webhook{}, fmt.Errorf("解码 Webhook JSON: %w", err)
	}
	// 防止超大嵌套结构绕过 LimitReader。
	if dec.More() {
		return Webhook{}, fmt.Errorf("Webhook 存在多余 JSON 内容")
	}
	return hook, nil
}

// NormalizeAlert 归一化单条告警：校验必填标签、解析目标、过滤元数据。
func NormalizeAlert(clusterID string, groupKey string, alert Alert) (NormalizedAlert, error) {
	if alert.Status != StatusFiring && alert.Status != StatusResolved {
		return NormalizedAlert{}, fmt.Errorf("告警状态 %q 不合法，仅允许 firing/resolved", alert.Status)
	}
	if err := requireLabel(alert.Labels, LabelAlertName); err != nil {
		return NormalizedAlert{}, err
	}
	if err := requireLabel(alert.Labels, LabelNamespace); err != nil {
		return NormalizedAlert{}, err
	}

	target, err := ResolveTarget(alert.Labels)
	if err != nil {
		return NormalizedAlert{}, err
	}

	// 注释先截断再过滤，防止任意大 Map 进入 CR。
	annotations := SanitizeMetadata(alert.Annotations, allowedAnnotationKeys(), maxAnnotationCount, maxAnnotationValueLength)
	labels := SanitizeMetadata(alert.Labels, allowedLabelKeys(), maxLabelCount, maxLabelValueLength)

	severity := labels[LabelSeverity]
	if severity == "" {
		severity = "warning"
	}

	na := NormalizedAlert{
		Cluster:             clusterID,
		GroupKey:            groupKey,
		Status:              alert.Status,
		AlertName:           labels[LabelAlertName],
		Severity:            severity,
		Target:              target,
		Labels:              labels,
		Annotations:         annotations,
		StartsAt:            alert.StartsAt,
		EndsAt:              alert.EndsAt,
		UpstreamFingerprint: alert.Fingerprint,
	}
	return na, nil
}

// ResolveTarget 从标签解析目标工作负载引用。MVP 只支持 Deployment。
func ResolveTarget(labels map[string]string) (opsv1alpha1.TargetReference, error) {
	namespace := labels[LabelNamespace]
	name := labels[LabelWorkload]
	if name == "" {
		name = labels[LabelDeployment]
	}
	if name == "" {
		return opsv1alpha1.TargetReference{}, fmt.Errorf("缺少目标标签 workload 或 deployment")
	}
	return opsv1alpha1.TargetReference{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Namespace:  namespace,
		Name:       name,
	}, nil
}

// SanitizeMetadata 按白名单过滤并截断 Map；任何非白名单键（可能含 Secret）都被丢弃。
func SanitizeMetadata(in map[string]string, allowlist map[string]bool, maxCount int, maxValueLength int) map[string]string {
	out := make(map[string]string)
	for k, v := range in {
		if !allowlist[k] {
			continue
		}
		if len(out) >= maxCount {
			break
		}
		out[k] = truncateUTF8(v, maxValueLength)
	}
	return out
}

func allowedLabelKeys() map[string]bool {
	return map[string]bool{
		LabelAlertName: true, LabelNamespace: true, LabelWorkload: true,
		LabelDeployment: true, LabelSeverity: true, LabelCluster: true,
	}
}

func allowedAnnotationKeys() map[string]bool {
	return map[string]bool{AnnotationSummary: true, AnnotationDescription: true}
}

func requireLabel(labels map[string]string, key string) error {
	if v, ok := labels[key]; !ok || strings.TrimSpace(v) == "" {
		return fmt.Errorf("缺少必填标签 %s", key)
	}
	return nil
}

// truncateUTF8 按字节截断字符串，保证不截断 UTF-8 序列。
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := s[:maxBytes]
	for !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}
