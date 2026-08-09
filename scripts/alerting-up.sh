#!/usr/bin/env bash
# alerting-up.sh — 启动本地告警通知测试环境(Alertmanager + MailHog)。
# 默认强制 MailHog 配置;真实 SMTP 需要 --allow-real-email 且提供
# deploy/observability/alertmanager/alertmanager.local.yml。
set -Eeuo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

ALLOW_REAL=false
for arg in "$@"; do
  case "$arg" in
    --allow-real-email) ALLOW_REAL=true ;;
    *) echo "未知参数: $arg" >&2; exit 1 ;;
  esac
done

require_tools() {
  for t in docker curl; do
    command -v "$t" >/dev/null 2>&1 || { echo "缺少工具: $t" >&2; exit 1; }
  done
}

validate_alertmanager_config() {
  local cfg="${1:-deploy/observability/alertmanager/alertmanager.mailhog.yml}"
  # Alertmanager 启动即不退出;用 timeout 校验配置加载后结束。
  if timeout 6 docker run --rm -v "$ROOT/$cfg":/etc/alertmanager/alertmanager.yml:ro \
      prom/alertmanager:v0.27.0 --config.file=/etc/alertmanager/alertmanager.yml \
      --storage.path=/tmp/am-data >/tmp/am-validate.log 2>&1; then
    :
  fi
  if grep -qE "msg=.Loading configuration file.|msg=.Completed loading" /tmp/am-validate.log; then
    echo "Alertmanager 配置校验通过"
  else
    echo "FAIL: Alertmanager 配置加载失败" >&2
    cat /tmp/am-validate.log >&2
    exit 1
  fi
}

start_mail_sink() {
  # 邮件集成测试只需要 Alertmanager + MailHog；不要顺带启动 Tempo/OTel。
  docker compose -f deploy/observability/docker-compose.alerting.yml up -d mailhog alertmanager
}

wait_for_health() {
  local i
  for i in $(seq 1 30); do
    if curl -sf http://127.0.0.1:19093/-/healthy >/dev/null 2>&1 \
       && curl -sf http://127.0.0.1:18025/api/v2/messages >/dev/null 2>&1; then
      echo "Alertmanager 与 MailHog 就绪"
      return 0
    fi
    sleep 2
  done
  echo "等待超时" >&2
  return 1
}

require_tools

if [[ "$ALLOW_REAL" == "false" ]]; then
  # 强制 MailHog:默认配置必须是 mailhog:1025,否则拒绝(真实 SMTP 需显式确认)。
  if ! grep -qE "smtp_smarthost:[[:space:]]+mailhog:1025" deploy/observability/alertmanager/alertmanager.mailhog.yml 2>/dev/null; then
    echo "FAIL: 默认配置不是 MailHog smarthost;真实 SMTP 必须传 --allow-real-email" >&2
    exit 1
  fi
  validate_alertmanager_config deploy/observability/alertmanager/alertmanager.mailhog.yml
else
  [[ -f deploy/observability/alertmanager/alertmanager.local.yml ]] || {
    echo "FAIL: --allow-real-email 需要 alertmanager.local.yml" >&2
    exit 1
  }
  validate_alertmanager_config deploy/observability/alertmanager/alertmanager.local.yml
fi

start_mail_sink
wait_for_health
echo "访问地址: Alertmanager http://127.0.0.1:19093 | Mail UI http://127.0.0.1:18025"
