"""评估入口的 provider 选择必须可离线验证。"""

from __future__ import annotations

import importlib.util
import sys
from asyncio import run
from pathlib import Path
import re
from types import ModuleType
from typing import Any

import pytest
from app.llm.base import LLMResponse
from app.llm.deepseek import DeepSeekClient
from app.llm.fake import FakeClient
from app.llm.prompts import PromptRegistry


def load_campaign() -> ModuleType:
    """按文件路径加载非 package 的评估脚本。"""
    path = Path(__file__).resolve().parents[4] / "eval" / "run_campaign.py"
    spec = importlib.util.spec_from_file_location("aegisops_eval_campaign", path)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


def test_fake_provider_uses_fake_client() -> None:
    campaign = load_campaign()

    assert isinstance(campaign.build_llm("fake"), FakeClient)


def test_deepseek_provider_uses_real_client_without_calling_network(monkeypatch: pytest.MonkeyPatch) -> None:
    campaign = load_campaign()
    monkeypatch.setenv("DEEPSEEK_API_KEY", "test-key-not-a-real-secret")

    llm = campaign.build_llm("deepseek")

    assert isinstance(llm, DeepSeekClient)
    assert llm.api_key == "test-key-not-a-real-secret"


@pytest.mark.parametrize("key", [None, "", "   "])
def test_deepseek_provider_requires_nonempty_key(
    monkeypatch: pytest.MonkeyPatch, key: str | None
) -> None:
    campaign = load_campaign()
    if key is None:
        monkeypatch.delenv("DEEPSEEK_API_KEY", raising=False)
    else:
        monkeypatch.setenv("DEEPSEEK_API_KEY", key)

    with pytest.raises(SystemExit, match="DEEPSEEK_API_KEY"):
        campaign.build_llm("deepseek")


def test_unknown_provider_fails_closed() -> None:
    campaign = load_campaign()

    with pytest.raises(SystemExit, match="未知 provider"):
        campaign.build_llm("other")


def test_default_output_is_timestamped_and_isolated() -> None:
    campaign = load_campaign()

    output_dir = campaign.resolve_output_dir("deepseek")

    assert output_dir.parent == campaign.EVAL_ROOT / "runs"
    assert re.fullmatch(r"deepseek-\d{8}T\d{6}Z", output_dir.name)


@pytest.mark.parametrize("protected", ["runs", "."])
def test_output_dir_refuses_historical_artifact_paths(protected: str) -> None:
    campaign = load_campaign()

    with pytest.raises(SystemExit, match="不能覆盖"):
        campaign.resolve_output_dir("fake", str(campaign.EVAL_ROOT / protected))


@pytest.mark.parametrize("name", ["deepseek-v2", "fake-v2"])
def test_output_dir_refuses_immutable_v2_artifacts(name: str) -> None:
    campaign = load_campaign()

    with pytest.raises(SystemExit, match="不能覆盖"):
        campaign.resolve_output_dir("deepseek", str(campaign.EVAL_ROOT / "runs" / name))


def test_output_dir_refuses_existing_campaign_outputs(tmp_path: Path) -> None:
    campaign = load_campaign()
    (tmp_path / "raw.jsonl").write_text("existing evidence\n", encoding="utf-8")

    with pytest.raises(SystemExit, match="拒绝覆盖"):
        campaign.resolve_output_dir("deepseek", str(tmp_path))


class _ContextCapturingClient:
    """离线替身：确认 campaign 传给 reviewer 的合同，而不访问网络。"""

    def __init__(self) -> None:
        self.review_prompt: dict[str, Any] | None = None

    async def generate_diagnosis(self, prompt: dict[str, Any]) -> LLMResponse:
        evidence_id = prompt["evidence"]["items"][0]["id"]
        return LLMResponse(
            content={
                "category": "OOMKilled",
                "root_cause": "test",
                "confidence": 0.9,
                "evidence_ids": [evidence_id],
                "runbook_refs": [],
                "proposal": {"action": "PatchResourceLimit", "parameters": {}},
            }
        )

    async def review(self, prompt: dict[str, Any]) -> LLMResponse:
        self.review_prompt = prompt
        return LLMResponse(content={"verdict": "pass", "issues": [], "pass": True})


def test_campaign_passes_incident_and_evidence_to_reviewer() -> None:
    campaign = load_campaign()
    client = _ContextCapturingClient()

    result = run(campaign.run_one(campaign.CASES[0], "clean", 0, client, PromptRegistry()))

    assert client.review_prompt is not None
    assert client.review_prompt["incident"]["uid"] == "uid-oomkilled-0"
    assert client.review_prompt["evidence"]["items"]
    assert result["reviewer_verdict"] == "pass"
    assert result["proposal_action_hit"] is True


def test_score_runs_keeps_no_action_samples_out_of_action_accuracy() -> None:
    campaign = load_campaign()
    runs = [
        {
            "ground_truth": {"category": "OOMKilled", "action": "PatchResourceLimit"},
            "strict_category_hit": True,
            "proposal_action_hit": False,
            "safe_no_action": None,
            "decision_contract_hit": False,
        },
        {
            "ground_truth": {"category": "Unknown", "action": None},
            "strict_category_hit": True,
            "proposal_action_hit": None,
            "safe_no_action": True,
            "decision_contract_hit": True,
        },
    ]

    metrics = campaign.score_runs(runs)

    assert len(metrics["actionable"]) == 1
    assert metrics["action_hits"] == 0
    assert len(metrics["no_action_expected"]) == 1
    assert metrics["safe_no_action_hits"] == 1
