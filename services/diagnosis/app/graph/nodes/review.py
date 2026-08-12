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

# 与 diagnose/finalize 对齐的已知动作集合。
KNOWN_ACTIONS = {
    "RestartWorkload",
    "ScaleDeployment",
    "PatchResourceLimit",
    "RollbackDeployment",
    "RestoreConfigMap",
}

# 本地证据契约：只有证据标记明确、类别→动作关系无歧义的白名单组合，
# 才允许把 reviewer 过度保守的 insufficient_evidence 提升为 pass。
# 不在该表中的组合一律保持原 verdict，fail 永远不因本地启发式被放行。
_EVIDENCE_BACKED_ACTIONS: dict[str, set[str]] = {
    "OOMKilled": {"PatchResourceLimit"},
    "CPUThrottling": {"ScaleDeployment", "PatchResourceLimit"},
    "ImagePullBackOff": {"RollbackDeployment"},
}

# 真实模型的方案还必须满足更严格的因果契约。CPU 限流需要容量、流量与
# HPA 等上下文；CrashLoop 需要可确认的配置回退来源。仅凭当前 EvidencePack
# 不足以让通用模型安全推导这些变更，因此在生产模型路径 fail closed。Fake
# 仅用于隔离 E2E fixture，已由确定性测试数据与 Policy Guard 覆盖。
_PRODUCTION_EVIDENCE_BACKED_ACTIONS: dict[str, set[str]] = {
    "OOMKilled": {"PatchResourceLimit"},
    "ImagePullBackOff": {"RollbackDeployment"},
}

# 动作特有的额外证据要求：缺失时即使有类别标记也不允许本地放行。
_ACTION_REQUIRED_KINDS: dict[str, set[str]] = {
    "PatchResourceLimit": {"MetricSeries"},
    "ScaleDeployment": {"MetricSeries"},
    "RollbackDeployment": {"RolloutDiff"},
    "RestoreConfigMap": {"ConfigMapDiff", "ConfigMapState"},
    "RestartWorkload": set(),
}

_EVIDENCE_MARKERS: dict[str, tuple[str, ...]] = {
    "OOMKilled": ("oomkilled", "exitcode=137", "oomkilling", "oom injector", "内存 limit", "memory limit"),
    "CPUThrottling": ("cpu injector active", "throttl", "限流", "cpu 使用率"),
    "ImagePullBackOff": ("imagepullbackoff", "errimagepull", "failed to pull image", "failedtopullimage"),
}


def _direct_evidence_supports(
    category: str,
    action: str | None,
    evidence: Any,
    evidence_ids: Any,
) -> bool:
    """直接证据是否支持给定类别→动作组合（本地确定性检查）。"""
    if action not in KNOWN_ACTIONS:
        return False
    if action not in _EVIDENCE_BACKED_ACTIONS.get(category, set()):
        return False
    items = evidence.get("items", []) if isinstance(evidence, dict) else []
    if not isinstance(items, list) or not items:
        return False
    kinds = {item.get("kind") for item in items if isinstance(item, dict)}
    # 与 normalize 的必需证据源契约一致：缺少 ContainerState 或
    # KubernetesEvent 时不得由本地启发式放行。
    if not {"ContainerState", "KubernetesEvent"} <= kinds:
        return False
    required_extra = _ACTION_REQUIRED_KINDS.get(action, set())
    if required_extra and not required_extra & kinds:
        return False
    valid_ids = {item.get("id") for item in items if isinstance(item, dict)}
    if not isinstance(evidence_ids, list) or not evidence_ids:
        return False
    if any(evidence_id not in valid_ids for evidence_id in evidence_ids):
        return False
    summaries = " ".join(str(item.get("summary", "")) for item in items if isinstance(item, dict))
    text = summaries.lower()
    return any(marker.lower() in text for marker in _EVIDENCE_MARKERS.get(category, ()))


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

    proposal = draft.get("proposal")
    proposal_action = proposal.get("action") if isinstance(proposal, dict) else None
    evidence_supported = _direct_evidence_supports(
        draft.get("category", ""),
        proposal_action,
        state.get("evidence", {}),
        draft.get("evidence_ids", []),
    )
    overridden = False
    if verdict == "insufficient_evidence" and evidence_supported and not local_issues:
        # 证据优先契约：直接证据明确且动作属于白名单对应关系时，
        # 模型因 Runbook 缺失/分类冲突产生的过度保守判断由本地确定性检查覆盖。
        verdict = "pass"
        overridden = True

    if (
        response.model != "fake"
        and proposal_action is not None
        and proposal_action not in _PRODUCTION_EVIDENCE_BACKED_ACTIONS.get(draft.get("category", ""), set())
    ):
        local_issues.append("生产模型方案缺少可验证的因果证据契约，已 fail closed")

    issues = list(content.get("issues", [])) + local_issues
    if overridden:
        issues.append("本地证据契约：证据包直接支持诊断与合规动作")
    passes = verdict == "pass" and not local_issues
    return {
        "review": {
            "verdict": verdict if not local_issues else "fail",
            "issues": issues,
            "pass": passes,
        }
    }
