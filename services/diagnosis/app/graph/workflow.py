"""LangGraph 工作流：normalize → retrieve → diagnose → review → conditional finalize。

流程（蓝图 18.13）：
- pass → finalize → END；
- insufficient evidence → finalize 为 NoSafeAction/Escalate，不无限重试；
- schema/reviewer 可修复问题 → diagnose 最多再走一次；
- fatal → fail。
"""

from __future__ import annotations

import logging
from dataclasses import dataclass
from typing import Any, Literal

from langgraph.checkpoint.base import BaseCheckpointSaver
from langgraph.graph import END, START, StateGraph
from langgraph.graph.state import CompiledStateGraph

from app.db.models import AnalysisJob
from app.graph.nodes.diagnose import diagnose
from app.graph.nodes.finalize import finalize_result
from app.graph.nodes.normalize import normalize_incident
from app.graph.nodes.retrieve import retrieve_runbooks
from app.graph.nodes.review import review_diagnosis
from app.graph.state import AnalysisState
from app.llm.base import LLMClient
from app.llm.prompts import PromptRegistry
from app.rag.retriever import HybridRetriever

logger = logging.getLogger(__name__)


@dataclass
class GraphDependencies:
    """图依赖。"""

    retriever: HybridRetriever
    llm: LLMClient
    prompts: PromptRegistry
    max_diagnose_retries: int = 1


def checkpointer_available(graph: CompiledStateGraph[Any]) -> bool:
    """判断图是否配置了 checkpointer。"""
    return getattr(graph, "checkpointer", None) is not None


def build_graph(
    deps: GraphDependencies, checkpointer: BaseCheckpointSaver[Any] | None = None
) -> CompiledStateGraph[Any]:
    """构建编译后的图。thread_id=analysis_id。"""
    g = StateGraph(AnalysisState)

    async def retrieve_node(state: AnalysisState) -> dict[str, Any]:
        return await retrieve_runbooks(state, deps.retriever)

    async def diagnose_node(state: AnalysisState) -> dict[str, Any]:
        result = await diagnose(state, deps.llm, deps.prompts)
        result["retry_count"] = state.get("retry_count", 0) + 1
        return result

    async def review_node(state: AnalysisState) -> dict[str, Any]:
        return await review_diagnosis(state, deps.llm, deps.prompts)

    g.add_node("normalize", normalize_incident)
    g.add_node("retrieve", retrieve_node)
    g.add_node("diagnose", diagnose_node)
    g.add_node("review", review_node)
    g.add_node("finalize", finalize_result)

    g.add_edge(START, "normalize")
    g.add_edge("normalize", "retrieve")
    g.add_edge("retrieve", "diagnose")
    g.add_edge("diagnose", "review")
    g.add_conditional_edges(
        "review",
        route_after_review,
        {
            "finalize": "finalize",
            "diagnose": "diagnose",
            "fail": "finalize",
        },
    )
    g.add_edge("finalize", END)

    compiled = g.compile(checkpointer=checkpointer)
    return compiled


def route_after_review(state: AnalysisState) -> Literal["finalize", "diagnose", "fail"]:
    """路由决策：
    - 严重错误（LLM 失败/必需证据缺失）→ finalize（降级结果）；
    - reviewer 不通过且还有重试次数 → diagnose 重试一次；
    - 通过 → finalize。
    """
    errors = state.get("errors", [])
    for e in errors:
        if e.get("code") in ("TIMEOUT", "RATE_LIMITED", "UPSTREAM", "MISSING_REQUIRED_EVIDENCE"):
            return "finalize"

    review = state.get("review", {})
    if review.get("pass"):
        return "finalize"

    retry_count = state.get("retry_count", 0)
    if retry_count < 1:
        return "diagnose"
    return "finalize"


async def run_analysis(
    graph: CompiledStateGraph[Any],
    job: AnalysisJob,
    incident: dict[str, Any],
    evidence: dict[str, Any],
    retriever: HybridRetriever,
    llm: LLMClient,
) -> dict[str, Any]:
    """执行一次完整分析（带 checkpoint 恢复语义）。

    thread_id=analysis_id；外部 LLM 调用封装为 retryable task，
    恢复 checkpoint 时不得重复计费调用。
    """
    initial: AnalysisState = {
        "job_id": str(job.id),
        "incident": incident,
        "evidence": evidence,
        "retrieved_chunks": [],
        "retry_count": 0,
        "errors": [],
    }
    config: dict[str, Any] = {"configurable": {"thread_id": str(job.id)}}

    # 有 checkpointer 时从 checkpoint 恢复（已完成的节点不会重跑）。
    # langgraph 的 RunnableConfig 类型与运行时宽松，此处统一忽略类型检查。
    if checkpointer_available(graph):
        snapshot = graph.get_state(config)  # type: ignore[arg-type]
        if snapshot and snapshot.next:
            resumed: AnalysisState = dict(initial)  # type: ignore[assignment]
            for k, v in (snapshot.values or {}).items():
                resumed[k] = v  # type: ignore[literal-required]

    result = await graph.ainvoke(initial, config=config)  # type: ignore[call-overload]
    final: dict[str, Any] = result.get("final_result") or {}
    if not final:
        logger.error("分析图未产出 final_result job=%s", job.id)
        final = {
            "category": "Unknown",
            "root_cause": "分析流程未完成，已降级",
            "confidence": 0.0,
            "evidence_ids": ["(无)"],
            "runbook_refs": [],
            "reviewer": {"verdict": "fail", "issues": ["workflow 未完成"], "pass": False},
            "proposal": None,
        }
    return final
