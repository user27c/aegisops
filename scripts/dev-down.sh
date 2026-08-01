#!/usr/bin/env bash
# 卸载本地开发环境。默认保留 PVC 与实验结果；--purge-data 为破坏性选项。
set -Eeuo pipefail
IFS=$'\n\t'
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/scripts/lib/common.sh"

CONTEXT=""
PURGE=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --context) CONTEXT="$2"; shift 2 ;;
    --purge-data) PURGE=true; shift ;;
    *) die "未知参数: $1" ;;
  esac
done

[[ -n "$CONTEXT" ]] || die "--context 必填"
kubectl config use-context "$CONTEXT" >/dev/null || die "无法切换到 context $CONTEXT"

if [[ "$PURGE" == "true" ]]; then
  log_warn "--purge-data 将删除 PVC 与全部实验数据！目标集群: $CONTEXT"
  confirm_if_interactive "确认继续?"
fi

log_info "dev-down 骨架：M1 起填充完整卸载流程。"
