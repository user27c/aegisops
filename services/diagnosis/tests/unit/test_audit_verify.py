"""审计链离线校验单元测试：不依赖数据库，注入假仓库验证四个场景。

- 连续合法链 → ok，无断点；
- 篡改中间事件 event_hash → 返回该事件下标作为首个断点；
- 空链 → 显式消息；
- 未知 incident_uid → 显式错误（UnknownIncidentError）。
"""

from __future__ import annotations

from types import SimpleNamespace
from typing import Any

import pytest
from app.audit_verify import (
    AuditVerificationReport,
    UnknownIncidentError,
    canonical_event_hash,
    verify_incident_chain,
)
from app.db.repositories import PostgresAuditRepository


class FakeReader:
    """假审计链读取器：可注入事件列表与存在性。"""

    def __init__(self, events: list[Any], *, exists: bool = True) -> None:
        self._events = events
        self._exists = exists

    async def list_events(self, incident_uid: str) -> list[Any]:
        return self._events

    async def incident_exists(self, incident_uid: str) -> bool:
        return self._exists


def make_event(
    sequence: int,
    component: str,
    event_type: str,
    actor: str | None,
    payload: dict[str, Any],
    previous_hash: str,
) -> SimpleNamespace:
    """用写入侧的真实哈希函数构造一个事件，保证链在语义上合法。"""
    event = SimpleNamespace(
        sequence=sequence,
        component=component,
        event_type=event_type,
        actor=actor,
        payload=payload,
        previous_hash=previous_hash,
        event_hash="",
    )
    event.event_hash = PostgresAuditRepository._hash_event(previous_hash, event)
    return event


def make_chain(n: int = 3) -> list[SimpleNamespace]:
    """构造 n 条事件的连续合法链。"""
    events: list[SimpleNamespace] = []
    previous = "genesis"
    for i in range(1, n + 1):
        event = make_event(
            sequence=i,
            component="controller",
            event_type=f"EventType{i}",
            actor="aegisops-operator",
            payload={"reason": f"step-{i}"},
            previous_hash=previous,
        )
        events.append(event)
        previous = event.event_hash
    return events


async def test_valid_chain_ok() -> None:
    """连续合法链应通过校验，无断点。"""
    report = await verify_incident_chain(FakeReader(make_chain(3)), "incident-valid")

    assert isinstance(report, AuditVerificationReport)
    assert report.ok is True
    assert report.event_count == 3
    assert report.first_breakpoint is None
    assert report.breakpoint_kind is None


async def test_tampered_event_hash_returns_first_breakpoint() -> None:
    """篡改中间事件的 event_hash 后，应返回该事件下标作为首个断点。"""
    chain = make_chain(3)
    chain[1].event_hash = "0" * 64  # 篡改第 2 条事件的哈希。

    report = await verify_incident_chain(FakeReader(chain), "incident-tampered")

    assert report.ok is False
    assert report.first_breakpoint == 2
    assert report.breakpoint_kind == "event_hash"


async def test_empty_chain_explicit_message() -> None:
    """已知事故但无审计事件时，应返回显式消息而非异常。"""
    report = await verify_incident_chain(FakeReader([], exists=True), "incident-empty")

    assert report.ok is False
    assert report.event_count == 0
    assert report.first_breakpoint is None
    assert report.message  # 非空显式消息


async def test_unknown_incident_raises() -> None:
    """未知 incident_uid 必须抛出显式错误。"""
    with pytest.raises(UnknownIncidentError):
        await verify_incident_chain(FakeReader([], exists=False), "incident-unknown")


def test_canonical_event_hash_matches_writer() -> None:
    """离线校验器重算哈希必须与写入侧 PostgresAuditRepository._hash_event 完全一致。"""
    event = SimpleNamespace(
        sequence=7,
        component="executor",
        event_type="ExecutionStarted",
        actor=None,
        payload={"action": "RestartWorkload"},
        previous_hash="genesis",
    )

    assert canonical_event_hash(
        7, "executor", "ExecutionStarted", None, {"action": "RestartWorkload"}, "genesis"
    ) == PostgresAuditRepository._hash_event("genesis", event)
