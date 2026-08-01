#!/usr/bin/env bash
# 启动本地开发环境。必须显式指定 --context，防止部署到错误集群。
set -Eeuo pipefail
IFS=$'\n\t'
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/scripts/lib/common.sh"

CONTEXT=""
OBSERVABILITY=false

usage() {
  cat <<EOF
用法: dev-up.sh --context CONTEXT [--observability]
  --context         kubectl context（必填，防止误部署）
  --observability  同时安装 Prometheus/Loki/Tempo/Grafana（可选）
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --context) CONTEXT="$2"; shift 2 ;;
    --observability) OBSERVABILITY=true; shift ;;
    *) die "未知参数: $1" ;;
  esac
done

[[ -n "$CONTEXT" ]] || { usage; exit 1; }
kubectl config use-context "$CONTEXT" >/dev/null || die "无法切换到 context $CONTEXT"

log_info "目标集群: $CONTEXT"
confirm_if_interactive "确认在集群 $CONTEXT 上执行 dev-up?"

# 后续里程碑填充：创建 namespaces → Secret → 构建/加载镜像 → Helm install → fault-lab → reindex。
log_info "dev-up 骨架：M1 起逐步填充完整安装流程。"
