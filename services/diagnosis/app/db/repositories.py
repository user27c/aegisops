"""数据访问层：协议与 PostgreSQL 实现。

核心约束：claim_next 必须使用 SELECT ... FOR UPDATE SKIP LOCKED，
同一 Job 同时只能被一个 Worker 领取。
"""

from __future__ import annotations

import hashlib
import json
import uuid
from datetime import UTC, datetime, timedelta
from typing import Any, Protocol, cast

from app.db.models import (
    AnalysisJob,
    AuditEvent,
    EvidenceSnapshot,
    ExecutionSnapshot,
    JobStatus,
    Runbook,
    RunbookChunk,
)
from sqlalchemy import CursorResult, select, text, update
from sqlalchemy.ext.asyncio import AsyncSession


def utcnow() -> datetime:
    """UTC 当前时间。"""
    return datetime.now(UTC)


class JobRepository(Protocol):
    """分析任务仓库协议。"""

    async def submit(
        self, idempotency_key: str, incident_uid: str, evidence_id: uuid.UUID | None, prompt_version: str
    ) -> AnalysisJob: ...
    async def get(self, job_id: uuid.UUID) -> AnalysisJob | None: ...
    async def get_by_idempotency_key(self, key: str) -> AnalysisJob | None: ...
    async def claim_next(self, worker_id: str, stale_after: timedelta) -> AnalysisJob | None: ...
    async def heartbeat(self, job_id: uuid.UUID, worker_id: str) -> None: ...
    async def complete(
        self, job_id: uuid.UUID, result: dict[str, Any], input_tokens: int, output_tokens: int
    ) -> None: ...
    async def fail(self, job_id: uuid.UUID, code: str, message: str, retryable: bool) -> None: ...
    async def requeue_stale(self, now: datetime) -> int: ...


class EvidenceRepository(Protocol):
    """证据仓库协议。"""

    async def upsert(
        self,
        incident_uid: str,
        pack: dict[str, Any],
        content_hash: str,
        redaction_count: int,
    ) -> uuid.UUID: ...
    async def get(self, evidence_id: uuid.UUID) -> EvidenceSnapshot | None: ...


class RunbookRepository(Protocol):
    """Runbook 仓库协议。"""

    async def upsert_document(self, doc: dict[str, Any], chunks: list[dict[str, Any]]) -> str: ...
    async def deactivate(self, document_id: str) -> None: ...
    async def list_active(self) -> list[Runbook]: ...


class AuditRepository(Protocol):
    """审计仓库协议（hash chain 追加）。"""

    async def append(
        self, incident_uid: str, idempotency_key: str, component: str,
        event_type: str, actor: str | None, payload: dict[str, Any],
    ) -> AuditEvent: ...
    async def find_by_key(self, idempotency_key: str) -> AuditEvent | None: ...
    async def timeline(self, incident_uid: str, limit: int = 200) -> list[AuditEvent]: ...


class SnapshotRepository(Protocol):
    """执行快照仓库协议。"""

    async def put(
        self, incident_uid: str, execution_id: str, action_type: str,
        resource_ref: dict[str, Any], snapshot: dict[str, Any], content_hash: str,
    ) -> ExecutionSnapshot: ...
    async def get(self, execution_id: str) -> ExecutionSnapshot | None: ...


class PostgresJobRepository:
    """PostgreSQL 任务仓库。"""

    def __init__(self, session: AsyncSession) -> None:
        self.session = session

    async def submit(
        self, idempotency_key: str, incident_uid: str, evidence_id: uuid.UUID | None, prompt_version: str
    ) -> AnalysisJob:
        job = AnalysisJob(
            idempotency_key=idempotency_key,
            incident_uid=incident_uid,
            evidence_id=evidence_id,
            prompt_version=prompt_version,
            status=JobStatus.QUEUED,
        )
        self.session.add(job)
        await self.session.flush()
        return job

    async def get(self, job_id: uuid.UUID) -> AnalysisJob | None:
        return await self.session.get(AnalysisJob, job_id)

    async def get_by_idempotency_key(self, key: str) -> AnalysisJob | None:
        result = await self.session.execute(
            select(AnalysisJob).where(AnalysisJob.idempotency_key == key)
        )
        return result.scalar_one_or_none()

    async def claim_next(self, worker_id: str, stale_after: timedelta) -> AnalysisJob | None:
        """领取下一个任务：FOR UPDATE SKIP LOCKED，保证并发 Worker 不重复领取。"""
        stale_before = utcnow() - stale_after
        stmt = (
            select(AnalysisJob)
            .where(
                (AnalysisJob.status == JobStatus.QUEUED)
                | (
                    (AnalysisJob.status == JobStatus.PROCESSING)
                    & (AnalysisJob.heartbeat_at < stale_before)
                )
            )
            .order_by(AnalysisJob.created_at)
            .limit(1)
            .with_for_update(skip_locked=True)
        )
        result = await self.session.execute(stmt)
        job = result.scalar_one_or_none()
        if job is None:
            return None
        job.status = JobStatus.PROCESSING
        job.worker_id = worker_id
        job.attempt += 1
        job.heartbeat_at = utcnow()
        job.started_at = job.started_at or utcnow()
        await self.session.flush()
        return job

    async def heartbeat(self, job_id: uuid.UUID, worker_id: str) -> None:
        await self.session.execute(
            update(AnalysisJob)
            .where(AnalysisJob.id == job_id, AnalysisJob.worker_id == worker_id)
            .values(heartbeat_at=utcnow())
        )

    async def complete(
        self, job_id: uuid.UUID, result: dict[str, Any], input_tokens: int, output_tokens: int
    ) -> None:
        await self.session.execute(
            update(AnalysisJob)
            .where(AnalysisJob.id == job_id)
            .values(
                status=JobStatus.SUCCEEDED,
                result=result,
                input_tokens=input_tokens,
                output_tokens=output_tokens,
                finished_at=utcnow(),
            )
        )

    async def fail(self, job_id: uuid.UUID, code: str, message: str, retryable: bool) -> None:
        job = await self.session.get(AnalysisJob, job_id)
        if job is None:
            return
        if retryable and job.attempt < job.max_attempts:
            job.status = JobStatus.QUEUED
            job.worker_id = None
            job.heartbeat_at = None
        else:
            job.status = JobStatus.FAILED
            job.error_code = code
            job.error_message = message[:1000]
            job.finished_at = utcnow()
        await self.session.flush()

    async def requeue_stale(self, now: datetime) -> int:
        """把心跳过期的 processing 任务退回 queued（attempt 未超限）。"""
        stale_before = now - timedelta(minutes=2)
        result = cast(CursorResult[Any], await self.session.execute(
                update(AnalysisJob)
                .where(
                    AnalysisJob.status == JobStatus.PROCESSING,
                    AnalysisJob.heartbeat_at < stale_before,
                    AnalysisJob.attempt < AnalysisJob.max_attempts,
                )
                .values(status=JobStatus.QUEUED, worker_id=None, heartbeat_at=None)
            ),
        )
        return result.rowcount or 0


class PostgresEvidenceRepository:
    """PostgreSQL 证据仓库。"""

    def __init__(self, session: AsyncSession) -> None:
        self.session = session

    async def upsert(
        self,
        incident_uid: str,
        pack: dict[str, Any],
        content_hash: str,
        redaction_count: int,
    ) -> uuid.UUID:
        """按内容哈希 upsert：相同哈希返回已有记录。"""
        existing = await self.session.execute(
            select(EvidenceSnapshot).where(EvidenceSnapshot.content_hash == content_hash)
        )
        row = existing.scalar_one_or_none()
        if row is not None:
            return row.id
        snapshot = EvidenceSnapshot(
            incident_uid=incident_uid,
            schema_version=pack.get("schemaVersion", "v1"),
            collector_version=pack.get("collectorVersion", "collector-v1"),
            content_hash=content_hash,
            window_start=datetime.fromisoformat(pack["window"]["start"]) if pack.get("window") else None,
            window_end=datetime.fromisoformat(pack["window"]["end"]) if pack.get("window") else None,
            payload=pack,
            redaction_count=redaction_count,
        )
        self.session.add(snapshot)
        await self.session.flush()
        return snapshot.id

    async def get(self, evidence_id: uuid.UUID) -> EvidenceSnapshot | None:
        return await self.session.get(EvidenceSnapshot, evidence_id)


class PostgresRunbookRepository:
    """PostgreSQL Runbook 仓库。"""

    def __init__(self, session: AsyncSession) -> None:
        self.session = session

    async def upsert_document(self, doc: dict[str, Any], chunks: list[dict[str, Any]]) -> str:
        """upsert Runbook 与其分块；内容哈希未变则跳过。"""
        existing = await self.session.execute(
            select(Runbook).where(
                Runbook.document_id == doc["document_id"], Runbook.version == doc["version"]
            )
        )
        row = existing.scalar_one_or_none()
        if row is not None and row.content_hash == doc["content_hash"]:
            return "unchanged"
        if row is None:
            row = Runbook(document_id=doc["document_id"], version=doc["version"])
            self.session.add(row)
        row.path = doc["path"]
        row.title = doc["title"]
        row.category = doc["category"]
        row.metadata_json = doc.get("metadata", {})
        row.content_hash = doc["content_hash"]
        row.active = True
        await self.session.flush()

        # 删除旧分块并重写（分块算法变化时保证一致）。
        await self.session.execute(
            text("DELETE FROM runbook_chunks WHERE runbook_id = :rid").bindparams(rid=row.id)
        )
        for idx, chunk in enumerate(chunks):
            self.session.add(
                RunbookChunk(
                    runbook_id=row.id,
                    chunk_index=idx,
                    content=chunk["content"],
                    metadata_json=chunk.get("metadata", {}),
                    textsearch=chunk.get("textsearch"),
                    embedding=chunk.get("embedding"),
                    content_hash=chunk["content_hash"],
                )
            )
        await self.session.flush()
        return "updated"

    async def deactivate(self, document_id: str) -> None:
        await self.session.execute(
            update(Runbook).where(Runbook.document_id == document_id).values(active=False)
        )

    async def list_active(self) -> list[Runbook]:
        result = await self.session.execute(select(Runbook).where(Runbook.active))
        return list(result.scalars())


class PostgresAuditRepository:
    """PostgreSQL 审计仓库（hash chain）。"""

    def __init__(self, session: AsyncSession) -> None:
        self.session = session

    async def append(
        self, incident_uid: str, idempotency_key: str, component: str,
        event_type: str, actor: str | None, payload: dict[str, Any],
    ) -> AuditEvent:
        """追加审计事件并计算 hash chain。使用每 Incident 的 advisory lock 保证顺序。"""
        # 串行化：对 incident_uid 取 advisory 锁（防止并发 append 乱序）。
        await self.session.execute(
            text("SELECT pg_advisory_xact_lock(hashtext(:uid))").bindparams(uid=incident_uid)
        )
        last = await self.session.execute(
            select(AuditEvent).where(AuditEvent.incident_uid == incident_uid)
            .order_by(AuditEvent.sequence.desc()).limit(1)
        )
        prev = last.scalar_one_or_none()
        sequence = (prev.sequence + 1) if prev else 1
        previous_hash = prev.event_hash if prev else "genesis"

        event = AuditEvent(
            incident_uid=incident_uid,
            sequence=sequence,
            idempotency_key=idempotency_key,
            component=component,
            event_type=event_type,
            actor=actor,
            payload=payload,
            previous_hash=previous_hash,
            event_hash="",  # 由调用方或下方计算
        )
        event.event_hash = self._hash_event(previous_hash, event)
        self.session.add(event)
        await self.session.flush()
        return event

    @staticmethod
    def _hash_event(previous_hash: str, event: AuditEvent) -> str:
        canonical = json.dumps(
            {
                "sequence": event.sequence,
                "component": event.component,
                "event_type": event.event_type,
                "actor": event.actor,
                "payload": event.payload,
                "previous_hash": previous_hash,
            },
            sort_keys=True,
            ensure_ascii=False,
            separators=(",", ":"),
        )
        return hashlib.sha256(canonical.encode("utf-8")).hexdigest()

    async def find_by_key(self, idempotency_key: str) -> AuditEvent | None:
        result = await self.session.execute(
            select(AuditEvent).where(AuditEvent.idempotency_key == idempotency_key)
        )
        return result.scalar_one_or_none()

    async def timeline(self, incident_uid: str, limit: int = 200) -> list[AuditEvent]:
        result = await self.session.execute(
            select(AuditEvent).where(AuditEvent.incident_uid == incident_uid)
            .order_by(AuditEvent.sequence.desc()).limit(limit)
        )
        return list(result.scalars())


class PostgresSnapshotRepository:
    """PostgreSQL 执行快照仓库。"""

    def __init__(self, session: AsyncSession) -> None:
        self.session = session

    async def put(
        self, incident_uid: str, execution_id: str, action_type: str,
        resource_ref: dict[str, Any], snapshot: dict[str, Any], content_hash: str,
    ) -> ExecutionSnapshot:
        row = ExecutionSnapshot(
            incident_uid=incident_uid,
            execution_id=execution_id,
            action_type=action_type,
            resource_ref=resource_ref,
            snapshot=snapshot,
            content_hash=content_hash,
            expires_at=utcnow() + timedelta(days=7),
        )
        self.session.add(row)
        await self.session.flush()
        return row

    async def get(self, execution_id: str) -> ExecutionSnapshot | None:
        result = await self.session.execute(
            select(ExecutionSnapshot).where(ExecutionSnapshot.execution_id == execution_id)
        )
        return result.scalar_one_or_none()
