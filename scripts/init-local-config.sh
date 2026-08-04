#!/usr/bin/env bash
# 初始化 .local/ 本地配置目录(模板 + 随机 token)。token 写入文件且 0600,不打印到终端。
set -Eeuo pipefail
IFS=$'\n\t'
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/scripts/lib/common.sh"

LOCAL="$ROOT/.local"
FORCE=false

usage() {
  cat <<EOF
用法: init-local-config.sh [--force]
  --force   覆盖已有文件(先备份 .bak-<时间戳>)
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --force) FORCE=true; shift ;;
    *) die "未知参数: $1" ;;
  esac
done

mkdir -p "$LOCAL"/{secrets,pids}

write_file() {
  local path="$1" content="$2"
  if [[ -e "$path" ]]; then
    if [[ "$FORCE" == "true" ]]; then
      cp "$path" "$path.bak-$(date +%s)"
    else
      log_info "已存在,跳过: ${path#$ROOT/}"
      return
    fi
  fi
  printf '%s' "$content" > "$path"
  log_info "已生成: ${path#$ROOT/}"
}

write_secret() {
  local path="$1" value="$2"
  if [[ -e "$path" ]]; then
    if [[ "$FORCE" == "true" ]]; then
      cp "$path" "$path.bak-$(date +%s)"
    else
      log_info "已存在,跳过: ${path#$ROOT/}"
      return
    fi
  fi
  printf '%s' "$value" > "$path"
  chmod 0600 "$path"
  log_info "已生成(0600): ${path#$ROOT/}"
}

# values.yaml:本地 Helm 覆盖(registry/llmProvider 等)。
write_file "$LOCAL/values.yaml" \
"# 本地 Helm values 覆盖(由 dev-up.sh 读取)。
global:
  imageRegistry: aegisops.local
diagnosis:
  llmProvider: fake
alerting:
  enabled: false
"

# values-email.local.yaml:从 example 复制,提示填写。
if [[ ! -e "$LOCAL/values-email.local.yaml" || "$FORCE" == "true" ]]; then
  if [[ -e "$ROOT/deploy/helm/aegisops/values-email.example.yaml" ]]; then
    write_file "$LOCAL/values-email.local.yaml" "$(cat "$ROOT/deploy/helm/aegisops/values-email.example.yaml")"
  fi
fi

# secrets:随机 token 只写文件,不打印。
write_secret "$LOCAL/secrets/webhook-token" "$(random_token 24)"
write_secret "$LOCAL/secrets/console-tokens" "console-token-$(random_token 8):viewer,approver
"
write_secret "$LOCAL/secrets/diagnosis-token" "$(random_token 24)"
# 占位空文件:邮箱/LLM 按需填写(留空即不启用对应功能)。
touch "$LOCAL/secrets/smtp-password" "$LOCAL/secrets/deepseek-api-key"
chmod 0600 "$LOCAL"/secrets/smtp-password "$LOCAL"/secrets/deepseek-api-key

# environment.json 模板(dev-up 成功后覆写真实值)。
if [[ ! -e "$LOCAL/environment.json" || "$FORCE" == "true" ]]; then
  write_file "$LOCAL/environment.json" '{}'
fi

log_info "本地配置目录就绪: $LOCAL"
log_info "提示: values-email.local.yaml 与 secrets/smtp-password、deepseek-api-key 按需填写;未填则对应功能保持关闭。"
