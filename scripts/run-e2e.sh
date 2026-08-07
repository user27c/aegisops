#!/usr/bin/env bash
# run-e2e.sh — 在已就绪的 E2E 环境上运行 Go E2E 测试包。
# 环境由 e2e-up.sh 准备(.local/e2e/environment.json);失败自动收集 artifacts。
set -Eeuo pipefail
IFS=$'\n\t'
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/scripts/lib/common.sh"

ENV_FILE="$ROOT/.local/e2e/environment.json"
TIMEOUT="${E2E_TIMEOUT:-30m}"
RUN_ID=""

require_file "$ENV_FILE" "E2E 环境未就绪(先运行 scripts/e2e-up.sh)"

RUN_ID="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["namespace"].split("-")[-1])' "$ENV_FILE")"

export AEGISOPS_E2E=1
export AEGISOPS_E2E_CONTEXT="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["context"])' "$ENV_FILE")"

log_info "E2E 运行: context=$AEGISOPS_E2E_CONTEXT run=$RUN_ID timeout=$TIMEOUT"
if ! go test ./tests/e2e/... -count=1 -timeout="$TIMEOUT" -v "$@"; then
  log_error "E2E 失败,收集 artifacts ..."
  "$ROOT/scripts/collect-e2e-artifacts.sh" --run-id "$RUN_ID" --context "$AEGISOPS_E2E_CONTEXT" || true
  exit 1
fi
log_info "E2E 全部通过"
