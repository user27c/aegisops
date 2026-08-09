#!/usr/bin/env bash
# collect-e2e-artifacts.sh — 保存 E2E 诊断 artifacts 到 artifacts/e2e/<runid>/。
# 原始目录只在本地/CI runner 隔离；上传前由 sanitize-e2e-artifacts.py 创建已验证的脱敏副本。
set -Eeuo pipefail
IFS=$'\n\t'
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/scripts/lib/common.sh"

CONTEXT=""
RUN_ID="$(date +%Y%m%d%H%M%S)"

usage() {
  cat <<EOF
用法: collect-e2e-artifacts.sh [--context CONTEXT] [--run-id ID]
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --context) CONTEXT="$2"; shift 2 ;;
    --run-id) RUN_ID="$2"; shift 2 ;;
    *) die "未知参数: $1" ;;
  esac
done
[[ -n "$CONTEXT" ]] || die "必须 --context CONTEXT"
require_kubectl_context "$CONTEXT"

ART="$ROOT/artifacts/e2e/$RUN_ID"
mkdir -p "$ART"

collect() {
  kubectl --context "$CONTEXT" get all -A -o yaml > "$ART/k8s-all.yaml" 2>/dev/null || true
  kubectl --context "$CONTEXT" get events -A -o yaml > "$ART/k8s-events.yaml" 2>/dev/null || true
  kubectl --context "$CONTEXT" get aiopsincidents,remediationpolicies,remediationapprovals -A -o yaml \
    > "$ART/aegisops-crds.yaml" 2>/dev/null || true
  kubectl --context "$CONTEXT" get pods -A -o wide > "$ART/pods.txt" 2>/dev/null || true

  local pods
  pods="$(kubectl --context "$CONTEXT" get pods -A -o jsonpath='{range .items[*]}{.metadata.namespace}{" "}{.metadata.name}{"\n"}{end}' 2>/dev/null || true)"
  while read -r ns pod; do
    [[ -n "$ns" ]] || continue
    kubectl --context "$CONTEXT" -n "$ns" logs "$pod" --tail=500 > "$ART/log-$ns-$pod.log" 2>/dev/null || true
    kubectl --context "$CONTEXT" -n "$ns" describe pod "$pod" > "$ART/describe-$ns-$pod.txt" 2>/dev/null || true
  done <<< "$pods"

  if curl -sf --max-time 5 "http://127.0.0.1:19090/api/v1/targets" > "$ART/prometheus-targets.json" 2>/dev/null; then
    curl -sf --max-time 5 "http://127.0.0.1:19090/api/v1/alerts" > "$ART/prometheus-alerts.json" 2>/dev/null || true
  fi
  curl -sf --max-time 5 "http://127.0.0.1:18025/api/v2/messages" > "$ART/mailhog-messages.json" 2>/dev/null || true
  curl -sf --max-time 5 "http://127.0.0.1:19093/api/v2/alerts" > "$ART/alertmanager-alerts.json" 2>/dev/null || true

  {
    echo "git_sha: $(git -C "$ROOT" rev-parse HEAD 2>/dev/null || echo unknown)"
    echo "k8s: $(kubectl --context "$CONTEXT" version --short 2>/dev/null | tr '\n' ' ')"
    echo "helm: $(helm version --short 2>/dev/null)"
  } > "$ART/versions.txt"
}

scan() {
  if ! python3 "$ROOT/scripts/sanitize-e2e-artifacts.py" \
    --source "$ART" --scan-only --report "$ART/SCAN-HITS.txt"; then
    log_warn "Secret/PII 扫描命中；原始 artifacts 已隔离，上传时只会使用脱敏副本: $ART"
  else
    log_info "Secret/PII 扫描通过"
  fi
}

collect
scan
log_info "artifacts: $ART"
