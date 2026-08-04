#!/usr/bin/env bash
# dev-up.sh — 一键启动 AegisOps 本地开发环境。
# 安全:--context 必填且二次显示;默认仅允许 kind-*/k3d-* 本地集群,其他需 --allow-nonlocal。
# 幂等:重复运行不重复创建,改为升级现有 release/secret(不可变字段除外)。
set -Eeuo pipefail
IFS=$'\n\t'
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/scripts/lib/common.sh"

CONTEXT=""
PROFILE="minimal"
REGISTRY="aegisops.local"
TAG=""
VALUES_FILES=()
OBSERVABILITY=false
MAILHOG=false
SKIP_BUILD=false
ALLOW_NONLOCAL=false
YES=false

usage() {
  cat <<EOF
用法: dev-up.sh --context CONTEXT [options]
  --context <ctx>         kubectl context(必填)
  --profile minimal|full  full 隐含 observability+mailhog(默认 minimal)
  --registry <reg>        镜像 registry(默认 aegisops.local)
  --tag <tag>             镜像 tag(必填,禁止 latest)
  --values <file>         附加 Helm values 文件(可多次)
  --observability         安装 Prometheus/Loki/Tempo/Grafana
  --mailhog               安装 MailHog
  --skip-build            跳过镜像构建(用于镜像已 load 的场景)
  --allow-nonlocal        允许非 kind-*/k3d-* context(危险)
  --yes                   跳过全部交互确认
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --context) CONTEXT="$2"; shift 2 ;;
    --profile) PROFILE="$2"; shift 2 ;;
    --registry) REGISTRY="$2"; shift 2 ;;
    --tag) TAG="$2"; shift 2 ;;
    --values) VALUES_FILES+=("$2"); shift 2 ;;
    --observability) OBSERVABILITY=true; shift ;;
    --mailhog) MAILHOG=true; shift ;;
    --skip-build) SKIP_BUILD=true; shift ;;
    --allow-nonlocal) ALLOW_NONLOCAL=true; shift ;;
    --yes) YES=true; shift ;;
    *) die "未知参数: $1" ;;
  esac
done

[[ "$PROFILE" == "minimal" || "$PROFILE" == "full" ]] || die "--profile 仅支持 minimal|full"
[[ -n "$TAG" ]] || die "--tag 必填(镜像 tag,禁止 latest)"
[[ "$TAG" != "latest" ]] || die "禁止使用 latest 作为镜像 tag"
[[ "$PROFILE" == "full" ]] && { OBSERVABILITY=true; MAILHOG=true; }

confirm() {
  [[ "$YES" == "true" ]] && return 0
  confirm_if_interactive "$*"
}

validate_context() {
  require_kubectl_context "$CONTEXT"
  log_info "目标集群: $CONTEXT"
  confirm "确认在集群 $CONTEXT 上执行 dev-up?"
}

check_context_is_safe() {
  if ! is_local_dev_context "$CONTEXT" && [[ "$ALLOW_NONLOCAL" != "true" ]]; then
    die "context '$CONTEXT' 非 kind-*/k3d-* 本地集群。如确需部署,加 --allow-nonlocal"
  fi
}

ensure_namespaces() {
  kubectl --context "$CONTEXT" create namespace aegisops-system >/dev/null 2>&1 || true
  kubectl --context "$CONTEXT" create namespace fault-lab >/dev/null 2>&1 || true
  kubectl --context "$CONTEXT" label namespace fault-lab aegisops.io/managed=true >/dev/null 2>&1 || true
  log_info "namespace 就绪: aegisops-system, fault-lab"
}

create_dev_secrets() {
  local webhook console_tokens diagnosis
  webhook="$(read_local_secret webhook-token)" || die "缺少 .local/secrets/webhook-token(先运行 scripts/init-local-config.sh)"
  console_tokens="$(read_local_secret console-tokens)" || die "缺少 .local/secrets/console-tokens"
  diagnosis="$(read_local_secret diagnosis-token)" || die "缺少 .local/secrets/diagnosis-token"

  kubectl --context "$CONTEXT" -n aegisops-system create secret generic aegisops-gateway-token \
    --from-literal=webhook-token="$webhook" --dry-run=client -o yaml | kubectl --context "$CONTEXT" apply -f - >/dev/null
  kubectl --context "$CONTEXT" -n aegisops-system create secret generic aegisops-console-auth \
    --from-literal=tokens="$console_tokens" --dry-run=client -o yaml | kubectl --context "$CONTEXT" apply -f - >/dev/null
  kubectl --context "$CONTEXT" -n aegisops-system create secret generic aegisops-diagnosis-token \
    --from-literal=token="$diagnosis" --dry-run=client -o yaml | kubectl --context "$CONTEXT" apply -f - >/dev/null

  local dk
  if dk="$(read_local_secret deepseek-api-key)" && [[ -n "$dk" ]]; then
    kubectl --context "$CONTEXT" -n aegisops-system create secret generic deepseek-api \
      --from-literal=api-key="$dk" --dry-run=client -o yaml | kubectl --context "$CONTEXT" apply -f - >/dev/null
    log_info "deepseek-api secret 已配置"
  fi
  log_info "secrets 已就绪(不打印 token)"
}

build_images() {
  [[ "$SKIP_BUILD" == "true" ]] && { log_info "跳过镜像构建(--skip-build)"; return 0; }
  "$ROOT/scripts/build-images.sh" --registry "$REGISTRY" --tag "$TAG"
}

load_images_into_kind() {
  if [[ "$CONTEXT" != kind-* ]]; then
    log_warn "非 kind context,跳过 kind load(需自行保证镜像可拉取: $REGISTRY)"
    return 0
  fi
  local cluster="${CONTEXT#kind-}"
  for img in aegisops-operator aegisops-alert-gateway aegisops-incident-api aegisops-diagnosis fault-lab; do
    log_info "kind load: $REGISTRY/$img:$TAG"
    kind load docker-image "$REGISTRY/$img:$TAG" --name "$cluster"
  done
}

install_observability() {
  [[ "$OBSERVABILITY" != "true" ]] && return 0
  require_cmd helm
  helm_ensure_repo prometheus-community https://prometheus-community.github.io/helm-charts
  helm_ensure_repo grafana https://grafana.github.io/helm-charts
  kubectl --context "$CONTEXT" create namespace observability >/dev/null 2>&1 || true
  helm --kube-context "$CONTEXT" upgrade --install kube-prometheus-stack prometheus-community/kube-prometheus-stack \
    -n observability --wait --timeout 10m >/dev/null 2>&1 || log_warn "kube-prometheus-stack 安装/升级未完成,继续(可单独重跑)"
  helm --kube-context "$CONTEXT" upgrade --install loki grafana/loki \
    -n observability --wait --timeout 10m >/dev/null 2>&1 || log_warn "loki 安装/升级未完成"
  helm --kube-context "$CONTEXT" upgrade --install tempo grafana/tempo \
    -n observability --wait --timeout 10m >/dev/null 2>&1 || log_warn "tempo 安装/升级未完成"
  mkdir -p "$ROOT/.tmp"
  kubectl --context "$CONTEXT" -n observability port-forward svc/kube-prometheus-stack-prometheus 19090:9090 --address 0.0.0.0 >/dev/null 2>&1 &
  echo "$!" >> "$ROOT/.tmp/pf.pids"
  log_info "observability 已安装(observability ns;Prometheus http://127.0.0.1:19090)"
}

install_mailhog() {
  [[ "$MAILHOG" != "true" ]] && return 0
  kubectl --context "$CONTEXT" create namespace mailhog >/dev/null 2>&1 || true
  kubectl --context "$CONTEXT" -n mailhog create deployment mailhog --image=mailhog/mailhog:v1.0.1 \
    --dry-run=client -o yaml | kubectl --context "$CONTEXT" apply -f - >/dev/null
  kubectl --context "$CONTEXT" -n mailhog create service clusterip mailhog --tcp=1025:1025,8025:8025 \
    --dry-run=client -o yaml | kubectl --context "$CONTEXT" apply -f - >/dev/null
  mkdir -p "$ROOT/.tmp"
  kubectl --context "$CONTEXT" -n mailhog rollout status deployment/mailhog --timeout=120s >/dev/null 2>&1 \
    && kubectl --context "$CONTEXT" -n mailhog port-forward svc/mailhog 18025:8025 --address 0.0.0.0 >/dev/null 2>&1 &
  echo "$!" >> "$ROOT/.tmp/pf.pids"
  log_info "mailhog 已安装(mailhog ns;UI http://127.0.0.1:18025,SMTP 集群内 mailhog:1025)"
}

install_aegisops() {
  require_cmd helm
  kubectl --context "$CONTEXT" apply -f "$ROOT/config/crd/bases/" >/dev/null
  # kindnet 的 NetworkPolicy 实现不可靠(跨 namespace DNS 放行失败/规则残留),
  # 开发环境默认关闭 netpol;生产部署(k3s/ACK 用成熟 CNI)显式 --set networkPolicy.enabled=true。
  log_warn "开发环境: networkPolicy 默认关闭(kindnet 限制);生产部署请显式开启"
  local args=(upgrade --install aegisops "$ROOT/deploy/helm/aegisops" -n aegisops-system
    --set global.imageRegistry="$REGISTRY" --set global.imageTag="$TAG"
    --set networkPolicy.enabled=false)
  [[ "$OBSERVABILITY" == "true" ]] && args+=(--set observability.serviceMonitor=true)
  if [[ -f "$ROOT/.local/values.yaml" ]]; then
    args+=(-f "$ROOT/.local/values.yaml")
  fi
  if [[ -f "$ROOT/.local/values-email.local.yaml" ]]; then
    args+=(-f "$ROOT/.local/values-email.local.yaml")
  fi
  for f in "${VALUES_FILES[@]}"; do
    [[ -f "$f" ]] || die "--values 文件不存在: $f"
    args+=(-f "$f")
  done
  log_info "helm ${args[*]}" >&2
  helm --kube-context "$CONTEXT" "${args[@]}" --wait --timeout 10m
  log_info "AegisOps Helm release 就绪(aegisops-system)"
}

install_fault_lab() {
  require_file "$ROOT/deploy/kind/faultlab.yaml"
  kubectl --context "$CONTEXT" apply -f "$ROOT/deploy/kind/faultlab.yaml" >/dev/null
  kubectl --context "$CONTEXT" apply -f "$ROOT/config/samples/ops_v1alpha1_remediationpolicy.yaml" >/dev/null
  log_info "fault-lab 与默认策略已应用"
}

index_runbooks() {
  log_info "索引 runbook 到 pgvector(M3 后可用;失败不阻塞)"
  make -C "$ROOT" runbooks-index >/dev/null 2>&1 || log_warn "runbook 索引失败(可稍后重跑 make runbooks-index)"
}

wait_for_rollouts() {
  for d in aegisops-operator aegisops-gateway aegisops-incident-api aegisops-diagnosis-api aegisops-diagnosis-worker; do
    log_info "等待 deployment/$d ..."
    wait_for_deployment "$CONTEXT" aegisops-system "$d" 300 || die "deployment/$d 未就绪(查看 kubectl -n aegisops-system get pods)"
  done
  if kubectl --context "$CONTEXT" -n fault-lab get deployment faultlab >/dev/null 2>&1; then
    wait_for_deployment "$CONTEXT" fault-lab faultlab 180 || log_warn "faultlab 未就绪"
  fi
}

start_port_forwards() {
  if [[ -x "$ROOT/scripts/pf-up.sh" ]]; then
    "$ROOT/scripts/pf-up.sh" up || log_warn "port-forward 启动不完整(可手动运行 scripts/pf-up.sh)"
  fi
}

run_smoke_checks() {
  if [[ "$OBSERVABILITY" == "true" ]]; then
    "$ROOT/scripts/check-prometheus-targets.sh" \
      --url http://127.0.0.1:19090 \
      --expected-job aegisops-operator --expected-job aegisops-gateway \
      --expected-job aegisops-incident-api --expected-job aegisops-diagnosis \
      --timeout 120 >/dev/null 2>&1 && log_info "Prometheus targets 全部 up" || log_warn "Prometheus targets 未全部 up(检查 observability)"
  fi
  if [[ "$MAILHOG" == "true" ]]; then
    if curl -sf --max-time 5 http://127.0.0.1:18025 >/dev/null 2>&1; then
      log_info "MailHog UI 可达(127.0.0.1:18025)"
    else
      log_warn "MailHog UI 不可达(检查 mailhog namespace)"
    fi
  fi
  log_info "smoke 检查完成(详细见 make smoke)"
}

write_environment_manifest() {
  local manifest="$ROOT/.local/environment.json"
  cat > "$manifest" <<EOF
{
  "context": "$CONTEXT",
  "registry": "$REGISTRY",
  "tag": "$TAG",
  "profile": "$PROFILE",
  "observability": $([[ "$OBSERVABILITY" == "true" ]] && echo true || echo false),
  "mailhog": $([[ "$MAILHOG" == "true" ]] && echo true || echo false),
  "pids": "$ROOT/.tmp/pf.pids",
  "apiBase": "http://127.0.0.1:18081",
  "diagnosis": "http://127.0.0.1:8000"
}
EOF
  log_info "环境清单: .local/environment.json"
}

validate_context
check_context_is_safe
ensure_namespaces
create_dev_secrets
build_images
load_images_into_kind
install_observability
install_mailhog
install_aegisops
install_fault_lab
index_runbooks
wait_for_rollouts
start_port_forwards
run_smoke_checks
write_environment_manifest

log_info "dev-up 完成: context=$CONTEXT tag=$TAG profile=$PROFILE"
