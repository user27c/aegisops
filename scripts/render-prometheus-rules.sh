#!/usr/bin/env bash
# render-prometheus-rules.sh — 从 Helm 渲染结果提取 PrometheusRule 规则,
# 并用 promtool check/test 校验。脚本只读,不修改仓库文件。
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

OUT="${TMPDIR:-/tmp}/aegisops-rules-$$"
mkdir -p "$OUT"
trap 'rm -rf "$OUT"' EXIT

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
' > "$OUT/rule.yaml" 2>/dev/null || { echo "FAIL: 渲染或解析失败" >&2; exit 1; }

if [[ ! -s "$OUT/rule.yaml" ]]; then
  echo "FAIL: 渲染结果为空(检查 alerting.enabled / observability.prometheusRule)" >&2
  exit 1
fi

PROMTOOL="${PROMTOOL:-promtool}"
PROMTOOL_DOCKER=""
if ! command -v "$PROMTOOL" >/dev/null 2>&1; then
  if docker ps --format '{{.Names}}' 2>/dev/null | grep -q '^aegisops-prom$'; then
    # 使用运行中的 prometheus 容器内的 promtool。
    PROMTOOL_DOCKER="docker exec -i aegisops-prom"
    PROMTOOL="promtool"
    docker exec -i aegisops-prom sh -c 'cat > /tmp/rule.yaml' < "$OUT/rule.yaml"
    RULE_IN_CONTAINER=/tmp/rule.yaml
  else
    echo "FAIL: 未找到 promtool 且无 aegisops-prom 容器" >&2
    exit 1
  fi
fi

echo "==> promtool check rules"
if [[ -n "$PROMTOOL_DOCKER" ]]; then
  $PROMTOOL_DOCKER promtool check rules "$RULE_IN_CONTAINER" || exit 1
else
  "$PROMTOOL" check rules "$OUT/rule.yaml" || exit 1
fi

TEST_FILE="$ROOT/deploy/observability/tests/aegisops.rules.test.yml"
if [[ -f "$TEST_FILE" ]]; then
  echo "==> promtool test rules"
  if [[ -n "$PROMTOOL_DOCKER" ]]; then
    docker cp "$TEST_FILE" aegisops-prom:/tmp/aegisops.rules.test.yml
    docker exec aegisops-prom sh -c 'cd /tmp && promtool test rules aegisops.rules.test.yml' || exit 1
  else
    "$PROMTOOL" test rules "$TEST_FILE" || exit 1
  fi
fi
echo "规则校验通过"
