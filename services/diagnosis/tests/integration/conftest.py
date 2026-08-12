"""集成测试公共 fixture：真实 pgvector PostgreSQL + Alembic 迁移。

test_postgres_api.py 与 diagnosis/ 下的 Worker 集成测试共享同一个会话级
容器与迁移。之所以必须共享：alembic env.py 通过 get_settings() 读取数据库
URL，而 get_settings 带 lru_cache——若每个测试文件各起一个容器并各自
command.upgrade，第二次迁移会命中缓存的第一个 URL，导致第二个容器未建表。
"""

from __future__ import annotations

import os
from collections.abc import Iterator
from pathlib import Path

import pytest
from alembic import command
from alembic.config import Config
from app.config import get_settings
from testcontainers.community.postgres import PostgresContainer

SERVICE_ROOT = Path(__file__).resolve().parents[2]


def _asyncpg_url(connection_url: str) -> str:
    """testcontainers 的 psycopg2 URL 转成应用使用的 asyncpg URL。"""
    return connection_url.replace("postgresql+psycopg2://", "postgresql+asyncpg://")


def _upgrade_database(database_url: str) -> None:
    """用真实 Alembic migration 初始化临时数据库。

    显式清空 get_settings 的 lru_cache，避免本次会话内更早的缓存污染迁移目标。
    """
    previous = os.environ.get("DATABASE_URL")
    os.environ["DATABASE_URL"] = database_url
    get_settings.cache_clear()
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
            f"真实 PostgreSQL 集成测试需要 Docker；请启动 Docker 或设置 AEGISOPS_INTEGRATION_DATABASE_URL。 原因: {exc}"
        )
