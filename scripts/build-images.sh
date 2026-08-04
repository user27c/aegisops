#!/usr/bin/env bash
# 构建全部 AegisOps 镜像。registry 默认 aegisops.local(本地);--push 时必须指定真实 registry。
set -Eeuo pipefail
IFS=$'\n\t'
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/scripts/lib/common.sh"

REGISTRY="aegisops.local"
TAG="dev"
PLATFORM=""
PUSH=false

usage() {
  cat <<EOF
用法: build-images.sh [--registry REGISTRY] [--tag TAG] [--platform PLATFORM] [--push]
  --registry   镜像仓库(默认 aegisops.local;--push 时必须指定真实 registry)
  --tag        镜像 tag(默认 dev;禁止仅用 latest)
  --platform   构建平台(如 linux/amd64)
  --push       构建并推送(--push 时 registry 必填)
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

[[ "$TAG" != "latest" ]] || die "禁止使用 latest 作为镜像 tag"
if [[ "$PUSH" == "true" && "$REGISTRY" == "aegisops.local" ]]; then
  die "--push 必须指定真实 --registry(本地默认 aegisops.local 不推送)"
fi

COMMIT="$(current_git_sha)"
CREATED="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
BUILDX_ARGS=()
[[ -n "$PLATFORM" ]] && BUILDX_ARGS+=(--platform "$PLATFORM")

IMAGES=(
  "aegisops-operator|docker/operator.Dockerfile|."
  "aegisops-alert-gateway|docker/alert-gateway.Dockerfile|."
  "aegisops-incident-api|docker/incident-api.Dockerfile|."
  "aegisops-diagnosis|services/diagnosis/Dockerfile|."
  "fault-lab|fault-lab/Dockerfile|fault-lab"
)

DIGESTS_FILE="$ROOT/.tmp/digests.txt"
rm -f "$DIGESTS_FILE"
mkdir -p "$ROOT/.tmp"

# 本地构建用 docker build;--push 需要 buildx 插件。
HAVE_BUILDX=false
if docker buildx version >/dev/null 2>&1; then
  HAVE_BUILDX=true
elif [[ "$PUSH" == "true" ]]; then
  die "docker buildx 插件缺失(--push 需要;本地构建可省略)"
fi

build_one() {
  local name="$1" dockerfile="$2" context="$3"
  local img="$REGISTRY/$name:$TAG"
  log_info "构建 $img (${dockerfile})"
  local metadata_file="$ROOT/.tmp/buildx-meta-$name.json"
  local -a cmd
  if [[ "$HAVE_BUILDX" == "true" ]]; then
    cmd=(docker buildx build
      --build-arg VERSION="$TAG"
      --build-arg COMMIT="$COMMIT"
      --build-arg CREATED="$CREATED"
      -t "$img"
      -f "$ROOT/$dockerfile")
    [[ -n "$PLATFORM" ]] && cmd+=(--platform "$PLATFORM")
    if [[ "$PUSH" == "true" ]]; then
      cmd+=(--push)
    else
      cmd+=(--load)
    fi
    cmd+=(--metadata-file "$metadata_file" "$ROOT/$context")
  else
    cmd=(docker build
      --build-arg VERSION="$TAG"
      --build-arg COMMIT="$COMMIT"
      --build-arg CREATED="$CREATED"
      -t "$img"
      -f "$ROOT/$dockerfile"
      "$ROOT/$context")
  fi
  "${cmd[@]}"
  if [[ "$PUSH" == "true" ]]; then
    # push 后本地无镜像,buildx --metadata-file 才是 digest 唯一可靠来源。
    if [[ "$HAVE_BUILDX" == "true" && -f "$metadata_file" ]]; then
      echo "$name=$REGISTRY/$name@$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["containerimage.descriptor"]["digest"])' "$metadata_file")" >> "$DIGESTS_FILE"
    else
      echo "$name=" >> "$DIGESTS_FILE"
    fi
  else
    echo "$name=$(docker image inspect --format '{{.Id}}' "$img")" >> "$DIGESTS_FILE"
  fi
}

for entry in "${IMAGES[@]}"; do
  IFS='|' read -r name dockerfile context <<< "$entry"
  build_one "$name" "$dockerfile" "$context"
done

# 记录 digest 到 dist/images-<tag>.json(供 Helm/CI 引用)。
DIST="$ROOT/dist"
mkdir -p "$DIST"
DIGESTS_FILE="$DIGESTS_FILE" python3 - "$TAG" "$([[ "$PUSH" == "true" ]] && echo true || echo false)" > "$DIST/images-$TAG.json" <<'PY'
import json, os, sys
tag, pushed = sys.argv[1], sys.argv[2] == "true"
images = {}
for line in open(os.environ.get("DIGESTS_FILE", "/dev/null")):
    line = line.strip()
    if not line:
        continue
    name, _, digest = line.partition("=")
    if digest:
        images[name] = digest
json.dump({"tag": tag, "pushed": pushed, "images": images}, sys.stdout, indent=2)
sys.stdout.write("\n")
PY
log_info "digest 记录: dist/images-$TAG.json"

# SBOM:优先 syft;缺失时跳过并提示(不阻塞构建)。
if command -v syft >/dev/null 2>&1; then
  SBOM_DIR="$DIST/sbom-$TAG"
  mkdir -p "$SBOM_DIR"
  for entry in "${IMAGES[@]}"; do
    name="${entry%%|*}"
    img="$REGISTRY/$name:$TAG"
    if syft "$img" -o spdx-json > "$SBOM_DIR/$name.spdx.json" 2>/dev/null; then
      log_info "SBOM: $SBOM_DIR/$name.spdx.json"
    else
      log_warn "syft 生成 SBOM 失败(跳过): $name"
    fi
  done
else
  log_warn "未安装 syft,跳过 SBOM(需生成时: brew install anchore/syft/syft 或见 docs)"
fi

log_info "全部镜像构建完成: $REGISTRY (tag=$TAG)"
