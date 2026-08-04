#!/usr/bin/env bash
# pf-up.sh — 为集群内服务建立持久 port-forward(0.0.0.0,供 Prometheus 容器抓取)。
# 用法:pf-up.sh [up|down] [--context CONTEXT]
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STATE="$ROOT/.tmp/pf.pids"
mkdir -p "$ROOT/.tmp"

CONTEXT=""
ACTION="up"
while [[ $# -gt 0 ]]; do
  case "$1" in
    up|down) ACTION="$1"; shift ;;
    --context) CONTEXT="$2"; shift 2 ;;
    *) echo "未知参数: $1" >&2; exit 1 ;;
  esac
done
[[ -n "$CONTEXT" ]] || { echo "必须 --context CONTEXT" >&2; exit 1; }

# 格式:namespace service local:remote;service 不存在则跳过。
CORE_FORWARDS=(
  "aegisops-system svc/aegisops-operator 8080:8080"
  "aegisops-system svc/aegisops-gateway 18080:8080"
  "aegisops-system svc/aegisops-incident-api 18081:8080"
  "aegisops-system svc/aegisops-diagnosis-api 8000:8000"
  "fault-lab svc/faultlab 18092:8080"
)
OPTIONAL_FORWARDS=(
  "observability svc/kube-prometheus-stack-prometheus 19090:9090"
  "mailhog svc/mailhog 18025:8025"
)

down() {
  if [[ -f "$STATE" ]]; then
    while read -r pid; do
      kill -9 "$pid" 2>/dev/null || true
    done < "$STATE"
    rm -f "$STATE"
  fi
  echo "port-forward 已全部停止"
}

up() {
  down
  : > "$STATE"
  local -a forwards=()
  for f in "${CORE_FORWARDS[@]}" "${OPTIONAL_FORWARDS[@]}"; do
    set -- $f
    local ns="$1" svc="$2"
    if kubectl --context "$CONTEXT" -n "$ns" get "$svc" >/dev/null 2>&1; then
      forwards+=("$f")
    else
      echo "SKIP $ns/$svc(服务不存在)"
    fi
  done
  for f in "${forwards[@]}"; do
    set -- $f
    local ns="$1" svc="$2" port="$3"
    setsid kubectl --context "$CONTEXT" -n "$ns" port-forward --address 0.0.0.0 "$svc" "$port" >/dev/null 2>&1 < /dev/null &
    echo $! >> "$STATE"
  done
  sleep 5
  local ok=0
  for f in "${forwards[@]}"; do
    set -- $f
    local port="${3%%:*}"
    if ss -tln 2>/dev/null | grep -q "0.0.0.0:$port "; then
      echo "OK  0.0.0.0:$port"
      ok=$((ok+1))
    else
      echo "FAIL 0.0.0.0:$port"
    fi
  done
  [[ "$ok" -eq "${#forwards[@]}" ]] || { echo "部分 port-forward 失败" >&2; return 1; }
}

case "$ACTION" in
  up) up ;;
  down) down ;;
esac
