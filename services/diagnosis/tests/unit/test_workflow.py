"""Fake LLM 与 graph 路由测试。"""

import asyncio

import pytest
from app.graph.nodes.diagnose import diagnose
from app.graph.nodes.finalize import finalize_result
from app.graph.nodes.review import review_diagnosis
from app.graph.workflow import route_after_review
from app.llm.fake import FakeClient
from app.llm.prompts import PromptRegistry

CRASH_SUMMARY = (
    "pod=checkout-api-5d455d6d45-5hb2q container=app ready=false restartCount=4 "
    "state=terminated:Error lastTermination={exitCode=1 reason=Error}"
)


def _state():
    return {
        "incident": {"uid": "u1", "category_hint": "CrashLoop", "severity": "warning"},
        "evidence": {
            "items": [
                {"id": "e1", "kind": "ContainerState", "summary": CRASH_SUMMARY},
                {"id": "evt-1", "kind": "KubernetesEvent", "summary": "type=Warning reason=BackOff message=x"},
            ]
        },
        "retrieved_chunks": [{"chunk_id": "k8s-crashloop-config-0", "document_id": "k8s-crashloop-config"}],
        "retry_count": 0,
        "errors": [],
    }


def test_fake_diagnosis_matches_crash_markers():
    state = _state()
    draft = asyncio.run(diagnose(state, FakeClient(), PromptRegistry()))
    d = draft["diagnosis_draft"]
    assert d["category"] == "CrashLoop"
    assert d["evidence_ids"] == ["e1"]


def test_fake_review_passes():
    state = _state()
    draft = asyncio.run(diagnose(state, FakeClient(), PromptRegistry()))
    review = asyncio.run(review_diagnosis({**state, **draft}, FakeClient(), PromptRegistry()))
    assert review["review"]["pass"] is True


def test_finalize_removes_untrusted_fields():
    state = _state()
    draft = asyncio.run(diagnose(state, FakeClient(), PromptRegistry()))
    review = asyncio.run(review_diagnosis({**state, **draft}, FakeClient(), PromptRegistry()))
    final = finalize_result({**state, **draft, **review})
    result = final["final_result"]
    # 模型自报字段必须被删除。
    proposal = result.get("proposal") or {}
    assert "actor" not in proposal
    assert "risk" not in proposal
    assert "mode" not in proposal


def test_finalize_downgrades_when_insufficient_evidence():
    state = _state()
    state["normalized"] = {"evidence": {"required_sources_present": False, "missing_required": ["ContainerState"]}}
    draft = {"diagnosis_draft": {
        "category": "CrashLoop", "root_cause": "x", "confidence": 0.9,
        "evidence_ids": ["e1"], "runbook_refs": [],
        "proposal": {"action": "RestartWorkload", "parameters": {}},
    }}
    review = {"review": {"verdict": "pass", "issues": [], "pass": True}}
    final = finalize_result({**state, **draft, **review})
    assert final["final_result"]["proposal"] is None  # 证据不足 → 强制无方案


def test_finalize_downgrades_when_review_fails():
    state = _state()
    draft = {"diagnosis_draft": {
        "category": "CrashLoop", "root_cause": "x", "confidence": 0.9,
        "evidence_ids": ["e1"], "runbook_refs": [],
        "proposal": {"action": "RestartWorkload", "parameters": {}},
    }}
    review = {"review": {"verdict": "fail", "issues": ["越权"], "pass": False}}
    final = finalize_result({**state, **draft, **review})
    assert final["final_result"]["proposal"] is None
    assert final["final_result"]["reviewer"]["pass"] is False


@pytest.mark.parametrize(
    "state,expected",
    [
        ({"review": {"pass": True}, "retry_count": 0, "errors": []}, "finalize"),
        ({"review": {"pass": False}, "retry_count": 0, "errors": []}, "diagnose"),
        ({"review": {"pass": False}, "retry_count": 1, "errors": []}, "finalize"),
        ({"review": {"pass": False}, "retry_count": 0, "errors": [{"code": "TIMEOUT"}]}, "finalize"),
        ({"review": {"pass": False}, "retry_count": 0, "errors": [{"code": "MISSING_REQUIRED_EVIDENCE"}]}, "finalize"),
    ],
)
def test_route_after_review(state, expected):
    assert route_after_review(state) == expected
