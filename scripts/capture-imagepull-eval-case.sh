#!/usr/bin/env bash
# 可逆地把隔离 Kind 的 FaultLab 镜像改为无效引用，采集 ImagePullBackOff evidence。
# 只允许 kind-aegisops-e2e；无论成功/失败都会恢复本次读取到的原镜像。
set -Eeuo pipefail
IFS=$'\n\t'

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KUBECONFIG_PATH="${AEGISOPS_EVAL_KUBECONFIG:-$HOME/.kube/config}"
CONTEXT="kind-aegisops-e2e"
ENV_FILE="$ROOT/.local/e2e/environment.json"
CASE_ID=""
VARIANT=""
RUN_ID="m97-controlled-$(date -u +%Y%m%dT%H%M%SZ)"
OUTPUT="$ROOT/eval/datasets/v1"

die() { printf '%s\n' "[ERROR] $*" >&2; exit 1; }
require_value() { [[ $# -ge 2 && -n "$2" ]] || die "$1 需要一个值"; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --case-id) require_value "$@"; CASE_ID="$2"; shift 2 ;;
    --variant) require_value "$@"; VARIANT="$2"; shift 2 ;;
    --run-id) require_value "$@"; RUN_ID="$2"; shift 2 ;;
    --output) require_value "$@"; OUTPUT="$2"; shift 2 ;;
    -h|--help) printf '%s\n' '用法: capture-imagepull-eval-case.sh --case-id <id> --variant clean|noisy|sparse'; exit 0 ;;
    *) die "未知参数: $1" ;;
  esac
done
[[ "$CASE_ID" =~ ^[a-z0-9][a-z0-9-]{2,79}$ ]] || die "--case-id 非法"
[[ "$VARIANT" == clean || "$VARIANT" == noisy || "$VARIANT" == sparse ]] || die "--variant 非法"
[[ -f "$ENV_FILE" && -f "$ROOT/eval/ground-truth/imagepullbackoff.json" ]] || die "缺少 E2E 环境或 ground truth"

NAMESPACE="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["namespace"])' "$ENV_FILE")"
GATEWAY_URL="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["gatewayUrl"])' "$ENV_FILE")"
INCIDENT_API_URL="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["incidentApiUrl"])' "$ENV_FILE")"
DIAGNOSIS_URL="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["diagnosisUrl"])' "$ENV_FILE")"
WEBHOOK_TOKEN="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["webhookToken"])' "$ENV_FILE")"
VIEWER_TOKEN="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["viewerToken"])' "$ENV_FILE")"
DIAGNOSIS_TOKEN="$(<"$ROOT/.local/secrets/diagnosis-token")"
kubectl_cmd=(kubectl --kubeconfig "$KUBECONFIG_PATH" --context "$CONTEXT" -n "$NAMESPACE")

# ImagePull evidence must not inherit the preceding case's container state or
# Pod-scoped events.  Begin from a fresh, ready Pod before applying the
# reversible invalid-image patch.
"${kubectl_cmd[@]}" rollout restart deployment/faultlab >/dev/null
"${kubectl_cmd[@]}" rollout status deployment/faultlab --timeout=120s >/dev/null
fresh_pod=""
for _ in {1..30}; do
  fresh_pod="$("${kubectl_cmd[@]}" get pods -l app.kubernetes.io/instance=faultlab -o json | python3 -c 'import json,sys; d=json.load(sys.stdin); pods=d.get("items",[]); statuses=pods[0].get("status",{}).get("containerStatuses",[]) if len(pods)==1 else []; ok=bool(statuses) and all(s.get("ready") and not s.get("lastState") for s in statuses); print(pods[0]["metadata"]["uid"] if ok else "")')"
  [[ -n "$fresh_pod" ]] && break
  sleep 1
done
[[ -n "$fresh_pod" ]] || die "未获得无历史终止状态的全新 faultlab Pod"
ORIGINAL_IMAGE="$("${kubectl_cmd[@]}" get deployment faultlab -o json | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d["spec"]["template"]["spec"]["containers"][0]["image"])')"
restore() {
  "${kubectl_cmd[@]}" set image deployment/faultlab faultlab="$ORIGINAL_IMAGE" >/dev/null 2>&1 || true
  "${kubectl_cmd[@]}" rollout status deployment/faultlab --timeout=180s >/dev/null 2>&1 || true
}
trap restore EXIT

CAMPAIGN_RECORD="campaigns/$RUN_ID.jsonl"

record_campaign_observation() {
  OUTPUT="$OUTPUT" CAMPAIGN_RECORD="$CAMPAIGN_RECORD" RUN_ID="$RUN_ID" CASE_ID="$CASE_ID" VARIANT="$VARIANT" \
    SCRIPT_PATH="$ROOT/scripts/capture-imagepull-eval-case.sh" python3 - <<'PY'
import hashlib
import json
import os
from datetime import UTC, datetime
from pathlib import Path

root = Path(os.environ["OUTPUT"])
record_path = root / os.environ["CAMPAIGN_RECORD"]
record_path.parent.mkdir(parents=True, exist_ok=True)
if record_path.exists():
    existing = [json.loads(line) for line in record_path.read_text(encoding="utf-8").splitlines() if line.strip()]
    if any(item.get("case_id") == os.environ["CASE_ID"] for item in existing):
        raise SystemExit("campaign record 已存在同 case_id，拒绝覆盖")
record = {
    "schema_version": 1,
    "capture_script": "aegisops-m97-controlled-capture-v2",
    "capture_script_sha256": hashlib.sha256(Path(os.environ["SCRIPT_PATH"]).read_bytes()).hexdigest(),
    "case_id": os.environ["CASE_ID"],
    "campaign_run_id": os.environ["RUN_ID"],
    "fault_type": "imagepullbackoff",
    "variant": os.environ["VARIANT"],
    "observed_at": datetime.now(UTC).isoformat().replace("+00:00", "Z"),
    "signals": ["ImagePullBackOff and ErrImagePull observed after reversible image patch"],
}
with record_path.open("a", encoding="utf-8") as handle:
    handle.write(json.dumps(record, ensure_ascii=False, sort_keys=True) + "\n")
PY
}

INVALID_IMAGE="nonexistent.invalid/aegisops/${CASE_ID}:nope"
"${kubectl_cmd[@]}" set image deployment/faultlab faultlab="$INVALID_IMAGE" >/dev/null
seen=false
for _ in {1..60}; do
  seen="$("${kubectl_cmd[@]}" get pods -l app.kubernetes.io/instance=faultlab -o json | python3 -c 'import json,sys; d=json.load(sys.stdin); print(any(s.get("state",{}).get("waiting",{}).get("reason") in {"ImagePullBackOff","ErrImagePull"} for p in d.get("items",[]) for s in p.get("status",{}).get("containerStatuses",[])))')"
  [[ "$seen" == True ]] && break
  sleep 2
done
[[ "$seen" == True ]] || die "未观察到 ImagePullBackOff"
record_campaign_observation

PAYLOAD="$(CASE_ID="$CASE_ID" RUN_ID="$RUN_ID" NAMESPACE="$NAMESPACE" python3 -c 'import datetime,hashlib,json,os; now=datetime.datetime.now(datetime.UTC).isoformat().replace("+00:00","Z"); case=os.environ["CASE_ID"]; run=os.environ["RUN_ID"]; fp="sha256:"+hashlib.sha256(("m97-"+run+"-"+case+"-"+now).encode()).hexdigest(); description="controlled FaultLab evidence capture run="+run+" case="+case; print(json.dumps({"version":"4","groupKey":"{}","status":"firing","alerts":[{"status":"firing","labels":{"alertname":"AegisOpsControlledEvaluation","namespace":os.environ["NAMESPACE"],"workload":"faultlab","severity":"critical","cluster":"kind-e2e"},"annotations":{"summary":"controlled M9.7 image pull capture","description":description},"startsAt":now,"fingerprint":fp}]}))')"
response="$(curl --silent --show-error --fail -H 'Content-Type: application/json' -H "Authorization: Bearer $WEBHOOK_TOKEN" --data "$PAYLOAD" "$GATEWAY_URL/webhooks/alertmanager")"
python3 -c 'import json,sys; d=json.loads(sys.argv[1]); assert d.get("accepted",0)>=1 and d.get("rejected",0)==0' "$response" || die "gateway 未接受告警"

incident=""
for _ in {1..60}; do
  incident="$("${kubectl_cmd[@]}" get aiopsincidents -o json | CASE_ID="$CASE_ID" RUN_ID="$RUN_ID" python3 -c 'import json,os,sys; d=json.load(sys.stdin); expected="controlled FaultLab evidence capture run="+os.environ["RUN_ID"]+" case="+os.environ["CASE_ID"]; print(next((x["metadata"]["name"] for x in d.get("items",[]) if x.get("spec",{}).get("commonAnnotations",{}).get("description")==expected),""))')"
  [[ -n "$incident" ]] && break
  sleep 2
done
[[ -n "$incident" ]] || die "未创建本次 case 对应的 Incident"

for _ in {1..60}; do
  evidence_id="$("${kubectl_cmd[@]}" get aiopsincident "$incident" -o json | python3 -c 'import json,sys; print(json.load(sys.stdin).get("status",{}).get("analysis",{}).get("evidenceID", ""))')"
  [[ -n "$evidence_id" ]] && break
  sleep 2
done
[[ -n "$evidence_id" ]] || die "Incident 未产生 diagnosis evidence"

AEGISOPS_EVAL_VIEWER_TOKEN="$VIEWER_TOKEN" \
AEGISOPS_EVAL_DIAGNOSIS_TOKEN="$DIAGNOSIS_TOKEN" \
"$ROOT/scripts/export-eval-case.sh" \
  --incident "$NAMESPACE/$incident" --case-id "$CASE_ID" --fault-type imagepullbackoff \
  --variant "$VARIANT" --run-id "$RUN_ID" --ground-truth "$ROOT/eval/ground-truth/imagepullbackoff.json" \
  --api-url "$INCIDENT_API_URL" --diagnosis-url "$DIAGNOSIS_URL" --context "$CONTEXT" --output "$OUTPUT" \
  --campaign-record "$CAMPAIGN_RECORD" --allow-rollout-evidence
printf 'captured controlled image-pull eval case: %s (incident %s)\n' "$CASE_ID" "$incident"
