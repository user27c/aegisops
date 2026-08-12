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
PROFILE=""

require_file "$ENV_FILE" "E2E 环境未就绪(先运行 scripts/e2e-up.sh)"

mapfile -t environment < <(python3 -c '
import json, sys
e = json.load(open(sys.argv[1], encoding="utf-8"))
for key in ("runID", "profile", "context", "namespace", "systemNamespace"):
    print(e.get(key, ""))
' "$ENV_FILE")
RUN_ID="${environment[0]}"
PROFILE="${environment[1]}"
E2E_CONTEXT="${environment[2]}"
E2E_NAMESPACE="${environment[3]}"
E2E_SYSTEM_NAMESPACE="${environment[4]}"

[[ -n "$RUN_ID" ]] || die "environment.json 缺少 runID"
[[ "$E2E_CONTEXT" == "kind-aegisops-e2e" ]] || die "拒绝非 E2E context: $E2E_CONTEXT"
[[ "$E2E_NAMESPACE" =~ ^aegisops-e2e-[a-zA-Z0-9-]+$ ]] || die "拒绝非 E2E namespace: $E2E_NAMESPACE"
[[ "$E2E_SYSTEM_NAMESPACE" == "$E2E_NAMESPACE" ]] || die "E2E systemNamespace 必须等于 run namespace"

E2E_KUBECONFIG="${AEGISOPS_E2E_KUBECONFIG:-$HOME/.kube/config}"
require_file "$E2E_KUBECONFIG" "缺少 E2E kubeconfig: $E2E_KUBECONFIG"
export KUBECONFIG="$E2E_KUBECONFIG"

case "$PROFILE" in
  full|core) ;;
  *) die "environment.json 含未知 E2E profile: $PROFILE" ;;
esac

PORT_FORWARD_STATE="$ROOT/.local/e2e/pf.pids"

is_e2e_forward_pid() {
  local pid="$1" command
  command="$(ps -p "$pid" -o args= 2>/dev/null || true)"
  [[ "$command" == *"kubectl"* && "$command" == *"port-forward"* ]]
}

stop_port_forwards() {
  [[ -f "$PORT_FORWARD_STATE" ]] || return 0
  while read -r pid; do
    [[ "$pid" =~ ^[0-9]+$ ]] || continue
    if ! is_e2e_forward_pid "$pid"; then
      log_warn "跳过非 E2E port-forward PID: $pid"
      continue
    fi
    kill -- "-$pid" 2>/dev/null || kill "$pid" 2>/dev/null || true
  done < "$PORT_FORWARD_STATE"
  rm -f "$PORT_FORWARD_STATE"
}

start_port_forwards() {
  require_cmd kubectl
  require_cmd setsid
  stop_port_forwards
  : > "$PORT_FORWARD_STATE"
  local -a forwards=(
    "$E2E_NAMESPACE svc/aegisops-gateway 18080:8080"
    "$E2E_NAMESPACE svc/aegisops-incident-api 18081:8080"
    "$E2E_NAMESPACE svc/aegisops-diagnosis-api 8000:8000"
    "$E2E_NAMESPACE svc/faultlab 18092:8080"
  )
  local -a ports=(18080 18081 8000 18092)
  if [[ "$PROFILE" == "full" ]]; then
    forwards+=(
      "observability svc/kube-prometheus-stack-prometheus 19090:9090"
      "observability svc/kube-prometheus-stack-alertmanager 19093:9093"
      "observability svc/loki 13100:3100"
      "observability svc/tempo 13200:3200"
      "mailhog svc/mailhog 18025:8025"
    )
    ports+=(19090 19093 13100 13200 18025)
  fi

  local entry namespace service port
  for entry in "${forwards[@]}"; do
    IFS=" " read -r namespace service port <<< "$entry"
    # The runner remains alive for the whole Go test process. These loops are
    # therefore not reaped after setup and can reconnect after Pod rollouts.
    setsid bash -c '
      context="$1"; namespace="$2"; service="$3"; port="$4"
      while true; do
        kubectl --context "$context" -n "$namespace" port-forward --address 127.0.0.1 "$service" "$port" >/dev/null 2>&1 || true
        sleep 1
      done
    ' _ "$E2E_CONTEXT" "$namespace" "$service" "$port" >/dev/null 2>&1 < /dev/null &
    echo $! >> "$PORT_FORWARD_STATE"
  done

  for port in "${ports[@]}"; do
    local ready=false
    for _ in {1..40}; do
      if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
        exec 3>&-
        exec 3<&-
        ready=true
        break
      fi
      sleep 0.25
    done
    [[ "$ready" == "true" ]] || die "E2E port-forward 未监听 127.0.0.1:$port"
  done
}

trap stop_port_forwards EXIT

export AEGISOPS_E2E=1
export AEGISOPS_E2E_CONTEXT="$E2E_CONTEXT"
export AEGISOPS_E2E_KUBECONFIG="$E2E_KUBECONFIG"
start_port_forwards

test_args=()
if [[ "$PROFILE" == "core" ]]; then
  # core 环境刻意未部署 Prometheus/MailHog，只执行其唯一支持的核心闭环。
  test_args=(-run '^TestE2EAutoRestart$')
fi

log_info "E2E 运行: context=$AEGISOPS_E2E_CONTEXT run=$RUN_ID profile=$PROFILE timeout=$TIMEOUT"
if ! go test ./tests/e2e/... -count=1 -timeout="$TIMEOUT" -v "$@" "${test_args[@]}"; then
  log_error "E2E 失败,收集 artifacts ..."
  "$ROOT/scripts/collect-e2e-artifacts.sh" --run-id "$RUN_ID" --context "$AEGISOPS_E2E_CONTEXT" || true
  exit 1
fi
log_info "E2E 全部通过"
