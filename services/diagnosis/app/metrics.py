"""Diagnosis 服务 Prometheus 指标。

- 所有指标低基数;不记录 prompt/evidence/Token 原文。
- /metrics 公开(不要求 Bearer),由 NetworkPolicy/ServiceMonitor 限制访问。
"""

from __future__ import annotations

from typing import Any

from fastapi import Response
from prometheus_client import (
    CONTENT_TYPE_LATEST,
    Counter,
    Gauge,
    Histogram,
    generate_latest,
)

# ---- 任务与队列 ----
jobs_total = Counter(
    "aegisops_diagnosis_jobs_total",
    "诊断任务完成总数",
    ["status"],
)
queue_depth = Gauge(
    "aegisops_diagnosis_queue_depth",
    "队列深度(queued/processing/stale)",
    ["status"],
)
job_duration = Histogram(
    "aegisops_diagnosis_job_duration_seconds",
    "任务耗时",
    ["result"],
    buckets=(1, 5, 15, 30, 60, 120, 300),
)

# ---- LLM ----
llm_requests = Counter(
    "aegisops_llm_requests_total",
    "LLM 请求总数",
    ["provider", "operation", "result"],
)
llm_duration = Histogram(
    "aegisops_llm_request_duration_seconds",
    "LLM 请求耗时",
    ["provider", "operation"],
    buckets=(0.5, 1, 2, 5, 10, 30, 60),
)
llm_tokens = Counter(
    "aegisops_llm_tokens_total",
    "LLM Token 总量(只累计数值)",
    ["provider", "direction"],
)

# ---- RAG ----
rag_duration = Histogram(
    "aegisops_rag_retrieval_duration_seconds",
    "RAG 检索耗时",
    ["result"],
)
rag_candidates = Counter(
    "aegisops_rag_candidates",
    "RAG 候选数",
    ["stage"],
)

# ---- 审计与 DB ----
audit_writes = Counter(
    "aegisops_audit_write_total",
    "审计写入总数",
    ["severity", "result"],
)
db_pool_checked_out = Gauge(
    "aegisops_db_pool_checked_out",
    "DB 连接池当前占用",
)


def observe_llm_call(
    provider: str, operation: str, result: str, duration: float, usage: dict[str, Any] | None = None
) -> None:
    """记录一次 LLM 调用(成功/429/timeout 等)。"""
    llm_requests.labels(provider=provider, operation=operation, result=result).inc()
    llm_duration.labels(provider=provider, operation=operation).observe(duration)
    if usage:
        if usage.get("prompt_tokens"):
            llm_tokens.labels(provider=provider, direction="prompt").inc(usage["prompt_tokens"])
        if usage.get("completion_tokens"):
            llm_tokens.labels(provider=provider, direction="completion").inc(usage["completion_tokens"])


def observe_job_transition(old_status: str, new_status: str) -> None:
    """任务状态迁移。completed 只在新状态为 succeeded/failed 时计一次。"""
    if old_status == new_status:
        return
    if new_status in ("succeeded", "failed"):
        jobs_total.labels(status=new_status).inc()


def metrics_response() -> Response:
    """/metrics 端点(公开)。"""
    return Response(generate_latest(), media_type=CONTENT_TYPE_LATEST)
