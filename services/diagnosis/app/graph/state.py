"""LangGraph 状态定义。

所有字段必须 JSON 可序列化；禁止放 ORM object、HTTP client、Exception。
"""

from __future__ import annotations

from typing import Any, TypedDict


class AnalysisState(TypedDict, total=False):
    """分析图状态。"""

    job_id: str
    incident: dict[str, Any]
    evidence: dict[str, Any]
    normalized: dict[str, Any]
    retrieved_chunks: list[dict[str, Any]]
    diagnosis_draft: dict[str, Any]
    review: dict[str, Any]
    final_result: dict[str, Any]
    errors: list[dict[str, str]]
    retry_count: int
