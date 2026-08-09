"""finalize 节点：本地 Pydantic/引用校验，生成最终结果。"""

from __future__ import annotations

from typing import Any

from pydantic import ValidationError

from app.api.schemas import DiagnosisResultModel, ReviewerModel
from app.graph.nodes.review import KNOWN_CATEGORIES
from app.graph.state import AnalysisState

# 已知 Action 集合（与 Policy Guard 对齐）。
KNOWN_ACTIONS = {
    "RestartWorkload",
    "ScaleDeployment",
    "PatchResourceLimit",
    "RollbackDeployment",
    "RestoreConfigMap",
}


def finalize_result(state: AnalysisState) -> dict[str, Any]:
    """本地校验并输出最终结果。

    - 重新做 Pydantic/引用校验；
    - 删除模型自报 actor/risk/mode；
    - Risk 由 Go Policy 决定；
    - 证据不足时强制 Proposal=None。
    """
    draft = state.get("diagnosis_draft", {})
    review = state.get("review", {})
    normalized = state.get("normalized", {})

    # 模型自报字段一律删除。
    proposal = draft.get("proposal")
    if isinstance(proposal, dict):
        proposal = {
            "action": proposal.get("action"),
            "parameters": proposal.get("parameters", {}),
        }
        if proposal["action"] not in KNOWN_ACTIONS:
            proposal = None

    evidence_ids = [e for e in draft.get("evidence_ids", []) if e]
    runbook_refs = [r for r in draft.get("runbook_refs", []) if r.startswith("runbook://")]
    category = draft.get("category", "Unknown") or "Unknown"
    if category not in KNOWN_CATEGORIES:
        # 不做别名归一化：保留严格 taxonomy 合同，外部结果安全降级为 Unknown。
        category = "Unknown"

    # 证据不足时强制无方案。
    if not normalized.get("evidence", {}).get("required_sources_present", False):
        proposal = None

    # Reviewer 不通过时降级。
    if not review.get("pass", False):
        proposal = None

    try:
        result = DiagnosisResultModel(
            category=category,
            root_cause=draft.get("root_cause", "") or "缺少根因描述",
            confidence=min(max(float(draft.get("confidence", 0.0)), 0.0), 1.0),
            evidence_ids=evidence_ids or ["(无)"],
            runbook_refs=runbook_refs,
            reviewer=ReviewerModel(
                verdict=review.get("verdict", "fail"),
                issues=review.get("issues", []),
                **{"pass": bool(review.get("pass", False))},
            ),
            proposal=proposal,
        )
    except ValidationError:
        # schema 校验失败：降级为"证据不足/无方案"而不是硬失败。
        result = DiagnosisResultModel(
            category="Unknown",
            root_cause="诊断结果 schema 校验失败，已降级",
            confidence=0.0,
            evidence_ids=["(无)"],
            reviewer=ReviewerModel(verdict="fail", issues=["schema 校验失败"], **{"pass": False}),
            proposal=None,
        )

    return {"final_result": result.model_dump(by_alias=True)}
