#!/usr/bin/env bash
# v0.2.0 发布门禁。默认 fail-closed；不会创建 tag、发布镜像或上传 artifact。
set -Eeuo pipefail
IFS=$'\n\t'

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck disable=SC1091 # ROOT is computed at runtime.
source "$ROOT/scripts/lib/common.sh"

WITH_INTEGRATION_E2E=false
ARTIFACT_DIR=""

usage() {
  cat <<'EOF'
用法: release-check.sh --with-integration-e2e --artifact-dir <sanitized-e2e-artifacts>

发布检查要求干净工作区、完整 Integration/Kind E2E、已脱敏 artifact、镜像构建、
SBOM、漏洞扫描与文档链接。此脚本不会提交、打 tag、push、发布或 destroy 资源。
EOF
}

require_value() { [[ $# -ge 2 && -n "$2" ]] || die "$1 需要一个值"; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --with-integration-e2e) WITH_INTEGRATION_E2E=true; shift ;;
    --artifact-dir) require_value "$@"; ARTIFACT_DIR="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) die "未知参数: $1" ;;
  esac
done

[[ "$WITH_INTEGRATION_E2E" == true ]] || die "发布门禁拒绝跳过 Integration/Kind E2E：必须传 --with-integration-e2e"
[[ -n "$ARTIFACT_DIR" && -d "$ARTIFACT_DIR" ]] || die "--artifact-dir 必须指向已脱敏的 E2E artifact 目录"

for command in docker helm kubeconform promtool syft trivy; do require_cmd "$command"; done

[[ -z "$(git -C "$ROOT" status --porcelain)" ]] || die "发布检查要求干净 Git 工作区"

bash "$ROOT/scripts/check-repo-hygiene.sh"
make -C "$ROOT" verify-generated
make -C "$ROOT" verify
make -C "$ROOT" test-envtest
make -C "$ROOT" test-integration
make -C "$ROOT" test-e2e

terraform -chdir="$ROOT/infra/terraform/aliyun" fmt -check -recursive
terraform -chdir="$ROOT/infra/terraform/aliyun" validate

rendered="$(mktemp "${TMPDIR:-/tmp}/aegisops-release.XXXXXX.yaml")"
trap 'rm -f "$rendered"' EXIT
helm template aegisops "$ROOT/deploy/helm/aegisops" > "$rendered"
# -ignore-missing-schemas:第三方 CRD(ServiceMonitor 等)不在 kubeconform
# 默认 schema 目录,与 CI 一致跳过;内置资源仍走 -strict 严格校验。
kubeconform -strict -ignore-missing-schemas -summary "$rendered"
# Prometheus 告警规则校验:渲染 Helm chart 的 PrometheusRule 后执行
# promtool check rules + promtool test rules。Loki 的 recording-rules.yml
# 是 LogQL(含 |~ 管道),不属 promtool 校验范围,由 Loki ruler 加载时校验。
"$ROOT/scripts/render-prometheus-rules.sh"

scripts/build-images.sh --registry aegisops-release-check --tag verify
for image in aegisops-operator aegisops-alert-gateway aegisops-incident-api aegisops-diagnosis fault-lab; do
  trivy image --exit-code 1 --severity HIGH,CRITICAL --ignorefile "$ROOT/.trivyignore" "aegisops-release-check/$image:verify"
done
# SBOM 由 build-images.sh 写入 dist/sbom-verify/。SBOM 含包维护者邮箱等公开
# 元数据,不属于 PII/Secret 扫描范围,故不写入 --artifact-dir 脱敏目录。
python3 "$ROOT/scripts/sanitize-e2e-artifacts.py" --source "$ARTIFACT_DIR" --scan-only

python3 "$ROOT/scripts/sanitize-e2e-artifacts.py" --source "$ARTIFACT_DIR" --scan-only

log_info "release-check 全部通过；仍需人工核对真实 DeepSeek、云端销毁、截图、视频与发布说明。"
