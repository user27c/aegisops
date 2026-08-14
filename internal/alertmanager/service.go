package alertmanager

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/clock"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	"github.com/user27c/aegisops/internal/observability"
)

// TargetResolver 解析目标工作负载的 UID，用于指纹与审计绑定。
type TargetResolver interface {
	// ResolveTargetUID 返回目标 Deployment 的 UID；目标不存在时返回错误（fail closed）。
	ResolveTargetUID(ctx context.Context, ref opsv1alpha1.TargetReference) (types.UID, error)
}

// IncidentWriter 创建或更新 AIOpsIncident。
type IncidentWriter interface {
	// UpsertFromAlert 按指纹去重写入 Incident。
	UpsertFromAlert(ctx context.Context, a NormalizedAlert) (UpsertResult, error)
}

// Metrics 是 Gateway 需要的指标接口（由 observability.Metrics 实现）。
type Metrics interface {
	// IncidentsCreated 记录新建事故。
	IncidentsCreated()
	// IncidentsDeduplicated 记录去重事故。
	IncidentsDeduplicated()
	// IncidentsRejected 记录被拒绝的告警。
	IncidentsRejected(reason string)
}

// Service 处理 Alertmanager Webhook 并维护 Incident。
type Service struct {
	clusterID string
	writer    IncidentWriter
	resolver  TargetResolver
	clock     clock.Clock
	metrics   Metrics
	logger    logr.Logger
}

// NewService 创建告警处理服务。
func NewService(clusterID string, writer IncidentWriter, resolver TargetResolver, clk clock.Clock, metrics Metrics) *Service {
	return &Service{
		clusterID: clusterID,
		writer:    writer,
		resolver:  resolver,
		clock:     clk,
		metrics:   metrics,
	}
}

// SetLogger 设置日志器（拒绝审计）。
func (s *Service) SetLogger(l logr.Logger) {
	s.logger = l
}

// Process 处理整个 Webhook，返回汇总结果。整体 JSON 解码失败返回错误。
func (s *Service) Process(ctx context.Context, hook Webhook) (ProcessResult, error) {
	result := ProcessResult{}
	for _, alert := range hook.Alerts {
		item := s.processOne(ctx, hook.GroupKey, alert)
		switch item.Outcome {
		case OutcomeCreated, OutcomeUpdated:
			result.Accepted++
		case OutcomeDeduplicated:
			result.Accepted++
			result.Deduplicated++
		case OutcomeResolved:
			result.Accepted++
		case OutcomeRejected:
			result.Rejected++
		default:
			return result, fmt.Errorf("未知处理结果 %q", item.Outcome)
		}
	}
	return result, nil
}

// processOne 处理单条告警，绝不 panic；所有失败都归为 rejected。
func (s *Service) processOne(ctx context.Context, groupKey string, alert Alert) ItemResult {
	ctx, span := observability.Tracer("alert-gateway").Start(ctx, "alert.process",
		trace.WithAttributes(
			attribute.String("alertname", alert.Labels["alertname"]),
			attribute.String("fingerprint", alert.Fingerprint),
			attribute.String("cluster.id", s.clusterID),
		),
	)
	defer span.End()

	_, normSpan := observability.Tracer("alert-gateway").Start(ctx, "alert.normalize")
	na, err := NormalizeAlert(s.clusterID, groupKey, alert)
	normSpan.End()
	if err != nil {
		span.RecordError(err)
		return s.rejectItem("normalize: " + err.Error())
	}

	resolveCtx, resolveSpan := observability.Tracer("alert-gateway").Start(ctx, "alert.resolve_target")
	uid, err := s.resolver.ResolveTargetUID(resolveCtx, na.Target)
	resolveSpan.End()
	if err != nil {
		span.RecordError(err)
		return s.rejectItem("resolve-target: " + err.Error())
	}
	na.Target.UID = uid

	writeCtx, writeSpan := observability.Tracer("alert-gateway").Start(ctx, "incident.upsert")
	res, err := s.writer.UpsertFromAlert(writeCtx, na)
	writeSpan.End()
	if err != nil {
		span.RecordError(err)
		return s.rejectItem("write: " + err.Error())
	}

	span.SetAttributes(
		attribute.String("incident.name", res.IncidentName),
		attribute.String("incident.namespace", na.Target.Namespace),
	)

	if s.metrics != nil {
		switch {
		case res.Created:
			s.metrics.IncidentsCreated()
		case res.Updated && na.Status != StatusResolved:
			s.metrics.IncidentsDeduplicated()
		}
	}

	outcome := OutcomeDeduplicated
	switch {
	case res.Created:
		outcome = OutcomeCreated
	case na.Status == StatusResolved:
		outcome = OutcomeResolved
	case res.Updated:
		// 重复 firing：只更新 LastReceivedAt 等字段，算去重。
		outcome = OutcomeDeduplicated
	}
	return ItemResult{Outcome: outcome, IncidentName: res.IncidentName}
}

func (s *Service) rejectItem(reason string) ItemResult {
	if s.metrics != nil {
		s.metrics.IncidentsRejected(reason)
	}
	if s.logger.GetSink() != nil {
		s.logger.Info("告警被拒绝", "reason", reason)
	}
	return ItemResult{Outcome: OutcomeRejected, Reason: reason}
}
