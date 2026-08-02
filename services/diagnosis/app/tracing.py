"""诊断服务 OpenTelemetry 追踪。未配置 endpoint 时导出为 no-op。"""

from __future__ import annotations

from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.resources import SERVICE_NAME, Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor

_provider: TracerProvider | None = None


def init_tracing(endpoint: str, service_name: str) -> None:
    """初始化全局 tracer provider(幂等)。"""
    global _provider
    if _provider is not None:
        return
    resource = Resource.create({SERVICE_NAME: service_name})
    provider = TracerProvider(resource=resource)
    if endpoint:
        exporter = OTLPSpanExporter(endpoint=endpoint, insecure=True)
        provider.add_span_processor(BatchSpanProcessor(exporter))
    trace.set_tracer_provider(provider)
    _provider = provider


def get_tracer() -> trace.Tracer:
    return trace.get_tracer("aegisops-diagnosis")
