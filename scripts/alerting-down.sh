#!/usr/bin/env bash
# alerting-down.sh — 仅停止 AegisOps alerting compose 资源。
set -Eeuo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
docker compose -f deploy/observability/docker-compose.alerting.yml down --remove-orphans
