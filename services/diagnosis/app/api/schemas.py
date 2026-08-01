"""API DTO。所有外部 DTO 使用 extra="forbid"（蓝图 18.4）。"""

from __future__ import annotations

import uuid
from datetime import datetime
from typing import Any, Literal, Union

from pydantic import BaseModel, ConfigDict, Field, model_validator


class StrictModel(BaseModel):
    """禁止未知字段的基类。"""

    model_config = ConfigDict(extra="forbid")


class TargetModel(StrictModel):
    apiVersion: str
    kind: str
    name: str


class IncidentModel(StrictModel):
    uid: str
    namespace: str
    name: str
    category_hint: str | None = None
    severity: str
    target: TargetModel


class EvidenceItemModel(StrictModel):
    id: str
    kind: str
    source: str
    timestamp: datetime | None = None
    summary: str
    payload: Any = None
    truncated: bool = False


class EvidencePackModel(StrictModel):
    schemaVersion: str = "v1"
    collectorVersion: str = "collector-v1"
    incidentUID: str | None = None
    window: dict[str, str] | None = None
    target: dict[str, Any] = Field(default_factory=dict)
    items: list[EvidenceItemModel] = Field(default_factory=list)
    redactions: list[dict[str, Any]] = Field(default_factory=list)
    hash: str = ""
    partial: bool = False
    missingSources: list[str] = Field(default_factory=list)

    @model_validator(mode="after")
    def check_size(self) -> EvidencePackModel:
        import json

        size = len(json.dumps(self.model_dump(mode="json"), ensure_ascii=False))
        if size > 524288:  # 蓝图 4.3：单证据包 512 KiB
            raise ValueError(f"证据包过大: {size} 字节 > 512KiB")
        return self


class SubmitAnalysisRequest(StrictModel):
    incident: IncidentModel
    evidence: EvidencePackModel
    requested_model: str | None = None
    prompt_version: str


class SubmitAnalysisResponse(StrictModel):
    analysis_id: uuid.UUID
    status: str
    evidence_id: uuid.UUID | None = None


class ReviewerModel(StrictModel):
    verdict: str
    issues: list[str] = Field(default_factory=list)
    pass_: bool = Field(alias="pass")


class RestartProposal(StrictModel):
    action: Literal["RestartWorkload"]
    parameters: dict[str, Any] = Field(default_factory=dict)


class ScaleProposal(StrictModel):
    action: Literal["ScaleDeployment"]
    parameters: dict[str, Any] = Field(default_factory=dict)


class PatchResourceProposal(StrictModel):
    action: Literal["PatchResourceLimit"]
    parameters: dict[str, Any] = Field(default_factory=dict)


class RollbackProposal(StrictModel):
    action: Literal["RollbackDeployment"]
    parameters: dict[str, Any] = Field(default_factory=dict)


class RestoreConfigProposal(StrictModel):
    action: Literal["RestoreConfigMap"]
    parameters: dict[str, Any] = Field(default_factory=dict)


# 运行时 discriminated union 需要 typing.Union（Pydantic 依赖其标签机制）。
ActionProposalModel = Union[  # noqa: UP007
    RestartProposal,
    ScaleProposal,
    PatchResourceProposal,
    RollbackProposal,
    RestoreConfigProposal,
]


class DiagnosisResultModel(StrictModel):
    category: str
    root_cause: str
    confidence: float = Field(ge=0.0, le=1.0)
    evidence_ids: list[str] = Field(min_length=1)
    runbook_refs: list[str] = Field(default_factory=list)
    reviewer: ReviewerModel
    proposal: ActionProposalModel | None = None


class AnalysisStatusResponse(StrictModel):
    id: uuid.UUID
    status: Literal["queued", "processing", "succeeded", "failed"]
    retry_after_seconds: int = 0
    result: DiagnosisResultModel | None = None
    error_code: str | None = None
    error_message: str | None = None
    input_tokens: int = 0
    output_tokens: int = 0


class AuditEventRequest(StrictModel):
    incident_uid: str
    component: str
    event_type: str
    actor: str | None = None
    payload: dict[str, Any] = Field(default_factory=dict)


class AuditEventResponse(StrictModel):
    id: uuid.UUID
    sequence: int
    previous_hash: str
    event_hash: str


class ExecutionSnapshotRequest(StrictModel):
    incident_uid: str
    execution_id: str
    action_type: str
    resource_ref: dict[str, Any] = Field(default_factory=dict)
    snapshot: dict[str, Any]
    max_bytes: int = 262144

    @model_validator(mode="after")
    def check_size(self) -> ExecutionSnapshotRequest:
        import json

        size = len(json.dumps(self.snapshot, ensure_ascii=False))
        if size > self.max_bytes:
            raise ValueError(f"快照过大: {size} 字节 > {self.max_bytes}")
        return self


class ExecutionSnapshotResponse(StrictModel):
    id: uuid.UUID
    sha256: str
    expires_at: datetime | None = None


class ExecutionSnapshotGet(StrictModel):
    id: uuid.UUID
    incident_uid: str
    execution_id: str
    action_type: str
    snapshot: dict[str, Any]
    sha256: str


class EvidenceGet(StrictModel):
    id: uuid.UUID
    incident_uid: str
    schema_version: str
    content_hash: str
    payload: dict[str, Any]
    created_at: datetime


class TimelineEntry(StrictModel):
    time: datetime
    type: str
    reason: str | None = None
    message: str | None = None
    actor: str | None = None
    sequence: int | None = None
    event_hash: str | None = None
