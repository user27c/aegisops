"""Evidence-first review contract tests.

覆盖 r5 D 组暴露的问题：OOM/CPU 等有充分直接证据的合规动作被 reviewer
误判为 insufficient_evidence，以及 Runbook 分类与证据冲突时错误降级。
"""

from __future__ import annotations

import asyncio
from typing import Any

from app.graph.nodes.review import review_diagnosis
from app.llm.base import LLMResponse
from app.llm.prompts import PromptRegistry


class FixedReviewClient:
    """固定 verdict 的 reviewer 替身，不访问网络。"""

    def __init__(self, verdict: str, model: str = "fake") -> None:
        self.verdict = verdict
        self.model = model

    async def review(self, prompt: dict[str, Any]) -> LLMResponse:
        return LLMResponse(
            content={
                "verdict": self.verdict,
                "issues": [],
                "pass": self.verdict == "pass",
            },
            model=self.model,
        )


def _oom_state() -> dict[str, Any]:
    return {
        "incident": {"uid": "u-oom", "category_hint": "OOMKilled", "severity": "critical"},
        "evidence": {
            "items": [
                {
                    "id": "e1",
                    "kind": "ContainerState",
                    "summary": (
                        "pod=faultlab-1 container=app ready=true restartCount=1 "
                        "state=running lastTermination={exitCode=137 reason=OOMKilled}"
                    ),
                },
                {
                    "id": "e2",
                    "kind": "KubernetesEvent",
                    "summary": (
                        "type=Warning reason=ControlledEvalEvidence "
                        "message=controlled observation: OOMKilled exitCode=137 observed after oom injector"
                    ),
                },
                {
                    "id": "e3",
                    "kind": "MetricSeries",
                    "summary": "各 Pod 内存 limit（bytes）",
                },
            ]
        },
        "retrieved_chunks": [],
        "retry_count": 0,
        "errors": [],
    }


def _oom_draft() -> dict[str, Any]:
    return {
        "diagnosis_draft": {
            "category": "OOMKilled",
            "root_cause": "memory limit 低于工作集",
            "confidence": 0.95,
            "evidence_ids": ["e1", "e2", "e3"],
            "runbook_refs": [],
            "proposal": {
                "action": "PatchResourceLimit",
                "parameters": {"container": "app", "memoryLimit": "384Mi"},
            },
        }
    }


def _cpu_state() -> dict[str, Any]:
    return {
        "incident": {"uid": "u-cpu", "category_hint": "CPUThrottling", "severity": "critical"},
        "evidence": {
            "items": [
                {
                    "id": "e0",
                    "kind": "ContainerState",
                    "summary": (
                        "pod=faultlab-1 container=app ready=true restartCount=0 "
                        "state=running lastTermination={exitCode=0 reason=}"
                    ),
                },
                {
                    "id": "e1",
                    "kind": "KubernetesEvent",
                    "summary": (
                        "type=Warning reason=ControlledEvalEvidence message=controlled observation: CPU injector active"
                    ),
                },
                {
                    "id": "e2",
                    "kind": "MetricSeries",
                    "summary": "各 Pod CPU 限流比例（0-1）",
                },
            ]
        },
        "retrieved_chunks": [],
        "retry_count": 0,
        "errors": [],
    }


def _cpu_draft() -> dict[str, Any]:
    return {
        "diagnosis_draft": {
            "category": "CPUThrottling",
            "root_cause": "CPU 限流持续",
            "confidence": 0.9,
            "evidence_ids": ["e0", "e1", "e2"],
            "runbook_refs": [],
            "proposal": {
                "action": "ScaleDeployment",
                "parameters": {"replicas": 2, "reason": "CPU 限流持续，按受控策略扩容"},
            },
        }
    }


def test_prompts_define_evidence_priority_and_strict_insufficient_contract() -> None:
    registry = PromptRegistry()
    diagnosis = registry.get_diagnosis()
    reviewer = registry.get_reviewer()

    assert diagnosis.version == "diagnosis-v4"
    assert "证据包是根因判断的最高优先级事实来源" in diagnosis.system
    assert "runbook_refs 缺失或 Runbook 分类与证据不一致都不是降级理由" in diagnosis.system

    assert reviewer.version == "reviewer-v4"
    assert "直接证据优先于 Runbook 分类" in reviewer.system
    assert "runbook_refs 为空" in reviewer.system
    assert "仅在证据包本身缺少判断根因或动作所需的必要来源/标记时使用" in reviewer.system


def test_local_contract_overrides_insufficient_for_oom_patch() -> None:
    state = {**_oom_state(), **_oom_draft()}
    review = asyncio.run(review_diagnosis(state, FixedReviewClient("insufficient_evidence"), PromptRegistry()))[
        "review"
    ]

    assert review["verdict"] == "pass"
    assert review["pass"] is True
    assert any("本地证据契约" in issue for issue in review["issues"])


def test_local_contract_overrides_insufficient_for_cpu_scale() -> None:
    state = {**_cpu_state(), **_cpu_draft()}
    review = asyncio.run(review_diagnosis(state, FixedReviewClient("insufficient_evidence"), PromptRegistry()))[
        "review"
    ]

    assert review["verdict"] == "pass"
    assert review["pass"] is True
    assert any("本地证据契约" in issue for issue in review["issues"])


def test_local_contract_keeps_insufficient_without_direct_marker() -> None:
    state = _oom_state()
    state["evidence"]["items"] = [
        {
            "id": "e1",
            "kind": "ContainerState",
            "summary": "pod=faultlab-1 container=app ready=true restartCount=0 state=running",
        }
    ]
    draft = _oom_draft()
    draft["diagnosis_draft"]["evidence_ids"] = ["e1"]
    review = asyncio.run(
        review_diagnosis({**state, **draft}, FixedReviewClient("insufficient_evidence"), PromptRegistry())
    )["review"]

    assert review["verdict"] == "insufficient_evidence"
    assert review["pass"] is False


def test_local_contract_requires_required_evidence_sources() -> None:
    state = _oom_state()
    state["evidence"]["items"] = [
        {
            "id": "e1",
            "kind": "KubernetesEvent",
            "summary": "message=controlled observation: OOMKilled exitCode=137 observed after oom injector",
        }
    ]
    review = asyncio.run(
        review_diagnosis({**state, **_oom_draft()}, FixedReviewClient("insufficient_evidence"), PromptRegistry())
    )["review"]

    assert review["verdict"] == "insufficient_evidence"
    assert review["pass"] is False


def test_local_contract_requires_action_specific_evidence() -> None:
    state = _oom_state()
    state["evidence"]["items"] = [item for item in state["evidence"]["items"] if item.get("kind") != "MetricSeries"]
    draft = _oom_draft()
    draft["diagnosis_draft"]["evidence_ids"] = ["e1", "e2"]
    review = asyncio.run(
        review_diagnosis({**state, **draft}, FixedReviewClient("insufficient_evidence"), PromptRegistry())
    )["review"]

    assert review["verdict"] == "insufficient_evidence"
    assert review["pass"] is False


def test_local_contract_never_overrides_fail() -> None:
    state = {**_oom_state(), **_oom_draft()}
    review = asyncio.run(review_diagnosis(state, FixedReviewClient("fail"), PromptRegistry()))["review"]

    assert review["verdict"] == "fail"
    assert review["pass"] is False


def test_local_contract_rejects_action_category_mismatch() -> None:
    state = _oom_state()
    draft = _oom_draft()
    draft["diagnosis_draft"]["proposal"] = {
        "action": "RestoreConfigMap",
        "parameters": {"targetConfigMap": "checkout-config"},
    }
    review = asyncio.run(
        review_diagnosis({**state, **draft}, FixedReviewClient("insufficient_evidence"), PromptRegistry())
    )["review"]

    assert review["verdict"] == "insufficient_evidence"
    assert review["pass"] is False


def test_local_contract_rejects_cpu_restart_mismatch() -> None:
    state = _cpu_state()
    draft = _cpu_draft()
    draft["diagnosis_draft"]["proposal"] = {
        "action": "RestartWorkload",
        "parameters": {"reason": "fake restart"},
    }
    review = asyncio.run(
        review_diagnosis({**state, **draft}, FixedReviewClient("insufficient_evidence"), PromptRegistry())
    )["review"]

    assert review["verdict"] == "insufficient_evidence"
    assert review["pass"] is False


def test_production_reviewer_fails_closed_for_cpu_scale() -> None:
    state = {**_cpu_state(), **_cpu_draft()}
    review = asyncio.run(review_diagnosis(state, FixedReviewClient("pass", model="deepseek"), PromptRegistry()))[
        "review"
    ]

    assert review["verdict"] == "fail"
    assert review["pass"] is False
    assert any("因果证据契约" in issue for issue in review["issues"])


def test_local_contract_keeps_fail_closed_without_evidence_ids() -> None:
    state = _oom_state()
    draft = _oom_draft()
    draft["diagnosis_draft"]["evidence_ids"] = []
    review = asyncio.run(
        review_diagnosis({**state, **draft}, FixedReviewClient("insufficient_evidence"), PromptRegistry())
    )["review"]

    assert review["verdict"] == "fail"
    assert review["pass"] is False
