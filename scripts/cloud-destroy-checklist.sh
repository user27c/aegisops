#!/usr/bin/env bash
# 云资源销毁前的只读检查清单。此脚本永不运行 terraform destroy。
set -Eeuo pipefail
IFS=$'\n\t'

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TF_DIR="$ROOT/infra/terraform/aliyun"
CONFIRM=""

usage() {
  cat <<'EOF'
用法: cloud-destroy-checklist.sh --confirm 'review destroy aliyun-demo' [--terraform-dir <dir>]

仅导出本地检查信息与 terraform plan -destroy；不会调用 terraform destroy，
也不会删除集群、审计记录、镜像或云资源。
EOF
}

require_value() { [[ $# -ge 2 && -n "$2" ]] || { echo "[ERROR] $1 需要一个值" >&2; exit 1; }; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --confirm|--terraform-dir) require_value "$@" ;;
  esac
  case "$1" in
    --confirm) CONFIRM="$2"; shift 2 ;;
    --terraform-dir) TF_DIR="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "[ERROR] 未知参数: $1" >&2; exit 1 ;;
  esac
done

[[ "$CONFIRM" == "review destroy aliyun-demo" ]] || { echo "[ERROR] 必须传 --confirm 'review destroy aliyun-demo'" >&2; exit 1; }
command -v terraform >/dev/null || { echo "[ERROR] 缺少 terraform" >&2; exit 1; }
[[ -d "$TF_DIR" ]] || { echo "[ERROR] Terraform 目录不存在: $TF_DIR" >&2; exit 1; }

echo "[INFO] 销毁前必须已在受控位置保存脱敏 audit、eval、截图和视频；本脚本不会收集或上传这些文件。"
echo "[INFO] 当前 Terraform state 资源(只读):"
terraform -chdir="$TF_DIR" state list || true
echo "[INFO] 生成 destroy plan（不会执行 destroy）:"
terraform -chdir="$TF_DIR" plan -destroy -out=destroy-review.tfplan
echo "[INFO] 请人工核对 ECS/EIP/磁盘/安全组与费用后，才可在独立命令中执行 terraform destroy。"
