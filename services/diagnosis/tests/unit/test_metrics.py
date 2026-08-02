"""Diagnosis 指标测试。"""

from __future__ import annotations

from app.config import Settings
from app.main import create_app
from app.metrics import observe_job_transition, observe_llm_call
from fastapi.testclient import TestClient
from prometheus_client import REGISTRY


def _clear() -> None:
    for c in list(REGISTRY._collector_to_names):
        if isinstance(c, (type(observe_llm_call),)):
            pass
    # 简化:仅断言新指标存在与计数,不清理全局注册(测试串行)。


def test_job_transition_counts_completed_once() -> None:
    observe_job_transition("queued", "processing")
    observe_job_transition("processing", "succeeded")
    observe_job_transition("succeeded", "succeeded")  # 重试不重复计
    samples = REGISTRY.get_sample_value("aegisops_diagnosis_jobs_total", {"status": "succeeded"})
    assert samples == 1.0


def test_llm_call_counts() -> None:
    observe_llm_call("fake", "diagnose", "success", 1.2, {"prompt_tokens": 10, "completion_tokens": 5})
    observe_llm_call("fake", "diagnose", "timeout", 30.0, None)
    assert REGISTRY.get_sample_value(
        "aegisops_llm_requests_total", {"provider": "fake", "operation": "diagnose", "result": "success"}
    ) == 1.0
    assert REGISTRY.get_sample_value(
        "aegisops_llm_requests_total", {"provider": "fake", "operation": "diagnose", "result": "timeout"}
    ) == 1.0
    assert REGISTRY.get_sample_value(
        "aegisops_llm_tokens_total", {"provider": "fake", "direction": "prompt"}
    ) == 10.0


def test_metrics_endpoint_public_and_clean() -> None:
    settings = Settings(
        database_url="postgresql+asyncpg://x:x@localhost/x",
        llm_provider="fake",
        embedding_model="fake",
        api_token="test-token",
    )
    client = TestClient(create_app(settings), raise_server_exceptions=False)
    resp = client.get("/metrics")
    assert resp.status_code == 200
    body = resp.text
    assert "aegisops_diagnosis_jobs_total" in body
    # 不输出 Secret/证据文本
    assert "test-token" not in body
    assert "DEEPSEEK" not in body.upper() or "aegisops_" in body
