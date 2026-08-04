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

# 校验 kubectl context 存在。
require_kubectl_context() {
  local ctx="$1"
  kubectl config get-contexts -o name | grep -qx "$ctx" \
    || die "context '$ctx' 不存在(用 kubectl config get-contexts 查看)"
}

# context 安全:仅 kind-*/k3d-* 视为本地开发集群;其他必须 --allow-nonlocal。
is_local_dev_context() {
  local ctx="$1"
  [[ "$ctx" == kind-* || "$ctx" == k3d-* ]]
}

# 随机 hex token(openssl 优先,fallback /dev/urandom)。
random_token() {
  local n="${1:-24}"
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex "$n"
  else
    head -c "$((n * 2))" /dev/urandom | od -An -tx1 | tr -d ' \n'
  fi
}

# namespace 是否存在。
namespace_exists() {
  local ctx="$1" ns="$2"
  kubectl --context "$ctx" get namespace "$ns" >/dev/null 2>&1
}

# 从 .local/secrets/<name> 读取 token;缺失时返回 1。
read_local_secret() {
  local name="$1"
  local f="$ROOT/.local/secrets/$name"
  if [[ -f "$f" ]]; then
    cat "$f"
    return 0
  fi
  return 1
}

# helm repo 幂等添加。
helm_ensure_repo() {
  local name="$1" url="$2"
  if ! helm repo list -o json 2>/dev/null | grep -q "\"name\":\"$name\""; then
    helm repo add "$name" "$url" >/dev/null
  fi
}

# 判断 Helm release 是否已安装。
helm_release_exists() {
  local ctx="$1" ns="$2" release="$3"
  helm --kube-context "$ctx" -n "$ns" status "$release" >/dev/null 2>&1
}
