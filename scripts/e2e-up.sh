#!/usr/bin/env bash
# e2e-up.sh — 创建隔离的 E2E Kind 集群(kind-aegisops-e2e)并安装完整系统。
# run namespace 由 RUN_ID 生成并注入 operator watchNamespaces;token 不打印到日志。
set -Eeuo pipefail
IFS=$'\n\t'
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/scripts/lib/common.sh"

CONTEXT="kind-aegisops-e2e"
CLUSTER="${CONTEXT#kind-}"
RUN_ID="$(date +%Y%m%d%H%M%S)"
RUN_NS="aegisops-e2e-$RUN_ID"
REGISTRY="aegisops.local"
TAG="dev"
SKIP_BUILD=false
KEEP_CLUSTER=false
E2E_CONSOLE_TOKENS=""

usage() {
  cat <<EOF
用法: e2e-up.sh [options]
  --run-id <id>       run 标识(默认时间戳;影响 namespace 与 environment.json)
  --registry <reg>    镜像 registry(默认 aegisops.local)
  --tag <tag>         镜像 tag(默认 dev,禁止 latest)
  --skip-build        跳过镜像构建(镜像已 load 时)
  --keep-cluster      复用已存在的 kind-aegisops-e2e(否则删除重建)
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --run-id) RUN_ID="$2"; RUN_NS="aegisops-e2e-$2"; shift 2 ;;
    --registry) REGISTRY="$2"; shift 2 ;;
    --tag) TAG="$2"; shift 2 ;;
    --skip-build) SKIP_BUILD=true; shift ;;
    --keep-cluster) KEEP_CLUSTER=true; shift ;;
    *) die "未知参数: $1" ;;
  esac
done

[[ "$TAG" != "latest" ]] || die "禁止使用 latest 作为镜像 tag"
require_cmd kind
require_cmd helm
require_cmd docker

E2E_DIR="$ROOT/.local/e2e"
mkdir -p "$E2E_DIR"

ensure_cluster() {
  if kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
    if [[ "$KEEP_CLUSTER" != "true" ]]; then
      log_warn "复用集群 $CLUSTER 需 --keep-cluster;删除重建"
      kind delete cluster --name "$CLUSTER"
      kind create cluster --name "$CLUSTER" --wait 120s
    else
      log_info "复用集群 $CLUSTER(--keep-cluster)"
    fi
  else
    kind create cluster --name "$CLUSTER" --wait 120s
  fi
}

build_and_load_images() {
  [[ "$SKIP_BUILD" == "true" ]] && { log_info "跳过镜像构建(--skip-build)"; return 0; }
  "$ROOT/scripts/build-images.sh" --registry "$REGISTRY" --tag "$TAG"
  for img in aegisops-operator aegisops-alert-gateway aegisops-incident-api aegisops-diagnosis fault-lab; do
    log_info "kind load: $REGISTRY/$img:$TAG"
    kind load docker-image "$REGISTRY/$img:$TAG" --name "$CLUSTER"
  done
}

install_observability() {
  helm_ensure_repo prometheus-community https://prometheus-community.github.io/helm-charts
  kubectl --context "$CONTEXT" create namespace observability >/dev/null 2>&1 || true
  local values_file="$E2E_DIR/kube-prometheus-stack.values.yaml"
  cat > "$values_file" <<EOF
alertmanager:
  enabled: true
  config:
    global:
      smtp_smarthost: mailhog.mailhog.svc.cluster.local:1025
      smtp_from: aegisops-e2e@example.invalid
      smtp_require_tls: false
    route:
      receiver: email-e2e
      group_interval: 30s
    receivers:
      - name: "null"
      - name: email-e2e
        email_configs:
          - to: receiver@example.invalid
            send_resolved: true
  serviceMonitorSelector:
    matchLabels:
      release: kube-prometheus-stack
  ingress:
    enabled: false
grafana:
  enabled: false
prometheus:
  prometheusSpec:
    serviceMonitorSelector:
      matchLabels:
        release: kube-prometheus-stack
EOF
  helm --kube-context "$CONTEXT" upgrade --install kube-prometheus-stack prometheus-community/kube-prometheus-stack \
    -n observability -f "$values_file" --wait --timeout 15m
  log_info "kube-prometheus-stack 就绪(observability)"
}

install_mailhog() {
  kubectl --context "$CONTEXT" create namespace mailhog >/dev/null 2>&1 || true
  kubectl --context "$CONTEXT" -n mailhog create deployment mailhog --image=mailhog/mailhog:v1.0.1 \
    --dry-run=client -o yaml | kubectl --context "$CONTEXT" apply -f - >/dev/null
  kubectl --context "$CONTEXT" -n mailhog create service clusterip mailhog --tcp=1025:1025,8025:8025 \
    --dry-run=client -o yaml | kubectl --context "$CONTEXT" apply -f - >/dev/null
  kubectl --context "$CONTEXT" -n mailhog rollout status deployment/mailhog --timeout=120s >/dev/null
  log_info "mailhog 就绪(mailhog)"
}

install_aegisops() {
  # --keep-cluster 复用旧集群时,清理上一轮 run 的 release 与 namespace
  # (ClusterRole 等集群级资源带旧 release ownership,新 run-id 无法 helm 导入)。
  local old_releases
  old_releases="$(helm --kube-context "$CONTEXT" list -A --filter aegisops -o json 2>/dev/null \
    | python3 -c 'import json,sys; print("\n".join(r["namespace"] for r in json.load(sys.stdin)))' 2>/dev/null || true)"
  if [[ -n "$old_releases" ]]; then
    while read -r old_ns; do
      [[ -n "$old_ns" && "$old_ns" != "$RUN_NS" ]] || continue
      log_info "清理旧 run release: $old_ns"
      helm --kube-context "$CONTEXT" uninstall aegisops -n "$old_ns" >/dev/null 2>&1 || true
      kubectl --context "$CONTEXT" delete namespace "$old_ns" --wait=false >/dev/null 2>&1 || true
    done <<< "$old_releases"
  fi
  kubectl --context "$CONTEXT" create namespace "$RUN_NS" >/dev/null
  kubectl --context "$CONTEXT" label namespace "$RUN_NS" aegisops.io/managed=true >/dev/null
  kubectl --context "$CONTEXT" apply -f "$ROOT/config/crd/bases/" >/dev/null

  local webhook console_tokens diagnosis viewer_only
  webhook="$(read_local_secret webhook-token)" || die "缺少 .local/secrets/webhook-token(先跑 scripts/init-local-config.sh)"
  console_tokens="$(read_local_secret console-tokens)" || die "缺少 .local/secrets/console-tokens"
  diagnosis="$(read_local_secret diagnosis-token)" || die "缺少 .local/secrets/diagnosis-token"

  # 角色边界 E2E 需要一枚 viewer-only token。旧版本地配置可能只有
  # viewer,approver 合并 token；为本次隔离 run 补一枚临时 token，
  # 不回写 .local/secrets，避免改变开发者已有凭证。
  viewer_only="$(awk -F: '$2 ~ /(^|,)viewer(,|$)/ && $2 !~ /(^|,)approver(,|$)/ {print; exit}' <<< "$console_tokens")"
  E2E_CONSOLE_TOKENS="$console_tokens"
  if [[ -z "$viewer_only" ]]; then
    E2E_CONSOLE_TOKENS+=$'\n'"console-token-$(random_token 24):viewer"
  fi

  kubectl --context "$CONTEXT" -n "$RUN_NS" create secret generic aegisops-gateway-token \
    --from-literal=webhook-token="$webhook" --dry-run=client -o yaml | kubectl --context "$CONTEXT" apply -f - >/dev/null
  kubectl --context "$CONTEXT" -n "$RUN_NS" create secret generic aegisops-console-auth \
    --from-literal=tokens="$E2E_CONSOLE_TOKENS" --dry-run=client -o yaml | kubectl --context "$CONTEXT" apply -f - >/dev/null
  kubectl --context "$CONTEXT" -n "$RUN_NS" create secret generic aegisops-diagnosis-token \
    --from-literal=token="$diagnosis" --dry-run=client -o yaml | kubectl --context "$CONTEXT" apply -f - >/dev/null

  helm --kube-context "$CONTEXT" upgrade --install aegisops "$ROOT/deploy/helm/aegisops" -n "$RUN_NS" \
    --set global.imageRegistry="$REGISTRY" --set global.imageTag="$TAG" \
    --set global.watchNamespaces="{${RUN_NS}}" \
    --set observability.serviceMonitor=true \
    --set alerting.enabled=true \
    --set alerting.smtp.smarthost=mailhog.mailhog.svc.cluster.local:1025 \
    --set alerting.smtp.from=aegisops-e2e@example.invalid \
    --set alerting.smtp.requireTLS=false \
    --set alerting.smtp.auth.passwordSecret.name=e2e-unused \
    --set alerting.smtp.auth.passwordSecret.key=password \
    --set alerting.email.to[0]=receiver@example.invalid \
    --set networkPolicy.enabled=false \
    --set diagnosis.llmProvider=fake \
    --wait --timeout 10m >/dev/null
  log_info "AegisOps 已安装($RUN_NS,watch=$RUN_NS)"
}

install_fault_lab() {
  require_file "$ROOT/deploy/kind/faultlab.yaml"
  local rendered="$E2E_DIR/faultlab.render.yaml"
  sed "s|aegisops.local/fault-lab:dev|$REGISTRY/fault-lab:$TAG|g; s|namespace: fault-lab|namespace: $RUN_NS|g" \
    "$ROOT/deploy/kind/faultlab.yaml" > "$rendered"
  kubectl --context "$CONTEXT" apply -f "$rendered" >/dev/null
  # 默认策略:RestartWorkload=Auto,Scale/Patch=ApprovalRequired(落到 RUN_NS)
  sed "s|namespace: fault-lab|namespace: $RUN_NS|g" \
    "$ROOT/config/samples/ops_v1alpha1_remediationpolicy.yaml" \
    | kubectl --context "$CONTEXT" apply -f - >/dev/null
  log_info "fault-lab 与默认策略已应用($RUN_NS)"
}

wait_rollouts() {
  for d in aegisops-operator aegisops-gateway aegisops-incident-api aegisops-diagnosis-api aegisops-diagnosis-worker; do
    log_info "等待 deployment/$d ..."
    wait_for_deployment "$CONTEXT" "$RUN_NS" "$d" 300 || die "deployment/$d 未就绪"
  done
  wait_for_deployment "$CONTEXT" "$RUN_NS" faultlab 180 || die "faultlab 未就绪"
  kubectl --context "$CONTEXT" -n "$RUN_NS" rollout status deployment/faultlab --timeout=120s >/dev/null
}

start_port_forwards() {
  local state="$E2E_DIR/pf.pids"
  : > "$state"
  local -a forwards=(
    "$RUN_NS svc/aegisops-gateway 18080:8080"
    "$RUN_NS svc/aegisops-incident-api 18081:8080"
    "$RUN_NS svc/aegisops-diagnosis-api 8000:8000"
    "$RUN_NS svc/faultlab 18092:8080"
    "observability svc/kube-prometheus-stack-prometheus 19090:9090"
    "mailhog svc/mailhog 18025:8025"
  )
  local f ns svc port
  for f in "${forwards[@]}"; do
    IFS=" " read -r ns svc port <<< "$f"
    # Pod 重启(例如 OOM E2E)会让一次性 kubectl port-forward 退出；循环
    # 重连，避免后续场景因本地转发失效而级联失败。
    setsid bash -c '
      context="$1"; ns="$2"; svc="$3"; port="$4"
      while true; do
        kubectl --context "$context" -n "$ns" port-forward --address 0.0.0.0 "$svc" "$port" >/dev/null 2>&1 || true
        sleep 1
      done
    ' _ "$CONTEXT" "$ns" "$svc" "$port" >/dev/null 2>&1 < /dev/null &
    echo $! >> "$state"
  done

  # port-forward 启动失败时 kubectl 会立即退出；仅 sleep 后打印 ready
  # 会把旧 run 的端口误当成新 run，导致测试请求串环境。
  local port
  for port in 18080 18081 8000 18092 19090 18025; do
    local ready=false
    for _ in {1..20}; do
      if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
        exec 3>&-
        exec 3<&-
        ready=true
        break
      fi
      sleep 0.25
    done
    [[ "$ready" == "true" ]] || die "port-forward 未监听 127.0.0.1:$port(端口可能被旧 run 占用)"
  done
  log_info "port-forward 就绪(PID: $state)"
}

write_environment() {
  local webhook console_tokens approver viewer
  webhook="$(read_local_secret webhook-token)"
  if [[ -n "$E2E_CONSOLE_TOKENS" ]]; then
    console_tokens="$E2E_CONSOLE_TOKENS"
  else
    console_tokens="$(read_local_secret console-tokens)"
  fi
  # tokens 格式:每行 token:role[,role...];取第一个 approver 与 viewer-only。
  approver="$(awk -F: '$2 ~ /(^|,)approver(,|$)/ {print $1; exit}' <<< "$console_tokens")"
  viewer="$(awk -F: '$2 ~ /(^|,)viewer(,|$)/ && $2 !~ /(^|,)approver(,|$)/ {print $1; exit}' <<< "$console_tokens")"
  [[ -n "$approver" && -n "$viewer" ]] || die "E2E console token 缺少 approver/viewer 角色"
  cat > "$E2E_DIR/environment.json" <<EOF
{
  "context": "$CONTEXT",
  "namespace": "$RUN_NS",
  "systemNamespace": "$RUN_NS",
  "gatewayUrl": "http://127.0.0.1:18080",
  "incidentApiUrl": "http://127.0.0.1:18081",
  "diagnosisUrl": "http://127.0.0.1:8000",
  "faultLabUrl": "http://127.0.0.1:18092",
  "prometheusUrl": "http://127.0.0.1:19090",
  "mailhogUrl": "http://127.0.0.1:18025",
  "webhookToken": "$webhook",
  "approverToken": "$approver",
  "viewerToken": "$viewer",
  "registry": "$REGISTRY",
  "tag": "$TAG"
}
EOF
  chmod 0600 "$E2E_DIR/environment.json"
  log_info "环境清单: $E2E_DIR/environment.json(run=$RUN_NS)"
}

ensure_cluster
build_and_load_images
install_observability
install_mailhog
install_aegisops
install_fault_lab
wait_rollouts
start_port_forwards
write_environment

log_info "e2e-up 完成: context=$CONTEXT run=$RUN_NS"
