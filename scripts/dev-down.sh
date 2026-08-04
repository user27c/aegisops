#!/usr/bin/env bash
# dev-down.sh — 卸载 AegisOps 本地开发环境。
# 默认只卸载 Helm release、fault-lab 与本项目 port-forward;PVC/数据保留。
# --purge-data 删除 PVC;--delete-kind-cluster 删除 Kind 集群。
set -Eeuo pipefail
IFS=$'\n\t'
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/scripts/lib/common.sh"

CONTEXT=""
PURGE=false
DELETE_KIND=false
YES=false

usage() {
  cat <<EOF
用法: dev-down.sh --context CONTEXT [options]
  --context <ctx>         kubectl context(必填)
  --purge-data           删除 PVC 与数据(先列出目标并确认)
  --delete-kind-cluster  删除对应 Kind 集群(context 须为 kind-*)
  --yes                  跳过全部交互确认
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --context) CONTEXT="$2"; shift 2 ;;
    --purge-data) PURGE=true; shift ;;
    --delete-kind-cluster) DELETE_KIND=true; shift ;;
    --yes) YES=true; shift ;;
    *) die "未知参数: $1" ;;
  esac
done

require_kubectl_context "$CONTEXT"

confirm() {
  [[ "$YES" == "true" ]] && return 0
  confirm_if_interactive "$*"
}

stop_port_forwards() {
  if [[ -x "$ROOT/scripts/pf-up.sh" ]]; then
    "$ROOT/scripts/pf-up.sh" down
  elif [[ -f "$ROOT/.tmp/pf.pids" ]]; then
    while read -r pid; do kill -9 "$pid" 2>/dev/null || true; done < "$ROOT/.tmp/pf.pids"
    rm -f "$ROOT/.tmp/pf.pids"
  fi
}

uninstall_releases() {
  if helm --kube-context "$CONTEXT" -n aegisops-system status aegisops >/dev/null 2>&1; then
    confirm "卸载 Helm release 'aegisops' (namespace aegisops-system)?"
    helm --kube-context "$CONTEXT" uninstall aegisops -n aegisops-system
  else
    log_info "Helm release 'aegisops' 不存在,跳过"
  fi
}

uninstall_fault_lab() {
  if [[ -f "$ROOT/deploy/kind/faultlab.yaml" ]] \
     && kubectl --context "$CONTEXT" -n fault-lab get deployment faultlab >/dev/null 2>&1; then
    confirm "删除 fault-lab 部署与默认策略?"
    kubectl --context "$CONTEXT" delete -f "$ROOT/deploy/kind/faultlab.yaml" >/dev/null 2>&1 || true
    kubectl --context "$CONTEXT" delete -f "$ROOT/config/samples/ops_v1alpha1_remediationpolicy.yaml" >/dev/null 2>&1 || true
  else
    log_info "fault-lab 不存在,跳过"
  fi
}

purge_data() {
  [[ "$PURGE" != "true" ]] && return 0
  local pvcs
  pvcs="$(kubectl --context "$CONTEXT" get pvc -A -o jsonpath='{range .items[*]}{.metadata.namespace}{" "}{.metadata.name}{"\n"}{end}' 2>/dev/null || true)"
  if [[ -z "$pvcs" ]]; then
    log_info "无 PVC 可清理"
    return 0
  fi
  log_warn "--purge-data 将删除以下 PVC(数据不可恢复):"
  echo "$pvcs"
  confirm "确认删除以上 PVC?"
  while read -r ns pvc; do
    [[ -n "$ns" ]] && kubectl --context "$CONTEXT" -n "$ns" delete pvc "$pvc" >/dev/null
  done <<< "$pvcs"
  log_info "PVC 已清理"
}

delete_kind_cluster() {
  [[ "$DELETE_KIND" != "true" ]] && return 0
  [[ "$CONTEXT" == kind-* ]] || die "--delete-kind-cluster 仅适用于 kind-* context"
  local cluster="${CONTEXT#kind-}"
  confirm "删除 Kind 集群 '$cluster'?"
  kind delete cluster --name "$cluster"
}

stop_port_forwards
uninstall_releases
uninstall_fault_lab
purge_data
delete_kind_cluster

log_info "dev-down 完成: context=$CONTEXT (namespace/数据默认保留)"
