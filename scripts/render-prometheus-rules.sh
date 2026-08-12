#!/usr/bin/env bash
# render-prometheus-rules.sh — 从 Helm 渲染结果提取 PrometheusRule 规则，
# 可选用本机 promtool 校验。默认只写临时文件，不接触任何运行中容器。
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

OUTPUT=""
SKIP_PROMTOOL=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --output)
      [[ $# -ge 2 && -n "$2" ]] || { echo "FAIL: --output 需要路径" >&2; exit 1; }
      OUTPUT="$2"
      shift 2
      ;;
    --skip-promtool) SKIP_PROMTOOL=true; shift ;;
    *) echo "FAIL: 未知参数: $1" >&2; exit 1 ;;
  esac
done

OUT="${TMPDIR:-/tmp}/aegisops-rules-$$"
if [[ -n "$OUTPUT" ]]; then
  RULE_FILE="$OUTPUT"
  mkdir -p "$(dirname "$RULE_FILE")"
else
  mkdir -p "$OUT"
  RULE_FILE="$OUT/rule.yaml"
  trap 'rm -rf "$OUT"' EXIT
fi

helm template aegisops deploy/helm/aegisops \
  --set global.imageRegistry=example.invalid \
  --set observability.prometheusRule=true \
  --set alerting.enabled=true \
  --set alerting.smtp.smarthost=mailhog:1025 \
  --set alerting.smtp.from=aegisops@example.invalid \
  --set alerting.email.to[0]=receiver@example.invalid \
  --set alerting.smtp.auth.username=test \
  --set alerting.smtp.auth.passwordSecret.name=aegisops-smtp \
  --set alerting.smtp.auth.passwordSecret.key=password 2>/dev/null \
  | python3 -c '
import sys, yaml
for doc in yaml.safe_load_all(sys.stdin):
    if doc and doc.get("kind") == "PrometheusRule":
        # promtool 需要裸 rulefmt 格式(groups 顶层)。
        print(yaml.safe_dump({"groups": doc["spec"]["groups"]}, sort_keys=False))
' > "$RULE_FILE" 2>/dev/null || { echo "FAIL: 渲染或解析失败" >&2; exit 1; }

if [[ ! -s "$RULE_FILE" ]]; then
  echo "FAIL: 渲染结果为空(检查 alerting.enabled / observability.prometheusRule)" >&2
  exit 1
fi

if [[ "$SKIP_PROMTOOL" == "true" ]]; then
  echo "规则已渲染: $RULE_FILE"
  exit 0
fi

PROMTOOL="${PROMTOOL:-promtool}"
if ! command -v "$PROMTOOL" >/dev/null 2>&1; then
  echo "FAIL: 未找到本机 promtool（CI 使用临时 prom/prometheus 容器）" >&2
  exit 1
fi

echo "==> promtool check rules"
"$PROMTOOL" check rules "$RULE_FILE" || exit 1

TEST_FILE="$ROOT/deploy/observability/tests/aegisops.rules.test.yml"
if [[ -f "$TEST_FILE" ]]; then
  echo "==> promtool test rules"
  # promtool test rules 的 rule_files 相对测试文件所在目录解析,而 rule.yaml
  # 渲染在 RULE_FILE(可能位于临时目录)。把测试文件复制到 rule.yaml 同目录再执行。
  TEST_COPY="$(dirname "$RULE_FILE")/aegisops.rules.test.yml"
  cp "$TEST_FILE" "$TEST_COPY"
  "$PROMTOOL" test rules "$TEST_COPY" || exit 1
fi
echo "规则校验通过"
