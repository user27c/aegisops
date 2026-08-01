// Package alertmanager 接收并归一化 Alertmanager Webhook。
//
// 职责：验证输入、计算稳定指纹、去重并创建/更新 AIOpsIncident。
// 它是唯一能创建 Incident 的组件；只能写 Incident CR，不修改任何工作负载。
package alertmanager

import (
	"time"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// Webhook 是对应 Alertmanager v4 Webhook 的最小 DTO，不依赖巨大第三方模型。
type Webhook struct {
	// Version 是 Alertmanager 版本。
	Version string `json:"version"`
	// GroupKey 是告警分组键。
	GroupKey string `json:"groupKey"`
	// Status 是分组状态：firing / resolved。
	Status string `json:"status"`
	// Alerts 是组内告警列表。
	Alerts []Alert `json:"alerts"`
}

// Alert 是单条告警。
type Alert struct {
	// Status 是 firing / resolved。
	Status string `json:"status"`
	// Labels 是告警标签。
	Labels map[string]string `json:"labels"`
	// Annotations 是告警注释。
	Annotations map[string]string `json:"annotations"`
	// StartsAt 是告警开始时间。
	StartsAt time.Time `json:"startsAt"`
	// EndsAt 是告警结束时间（resolved 时有效）。
	EndsAt time.Time `json:"endsAt"`
	// Fingerprint 是 Alertmanager 计算的上游指纹。
	Fingerprint string `json:"fingerprint"`
	// GeneratorURL 是告警生成 URL（可选，写入注释需过滤）。
	GeneratorURL string `json:"generatorURL,omitempty"`
}

// NormalizedAlert 是归一化后的单条告警，可直接用于指纹与 Incident 创建。
type NormalizedAlert struct {
	// Cluster 是集群逻辑 ID。
	Cluster string
	// GroupKey 是上游分组键。
	GroupKey string
	// Status 是 firing / resolved。
	Status string
	// AlertName 是告警名称。
	AlertName string
	// Severity 是严重级别（缺省 critical 由上游决定，此处透传 labels）。
	Severity string
	// Target 是解析出的目标工作负载引用。
	Target opsv1alpha1.TargetReference
	// Labels 是过滤后的安全标签。
	Labels map[string]string
	// Annotations 是过滤后的安全注释。
	Annotations map[string]string
	// StartsAt / EndsAt 是时间窗口。
	StartsAt time.Time
	EndsAt   time.Time
	// UpstreamFingerprint 是 Alertmanager 指纹。
	UpstreamFingerprint string
}

// ProcessResult 是一次 Webhook 处理的结果汇总。
type ProcessResult struct {
	// Accepted 是成功处理的告警数。
	Accepted int `json:"accepted"`
	// Deduplicated 是去重（重复 firing）的告警数。
	Deduplicated int `json:"deduplicated"`
	// Rejected 是拒绝的告警数。
	Rejected int `json:"rejected"`
}

// ItemResult 是单条告警的处理结果。
type ItemResult struct {
	// Outcome 是 created / updated / deduplicated / resolved / rejected。
	Outcome string
	// IncidentName 是创建的 Incident 名称（如有）。
	IncidentName string
	// Reason 是拒绝原因（rejected 时）。
	Reason string
}

// UpsertResult 是 Incident 写入结果。
type UpsertResult struct {
	// IncidentName 是 Incident 名称。
	IncidentName string
	// Created 表示本次是新创建。
	Created bool
	// Updated 表示本次是更新已有对象。
	Updated bool
}

// 处理结果枚举。
const (
	OutcomeCreated      = "created"
	OutcomeUpdated      = "updated"
	OutcomeDeduplicated = "deduplicated"
	OutcomeResolved     = "resolved"
	OutcomeRejected     = "rejected"
)
