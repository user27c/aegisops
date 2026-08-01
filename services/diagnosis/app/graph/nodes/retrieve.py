"""retrieve 节点：构造检索 query 并调用混合检索。

query 只用告警名、分类提示、事件 reason、退出码；不把整段日志当 query。
"""

from __future__ import annotations

from typing import Any

from app.graph.state import AnalysisState
from app.rag.retriever import HybridRetriever, RetrievalQuery


def build_query(state: AnalysisState) -> str:
    """从告警名/分类/事件 reason/退出码构造 query。"""
    incident = state.get("incident", {})
    evidence = state.get("evidence", {})
    parts: list[str] = []

    if incident.get("category_hint"):
        parts.append(str(incident["category_hint"]))
    if incident.get("alert"):
        parts.append(str(incident["alert"]))

    for item in evidence.get("items", []):
        summary = str(item.get("summary", ""))
        if item.get("kind") == "KubernetesEvent":
            # reason 形如 "OOMKilling"/"BackOff"/"FailedToPullImage"
            for token in ("OOM", "BackOff", "Pull", "Unhealthy", "Failed"):
                if token in summary:
                    parts.append(token)
        elif item.get("kind") == "ContainerState":
            for token in ("OOMKilled", "CrashLoopBackOff", "ImagePullBackOff"):
                if token in summary:
                    parts.append(token)

    return " ".join(dict.fromkeys(parts)) or "kubernetes incident"


async def retrieve_runbooks(
    state: AnalysisState, retriever: HybridRetriever
) -> dict[str, Any]:
    """检索 Top-K Runbook。metadata 先过滤 category/kind。"""
    query_text = build_query(state)
    category = state.get("incident", {}).get("category_hint")
    target = state.get("incident", {}).get("target", {})
    kind = target.get("kind") if isinstance(target, dict) else None

    chunks = await retriever.search(
        RetrievalQuery(
            text=query_text,
            category=category,
            workload_kind=kind,
            top_k=5,
        ),
        top_k=5,
    )
    return {
        "retrieved_chunks": [
            {
                "chunk_id": c.chunk_id,
                "document_id": c.document_id,
                "runbook_version": c.runbook_version,
                "category": c.category,
                "section": c.section,
                "content": c.content,
            }
            for c in chunks
        ]
    }
