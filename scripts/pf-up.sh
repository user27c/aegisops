#!/usr/bin/env bash
# pf-up.sh — 为集群内服务建立仅本机可访问的持久 port-forward。
# 用法:pf-up.sh [up|down] [--context CONTEXT]
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/scripts/lib/common.sh"
STATE=""

CONTEXT=""
ACTION="up"
while [[ $# -gt 0 ]]; do
  case "$1" in
    up|down) ACTION="$1"; shift ;;
    --context) CONTEXT="$2"; shift 2 ;;
    *) die "未知参数: $1" ;;
  esac
done
[[ -n "$CONTEXT" ]] || die "必须 --context CONTEXT"
require_kubectl_context "$CONTEXT"
safe_context="${CONTEXT//[^a-zA-Z0-9_.-]/_}"
STATE="$ROOT/.local/pf-$safe_context.pids"
mkdir -p "$ROOT/.local"

# 格式:namespace service local:remote。CORE 缺失即失败;OPTIONAL 缺失允许跳过。
CORE_FORWARDS=(
  "aegisops-system svc/aegisops-operator 8080:8080"
  "aegisops-system svc/aegisops-gateway 18080:8080"
  "aegisops-system svc/aegisops-incident-api 18081:8080"
  "aegisops-system svc/aegisops-diagnosis-api 8000:8000"
  "fault-lab svc/faultlab 18092:8080"
)
OPTIONAL_FORWARDS=(
  "observability svc/kube-prometheus-stack-prometheus 19090:9090"
  "observability svc/kube-prometheus-stack-grafana 13000:80"
  "observability svc/kube-prometheus-stack-alertmanager 19093:9093"
  "observability svc/tempo 13200:3200"
  "mailhog svc/mailhog 18025:8025"
)

is_owned_forward() {
  local pid="$1" command
  command="$(ps -p "$pid" -o args= 2>/dev/null || true)"
  [[ "$command" == *"bash -c"* && "$command" == *"kubectl"* && "$command" == *"port-forward"* && "$command" == *"--context \"\$context\""* && "$command" == *"--address 127.0.0.1"* ]]
}

down() {
  if [[ -f "$STATE" ]]; then
    while read -r pid; do
      [[ "$pid" =~ ^[0-9]+$ ]] || continue
      if ! is_owned_forward "$pid"; then
        log_warn "跳过非本脚本 port-forward PID: $pid"
        continue
      fi
      # setsid 使每条 forward 自成进程组；优雅停止，绝不 SIGKILL 或触碰其他进程。
      kill -- "-$pid" 2>/dev/null || kill "$pid" 2>/dev/null || true
    done < "$STATE"
    rm -f "$STATE"
  fi
  echo "port-forward 已全部停止"
}

collect_forwards() {
  local -a missing_core=()
  local f ns svc port
  for f in "${CORE_FORWARDS[@]}"; do
    IFS=" " read -r ns svc port <<< "$f"
    if kubectl --context "$CONTEXT" -n "$ns" get "$svc" >/dev/null 2>&1; then
      echo "$f"
    else
      missing_core+=("$ns/$svc")
    fi
  done
  if [[ "${#missing_core[@]}" -gt 0 ]]; then
    echo "CORE 服务缺失(不可跳过): ${missing_core[*]}" >&2
    return 1
  fi
  for f in "${OPTIONAL_FORWARDS[@]}"; do
    IFS=" " read -r ns svc port <<< "$f"
    if kubectl --context "$CONTEXT" -n "$ns" get "$svc" >/dev/null 2>&1; then
      echo "$f"
    else
      echo "SKIP 可选服务不存在: $ns/$svc" >&2
    fi
  done
}

up() {
  down
  : > "$STATE"
  local -a forwards=()
  local tmp="$ROOT/.tmp/pf-forwards.tmp"
  if ! collect_forwards > "$tmp"; then
    rm -f "$tmp"
    return 1
  fi
  mapfile -t forwards < "$tmp"
  rm -f "$tmp"
  local f ns svc port
  for f in "${forwards[@]}"; do
    IFS=" " read -r ns svc port <<< "$f"
    # 保持外层 bash 作为 PID leader：它可在 kubectl 断连后重试，也让
    # down() 能精确识别本脚本创建的进程组而非猜测一个裸 kubectl PID。
    setsid bash -c '
      context="$1" namespace="$2" service="$3" port="$4"
      while true; do
        kubectl --context "$context" -n "$namespace" port-forward --address 127.0.0.1 "$service" "$port" >/dev/null 2>&1 || true
        sleep 1
      done
    ' _ "$CONTEXT" "$ns" "$svc" "$port" >/dev/null 2>&1 < /dev/null &
    echo $! >> "$STATE"
  done
  sleep 5
  local ok=0
  for f in "${forwards[@]}"; do
    IFS=" " read -r ns svc port <<< "$f"
    port="${port%%:*}"
    if ss -tln 2>/dev/null | grep -q "127.0.0.1:$port "; then
      echo "OK  127.0.0.1:$port"
      ok=$((ok+1))
    else
      echo "FAIL 127.0.0.1:$port"
    fi
  done
  [[ "$ok" -eq "${#forwards[@]}" ]] || { echo "部分 port-forward 失败" >&2; return 1; }
}

case "$ACTION" in
  up) up ;;
  down) down ;;
esac
