#!/usr/bin/env bash
# pf-up.sh — 为集群内服务建立持久 port-forward(0.0.0.0,供 Prometheus 容器抓取)。
# pf-down.sh 停止。用法:pf-up.sh [up|down]
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STATE="$ROOT/.tmp/pf.pids"
mkdir -p "$ROOT/.tmp"

# 格式:namespace service local:remote
FORWARDS=(
  "aegisops-system svc/aegisops-operator 8080:8080"
  "aegisops-system svc/aegisops-gateway 18080:8080"
  "aegisops-system svc/aegisops-incident-api 18081:8080"
  "aegisops-system svc/aegisops-diagnosis-api 8000:8000"
  "fault-lab svc/faultlab 18092:8080"
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
  for f in "${FORWARDS[@]}"; do
    set -- $f
    ns="$1"; svc="$2"; port="$3"
    setsid kubectl -n "$ns" port-forward --address 0.0.0.0 "$svc" "$port" >/dev/null 2>&1 < /dev/null &
    echo $! >> "$STATE"
  done
  sleep 5
  # 验证
  local ok=0
  for f in "${FORWARDS[@]}"; do
    set -- $f
    port="${3%%:*}"
    if ss -tln 2>/dev/null | grep -q "0.0.0.0:$port "; then
      echo "OK  0.0.0.0:$port"
      ok=$((ok+1))
    else
      echo "FAIL 0.0.0.0:$port"
    fi
  done
  [[ "$ok" -eq "${#FORWARDS[@]}" ]] || { echo "部分 port-forward 失败" >&2; return 1; }
}

case "${1:-up}" in
  up) up ;;
  down) down ;;
  *) echo "用法: pf-up.sh [up|down]"; exit 1 ;;
esac
