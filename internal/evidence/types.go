// Package evidence 采集并打包事故证据。
//
// 边界：只读外部数据（Kubernetes/Prometheus/Loki/Tempo），不修改任何资源。
// 模型不自行浏览集群：Operator 先生成有边界、可复放的证据包。
package evidence

import (
	"context"
	"encoding/json"
	"time"

	"k8s.io/apimachinery/pkg/types"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// ItemKind 是证据条目类型。
type ItemKind string

// 全部证据类型。
const (
	KindAlert           ItemKind = "Alert"
	KindKubernetesEvent ItemKind = "KubernetesEvent"
	KindPodState        ItemKind = "PodState"
	KindContainerState  ItemKind = "ContainerState"
	KindMetricSeries    ItemKind = "MetricSeries"
	KindLogExcerpt      ItemKind = "LogExcerpt"
	KindTraceSummary    ItemKind = "TraceSummary"
	KindRolloutDiff     ItemKind = "RolloutDiff"
	KindConfigHash      ItemKind = "ConfigHash"
	KindTargetSnapshot  ItemKind = "TargetSnapshot"
)

// EvidenceItem 是单条证据。
//
//nolint:revive // 蓝图指定名称 evidence.EvidenceItem
type EvidenceItem struct {
	// ID 是稳定标识（如 event-4、prom-2、log-7），供诊断引用。
	ID string `json:"id"`
	// Kind 是证据类型。
	Kind ItemKind `json:"kind"`
	// Source 是来源描述（如 "prometheus/container_memory_working_set"）。
	Source string `json:"source"`
	// Timestamp 是证据时间。
	Timestamp time.Time `json:"timestamp"`
	// Summary 是面向 LLM 的摘要（脱敏后）。
	Summary string `json:"summary"`
	// Payload 是原始数据（脱敏、截断后）。
	Payload json.RawMessage `json:"payload,omitempty"`
	// Truncated 标记是否被截断。
	Truncated bool `json:"truncated,omitempty"`
}

// TargetSnapshot 是目标工作负载的关键状态快照。
type TargetSnapshot struct {
	// Deployment 名称与 UID。
	Name string    `json:"name"`
	UID  types.UID `json:"uid"`
	// Generation / ResourceVersion 是观测值。
	Generation      int64  `json:"generation"`
	ResourceVersion string `json:"resourceVersion"`
	// Replicas 是期望/可用副本数。
	DesiredReplicas   int32 `json:"desiredReplicas"`
	AvailableReplicas int32 `json:"availableReplicas"`
	// ReadyReplicas 是就绪副本数。
	ReadyReplicas int32 `json:"readyReplicas"`
	// Paused 标记 rollout 是否暂停。
	Paused bool `json:"paused"`
	// ObservedAt 是快照时间。
	ObservedAt time.Time `json:"observedAt"`
}

// EvidencePack 是一次采集的完整证据包。
//
//nolint:revive // 蓝图指定名称 evidence.EvidencePack
type EvidencePack struct {
	// SchemaVersion 是包格式版本。
	SchemaVersion string `json:"schemaVersion"`
	// CollectorVersion 是采集器实现版本，参与哈希（采集逻辑变化使旧证据失效）。
	CollectorVersion string `json:"collectorVersion"`
	// IncidentUID 是事故 UID。
	IncidentUID types.UID `json:"incidentUID"`
	// Window 是采集时间窗口。
	Window TimeWindow `json:"window"`
	// Target 是目标快照。
	Target TargetSnapshot `json:"target"`
	// Items 是证据条目。
	Items []EvidenceItem `json:"items"`
	// Redactions 是脱敏事件汇总。
	Redactions []Redaction `json:"redactions,omitempty"`
	// Hash 是包内容哈希（幂等键与去重用）。
	Hash string `json:"hash"`
	// Partial 标记是否有可选源缺失。
	Partial bool `json:"partial,omitempty"`
	// MissingSources 是缺失的可选源。
	MissingSources []string `json:"missingSources,omitempty"`
}

// TimeWindow 是时间窗口。
type TimeWindow struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Collector 采集一次证据包。
type Collector interface {
	// Collect 返回完整证据包。必需源失败必须返回错误（不调用 LLM）。
	Collect(ctx context.Context, incident *opsv1alpha1.AIOpsIncident) (EvidencePack, error)
}

// TargetSnapshotter 采集目标快照（由 MultiCollector 内部使用）。
type TargetSnapshotter interface {
	Snapshot(ctx context.Context, ref opsv1alpha1.TargetReference) (TargetSnapshot, error)
}

// DefaultEvidenceWindow 是证据采集窗口（与 MultiCollector 的 Prom/Loki 窗口一致）。
const DefaultEvidenceWindow = 30 * time.Minute

// 限制常量（蓝图 4.3）。
const (
	// MaxPackBytes 是单个证据包 JSON 上限。
	MaxPackBytes = 512 * 1024
	// MaxLogLineBytes 是原始日志行上限。
	MaxLogLineBytes = 8 * 1024
	// MaxLogLinesPerSource 是单类日志最多行数。
	MaxLogLinesPerSource = 200
)
