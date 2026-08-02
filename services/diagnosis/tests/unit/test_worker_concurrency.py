"""Worker 并发上限测试:在途任务峰值不得超过配置。"""

from __future__ import annotations

import asyncio

import pytest
from app.worker import discard_finished, wait_for_capacity


async def _task(i: int, delay: float, fail: bool = False) -> None:
    await asyncio.sleep(delay)
    if fail:
        raise RuntimeError(f"task {i} failed")


def test_wait_for_capacity_blocks_until_slot_free() -> None:
    """达到上限时 wait_for_capacity 阻塞到至少一个任务结束。"""
    async def run() -> None:
        tasks: set[asyncio.Task[None]] = set()
        for i in range(2):
            tasks.add(asyncio.create_task(_task(i, 0.2)))
        # 2 个任务占满上限 2,再调用应立即等待。
        started = asyncio.get_event_loop().time()
        await wait_for_capacity(tasks, concurrency=2)
        elapsed = asyncio.get_event_loop().time() - started
        assert elapsed >= 0.15, f"应阻塞等待,实际 {elapsed:.3f}s"
        assert len(tasks) <= 1

    asyncio.run(run())


def test_wait_for_capacity_no_block_when_under_limit() -> None:
    async def run() -> None:
        tasks: set[asyncio.Task[None]] = set()
        tasks.add(asyncio.create_task(_task(0, 5)))
        started = asyncio.get_event_loop().time()
        await wait_for_capacity(tasks, concurrency=4)
        elapsed = asyncio.get_event_loop().time() - started
        assert elapsed < 0.5, "未达上限不应阻塞"
        # 清理
        tasks.pop().cancel()

    asyncio.run(run())


def test_wait_for_capacity_removes_finished_and_keeps_pending() -> None:
    async def run() -> None:
        tasks: set[asyncio.Task[None]] = set()
        tasks.add(asyncio.create_task(_task(1, 0.05)))  # 先结束
        tasks.add(asyncio.create_task(_task(2, 0.5)))  # 后结束
        await wait_for_capacity(tasks, concurrency=2)
        assert len(tasks) == 1
        # 清理
        for t in tasks:
            t.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)

    asyncio.run(run())


def test_discard_finished_reads_exception() -> None:
    """discard_finished 读取异常,避免 Task exception was never retrieved。"""
    async def run() -> None:
        tasks: set[asyncio.Task[None]] = set()
        tasks.add(asyncio.create_task(_task(0, 0.01, fail=True)))
        await asyncio.sleep(0.05)
        discard_finished(tasks)
        assert len(tasks) == 0

    asyncio.run(run())


def test_worker_concurrency_validation() -> None:
    """worker_concurrency 必须 1–32。"""
    from app.config import Settings
    from pydantic import ValidationError

    base = dict(
        database_url="postgresql+asyncpg://x:x@localhost/x",
        llm_provider="fake",
        embedding_model="fake",
    )
    assert Settings(**base, worker_concurrency=2).worker_concurrency == 2
    with pytest.raises(ValidationError):
        Settings(**base, worker_concurrency=0)
    with pytest.raises(ValidationError):
        Settings(**base, worker_concurrency=33)
