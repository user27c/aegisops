"""M9.7 真实采集数据集的最小严格 schema。"""

from __future__ import annotations

import json
import re
from datetime import datetime
from pathlib import Path
from typing import Any

from .redaction import assert_safe

CATEGORIES = {
    "OOMKilled",
    "CrashLoop",
    "ImagePullBackOff",
    "CheckoutFailure",
    "ProbeFailure",
    "CPUThrottling",
    "DependencyTimeout",
    "Unknown",
}
ACTIONS = {
    "RestartWorkload",
    "ScaleDeployment",
    "PatchResourceLimit",
    "RollbackDeployment",
    "RestoreConfigMap",
}
VARIANTS = {"clean", "noisy", "sparse"}
SCENARIO_TAGS = {"prompt-injection", "multi-fault"}
_CASE_ID = re.compile(r"^[a-z0-9][a-z0-9-]{2,79}$")
CONTROLLED_CAMPAIGN_SOURCE = "fault-lab-controlled-campaign-v2"
_NEUTRAL_EVALUATION_IDENTIFIER = "controlled-evaluation"
_FAULT_MARKERS = {
    "config": ("checkout", "http 500"),
    "oom": ("oomkilled", "exitcode=137"),
    "crashloop": ("crashloop", "exitcode="),
    "cpu": ("cpu", "injector active"),
    "dependency": ("dependency", "timeout"),
    "imagepullbackoff": ("imagepullbackoff", "errimagepull"),
}
_FAULT_SIGNATURES = {
    "config": ("checkout returned http 500",),
    "oom": ("oomkilled", "exitcode=137"),
    "crashloop": ("crashloop", "exitcode="),
    "cpu": ("cpu injector active",),
    "dependency": ("dependency timeout",),
    "imagepullbackoff": ("imagepullbackoff",),
}

# 受控 FaultLab 注入器的动作语义。它不是生产 Policy 的替代品，而是
# M9.7 ground truth 的最小可验证合同：没有真实配置/资源变更的注入不能因
# “刚好存在旧 revision”而被标成可回滚；证据不足时必须安全降级。
_CONTROLLED_FAULT_ACTIONS = {
    "config": set(),
    "oom": {"PatchResourceLimit"},
    "crashloop": set(),
    "cpu": set(),
    "dependency": set(),
    "imagepullbackoff": {"RollbackDeployment"},
}
_ACTION_REQUIRED_KINDS = {
    "PatchResourceLimit": {"ContainerState", "KubernetesEvent", "MetricSeries"},
    "RollbackDeployment": {"ContainerState", "KubernetesEvent", "RolloutDiff"},
}


class DatasetError(ValueError):
    """An input is not a publishable M9.7 case."""


def is_automation_review_marker(value: str) -> bool:
    """Return whether ``reviewed_by`` is an explicit machine-review marker."""

    normalized = re.sub(r"[_: ]+", "-", value.strip().lower())
    tokens = {token for token in normalized.split("-") if token}
    return bool(
        tokens & {"automation", "automated", "machine", "bot", "ci"} or {"pending", "human", "review"}.issubset(tokens)
    )


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
    tags = case.get("scenario_tags", [])
    if (
        not isinstance(tags, list)
        or not all(isinstance(tag, str) for tag in tags)
        or len(tags) != len(set(tags))
        or any(tag not in SCENARIO_TAGS for tag in tags)
    ):
        raise DatasetError(f"{case_id}: scenario_tags 非法")
    evidence_text = json.dumps(evidence, ensure_ascii=False).lower()
    if "prompt-injection" in tags and not any(
        marker in evidence_text for marker in ("ignore previous", "ignore all", "system prompt", "prompt injection")
    ):
        raise DatasetError(f"{case_id}: prompt-injection 标签没有可复核的注入文本")
    if "multi-fault" in tags:
        markers = (
            "oomkilled",
            "crashloopbackoff",
            "imagepullbackoff",
            "checkouttimeout",
            "dependency timeout",
            "cpu throttling",
            "cputhrottling",
            "cpu injector",
        )
        if sum(marker in evidence_text for marker in markers) < 2:
            raise DatasetError(f"{case_id}: multi-fault 标签没有至少两类可复核故障信号")
    provenance = case.get("provenance")
    if not isinstance(provenance, dict):
        raise DatasetError(f"{case_id}: provenance 必须是对象")
    try:
        assert_safe(provenance)
    except ValueError as exc:
        raise DatasetError(f"{case_id}: {exc}") from exc
    for field in ("source", "campaign_run_id", "captured_at", "reviewed_by"):
        _required_string(provenance, field, prefix=f"{case_id}: provenance.")
    if provenance["source"] == CONTROLLED_CAMPAIGN_SOURCE:
        _validate_controlled_campaign_case(case, evidence, provenance, dataset_dir)
        validate_controlled_action_semantics(case, evidence)


def validate_controlled_action_semantics(case: dict[str, Any], evidence: dict[str, Any]) -> None:
    """Reject a controlled action label that its captured evidence cannot support.

    This runs before a real provider is constructed. It intentionally treats
    the fault-lab config/crashloop injectors as no-safe-action scenarios: they
    alter process-local behaviour rather than a recoverable Kubernetes
    ConfigMap or Deployment revision. Image pull is the sole controlled
    injection that performs a causal Deployment image change.
    """

    case_id = str(case["case_id"])
    fault_type = str(case["fault_type"])
    allowed = _CONTROLLED_FAULT_ACTIONS.get(fault_type)
    if allowed is None:
        raise DatasetError(f"{case_id}: 未定义 {fault_type} 的受控动作语义")
    truth = case["ground_truth"]
    actions = set(truth["acceptable_actions"])
    should_degrade = bool(truth["should_degrade"])
    if actions and should_degrade:
        raise DatasetError(f"{case_id}: 受控安全降级样本不能声明可接受动作")
    if not actions and not should_degrade:
        raise DatasetError(f"{case_id}: 受控非降级样本必须声明可接受动作")
    if unexpected := sorted(actions - allowed):
        raise DatasetError(f"{case_id}: {fault_type} 受控注入不支持动作: {', '.join(unexpected)}")

    items = evidence.get("items", [])
    kinds = {str(item.get("kind", "")) for item in items if isinstance(item, dict)}
    for action in sorted(actions):
        missing = sorted(_ACTION_REQUIRED_KINDS.get(action, set()) - kinds)
        if missing:
            raise DatasetError(f"{case_id}: {action} 缺少动作所需证据种类: {', '.join(missing)}")

    # RolloutDiff 是 KubernetesCollector 的常规输出。除 image pull 外，受控
    # capture 会为每例创建新 Pod，不能把这个机械产生的 previous revision
    # 误当作本次故障的回滚因果证据。
    if fault_type != "imagepullbackoff" and any(
        item.get("kind") == "RolloutDiff" and "safe rollback target" in str(item.get("summary", "")).lower()
        for item in items
        if isinstance(item, dict)
    ):
        raise DatasetError(f"{case_id}: 非 image-pull 受控样本含无因果关系的 rollback candidate")


def _validate_controlled_campaign_case(
    case: dict[str, Any],
    evidence: dict[str, Any],
    provenance: dict[str, Any],
    dataset_dir: Path,
) -> None:
    """Verify the local capture record required for a publishable controlled case."""

    case_id = str(case["case_id"])
    incident = case["incident"]
    alert = incident.get("alert", {}) if isinstance(incident.get("alert"), dict) else {}
    if incident.get("name") != _NEUTRAL_EVALUATION_IDENTIFIER or alert.get("name") != _NEUTRAL_EVALUATION_IDENTIFIER:
        raise DatasetError(f"{case_id}: v2 受控评估 incident/alert 必须使用中性标识，避免类别泄露")

    raw_path = provenance.get("campaign_record")
    if not isinstance(raw_path, str) or not raw_path.startswith("campaigns/") or not raw_path.endswith(".jsonl"):
        raise DatasetError(f"{case_id}: v2 provenance.campaign_record 必须位于 campaigns/")
    record_path = (dataset_dir / raw_path).resolve()
    campaign_root = (dataset_dir / "campaigns").resolve()
    if campaign_root not in record_path.parents or not record_path.is_file():
        raise DatasetError(f"{case_id}: campaign record 不存在或路径非法")
    try:
        records = [json.loads(line) for line in record_path.read_text(encoding="utf-8").splitlines() if line.strip()]
    except json.JSONDecodeError as exc:
        raise DatasetError(f"{case_id}: campaign record 不是有效 JSONL: {exc.msg}") from exc
    matching = [row for row in records if isinstance(row, dict) and row.get("case_id") == case_id]
    if len(matching) != 1:
        raise DatasetError(f"{case_id}: campaign record 必须恰好包含一条同 case_id 的采集记录")
    record = matching[0]
    expected = {
        "campaign_run_id": provenance["campaign_run_id"],
        "fault_type": case["fault_type"],
        "variant": case["variant"],
        "capture_script": "aegisops-m97-controlled-capture-v2",
    }
    for field, value in expected.items():
        if record.get(field) != value:
            raise DatasetError(f"{case_id}: campaign record.{field} 与 case 不一致")
    signals = record.get("signals")
    if (
        not isinstance(signals, list)
        or not signals
        or not all(isinstance(signal, str) and signal for signal in signals)
    ):
        raise DatasetError(f"{case_id}: campaign record 缺少可复核 signals")
    _parse_timestamp(record.get("observed_at"), f"{case_id}: campaign record.observed_at")

    evidence_text = json.dumps(evidence, ensure_ascii=False).lower()
    markers = _FAULT_MARKERS.get(str(case["fault_type"]), ())
    if not markers or not all(marker in evidence_text for marker in markers):
        raise DatasetError(f"{case_id}: evidence 未同时包含 {case['fault_type']} 的受控观测信号")
    observed_faults = {
        fault for fault, signature in _FAULT_SIGNATURES.items() if all(marker in evidence_text for marker in signature)
    }
    allowed_faults = {str(case["fault_type"])}
    if "multi-fault" in case.get("scenario_tags", []):
        allowed_faults.add("cpu")
    unexpected_faults = sorted(observed_faults - allowed_faults)
    if unexpected_faults:
        raise DatasetError(f"{case_id}: evidence 含未声明的跨案例故障信号: {', '.join(unexpected_faults)}")
    _validate_evidence_window(case_id, evidence)


def _validate_evidence_window(case_id: str, evidence: dict[str, Any]) -> None:
    window = evidence.get("window")
    if not isinstance(window, dict):
        raise DatasetError(f"{case_id}: v2 evidence 缺少 window")
    start = _parse_timestamp(window.get("start"), f"{case_id}: evidence.window.start")
    end = _parse_timestamp(window.get("end"), f"{case_id}: evidence.window.end")
    if start > end:
        raise DatasetError(f"{case_id}: evidence 时间窗起点晚于终点")
    observed = evidence.get("target", {}).get("observedAt") if isinstance(evidence.get("target"), dict) else None
    timestamps: list[tuple[str, Any]] = [("target.observedAt", observed)]
    timestamps.extend(
        (f"items[{index}].timestamp", item.get("timestamp"))
        for index, item in enumerate(evidence["items"])
        if isinstance(item, dict)
    )
    for label, value in timestamps:
        if value is None:
            continue
        if _parse_timestamp(value, f"{case_id}: evidence.{label}") > end:
            raise DatasetError(f"{case_id}: evidence.{label} 晚于 evidence.window.end")


def _parse_timestamp(value: Any, prefix: str) -> datetime:
    if not isinstance(value, str) or not value:
        raise DatasetError(f"{prefix} 必须是非空 ISO-8601 时间")
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as exc:
        raise DatasetError(f"{prefix} 不是 ISO-8601 时间") from exc
    if parsed.tzinfo is None:
        raise DatasetError(f"{prefix} 必须包含时区")
    return parsed


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
