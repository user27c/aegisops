#!/usr/bin/env bash
# send-test-alert.sh — 向 Alertmanager 发送测试告警(仅用于通知链路 smoke;
# 生产告警必须由 PrometheusRule 产生)。真实 SMTP 需 --allow-real-email。
set -Eeuo pipefail

AM_URL="http://127.0.0.1:19093"
SEVERITY="warning"
STATUS="firing"
NAME="AegisOpsTest"
NAMESPACE="fault-lab"
ALLOW_REAL=false

usage() {
  echo "用法: send-test-alert.sh [--alertmanager-url URL] [--severity warning|critical]"
  echo "    [--status firing|resolved] [--name NAME] [--namespace NS] [--allow-real-email]"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --alertmanager-url) AM_URL="$2"; shift 2 ;;
    --severity) SEVERITY="$2"; shift 2 ;;
    --status) STATUS="$2"; shift 2 ;;
    --name) NAME="$2"; shift 2 ;;
    --namespace) NAMESPACE="$2"; shift 2 ;;
    --allow-real-email) ALLOW_REAL=true; shift ;;
    *) usage; exit 1 ;;
  esac
done

[[ "$SEVERITY" == "warning" || "$SEVERITY" == "critical" ]] || { echo "severity 必须为 warning/critical" >&2; exit 1; }
[[ "$STATUS" == "firing" || "$STATUS" == "resolved" ]] || { echo "status 必须为 firing/resolved" >&2; exit 1; }

# 真实 SMTP 保护:非本地 Alertmanager 需要显式确认。
if [[ "$ALLOW_REAL" != "true" ]] && ! echo "$AM_URL" | grep -q "127.0.0.1"; then
  echo "FAIL: 非本地 Alertmanager 需要 --allow-real-email" >&2
  exit 1
fi

NOW_UTC=$(date -u +%Y-%m-%dT%H:%M:%SZ)
if [[ "$STATUS" == "resolved" ]]; then
  STARTS_AT=$(date -u -d '1 minute ago' +%Y-%m-%dT%H:%M:%SZ)
  ENDS_AT="$NOW_UTC"
else
  STARTS_AT="$NOW_UTC"
  ENDS_AT=$(date -u -d '1 hour' +%Y-%m-%dT%H:%M:%SZ)
fi

# v2 API 接收 PostableAlerts 数组。
PAYLOAD=$(printf '[{
  "labels": {
    "alertname": "%s",
    "severity": "%s",
    "cluster": "local-k3s",
    "namespace": "%s",
    "instance": "test-instance-1"
  },
  "annotations": {
    "summary": "AegisOps 测试告警(%s/%s)",
    "description": "由 scripts/send-test-alert.sh 产生的测试通知",
    "runbook_url": "https://github.com/user27c/aegisops/blob/main/docs/operations.md",
    "grafana_url": "http://127.0.0.1:13000/d/aegisops-overview"
  },
  "startsAt": "%s",
  "endsAt": "%s"
}]' "$NAME" "$SEVERITY" "$NAMESPACE" "$SEVERITY" "$STATUS" "$STARTS_AT" "$ENDS_AT")

curl -sf -X POST -H "Content-Type: application/json" --data "$PAYLOAD" "$AM_URL/api/v2/alerts" \
  && echo "已发送 $STATUS/$SEVERITY/$NAME"
