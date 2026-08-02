"""诊断 Worker：异步领取 analysis_jobs 并执行 LangGraph 图。

- 每个 Worker 并发上限 settings.worker_concurrency；
- 领取 Job 后启动 heartbeat；
- Pod 终止时停止领新任务并等待当前任务最多 35 秒；
- stale heartbeat 任务在 attempt 未超限时回 queued。
"""

from __future__ import annotations

import asyncio
import contextlib
import logging
import uuid
from datetime import UTC, timedelta
from typing import Any

from app.config import Settings, get_settings
from app.db.engine import check_migration_head, create_engine, create_session_factory
from app.db.models import AnalysisJob
from app.db.repositories import (
    PostgresEvidenceRepository,
    PostgresJobRepository,
)
from app.graph.workflow import GraphDependencies, build_graph, run_analysis
from app.llm.base import LLMClient
from app.llm.deepseek import DeepSeekClient
from app.llm.fake import FakeClient
from app.llm.prompts import PromptRegistry
from app.rag.embedding import Embedder, FakeEmbedder, SentenceTransformerEmbedder
from app.rag.retriever import HybridRetriever
from app.tracing import get_tracer, init_tracing

logger = logging.getLogger(__name__)

# LangGraph 编译锁：避免并发 compile 触发 precompile artifact 冲突。
_GRAPH_COMPILE_LOCK = asyncio.Lock()

# 心跳间隔与过期阈值。
HEARTBEAT_INTERVAL = timedelta(seconds=15)
STALE_AFTER = timedelta(minutes=2)
# 优雅停机等待（蓝图：最多 35 秒）。
GRACEFUL_SHUTDOWN_SECONDS = 35


class WorkerDependencies:
    """Worker 依赖集合。"""

    def __init__(self, settings: Settings) -> None:
        self.settings = settings
        self.engine = create_engine(settings)
        self.factory = create_session_factory(self.engine)
        self.prompts = PromptRegistry()
        self.llm: LLMClient

        if settings.llm_provider == "fake":
            self.llm = FakeClient()
        else:
            if not settings.deepseek_api_key:
                raise ValueError("llm_provider=deepseek 必须配置 DEEPSEEK_API_KEY")
            self.llm = DeepSeekClient(
                api_key=settings.deepseek_api_key.get_secret_value(),
                base_url=str(settings.deepseek_base_url),
                model=settings.deepseek_model,
            )

        self.embedder: Embedder
        if settings.embedding_model and settings.embedding_model != "fake":
            self.embedder = SentenceTransformerEmbedder(
                settings.embedding_model, settings.embedding_cache_dir
            )
        else:
            self.embedder = FakeEmbedder()

    async def dispose(self) -> None:
        await self.engine.dispose()


async def wait_for_capacity(
    tasks: set[asyncio.Task[None]], concurrency: int
) -> None:
    """在任务数达到上限时等待至少一个任务结束，并传播/记录异常。"""
    if len(tasks) < concurrency:
        return
    done, pending = await asyncio.wait(tasks, return_when=asyncio.FIRST_COMPLETED)
    for t in done:
        exc = t.exception()
        if exc is not None:
            logger.error("在途任务异常: %s", exc)
        tasks.discard(t)
    tasks.update(pending)


def discard_finished(tasks: set[asyncio.Task[None]]) -> None:
    """移除已完成任务并读取 exception，避免 Task exception was never retrieved。"""
    finished = [t for t in tasks if t.done()]
    for t in finished:
        exc = t.exception()
        if exc is not None:
            logger.error("在途任务异常: %s", exc)
        tasks.discard(t)


async def claim_one(deps: WorkerDependencies, worker_id: str) -> AnalysisJob | None:
    """在独立短事务内领取一个任务。"""
    async with deps.factory() as session:
        repo = PostgresJobRepository(session)
        job = await repo.claim_next(worker_id, STALE_AFTER)
        if job is not None:
            await session.commit()
    return job


async def worker_loop(
    settings: Settings, deps: WorkerDependencies, stop: asyncio.Event
) -> None:
    """主循环：容量驱动并发领取并处理任务（在途任务数 ≤ worker_concurrency）。"""
    worker_id = f"worker-{uuid.uuid4().hex[:8]}"
    init_tracing(settings.otel_endpoint, "aegisops-diagnosis-worker")
    logger.info("Worker 启动 worker_id=%s concurrency=%d", worker_id, settings.worker_concurrency)

    tasks: set[asyncio.Task[None]] = set()

    try:
        while not stop.is_set():
            await wait_for_capacity(tasks, settings.worker_concurrency)
            if stop.is_set():
                break
            job = await claim_one(deps, worker_id)
            if job is None:
                await asyncio.sleep(2)
                continue
            task = asyncio.create_task(_process_wrapped(job, deps, worker_id))
            tasks.add(task)
    finally:
        logger.info("Worker 停止领取新任务，等待 %d 个在途任务（最多 %ds）", len(tasks), GRACEFUL_SHUTDOWN_SECONDS)
        if tasks:
            done, pending = await asyncio.wait(tasks, timeout=GRACEFUL_SHUTDOWN_SECONDS)
            for t in pending:
                t.cancel()


async def _process_wrapped(job: AnalysisJob, deps: WorkerDependencies, worker_id: str) -> None:
    """包装 process_job：捕获异常并标记失败。"""
    try:
        await process_job(job, deps, worker_id)
    except Exception as exc:  # noqa: BLE001
        logger.exception("任务处理异常 job=%s", job.id)
        async with deps.factory() as session:
            repo = PostgresJobRepository(session)
            await repo.fail(job.id, "WORKER_ERROR", str(exc)[:500], retryable=True)
            await session.commit()


async def process_job(job: AnalysisJob, deps: WorkerDependencies, worker_id: str) -> None:
    """处理单个任务：读取证据 → 构建图 → 执行 → 写结果。"""
    tracer = get_tracer()
    with tracer.start_as_current_span("diagnosis.process_job") as span:
        span.set_attribute("job.id", str(job.id))
        await _process_job_inner(job, deps, worker_id, tracer)
        if span.is_recording():
            span.set_attribute("job.result", "done")
    return


async def _process_job_inner(job: AnalysisJob, deps: WorkerDependencies, worker_id: str, tracer: Any) -> None:
    """process_job 主体(span 已包裹)。"""
    heartbeat_task = asyncio.create_task(heartbeat_loop(job.id, deps, worker_id, deps.settings))

    try:
        # 从 job.result 中恢复 incident 摘要（submit 时写入）。
        job_meta = job.result or {}
        incident: dict[str, Any] = job_meta.get("incident", {})

        async with deps.factory() as session:
            evidence_repo = PostgresEvidenceRepository(session)
            evidence_snapshot = await evidence_repo.get(job.evidence_id) if job.evidence_id else None
            evidence_pack = evidence_snapshot.payload if evidence_snapshot else {"items": []}

        async with deps.factory() as session:
            retriever = HybridRetriever(session, deps.embedder)
        # langgraph 并发 compile 同一图会触发 precompile artifact 注册冲突，
        # 用进程级锁串行化编译（编译本身毫秒级，不影响吞吐）。
        async with _GRAPH_COMPILE_LOCK:
            graph = build_graph(
                GraphDependencies(retriever=retriever, llm=deps.llm, prompts=deps.prompts)
            )

        result = await run_analysis(graph, job, incident, evidence_pack, retriever, deps.llm)

        usage = result.get("_usage", {})
        async with deps.factory() as session:
            repo = PostgresJobRepository(session)
            await repo.complete(
                job.id,
                result=result,
                input_tokens=int(usage.get("input", 0)),
                output_tokens=int(usage.get("output", 0)),
            )
            await session.commit()
        logger.info("任务完成 job=%s category=%s", job.id, result.get("category"))
    finally:
        heartbeat_task.cancel()
        with contextlib.suppress(asyncio.CancelledError):
            await heartbeat_task


async def heartbeat_loop(
    job_id: uuid.UUID,
    deps: WorkerDependencies,
    worker_id: str,
    settings: Settings,
) -> None:
    """心跳：处理期间定期刷新 heartbeat_at。"""
    while True:
        try:
            async with deps.factory() as session:
                repo = PostgresJobRepository(session)
                await repo.heartbeat(job_id, worker_id)
                await session.commit()
        except Exception:  # noqa: BLE001
            logger.warning("心跳失败 job=%s", job_id, exc_info=True)
        await asyncio.sleep(HEARTBEAT_INTERVAL.total_seconds())


async def reaper_loop(repo: PostgresJobRepository, stop: asyncio.Event) -> None:
    """把心跳过期的任务退回队列（attempt 未超限时）。"""
    from datetime import datetime

    while not stop.is_set():
        try:
            count = await repo.requeue_stale(datetime.now(UTC))
            if count:
                logger.info("重排队 %d 个过期任务", count)
        except Exception:  # noqa: BLE001
            logger.warning("reaper 运行失败", exc_info=True)
        await asyncio.sleep(60)


def run() -> None:
    """命令行入口。"""
    logging.basicConfig(
        level=getattr(logging, get_settings().log_level.upper(), logging.INFO),
        format="%(asctime)s %(levelname)s %(name)s %(message)s",
    )
    settings = get_settings()
    deps = WorkerDependencies(settings)
    stop = asyncio.Event()

    async def main() -> None:
        # 等待 migration 完成（migration hook 先于 worker 启动）。
        for _ in range(30):
            if await check_migration_head(deps.engine):
                break
            await asyncio.sleep(2)
        else:
            logger.error("数据库 migration 未就绪，Worker 退出")
            return

        await worker_loop(settings, deps, stop)

    try:
        asyncio.run(main())
    except KeyboardInterrupt:
        stop.set()
        logger.info("收到中断信号，正在退出")
    finally:
        asyncio.run(deps.dispose())


if __name__ == "__main__":
    run()
