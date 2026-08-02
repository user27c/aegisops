#!/usr/bin/env bash
# check-loki-evidence.sh — 验证 Loki 采集链路与证据脱敏前提。
#
# 1. 向 Loki push 唯一 marker 的测试日志(含 password=... 测试文本);
# 2. LogQL 查询验证 marker 被采集(采集链路 OK);
# 3. 说明脱敏边界:证据链路的正则脱敏由 internal/evidence/redactor 单测保证,
#    Loki 本身保留原始日志(由 NetworkPolicy 与访问控制保护)。
#
# 用法:check-loki-evidence.sh [--url http://127.0.0.1:13100] [--marker 唯一值]
set -Eeuo pipefail

URL="http://127.0.0.1:13100"
MARKER="aegisops-evidence-$(date +%s)-$RANDOM"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --url) URL="$2"; shift 2 ;;
    --marker) MARKER="$2"; shift 2 ;;
    *) echo "未知参数: $1" >&2; exit 1 ;;
  esac
done

echo "marker: $MARKER"

# 1. push 测试日志(marker + password 测试文本)
NOW_NS=$(( $(date +%s) * 1000000000 ))
PAYLOAD=$(printf '{"streams":[{"stream":{"app":"faultlab","namespace":"fault-lab","pod":"evidence-check"},"values":[["%s","level=error msg=\\"checkout request failed\\" marker=%s password=test-secret-abc123"]]}]}' "$NOW_NS" "$MARKER")

curl -sf --max-time 5 -X POST -H "Content-Type: application/json" \
  --data "$PAYLOAD" "$URL/loki/api/v1/push" >/dev/null \
  || { echo "FAIL: Loki push 失败" >&2; exit 1; }
echo "OK  push 成功"

# 2. LogQL 查询验证 marker 被采集
for i in $(seq 1 10); do
  QUERY=$(python3 -c "import urllib.parse; print(urllib.parse.quote('{app=\"faultlab\",pod=\"evidence-check\"} |= \"$MARKER\"'))")
  HIT=$(curl -sf --max-time 5 "$URL/loki/api/v1/query_range?query=$QUERY&limit=5" \
    | python3 -c "import json,sys; d=json.load(sys.stdin); print(len(d['data']['result']))" 2>/dev/null || echo 0)
  if [[ "$HIT" -ge 1 ]]; then
    echo "OK  采集链路:marker 可查询(流数=$HIT)"
    break
  fi
  sleep 2
  if [[ "$i" -eq 10 ]]; then
    echo "FAIL: marker 未在 Loki 中查询到" >&2
    exit 1
  fi
done

# 3. 验证 password 测试文本在 Loki 中按预期保留原始(脱敏发生在 evidence 采集链路,
#    由 internal/evidence/redactor 单测锁定;Loki 由 NetworkPolicy 限制访问)
PWD_QUERY=$(python3 -c "import urllib.parse; print(urllib.parse.quote('{app=\"faultlab\",pod=\"evidence-check\"} |= \"$MARKER\" |= \"test-secret-abc123\"'))")
PWD_HIT=$(curl -sf --max-time 5 "$URL/loki/api/v1/query_range?query=$PWD_QUERY&limit=5" \
  | python3 -c "import json,sys; d=json.load(sys.stdin); print(len(d['data']['result']))" 2>/dev/null || echo 0)
if [[ "$PWD_HIT" -ge 1 ]]; then
  echo "INFO Loki 保留原始日志(预期);证据脱敏由 redactor 单测保证"
else
  echo "FAIL: password 测试文本查询异常" >&2
  exit 1
fi

echo "Loki 采集链路验证通过"
