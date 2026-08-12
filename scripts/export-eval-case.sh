#!/usr/bin/env bash
# 从受控 Incident 导出 M9.7 评估 case。只接受 Incident API 的脱敏 evidence；
# 不读取 Kubernetes Secret，也不保存诊断/提案，避免把模型旧答案泄漏给评估输入。
set -Eeuo pipefail
IFS=$'\n\t'

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INCIDENT=""
CASE_ID=""
FAULT_TYPE=""
VARIANT=""
RUN_ID=""
OUTPUT="$ROOT/eval/datasets/v1"
API_URL="${AEGISOPS_EVAL_INCIDENT_API_URL:-}"
TOKEN="${AEGISOPS_EVAL_VIEWER_TOKEN:-}"
DIAGNOSIS_URL="${AEGISOPS_EVAL_DIAGNOSIS_URL:-}"
DIAGNOSIS_TOKEN="${AEGISOPS_EVAL_DIAGNOSIS_TOKEN:-}"
CONTEXT="${AEGISOPS_EVAL_CONTEXT:-}"
GROUND_TRUTH=""
REVIEWED_BY="automation-pending-human-review"
SCENARIO_TAGS=""
CAMPAIGN_RECORD=""
ALLOW_ROLLOUT_EVIDENCE=false

die() { printf '%s\n' "[ERROR] $*" >&2; exit 1; }

usage() {
  cat <<'EOF'
用法:
  scripts/export-eval-case.sh \
    --incident <namespace/name> --case-id <id> --fault-type <type> \
    --variant clean|noisy|sparse --run-id <campaign> --ground-truth <json> \
    --api-url <incident-api-url> [--scenario-tags <comma-separated>] \
    --campaign-record <campaigns/<run>.jsonl> \
    [--allow-rollout-evidence] \
    [--context <kube-context>] [--output <dir>]

环境变量 AEGISOPS_EVAL_VIEWER_TOKEN 提供只读 viewer token；它只在本进程内
用于 Incident API，不会写入输出或日志。ground-truth JSON 必须包含 category、
root_cause_keywords、acceptable_actions、must_not_actions、should_degrade。
EOF
}

require_value() { [[ $# -ge 2 && -n "$2" ]] || die "$1 需要一个值"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --incident) require_value "$@"; INCIDENT="$2"; shift 2 ;;
    --case-id) require_value "$@"; CASE_ID="$2"; shift 2 ;;
    --fault-type) require_value "$@"; FAULT_TYPE="$2"; shift 2 ;;
    --variant) require_value "$@"; VARIANT="$2"; shift 2 ;;
    --run-id) require_value "$@"; RUN_ID="$2"; shift 2 ;;
    --ground-truth) require_value "$@"; GROUND_TRUTH="$2"; shift 2 ;;
    --api-url) require_value "$@"; API_URL="$2"; shift 2 ;;
    --diagnosis-url) require_value "$@"; DIAGNOSIS_URL="$2"; shift 2 ;;
    --context) require_value "$@"; CONTEXT="$2"; shift 2 ;;
    --output) require_value "$@"; OUTPUT="$2"; shift 2 ;;
    --reviewed-by) require_value "$@"; REVIEWED_BY="$2"; shift 2 ;;
    --scenario-tags) require_value "$@"; SCENARIO_TAGS="$2"; shift 2 ;;
    --campaign-record) require_value "$@"; CAMPAIGN_RECORD="$2"; shift 2 ;;
    --allow-rollout-evidence) ALLOW_ROLLOUT_EVIDENCE=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "未知参数: $1" ;;
  esac
done

[[ "$INCIDENT" =~ ^[^/[:space:]]+/[^/[:space:]]+$ ]] || die "--incident 必须是 namespace/name"
[[ "$CASE_ID" =~ ^[a-z0-9][a-z0-9-]{2,79}$ ]] || die "--case-id 必须是 3-80 位小写字母/数字/连字符"
[[ "$VARIANT" == "clean" || "$VARIANT" == "noisy" || "$VARIANT" == "sparse" ]] || die "--variant 非法"
[[ -n "$FAULT_TYPE" && -n "$RUN_ID" ]] || die "--fault-type 和 --run-id 必填"
[[ -n "$API_URL" ]] || die "--api-url 或 AEGISOPS_EVAL_INCIDENT_API_URL 必填"
[[ -n "$TOKEN" ]] || die "AEGISOPS_EVAL_VIEWER_TOKEN 必填（只读 Incident API token）"
[[ -f "$GROUND_TRUTH" ]] || die "--ground-truth 文件不存在: $GROUND_TRUTH"
[[ "$CAMPAIGN_RECORD" =~ ^campaigns/[a-z0-9][a-z0-9-]{2,119}\.jsonl$ ]] || die "--campaign-record 必须位于 campaigns/"
[[ -f "$OUTPUT/$CAMPAIGN_RECORD" ]] || die "--campaign-record 文件不存在: $OUTPUT/$CAMPAIGN_RECORD"
command -v kubectl >/dev/null || die "缺少 kubectl"
command -v python3 >/dev/null || die "缺少 python3"

namespace="${INCIDENT%%/*}"
name="${INCIDENT#*/}"
tmpdir="$(mktemp -d)"
trap 'rm -rf -- "$tmpdir"' EXIT

kubectl_args=()
[[ -n "$CONTEXT" ]] && kubectl_args+=(--context "$CONTEXT")
kubectl "${kubectl_args[@]}" -n "$namespace" get aiopsincident "$name" -o json > "$tmpdir/incident.json"

# Python 使用内存中的 header，避免把 viewer token 放进 curl 命令行或落盘。
API_URL="$API_URL" TOKEN="$TOKEN" DIAGNOSIS_URL="$DIAGNOSIS_URL" DIAGNOSIS_TOKEN="$DIAGNOSIS_TOKEN" \
  ALLOW_ROLLOUT_EVIDENCE="$ALLOW_ROLLOUT_EVIDENCE" \
  NAMESPACE="$namespace" NAME="$name" python3 - "$tmpdir/evidence.json" "$tmpdir/incident.json" <<'PY'
import json
import hashlib
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

base = os.environ["API_URL"].rstrip("/")
suffix = "/".join((
    urllib.parse.quote(os.environ["NAMESPACE"], safe=""),
    urllib.parse.quote(os.environ["NAME"], safe=""),
    "evidence",
))
# Older local incident-api images serve detail endpoints at /incidents while
# list/detail stay at /api/v1.  Only a route-level 404 may fall back; auth,
# transport, and decoding errors must remain fail-closed.
endpoints = (f"{base}/api/v1/incidents/{suffix}", f"{base}/incidents/{suffix}")
value = None
for index, endpoint in enumerate(endpoints):
    request = urllib.request.Request(endpoint, headers={"Authorization": "Bearer " + os.environ["TOKEN"]})
    try:
        with urllib.request.urlopen(request, timeout=15) as response:
            if response.status != 200:
                raise RuntimeError(f"Incident API evidence 返回 {response.status}")
            if response.headers.get_content_type() != "application/json":
                continue
            parsed = json.load(response)
            if isinstance(parsed, dict):
                value = parsed
                break
    except urllib.error.HTTPError as exc:
        if exc.code == 404 and index == 0:
            continue
        raise SystemExit(f"获取脱敏 evidence 失败: HTTP {exc.code}") from exc
    except (urllib.error.URLError, json.JSONDecodeError) as exc:
        raise SystemExit(f"获取脱敏 evidence 失败: {exc}") from exc
if value is None and os.environ["DIAGNOSIS_URL"] and os.environ["DIAGNOSIS_TOKEN"]:
    incident = json.load(open(sys.argv[2], encoding="utf-8"))
    evidence_id = incident.get("status", {}).get("analysis", {}).get("evidenceID", "")
    if not isinstance(evidence_id, str) or not evidence_id:
        raise SystemExit("Incident 缺少 status.analysis.evidenceID，拒绝猜测 evidence ID")
    endpoint = os.environ["DIAGNOSIS_URL"].rstrip("/") + "/v1/evidence/" + urllib.parse.quote(evidence_id, safe="")
    request = urllib.request.Request(endpoint, headers={"Authorization": "Bearer " + os.environ["DIAGNOSIS_TOKEN"]})
    try:
        with urllib.request.urlopen(request, timeout=15) as response:
            if response.headers.get_content_type() != "application/json":
                raise SystemExit("Diagnosis evidence 返回非 JSON，拒绝导出")
            parsed = json.load(response)
            if not isinstance(parsed, dict):
                raise SystemExit("Diagnosis evidence 返回非对象，拒绝导出")
            payload = parsed.get("payload", parsed)
            if not isinstance(payload, dict):
                raise SystemExit("Diagnosis evidence payload 非对象，拒绝导出")
            value = payload
    except (urllib.error.URLError, urllib.error.HTTPError, json.JSONDecodeError) as exc:
        raise SystemExit(f"获取 Diagnosis evidence 失败: {exc}") from exc
if value is None:
    raise SystemExit("获取脱敏 evidence 失败: Incident API 不可用且未提供 Diagnosis fallback")

# 每例采集前都会为隔离而新建 Pod；KubernetesCollector 因而可能包含与
# 本次注入无因果关系的旧 revision。除 ImagePullBackOff（唯一实际修改
# Deployment image 的受控注入）外，不把 RolloutDiff 投给模型或用作真值。
if os.environ.get("ALLOW_ROLLOUT_EVIDENCE") != "true":
    items = value.get("items")
    if isinstance(items, list):
        projected_items = [item for item in items if not (isinstance(item, dict) and item.get("kind") == "RolloutDiff")]
        if len(projected_items) != len(items):
            source_hash = value.get("hash", "")
            value = dict(value)
            value["items"] = projected_items
            value["projection"] = {
                "kind": "m97-action-semantic-v1",
                "excludedKinds": ["RolloutDiff"],
                "sourceHash": source_hash,
            }
            canonical = json.dumps({key: val for key, val in value.items() if key != "hash"}, ensure_ascii=False, sort_keys=True)
            value["hash"] = hashlib.sha256(canonical.encode("utf-8")).hexdigest()
with open(sys.argv[1], "x", encoding="utf-8") as output:
    json.dump(value, output, ensure_ascii=False, sort_keys=True)
    output.write("\n")
PY

PYTHONPATH="$ROOT" SCENARIO_TAGS="$SCENARIO_TAGS" CAMPAIGN_RECORD="$CAMPAIGN_RECORD" python3 - "$tmpdir/incident.json" "$tmpdir/evidence.json" "$GROUND_TRUTH" "$OUTPUT" "$CASE_ID" "$FAULT_TYPE" "$VARIANT" "$RUN_ID" "$REVIEWED_BY" <<'PY'
import hashlib
import json
import os
import sys
from datetime import UTC, datetime
from pathlib import Path

from eval.aegis_eval.dataset import validate_case
from eval.aegis_eval.redaction import assert_safe

raw_incident = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
evidence = json.loads(Path(sys.argv[2]).read_text(encoding="utf-8"))
truth = json.loads(Path(sys.argv[3]).read_text(encoding="utf-8"))
output = Path(sys.argv[4]).resolve()
case_id, fault_type, variant, run_id, reviewed_by = sys.argv[5:]

# 用固定的中性标识重新构造模型输入 Incident；特意不复制
# status.diagnosis/status.proposal，也不把资源名或 alertname 中可能存在的
# taxonomy 线索带进 A(alert-only) 基线。
metadata = raw_incident.get("metadata", {})
spec = raw_incident.get("spec", {})
target = spec.get("targetRef", {})
incident = {
    "uid": metadata.get("uid", ""),
    "namespace": metadata.get("namespace", ""),
    "name": "controlled-evaluation",
    "severity": spec.get("severity", ""),
    "target": {
        "kind": target.get("kind", ""),
        "namespace": target.get("namespace", ""),
        "name": target.get("name", ""),
    },
    "alert": {"name": "controlled-evaluation"},
}
assert_safe(incident)
assert_safe(evidence)

evidence_dir = output / "evidence"
evidence_dir.mkdir(parents=True, exist_ok=True)
evidence_path = evidence_dir / f"{case_id}.json"
try:
    with evidence_path.open("x", encoding="utf-8") as handle:
        json.dump(evidence, handle, ensure_ascii=False, sort_keys=True, indent=2)
        handle.write("\n")
except FileExistsError as exc:
    raise SystemExit(f"拒绝覆盖既有 evidence: {evidence_path}") from exc

case = {
    "case_id": case_id,
    "dataset_version": "v1",
    "fault_type": fault_type,
    "variant": variant,
    "incident": incident,
    "evidence_path": f"evidence/{case_id}.json",
    "ground_truth": truth,
    "provenance": {
        "source": "fault-lab-controlled-campaign-v2",
        "campaign_run_id": run_id,
        "captured_at": datetime.now(UTC).isoformat().replace("+00:00", "Z"),
        # This value deliberately tells downstream reports it is not a human assertion.
        "reviewed_by": reviewed_by,
        "campaign_record": os.environ["CAMPAIGN_RECORD"],
    },
}
tags = [tag.strip() for tag in os.environ.get("SCENARIO_TAGS", "").split(",") if tag.strip()]
if tags:
    case["scenario_tags"] = tags
try:
    validate_case(case, output)
except Exception:
    # Evidence is written first because validation intentionally requires its
    # on-disk path.  Do not leave an orphan payload if any fail-closed gate
    # rejects the case before its manifest entry is appended.
    evidence_path.unlink(missing_ok=True)
    raise
manifest = output / "incidents.jsonl"
existing = manifest.read_text(encoding="utf-8").splitlines() if manifest.exists() else []
if any(json.loads(line).get("case_id") == case_id for line in existing if line.strip()):
    evidence_path.unlink()
    raise SystemExit(f"拒绝重复 case_id: {case_id}")
with manifest.open("a", encoding="utf-8") as handle:
    handle.write(json.dumps(case, ensure_ascii=False, sort_keys=True) + "\n")

sums = output / "SHA256SUMS"
entries = []
for path in sorted((output / "evidence").glob("*.json")):
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    entries.append(f"{digest}  evidence/{path.name}")
for path in sorted((output / "campaigns").glob("*.jsonl")):
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    entries.append(f"{digest}  campaigns/{path.name}")
entries.append(f"{hashlib.sha256(manifest.read_bytes()).hexdigest()}  incidents.jsonl")
tmp = sums.with_suffix(".tmp")
with tmp.open("x", encoding="utf-8") as handle:
    handle.write("\n".join(entries) + "\n")
os.replace(tmp, sums)
print(f"exported safe eval case: {case_id}")
PY
