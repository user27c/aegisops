"""审计链离线校验：只读验证 sequence / previous_hash / event_hash 一致性。

用途：事故复盘时独立校验已持久化的审计链是否连续、未被篡改。
本模块只读 audit_events 表，不触碰任何审计写路径。
"""

from __future__ import annotations

import argparse
import asyncio
import hashlib
import json
import sys
from typing import Any, Literal, Protocol

from pydantic import BaseModel, ConfigDict
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db.models import AuditEvent

# 与 PostgresAuditRepository.append 中首条事件的 previous_hash 保持一致。
GENESIS_HASH = "genesis"


def short_uid(uid: str, keep: int = 12) -> str:
    """截断 UID，避免完整明文进入日志与终端输出。"""
    if len(uid) <= keep:
        return uid
    return f"{uid[:keep]}…"


class UnknownIncidentError(LookupError):
    """审计链中不存在该事故。"""


class AuditChainReader(Protocol):
    """只读审计链读取接口（DI 注入点，离线校验不依赖写路径）。"""

    async def list_events(self, incident_uid: str) -> list[AuditEvent]: ...
    async def incident_exists(self, incident_uid: str) -> bool: ...


class AuditVerificationReport(BaseModel):
    """审计链校验结果。"""

    model_config = ConfigDict(extra="forbid")

    incident_uid: str
    ok: bool
    event_count: int
    first_breakpoint: int | None = None
    breakpoint_kind: Literal["sequence", "previous_hash", "event_hash"] | None = None
    message: str


def canonical_event_hash(
    sequence: int,
    component: str,
    event_type: str,
    actor: str | None,
    payload: dict[str, Any],
    previous_hash: str,
) -> str:
    """标准序列化 SHA256。必须与 PostgresAuditRepository._hash_event 完全一致。"""
    canonical = json.dumps(
        {
            "sequence": sequence,
            "component": component,
            "event_type": event_type,
            "actor": actor,
            "payload": payload,
            "previous_hash": previous_hash,
        },
        sort_keys=True,
        ensure_ascii=False,
        separators=(",", ":"),
    )
    return hashlib.sha256(canonical.encode("utf-8")).hexdigest()


def _broken(
    incident_uid: str,
    event_count: int,
    index: int,
    kind: Literal["sequence", "previous_hash", "event_hash"],
    message: str,
) -> AuditVerificationReport:
    """构造断点报告。"""
    return AuditVerificationReport(
        incident_uid=incident_uid,
        ok=False,
        event_count=event_count,
        first_breakpoint=index,
        breakpoint_kind=kind,
        message=message,
    )


async def verify_incident_chain(repo: AuditChainReader, incident_uid: str) -> AuditVerificationReport:
    """验证 sequence、previous_hash 与 event_hash，返回首个断点。

    未知事故抛 UnknownIncidentError；已知但无事件返回显式空链报告。
    """
    if not await repo.incident_exists(incident_uid):
        raise UnknownIncidentError(f"事故 {short_uid(incident_uid)} 不存在，无可校验的审计链")

    events = sorted(await repo.list_events(incident_uid), key=lambda e: e.sequence)

    if not events:
        return AuditVerificationReport(
            incident_uid=incident_uid,
            ok=False,
            event_count=0,
            first_breakpoint=None,
            breakpoint_kind=None,
            message=f"事故 {short_uid(incident_uid)} 无审计事件，链为空",
        )

    expected_previous = GENESIS_HASH
    for idx, event in enumerate(events, start=1):
        if event.sequence != idx:
            return _broken(
                incident_uid,
                len(events),
                idx,
                "sequence",
                f"sequence 断点：第 {idx} 条期望 sequence={idx}，实际 {event.sequence}",
            )
        if event.previous_hash != expected_previous:
            return _broken(
                incident_uid,
                len(events),
                idx,
                "previous_hash",
                f"previous_hash 断点：第 {idx} 条与前一事件 event_hash 不一致",
            )
        recomputed = canonical_event_hash(
            event.sequence,
            event.component,
            event.event_type,
            event.actor,
            event.payload,
            event.previous_hash,
        )
        if recomputed != event.event_hash:
            return _broken(
                incident_uid,
                len(events),
                idx,
                "event_hash",
                f"event_hash 断点：第 {idx} 条重算哈希与存储值不一致（可能被篡改）",
            )
        expected_previous = event.event_hash

    return AuditVerificationReport(
        incident_uid=incident_uid,
        ok=True,
        event_count=len(events),
        first_breakpoint=None,
        breakpoint_kind=None,
        message=f"审计链完整，共 {len(events)} 条事件",
    )


class PostgresAuditChainReader:
    """只读审计链读取（离线校验用，不经过写仓库）。"""

    def __init__(self, session: AsyncSession) -> None:
        self._session = session

    async def incident_exists(self, incident_uid: str) -> bool:
        result = await self._session.execute(
            select(AuditEvent.id).where(AuditEvent.incident_uid == incident_uid).limit(1)
        )
        return result.scalar_one_or_none() is not None

    async def list_events(self, incident_uid: str) -> list[AuditEvent]:
        result = await self._session.execute(
            select(AuditEvent).where(AuditEvent.incident_uid == incident_uid).order_by(AuditEvent.sequence.asc())
        )
        return list(result.scalars())


async def _verify(incident_uid: str) -> int:
    """CLI 主流程：连库 → 校验 → 输出报告，返回退出码。"""
    from app.config import get_settings
    from app.db.engine import create_engine, create_session_factory

    settings = get_settings()
    engine = create_engine(settings)
    factory = create_session_factory(engine)
    try:
        async with factory() as session:
            repo: AuditChainReader = PostgresAuditChainReader(session)
            try:
                report = await verify_incident_chain(repo, incident_uid)
            except UnknownIncidentError as exc:
                print(f"错误: {exc}", file=sys.stderr)
                return 2
            print(json.dumps(report.model_dump(), ensure_ascii=False, indent=2))
            return 0 if report.ok else 1
    finally:
        await engine.dispose()


def cli() -> None:
    """命令行入口：aegis-audit verify --incident-uid <uid>。"""
    parser = argparse.ArgumentParser(prog="aegis-audit", description="审计链离线校验")
    parser.add_argument("command", choices=["verify"], help="子命令")
    parser.add_argument("--incident-uid", required=True, help="事故 UID")
    args = parser.parse_args()

    code = asyncio.run(_verify(args.incident_uid))
    raise SystemExit(code)


if __name__ == "__main__":
    cli()
