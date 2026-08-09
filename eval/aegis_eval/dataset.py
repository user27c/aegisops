"""M9.7 真实采集数据集的最小严格 schema。"""

from __future__ import annotations

import json
import re
from pathlib import Path
from typing import Any

from .redaction import assert_safe

CATEGORIES = {
    "OOMKilled", "CrashLoop", "ImagePullBackOff", "CheckoutFailure",
    "ProbeFailure", "CPUThrottling", "DependencyTimeout", "Unknown",
}
ACTIONS = {
    "RestartWorkload", "ScaleDeployment", "PatchResourceLimit",
    "RollbackDeployment", "RestoreConfigMap",
}
VARIANTS = {"clean", "noisy", "sparse"}
_CASE_ID = re.compile(r"^[a-z0-9][a-z0-9-]{2,79}$")


class DatasetError(ValueError):
    """An input is not a publishable M9.7 case."""


def load_cases(dataset_dir: Path) -> list[dict[str, Any]]:
    """Load, validate and secret-scan every case and its evidence payload."""

    manifest = dataset_dir / "incidents.jsonl"
    if not manifest.is_file():
        raise DatasetError(f"缺少数据集清单: {manifest}")
    cases: list[dict[str, Any]] = []
    seen: set[str] = set()
    for lineno, line in enumerate(manifest.read_text(encoding="utf-8").splitlines(), 1):
        if not line.strip():
            continue
        try:
            case = json.loads(line)
        except json.JSONDecodeError as exc:
            raise DatasetError(f"incidents.jsonl:{lineno} 不是 JSON: {exc.msg}") from exc
        validate_case(case, dataset_dir)
        case_id = case["case_id"]
        if case_id in seen:
            raise DatasetError(f"重复 case_id: {case_id}")
        seen.add(case_id)
        cases.append(case)
    if not cases:
        raise DatasetError("数据集没有任何 case")
    return cases


def validate_case(case: Any, dataset_dir: Path) -> None:
    if not isinstance(case, dict):
        raise DatasetError("case 必须是对象")
    case_id = _required_string(case, "case_id")
    if not _CASE_ID.fullmatch(case_id):
        raise DatasetError(f"非法 case_id: {case_id}")
    if case.get("dataset_version") != "v1":
        raise DatasetError(f"{case_id}: dataset_version 必须为 v1")
    _required_string(case, "fault_type")
    if case.get("variant") not in VARIANTS:
        raise DatasetError(f"{case_id}: variant 必须是 {sorted(VARIANTS)}")
    if not isinstance(case.get("incident"), dict):
        raise DatasetError(f"{case_id}: incident 必须是对象")
    path = _evidence_path(case_id, case.get("evidence_path"), dataset_dir)
    try:
        evidence = json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        raise DatasetError(f"{case_id}: evidence JSON 非法: {exc.msg}") from exc
    if not isinstance(evidence, dict) or not isinstance(evidence.get("items"), list):
        raise DatasetError(f"{case_id}: evidence 必须含 items 数组")
    try:
        assert_safe(case["incident"])
        assert_safe(evidence)
    except ValueError as exc:
        raise DatasetError(f"{case_id}: {exc}") from exc
    truth = case.get("ground_truth")
    if not isinstance(truth, dict):
        raise DatasetError(f"{case_id}: ground_truth 必须是对象")
    if truth.get("category") not in CATEGORIES:
        raise DatasetError(f"{case_id}: category 不在项目 taxonomy")
    keywords = truth.get("root_cause_keywords")
    if not isinstance(keywords, list) or not all(isinstance(item, str) and item for item in keywords):
        raise DatasetError(f"{case_id}: root_cause_keywords 必须是非空字符串数组")
    for field in ("acceptable_actions", "must_not_actions"):
        actions = truth.get(field)
        if not isinstance(actions, list) or not all(action in ACTIONS for action in actions):
            raise DatasetError(f"{case_id}: {field} 含未知动作")
    if not isinstance(truth.get("should_degrade"), bool):
        raise DatasetError(f"{case_id}: should_degrade 必须为 bool")
    if truth["should_degrade"] and truth["acceptable_actions"]:
        raise DatasetError(f"{case_id}: 安全降级样本不能含 acceptable_actions")
    provenance = case.get("provenance")
    if not isinstance(provenance, dict):
        raise DatasetError(f"{case_id}: provenance 必须是对象")
    for field in ("source", "campaign_run_id", "captured_at", "reviewed_by"):
        _required_string(provenance, field, prefix=f"{case_id}: provenance.")


def _required_string(value: dict[str, Any], field: str, prefix: str = "") -> str:
    result = value.get(field)
    if not isinstance(result, str) or not result.strip():
        raise DatasetError(f"{prefix}{field} 必须是非空字符串")
    return result


def _evidence_path(case_id: str, value: Any, dataset_dir: Path) -> Path:
    if not isinstance(value, str) or not value.startswith("evidence/"):
        raise DatasetError(f"{case_id}: evidence_path 必须位于 evidence/")
    path = (dataset_dir / value).resolve()
    evidence_root = (dataset_dir / "evidence").resolve()
    if evidence_root not in path.parents or path.suffix != ".json":
        raise DatasetError(f"{case_id}: evidence_path 非法")
    if not path.is_file():
        raise DatasetError(f"{case_id}: evidence 文件不存在: {value}")
    return path
