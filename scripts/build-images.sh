#!/usr/bin/env bash
# 构建全部 AegisOps 镜像。默认只本地构建不推送。
set -Eeuo pipefail
IFS=$'\n\t'
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/scripts/lib/common.sh"

REGISTRY=""
TAG="dev"
PLATFORM=""
PUSH=false

usage() {
  cat <<EOF
用法: build-images.sh --registry REGISTRY [--tag TAG] [--platform PLATFORM] [--push]
  --registry   镜像仓库（必填）
  --tag        镜像 tag（默认 dev；禁止仅用 latest）
  --platform   构建平台（如 linux/amd64）
  --push       构建后推送
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --registry) REGISTRY="$2"; shift 2 ;;
    --tag) TAG="$2"; shift 2 ;;
    --platform) PLATFORM="$2"; shift 2 ;;
    --push) PUSH=true; shift ;;
    *) die "未知参数: $1" ;;
  esac
done

[[ -n "$REGISTRY" ]] || die "--registry 必填"
[[ "$TAG" != "latest" ]] || die "禁止使用 latest 作为镜像 tag"

COMMIT="$(current_git_sha)"
CREATED="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
BUILDX_ARGS=()
[[ -n "$PLATFORM" ]] && BUILDX_ARGS+=(--platform "$PLATFORM")

build_one() {
  local name="$1" dockerfile="$2" context="$3"
  local img="$REGISTRY/$name:$TAG"
  log_info "构建 $img (${dockerfile})"
  docker buildx build \
    --build-arg VERSION="$TAG" \
    --build-arg COMMIT="$COMMIT" \
    --build-arg CREATED="$CREATED" \
    -t "$img" \
    -f "$ROOT/$dockerfile" \
    "${BUILDX_ARGS[@]}" \
    "$( [[ "$PUSH" == true ]] && echo --push )" \
    "$ROOT/$context"
}

build_one "aegisops-operator"       "docker/operator.Dockerfile"         "."
build_one "aegisops-alert-gateway"  "docker/alert-gateway.Dockerfile"    "."
build_one "aegisops-incident-api"   "docker/incident-api.Dockerfile"     "."
build_one "aegisops-diagnosis"      "services/diagnosis/Dockerfile"      "services/diagnosis"
build_one "fault-lab"               "fault-lab/Dockerfile"               "fault-lab"

log_info "全部镜像构建完成: $REGISTRY"
