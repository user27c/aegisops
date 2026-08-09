"""Worker stale-job recovery tests: no Docker or database required."""

from __future__ import annotations

import asyncio
from contextlib import AbstractAsyncContextManager
from datetime import UTC, datetime
from types import SimpleNamespace
from typing import Any

import app.worker as worker


class _FakeSession(AbstractAsyncContextManager["_FakeSession"]):
    def __init__(self) -> None:
        self.commits = 0

    async def __aenter__(self) -> _FakeSession:
        return self

    async def __aexit__(self, exc_type: Any, exc: Any, traceback: Any) -> None:
        return None

    async def commit(self) -> None:
        self.commits += 1


def test_requeue_stale_jobs_uses_short_transaction_and_commits(monkeypatch: Any) -> None:
    """恢复实际经由 PostgresJobRepository 执行，并在同一短事务内提交。"""
    session = _FakeSession()
    seen: dict[str, Any] = {}

    class FakeRepository:
        def __init__(self, received_session: _FakeSession) -> None:
            assert received_session is session

        async def requeue_stale(self, now: datetime) -> int:
            seen["now"] = now
            return 3

    monkeypatch.setattr(worker, "PostgresJobRepository", FakeRepository)
    deps = SimpleNamespace(factory=lambda: session)
    now = datetime(2026, 8, 9, tzinfo=UTC)

    count = asyncio.run(worker.requeue_stale_jobs(deps, now))

    assert count == 3
    assert seen["now"] == now
    assert session.commits == 1


def test_reaper_loop_limits_recovery_to_configured_interval(monkeypatch: Any) -> None:
    """连续空闲时不会高频扫表；第二次恢复至少等待一个周期。"""
    calls: list[float] = []
    stop = asyncio.Event()

    async def fake_requeue(_deps: Any, _now: datetime) -> int:
        calls.append(asyncio.get_running_loop().time())
        if len(calls) == 2:
            stop.set()
        return 0

    monkeypatch.setattr(worker, "requeue_stale_jobs", fake_requeue)

    asyncio.run(worker.reaper_loop(SimpleNamespace(), stop, interval_seconds=0.025))

    assert len(calls) == 2
    assert calls[1] - calls[0] >= 0.020


def test_worker_loop_starts_stale_recovery_task(monkeypatch: Any) -> None:
    """运行循环会实际启动 reaper，而非只定义未调用的函数。"""
    stop = asyncio.Event()
    reaper_started = asyncio.Event()

    async def fake_reaper(_deps: Any, received_stop: asyncio.Event) -> None:
        reaper_started.set()
        await received_stop.wait()

    async def fake_claim(_deps: Any, _worker_id: str) -> None:
        await reaper_started.wait()
        stop.set()
        return None

    async def no_sleep(_seconds: float) -> None:
        return None

    monkeypatch.setattr(worker, "reaper_loop", fake_reaper)
    monkeypatch.setattr(worker, "claim_one", fake_claim)
    monkeypatch.setattr(worker, "init_tracing", lambda *_args: None)
    monkeypatch.setattr(worker.asyncio, "sleep", no_sleep)
    settings = SimpleNamespace(worker_concurrency=1, otel_endpoint="")

    asyncio.run(worker.worker_loop(settings, SimpleNamespace(), stop))

    assert reaper_started.is_set()
