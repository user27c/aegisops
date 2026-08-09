#!/usr/bin/env bash
# alerting-down.sh — 仅停止邮件集成测试启动的 Alertmanager 与 MailHog。
# 不执行 compose down，避免删除同一项目中独立使用的 Tempo/OTel 服务。
set -Eeuo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
docker compose -f deploy/observability/docker-compose.alerting.yml rm --stop --force alertmanager mailhog
