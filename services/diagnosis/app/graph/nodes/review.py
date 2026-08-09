"""review 节点：第二次调用扮演 Reviewer，检查证据支持、反证与越权。"""

from __future__ import annotations

from typing import Any

from app.graph.state import AnalysisState
from app.llm.base import LLMClient
from app.llm.deepseek import LLMError
from app.llm.prompts import PromptRegistry, render_prompt

# 模型自报的字段一律删除（Risk 由 Go Policy 决定）。
UNTRUSTED_FIELDS = {"actor", "risk", "mode", "priority"}

# 与提示词、operator 的告警分类提示对齐。拒绝别名可以让输出合同稳定，
# 同时避免把不受控的模型自由文本带入下游状态和指标。
KNOWN_CATEGORIES = {
    "OOMKilled",
    "CrashLoop",
    "ImagePullBackOff",
    "CheckoutFailure",
    "ProbeFailure",
    "CPUThrottling",
    "DependencyTimeout",
    "Unknown",
}


async def review_diagnosis(
    state: AnalysisState, llm: LLMClient, prompts: PromptRegistry
) -> dict[str, Any]:
    """审查诊断草稿。Reviewer 只输出 verdict/issues，不执行工具。"""
    draft = state.get("diagnosis_draft", {})
    template = prompts.get_reviewer()
    messages = render_prompt(
        template,
        incident=state.get("incident", {}),
        evidence=state.get("evidence", {}),
        chunks=state.get("retrieved_chunks", []),
        extra={"diagnosis": draft},
    )

    try:
        response = await llm.review(
            {
                "messages": messages,
                "template_version": template.version,
                "incident": state.get("incident", {}),
                "evidence": state.get("evidence", {}),
                "diagnosis": draft,
            }
        )
    except LLMError as exc:
        return {"errors": [{"code": exc.code, "message": str(exc)}]}

    content = response.content or {}
    verdict = str(content.get("verdict", "fail"))
    if verdict not in ("pass", "fail", "insufficient_evidence"):
        verdict = "fail"

    # 本地二次校验（不依赖 LLM 判断）。
    local_issues: list[str] = []
    if not draft.get("category"):
        local_issues.append("诊断缺少 category")
    elif draft.get("category") not in KNOWN_CATEGORIES:
        local_issues.append("诊断 category 不在受支持 taxonomy 中")
    if not draft.get("evidence_ids"):
        local_issues.append("诊断没有引用任何证据")
    if draft.get("proposal") is None and draft.get("confidence", 0) > 0.5:
        local_issues.append("高置信度却没有方案，矛盾")
    if draft.get("confidence", 0) > 1.0 or draft.get("confidence", 0) < 0.0:
        local_issues.append("置信度超出 [0,1]")
    for field in UNTRUSTED_FIELDS:
        if isinstance(draft.get("proposal"), dict) and field in draft["proposal"]:
            local_issues.append(f"方案包含模型自报字段 {field}，已忽略")

    issues = list(content.get("issues", [])) + local_issues
    passes = verdict == "pass" and not local_issues
    return {
        "review": {
            "verdict": verdict if not local_issues else "fail",
            "issues": issues,
            "pass": passes,
        }
    }
