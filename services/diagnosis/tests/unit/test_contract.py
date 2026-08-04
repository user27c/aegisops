"""诊断服务 OpenAPI 契约测试(M9.4 契约对齐)。

docs/api-contracts.md 声明的诊断端点必须与 OpenAPI 实际路由一致;
声明了但未实现(或实现未声明)的端点在此测试中暴露,防止契约漂移。
"""

from __future__ import annotations

from app.config import Settings
from app.main import create_app
from fastapi.testclient import TestClient
from pydantic import SecretStr


def _openapi_paths() -> set[str]:
    settings = Settings(
        database_url=SecretStr("postgresql+asyncpg://t:t@localhost:5432/t"),
        llm_provider="fake",
    )
    client = TestClient(create_app(settings))
    return set(client.app.openapi()["paths"].keys())


def test_contract_endpoints_present() -> None:
    paths = _openapi_paths()
    expected = {
        "/healthz",
        "/readyz",
        "/v1/analyses",
        "/v1/analyses/{analysis_id}",
        "/v1/audit-events",
        "/v1/execution-snapshots",
        "/v1/execution-snapshots/{execution_id}",
        "/v1/evidence/{evidence_id}",
        "/v1/incidents/{incident_uid}/timeline",
    }
    missing = expected - paths
    assert not missing, f"契约端点未实现: {sorted(missing)}"


def test_contract_removed_endpoints_absent() -> None:
    spec = TestClient(
        create_app(
            Settings(
                database_url=SecretStr("postgresql+asyncpg://t:t@localhost:5432/t"),
                llm_provider="fake",
            )
        )
    ).app.openapi()
    # 契约已声明删除的端点:GET /v1/runbooks 与 GET /v1/audit-events(未实现)。
    # /v1/audit-events 的 POST(审计写入)是存在的,这里只禁止 GET。
    assert "/v1/runbooks" not in spec["paths"], "GET /v1/runbooks 已从契约删除,不应实现"
    methods = set(spec["paths"].get("/v1/audit-events", {}).keys())
    assert "get" not in methods, "GET /v1/audit-events 已从契约删除,不应实现"
