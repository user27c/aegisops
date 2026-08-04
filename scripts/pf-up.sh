#!/usr/bin/env bash
# pf-up.sh — 为集群内服务建立持久 port-forward(0.0.0.0,供 Prometheus 容器抓取)。
# 用法:pf-up.sh [up|down] [--context CONTEXT]
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/scripts/lib/common.sh"
STATE="$ROOT/.tmp/pf.pids"
mkdir -p "$ROOT/.tmp"

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
      echo "SKIP $ns/$svc(可选服务不存在)"
    fi
  done
}

up() {
  down
  : > "$STATE"
  local -a forwards=()
  mapfile -t forwards < <(collect_forwards)
  local f ns svc port
  for f in "${forwards[@]}"; do
    [[ "$f" == SKIP* ]] && continue
    IFS=" " read -r ns svc port <<< "$f"
    setsid kubectl --context "$CONTEXT" -n "$ns" port-forward --address 0.0.0.0 "$svc" "$port" >/dev/null 2>&1 < /dev/null &
    echo $! >> "$STATE"
  done
  sleep 5
  local ok=0
  for f in "${forwards[@]}"; do
    [[ "$f" == SKIP* ]] && continue
    IFS=" " read -r ns svc port <<< "$f"
    port="${port%%:*}"
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
