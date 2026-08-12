#!/usr/bin/env bash
# Build the local, independently auditable M9.7 controlled dataset.  Every
# capture runs in the isolated Kind E2E namespace and each child script cleans
# its injected FaultLab state before returning.
set -Eeuo pipefail
IFS=$'\n\t'

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUTPUT="$ROOT/eval/datasets/v1-verified-r5"
RUN_ID="m97v3-local-$(date -u +%Y%m%dT%H%M%SZ)"
CONFIRM=""

usage() {
  cat <<'EOF'
用法: capture-m97-verified-dataset.sh [options]

在隔离 kind-aegisops-e2e 环境中采集 36 个 M9.7 受控案例：每类至少五条、
覆盖 clean/noisy/sparse，并包含六条 prompt-injection + multi-fault 对抗样本。

选项:
  --output <dir>   数据集输出目录（默认 eval/datasets/v1-verified-r5）
  --run-id <id>    campaign 标识（默认 UTC 时间戳）
  --confirm <text> 必须精确为 "capture m97 local verified"
  -h, --help       显示本帮助
EOF
}

die() { printf '%s\n' "[ERROR] $*" >&2; exit 1; }
require_value() { [[ $# -ge 2 && -n "$2" ]] || die "$1 需要一个值"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --output) require_value "$@"; OUTPUT="$2"; shift 2 ;;
    --run-id) require_value "$@"; RUN_ID="$2"; shift 2 ;;
    --confirm) require_value "$@"; CONFIRM="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) die "未知参数: $1" ;;
  esac
done

[[ "$CONFIRM" == "capture m97 local verified" ]] || die "必须传 --confirm 'capture m97 local verified'"
[[ "$RUN_ID" =~ ^[a-z0-9][a-z0-9-]{2,119}$ ]] || die "--run-id 非法"
mkdir -p "$OUTPUT"

already_captured() {
  local case_id="$1" manifest="$OUTPUT/incidents.jsonl"
  [[ -f "$manifest" ]] || return 1
  CASE_ID="$case_id" MANIFEST="$manifest" python3 - <<'PY'
import json
import os
from pathlib import Path

for line in Path(os.environ["MANIFEST"]).read_text(encoding="utf-8").splitlines():
    if line.strip() and json.loads(line).get("case_id") == os.environ["CASE_ID"]:
        raise SystemExit(0)
raise SystemExit(1)
PY
}

capture() {
  local fault="$1" case_id="$2" variant="$3" truth="$4"
  if already_captured "$case_id"; then
    printf '[INFO] skip existing case: %s\n' "$case_id"
    return
  fi
  "$ROOT/scripts/capture-controlled-eval-case.sh" \
    --fault "$fault" --case-id "$case_id" --variant "$variant" \
    --ground-truth "$ROOT/eval/ground-truth/$truth.json" \
    --run-id "$RUN_ID" --output "$OUTPUT"
}

capture_adversarial_dependency() {
  local case_id="$1"
  if already_captured "$case_id"; then
    printf '[INFO] skip existing case: %s\n' "$case_id"
    return
  fi
  "$ROOT/scripts/capture-controlled-eval-case.sh" \
    --fault dependency --secondary-fault cpu --case-id "$case_id" --variant noisy \
    --ground-truth "$ROOT/eval/ground-truth/dependency.json" \
    --scenario-tags prompt-injection,multi-fault \
    --run-id "$RUN_ID" --output "$OUTPUT"
}

capture_imagepull() {
  local case_id="$1" variant="$2"
  if already_captured "$case_id"; then
    printf '[INFO] skip existing case: %s\n' "$case_id"
    return
  fi
  "$ROOT/scripts/capture-imagepull-eval-case.sh" \
    --case-id "$case_id" --variant "$variant" --run-id "$RUN_ID" --output "$OUTPUT"
}

capture config config-clean-001 clean config
capture config config-noisy-002 noisy config
capture config config-sparse-003 sparse config
capture config config-clean-004 clean config
capture config config-noisy-005 noisy config

capture oom oom-clean-001 clean oom
capture oom oom-noisy-002 noisy oom
capture oom oom-sparse-003 sparse oom
capture oom oom-clean-004 clean oom
capture oom oom-noisy-005 noisy oom

capture crashloop crashloop-clean-001 clean crashloop
capture crashloop crashloop-noisy-002 noisy crashloop
capture crashloop crashloop-sparse-003 sparse crashloop
capture crashloop crashloop-clean-004 clean crashloop
capture crashloop crashloop-noisy-005 noisy crashloop

capture cpu cpu-clean-001 clean cpu
capture cpu cpu-noisy-002 noisy cpu
capture cpu cpu-sparse-003 sparse cpu
capture cpu cpu-clean-004 clean cpu
capture cpu cpu-noisy-005 noisy cpu

capture dependency dependency-clean-001 clean dependency
capture dependency dependency-noisy-002 noisy dependency
capture dependency dependency-sparse-003 sparse dependency
capture dependency dependency-clean-004 clean dependency
capture dependency dependency-noisy-005 noisy dependency

for index in {1..6}; do
  capture_adversarial_dependency "adversarial-dependency-$(printf '%03d' "$index")"
done

capture_imagepull imagepull-clean-001 clean
capture_imagepull imagepull-noisy-002 noisy
capture_imagepull imagepull-sparse-003 sparse
capture_imagepull imagepull-clean-004 clean
capture_imagepull imagepull-noisy-005 noisy

PYTHONPATH="$ROOT" OUTPUT="$OUTPUT" uv run --project "$ROOT/services/diagnosis" python - <<'PY'
from pathlib import Path
import os

from eval.aegis_eval.dataset import load_cases
from eval.aegis_eval.experiment import dataset_readiness

cases = load_cases(Path(os.environ["OUTPUT"]))
failures = dataset_readiness(cases)
# Capture is intentionally performed before the explicit reviewer-signing
# step.  Keep only non-review readiness failures here; the following audit
# command is responsible for converting these placeholders to the authorized
# reviewer signature.
non_review = [failure for failure in failures if "审核未完成" not in failure]
if non_review:
    raise SystemExit("dataset structural gate failed: " + "；".join(non_review))
print(f"captured {len(cases)} structurally valid controlled cases; pending independent review")
PY
