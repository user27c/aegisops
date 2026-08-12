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
PROFILE="full"
E2E_VERIFICATION_WINDOW="${AEGISOPS_E2E_VERIFICATION_WINDOW:-5m}"

usage() {
  cat <<EOF
用法: e2e-up.sh [options]
  --run-id <id>       run 标识(默认时间戳;影响 namespace 与 environment.json)
  --registry <reg>    镜像 registry(默认 aegisops.local)
  --tag <tag>         镜像 tag(默认 dev,禁止 latest)
  --skip-build        跳过镜像构建(镜像已 load 时)
  --keep-cluster      显式复用已存在的 kind-aegisops-e2e（默认拒绝；绝不删除已有集群）
  --profile <name>    E2E 拓扑: full(默认，所有场景)或 core(仅 Auto Restart)
  -h, --help          显示本帮助
EOF
}

require_option_value() {
  [[ $# -ge 2 && -n "$2" ]] || die "$1 需要一个值"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --run-id) require_option_value "$@"; RUN_ID="$2"; RUN_NS="aegisops-e2e-$2"; shift 2 ;;
    --registry) require_option_value "$@"; REGISTRY="$2"; shift 2 ;;
    --tag) require_option_value "$@"; TAG="$2"; shift 2 ;;
    --skip-build) SKIP_BUILD=true; shift ;;
    --keep-cluster) KEEP_CLUSTER=true; shift ;;
    --profile) require_option_value "$@"; PROFILE="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) die "未知参数: $1" ;;
  esac
done

[[ "$TAG" != "latest" ]] || die "禁止使用 latest 作为镜像 tag"
case "$PROFILE" in
  full|core) ;;
  *) die "未知 E2E profile: $PROFILE(可选 full 或 core)" ;;
esac
[[ "$E2E_VERIFICATION_WINDOW" =~ ^[1-9][0-9]*[smh]$ ]] || die "AEGISOPS_E2E_VERIFICATION_WINDOW 必须是正整数加 s/m/h(当前: $E2E_VERIFICATION_WINDOW)"
require_cmd kind
require_cmd helm
require_cmd docker

E2E_KUBECONFIG="${AEGISOPS_E2E_KUBECONFIG:-$HOME/.kube/config}"
# GitHub-hosted runner 没有预置 kubeconfig；Kind 会在创建集群时写入这个
# 路径。只有未显式指定时才创建空文件，显式 kubeconfig 仍必须由调用方提供，
# 防止把拼写错误或受保护环境悄悄替换为空配置。
if [[ -z "${AEGISOPS_E2E_KUBECONFIG:-}" ]]; then
  mkdir -p "$(dirname "$E2E_KUBECONFIG")"
  touch "$E2E_KUBECONFIG"
  chmod 0600 "$E2E_KUBECONFIG"
else
  require_file "$E2E_KUBECONFIG" "缺少 E2E kubeconfig: $E2E_KUBECONFIG"
fi
export KUBECONFIG="$E2E_KUBECONFIG"

E2E_DIR="$ROOT/.local/e2e"
mkdir -p "$E2E_DIR"
PORT_FORWARD_STATE="$E2E_DIR/pf.pids"
E2E_CLUSTER_STATE="$E2E_DIR/created-cluster"

stop_port_forwards() {
  [[ -f "$PORT_FORWARD_STATE" ]] || return 0
  while read -r pid; do
    [[ "$pid" =~ ^[0-9]+$ ]] || continue
    local command
    command="$(ps -p "$pid" -o args= 2>/dev/null || true)"
    # PID state may be stale and later reused. Only signal the detached
    # kubectl loop we created; never kill an unrelated user process.
    if [[ "$command" != *"kubectl"* || "$command" != *"port-forward"* ]]; then
      log_warn "跳过非 E2E port-forward PID: $pid"
      continue
    fi
    # The forward loop is started with setsid, so stopping its process group
    # also stops the child kubectl process.  Fall back to the leader if the
    # process already lost its session.
    kill -- "-$pid" 2>/dev/null || kill "$pid" 2>/dev/null || true
  done < "$PORT_FORWARD_STATE"
  rm -f "$PORT_FORWARD_STATE"
}

# A failed setup must not leave reconnecting port-forward loops behind.  A
# successful setup intentionally keeps them alive for run-e2e.sh.
cleanup_failed_setup() {
  local status=$?
  if [[ "$status" -ne 0 ]]; then
    stop_port_forwards
  fi
  exit "$status"
}
trap cleanup_failed_setup EXIT

ensure_cluster() {
  if kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
    [[ "$KEEP_CLUSTER" == "true" ]] || die "检测到已有集群 $CLUSTER；为保护用户资源，使用 --keep-cluster 才可复用，脚本绝不删除已有集群"
    log_info "复用集群 $CLUSTER(--keep-cluster)"
    return
  fi
  kind create cluster --name "$CLUSTER" --wait 120s
  printf '%s\n' "$CLUSTER" > "$E2E_CLUSTER_STATE"
}

build_and_load_images() {
  if [[ "$SKIP_BUILD" == "true" ]]; then
    log_info "跳过镜像构建(--skip-build),仍加载本地已有镜像"
  else
    "$ROOT/scripts/build-images.sh" --registry "$REGISTRY" --tag "$TAG"
  fi
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

install_loki() {
  helm_ensure_repo grafana https://grafana.github.io/helm-charts
  kubectl --context "$CONTEXT" create namespace observability >/dev/null 2>&1 || true
  helm --kube-context "$CONTEXT" upgrade --install loki grafana/loki \
    -n observability -f "$ROOT/deploy/observability/loki/values-e2e.yaml" \
    --wait --timeout 10m >/dev/null
  helm --kube-context "$CONTEXT" upgrade --install promtail grafana/promtail \
    -n observability -f "$ROOT/deploy/observability/loki/promtail-values-e2e.yaml" \
    --wait --timeout 10m >/dev/null
  log_info "loki/promtail 就绪(observability)"
}

install_tempo() {
  helm_ensure_repo grafana https://grafana.github.io/helm-charts
  kubectl --context "$CONTEXT" create namespace observability >/dev/null 2>&1 || true
  helm --kube-context "$CONTEXT" upgrade --install tempo grafana/tempo \
    -n observability -f "$ROOT/deploy/observability/tempo/values-e2e.yaml" \
    --wait --timeout 10m >/dev/null
  log_info "tempo 就绪(observability)"
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
  old_releases="$(helm --kube-context "$CONTEXT" list -A --filter '^aegisops$' -o json 2>/dev/null \
    | python3 -c 'import json,sys; print("\n".join(r["namespace"] for r in json.load(sys.stdin)))' 2>/dev/null || true)"
  if [[ "$KEEP_CLUSTER" == "true" && -n "$old_releases" ]]; then
    while read -r old_ns; do
      [[ -n "$old_ns" && "$old_ns" != "$RUN_NS" ]] || continue
      if [[ ! "$old_ns" =~ ^aegisops-e2e-[a-zA-Z0-9-]+$ ]]; then
        log_warn "跳过非 E2E namespace 的 release: $old_ns"
        continue
      fi
      if [[ "$(kubectl --context "$CONTEXT" get namespace "$old_ns" -o go-template='{{ index .metadata.labels "aegisops.io/managed" }}' 2>/dev/null || true)" != "true" ]]; then
        log_warn "跳过未标记 aegisops.io/managed=true 的 namespace: $old_ns"
        continue
      fi
      log_info "清理旧 run release: $old_ns"
      helm --kube-context "$CONTEXT" uninstall aegisops -n "$old_ns" >/dev/null 2>&1 || true
      kubectl --context "$CONTEXT" delete namespace "$old_ns" --wait=false >/dev/null 2>&1 || true
    done <<< "$old_releases"
  fi
  kubectl --context "$CONTEXT" create namespace "$RUN_NS" --dry-run=client -o yaml \
    | kubectl --context "$CONTEXT" apply -f - >/dev/null
  kubectl --context "$CONTEXT" label namespace "$RUN_NS" aegisops.io/managed=true --overwrite >/dev/null
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

  local -a helm_values=(
    --set "global.imageRegistry=$REGISTRY"
    --set "global.imageTag=$TAG"
    --set "global.watchNamespaces={$RUN_NS}"
    --set "networkPolicy.enabled=false"
    --set "diagnosis.llmProvider=fake"
  )
  if [[ "$PROFILE" == "core" ]]; then
    # Auto Restart 由 gateway 的合成 webhook 驱动；不需要 Prometheus、
    # Alertmanager 或邮件，因此不渲染 ServiceMonitor/PrometheusRule。
    helm_values+=(
      --set "operator.replicas=1"
      --set "diagnosis.worker.concurrency=1"
      --set "postgresql.internal.storageSize=1Gi"
      --set "observability.serviceMonitor=false"
      --set "observability.prometheusRule=false"
      --set "alerting.enabled=false"
    )
  else
    helm_values+=(
      --set "observability.serviceMonitor=true"
      --set "observability.otelCollector.enabled=true"
      --set "operator.evidence.prometheusURL=http://kube-prometheus-stack-prometheus.observability.svc:9090"
      --set "operator.evidence.lokiURL=http://loki.observability.svc:3100"
      --set "alerting.enabled=true"
      --set "alerting.smtp.smarthost=mailhog.mailhog.svc.cluster.local:1025"
      --set "alerting.smtp.from=aegisops-e2e@example.invalid"
      --set "alerting.smtp.requireTLS=false"
      --set "alerting.smtp.auth.passwordSecret.name=e2e-unused"
      --set "alerting.smtp.auth.passwordSecret.key=password"
      --set "alerting.email.to[0]=receiver@example.invalid"
    )
  fi

  helm --kube-context "$CONTEXT" upgrade --install aegisops "$ROOT/deploy/helm/aegisops" -n "$RUN_NS" \
    "${helm_values[@]}" \
    --wait --timeout 10m >/dev/null
  # RestoreConfigMap is intentionally not enabled by broad Helm RBAC. The
  # local fixture adds a resourceNames-scoped Role for checkout-config only.
  local restore_rbac="$E2E_DIR/restore-configmap-rbac.render.yaml"
  sed "s|namespace: fault-lab|namespace: $RUN_NS|g" \
    "$ROOT/deploy/kind/restore-configmap-rbac.yaml" > "$restore_rbac"
  kubectl --context "$CONTEXT" apply -f "$restore_rbac" >/dev/null
  if [[ "$KEEP_CLUSTER" == "true" ]]; then
    # 本地 E2E 固定使用 dev tag。重新 kind load 后 Helm values 不变，
    # 因此不会自动滚动 Pod；复用隔离集群时显式滚动所有本项目 Deployment。
    # 这也让外部 console-auth Secret 的临时 viewer token 被 incident-api 重读。
    for deployment in aegisops-operator aegisops-gateway aegisops-incident-api aegisops-diagnosis-api aegisops-diagnosis-worker; do
      kubectl --context "$CONTEXT" -n "$RUN_NS" rollout restart "deployment/$deployment" >/dev/null
    done
  fi
  log_info "AegisOps 已安装($RUN_NS,watch=$RUN_NS,profile=$PROFILE)"
}

install_fault_lab() {
  require_file "$ROOT/deploy/kind/faultlab.yaml"
  require_file "$ROOT/deploy/kind/faultlab-configmaps.yaml"
  local rendered="$E2E_DIR/faultlab.render.yaml"
  local configmaps_rendered="$E2E_DIR/faultlab-configmaps.render.yaml"
  sed "s|namespace: fault-lab|namespace: $RUN_NS|g" \
    "$ROOT/deploy/kind/faultlab-configmaps.yaml" > "$configmaps_rendered"
  kubectl --context "$CONTEXT" apply -f "$configmaps_rendered" >/dev/null
  if [[ "$PROFILE" == "core" ]]; then
    # faultlab.yaml 的最后一个文档是仅供邮件场景使用的 ServiceMonitor。
    # core profile 不应在未安装 Prometheus CRD 的集群中创建它。
    awk '/^---$/ { separators++; if (separators == 3) exit } { print }' "$ROOT/deploy/kind/faultlab.yaml" \
      | sed "s|aegisops.local/fault-lab:dev|$REGISTRY/fault-lab:$TAG|g; s|namespace: fault-lab|namespace: $RUN_NS|g" \
      > "$rendered"
  else
    sed "s|aegisops.local/fault-lab:dev|$REGISTRY/fault-lab:$TAG|g; s|namespace: fault-lab|namespace: $RUN_NS|g" \
      "$ROOT/deploy/kind/faultlab.yaml" > "$rendered"
  fi
  kubectl --context "$CONTEXT" apply -f "$rendered" >/dev/null
  if [[ "$KEEP_CLUSTER" == "true" ]]; then
    kubectl --context "$CONTEXT" -n "$RUN_NS" rollout restart deployment/faultlab >/dev/null
  fi
  # 默认策略:RestartWorkload=Auto,Scale/Patch=ApprovalRequired(落到 RUN_NS)
  sed "s|namespace: fault-lab|namespace: $RUN_NS|g; s|^  verificationWindow: .*|  verificationWindow: $E2E_VERIFICATION_WINDOW|" \
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
  local state="$PORT_FORWARD_STATE"
  stop_port_forwards
  : > "$state"
  local -a forwards=(
    "$RUN_NS svc/aegisops-gateway 18080:8080"
    "$RUN_NS svc/aegisops-incident-api 18081:8080"
    "$RUN_NS svc/aegisops-diagnosis-api 8000:8000"
    "$RUN_NS svc/faultlab 18092:8080"
  )
  local -a ports=(18080 18081 8000 18092)
  if [[ "$PROFILE" == "full" ]]; then
    forwards+=(
      "observability svc/kube-prometheus-stack-prometheus 19090:9090"
      "observability svc/kube-prometheus-stack-alertmanager 19093:9093"
      "observability svc/loki 13100:3100"
      "observability svc/tempo 13200:3200"
      "mailhog svc/mailhog 18025:8025"
    )
    ports+=(19090 19093 13100 13200 18025)
  fi
  local f ns svc port
  for f in "${forwards[@]}"; do
    IFS=" " read -r ns svc port <<< "$f"
    # Pod 重启(例如 OOM E2E)会让一次性 kubectl port-forward 退出；循环
    # 重连，避免后续场景因本地转发失效而级联失败。
    # shellcheck disable=SC2016
    # The child bash intentionally expands its own positional parameters.
    setsid bash -c '
      context="$1"; ns="$2"; svc="$3"; port="$4"
      while true; do
        kubectl --context "$context" -n "$ns" port-forward --address 127.0.0.1 "$svc" "$port" >/dev/null 2>&1 || true
        sleep 1
      done
    ' _ "$CONTEXT" "$ns" "$svc" "$port" >/dev/null 2>&1 < /dev/null &
    echo $! >> "$state"
  done

  # port-forward 启动失败时 kubectl 会立即退出；仅 sleep 后打印 ready
  # 会把旧 run 的端口误当成新 run，导致测试请求串环境。
  local port
  for port in "${ports[@]}"; do
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
  "profile": "$PROFILE",
  "runID": "$RUN_ID",
  "context": "$CONTEXT",
  "namespace": "$RUN_NS",
  "systemNamespace": "$RUN_NS",
  "gatewayUrl": "http://127.0.0.1:18080",
  "incidentApiUrl": "http://127.0.0.1:18081",
  "diagnosisUrl": "http://127.0.0.1:8000",
  "faultLabUrl": "http://127.0.0.1:18092",
EOF
  if [[ "$PROFILE" == "full" ]]; then
    cat >> "$E2E_DIR/environment.json" <<EOF
  "prometheusUrl": "http://127.0.0.1:19090",
  "lokiUrl": "http://127.0.0.1:13100",
  "tempoUrl": "http://127.0.0.1:13200",
  "mailhogUrl": "http://127.0.0.1:18025",
EOF
  fi
  cat >> "$E2E_DIR/environment.json" <<EOF
  "webhookToken": "$webhook",
  "approverToken": "$approver",
  "viewerToken": "$viewer",
  "registry": "$REGISTRY",
  "tag": "$TAG"
}
EOF
  chmod 0600 "$E2E_DIR/environment.json"
  log_info "环境清单: $E2E_DIR/environment.json(run=$RUN_NS,profile=$PROFILE)"
}

ensure_cluster
build_and_load_images
if [[ "$PROFILE" == "full" ]]; then
  install_observability
  install_loki
  install_tempo
  install_mailhog
fi
install_aegisops
install_fault_lab
wait_rollouts
start_port_forwards
write_environment

log_info "e2e-up 完成: context=$CONTEXT run=$RUN_NS profile=$PROFILE"
