"""/v1/audit-events 与 /v1/execution-snapshots、/v1/evidence、/v1/incidents/{uid}/timeline。"""

from __future__ import annotations

import hashlib
import json
import uuid
from typing import Annotated

from fastapi import APIRouter, Depends, Header, HTTPException
from sqlalchemy.ext.asyncio import AsyncSession

from app.api.deps import get_session
from app.api.schemas import (
    AuditEventRequest,
    AuditEventResponse,
    EvidenceGet,
    ExecutionSnapshotGet,
    ExecutionSnapshotRequest,
    ExecutionSnapshotResponse,
    TimelineEntry,
)
from app.db.repositories import (
    PostgresAuditRepository,
    PostgresEvidenceRepository,
    PostgresSnapshotRepository,
)

router = APIRouter(prefix="/v1", tags=["audit"])

IdempotencyKey = Annotated[str, Header(alias="Idempotency-Key")]
SessionDep = Annotated[AsyncSession, Depends(get_session)]


@router.post("/audit-events", status_code=201)
async def append_audit_event(
    request: AuditEventRequest,
    idempotency_key: IdempotencyKey,
    session: SessionDep,
) -> AuditEventResponse:
    """追加审计事件。服务端计算 previous_hash 与 event_hash，客户端不能提交。"""
    if not idempotency_key.strip():
        raise HTTPException(status_code=400, detail="Idempotency-Key 必填")

    repo = PostgresAuditRepository(session)
    # 幂等：重复键返回已有事件。
    existing = await repo.find_by_key(idempotency_key)
    if existing is not None:
        await session.commit()
        return AuditEventResponse(
            id=existing.id,
            sequence=existing.sequence,
            previous_hash=existing.previous_hash,
            event_hash=existing.event_hash,
        )

    event = await repo.append(
        incident_uid=request.incident_uid,
        idempotency_key=idempotency_key,
        component=request.component,
        event_type=request.event_type,
        actor=request.actor,
        payload=request.payload,
    )
    await session.commit()
    return AuditEventResponse(
        id=event.id,
        sequence=event.sequence,
        previous_hash=event.previous_hash,
        event_hash=event.event_hash,
    )


@router.post("/execution-snapshots", status_code=201)
async def put_execution_snapshot(
    request: ExecutionSnapshotRequest,
    idempotency_key: IdempotencyKey,
    session: SessionDep,
) -> ExecutionSnapshotResponse:
    """保存执行前快照。执行 ID 唯一。"""
    if not idempotency_key.strip():
        raise HTTPException(status_code=400, detail="Idempotency-Key 必填")

    repo = PostgresSnapshotRepository(session)
    existing = await repo.get(request.execution_id)
    if existing is not None:
        await session.commit()
        return ExecutionSnapshotResponse(id=existing.id, sha256=existing.content_hash)

    content_hash = sha256_of(request.snapshot)
    row = await repo.put(
        incident_uid=request.incident_uid,
        execution_id=request.execution_id,
        action_type=request.action_type,
        resource_ref=request.resource_ref,
        snapshot=request.snapshot,
        content_hash=content_hash,
    )
    await session.commit()
    return ExecutionSnapshotResponse(id=row.id, sha256=content_hash, expires_at=row.expires_at)


@router.get("/execution-snapshots/{execution_id}")
async def get_execution_snapshot(
    execution_id: str,
    session: SessionDep,
) -> ExecutionSnapshotGet:
    """读取执行前快照。响应必须校验 SHA256。"""
    row = await PostgresSnapshotRepository(session).get(execution_id)
    if row is None:
        raise HTTPException(status_code=404, detail="快照不存在")
    return ExecutionSnapshotGet(
        id=row.id,
        incident_uid=row.incident_uid,
        execution_id=row.execution_id,
        action_type=row.action_type,
        snapshot=row.snapshot,
        sha256=row.content_hash,
    )


@router.get("/evidence/{evidence_id}")
async def get_evidence(
    evidence_id: uuid.UUID,
    session: SessionDep,
) -> EvidenceGet:
    """读取脱敏证据。只允许 Incident API 服务账户访问（NetworkPolicy 保证）。"""
    row = await PostgresEvidenceRepository(session).get(evidence_id)
    if row is None:
        raise HTTPException(status_code=404, detail="证据不存在")
    return EvidenceGet(
        id=row.id,
        incident_uid=row.incident_uid,
        schema_version=row.schema_version,
        content_hash=row.content_hash,
        payload=row.payload,
        created_at=row.created_at,
    )


@router.get("/incidents/{incident_uid}/timeline")
async def get_timeline(
    incident_uid: str,
    session: SessionDep,
) -> list[TimelineEntry]:
    """合并审计事件为时间线。"""
    events = await PostgresAuditRepository(session).timeline(incident_uid)
    return [
        TimelineEntry(
            time=e.created_at,
            type=e.event_type,
            reason=e.payload.get("reason"),
            message=e.payload.get("message"),
            actor=e.actor,
            sequence=e.sequence,
            event_hash=e.event_hash,
        )
        for e in events
    ]


def sha256_of(value: dict[str, object]) -> str:
    """计算 dict 的稳定 SHA256。"""
    canonical = json.dumps(value, sort_keys=True, ensure_ascii=False, separators=(",", ":"))
    return hashlib.sha256(canonical.encode("utf-8")).hexdigest()
