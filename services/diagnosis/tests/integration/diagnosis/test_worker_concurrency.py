"""Worker 并发与过期任务回收：对真实 pgvector PostgreSQL 的集成测试。

覆盖四条关键事实（对应 docs/implementation-status.md 第 16 行的事实表缺口）：
(a) burst 下 Worker 峰值并发不超过 worker_concurrency（峰值 == 配置）；
(b) FOR UPDATE SKIP LOCKED 保证两个 Worker 不重复领取同一 Job；
(c) 心跳过期任务被回收 requeue，且 attempt 不超过 max_attempts；
(d) 单个任务抛异常不会阻塞队列，后续任务仍被领取。

测试以 fake LLM + monkeypatch 替换 process_job 驱动 worker_loop 的真实
claim/heartbeat/reaper 逻辑；DB 层（claim_next 的 FOR UPDATE SKIP LOCKED、
requeue_stale 的过期更新）全部走真实 PostgreSQL 实现，不注入离线 mock。
"""

from __future__ import annotations

import asyncio
import uuid
from datetime import UTC, datetime, timedelta

from app import worker
from app.config import Settings
from app.db.models import AnalysisJob, JobStatus
from app.db.repositories import PostgresJobRepository
from app.worker import WorkerDependencies
from pydantic import SecretStr
from sqlalchemy import select, update

BURST_SIZE = 20


def _settings(database_url: str, concurrency: int) -> Settings:
    return Settings(
        database_url=SecretStr(database_url),
        llm_provider="fake",
        embedding_model="fake",
        worker_concurrency=concurrency,
    )


async def _submit_burst(deps: WorkerDependencies, prefix: str, n: int) -> None:
    async with deps.factory() as session:
        repo = PostgresJobRepository(session)
        for i in range(n):
            await repo.submit(f"{prefix}-key-{i}", f"{prefix}-incident-{i}", None, "integration-v1")
        await session.commit()


async def _get_job(deps: WorkerDependencies, job_id: uuid.UUID) -> AnalysisJob:
    async with deps.factory() as session:
        result = await session.execute(select(AnalysisJob).where(AnalysisJob.id == job_id))
        return result.scalar_one()


async def _age_heartbeat(deps: WorkerDependencies, job_id: uuid.UUID, minutes: int) -> None:
    """把 heartbeat_at 推到 minutes 分钟前，模拟心跳过期。"""
    async with deps.factory() as session:
        await session.execute(
            update(AnalysisJob)
            .where(AnalysisJob.id == job_id)
            .values(heartbeat_at=datetime.now(UTC) - timedelta(minutes=minutes))
        )
        await session.commit()


async def _complete_job(deps: WorkerDependencies, job_id: uuid.UUID) -> None:
    async with deps.factory() as session:
        await PostgresJobRepository(session).complete(job_id, {"category": "integration"}, 0, 0)
        await session.commit()


async def test_worker_peak_concurrency_bounded_by_config(database_url: str) -> None:
    """(a) burst 下 Worker 峰值并发 == worker_concurrency，不得超过。"""
    settings = _settings(database_url, concurrency=2)
    deps = WorkerDependencies(settings)
    prefix = f"burst-{uuid.uuid4().hex}"
    try:
        await _submit_burst(deps, prefix, BURST_SIZE)

        peak = 0
        active = 0
        completed = 0
        lock = asyncio.Lock()

        async def fake_process_job(job: AnalysisJob, deps: WorkerDependencies, worker_id: str) -> None:
            nonlocal peak, active, completed
            del job, deps, worker_id
            async with lock:
                active += 1
                peak = max(peak, active)
            await asyncio.sleep(0.05)
            async with lock:
                active -= 1
                completed += 1

        original = worker.process_job
        worker.process_job = fake_process_job
        stop = asyncio.Event()
        loop_task = asyncio.create_task(worker.worker_loop(settings, deps, stop))
        try:
            for _ in range(200):
                if completed >= BURST_SIZE:
                    break
                await asyncio.sleep(0.05)
            assert completed >= BURST_SIZE, f"只处理了 {completed}/{BURST_SIZE} 个任务"
        finally:
            stop.set()
            worker.process_job = original
            await asyncio.wait_for(loop_task, timeout=15)

        assert peak == settings.worker_concurrency, (
            f"峰值并发 {peak} 应等于配置 worker_concurrency={settings.worker_concurrency}"
        )
    finally:
        await deps.dispose()


async def test_two_workers_do_not_double_claim(database_url: str) -> None:
    """(b) 两个 Worker 并发领取，FOR UPDATE SKIP LOCKED 保证每个 Job 恰好领取一次。"""
    settings = _settings(database_url, concurrency=2)
    deps = WorkerDependencies(settings)
    prefix = f"two-{uuid.uuid4().hex}"
    n = 10
    try:
        await _submit_burst(deps, prefix, n)

        done_ids: set[uuid.UUID] = set()

        async def fake_process_job(job: AnalysisJob, deps: WorkerDependencies, worker_id: str) -> None:
            del worker_id
            await _complete_job(deps, job.id)
            done_ids.add(job.id)

        original = worker.process_job
        worker.process_job = fake_process_job
        stop = asyncio.Event()
        loops = [asyncio.create_task(worker.worker_loop(settings, deps, stop)) for _ in range(2)]
        try:
            for _ in range(400):
                if len(done_ids) >= n:
                    break
                await asyncio.sleep(0.05)
        finally:
            stop.set()
            worker.process_job = original
            await asyncio.gather(*loops, return_exceptions=True)

        assert len(done_ids) >= n, f"只完成了 {len(done_ids)}/{n} 个任务"

        async with deps.factory() as session:
            result = await session.execute(select(AnalysisJob).where(AnalysisJob.incident_uid.like(f"{prefix}%")))
            jobs = list(result.scalars())

        assert len(jobs) == n
        assert all(j.status == JobStatus.SUCCEEDED for j in jobs)
        assert all(j.attempt == 1 for j in jobs), "存在被重复领取的 Job（attempt > 1）"
    finally:
        await deps.dispose()


async def test_stale_job_requeued_with_attempt_capped(database_url: str) -> None:
    """(c) 心跳过期任务被回收 requeue，且 attempt 不超过 max_attempts。"""
    settings = _settings(database_url, concurrency=2)
    deps = WorkerDependencies(settings)
    prefix = f"stale-{uuid.uuid4().hex}"
    try:
        async with deps.factory() as session:
            repo = PostgresJobRepository(session)
            job = await repo.submit(f"{prefix}-key", f"{prefix}-incident", None, "integration-v1")
            await session.commit()
        job_id = job.id

        # 1) 第一次领取 attempt=1。
        first = await worker.claim_one(deps, "worker-a")
        assert first is not None and first.id == job_id
        assert first.attempt == 1

        # 2) 老化 heartbeat 到 5 分钟前（超过 STALE_AFTER=2 分钟）。
        await _age_heartbeat(deps, job_id, minutes=5)

        # 3) reaper 回收 → queued，attempt 不因 requeue 递增。
        assert await worker.requeue_stale_jobs(deps, datetime.now(UTC)) == 1
        j = await _get_job(deps, job_id)
        assert j.status == JobStatus.QUEUED
        assert j.worker_id is None
        assert j.heartbeat_at is None
        assert j.attempt == 1

        # 4) 再次领取 attempt=2。
        second = await worker.claim_one(deps, "worker-b")
        assert second is not None and second.attempt == 2

        # 5) 再次老化，attempt 已达 max_attempts(2) → 不再回收，attempt 封顶。
        await _age_heartbeat(deps, job_id, minutes=5)
        assert await worker.requeue_stale_jobs(deps, datetime.now(UTC)) == 0
        j2 = await _get_job(deps, job_id)
        assert j2.status == JobStatus.PROCESSING, "attempt 达上限后过期任务不得再回 queued"
        assert j2.attempt == 2, "attempt 不得超过 max_attempts"
    finally:
        await deps.dispose()


async def test_failing_job_does_not_block_queue(database_url: str) -> None:
    """(d) 单个任务抛异常不会阻塞队列，后续任务仍被领取并完成。"""
    settings = _settings(database_url, concurrency=1)
    deps = WorkerDependencies(settings)
    prefix = f"fail-{uuid.uuid4().hex}"
    try:
        async with deps.factory() as session:
            repo = PostgresJobRepository(session)
            job1 = await repo.submit(f"{prefix}-key-1", f"{prefix}-incident-1", None, "integration-v1")
            job2 = await repo.submit(f"{prefix}-key-2", f"{prefix}-incident-2", None, "integration-v1")
            await session.commit()
        job1_id, job2_id = job1.id, job2.id

        async def fake_process_job(job: AnalysisJob, deps: WorkerDependencies, worker_id: str) -> None:
            del worker_id
            if job.id == job1_id:
                raise RuntimeError("boom")
            await _complete_job(deps, job.id)

        original = worker.process_job
        worker.process_job = fake_process_job
        stop = asyncio.Event()
        loop_task = asyncio.create_task(worker.worker_loop(settings, deps, stop))
        try:
            # 等待两个任务都到达终态：job1 因重试耗尽 FAILED，job2 成功。
            # 只等 job2 会与 job1 的重试时机产生竞态（job1 可能仍处于 queued/processing）。
            for _ in range(200):
                j1 = await _get_job(deps, job1_id)
                j2 = await _get_job(deps, job2_id)
                if j1.status == JobStatus.FAILED and j2.status == JobStatus.SUCCEEDED:
                    break
                await asyncio.sleep(0.05)
            j2 = await _get_job(deps, job2_id)
            assert j2.status == JobStatus.SUCCEEDED, "异常任务阻塞了队列，后续任务未被领取"
        finally:
            stop.set()
            worker.process_job = original
            await asyncio.wait_for(loop_task, timeout=15)

        # job1 经 retryable 失败重试后 attempt 耗尽 → FAILED，不再阻塞队列。
        j1 = await _get_job(deps, job1_id)
        assert j1.status == JobStatus.FAILED
        assert j1.error_code == "WORKER_ERROR"
    finally:
        await deps.dispose()
