"""M9.7 实验执行器：A/B/C/D、预算、断点续跑与审计记录。

它只读取已通过 ``load_cases`` 的脱敏数据；真实 provider 在数据集没有
达到 M9.7 门槛时会拒绝启动，防止把小样本 smoke 伪装成质量评估。
"""

from __future__ import annotations

import asyncio
import hashlib
import json
import math
import re
import subprocess
import sys
import time
import uuid
from collections import Counter
from collections.abc import Iterable
from dataclasses import asdict, dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from .dataset import CONTROLLED_CAMPAIGN_SOURCE, is_automation_review_marker, load_cases
from .providers import create_provider
from .redaction import assert_safe

_ROOT = Path(__file__).resolve().parents[2]
_SERVICE_ROOT = _ROOT / "services" / "diagnosis"
if str(_SERVICE_ROOT) not in sys.path:
    sys.path.insert(0, str(_SERVICE_ROOT))

from app.graph.nodes.diagnose import diagnose
from app.graph.nodes.review import review_diagnosis
from app.llm.base import LLMClient, LLMResponse
from app.llm.prompts import DIAGNOSIS_PROMPT_VERSION, REVIEWER_PROMPT_VERSION, PromptRegistry
from app.rag.chunker import chunk_markdown, parse_frontmatter
from app.rag.rrf import reciprocal_rank_fusion


@dataclass(frozen=True)
class ExperimentConfig:
    name: str
    include_evidence: bool
    include_runbooks: bool
    reviewer: bool

    @property
    def logical_calls_per_case(self) -> int:
        return 2 if self.reviewer else 1


CONFIGS: dict[str, ExperimentConfig] = {
    "a-alert-only": ExperimentConfig("a-alert-only", False, False, False),
    "b-evidence": ExperimentConfig("b-evidence", True, False, False),
    "c-evidence-rag": ExperimentConfig("c-evidence-rag", True, True, False),
    "d-evidence-rag-review": ExperimentConfig("d-evidence-rag-review", True, True, True),
}


@dataclass(frozen=True)
class ExperimentOptions:
    provider: str
    dataset_dir: Path
    output_root: Path
    config_names: tuple[str, ...] = tuple(CONFIGS)
    max_calls: int = 180
    max_input_tokens: int = 16_000
    max_output_tokens: int = 2_048
    resume_dir: Path | None = None
    allow_incomplete_dataset: bool = False
    confirm_budget: bool = False
    runbooks_root: Path = _ROOT / "runbooks"


class BudgetExceeded(RuntimeError):
    """A pre-call budget guard rejected the request."""


class CallBudget:
    def __init__(
        self,
        max_calls: int,
        max_input_tokens: int,
        max_output_tokens: int,
        initial_logical_calls: int = 0,
    ) -> None:
        self.max_calls = max_calls
        self.max_input_tokens = max_input_tokens
        self.max_output_tokens = max_output_tokens
        self.logical_calls = initial_logical_calls

    def reserve(self, messages: list[dict[str, str]]) -> int:
        estimate = max(1, math.ceil(sum(len(m.get("content", "")) for m in messages) / 4))
        if estimate > self.max_input_tokens:
            raise BudgetExceeded(f"预估输入 {estimate} tokens 超过上限 {self.max_input_tokens}")
        if self.logical_calls >= self.max_calls:
            raise BudgetExceeded(f"逻辑调用已达预算 {self.max_calls}")
        self.logical_calls += 1
        return estimate


class RecordingClient:
    """记录每一条模型调用的安全审计信息，不改变生产节点的行为。"""

    def __init__(self, inner: LLMClient, budget: CallBudget) -> None:
        self.inner = inner
        self.budget = budget
        self.calls: list[dict[str, Any]] = []

    async def generate_diagnosis(self, prompt: dict[str, Any]) -> LLMResponse:
        return await self._call("diagnosis", self.inner.generate_diagnosis, prompt)

    async def review(self, prompt: dict[str, Any]) -> LLMResponse:
        return await self._call("review", self.inner.review, prompt)

    async def _call(self, kind: str, method: Any, prompt: dict[str, Any]) -> LLMResponse:
        messages = prompt.get("messages", [])
        if not isinstance(messages, list):
            raise BudgetExceeded("模型调用缺少 messages")
        assert_safe(messages)
        prompt_hash = _sha256(messages)
        estimated_input = self.budget.reserve(messages)
        started = time.monotonic()
        try:
            response = await method(prompt)
        except Exception as exc:
            self.calls.append(
                {
                    "kind": kind,
                    "prompt_hash": prompt_hash,
                    "estimated_input_tokens": estimated_input,
                    "latency_ms": round((time.monotonic() - started) * 1000),
                    "error_code": type(exc).__name__,
                }
            )
            raise
        assert_safe(response.content)
        record = {
            "kind": kind,
            "prompt_hash": prompt_hash,
            "estimated_input_tokens": estimated_input,
            "latency_ms": round((time.monotonic() - started) * 1000),
            "model": response.model,
            "request_id": response.request_id,
            "attempts": response.attempts,
            "input_tokens": response.usage.input_tokens,
            "output_tokens": response.usage.output_tokens,
            "finish_reason": response.finish_reason,
            "output_hash": _sha256(response.content),
        }
        assert_safe(record)
        if response.usage.output_tokens > self.budget.max_output_tokens:
            raise BudgetExceeded(
                f"模型输出 {response.usage.output_tokens} tokens 超过上限 {self.budget.max_output_tokens}"
            )
        self.calls.append(record)
        return response


def dataset_readiness(cases: Iterable[dict[str, Any]]) -> list[str]:
    """Return all M9.7 data-gate failures without silently weakening the gate."""
    rows = list(cases)
    counts = Counter(str(row["fault_type"]) for row in rows)
    variants = {str(row["variant"]) for row in rows}
    degradations = sum(bool(row["ground_truth"]["should_degrade"]) for row in rows)
    tags = Counter(tag for row in rows for tag in row.get("scenario_tags", []) if isinstance(tag, str))
    failures: list[str] = []
    if len(rows) < 36:
        failures.append(f"样本数 {len(rows)}/36")
    underfilled = sorted(name for name, count in counts.items() if count < 5)
    if len(counts) < 6 or underfilled:
        details = ", ".join(f"{name}={count}" for name, count in sorted(counts.items()))
        failures.append("每类至少 5 条、至少 6 类未满足（" + details + "）")
    if variants != {"clean", "noisy", "sparse"}:
        failures.append("未覆盖 clean/noisy/sparse 三种证据变体")
    if degradations < 6:
        failures.append(f"安全降级负样本 {degradations}/6")
    for tag, label in (("prompt-injection", "注入样本"), ("multi-fault", "多故障/干扰样本")):
        if tags[tag] < 6:
            failures.append(f"{label} {tags[tag]}/6")
    machine_review = sorted(
        f"{row['case_id']}={row['provenance']['reviewed_by']}"
        for row in rows
        if is_automation_review_marker(str(row["provenance"]["reviewed_by"]))
    )
    if machine_review:
        failures.append("审核未完成（自动化占位值）: " + ", ".join(machine_review[:8]))
    legacy_campaigns = sorted(
        str(row["case_id"])
        for row in rows
        if row["provenance"].get("source") != CONTROLLED_CAMPAIGN_SOURCE
    )
    if legacy_campaigns:
        failures.append("受控采集 provenance 未达到 v2 可审计标准: " + ", ".join(legacy_campaigns[:8]))
    return failures


def planned_logical_calls(case_count: int, config_names: Iterable[str]) -> int:
    """Return the exact scheduled call count before any provider is contacted."""

    names = tuple(config_names)
    if case_count < 0:
        raise ValueError("case_count 不能为负数")
    unknown = [name for name in names if name not in CONFIGS]
    if unknown:
        raise ValueError("未知实验配置: " + ", ".join(unknown))
    return case_count * sum(CONFIGS[name].logical_calls_per_case for name in names)


def _dataset_provenance(cases: Iterable[dict[str, Any]]) -> list[dict[str, str]]:
    return [
        {
            "case_id": str(case["case_id"]),
            "source": str(case["provenance"]["source"]),
            "campaign_run_id": str(case["provenance"]["campaign_run_id"]),
            "captured_at": str(case["provenance"]["captured_at"]),
            "reviewed_by": str(case["provenance"]["reviewed_by"]),
        }
        for case in cases
    ]


def create_run_dir(options: ExperimentOptions) -> Path:
    """Create an empty, nonce-suffixed run directory; no historic run is reusable."""
    if options.resume_dir:
        path = options.resume_dir.resolve()
        if not (path / "manifest.json").is_file() or not (path / "raw.jsonl").is_file():
            raise ValueError("--resume 必须指向含 manifest.json 与 raw.jsonl 的本次实验目录")
        return path
    run_id = f"{options.provider}-m97-{datetime.now(UTC).strftime('%Y%m%dT%H%M%SZ')}-{uuid.uuid4().hex[:8]}"
    path = (options.output_root / run_id).resolve()
    if path.exists():  # nonce collision must not turn into an overwrite.
        raise FileExistsError(f"评估输出已存在，拒绝覆盖: {path}")
    path.parent.mkdir(parents=True, exist_ok=True)
    path.mkdir(parents=True)
    return path


def _git_sha() -> str:
    # Fixed executable and repository path; this records provenance only.
    result = subprocess.run(
        ["/usr/bin/git", "-C", str(_ROOT), "rev-parse", "HEAD"],
        capture_output=True,
        text=True,
        check=False,
    )
    return result.stdout.strip() if result.returncode == 0 else "unknown"


def _sha256(value: Any) -> str:
    return hashlib.sha256(json.dumps(value, ensure_ascii=False, sort_keys=True).encode("utf-8")).hexdigest()


def _safe_incident(case: dict[str, Any]) -> dict[str, Any]:
    raw = case["incident"]
    target = raw.get("target", {}) if isinstance(raw.get("target"), dict) else {}
    return {
        "uid": raw.get("uid", ""),
        "namespace": raw.get("namespace", ""),
        # Kubernetes incident resource names and Alertmanager alert names are
        # operational identifiers, not evidence.  They often embed taxonomy
        # labels (for example ``ContainerOOMKilled``), which would leak the
        # expected class to the alert-only baseline.  Give every arm the same
        # neutral identifier; B/C/D still receive the collected evidence.
        "name": "controlled-evaluation",
        "severity": raw.get("severity", ""),
        "target": {
            "kind": target.get("kind", ""),
            "namespace": target.get("namespace", ""),
            "name": target.get("name", ""),
        },
        "alert": "controlled-evaluation",
        # Ground truth is deliberately never supplied to the model.
        "category_hint": None,
    }


_WORD = re.compile(r"[A-Za-z0-9_]+|[\u4e00-\u9fff]+")


def _terms(value: str) -> set[str]:
    return {part.lower() for part in _WORD.findall(value) if len(part) > 1}


def _chargrams(value: str) -> Counter[str]:
    text = re.sub(r"\s+", "", value.lower())
    return Counter(text[index : index + 3] for index in range(max(0, len(text) - 2)))


def _cosine(left: Counter[str], right: Counter[str]) -> float:
    dot = sum(left[key] * right[key] for key in left.keys() & right.keys())
    left_norm = math.sqrt(sum(value * value for value in left.values()))
    right_norm = math.sqrt(sum(value * value for value in right.values()))
    return dot / (left_norm * right_norm) if left_norm and right_norm else 0.0


class FileHybridRetriever:
    """由受版本控制 Runbook 快照组成的双路离线检索器。

    它保留生产 chunker 与 RRF 融合语义，但不声称这是 PGVector 线上检索；
    run manifest 会明确记录 ``file-hybrid-runbook-snapshot``。
    """

    def __init__(self, root: Path) -> None:
        self.chunks: list[dict[str, Any]] = []
        for path in sorted(root.glob("*.md")):
            doc = parse_frontmatter(path)
            for index, chunk in enumerate(chunk_markdown(doc)):
                self.chunks.append(
                    {
                        "chunk_id": f"{doc.document_id}-{index}",
                        "document_id": doc.document_id,
                        "runbook_version": doc.version,
                        "category": doc.category,
                        "section": str(chunk.metadata.get("section", "")),
                        "content": chunk.content,
                    }
                )
        if not self.chunks:
            raise ValueError(f"Runbook 目录为空: {root}")

    def search(self, incident: dict[str, Any], evidence: dict[str, Any], top_k: int = 5) -> list[dict[str, Any]]:
        query = " ".join(
            [
                str(incident.get("alert", "")),
                *[str(item.get("summary", "")) for item in evidence.get("items", [])],
            ]
        )
        query_terms, query_grams = _terms(query), _chargrams(query)
        lexical = sorted(
            self.chunks,
            key=lambda item: len(query_terms & _terms(item["content"])),
            reverse=True,
        )
        semantic = sorted(
            self.chunks,
            key=lambda item: _cosine(query_grams, _chargrams(item["content"])),
            reverse=True,
        )
        merged = reciprocal_rank_fusion(
            [[item["chunk_id"] for item in lexical], [item["chunk_id"] for item in semantic]], k=60, limit=top_k
        )
        by_id = {item["chunk_id"]: item for item in self.chunks}
        return [by_id[entry.id] for entry in merged if entry.id in by_id]


def _record_key(case_id: str, config_name: str) -> str:
    return f"{case_id}\0{config_name}"


def _completed_keys(raw_path: Path) -> set[str]:
    completed: set[str] = set()
    if not raw_path.exists():
        return completed
    for line in raw_path.read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        row = json.loads(line)
        if row.get("status") == "completed":
            completed.add(_record_key(str(row["case_id"]), str(row["config"])))
    return completed


def _recorded_logical_calls(raw_path: Path) -> int:
    """Count calls already persisted so resume cannot reset the cost guard."""

    total = 0
    if not raw_path.exists():
        return total
    for line in raw_path.read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        row = json.loads(line)
        calls = row.get("calls", [])
        if not isinstance(calls, list):
            raise TypeError("raw.jsonl 的 calls 字段非法，拒绝恢复")
        total += len(calls)
    return total


def _error_codes(errors: Any) -> list[str]:
    if not errors:
        return []
    if not isinstance(errors, list):
        return ["INVALID_ERROR_PAYLOAD"]
    return [
        str(item.get("code", "UNKNOWN")) if isinstance(item, dict) else "INVALID_ERROR"
        for item in errors
    ]


async def _run_case(
    case: dict[str, Any],
    config: ExperimentConfig,
    provider: LLMClient,
    budget: CallBudget,
    retriever: FileHybridRetriever,
) -> dict[str, Any]:
    incident = _safe_incident(case)
    evidence = json.loads((Path(case["_dataset_dir"]) / case["evidence_path"]).read_text(encoding="utf-8"))
    model_evidence = (
        evidence
        if config.include_evidence
        else {"items": [], "partial": True, "missingSources": ["withheld-for-baseline"]}
    )
    chunks = retriever.search(incident, model_evidence) if config.include_runbooks else []
    prompts = PromptRegistry()
    recording = RecordingClient(provider, budget)
    state: dict[str, Any] = {"incident": incident, "evidence": model_evidence, "retrieved_chunks": chunks}
    started_at = datetime.now(UTC).isoformat().replace("+00:00", "Z")
    monotonic = time.monotonic()
    normalized_input = {"incident": incident, "evidence": model_evidence, "retrieved_chunks": chunks}
    record: dict[str, Any] = {
        "case_id": case["case_id"],
        "fault_type": case["fault_type"],
        "config": config.name,
        "status": "error",
        "started_at": started_at,
        "ground_truth": case["ground_truth"],
        "input_summary": {
            "evidence_item_count": len(model_evidence.get("items", [])),
            "retrieved_chunk_count": len(chunks),
        },
    }
    try:
        diagnosis_result = await diagnose(state, recording, prompts)
        errors = diagnosis_result.get("errors", [])
        draft = diagnosis_result.get("diagnosis_draft", {})
        raw_proposal = draft.get("proposal") if isinstance(draft, dict) else None
        draft_action = raw_proposal.get("action") if isinstance(raw_proposal, dict) else None
        evidence_refs = draft.get("evidence_ids", []) if isinstance(draft, dict) else []
        runbook_refs = draft.get("runbook_refs", []) if isinstance(draft, dict) else []
        citation_summary = {
            # diagnose() has already intersected both lists with known evidence
            # IDs / retrieved runbook chunks. Keep counts only, not references.
            "evidence_reference_count": len(evidence_refs) if isinstance(evidence_refs, list) else 0,
            "evidence_references_validated": isinstance(evidence_refs, list),
            "runbook_reference_count": len(runbook_refs) if isinstance(runbook_refs, list) else 0,
            "runbook_references_validated": isinstance(runbook_refs, list),
        }
        review: dict[str, Any] | None = None
        if not errors and config.reviewer:
            review_result = await review_diagnosis({**state, **diagnosis_result}, recording, prompts)
            errors = list(errors) + list(review_result.get("errors", []))
            raw_review = review_result.get("review", {})
            review = {
                "verdict": str(raw_review.get("verdict", "fail")),
                "pass": raw_review.get("pass") is True,
                "issue_count": len(raw_review.get("issues", []))
                if isinstance(raw_review.get("issues", []), list)
                else 0,
            }
        effective = (
            draft.get("proposal")
            if not config.reviewer or review and review.get("pass") is True
            else None
        )
        safe_errors = [{"code": code} for code in _error_codes(errors)]
        record.update(
            {
                "review": review,
                "category": draft.get("category"),
                "draft_action": draft_action,
                "action": effective.get("action") if isinstance(effective, dict) else None,
                "citation_summary": citation_summary,
                "errors": safe_errors,
                "status": "completed" if not safe_errors else "error",
            }
        )
    except BudgetExceeded:
        record.update({"error_code": "BUDGET_EXCEEDED", "errors": [{"code": "BUDGET_EXCEEDED"}]})
    except Exception as exc:  # noqa: BLE001 - preserve unexpected failures in the denominator.
        record.update(
            {"error_code": type(exc).__name__, "errors": [{"code": type(exc).__name__}]}
        )
    record["calls"] = recording.calls
    record["finished_at"] = datetime.now(UTC).isoformat().replace("+00:00", "Z")
    record["latency_ms"] = round((time.monotonic() - monotonic) * 1000)
    record["input_hash"] = _sha256(normalized_input)
    record["prompt_hashes"] = [call["prompt_hash"] for call in recording.calls]
    record["output_hashes"] = [call["output_hash"] for call in recording.calls if "output_hash" in call]
    assert_safe(record)
    return record


def _write_json_atomic(path: Path, value: Any) -> None:
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(value, ensure_ascii=False, sort_keys=True, indent=2) + "\n", encoding="utf-8")
    temporary.replace(path)


async def run_experiment(options: ExperimentOptions) -> Path:
    """Run each selected config on each case sequentially to respect API budget."""
    cases = load_cases(options.dataset_dir)
    readiness = dataset_readiness(cases)
    if options.provider == "deepseek" and readiness and not options.allow_incomplete_dataset:
        raise ValueError("真实 DeepSeek M9.7 拒绝不完整数据集: " + "；".join(readiness))
    if options.provider == "deepseek" and options.allow_incomplete_dataset:
        raise ValueError("真实 DeepSeek 不支持 --allow-incomplete-dataset")
    if not options.config_names:
        raise ValueError("至少选择一个实验配置")
    unknown = [name for name in options.config_names if name not in CONFIGS]
    if unknown:
        raise ValueError("未知实验配置: " + ", ".join(unknown))
    configs = [CONFIGS[name] for name in options.config_names]
    planned = planned_logical_calls(len(cases), options.config_names)
    if options.max_calls <= 0:
        raise ValueError("--max-calls 必须为正数")
    if planned > options.max_calls:
        raise ValueError(
            f"计划需要 {planned} 次逻辑调用，但 --max-calls={options.max_calls}；"
            "拒绝启动。请显式设置足够的 --max-calls 并传 --confirm-budget，成本保护不会静默放宽"
        )
    if not options.confirm_budget:
        raise ValueError(
            f"计划需要 {planned} 次逻辑调用；请传 --confirm-budget 显式确认预算，拒绝无确认启动"
        )

    provider = create_provider(options.provider, max_output_tokens=options.max_output_tokens)
    output = create_run_dir(options)
    raw_path = output / "raw.jsonl"
    manifest_path = output / "manifest.json"
    completed = _completed_keys(raw_path)
    retriever = FileHybridRetriever(options.runbooks_root)
    prior_logical_calls = _recorded_logical_calls(raw_path)
    if prior_logical_calls > options.max_calls:
        raise ValueError(
            f"恢复记录已有 {prior_logical_calls} 次逻辑调用，高于 --max-calls={options.max_calls}；拒绝恢复"
        )
    budget = CallBudget(
        options.max_calls,
        options.max_input_tokens,
        options.max_output_tokens,
        initial_logical_calls=prior_logical_calls,
    )
    provenance = _dataset_provenance(cases)
    governance_status = "incomplete" if options.provider == "fake" or readiness else "user-authorized-review"
    manifest = {
        "schema_version": 1,
        "run_id": output.name,
        "provider": options.provider,
        "evaluation_mode": "deterministic-test-double" if options.provider == "fake" else "model-evaluation",
        "quality_claim": options.provider == "deepseek",
        "report_watermark": "DETERMINISTIC TEST DOUBLE — NOT MODEL QUALITY" if options.provider == "fake" else None,
        "git_sha": _git_sha(),
        "dataset_dir": str(options.dataset_dir.resolve()),
        "dataset_manifest_sha256": hashlib.sha256((options.dataset_dir / "incidents.jsonl").read_bytes()).hexdigest(),
        "dataset_provenance": provenance,
        "data_governance": {"status": governance_status, "gate_failures": readiness},
        "configs": [asdict(config) for config in configs],
        "prompt_versions": {"diagnosis": DIAGNOSIS_PROMPT_VERSION, "reviewer": REVIEWER_PROMPT_VERSION},
        "runbook_retrieval": "file-hybrid-runbook-snapshot",
        "runbooks_sha256": _sha256(
            [
                {"path": path.name, "sha256": hashlib.sha256(path.read_bytes()).hexdigest()}
                for path in sorted(options.runbooks_root.glob("*.md"))
            ]
        ),
        "m9_7_dataset_gate_failures": readiness,
        "planned_logical_calls": planned,
        "expected_records": len(cases) * len(configs),
        "budget": {
            "max_calls": options.max_calls,
            "max_input_tokens": options.max_input_tokens,
            "max_output_tokens": options.max_output_tokens,
            "confirmed": options.confirm_budget,
        },
        "created_at": datetime.now(UTC).isoformat().replace("+00:00", "Z"),
    }
    assert_safe(manifest)
    _write_json_atomic(manifest_path, manifest)
    with raw_path.open("a", encoding="utf-8") as handle:
        for index, case in enumerate(cases):
            case["_dataset_dir"] = str(options.dataset_dir.resolve())
            # Rotate arms per case so transient provider changes do not always affect one arm last.
            rotated = configs[index % len(configs) :] + configs[: index % len(configs)]
            for config in rotated:
                if _record_key(case["case_id"], config.name) in completed:
                    continue
                record = await _run_case(case, config, provider, budget, retriever)
                handle.write(json.dumps(record, ensure_ascii=False, sort_keys=True) + "\n")
                handle.flush()
    manifest["finished_at"] = datetime.now(UTC).isoformat().replace("+00:00", "Z")
    manifest["logical_calls"] = budget.logical_calls
    _write_json_atomic(manifest_path, manifest)
    return output


def run(options: ExperimentOptions) -> Path:
    return asyncio.run(run_experiment(options))
