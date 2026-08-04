#!/usr/bin/env bash
# check-prometheus-targets.sh — 校验 Prometheus 抓取目标健康。
# 用法:check-prometheus-targets.sh --url http://localhost:19090 \
#       --expected-job aegisops-operator [--expected-job ...] [--timeout 300]
set -Eeuo pipefail

URL="http://127.0.0.1:19090"
TIMEOUT=300
EXPECTED=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --url) URL="$2"; shift 2 ;;
    --expected-job) EXPECTED+=("$2"); shift 2 ;;
    --timeout) TIMEOUT="$2"; shift 2 ;;
    *) echo "未知参数: $1" >&2; exit 1 ;;
  esac
done

[[ ${#EXPECTED[@]} -gt 0 ]] || { echo "FAIL: 至少需要一个 --expected-job" >&2; exit 1; }

declare -A UP=()
deadline=$(( $(date +%s) + TIMEOUT ))
while (( $(date +%s) < deadline )); do
  data=$(curl -sf --max-time 5 "$URL/api/v1/targets?state=active" 2>/dev/null || echo "")
  if [[ -n "$data" ]]; then
    # 输出: job health lastError(逐行)
    echo "$data" | python3 -c '
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(0)
for t in d.get("data", {}).get("activeTargets", []):
    job = t.get("labels", {}).get("job", "")
    health = t.get("health", "")
    err = t.get("lastError", "")
    print(f"{job}\t{health}\t{err}")
' > /tmp/prom-targets.txt
    missing=0
    for job in "${EXPECTED[@]}"; do
      line=$(awk -F'\t' -v j="$job" '$1 ~ j {print}' /tmp/prom-targets.txt || true)
      if [[ -z "$line" ]]; then
        echo "WAIT: $job 尚未出现在 target 列表" >&2
        missing=1
      elif ! echo "$line" | grep -q $'\tup\t'; then
        echo "DOWN: $line" >&2
        missing=1
      fi
    done
    if (( ! missing )); then
      echo "全部 ${#EXPECTED[@]} 个 target up:"
      for job in "${EXPECTED[@]}"; do
        awk -F'\t' -v j="$job" '$1 ~ j {print}' /tmp/prom-targets.txt
      done
      exit 0
    fi
  fi
  sleep 5
done

echo "FAIL: ${TIMEOUT}s 内未能全部 up(最后状态):" >&2
cat /tmp/prom-targets.txt >&2 2>/dev/null || true
exit 1
