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
CONTEXT="${AEGISOPS_EVAL_CONTEXT:-}"
GROUND_TRUTH=""
REVIEWED_BY="automation-pending-human-review"

die() { printf '%s\n' "[ERROR] $*" >&2; exit 1; }

usage() {
  cat <<'EOF'
用法:
  scripts/export-eval-case.sh \
    --incident <namespace/name> --case-id <id> --fault-type <type> \
    --variant clean|noisy|sparse --run-id <campaign> --ground-truth <json> \
    --api-url <incident-api-url> [--context <kube-context>] [--output <dir>]

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
    --context) require_value "$@"; CONTEXT="$2"; shift 2 ;;
    --output) require_value "$@"; OUTPUT="$2"; shift 2 ;;
    --reviewed-by) require_value "$@"; REVIEWED_BY="$2"; shift 2 ;;
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
API_URL="$API_URL" TOKEN="$TOKEN" NAMESPACE="$namespace" NAME="$name" python3 - "$tmpdir/evidence.json" <<'PY'
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

endpoint = (
    os.environ["API_URL"].rstrip("/")
    + "/api/v1/incidents/"
    + urllib.parse.quote(os.environ["NAMESPACE"], safe="")
    + "/"
    + urllib.parse.quote(os.environ["NAME"], safe="")
    + "/evidence"
)
request = urllib.request.Request(endpoint, headers={"Authorization": "Bearer " + os.environ["TOKEN"]})
try:
    with urllib.request.urlopen(request, timeout=15) as response:
        if response.status != 200:
            raise RuntimeError(f"Incident API evidence 返回 {response.status}")
        value = json.load(response)
except (urllib.error.URLError, urllib.error.HTTPError, json.JSONDecodeError) as exc:
    raise SystemExit(f"获取脱敏 evidence 失败: {exc}") from exc
with open(sys.argv[1], "x", encoding="utf-8") as output:
    json.dump(value, output, ensure_ascii=False, sort_keys=True)
    output.write("\n")
PY

PYTHONPATH="$ROOT" python3 - "$tmpdir/incident.json" "$tmpdir/evidence.json" "$GROUND_TRUTH" "$OUTPUT" "$CASE_ID" "$FAULT_TYPE" "$VARIANT" "$RUN_ID" "$REVIEWED_BY" <<'PY'
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

# 用 allowlist 重新构造 Incident；特意不复制 status.diagnosis/status.proposal。
metadata = raw_incident.get("metadata", {})
spec = raw_incident.get("spec", {})
target = spec.get("targetRef", {})
incident = {
    "uid": metadata.get("uid", ""),
    "namespace": metadata.get("namespace", ""),
    "name": metadata.get("name", ""),
    "severity": spec.get("severity", ""),
    "target": {
        "kind": target.get("kind", ""),
        "namespace": target.get("namespace", ""),
        "name": target.get("name", ""),
    },
    "alert": {"name": spec.get("alert", {}).get("name", "")},
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
        "source": "fault-lab-controlled-campaign",
        "campaign_run_id": run_id,
        "captured_at": datetime.now(UTC).isoformat().replace("+00:00", "Z"),
        # This value deliberately tells downstream reports it is not a human assertion.
        "reviewed_by": reviewed_by,
    },
}
validate_case(case, output)
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
entries.append(f"{hashlib.sha256(manifest.read_bytes()).hexdigest()}  incidents.jsonl")
tmp = sums.with_suffix(".tmp")
with tmp.open("x", encoding="utf-8") as handle:
    handle.write("\n".join(entries) + "\n")
os.replace(tmp, sums)
print(f"exported safe eval case: {case_id}")
PY
