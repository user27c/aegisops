"""diagnose 节点：调用 LLM 生成根因诊断。"""

from __future__ import annotations

from typing import Any

from app.graph.state import AnalysisState
from app.llm.base import LLMClient
from app.llm.deepseek import LLMError
from app.llm.prompts import PromptRegistry, render_prompt

# 诊断 schema 允许的 Action。
KNOWN_ACTIONS = {
    "RestartWorkload",
    "ScaleDeployment",
    "PatchResourceLimit",
    "RollbackDeployment",
    "RestoreConfigMap",
}


async def diagnose(state: AnalysisState, llm: LLMClient, prompts: PromptRegistry) -> dict[str, Any]:
    """生成诊断草稿。要求 JSON Output；引用只能选给定 Evidence ID/Chunk ID。"""
    template = prompts.get_diagnosis()
    messages = render_prompt(
        template,
        incident=state.get("incident", {}),
        evidence=state.get("evidence", {}),
        chunks=state.get("retrieved_chunks", []),
    )

    try:
        response = await llm.generate_diagnosis(
            {
                "messages": messages,
                "template_version": template.version,
                # 结构化字段供 Fake 客户端与 eval 使用（DeepSeek 忽略）。
                "incident": state.get("incident", {}),
                "evidence": state.get("evidence", {}),
            }
        )
    except LLMError as exc:
        return {"errors": [{"code": exc.code, "message": str(exc)}]}
    except TimeoutError:
        return {"errors": [{"code": "TIMEOUT", "message": "LLM 调用超时"}]}

    content = response.content or {}
    # 只允许引用证据包中存在的 ID 与检索到的 chunk ID。
    valid_evidence = {item.get("id") for item in state.get("evidence", {}).get("items", [])}
    valid_chunks = {c["chunk_id"] for c in state.get("retrieved_chunks", [])}

    evidence_ids = [e for e in content.get("evidence_ids", []) if e in valid_evidence]
    runbook_refs = [
        r for r in content.get("runbook_refs", [])
        if _runbook_chunk_valid(r, valid_chunks)
    ]

    proposal = content.get("proposal")
    if isinstance(proposal, dict):
        action = proposal.get("action")
        if action not in KNOWN_ACTIONS:
            proposal = None  # 未知动作直接丢弃
        elif not isinstance(proposal.get("parameters"), dict):
            proposal = None

    draft = {
        "category": str(content.get("category", "")),
        "root_cause": str(content.get("root_cause", "")),
        "confidence": float(content.get("confidence", 0.0)),
        "evidence_ids": evidence_ids,
        "runbook_refs": runbook_refs,
        "proposal": proposal,
    }
    return {"diagnosis_draft": draft}


def _runbook_chunk_valid(ref: str, valid_chunks: set[str]) -> bool:
    """校验 runbook 引用格式（runbook://<id>/<version>）并确认 chunk 存在。"""
    if not isinstance(ref, str) or not ref.startswith("runbook://"):
        return False
    doc_id = ref.split("/")[2] if len(ref.split("/")) >= 3 else ""
    return any(cid.startswith(f"{doc_id}-") for cid in valid_chunks)
