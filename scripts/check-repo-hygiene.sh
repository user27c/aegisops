#!/usr/bin/env bash
# check-repo-hygiene.sh — 仓库卫生只读检查。
# 违反任一规则返回非零；不自动删除任何文件。
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

FAIL=0

check_for_tracked_cache() {
  local hits
  hits=$(git ls-files 'buildcache/**' '.tmp/**' 2>/dev/null)
  if [[ -n "$hits" ]]; then
    echo "FAIL: 构建缓存被 Git 跟踪:" >&2
    echo "$hits" | head -5 >&2
    FAIL=1
  fi
}

check_for_large_binaries() {
  local hits
  hits=$(git ls-files -z | while IFS= read -r -d '' f; do
    if [[ -f "$f" ]]; then
      size=$(stat -c %s "$f" 2>/dev/null || echo 0)
      if (( size > 5242880 )); then
        echo "$f ($size bytes)"
      fi
    fi
  done)
  if [[ -n "$hits" ]]; then
    echo "FAIL: 跟踪文件超过 5MiB(白名单外):" >&2
    echo "$hits" | head -5 >&2
    FAIL=1
  fi
}

check_required_docs() {
  # README 引用的本地 Markdown 必须存在。
  local missing=0
  while read -r link; do
    [[ -z "$link" ]] && continue
    case "$link" in
      http*|mailto:*|'#'*) continue ;;
    esac
    # 去掉锚点
    target="${link%%#*}"
    [[ -z "$target" ]] && continue
    if [[ ! -e "$target" ]]; then
      echo "FAIL: README 引用的文档不存在: $target" >&2
      missing=1
    fi
  done < <(grep -oE '\]\([^)]+\)' README.md | sed -E 's/^\]\(//; s/\)$//')
  (( missing )) && FAIL=1
}

check_placeholder_markers() {
  local hits
  hits=$(rg -l 'M8 里程碑填充|后续里程碑填充|当前为空占位' \
    --glob '!docs/PROJECT-STATUS-v0.1.md' \
    --glob '!docs/NEXT-STEPS-IMPLEMENTATION-PLAN.md' \
    --glob '!scripts/check-repo-hygiene.sh' . 2>/dev/null || true)
  if [[ -n "$hits" ]]; then
    echo "FAIL: 存在占位标记的文件:" >&2
    echo "$hits" | head -5 >&2
    FAIL=1
  fi
}

check_sensitive_files() {
  local hits
  hits=$(git ls-files | grep -E '\.(pem|key|p12|pfx)$|(^|/)(\.env|secrets?/|.*\.local\.ya?ml)$' || true)
  if [[ -n "$hits" ]]; then
    echo "FAIL: 可能敏感的跟踪文件:" >&2
    echo "$hits" | head -5 >&2
    FAIL=1
  fi
}

main() {
  check_for_tracked_cache
  check_for_large_binaries
  check_required_docs
  check_placeholder_markers
  check_sensitive_files
  if (( FAIL )); then
    echo "仓库卫生检查未通过" >&2
    exit 1
  fi
  echo "仓库卫生检查通过"
}

main "$@"
