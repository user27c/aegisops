// Package audit 提供追加式审计事件。
//
// Critical 事件失败必须 fail-closed（执行前审计不可用 → 拒绝执行）；
// BestEffort 事件失败只记日志，不阻塞流程。
package audit

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
)

// Severity 是审计事件分级。
type Severity string

// 分级常量。
const (
	// Critical 是必须成功写入的事件（执行前）。
	Critical Severity = "critical"
	// BestEffort 是可容忍失败的事件（状态记录）。
	BestEffort Severity = "best_effort"
)

// Event 是审计事件。
type Event struct {
	IncidentUID string
	Component   string
	EventType   string
	Actor       string
	Payload     map[string]any
	Severity    Severity
}

// Sink 是审计事件持久化目标。
type Sink interface {
	// Append 追加事件（幂等键保证重复调用不重复写）。
	Append(ctx context.Context, idempotencyKey string, e Event) error
}

// SinkFunc 是 Sink 的函数适配器。
type SinkFunc func(ctx context.Context, idempotencyKey string, e Event) error

// Append 实现 Sink。
func (f SinkFunc) Append(ctx context.Context, key string, e Event) error {
	return f(ctx, key, e)
}

// Writer 是审计写入器。
type Writer struct {
	sink   Sink
	logger logr.Logger
}

// NewWriter 创建审计写入器。
func NewWriter(sink Sink, logger logr.Logger) *Writer {
	return &Writer{sink: sink, logger: logger}
}

// Critical 写 Critical 事件；失败返回错误（调用方 fail-closed）。
func (w *Writer) Critical(ctx context.Context, key string, incidentUID, eventType, actor string, payload map[string]any) error {
	e := Event{
		IncidentUID: incidentUID,
		Component:   "controller",
		EventType:   eventType,
		Actor:       actor,
		Payload:     payload,
		Severity:    Critical,
	}
	if w.sink == nil {
		return fmt.Errorf("审计 sink 未配置")
	}
	if err := w.sink.Append(ctx, key, e); err != nil {
		return fmt.Errorf("审计写入失败(critical): %w", err)
	}
	return nil
}

// BestEffort 写 BestEffort 事件；失败只记日志。
func (w *Writer) BestEffort(ctx context.Context, key string, incidentUID, eventType, actor string, payload map[string]any) {
	e := Event{
		IncidentUID: incidentUID,
		Component:   "controller",
		EventType:   eventType,
		Actor:       actor,
		Payload:     payload,
		Severity:    BestEffort,
	}
	if w.sink == nil {
		return
	}
	if err := w.sink.Append(ctx, key, e); err != nil {
		w.logger.Error(err, "审计写入失败(best-effort)", "eventType", eventType)
	}
}
