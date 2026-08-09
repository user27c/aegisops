package observability

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// TracingConfig 是 Trace 初始化配置。
type TracingConfig struct {
	// ServiceName 是服务名。
	ServiceName string
	// Endpoint 是 OTLP gRPC 端点；为空表示不导出（noop）。
	Endpoint string
}

// InitTracer 初始化 OTel Tracer。Endpoint 为空时返回 noop，不报错。
// 返回的 shutdown 用于退出时 flush（最大等待 5 秒）。
func InitTracer(ctx context.Context, cfg TracingConfig) (func(context.Context) error, error) {
	if cfg.Endpoint == "" {
		otel.SetTextMapPropagator(propagation.TraceContext{})
		return func(context.Context) error { return nil }, nil
	}

	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(cfg.Endpoint),
		// Helm 自动配置的是同集群 Collector 的明文 gRPC Service；TLS
		// 终止应由网格/Collector 边界处理，不能让 exporter 默认 TLS 而静默丢失 trace。
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithTimeout(3*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 OTLP exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(cfg.ServiceName)),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 OTel resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp, sdktrace.WithBatchTimeout(2*time.Second)),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return func(ctx context.Context) error {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return tp.Shutdown(shutdownCtx)
	}, nil
}
