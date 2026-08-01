#!/usr/bin/env bash
# 检查并报告缺失工具；默认不修改系统。--install-local-bin 时下载到 .bin/ 并校验 SHA256。
set -Eeuo pipefail
IFS=$'\n\t'
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/scripts/lib/common.sh"

INSTALL_LOCAL=false
[[ "${1:-}" == "--install-local-bin" ]] && INSTALL_LOCAL=true

TOOL_VERSIONS="$ROOT/.tool-versions"
require_file "$TOOL_VERSIONS"

missing=0
while read -r tool version; do
  [[ -n "$tool" ]] || continue
  if command -v "$tool" >/dev/null 2>&1; then
    log_info "$tool: 已安装"
  else
    log_warn "$tool: 缺失（.tool-versions 要求 $version）"
    missing=1
  fi
done < "$TOOL_VERSIONS"

if [[ "$missing" == "1" ]]; then
  log_warn "存在缺失工具。请按 .tool-versions 中的版本安装，或使用 --install-local-bin 下载到 .bin/。"
  exit 1
fi
log_info "工具链完整。"
