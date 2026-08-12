#!/usr/bin/env bash
# 部署到受控的阿里云单节点 k3s 演示环境。不会创建 Secret，也不会输出凭据。
set -Eeuo pipefail
IFS=$'\n\t'

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck disable=SC1091 # ROOT is computed at runtime; test invokes ShellCheck with the repo source tree.
source "$ROOT/scripts/lib/common.sh"

CONTEXT=""
REGISTRY=""
TAG=""
DOMAIN=""
CONFIRM=""
VALUES=("$ROOT/deploy/helm/aegisops/values-aliyun-demo.yaml")

usage() {
  cat <<'EOF'
用法: cloud-deploy.sh --context <k3s-context> --registry <registry> --tag <immutable-tag> \
  --confirm 'deploy aliyun-demo' [--values <local-values>] [--domain <https-domain>]

部署前必须由操作者在集群中创建 gateway、console、diagnosis 与 deepseek-api Secret。
本脚本不创建或打印任何 token、API key、SMTP 密码或域名凭据。
EOF
}

require_value() { [[ $# -ge 2 && -n "$2" ]] || die "$1 需要一个值"; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --context|--registry|--tag|--confirm|--domain|--values) require_value "$@" ;;
  esac
  case "$1" in
    --context) CONTEXT="$2"; shift 2 ;;
    --registry) REGISTRY="$2"; shift 2 ;;
    --tag) TAG="$2"; shift 2 ;;
    --confirm) CONFIRM="$2"; shift 2 ;;
    --domain) DOMAIN="$2"; shift 2 ;;
    --values) VALUES+=("$2"); shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) die "未知参数: $1" ;;
  esac
done

[[ "$CONFIRM" == "deploy aliyun-demo" ]] || die "拒绝部署：必须传 --confirm 'deploy aliyun-demo'"
[[ -n "$CONTEXT" && -n "$REGISTRY" && -n "$TAG" ]] || die "--context、--registry、--tag 必填"
[[ "$TAG" != "latest" ]] || die "禁止使用 latest"
[[ "$CONTEXT" != kind-* && "$CONTEXT" != k3d-* ]] || die "cloud-deploy 仅允许非本地 k3s context"
[[ -z "$DOMAIN" || "$DOMAIN" == https://* || "$DOMAIN" == http://* ]] || die "--domain 必须是 http(s) URL"

require_cmd kubectl
require_cmd helm
require_kubectl_context "$CONTEXT"
for value in "${VALUES[@]}"; do require_file "$value"; done

# Prevent a typo from placing a cloud release in an arbitrary context before any write.
kubectl --context "$CONTEXT" get nodes >/dev/null
kubectl --context "$CONTEXT" create namespace aegisops-system >/dev/null 2>&1 || true
kubectl --context "$CONTEXT" create namespace fault-lab >/dev/null 2>&1 || true
for secret in aegisops-gateway-token aegisops-console-auth aegisops-diagnosis-token deepseek-api; do
  kubectl --context "$CONTEXT" -n aegisops-system get secret "$secret" >/dev/null 2>&1 \
    || die "缺少 aegisops-system/$secret；请通过受控本地流程创建，脚本拒绝代建 Secret"
done

kubectl --context "$CONTEXT" label namespace fault-lab aegisops.io/managed=true --overwrite >/dev/null
kubectl --context "$CONTEXT" apply -f "$ROOT/config/crd/bases/"

helm_args=(upgrade --install aegisops "$ROOT/deploy/helm/aegisops" -n aegisops-system
  --set global.imageRegistry="$REGISTRY" --set global.imageTag="$TAG")
for value in "${VALUES[@]}"; do helm_args+=(-f "$value"); done
if [[ -n "$DOMAIN" ]]; then
  log_info "Ingress 域名由 local values 负责配置：$DOMAIN"
fi
helm --kube-context "$CONTEXT" "${helm_args[@]}" --wait --timeout 10m

faultlab="$ROOT/deploy/kind/faultlab.yaml"
rendered="$(mktemp "${TMPDIR:-/tmp}/aegisops-cloud-faultlab.XXXXXX.yaml")"
trap 'rm -f "$rendered"' EXIT
sed "s|aegisops.local/fault-lab:dev|$REGISTRY/fault-lab:$TAG|g" "$faultlab" > "$rendered"
kubectl --context "$CONTEXT" apply -f "$rendered"
kubectl --context "$CONTEXT" apply -f "$ROOT/config/samples/ops_v1alpha1_remediationpolicy.yaml"

for deployment in aegisops-operator aegisops-gateway aegisops-incident-api aegisops-diagnosis-api aegisops-diagnosis-worker; do
  kubectl --context "$CONTEXT" -n aegisops-system rollout status "deployment/$deployment" --timeout=300s
done
kubectl --context "$CONTEXT" -n fault-lab rollout status deployment/faultlab --timeout=180s

log_info "云端 AegisOps 部署完成。下一步运行 scripts/cloud-smoke.sh --context $CONTEXT --confirm 'smoke aliyun-demo'。"
