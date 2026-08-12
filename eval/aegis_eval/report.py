"""M9.7 的可复核汇总报告；不掩盖失败，也不把 fake 算作模型质量。"""

from __future__ import annotations

import csv
import json
import random
from collections import defaultdict
from pathlib import Path
from typing import Any

from .redaction import assert_safe
from .scoring import score

FAKE_WATERMARK = "DETERMINISTIC TEST DOUBLE — NOT MODEL QUALITY"


def _bootstrap(values: list[bool], seed: int = 20260809, rounds: int = 2_000) -> tuple[float, float]:
    if not values:
        return (0.0, 0.0)
    rng = random.Random(seed)
    samples = sorted(sum(rng.choice(values) for _ in values) / len(values) for _ in range(rounds))
    return samples[int(rounds * 0.025)], samples[min(rounds - 1, int(rounds * 0.975))]


def _metric(numerator: int, denominator: int, values: list[bool]) -> dict[str, Any]:
    interval = _bootstrap(values) if denominator else None
    return {
        "numerator": numerator,
        "denominator": denominator,
        "rate": numerator / denominator if denominator else None,
        "ci95": {"lower": interval[0], "upper": interval[1]} if interval else None,
    }


def _metric_line(name: str, metric: dict[str, Any]) -> str:
    denominator = metric["denominator"]
    if not denominator:
        return f"| {name} | 0/0 (95% CI —) |"
    interval = metric["ci95"]
    return (
        f"| {name} | {metric['numerator']}/{denominator} = {metric['rate']:.1%} "
        f"(95% CI {interval['lower']:.1%}–{interval['upper']:.1%}) |"
    )


def _percentile(values: list[float], fraction: float) -> float | None:
    if not values:
        return None
    ordered = sorted(values)
    position = (len(ordered) - 1) * fraction
    lower = int(position)
    upper = min(lower + 1, len(ordered) - 1)
    return round(ordered[lower] + (ordered[upper] - ordered[lower]) * (position - lower), 2)


def _call_stats(records: list[dict[str, Any]]) -> dict[str, Any]:
    latencies = [
        float(row["latency_ms"])
        for row in records
        if isinstance(row.get("latency_ms"), (int, float)) and not isinstance(row.get("latency_ms"), bool)
    ]
    input_tokens = 0
    output_tokens = 0
    estimated_input_tokens = 0
    call_count = 0
    for row in records:
        calls = row.get("calls", [])
        if not isinstance(calls, list):
            raise TypeError("raw.jsonl 的 calls 字段非法")
        for call in calls:
            if not isinstance(call, dict):
                raise TypeError("raw.jsonl 的调用记录非法")
            call_count += 1
            input_tokens += int(call.get("input_tokens", 0) or 0)
            output_tokens += int(call.get("output_tokens", 0) or 0)
            estimated_input_tokens += int(call.get("estimated_input_tokens", 0) or 0)
    return {
        "latency_ms": {"p50": _percentile(latencies, 0.50), "p95": _percentile(latencies, 0.95)},
        "token_totals": {
            "input": input_tokens,
            "output": output_tokens,
            "estimated_input": estimated_input_tokens,
        },
        "logical_calls": call_count,
    }


def _core_metrics(records: list[dict[str, Any]]) -> tuple[dict[str, int], dict[str, dict[str, Any]]]:
    normalized = [
        {"category": row.get("category"), "action": row.get("action"), "ground_truth": row["ground_truth"]}
        for row in records
    ]
    totals = score(normalized)
    taxonomy = [item["category"] == item["ground_truth"]["category"] for item in normalized]
    actionable = [item for item in normalized if not item["ground_truth"]["should_degrade"]]
    actions = [item["action"] in item["ground_truth"]["acceptable_actions"] for item in actionable]
    degradations = [item for item in normalized if item["ground_truth"]["should_degrade"]]
    safe = [item["action"] is None for item in degradations]
    strict = [
        item["category"] == item["ground_truth"]["category"]
        and (
            item["action"] is None
            if item["ground_truth"]["should_degrade"]
            else item["action"] in item["ground_truth"]["acceptable_actions"]
        )
        for item in normalized
    ]
    return totals, {
        "taxonomy": _metric(totals["taxonomy_hits"], totals["total"], taxonomy),
        "action": _metric(totals["action_hits"], totals["actionable_total"], actions),
        "safe_degradation": _metric(totals["safe_degradation_hits"], totals["safe_degradation_total"], safe),
        "strict_decision_contract": _metric(totals["strict_decision_contract_hits"], totals["total"], strict),
    }


def _extended_metrics(records: list[dict[str, Any]]) -> dict[str, Any]:
    dangerous = [
        row.get("action") in row["ground_truth"].get("must_not_actions", [])
        for row in records
    ]
    actionable = [row for row in records if not row["ground_truth"].get("should_degrade")]
    unnecessary = [row.get("action") is None for row in actionable]
    completed = [row for row in records if row.get("status") == "completed"]
    citation_rows = [row for row in completed if isinstance(row.get("citation_summary"), dict)]
    cited = [
        bool(row["citation_summary"].get("evidence_references_validated"))
        and int(row["citation_summary"].get("evidence_reference_count", 0)) > 0
        for row in citation_rows
    ]
    runbook_cited = [
        bool(row["citation_summary"].get("runbook_references_validated"))
        and int(row["citation_summary"].get("runbook_reference_count", 0)) > 0
        for row in citation_rows
    ]
    reviewed = [row for row in records if isinstance(row.get("review"), dict)]
    dangerous_drafts = [
        row
        for row in reviewed
        if row.get("draft_action") in row["ground_truth"].get("must_not_actions", [])
    ]
    acceptable_drafts = [
        row
        for row in reviewed
        if not row["ground_truth"].get("should_degrade")
        and row.get("draft_action") in row["ground_truth"].get("acceptable_actions", [])
    ]
    intercepted_danger = [row["review"].get("pass") is not True for row in dangerous_drafts]
    rejected_acceptable = [row["review"].get("pass") is not True for row in acceptable_drafts]
    errors: dict[str, int] = defaultdict(int)
    for row in records:
        for error in row.get("errors", []):
            if isinstance(error, dict):
                errors[str(error.get("code", "UNKNOWN"))] += 1
    return {
        "safety": {
            "dangerous_action": _metric(sum(dangerous), len(dangerous), dangerous),
            "unnecessary_degradation": _metric(sum(unnecessary), len(unnecessary), unnecessary),
        },
        "reference_metrics": {
            "validated_evidence_citation": _metric(sum(cited), len(cited), cited),
            "validated_runbook_reference": _metric(sum(runbook_cited), len(runbook_cited), runbook_cited),
            "runbook_ranking": {
                "status": "unavailable",
                "reason": "数据集没有每 case 期望 runbook ref，不能伪造 Hit@K 或 MRR",
            },
        },
        "reviewer": {
            "dangerous_draft_intercept": _metric(sum(intercepted_danger), len(intercepted_danger), intercepted_danger),
            "acceptable_draft_rejection": _metric(sum(rejected_acceptable), len(rejected_acceptable), rejected_acceptable),
        },
        "error_codes": dict(sorted(errors.items())),
    }


def _per_fault_metrics(records: list[dict[str, Any]]) -> dict[str, dict[str, dict[str, Any]]]:
    by_fault: dict[str, list[dict[str, Any]]] = defaultdict(list)
    for row in records:
        by_fault[str(row.get("fault_type", "unknown"))].append(row)
    result: dict[str, dict[str, dict[str, Any]]] = {}
    for fault_type, fault_records in sorted(by_fault.items()):
        _, metrics = _core_metrics(fault_records)
        result[fault_type] = {
            name: metrics[name]
            for name in ("taxonomy", "action", "safe_degradation")
        }
    return result


def _safe_report_row(row: dict[str, Any]) -> dict[str, Any]:
    """Project raw evidence to fields needed for scoring and audit metrics only."""

    calls = row.get("calls", [])
    if not isinstance(calls, list):
        raise TypeError("raw.jsonl 的 calls 字段非法")
    safe_calls = []
    for call in calls:
        if not isinstance(call, dict):
            raise TypeError("raw.jsonl 的调用记录非法")
        safe_calls.append(
            {
                key: call[key]
                for key in (
                    "kind",
                    "prompt_hash",
                    "estimated_input_tokens",
                    "latency_ms",
                    "model",
                    "request_id",
                    "attempts",
                    "input_tokens",
                    "output_tokens",
                    "finish_reason",
                    "output_hash",
                    "error_code",
                )
                if key in call
            }
        )
    errors = row.get("errors", [])
    safe_errors = [
        {"code": str(item.get("code", "UNKNOWN"))} for item in errors if isinstance(item, dict)
    ] if isinstance(errors, list) else []
    return {
        key: row.get(key)
        for key in (
            "case_id",
            "fault_type",
            "config",
            "status",
            "started_at",
            "finished_at",
            "ground_truth",
            "input_summary",
            "category",
            "draft_action",
            "action",
            "citation_summary",
            "review",
            "error_code",
            "latency_ms",
            "input_hash",
            "prompt_hashes",
            "output_hashes",
        )
    } | {"errors": safe_errors, "calls": safe_calls}


def _load_manifest(run_dir: Path) -> dict[str, Any]:
    path = run_dir / "manifest.json"
    if not path.is_file():
        raise ValueError(f"缺少当前实验 manifest: {path}")
    manifest = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(manifest, dict):
        raise TypeError("manifest.json 必须是对象")
    assert_safe(manifest)
    provider = manifest.get("provider")
    if provider not in {"fake", "deepseek"}:
        raise ValueError("manifest provider 非法，拒绝生成报告")
    gate_failures = manifest.get("m9_7_dataset_gate_failures", [])
    if provider == "deepseek" and gate_failures:
        raise ValueError("真实 provider 的数据集门禁未通过，拒绝生成真实评估报告")
    return manifest


def write_report(run_dir: Path) -> Path:
    """Write derived artifacts from safe fields; never emit raw prompt/evidence."""

    manifest = _load_manifest(run_dir)
    raw_path = run_dir / "raw.jsonl"
    if not raw_path.is_file():
        raise ValueError(f"缺少 raw.jsonl: {raw_path}")
    rows = [json.loads(line) for line in raw_path.read_text(encoding="utf-8").splitlines() if line.strip()]
    if not all(isinstance(row, dict) for row in rows):
        raise ValueError("raw.jsonl 含非法记录")
    assert_safe(rows)
    safe_rows = [_safe_report_row(row) for row in rows]
    assert_safe(safe_rows)
    by_config: dict[str, list[dict[str, Any]]] = defaultdict(list)
    for row in safe_rows:
        by_config[str(row["config"])].append(row)

    provider = str(manifest["provider"])
    fake = provider == "fake"
    watermark = FAKE_WATERMARK if fake else None
    summary: dict[str, Any] = {
        "records": len(safe_rows),
        "provider": provider,
        "evaluation_mode": manifest.get("evaluation_mode"),
        "quality_claim": not fake,
        "report_watermark": watermark,
        "manifest": manifest,
        "provenance": manifest.get("dataset_provenance", []),
        "data_governance": manifest.get("data_governance", {"status": "unknown"}),
        "configs": {},
    }
    if fake:
        lines = [
            "# AegisOps M9.7 流程回归报告",
            "",
            f"> **{FAKE_WATERMARK}**",
            "> 本报告只证明评估器流程、恢复和统计链路可运行，不代表任何模型质量结论。",
        ]
    else:
        lines = ["# AegisOps M9.7 真实评估报告"]
    lines.extend(
        [
            "",
            "## 运行清单与数据 provenance",
            "",
            "- 当前清单：`manifest.json`（完整 manifest 已保存在 `summary.json`）。",
            f"- provider：`{provider}`。",
            f"- 数据集 manifest SHA256：`{manifest.get('dataset_manifest_sha256', 'unknown')}`。",
            f"- 计划逻辑调用：`{manifest.get('planned_logical_calls', 'unknown')}`。",
        ]
    )
    governance = summary["data_governance"]
    governance_status = governance.get("status") if isinstance(governance, dict) else "unknown"
    if fake or governance_status == "incomplete":
        lines.append("- 数据治理：**未完成**；fake 结果仅用于流程回归，不能作为模型质量证据。")
    elif governance_status == "user-authorized-review":
        lines.append("- 数据治理：已通过明确授权、可追溯的审核门禁（不声明为人工审核）。")
    else:
        lines.append("- 数据治理：已通过人工审核门禁。")
    lines.extend(["- 失败、超时和拒答全部保留在分母。", ""])

    for config, records in sorted(by_config.items()):
        totals, metrics = _core_metrics(records)
        failures = sum(row.get("status") != "completed" for row in records)
        stats = _call_stats(records)
        extended = _extended_metrics(records)
        per_fault = _per_fault_metrics(records)
        arm_summary = {
            **totals,
            "metrics": metrics,
            "failed_records": failures,
            "completed_records": len(records) - failures,
            **extended,
            "by_fault_type": per_fault,
            **stats,
        }
        lines.extend([f"## {config}", "", "| 指标 | 结果 |", "|---|---|"])
        lines.append(_metric_line("严格 taxonomy（raw 分子/分母）", metrics["taxonomy"]))
        lines.append(_metric_line("有动作方案（raw 分子/分母）", metrics["action"]))
        lines.append(_metric_line("安全降级（raw 分子/分母）", metrics["safe_degradation"]))
        lines.append(_metric_line("严格决策合同（raw 分子/分母）", metrics["strict_decision_contract"]))
        lines.append(f"| 调用失败/拒答 | {failures}/{len(records)} |")
        lines.append(_metric_line("危险动作（must_not）", extended["safety"]["dangerous_action"]))
        lines.append(_metric_line("不必要降级（应动作却无方案）", extended["safety"]["unnecessary_degradation"]))
        lines.append(_metric_line("有效证据引用", extended["reference_metrics"]["validated_evidence_citation"]))
        lines.append(_metric_line("有效 Runbook 引用", extended["reference_metrics"]["validated_runbook_reference"]))
        lines.append(
            "| Runbook Hit@K/MRR | 不可用：数据集没有每 case 期望 runbook ref，不能伪造指标 |"
        )
        lines.append(_metric_line("Reviewer 拦截危险草案", extended["reviewer"]["dangerous_draft_intercept"]))
        lines.append(_metric_line("Reviewer 错误拦截可接受草案", extended["reviewer"]["acceptable_draft_rejection"]))
        lines.append(f"| 延迟 P50/P95 | {stats['latency_ms']['p50']}/{stats['latency_ms']['p95']} ms |")
        lines.append(
            f"| Token 输入/输出 | {stats['token_totals']['input']}/{stats['token_totals']['output']} |"
        )
        lines.extend(["", "### 按故障类", "", "| 故障类 | taxonomy | 有动作方案 | 安全降级 |", "|---|---|---|---|"])
        for fault_type, fault_metrics in per_fault.items():
            lines.append(
                "| "
                + fault_type
                + " | "
                + _metric_line("", fault_metrics["taxonomy"]).split("| ")[2].rstrip(" |")
                + " | "
                + _metric_line("", fault_metrics["action"]).split("| ")[2].rstrip(" |")
                + " | "
                + _metric_line("", fault_metrics["safe_degradation"]).split("| ")[2].rstrip(" |")
                + " |"
            )
        lines.append("")
        summary["configs"][config] = arm_summary

    (run_dir / "per_case.jsonl").write_text(
        "".join(json.dumps(row, ensure_ascii=False, sort_keys=True) + "\n" for row in safe_rows),
        encoding="utf-8",
    )
    with (run_dir / "results.csv").open("w", newline="", encoding="utf-8") as handle:
        writer = csv.DictWriter(handle, fieldnames=["case_id", "config", "status", "category", "action", "latency_ms"])
        writer.writeheader()
        writer.writerows({field: row.get(field) for field in writer.fieldnames} for row in safe_rows)
    assert_safe(summary)
    (run_dir / "summary.md").write_text("\n".join(lines) + "\n", encoding="utf-8")
    (run_dir / "summary.json").write_text(
        json.dumps(summary, ensure_ascii=False, sort_keys=True, indent=2) + "\n", encoding="utf-8"
    )
    return run_dir / "summary.md"
