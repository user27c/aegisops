"""normalize 节点：校验必需证据源、归一化单位与时间线。"""

from __future__ import annotations

from typing import Any

from app.graph.state import AnalysisState

# 必需证据源（蓝图 7.1）：缺少则证据不足。
REQUIRED_KINDS = {"ContainerState", "KubernetesEvent"}


def normalize_incident(state: AnalysisState) -> dict[str, Any]:
    """归一化 Incident 与证据，输出 compact data。"""
    incident = state.get("incident", {})
    evidence = state.get("evidence", {})

    items = evidence.get("items", [])
    kinds = {item.get("kind") for item in items}
    missing = sorted(REQUIRED_KINDS - kinds)
    has_required = not missing

    normalized: dict[str, Any] = {
        "incident": {
            "uid": incident.get("uid"),
            "namespace": incident.get("namespace"),
            "name": incident.get("name"),
            "alert": incident.get("category_hint"),
            "severity": incident.get("severity"),
            "target": incident.get("target"),
        },
        "evidence": {
            "required_sources_present": has_required,
            "missing_required": missing,
            "total_items": len(items),
            "partial": evidence.get("partial", False),
            "missing_sources": evidence.get("missingSources", []),
        },
        "errors": [],
    }

    errors: list[dict[str, str]] = []
    if not has_required:
        errors.append(
            {"code": "MISSING_REQUIRED_EVIDENCE", "message": f"缺少必需证据源: {missing}"}
        )
    if evidence.get("partial"):
        errors.append({"code": "PARTIAL_EVIDENCE", "message": "部分可选证据源缺失"})
    normalized["errors"] = errors
    return {"normalized": normalized, "errors": errors}
