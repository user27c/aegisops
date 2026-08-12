#!/usr/bin/env bash
# 云端只读 smoke。默认不调用 DeepSeek 或发送真实邮件，避免未经批准产生外部影响。
set -Eeuo pipefail
IFS=$'\n\t'

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck disable=SC1091 # ROOT is computed at runtime; test invokes ShellCheck with the repo source tree.
source "$ROOT/scripts/lib/common.sh"

CONTEXT=""
PROM_URL=""
CONFIRM=""

usage() {
  cat <<'EOF'
用法: cloud-smoke.sh --context <k3s-context> --prom-url <https://prometheus> \
  --confirm 'smoke aliyun-demo'

只检查 Node、workload、NetworkPolicy、DeepSeek Secret/配置和 Prometheus targets；
不调用模型、不发送邮件、不执行修复动作。这三项需在获得单独成本/通知授权后手工验收。
EOF
}

require_value() { [[ $# -ge 2 && -n "$2" ]] || die "$1 需要一个值"; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --context|--prom-url|--confirm) require_value "$@" ;;
  esac
  case "$1" in
    --context) CONTEXT="$2"; shift 2 ;;
    --prom-url) PROM_URL="$2"; shift 2 ;;
    --confirm) CONFIRM="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) die "未知参数: $1" ;;
  esac
done

[[ "$CONFIRM" == "smoke aliyun-demo" ]] || die "拒绝运行：必须传 --confirm 'smoke aliyun-demo'"
[[ -n "$CONTEXT" && -n "$PROM_URL" ]] || die "--context 与 --prom-url 必填"
[[ "$CONTEXT" != kind-* && "$CONTEXT" != k3d-* ]] || die "cloud-smoke 仅允许非本地 k3s context"
[[ "$PROM_URL" == https://* || "$PROM_URL" == http://127.0.0.1:* ]] || die "--prom-url 必须为 TLS URL 或受控本地 tunnel"
require_cmd kubectl
require_kubectl_context "$CONTEXT"

kubectl --context "$CONTEXT" get nodes --no-headers | awk '$2 != "Ready" {bad=1} END {exit bad}' \
  || die "存在非 Ready Node"
for deployment in aegisops-operator aegisops-gateway aegisops-incident-api aegisops-diagnosis-api aegisops-diagnosis-worker; do
  kubectl --context "$CONTEXT" -n aegisops-system rollout status "deployment/$deployment" --timeout=60s
done
kubectl --context "$CONTEXT" -n fault-lab rollout status deployment/faultlab --timeout=60s
kubectl --context "$CONTEXT" -n aegisops-system get networkpolicy >/dev/null \
  || die "NetworkPolicy 未启用或不可读取"
kubectl --context "$CONTEXT" -n aegisops-system get secret deepseek-api >/dev/null \
  || die "缺少 deepseek-api Secret"
provider="$(kubectl --context "$CONTEXT" -n aegisops-system get deployment aegisops-diagnosis-api -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="LLM_PROVIDER")].value}')"
[[ "$provider" == "fake" ]] || die "diagnosis API 未配置 LLM_PROVIDER=fake（gate-down 只读 smoke 不得触发真实模型调用）"

"$ROOT/scripts/check-prometheus-targets.sh" --url "$PROM_URL" \
  --expected-job aegisops-operator --expected-job aegisops-gateway \
  --expected-job aegisops-incident-api --expected-job aegisops-diagnosis-api \
  --expected-job faultlab --timeout 120

log_info "云端只读 smoke 通过。DeepSeek 实调用、邮件和 Auto Restart 闭环仍需要单独授权与证据归档。"
