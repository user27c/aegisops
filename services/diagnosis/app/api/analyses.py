"""/v1/analyses：任务提交与轮询。API 不执行 embedding/LLM。"""

from __future__ import annotations

import uuid
from typing import Annotated

from fastapi import APIRouter, Depends, Header, HTTPException, status
from sqlalchemy.ext.asyncio import AsyncSession

from app.api.deps import get_session
from app.api.schemas import (
    AnalysisStatusResponse,
    SubmitAnalysisRequest,
    SubmitAnalysisResponse,
)
from app.db.repositories import (
    PostgresEvidenceRepository,
    PostgresJobRepository,
)

router = APIRouter(prefix="/v1/analyses", tags=["analyses"])

IdempotencyKey = Annotated[str, Header(alias="Idempotency-Key")]
SessionDep = Annotated[AsyncSession, Depends(get_session)]


@router.post("", status_code=status.HTTP_202_ACCEPTED)
async def submit_analysis(
    request: SubmitAnalysisRequest,
    idempotency_key: IdempotencyKey,
    session: SessionDep,
) -> SubmitAnalysisResponse:
    """提交分析任务。

    事务：校验 → 按哈希 upsert 证据 → 按幂等键插入 Job；冲突时返回旧 Job。
    """
    if not idempotency_key.strip():
        raise HTTPException(status_code=400, detail="Idempotency-Key 必填")

    jobs = PostgresJobRepository(session)
    evidence_repo = PostgresEvidenceRepository(session)

    # 1. 证据去重保存（mode=json 序列化 datetime 等）。
    pack = request.evidence.model_dump(mode="json")
    content_hash = pack.get("hash") or ""
    if not content_hash:
        raise HTTPException(status_code=422, detail="证据包缺少 hash")
    evidence_id = await evidence_repo.upsert(
        incident_uid=request.incident.uid,
        pack=pack,
        content_hash=content_hash,
        redaction_count=len(pack.get("redactions", [])),
    )

    # 2. 按幂等键 upsert Job。
    job = await jobs.get_by_idempotency_key(idempotency_key)
    if job is not None:
        await session.commit()
        return SubmitAnalysisResponse(
            analysis_id=job.id,
            status=job.status,
            evidence_id=job.evidence_id,
        )

    job = await jobs.submit(
        idempotency_key=idempotency_key,
        incident_uid=request.incident.uid,
        evidence_id=evidence_id,
        prompt_version=request.prompt_version,
    )
    # 把 Incident/Evidence 摘要存到 job 的 result 里备用（worker 读取完整证据）。
    job.result = {
        "incident": request.incident.model_dump(),
        "evidence_hash": content_hash,
    }
    await session.commit()
    return SubmitAnalysisResponse(analysis_id=job.id, status=job.status, evidence_id=evidence_id)


@router.get("/{analysis_id}")
async def get_analysis(
    analysis_id: uuid.UUID,
    session: SessionDep,
) -> AnalysisStatusResponse:
    """轮询任务结果。"""
    job = await PostgresJobRepository(session).get(analysis_id)
    if job is None:
        raise HTTPException(status_code=404, detail="分析任务不存在")

    return AnalysisStatusResponse(
        id=job.id,
        status=job.status,
        retry_after_seconds=5 if job.status in ("queued", "processing") else 0,
        result=job.result if job.status == "succeeded" else None,
        error_code=job.error_code,
        error_message=job.error_message,
        input_tokens=job.input_tokens or 0,
        output_tokens=job.output_tokens or 0,
    )
