#!/usr/bin/env bash
# smoke.sh — 开发环境冒烟检查:Pod 就绪 + Prometheus targets + 服务健康。
# 用法:smoke.sh --context CONTEXT [--prom-url URL]
set -Eeuo pipefail
IFS=$'\n\t'
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/scripts/lib/common.sh"

CONTEXT=""
PROM_URL="http://127.0.0.1:19090"
FAILS=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --context) CONTEXT="$2"; shift 2 ;;
    --prom-url) PROM_URL="$2"; shift 2 ;;
    *) die "未知参数: $1" ;;
  esac
done

require_kubectl_context "$CONTEXT"

check_pods_ready() {
  local pending
  pending="$(kubectl --context "$CONTEXT" -n aegisops-system get pods -o jsonpath='{range .items[?(@.status.phase!="Running")]}{.metadata.name}{" "}{.status.phase}{"\n"}{end}' 2>/dev/null | grep -v " Succeeded$" || true)"
  if [[ -n "$pending" ]]; then
    log_error "非 Running Pod:"
    echo "$pending" >&2
    FAILS=$((FAILS + 1))
  else
    log_info "aegisops-system 全部 Pod Running"
  fi
  kubectl --context "$CONTEXT" -n aegisops-system rollout status deployment --timeout=60s >/dev/null 2>&1 \
    || { log_error "aegisops-system rollout 未完成"; FAILS=$((FAILS + 1)); }
  if kubectl --context "$CONTEXT" -n fault-lab get deployment faultlab >/dev/null 2>&1; then
    kubectl --context "$CONTEXT" -n fault-lab rollout status deployment/faultlab --timeout=30s >/dev/null 2>&1 \
      || { log_error "faultlab rollout 未完成"; FAILS=$((FAILS + 1)); }
  fi
}

check_prometheus() {
  if curl -sf --max-time 5 "$PROM_URL/-/healthy" >/dev/null 2>&1; then
    if "$ROOT/scripts/check-prometheus-targets.sh" --url "$PROM_URL" \
        --expected-job aegisops-operator --expected-job aegisops-gateway \
        --expected-job aegisops-incident-api --expected-job aegisops-diagnosis \
        --expected-job faultlab \
        --timeout 60 >/dev/null 2>&1; then
      log_info "Prometheus 5 个 AegisOps targets up(operator/gateway/api/diagnosis/faultlab)"
    else
      log_error "Prometheus targets 未全部 up"
      FAILS=$((FAILS + 1))
    fi
  else
    log_error "Prometheus 不可达($PROM_URL)"
    FAILS=$((FAILS + 1))
  fi
}

check_apis() {
  if curl -sf --max-time 5 http://127.0.0.1:18081/healthz >/dev/null 2>&1; then
    log_info "incident-api /healthz OK"
  else
    log_error "incident-api 不可达(http://127.0.0.1:18081/healthz)"
    FAILS=$((FAILS + 1))
  fi
  if curl -sf --max-time 5 http://127.0.0.1:18092/readyz >/dev/null 2>&1; then
    log_info "faultlab /readyz OK"
  else
    log_error "faultlab 不可达(http://127.0.0.1:18092/readyz)"
    FAILS=$((FAILS + 1))
  fi
}

check_pods_ready
check_prometheus
check_apis

if [[ "$FAILS" -gt 0 ]]; then
  log_error "smoke 未通过($FAILS 项失败)"
  exit 1
fi
log_info "smoke 全部通过"
