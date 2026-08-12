"""diagnosis Worker 集成测试：测试间清理 analysis_jobs，隔离跨测试泄漏。

test (c) 会把一个 attempt 已到上限的 processing 任务留在库里，若不清理，
test (d) 的 worker 会把它当作 stale 任务领走。每个测试前清空 analysis_jobs
可让各测试只看到自己提交的任务，消除顺序/时序依赖。
"""

from __future__ import annotations

from collections.abc import AsyncIterator

import pytest
from app.config import Settings
from app.db.engine import create_engine
from pydantic import SecretStr
from sqlalchemy import text


@pytest.fixture(autouse=True)
async def _clean_analysis_jobs(database_url: str) -> AsyncIterator[None]:
    engine = create_engine(Settings(database_url=SecretStr(database_url)))
    try:
        async with engine.begin() as conn:
            await conn.execute(text("DELETE FROM analysis_jobs"))
    finally:
        await engine.dispose()
    yield
