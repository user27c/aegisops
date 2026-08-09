"""Diagnosis API 对真实 PostgreSQL 的集成测试。

默认以 testcontainers 启动一个临时 pgvector 容器；CI 或开发机若已提供
数据库，可用 AEGISOPS_INTEGRATION_DATABASE_URL 复用它。缺少可用 Docker
必须失败，而不能把集成测试伪装成跳过成功。
"""

from __future__ import annotations

import os
from collections.abc import Iterator
from pathlib import Path
from uuid import uuid4

import pytest
from alembic import command
from alembic.config import Config
from app.config import Settings
from app.db.engine import create_engine, create_session_factory
from app.db.models import RunbookChunk
from app.db.repositories import PostgresRunbookRepository
from app.main import create_app
from fastapi.testclient import TestClient
from pydantic import SecretStr
from sqlalchemy import select
from testcontainers.community.postgres import PostgresContainer

SERVICE_ROOT = Path(__file__).resolve().parents[2]
INTEGRATION_TOKEN = "integration-token"  # noqa: S105 -- test-only service token.


def _asyncpg_url(connection_url: str) -> str:
    """testcontainers 的 psycopg2 URL 转成应用使用的 asyncpg URL。"""
    return connection_url.replace("postgresql+psycopg2://", "postgresql+asyncpg://")


def _upgrade_database(database_url: str) -> None:
    """用真实 Alembic migration 初始化临时数据库。"""
    previous = os.environ.get("DATABASE_URL")
    os.environ["DATABASE_URL"] = database_url
    try:
        config = Config(str(SERVICE_ROOT / "alembic.ini"))
        # alembic.ini 的相对 script_location 会受 pytest 当前工作目录影响；
        # 集成测试必须能从仓库根目录或服务目录一致运行。
        config.set_main_option("script_location", str(SERVICE_ROOT / "alembic"))
        command.upgrade(config, "head")
    finally:
        if previous is None:
            os.environ.pop("DATABASE_URL", None)
        else:
            os.environ["DATABASE_URL"] = previous


@pytest.fixture(scope="session")
def database_url() -> Iterator[str]:
    """提供已迁移到 head 的真实 pgvector PostgreSQL URL。"""
    external = os.environ.get("AEGISOPS_INTEGRATION_DATABASE_URL")
    if external:
        _upgrade_database(external)
        yield external
        return

    # 本测试使用 context manager 自行 stop 容器；禁用 Ryuk 可少拉取一个
    # Docker Hub 镜像，也避免它成为唯一的网络依赖。
    os.environ.setdefault("TESTCONTAINERS_RYUK_DISABLED", "true")
    try:
        with PostgresContainer(
            "pgvector/pgvector:pg16",
            username="aegisops",
            password="aegisops",  # noqa: S106 -- ephemeral testcontainer credentials.
            dbname="diagnosis",
        ) as postgres:
            url = _asyncpg_url(postgres.get_connection_url())
            _upgrade_database(url)
            yield url
    except Exception as exc:  # pragma: no cover - depends on host Docker diagnostics.
        pytest.fail(
            "真实 PostgreSQL 集成测试需要 Docker；"
            "请启动 Docker 或设置 AEGISOPS_INTEGRATION_DATABASE_URL。"
            f" 原因: {exc}"
        )


@pytest.fixture
def client(database_url: str) -> Iterator[TestClient]:
    settings = Settings(
        database_url=SecretStr(database_url),
        llm_provider="fake",
        embedding_model="fake",
        api_token=SecretStr(INTEGRATION_TOKEN),
        api_token_file="",
    )
    with TestClient(create_app(settings)) as test_client:
        assert test_client.get("/readyz").status_code == 200
        yield test_client


def _submit_payload(incident_uid: str, evidence_hash: str) -> dict[str, object]:
    return {
        "incident": {
            "uid": incident_uid,
            "namespace": "integration",
            "name": "checkout",
            "severity": "critical",
            "target": {"apiVersion": "apps/v1", "kind": "Deployment", "name": "checkout"},
        },
        "evidence": {
            "hash": evidence_hash,
            "items": [
                {
                    "id": "container-checkout",
                    "kind": "ContainerState",
                    "source": "kubernetes/container-status",
                    "summary": "pod=checkout ready=false",
                }
            ],
        },
        "prompt_version": "integration-v1",
    }


def test_submit_analysis_is_idempotent_with_real_postgres(client: TestClient) -> None:
    """相同幂等键经真实 API/事务只能创建一个 Job 与一份 Evidence。"""
    suffix = uuid4().hex
    headers = {"Authorization": f"Bearer {INTEGRATION_TOKEN}", "Idempotency-Key": f"integration-{suffix}"}
    payload = _submit_payload(f"incident-{suffix}", f"sha256:{suffix}")

    first = client.post("/v1/analyses", headers=headers, json=payload)
    second = client.post("/v1/analyses", headers=headers, json=payload)

    assert first.status_code == 202, first.text
    assert second.status_code == 202, second.text
    assert first.json()["analysis_id"] == second.json()["analysis_id"]
    assert first.json()["evidence_id"] == second.json()["evidence_id"]
    assert client.get(f"/v1/evidence/{first.json()['evidence_id']}", headers=headers).status_code == 200


def test_audit_api_preserves_idempotency_and_hash_chain(client: TestClient) -> None:
    """审计 API 必须用真实事务生成连续、可重放的 hash chain。"""
    incident_uid = f"incident-{uuid4().hex}"
    headers = {"Authorization": f"Bearer {INTEGRATION_TOKEN}"}
    first = client.post(
        "/v1/audit-events",
        headers={**headers, "Idempotency-Key": f"audit-first-{incident_uid}"},
        json={
            "incident_uid": incident_uid,
            "component": "integration-test",
            "event_type": "IncidentDetected",
            "payload": {"reason": "integration"},
        },
    )
    second = client.post(
        "/v1/audit-events",
        headers={**headers, "Idempotency-Key": f"audit-second-{incident_uid}"},
        json={
            "incident_uid": incident_uid,
            "component": "integration-test",
            "event_type": "EvidenceCollected",
            "payload": {"reason": "integration"},
        },
    )
    repeated = client.post(
        "/v1/audit-events",
        headers={**headers, "Idempotency-Key": f"audit-second-{incident_uid}"},
        json={
            "incident_uid": incident_uid,
            "component": "integration-test",
            "event_type": "EvidenceCollected",
            "payload": {"reason": "integration"},
        },
    )

    assert first.status_code == 201, first.text
    assert second.status_code == 201, second.text
    assert repeated.status_code == 201, repeated.text
    assert first.json()["sequence"] == 1
    assert first.json()["previous_hash"] == "genesis"
    assert second.json()["sequence"] == 2
    assert second.json()["previous_hash"] == first.json()["event_hash"]
    assert repeated.json() == second.json()


def test_execution_snapshot_round_trips_with_stable_hash(client: TestClient) -> None:
    """执行快照经真实 PostgreSQL 保存后必须可按 execution ID 完整读取。"""
    suffix = uuid4().hex
    execution_id = f"execution-{suffix}"
    headers = {
        "Authorization": f"Bearer {INTEGRATION_TOKEN}",
        "Idempotency-Key": f"snapshot-{suffix}",
    }
    payload = {
        "incident_uid": f"incident-{suffix}",
        "execution_id": execution_id,
        "action_type": "RestartWorkload",
        "resource_ref": {"apiVersion": "apps/v1", "kind": "Deployment", "name": "checkout"},
        "snapshot": {"replicas": 2, "annotations": {"ops.aegis.io/operation-id": "before"}},
    }

    stored = client.post("/v1/execution-snapshots", headers=headers, json=payload)
    repeated = client.post("/v1/execution-snapshots", headers=headers, json=payload)
    fetched = client.get(f"/v1/execution-snapshots/{execution_id}", headers=headers)

    assert stored.status_code == 201, stored.text
    assert repeated.status_code == 201, repeated.text
    assert fetched.status_code == 200, fetched.text
    assert stored.json()["id"] == repeated.json()["id"] == fetched.json()["id"]
    assert stored.json()["sha256"] == fetched.json()["sha256"]
    assert fetched.json()["snapshot"] == payload["snapshot"]


async def test_runbook_chunk_metadata_round_trips_with_real_postgres(database_url: str) -> None:
    """Runbook 分块应写入 ORM 的 metadata_json 字段，而不是同名 SQL 列别名。"""
    engine = create_engine(Settings(database_url=SecretStr(database_url)))
    factory = create_session_factory(engine)
    suffix = uuid4().hex
    document_id = f"runbook-{suffix}"
    try:
        async with factory() as session:
            repo = PostgresRunbookRepository(session)
            assert await repo.upsert_document(
                doc={
                    "document_id": document_id,
                    "version": "v1",
                    "path": f"runbooks/{document_id}.md",
                    "title": "Integration Runbook",
                    "category": "integration",
                    "metadata": {"owner": "integration-test"},
                    "content_hash": f"sha256:{suffix}",
                },
                chunks=[
                    {
                        "content": "## Symptoms\\nIntegration test chunk",
                        "metadata": {"section": "Symptoms", "source": document_id},
                        "textsearch": "Integration test chunk",
                        "embedding": None,
                        "content_hash": f"sha256:chunk-{suffix}",
                    }
                ],
            ) == "updated"
            await session.commit()

        async with factory() as session:
            result = await session.execute(
                select(RunbookChunk).where(RunbookChunk.content_hash == f"sha256:chunk-{suffix}")
            )
            chunk = result.scalar_one()
            assert chunk.metadata_json == {"section": "Symptoms", "source": document_id}
    finally:
        await engine.dispose()
