#!/usr/bin/env bash
# AegisOps 脚本公共函数库。
set -Eeuo pipefail
IFS=$'\n\t'

# 颜色（仅 TTY 输出）
if [[ -t 1 ]]; then
  C_RED=$'\033[31m'; C_YELLOW=$'\033[33m'; C_GREEN=$'\033[32m'; C_RESET=$'\033[0m'
else
  C_RED=''; C_YELLOW=''; C_GREEN=''; C_RESET=''
fi

log_info()  { echo "${C_GREEN}[INFO]${C_RESET} $*"; }
log_warn()  { echo "${C_YELLOW}[WARN]${C_RESET} $*" >&2; }
log_error() { echo "${C_RED}[ERROR]${C_RESET} $*" >&2; }

die() {
  log_error "$*"
  exit 1
}

require_cmd() {
  local cmd="$1"
  command -v "$cmd" >/dev/null 2>&1 || die "缺少必需命令: $cmd"
}

require_file() {
  local file="$1"
  [[ -f "$file" ]] || die "缺少必需文件: $file"
}

# 交互确认；非交互环境直接通过。
confirm_if_interactive() {
  if [[ -t 0 ]]; then
    read -r -p "$* [y/N] " ans
    [[ "${ans,,}" == "y" ]] || die "已取消"
  fi
}

repo_root() {
  local dir
  dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
  echo "$dir"
}

current_git_sha() {
  git -C "$(repo_root)" rev-parse --short HEAD
}

# 等待 Deployment 就绪，带超时（秒）。
wait_for_deployment() {
  local ctx="$1" ns="$2" name="$3" timeout="${4:-180}"
  local deadline=$((SECONDS + timeout))
  while (( SECONDS < deadline )); do
    if kubectl --context "$ctx" -n "$ns" rollout status deployment/"$name" --timeout=10s >/dev/null 2>&1; then
      return 0
    fi
    sleep 5
  done
  return 1
}
